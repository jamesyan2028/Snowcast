package tests

import (
	"context"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestBackupReplicationAfterHandshake(t *testing.T) {
	before := backupLastWalSeq(t)

	requireHandshake(t, 5100)

	after := backupLastWalSeq(t)
	require.GreaterOrEqual(t, after, before+1)
}

func TestBackupReplicationAfterSetStation(t *testing.T) {
	before := backupLastWalSeq(t)

	requireHandshake(t, 5101)
	requireSetStation(t, 0, 5101)

	after := backupLastWalSeq(t)
	require.GreaterOrEqual(t, after, before+2)
}

func TestBackupFailoverPromotion(t *testing.T) {
	pidStr := os.Getenv("PRIMARY_PID")
	if pidStr == "" {
		t.Skip("PRIMARY_PID not set; run via scripts/run-backup-tests.sh")
	}
	pid, err := strconv.Atoi(pidStr)
	require.NoError(t, err)

	requireHandshake(t, 5102)

	proc, err := os.FindProcess(pid)
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGKILL))

	require.Eventually(t, func() bool {
		conn, err := grpc.NewClient("127.0.0.1:16800", grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return false
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		resp, err := pb.NewSnowcastControlClient(conn).Health(ctx, &pb.HealthRequest{})
		return err == nil && resp != nil && resp.Ok
	}, 15*time.Second, 200*time.Millisecond, "backup did not promote within timeout")

	conn, err := grpc.NewClient("127.0.0.1:16800", grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	promoted := pb.NewSnowcastControlClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := promoted.Handshake(ctx, &pb.HelloMessage{UdpPort: 5010})
	require.NoError(t, err)
	require.NotZero(t, resp.NumStations)
}
