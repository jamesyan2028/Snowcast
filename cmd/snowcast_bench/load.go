package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type loadConfig struct {
	rps         int
	duration    time.Duration
	controlAddr string
	replAddr    string
	numStations int
	portBase    int
	mode        string // sequential | concurrent
	concurrency int
}

type replicationLoadResult struct {
	Mode           string             `json:"mode"`
	Concurrency    int                `json:"concurrency"`
	TargetRPS      int                `json:"target_rps"`
	AchievedRPS    float64            `json:"achieved_rps"`
	Saturated      bool               `json:"saturated"`
	DurationSec    float64            `json:"duration_sec"`
	CompletedOps   int                `json:"completed_ops"`
	LatencyMS      map[string]float64 `json:"latency_ms"`
	MaxBackupLagMS float64            `json:"max_backup_lag_ms"`
	MaxSeqBehind   int                `json:"max_seq_behind"`
}

type concurrencyLevelResult struct {
	Concurrency int                `json:"concurrency"`
	TargetRPS   int                `json:"target_rps"`
	AchievedRPS float64            `json:"achieved_rps"`
	LatencyMS   map[string]float64 `json:"latency_ms"`
	Saturated   bool               `json:"saturated"`
	Degraded    bool               `json:"degraded"`
}

type concurrencySweepResult struct {
	TargetRPS                int                    `json:"target_rps"`
	BaselineLatencyMS        map[string]float64     `json:"baseline_latency_ms"`
	DegradeThreshold         float64                `json:"degrade_threshold"`
	DegradationAtConcurrency int                    `json:"degradation_at_concurrency"`
	Levels                   []concurrencyLevelResult `json:"levels"`
}

func parseLoadConfig(args []string) (loadConfig, error) {
	fs := flag.NewFlagSet("replication-load", flag.ExitOnError)
	rps := fs.Int("rps", 500, "target SetStation requests per second")
	durationStr := fs.String("duration", "30s", "load duration")
	controlAddr := fs.String("control-addr", "127.0.0.1:16800", "primary control gRPC addr")
	replAddr := fs.String("repl-addr", "127.0.0.1:16801", "backup replication addr")
	numStations := fs.Int("stations", 7, "station count modulo")
	portBase := fs.Int("port-base", 20000, "base UDP port for worker clients")
	mode := fs.String("mode", "sequential", "sequential (one in-flight) or concurrent")
	concurrency := fs.Int("concurrency", 0, "max in-flight requests for concurrent mode (default min(rps,500))")
	if err := fs.Parse(args); err != nil {
		return loadConfig{}, err
	}

	cfg := loadConfig{
		rps:         *rps,
		duration:    parseDuration(*durationStr, 30*time.Second),
		controlAddr: *controlAddr,
		replAddr:    *replAddr,
		numStations: *numStations,
		portBase:    *portBase,
		mode:        strings.ToLower(*mode),
		concurrency: *concurrency,
	}
	if cfg.mode != "sequential" && cfg.mode != "concurrent" {
		return loadConfig{}, fmt.Errorf("mode must be sequential or concurrent")
	}
	return cfg, nil
}

func runReplicationLoad(args []string) (any, error) {
	cfg, err := parseLoadConfig(args)
	if err != nil {
		return nil, err
	}
	return executeLoad(cfg)
}

