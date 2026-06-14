package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"

	"snowcast-jamesyan2028/internal/leadership"

	pb "snowcast-jamesyan2028/pkg/protocol"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type failoverResult struct {
	TotalFailoverMS      float64            `json:"total_failover_ms"`
	LeaseExpiryWaitMS    float64            `json:"lease_expiry_wait_ms"`
	DetectAndAcquireMS   float64            `json:"detect_and_acquire_ms"`
	WalReplayMS          float64            `json:"wal_replay_ms"`
	GrpcStartupMS        float64            `json:"grpc_startup_ms"`
	PromoteTotalMS       float64            `json:"promote_total_ms"`
	PreloadOps           int                `json:"preload_ops"`
	BenchPhases          map[string]float64 `json:"bench_phases,omitempty"`
}

func runFailover(args []string) (any, error) {
	fs := flag.NewFlagSet("failover", flag.ExitOnError)
	primaryPID := fs.Int("primary-pid", 0, "primary process PID to kill")
	preloadRPS := fs.Int("preload-rps", 1000, "SetStation RPS before kill")
	preloadDur := fs.String("preload-duration", "15s", "preload duration")
	portBase := fs.Int("port-base", 50000, "UDP port base for preload workers")
	controlAddr := fs.String("control-addr", "127.0.0.1:16800", "client control addr")
	replAddr := fs.String("repl-addr", "127.0.0.1:16801", "backup replication addr")
	etcdEndpoints := fs.String("etcd-endpoints", "127.0.0.1:2379", "etcd endpoints")
	etcdKey := fs.String("etcd-key", leadership.DefaultKey, "etcd primary key")
	backupLog := fs.String("backup-log", "/tmp/snowcast-bench-backup.log", "backup log path")
	timeout := fs.String("timeout", "30s", "failover wait timeout")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	pid := *primaryPID
	if pid == 0 {
		if env := os.Getenv("PRIMARY_PID"); env != "" {
			var err error
			pid, err = strconv.Atoi(env)
			if err != nil {
				return nil, fmt.Errorf("invalid PRIMARY_PID: %w", err)
			}
		}
	}
	if pid == 0 {
		return nil, fmt.Errorf("primary PID required (--primary-pid or PRIMARY_PID)")
	}

	preloadArgs := []string{
		"--mode", "sequential",
		"--rps", strconv.Itoa(*preloadRPS),
		"--duration", *preloadDur,
		"--control-addr", *controlAddr,
		"--repl-addr", *replAddr,
		"--port-base", strconv.Itoa(*portBase),
	}
	preloadRes, err := runReplicationLoad(preloadArgs)
	if err != nil {
		return nil, fmt.Errorf("preload: %w", err)
	}
	preload, ok := preloadRes.(*replicationLoadResult)
	if !ok || preload == nil {
		return nil, fmt.Errorf("preload: unexpected result type")
	}

	tKill := time.Now()
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}
	if err := proc.Signal(syscall.SIGKILL); err != nil {
		return nil, fmt.Errorf("kill primary: %w", err)
	}

	waitTimeout := parseDuration(*timeout, 30*time.Second)
	deadline := time.Now().Add(waitTimeout)

	ec, err := leadership.NewClient(*etcdEndpoints)
	if err != nil {
		return nil, err
	}
	defer ec.Close()

	var tKeyGone time.Time
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, held, err := leadership.GetPrimary(ctx, ec, *etcdKey)
		cancel()
		if err == nil && !held {
			tKeyGone = time.Now()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if tKeyGone.IsZero() {
		return nil, fmt.Errorf("etcd key still held after %v", waitTimeout)
	}

	var tReady time.Time
	for time.Now().Before(deadline) {
		conn, err := grpc.NewClient(*controlAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := pb.NewSnowcastControlClient(conn).Health(ctx, &pb.HealthRequest{})
		cancel()
		conn.Close()
		if err == nil && resp != nil && resp.Ok {
			tReady = time.Now()
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if tReady.IsZero() {
		return nil, fmt.Errorf("backup did not become ready after %v", waitTimeout)
	}

	tPromoteStart, _ := findLogEventTime(*backupLog, "Promoting to leader")
	if tPromoteStart.IsZero() {
		tPromoteStart, _ = findLogEventTime(*backupLog, "failover_promote_start")
	}

	res := failoverResult{
		TotalFailoverMS:   msBetween(tKill, tReady),
		LeaseExpiryWaitMS: msBetween(tKill, tKeyGone),
		PreloadOps:        preload.CompletedOps,
	}

	if !tPromoteStart.IsZero() && tPromoteStart.After(tKeyGone) {
		res.DetectAndAcquireMS = msBetween(tKeyGone, tPromoteStart)
	} else if !tPromoteStart.IsZero() {
		res.DetectAndAcquireMS = msBetween(tKeyGone, tPromoteStart)
		if res.DetectAndAcquireMS < 0 {
			res.DetectAndAcquireMS = 0
		}
	}

	phases, err := parseBenchPhases(*backupLog)
	if err == nil {
		res.BenchPhases = phases
		if v, ok := phases["failover_wal_replay"]; ok {
			res.WalReplayMS = v
		}
		if v, ok := phases["failover_grpc_startup"]; ok {
			res.GrpcStartupMS = v
		}
		if v, ok := phases["failover_promote_total"]; ok {
			res.PromoteTotalMS = v
		}
	}

	return res, nil
}

func msBetween(a, b time.Time) float64 {
	return float64(b.Sub(a).Microseconds()) / 1000.0
}
