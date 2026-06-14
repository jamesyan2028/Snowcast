package tests

import (
	"path/filepath"
	"testing"

	pb "snowcast-jamesyan2028/pkg/protocol"
	"snowcast-jamesyan2028/pkg/wal"

	"github.com/stretchr/testify/require"
)

func TestWalAppendAndReadAll(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.wal")
	log, err := wal.Open(path)
	require.NoError(t, err)
	defer log.Close()

	entry := &pb.WalEntry{
		Op: &pb.WalEntry_Handshake{
			Handshake: &pb.HandshakeOp{ClientIp: "127.0.0.1", UdpPort: 42},
		},
	}
	seq, err := log.Append(entry)
	require.NoError(t, err)
	require.Equal(t, uint64(1), seq)

	entries, err := log.ReadAll()
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, uint64(1), entries[0].Seq)

	log2, err := wal.Open(path)
	require.NoError(t, err)
	defer log2.Close()
	require.Equal(t, uint64(1), log2.LastSeq())
}

func TestWalAppendIncrementsSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "increment.wal")

	log, err := wal.Open(path)
	require.NoError(t, err)
	defer log.Close()

	for i := uint64(1); i <= 3; i++ {
		seq, err := log.Append(&pb.WalEntry{
			Op: &pb.WalEntry_Disconnect{
				Disconnect: &pb.DisconnectOp{ClientIp: "127.0.0.1", UdpPort: uint32(i)},
			},
		})
		require.NoError(t, err)
		require.Equal(t, i, seq)
	}
	require.Equal(t, uint64(3), log.LastSeq())
}
