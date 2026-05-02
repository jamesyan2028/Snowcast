package tests

import (
	"context"
	"os"
	"testing"
	pb "snowcast-jamesyan2028/pkg/protocol"
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
	resp, err := client.Handshake(context.Background(), &pb.HelloMessage{UdpPort: 5000})
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}
	if resp.NumStations == 0 {
		t.Error("Expected at least one station")
	}
}

func TestSetStationValid(t *testing.T) {
	_, err := client.Handshake(context.Background(), &pb.HelloMessage{UdpPort: 5001})
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	stream, err := client.SetStation(context.Background(), &pb.SetStationMessage{
		StationNumber: 0,
		UdpPort:       5001,
	})
	if err != nil {
		t.Fatalf("SetStation failed: %v", err)
	}

	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv failed: %v", err)
	}

	announce, ok := event.Event.(*pb.ServerEvent_Announce)
	if !ok {
		t.Fatal("Expected announce event")
	}
	if announce.Announce.SongName == "" {
		t.Error("Expected non-empty song name")
	}
}

func TestSetStationInvalid(t *testing.T) {
	_, err := client.Handshake(context.Background(), &pb.HelloMessage{UdpPort: 5002})
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	stream, err := client.SetStation(context.Background(), &pb.SetStationMessage{
		StationNumber: 99,
		UdpPort:       5002,
	})
	if err != nil {
		t.Fatalf("SetStation failed: %v", err)
	}

	event, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv failed: %v", err)
	}

	_, ok := event.Event.(*pb.ServerEvent_Invalid)
	if !ok {
		t.Fatal("Expected invalid command event")
	}
}

func TestDisconnect(t *testing.T) {
	_, err := client.Handshake(context.Background(), &pb.HelloMessage{UdpPort: 5003})
	if err != nil {
		t.Fatalf("Handshake failed: %v", err)
	}

	resp, err := client.Disconnect(context.Background(), &pb.DisconnectRequest{UdpPort: 5003})
	if err != nil {
		t.Fatalf("Disconnect failed: %v", err)
	}
	if !resp.Success {
		t.Error("Expected successful disconnect")
	}
}