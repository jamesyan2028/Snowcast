package main

import (
	"flag"
	"log"

	"snowcast-jamesyan2028/internal/replication"
	"snowcast-jamesyan2028/internal/runtime"
	"snowcast-jamesyan2028/internal/state"
)

func main() {
	backupAddr := flag.String("backup-addr", "", "backup replication gRPC address (required), e.g. 127.0.0.1:16801")
	walPath := flag.String("wal", "", "primary WAL file path (default: /tmp/snowcast-<port>.wal)")
	flag.Parse()

	args := flag.Args()
	if *backupAddr == "" {
		log.Fatal("Usage: ./snowcast_server --backup-addr <host:port> [--wal <path>] <listen_port> <file0> [file1] ...")
	}
	if len(args) < 2 {
		log.Fatal("Usage: ./snowcast_server --backup-addr <host:port> [--wal <path>] <listen_port> <file0> [file1] ...")
	}

	port := args[0]
	files := args[1:]

	if *walPath == "" {
		*walPath = replication.DefaultWalPath(port)
	}

	state.InitStations(files)

	if err := replication.Start(*walPath, *backupAddr); err != nil {
		log.Fatalf("Failed to start replication: %v", err)
	}
	defer replication.Global().Shutdown()

	coord := replication.Global()
	grpcServer, err := runtime.Serve(port, files, coord)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	runtime.HandleUserInput(grpcServer)
}
