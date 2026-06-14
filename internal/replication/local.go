package replication

import (
	"context"
	"fmt"

	"snowcast-jamesyan2028/internal/state"
	"snowcast-jamesyan2028/pkg/wal"

	pb "snowcast-jamesyan2028/pkg/protocol"
)

//utils for replicating to local WAL

// LocalCoordinator persists to WAL and applies locally (promoted backup, no remote peer).
type LocalCoordinator struct {
	wal *wal.Log
}

// StartLocal opens a WAL for promoted-primary mode.
func StartLocal(walPath string) (*LocalCoordinator, error) {
	w, err := wal.Open(walPath)
	if err != nil {
		return nil, err
	}
	return &LocalCoordinator{wal: w}, nil
}

// NewLocalFromLog uses an already-open WAL (promotion path).
func NewLocalFromLog(w *wal.Log) *LocalCoordinator {
	return &LocalCoordinator{wal: w}
}

// replays WAL entries into memory.
func (c *LocalCoordinator) Recover() error {
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

// appends to WAL and applies locally.
func (c *LocalCoordinator) ReplicateAndWait(ctx context.Context, entry *pb.WalEntry, apply func() error, rollback func()) error {
	_, err := c.wal.Append(entry)
	if err != nil {
		return fmt.Errorf("wal append: %w", err)
	}
	if err := apply(); err != nil {
		rollback()
		return err
	}
	return nil
}

// closes the WAL.
func (c *LocalCoordinator) Close() error {
	if c.wal != nil {
		return c.wal.Close()
	}
	return nil
}
