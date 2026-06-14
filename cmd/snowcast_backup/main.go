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
	leasePoll := flag.Duration("lease-poll", time.Second, "etcd poll interval")
	replPort := flag.String("repl-port", "", "replication listen port (required)")
	clientPort := flag.String("client-port", "16800", "client port after promotion")
	backupAddr := flag.String("backup-addr", "", "peer replication addr after promotion, e.g. 127.0.0.1:16801")
	walPath := flag.String("wal", "", "backup WAL path")
	flag.Parse()

	args := flag.Args()
	if *etcdEndpoints == "" || *replPort == "" {
		log.Fatal("Usage: ./snowcast_backup --etcd-endpoints <host:port> --repl-port <port> [--client-port <port>] [--backup-addr <host:port>] [--wal <path>] <file0> ...")
	}
	if len(args) < 1 {
		log.Fatal("Usage: ./snowcast_backup --etcd-endpoints <host:port> --repl-port <port> [--client-port <port>] [--backup-addr <host:port>] [--wal <path>] <file0> ...")
	}

	if *walPath == "" {
		*walPath = replication.DefaultBackupWalPath(*replPort)
	}

	mgr, err := node.New(node.Config{
		EtcdEndpoints: *etcdEndpoints,
		EtcdKey:       *etcdKey,
		LeaseTTL:      *leaseTTL,
		LeasePoll:     *leasePoll,
		ClientPort:    *clientPort,
		ReplPort:      *replPort,
		BackupAddr:    *backupAddr,
		WalPath:       *walPath,
		Files:         args,
	})
	if err != nil {
		log.Fatalf("Failed to init node: %v", err)
	}
	defer mgr.Close()

	if err := mgr.RunAsStandby(); err != nil {
		log.Fatalf("Failed to run standby: %v", err)
	}
}
