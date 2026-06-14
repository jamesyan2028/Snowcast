package runtime

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"snowcast-jamesyan2028/internal/control"
	"snowcast-jamesyan2028/internal/state"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"google.golang.org/grpc"
)

// Server runs client-facing gRPC control and UDP streaming.
type Server struct {
	grpc    *grpc.Server
	udpConn *net.UDPConn
	mu      sync.Mutex
	stopped bool
}

// Serve starts UDP streaming and gRPC control on port.
func Serve(port string, files []string, repl control.Replicator) (*Server, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:"+port)
	if err != nil {
		return nil, fmt.Errorf("udp addr: %w", err)
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("udp listen: %w", err)
	}

	for i, path := range files {
		go streamStation(i, path, udpConn)
	}

	listen, err := net.Listen("tcp", ":"+port)
	if err != nil {
		udpConn.Close()
		return nil, fmt.Errorf("tcp listen: %w", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterSnowcastControlServer(grpcServer, &control.Server{
		NumStations: len(files),
		Repl:        repl,
	})

	s := &Server{grpc: grpcServer, udpConn: udpConn}

	go func() {
		if err := grpcServer.Serve(listen); err != nil {
			log.Printf("gRPC serve ended: %v", err)
		}
	}()

	fmt.Printf("Snowcast server started on port %s with %d stations\n", port, len(files))
	return s, nil
}

// Stop gracefully stops client serving.
func (s *Server) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	s.grpc.GracefulStop()
	if s.udpConn != nil {
		s.udpConn.Close()
	}
}

// GRPC returns the underlying gRPC server (for CLI shutdown).
func (s *Server) GRPC() *grpc.Server {
	return s.grpc
}

func streamStation(id int, filename string, udpConn *net.UDPConn) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Error opening file: %s\n", filename)
		return
	}
	defer file.Close()
	buffer := make([]byte, 1500)
	ticker := time.NewTicker(91550 * time.Microsecond)
	defer ticker.Stop()
	for range ticker.C {
		n, err := file.Read(buffer)
		if n > 0 {
			currStation := &state.StationList[id]
			currStation.Mutex.Lock()
			for _, client := range currStation.Clients {
				udpConn.WriteToUDP(buffer[:n], client.UDPAddr)
			}
			currStation.Mutex.Unlock()
		}

		if err != nil {
			if err == io.EOF {
				file.Seek(0, 0)
				event := &pb.ServerEvent{
					Event: &pb.ServerEvent_Announce{
						Announce: &pb.AnnounceMessage{
							SongName: filename,
						},
					},
				}
				station := &state.StationList[id]
				station.Mutex.Lock()
				for _, client := range station.Clients {
					select {
					case client.EventChan <- event:
					default:
					}
				}
				station.Mutex.Unlock()
			} else {
				fmt.Printf("Error reading file: %s\n", err)
			}
		}
	}
}

// HandleUserInput runs the server CLI until quit.
func HandleUserInput(s *Server) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		words := strings.Fields(input)
		if len(words) == 0 {
			continue
		}
		switch words[0] {
		case "p":
			if len(words) == 1 {
				fmt.Print(FormatStationString())
			} else if len(words) == 2 {
				if err := os.WriteFile(words[1], []byte(FormatStationString()), 0644); err != nil {
					fmt.Printf("Error writing output to file: %s\n", err)
				}
			} else {
				fmt.Printf("Invalid Command Type\n")
			}
		case "q":
			state.ClientMutex.Lock()
			for key, client := range state.CurrentClients {
				close(client.EventChan)
				delete(state.CurrentClients, key)
			}
			state.ClientMutex.Unlock()
			s.Stop()
			os.Exit(0)
		default:
			fmt.Printf("Invalid command\n")
		}
	}
	select {}
}

// FormatStationString returns station/client status for the p command.
func FormatStationString() string {
	var builder strings.Builder
	state.ClientMutex.Lock()
	defer state.ClientMutex.Unlock()

	for i := range state.StationList {
		station := &state.StationList[i]
		station.Mutex.Lock()
		builder.WriteString(fmt.Sprintf("%d,%s", i, station.Name))
		for _, client := range station.Clients {
			builder.WriteString(fmt.Sprintf(",%s:%d", client.UDPAddr.IP.String(), client.UDPAddr.Port))
		}
		builder.WriteString("\n")
		station.Mutex.Unlock()
	}
	return builder.String()
}
