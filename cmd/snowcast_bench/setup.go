package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"snowcast-jamesyan2028/internal/state"
	"snowcast-jamesyan2028/pkg/wal"

	pb "snowcast-jamesyan2028/pkg/protocol"
)

type setupResult struct {
	IntegratedPrimaryRecoverMS float64            `json:"integrated_primary_recover_ms"`
	IsolatedReplay             map[string]float64 `json:"isolated_replay_ms"`
}

func runSetupWalReplay(args []string) (any, error) {
	fs := flag.NewFlagSet("setup-wal-replay", flag.ExitOnError)
	primaryLog := fs.String("primary-log", "/tmp/snowcast-bench-primary.log", "primary server log path")
	isolatedCounts := fs.String("isolated-counts", "1000,10000,50000", "comma-separated WAL entry counts")
	stationFiles := fs.String("stations", "mp3/a.mp3", "placeholder station file for InitStations")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	res := setupResult{
		IsolatedReplay: make(map[string]float64),
	}

	if ms, err := integratedRecoverMS(*primaryLog); err == nil {
		res.IntegratedPrimaryRecoverMS = ms
	} else {
		fmt.Fprintf(os.Stderr, "warning: integrated recover: %v\n", err)
	}

	state.InitStations([]string{*stationFiles})
	for _, n := range parseIntList(*isolatedCounts, []int{1000, 10000, 50000}) {
		state.ClientMutex.Lock()
		state.CurrentClients = make(map[string]*state.ClientInfo)
		state.ClientMutex.Unlock()
		ms, err := isolatedReplayMS(n)
		if err != nil {
			return nil, fmt.Errorf("isolated replay n=%d: %w", n, err)
		}
		res.IsolatedReplay[fmt.Sprintf("%d_entries", n)] = ms
	}

	return res, nil
}

func isolatedReplayMS(n int) (float64, error) {
	path := tempWalPath(fmt.Sprintf("bench-replay-%d.wal", n))
	os.Remove(path)

	log, err := wal.Open(path)
	if err != nil {
		return 0, err
	}
	for i := 0; i < n; i++ {
		_, err := log.Append(&pb.WalEntry{
			Op: &pb.WalEntry_Handshake{
				Handshake: &pb.HandshakeOp{
					ClientIp: "127.0.0.1",
					UdpPort:  uint32(10000 + i),
				},
			},
		})
		if err != nil {
			log.Close()
			return 0, err
		}
	}
	log.Close()

	replayLog, err := wal.Open(path)
	if err != nil {
		return 0, err
	}
	defer replayLog.Close()
	os.Remove(path)

	state.InitStations([]string{filepath.Join("mp3", "a.mp3")})
	// reset client map between runs
	state.ClientMutex.Lock()
	state.CurrentClients = make(map[string]*state.ClientInfo)
	state.ClientMutex.Unlock()

	start := time.Now()
	entries, err := replayLog.ReadAll()
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		if err := state.ApplyWalEntry(entry); err != nil {
			return 0, err
		}
	}
	return float64(time.Since(start).Microseconds()) / 1000.0, nil
}
