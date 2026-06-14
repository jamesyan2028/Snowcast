package main

import (
	"flag"
	"log"
	"time"

	"snowcast-jamesyan2028/internal/node"
	"snowcast-jamesyan2028/internal/replication"
)

func main() {
	etcdEndpoints := flag.String("etcd-endpoints", "", "etcd endpoints (required), e.g. 127.0.0.1:2379")
	etcdKey := flag.String("etcd-key", "", "etcd primary key (default /snowcast/primary)")
	leaseTTL := flag.Int64("lease-ttl", 5, "etcd lease TTL in seconds")
	backupAddr := flag.String("backup-addr", "", "backup replication address (required), e.g. 127.0.0.1:16801")
	replPort := flag.String("repl-port", "", "replication port when demoted to standby (required), e.g. 16801")
	walPath := flag.String("wal", "", "primary WAL file path (default: /tmp/snowcast-<port>.wal)")
	flag.Parse()

	args := flag.Args()
	if *etcdEndpoints == "" || *backupAddr == "" || *replPort == "" {
		log.Fatal("Usage: ./snowcast_server --etcd-endpoints <host:port> --backup-addr <host:port> --repl-port <port> [--wal <path>] <listen_port> <file0> ...")
	}
	if len(args) < 2 {
		log.Fatal("Usage: ./snowcast_server --etcd-endpoints <host:port> --backup-addr <host:port> --repl-port <port> [--wal <path>] <listen_port> <file0> ...")
	}

	port := args[0]
	files := args[1:]

	if *walPath == "" {
		*walPath = replication.DefaultWalPath(port)
	}

	mgr, err := node.New(node.Config{
		EtcdEndpoints: *etcdEndpoints,
		EtcdKey:       *etcdKey,
		LeaseTTL:      *leaseTTL,
		LeasePoll:     time.Second,
		ClientPort:    port,
		ReplPort:      *replPort,
		BackupAddr:    *backupAddr,
		WalPath:       *walPath,
		Files:         files,
	})
	if err != nil {
		log.Fatalf("Failed to init node: %v", err)
	}
	defer mgr.Close()

	if err := mgr.RunAsLeader(); err != nil {
		log.Fatalf("Failed to run as leader: %v", err)
	}
}
