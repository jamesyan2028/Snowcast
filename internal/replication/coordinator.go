package replication

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"snowcast-jamesyan2028/internal/state"
	"snowcast-jamesyan2028/pkg/wal"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	replicationTimeout = 5 * time.Second
	backupReadyTimeout = 30 * time.Second
)

// Coordinator replicates WAL entries to a remote backup before client ack.
type Coordinator struct {
	wal        *wal.Log
	client     pb.SnowcastReplicationClient
	conn       *grpc.ClientConn
	lastWalSeq uint64
	mu         sync.Mutex
}

var global *Coordinator

// Global returns the active coordinator.
func Global() *Coordinator {
	return global
}

// DefaultWalPath returns the default primary WAL path for a port.
func DefaultWalPath(port string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("snowcast-%s.wal", port))
}

// DefaultBackupWalPath returns the default backup WAL path.
func DefaultBackupWalPath(replPort string) string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("snowcast-backup-%s.wal", replPort))
}

// Start connects to backup, recovers primary state, and syncs backup WAL.
func Start(walPath, backupAddr string) error {
	w, err := wal.Open(walPath)
	if err != nil {
		return fmt.Errorf("open wal: %w", err)
	}
	return StartWithWal(w, backupAddr)
}

// StartWithWal uses an already-open WAL as leader outbound replication coordinator.
func StartWithWal(w *wal.Log, backupAddr string) error {
	return startWithWal(w, backupAddr, backupReadyTimeout)
}

// StartWithWalTimeout is like StartWithWal with a custom readiness timeout.
func StartWithWalTimeout(w *wal.Log, backupAddr string, readyTimeout time.Duration) error {
	return startWithWal(w, backupAddr, readyTimeout)
}

func startWithWal(w *wal.Log, backupAddr string, readyTimeout time.Duration) error {
	conn, err := dialBackup(backupAddr, readyTimeout)
	if err != nil {
		return err
	}

	global = &Coordinator{
		wal:    w,
		client: pb.NewSnowcastReplicationClient(conn),
		conn:   conn,
	}

	if err := global.recoverPrimary(); err != nil {
		global.ShutdownClient()
		return fmt.Errorf("wal recover primary: %w", err)
	}
	if err := global.syncBackupWal(); err != nil {
		global.ShutdownClient()
		return fmt.Errorf("sync backup wal: %w", err)
	}

	log.Printf("Backup ready at %s", backupAddr)
	return nil
}

// WAL returns the coordinator WAL handle.
func (c *Coordinator) WAL() *wal.Log {
	if c == nil {
		return nil
	}
	return c.wal
}

func dialBackup(backupAddr string, readyTimeout time.Duration) (*grpc.ClientConn, error) {
	deadline := time.Now().Add(readyTimeout)
	for time.Now().Before(deadline) {
		conn, err := grpc.NewClient(backupAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil { //error creating gRPC client for some reason
			time.Sleep(100 * time.Millisecond)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second) //create new go context, blocking functions in go take a context as first argument by default. Lets me cancel the request after 1s
		_, err = pb.NewSnowcastReplicationClient(conn).Ping(ctx, &pb.PingRequest{})
		cancel() //good practice to manually cancel to clean up the timer
		if err == nil {
			return conn, nil
		}
		conn.Close()
		time.Sleep(100 * time.Millisecond)
	}
	return nil, fmt.Errorf("backup not ready at %s", backupAddr)
}

//Restore in memory state from primary WAL
func (c *Coordinator) recoverPrimary() error {
	entries, err := c.wal.ReadAll()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := state.ApplyWalEntry(entry); err != nil {
			return fmt.Errorf("recover seq %d: %w", entry.Seq, err)
		}
	}
	return nil
}

//return the last WAL sequence number from the backup
func (c *Coordinator) backupLastWalSeq(ctx context.Context) (uint64, error) {
	resp, err := c.client.Ping(ctx, &pb.PingRequest{})
	if err != nil {
		return 0, err
	}
	return resp.LastWalSeq, nil
}

//sync backup WAL to primary WAL
func (c *Coordinator) syncBackupWal() error {
	ctx := context.Background()
	lastSeq, err := c.backupLastWalSeq(ctx)
	if err != nil {
		return err
	}

	entries, err := c.wal.ReadAll()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Seq <= lastSeq {
			continue
		}
		if err := c.replicate(ctx, entry); err != nil {
			return err
		}
		if entry.Seq > c.lastWalSeq {
			c.lastWalSeq = entry.Seq
		}
	}
	return nil
}

// ReplicateAndWait appends to primary WAL, applies state, replicates to backup WAL, then acks.
func (c *Coordinator) ReplicateAndWait(ctx context.Context, entry *pb.WalEntry, apply func() error, rollback func()) error {
	//update WAL
	seq, err := c.wal.Append(entry)
	if err != nil {
		return fmt.Errorf("wal append: %w", err)
	}
	entry.Seq = seq

	//update in memory state
	if err := apply(); err != nil {
		return err
	}

	//sync to backup WAL
	if err := c.replicateWithRetry(ctx, entry); err != nil {
		rollback()
		return fmt.Errorf("replication failed: %w", err)
	}

	c.mu.Lock()
	if seq > c.lastWalSeq {
		c.lastWalSeq = seq
	}
	c.mu.Unlock()
	return nil
}

//replicate with a single retry
func (c *Coordinator) replicateWithRetry(ctx context.Context, entry *pb.WalEntry) error {
	if err := c.replicate(ctx, entry); err == nil {
		return nil
	}

	//something went wrong with replication, so sync states and retry
	if syncErr := c.syncBackupWal(); syncErr != nil {
		return syncErr
	}
	return c.replicate(ctx, entry)
}

func (c *Coordinator) replicate(ctx context.Context, entry *pb.WalEntry) error {
	ctx, cancel := context.WithTimeout(ctx, replicationTimeout)
	defer cancel()

	resp, err := c.client.Replicate(ctx, &pb.ReplicateRequest{Entry: entry})
	if err != nil {
		return err
	}
	if !resp.Ok {
		if resp.Error != "" {
			return fmt.Errorf("%s", resp.Error)
		}
		return fmt.Errorf("backup rejected seq %d", entry.Seq)
	}
	return nil
}

// ShutdownClient closes the outbound replication client but keeps the WAL open.
func (c *Coordinator) ShutdownClient() {
	if c == nil {
		return
	}
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	if global == c {
		global = nil
	}
}

// Shutdown closes backup connection and WAL.
func (c *Coordinator) Shutdown() {
	if c == nil {
		return
	}
	c.ShutdownClient()
	if c.wal != nil {
		c.wal.Close()
		c.wal = nil
	}
}

// ShutdownGlobal shuts down the active coordinator.
func ShutdownGlobal() {
	if global != nil {
		global.ShutdownClient()
	}
}
