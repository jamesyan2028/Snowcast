package tests

import (
	"context"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func backupReplClient(t *testing.T) (pb.SnowcastReplicationClient, *grpc.ClientConn) {
	t.Helper()
	conn, err := grpc.NewClient("127.0.0.1:16801", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("connect to backup replication: %v", err)
	}
	return pb.NewSnowcastReplicationClient(conn), conn
}

func backupLastWalSeq(t *testing.T) uint64 {
	t.Helper()
	repl, conn := backupReplClient(t)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := repl.Ping(ctx, &pb.PingRequest{})
	if err != nil {
		t.Fatalf("backup Ping failed: %v", err)
	}
	return resp.LastWalSeq
}

func TestBackupReplicationAfterHandshake(t *testing.T) {
	before := backupLastWalSeq(t)

	_, err := client.Handshake(context.Background(), &pb.HelloMessage{UdpPort: 5100})
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	after := backupLastWalSeq(t)
	if after < before+1 {
		t.Fatalf("expected backup last_wal_seq >= %d, got %d", before+1, after)
	}
}

func TestBackupReplicationAfterSetStation(t *testing.T) {
	before := backupLastWalSeq(t)

	_, err := client.Handshake(context.Background(), &pb.HelloMessage{UdpPort: 5101})
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	stream, err := client.SetStation(context.Background(), &pb.SetStationMessage{
		StationNumber: 0,
		UdpPort:       5101,
	})
	if err != nil {
		t.Fatalf("SetStation failed: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv failed: %v", err)
	}

	after := backupLastWalSeq(t)
	if after < before+2 {
		t.Fatalf("expected backup last_wal_seq >= %d, got %d", before+2, after)
	}
}

func TestBackupFailoverPromotion(t *testing.T) {
	pidStr := os.Getenv("PRIMARY_PID")
	if pidStr == "" {
		t.Skip("PRIMARY_PID not set; run via scripts/run-backup-tests.sh")
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		t.Fatalf("invalid PRIMARY_PID: %v", err)
	}

	_, err = client.Handshake(context.Background(), &pb.HelloMessage{UdpPort: 5102})
	if err != nil {
		t.Fatalf("pre-failover Handshake failed: %v", err)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill primary: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	var promoted pb.SnowcastControlClient
	for time.Now().Before(deadline) {
		conn, err := grpc.NewClient("127.0.0.1:16800", grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := pb.NewSnowcastControlClient(conn).Health(ctx, &pb.HealthRequest{})
		cancel()
		if err == nil && resp != nil && resp.Ok {
			promoted = pb.NewSnowcastControlClient(conn)
			break
		}
		conn.Close()
		time.Sleep(200 * time.Millisecond)
	}
	if promoted == nil {
		t.Fatal("backup did not promote within timeout")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := promoted.Handshake(ctx, &pb.HelloMessage{UdpPort: 5010})
	if err != nil {
		t.Fatalf("post-failover Handshake failed: %v", err)
	}
	if resp.NumStations == 0 {
		t.Error("expected at least one station after promotion")
	}
}