func runConcurrencySweep(args []string) (any, error) {
	fs := flag.NewFlagSet("concurrency-sweep", flag.ExitOnError)
	rps := fs.Int("rps", 500, "target RPS while sweeping concurrency")
	durationStr := fs.String("duration", "10s", "duration per concurrency level")
	levelsStr := fs.String("levels", "1,2,4,8,16,32,64,128,256", "comma-separated concurrency levels")
	threshold := fs.Float64("degrade-threshold", 1.5, "p95 latency multiplier vs baseline to mark degradation")
	controlAddr := fs.String("control-addr", "127.0.0.1:16800", "primary control gRPC addr")
	replAddr := fs.String("repl-addr", "127.0.0.1:16801", "backup replication addr")
	portBase := fs.Int("port-base", 30000, "base UDP port for worker clients")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	duration := parseDuration(*durationStr, 10*time.Second)
	levels := parseIntList(*levelsStr, []int{1, 2, 4, 8, 16, 32, 64, 128, 256})
	if len(levels) == 0 {
		return nil, fmt.Errorf("no concurrency levels")
	}

	baselineCfg := loadConfig{
		rps:         *rps,
		duration:    duration,
		controlAddr: *controlAddr,
		replAddr:    *replAddr,
		numStations: 7,
		portBase:    *portBase,
		mode:        "sequential",
		concurrency: 1,
	}
	baselineRes, err := executeLoad(baselineCfg)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	baselineP95 := baselineRes.LatencyMS["p95"]

	out := concurrencySweepResult{
		TargetRPS:         *rps,
		BaselineLatencyMS: baselineRes.LatencyMS,
		DegradeThreshold:  *threshold,
		Levels:            make([]concurrencyLevelResult, 0, len(levels)),
	}

	degradationAt := 0
	for i, level := range levels {
		portBaseLevel := *portBase + i*1000
		cfg := loadConfig{
			rps:         *rps,
			duration:    duration,
			controlAddr: *controlAddr,
			replAddr:    *replAddr,
			numStations: 7,
			portBase:    portBaseLevel,
			mode:        "concurrent",
			concurrency: level,
		}
		res, err := executeLoad(cfg)
		if err != nil {
			return nil, fmt.Errorf("concurrency %d: %w", level, err)
		}

		degraded := baselineP95 > 0 && res.LatencyMS["p95"] > baselineP95*(*threshold)
		if degraded && degradationAt == 0 {
			degradationAt = level
		}

		out.Levels = append(out.Levels, concurrencyLevelResult{
			Concurrency: level,
			TargetRPS:   res.TargetRPS,
			AchievedRPS: res.AchievedRPS,
			LatencyMS:   res.LatencyMS,
			Saturated:   res.Saturated,
			Degraded:    degraded,
		})
	}

	out.DegradationAtConcurrency = degradationAt
	return out, nil
}

func executeLoad(cfg loadConfig) (*replicationLoadResult, error) {
	concurrency := cfg.concurrency
	if cfg.mode == "sequential" {
		concurrency = 1
	} else if concurrency <= 0 {
		concurrency = cfg.rps
		if concurrency > 500 {
			concurrency = 500
		}
		if concurrency < 1 {
			concurrency = 1
		}
	}

	conn, err := grpc.NewClient(cfg.controlAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("control client: %w", err)
	}
	defer conn.Close()
	client := pb.NewSnowcastControlClient(conn)

	replConn, err := grpc.NewClient(cfg.replAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("repl client: %w", err)
	}
	defer replConn.Close()
	replClient := pb.NewSnowcastReplicationClient(replConn)

	ctx := context.Background()
	ports := make([]uint32, concurrency)
	for i := 0; i < concurrency; i++ {
		ports[i] = uint32(cfg.portBase + i)
		_, _ = client.Disconnect(ctx, &pb.DisconnectRequest{UdpPort: ports[i]})
		_, err := client.Handshake(ctx, &pb.HelloMessage{UdpPort: ports[i]})
		if err != nil {
			return nil, fmt.Errorf("warmup handshake worker %d (port %d): %w", i, ports[i], err)
		}
	}

	startSeq, err := backupLastSeq(replClient)
	if err != nil {
		return nil, err
	}

	var completed atomic.Uint64
	loadDone := make(chan struct{})
	var maxSeqBehind atomic.Int64
	var maxLagMS atomic.Uint64

	go func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-loadDone:
				return
			case <-ticker.C:
				seq, err := backupLastSeq(replClient)
				if err != nil {
					continue
				}
				done := completed.Load()
				behind := int64(done) - int64(seq-startSeq)
				if behind > maxSeqBehind.Load() {
					maxSeqBehind.Store(behind)
					maxLagMS.Add(5)
				}
			}
		}
	}()

	var latencies []float64
	loadStart := time.Now()
	stopAt := loadStart.Add(cfg.duration)

	if cfg.mode == "sequential" {
		latencies = runSequentialLoad(ctx, client, ports[0], cfg, stopAt, &completed)
	} else {
		latencies = runConcurrentLoad(ctx, client, ports, cfg, stopAt, concurrency, &completed)
	}

	close(loadDone)

	elapsed := time.Since(loadStart).Seconds()
	ops := completed.Load()
	achieved := float64(ops) / elapsed
	saturated := achieved < float64(cfg.rps)*0.95

	return &replicationLoadResult{
		Mode:           cfg.mode,
		Concurrency:    concurrency,
		TargetRPS:      cfg.rps,
		AchievedRPS:    achieved,
		Saturated:      saturated,
		DurationSec:    elapsed,
		CompletedOps:   int(ops),
		LatencyMS:      summarizeLatencies(latencies),
		MaxBackupLagMS: float64(maxLagMS.Load()),
		MaxSeqBehind:   int(maxSeqBehind.Load()),
	}, nil
}

