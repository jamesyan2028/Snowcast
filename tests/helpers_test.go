package tests

import (
	"context"
	"testing"
	"time"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func requireHandshake(t *testing.T, udpPort uint32) *pb.WelcomeMessage {
	t.Helper()
	resp, err := client.Handshake(context.Background(), &pb.HelloMessage{UdpPort: udpPort})
	require.NoError(t, err)
	return resp
}

func requireSetStation(t *testing.T, station, udpPort uint32) *pb.ServerEvent {
	t.Helper()
	stream, err := client.SetStation(context.Background(), &pb.SetStationMessage{
		StationNumber: station,
		UdpPort:       udpPort,
	})
	require.NoError(t, err)

	event, err := stream.Recv()
	require.NoError(t, err)
	return event
}

func backupReplClient(t *testing.T) (pb.SnowcastReplicationClient, *grpc.ClientConn) {
	t.Helper()
	conn, err := grpc.NewClient("127.0.0.1:16801", grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	return pb.NewSnowcastReplicationClient(conn), conn
}

func backupLastWalSeq(t *testing.T) uint64 {
	t.Helper()
	repl, conn := backupReplClient(t)
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := repl.Ping(ctx, &pb.PingRequest{})
	require.NoError(t, err)
	return resp.LastWalSeq
}
