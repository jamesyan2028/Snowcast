package control

import (
	"context"
	"fmt"

	"snowcast-jamesyan2028/internal/state"

	pb "snowcast-jamesyan2028/pkg/protocol"
)

// Interface for things that WAL + apply + optional remote replication before client ack.
type Replicator interface {
	ReplicateAndWait(ctx context.Context, entry *pb.WalEntry, apply func() error, rollback func()) error
}

// Server implements SnowcastControl.
type Server struct {
	pb.UnimplementedSnowcastControlServer
	NumStations int
	Repl        Replicator
}

func (s *Server) Handshake(ctx context.Context, req *pb.HelloMessage) (*pb.WelcomeMessage, error) {
	udpPort := req.UdpPort
	fmt.Printf("Client handshake received, client listening on UDP port %d\n", udpPort)

	clientIP := getClientIP(ctx)
	clientKey := state.ClientKey(clientIP, udpPort)
	if _, found := state.GetClient(clientKey); found {
		return nil, fmt.Errorf("client already connected: %s", clientKey)
	}

	entry := &pb.WalEntry{
		Op: &pb.WalEntry_Handshake{
			Handshake: &pb.HandshakeOp{
				ClientIp: clientIP,
				UdpPort:  udpPort,
			},
		},
	}

	err := s.Repl.ReplicateAndWait(ctx, entry,
		func() error {
			_, err := state.ApplyHandshake(clientIP, udpPort)
			return err
		},
		func() {
			state.RollbackHandshake(clientIP, udpPort)
		},
	)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Client successfully connected: %s\n", clientKey)
	return &pb.WelcomeMessage{NumStations: uint32(s.NumStations)}, nil
}

func (s *Server) SetStation(req *pb.SetStationMessage, stream pb.SnowcastControl_SetStationServer) error {
	stationNum := int(req.StationNumber)

	if stationNum < 0 || stationNum >= len(state.StationList) {
		stream.Send(&pb.ServerEvent{
			Event: &pb.ServerEvent_Invalid{
				Invalid: &pb.InvalidCommandMessage{
					ReplyString: "Invalid Station Number",
				},
			},
		})
		return fmt.Errorf("invalid station number: %d", stationNum)
	}

	clientIP := getClientIP(stream.Context())
	clientKey := state.ClientKey(clientIP, req.UdpPort)

	clientInfo, found := state.GetClient(clientKey)
	if !found {
		return fmt.Errorf("client not found, %s", clientKey)
	}

	previousStation := clientInfo.CurrStation
	entry := &pb.WalEntry{
		Op: &pb.WalEntry_SetStation{
			SetStation: &pb.SetStationOp{
				ClientIp:        clientIP,
				UdpPort:         req.UdpPort,
				StationNumber:   int32(stationNum),
				PreviousStation: int32(previousStation),
			},
		},
	}

	var songName string
	err := s.Repl.ReplicateAndWait(stream.Context(), entry,
		func() error {
			var err error
			songName, err = state.ApplySetStation(clientKey, stationNum)
			return err
		},
		func() {
			state.RollbackSetStation(clientKey, previousStation)
		},
	)
	if err != nil {
		return err
	}

	if err := stream.Send(&pb.ServerEvent{
		Event: &pb.ServerEvent_Announce{
			Announce: &pb.AnnounceMessage{
				SongName: songName,
			},
		},
	}); err != nil {
		return err
	}


	//Send server events to client, particularly when songs loop back.
	for {
		select {
		case event, ok := <-clientInfo.EventChan:
			if !ok {
				return nil
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
}

func (s *Server) Disconnect(ctx context.Context, req *pb.DisconnectRequest) (*pb.DisconnectResponse, error) {
	clientIP := getClientIP(ctx)
	clientKey := state.ClientKey(clientIP, req.UdpPort)

	clientInfo, found := state.GetClient(clientKey)
	if !found {
		return &pb.DisconnectResponse{Success: false}, nil
	}

	savedClient := &state.ClientInfo{
		UDPAddr:     clientInfo.UDPAddr,
		CurrStation: clientInfo.CurrStation,
		EventChan:   make(chan *pb.ServerEvent, 100),
	}

	entry := &pb.WalEntry{
		Op: &pb.WalEntry_Disconnect{
			Disconnect: &pb.DisconnectOp{
				ClientIp: clientIP,
				UdpPort:  req.UdpPort,
			},
		},
	}

	err := s.Repl.ReplicateAndWait(ctx, entry,
		func() error {
			return state.ApplyDisconnect(clientKey)
		},
		func() {
			state.RollbackDisconnect(clientKey, savedClient)
		},
	)
	if err != nil {
		return &pb.DisconnectResponse{Success: false}, err
	}

	fmt.Printf("Client disconnected: %s\n", clientKey)
	return &pb.DisconnectResponse{Success: true}, nil
}

func (s *Server) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Ok: true}, nil
}

func getClientIP(ctx context.Context) string {
	return "127.0.0.1"
}
