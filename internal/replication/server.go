package replication

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"snowcast-jamesyan2028/pkg/wal"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"google.golang.org/grpc"
)

type inboundHandler struct {
	pb.UnimplementedSnowcastReplicationServer
	wal        *wal.Log
	mu         sync.Mutex
	lastWalSeq uint64
	accepting  bool
}

// This class is responsible for serving inbound SnowcastReplication RPCs when a server is in standby mode.
type ReplServer struct {
	grpc   *grpc.Server //replication server
	lis    net.Listener //holds port 16801 to listen for replication requests
	handler *inboundHandler //replication logic
	done   chan struct{} //allows for graceful stop, serve all active gRPC requests before shutting down
}

//listens on replPort and persists replicated WAL entries.
func StartReplServer(replPort string, w *wal.Log) (*ReplServer, error) {
	lis, err := net.Listen("tcp", ":"+replPort)
	if err != nil {
		return nil, fmt.Errorf("listen replication: %w", err)
	}

	h := &inboundHandler{wal: w, lastWalSeq: w.LastSeq(), accepting: true}
	grpcServer := grpc.NewServer()
	pb.RegisterSnowcastReplicationServer(grpcServer, h)

	rs := &ReplServer{
		grpc:    grpcServer,
		lis:     lis,
		handler: h,
		done:    make(chan struct{}),
	}

	//accept replication connections
	go func() {
		defer close(rs.done)
		log.Printf("Replication server listening on :%s", replPort)
		if err := grpcServer.Serve(lis); err != nil {
			log.Printf("replication server stopped: %v", err)
		}
	}()

	//return replication server object to manager to close later
	return rs, nil
}

// GracefulStop stops accepting replication RPCs and waits for shutdown.
func (r *ReplServer) GracefulStop() {
	if r == nil {
		return
	}
	r.handler.mu.Lock()
	r.handler.accepting = false
	r.handler.mu.Unlock()

	//call graceful stop on gRPC server object
	r.grpc.GracefulStop()
	<-r.done
}

func (h *inboundHandler) Replicate(ctx context.Context, req *pb.ReplicateRequest) (*pb.ReplicateResponse, error) {
	entry := req.Entry
	if entry == nil {
		return &pb.ReplicateResponse{Ok: false, Error: "missing entry"}, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if !h.accepting {
		return &pb.ReplicateResponse{Ok: false, Error: "replication server stopped"}, nil
	}
	if entry.Seq <= h.lastWalSeq {
		return &pb.ReplicateResponse{Seq: entry.Seq, Ok: true}, nil
	}
	if h.lastWalSeq > 0 && entry.Seq != h.lastWalSeq+1 {
		return &pb.ReplicateResponse{
			Seq:   entry.Seq,
			Ok:    false,
			Error: fmt.Sprintf("expected seq %d, got %d", h.lastWalSeq+1, entry.Seq),
		}, nil
	}

	if err := h.wal.AppendReplicated(entry); err != nil {
		return &pb.ReplicateResponse{Seq: entry.Seq, Ok: false, Error: err.Error()}, nil
	}
	h.lastWalSeq = entry.Seq
	return &pb.ReplicateResponse{Seq: entry.Seq, Ok: true}, nil
}

func (h *inboundHandler) Ping(ctx context.Context, req *pb.PingRequest) (*pb.PingResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return &pb.PingResponse{LastWalSeq: h.lastWalSeq}, nil
}
