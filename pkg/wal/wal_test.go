package wal

import (
	"os"
	"path/filepath"
	"testing"

	pb "snowcast-jamesyan2028/pkg/protocol"
)

func TestAppendAndReadAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	entry := &pb.WalEntry{
		Op: &pb.WalEntry_Handshake{
			Handshake: &pb.HandshakeOp{ClientIp: "127.0.0.1", UdpPort: 42},
		},
	}
	seq, err := log.Append(entry)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("expected seq 1, got %d", seq)
	}

	entries, err := log.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Seq != 1 {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	log2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log2.Close()
	if log2.LastSeq() != 1 {
		t.Fatalf("expected LastSeq 1, got %d", log2.LastSeq())
	}
}

func TestAppendIncrementsSeq(t *testing.T) {
	path := filepath.Join(os.TempDir(), "snowcast-wal-test-increment.wal")
	defer os.Remove(path)

	log, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	for i := uint64(1); i <= 3; i++ {
		seq, err := log.Append(&pb.WalEntry{
			Op: &pb.WalEntry_Disconnect{
				Disconnect: &pb.DisconnectOp{ClientIp: "127.0.0.1", UdpPort: uint32(i)},
			},
		})
		if err != nil || seq != i {
			t.Fatalf("append %d: seq=%d err=%v", i, seq, err)
		}
	}
	if log.LastSeq() != 3 {
		t.Fatalf("LastSeq=%d", log.LastSeq())
	}
}
