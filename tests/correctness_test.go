package tests

import (
	"context"
	"os"
	"testing"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	client pb.SnowcastControlClient
	conn   *grpc.ClientConn
)

func TestMain(m *testing.M) {
	var err error
	conn, err = grpc.NewClient("127.0.0.1:16800", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		panic("Failed to connect to server: " + err.Error())
	}
	client = pb.NewSnowcastControlClient(conn)

	code := m.Run()

	conn.Close()
	os.Exit(code)
}

func TestHandshake(t *testing.T) {
	resp := requireHandshake(t, 5000)
	require.NotZero(t, resp.NumStations)
}

func TestSetStationValid(t *testing.T) {
	requireHandshake(t, 5001)

	event := requireSetStation(t, 0, 5001)
	announce, ok := event.Event.(*pb.ServerEvent_Announce)
	require.True(t, ok)
	require.NotEmpty(t, announce.Announce.SongName)
}

func TestSetStationInvalid(t *testing.T) {
	requireHandshake(t, 5002)

	event := requireSetStation(t, 99, 5002)
	_, ok := event.Event.(*pb.ServerEvent_Invalid)
	require.True(t, ok)
}

func TestDisconnect(t *testing.T) {
	requireHandshake(t, 5003)

	resp, err := client.Disconnect(context.Background(), &pb.DisconnectRequest{UdpPort: 5003})
	require.NoError(t, err)
	require.True(t, resp.Success)
}
