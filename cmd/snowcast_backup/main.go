package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"snowcast-jamesyan2028/internal/replication"
	"snowcast-jamesyan2028/internal/runtime"
	"snowcast-jamesyan2028/internal/state"
	"snowcast-jamesyan2028/pkg/wal"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type backupConfig struct {
	replPort          string
	primaryAddr       string
	clientPort        string
	walPath           string
	heartbeatInterval time.Duration
	heartbeatTimeout  time.Duration
	stationFiles      []string
}

type replicationServer struct {
	pb.UnimplementedSnowcastReplicationServer
	wal        *wal.Log
	mu         sync.Mutex
	lastWalSeq uint64
	promoted   bool
}

func main() {
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	w, err := wal.Open(cfg.walPath)
	if err != nil {
		log.Fatalf("open wal: %v", err)
	}
	defer w.Close()

	srv := &replicationServer{wal: w, lastWalSeq: w.LastSeq()}

	lis, err := net.Listen("tcp", ":"+cfg.replPort)
	if err != nil {
		log.Fatalf("listen replication: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterSnowcastReplicationServer(grpcServer, srv)

	go func() {
		log.Printf("Snowcast backup standby on replication port %s (WAL: %s)", cfg.replPort, cfg.walPath)
		if err := grpcServer.Serve(lis); err != nil {
			log.Fatalf("replication serve: %v", err)
		}
	}()

	runHeartbeat(cfg, srv, grpcServer, w)
}

func parseConfig(args []string) (backupConfig, error) {
	cfg := backupConfig{
		heartbeatInterval: 2 * time.Second,
		heartbeatTimeout:  10 * time.Second,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--repl-port":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("missing --repl-port value")
			}
			cfg.replPort = args[i]
		case "--primary-addr":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("missing --primary-addr value")
			}
			cfg.primaryAddr = args[i]
		case "--wal":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("missing --wal value")
			}
			cfg.walPath = args[i]
		case "--client-port":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("missing --client-port value")
			}
			cfg.clientPort = args[i]
		case "--heartbeat-interval":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("missing --heartbeat-interval value")
			}
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return cfg, err
			}
			cfg.heartbeatInterval = d
		case "--heartbeat-timeout":
			i++
			if i >= len(args) {
				return cfg, fmt.Errorf("missing --heartbeat-timeout value")
			}
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return cfg, err
			}
			cfg.heartbeatTimeout = d
		default:
			cfg.stationFiles = append(cfg.stationFiles, args[i])
		}
	}

	if cfg.replPort == "" {
		return cfg, fmt.Errorf("--repl-port is required")
	}
	if cfg.primaryAddr == "" {
		return cfg, fmt.Errorf("--primary-addr is required")
	}
	if len(cfg.stationFiles) == 0 {
		return cfg, fmt.Errorf("at least one station file is required")
	}
	if cfg.walPath == "" {
		cfg.walPath = replication.DefaultBackupWalPath(cfg.replPort)
	}
	if cfg.clientPort == "" {
		_, port, err := net.SplitHostPort(cfg.primaryAddr)
		if err != nil {
			return cfg, fmt.Errorf("invalid --primary-addr: %w", err)
		}
		cfg.clientPort = port
	}
	return cfg, nil
}

func (s *replicationServer) Replicate(ctx context.Context, req *pb.ReplicateRequest) (*pb.ReplicateResponse, error) {
	entry := req.Entry
	if entry == nil {
		return &pb.ReplicateResponse{Ok: false, Error: "missing entry"}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.promoted {
		return &pb.ReplicateResponse{Ok: false, Error: "backup promoted"}, nil
	}
	if entry.Seq <= s.lastWalSeq {
		return &pb.ReplicateResponse{Seq: entry.Seq, Ok: true}, nil
	}

	//gap in sequence numbers, something went wrong
	if s.lastWalSeq > 0 && entry.Seq != s.lastWalSeq+1 {
		return &pb.ReplicateResponse{
			Seq:   entry.Seq,
			Ok:    false,
			Error: fmt.Sprintf("expected seq %d, got %d", s.lastWalSeq+1, entry.Seq),
		}, nil
	}

	if err := s.wal.AppendReplicated(entry); err != nil {
		return &pb.ReplicateResponse{Seq: entry.Seq, Ok: false, Error: err.Error()}, nil
	}
	s.lastWalSeq = entry.Seq
	return &pb.ReplicateResponse{Seq: entry.Seq, Ok: true}, nil
}

func (s *replicationServer) Ping(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.PingResponse{LastWalSeq: s.lastWalSeq}, nil
}

func runHeartbeat(cfg backupConfig, srv *replicationServer, grpcServer *grpc.Server, w *wal.Log) {
	var failedSince time.Time
	var failed bool

	for {
		time.Sleep(cfg.heartbeatInterval)

		srv.mu.Lock()
		promoted := srv.promoted
		srv.mu.Unlock()
		if promoted {
			return
		}

		if pingPrimary(cfg.primaryAddr) {
			failed = false
			continue
		}

		if !failed {
			failed = true
			failedSince = time.Now()
			log.Printf("Primary %s unreachable, monitoring for failover", cfg.primaryAddr)
			continue
		}
		if time.Since(failedSince) >= cfg.heartbeatTimeout {
			promote(cfg, srv, grpcServer, w)
			return
		}
	}
}

func pingPrimary(addr string) bool {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return false
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resp, err := pb.NewSnowcastControlClient(conn).Health(ctx, &pb.HealthRequest{})
	return err == nil && resp != nil && resp.Ok
}

func promote(cfg backupConfig, srv *replicationServer, grpcServer *grpc.Server, w *wal.Log) {
	srv.mu.Lock()
	if srv.promoted {
		srv.mu.Unlock()
		return
	}
	srv.promoted = true
	srv.mu.Unlock()

	log.Printf("Promoting backup: replaying WAL and serving on port %s", cfg.clientPort)

	entries, err := w.ReadAll()
	if err != nil {
		log.Fatalf("read wal on promotion: %v", err)
	}

	state.InitStations(cfg.stationFiles)
	for _, entry := range entries {
		if err := state.ApplyWalEntry(entry); err != nil {
			log.Fatalf("apply wal seq %d on promotion: %v", entry.Seq, err)
		}
	}

	local := replication.NewLocalFromLog(w)

	grpcServer.GracefulStop()

	_, err = runtime.Setrve(cfg.clientPort, cfg.stationFiles, local)
	if err != nil {
		log.Fatalf("serve promoted primary: %v", err)
	}

	log.Printf("Backup promoted to primary on port %s", cfg.clientPort)
	select {}
}