func runSequentialLoad(ctx context.Context, client pb.SnowcastControlClient, port uint32, cfg loadConfig, stopAt time.Time, completed *atomic.Uint64) []float64 {
	interval := time.Second / time.Duration(cfg.rps)
	if interval <= 0 {
		interval = time.Millisecond
	}

	var latencies []float64
	var opSeq uint64
	nextSlot := time.Now()

	for time.Now().Before(stopAt) {
		if opSeq > 0 {
			wait := time.Until(nextSlot)
			if wait > 0 {
				time.Sleep(wait)
			}
			nextSlot = nextSlot.Add(interval)
		} else {
			nextSlot = time.Now().Add(interval)
		}

		station := uint32(opSeq % uint64(cfg.numStations))
		opSeq++

		lat, ok := doSetStation(ctx, client, port, station)
		if ok {
			completed.Add(1)
			latencies = append(latencies, lat)
		}
	}
	return latencies
}

func runConcurrentLoad(ctx context.Context, client pb.SnowcastControlClient, ports []uint32, cfg loadConfig, stopAt time.Time, concurrency int, completed *atomic.Uint64) []float64 {
	interval := time.Second / time.Duration(cfg.rps)
	if interval <= 0 {
		interval = time.Millisecond
	}

	var latencies []float64
	var latMu sync.Mutex
	freeWorkers := make(chan int, concurrency)
	for i := 0; i < concurrency; i++ {
		freeWorkers <- i
	}

	var wg sync.WaitGroup
	var opSeq atomic.Uint64
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

dispatch:
	for time.Now().Before(stopAt) {
		select {
		case <-ticker.C:
			select {
			case workerID := <-freeWorkers:
				wg.Add(1)
				go func(wid int) {
					defer wg.Done()
					defer func() { freeWorkers <- wid }()

					slot := opSeq.Add(1)
					station := uint32(slot % uint64(cfg.numStations))
					lat, ok := doSetStation(ctx, client, ports[wid], station)
					if ok {
						completed.Add(1)
						latMu.Lock()
						latencies = append(latencies, lat)
						latMu.Unlock()
					}
				}(workerID)
			default:
			}
		case <-time.After(time.Until(stopAt)):
			break dispatch
		}
	}

	wg.Wait()
	return latencies
}

func doSetStation(ctx context.Context, client pb.SnowcastControlClient, port, station uint32) (float64, bool) {
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	start := time.Now()
	stream, err := client.SetStation(callCtx, &pb.SetStationMessage{
		StationNumber: station,
		UdpPort:       port,
	})
	if err != nil {
		return 0, false
	}
	ev, err := stream.Recv()
	lat := float64(time.Since(start).Microseconds()) / 1000.0
	if err != nil {
		return 0, false
	}
	if _, ok := ev.Event.(*pb.ServerEvent_Announce); !ok {
		return 0, false
	}
	return lat, true
}

func backupLastSeq(repl pb.SnowcastReplicationClient) (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := repl.Ping(ctx, &pb.PingRequest{})
	if err != nil {
		return 0, err
	}
	return resp.LastWalSeq, nil
}
