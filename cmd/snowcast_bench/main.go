package main

import (
	"encoding/json"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	var result any
	var err error

	switch os.Args[1] {
	case "setup-wal-replay":
		result, err = runSetupWalReplay(os.Args[2:])
	case "replication-load":
		result, err = runReplicationLoad(os.Args[2:])
	case "concurrency-sweep":
		result, err = runConcurrencySweep(os.Args[2:])
	case "failover":
		result, err = runFailover(os.Args[2:])
	default:
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "encode: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `Usage: snowcast_bench <command> [flags]

Commands:
  setup-wal-replay   Measure WAL replay on setup (integrated + isolated)
  replication-load     SetStation load (--mode sequential|concurrent)
  concurrency-sweep    Find concurrency level where latency degrades
  failover           Kill primary and measure failover breakdown

`)
}
