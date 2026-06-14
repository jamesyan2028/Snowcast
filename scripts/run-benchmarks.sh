#!/bin/bash
# Snowcast benchmark orchestrator.
#
# Measures:
#   - WAL replay on primary setup
#   - SetStation replication latency (sequential, then concurrency sweep)
#   - Failover latency breakdown (lease expiry, poll, WAL replay, gRPC startup)
#
# SetStation is streaming: the load driver cancels after the first Announce.
# High target RPS may saturate a single node; achieved RPS is reported honestly.
#
# Usage: bash scripts/run-benchmarks.sh [--skip-failover] [--output-dir benchmarks]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

OUTPUT_DIR="$PROJECT_ROOT/benchmarks"
SKIP_FAILOVER=0
RPS_LIST="500 1000 5000"
LOAD_DURATION="30s"
PRELOAD_RPS=1000
PRELOAD_DURATION="15s"
SWEEP_RPS=500
SWEEP_DURATION="10s"
LEASE_TTL=3
LEASE_POLL="500ms"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --skip-failover) SKIP_FAILOVER=1; shift ;;
        --output-dir) OUTPUT_DIR="$2"; shift 2 ;;
        --rps-list) RPS_LIST="$2"; shift 2 ;;
        --load-duration) LOAD_DURATION="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

mkdir -p "$OUTPUT_DIR"
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
REPORT_TXT="$OUTPUT_DIR/results-${TIMESTAMP}.txt"
REPORT_JSON="$OUTPUT_DIR/results-${TIMESTAMP}.json"
TMP_DIR="$(mktemp -d)"
PRIMARY_LOG="/tmp/snowcast-bench-primary.log"
BACKUP_LOG="/tmp/snowcast-bench-backup.log"

cd "$PROJECT_ROOT"
go build -o snowcast_server ./cmd/snowcast_server/
go build -o snowcast_backup ./cmd/snowcast_backup/
go build -o snowcast_bench ./cmd/snowcast_bench/

fuser -k 16800/tcp 2>/dev/null || true
fuser -k 16800/udp 2>/dev/null || true
fuser -k 16801/tcp 2>/dev/null || true
pkill -f 'etcd --listen-client-urls http://127.0.0.1:2379' 2>/dev/null || true
sleep 1

rm -rf /tmp/snowcast-etcd
rm -f /tmp/snowcast-16800.wal /tmp/snowcast-backup-16801.wal
: > "$PRIMARY_LOG"
: > "$BACKUP_LOG"

ETCD_BIN="$(command -v etcd || true)"
if [ -z "$ETCD_BIN" ]; then
    ETCD_DIR="/tmp/etcd-v3.5.17-linux-amd64"
    if [ ! -x "$ETCD_DIR/etcd" ]; then
        curl -fsSL -o /tmp/etcd.tgz https://github.com/etcd-io/etcd/releases/download/v3.5.17/etcd-v3.5.17-linux-amd64.tar.gz
        tar -xzf /tmp/etcd.tgz -C /tmp
    fi
    ETCD_BIN="$ETCD_DIR/etcd"
fi

"$ETCD_BIN" --listen-client-urls http://127.0.0.1:2379 \
     --advertise-client-urls http://127.0.0.1:2379 \
     --data-dir /tmp/snowcast-etcd &
ETCD_PID=$!
sleep 1

export SNOWCAST_BENCH=1

./snowcast_server \
  --etcd-endpoints 127.0.0.1:2379 \
  --backup-addr 127.0.0.1:16801 \
  --repl-port 16801 \
  --lease-ttl "$LEASE_TTL" \
  16800 mp3/*.mp3 >>"$PRIMARY_LOG" 2>&1 &
SERVER_PID=$!
export PRIMARY_PID=$SERVER_PID
sleep 2

./snowcast_backup \
  --etcd-endpoints 127.0.0.1:2379 \
  --repl-port 16801 \
  --client-port 16800 \
  --backup-addr 127.0.0.1:16801 \
  --lease-ttl "$LEASE_TTL" \
  --lease-poll "$LEASE_POLL" \
  mp3/*.mp3 >>"$BACKUP_LOG" 2>&1 &
BACKUP_PID=$!
sleep 2

cleanup() {
    kill $SERVER_PID 2>/dev/null || true
    kill $BACKUP_PID 2>/dev/null || true
    kill $ETCD_PID 2>/dev/null || true
    fuser -k 16800/tcp 16800/udp 16801/tcp 2>/dev/null || true
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Server failed to start!"
    tail -20 "$PRIMARY_LOG"
    exit 1
fi

for _ in $(seq 1 30); do
    if grep -q "Backup ready at" "$PRIMARY_LOG" 2>/dev/null; then
        break
    fi
    sleep 1
done

./snowcast_bench setup-wal-replay --primary-log "$PRIMARY_LOG" --isolated-counts "1000,5000,10000" > "$TMP_DIR/setup.json"

{
    echo "=== Snowcast Benchmarks ==="
    echo "Config: lease_ttl=${LEASE_TTL}s lease_poll=${LEASE_POLL}"
    echo ""
    echo "--- WAL replay (setup) ---"
} > "$REPORT_TXT"

python3 - <<PY >> "$REPORT_TXT"
import json
with open("$TMP_DIR/setup.json") as f:
    d = json.load(f)
print(f"integrated_primary_recover_ms: {d.get('integrated_primary_recover_ms', 'n/a')}")
for k, v in sorted(d.get('isolated_replay_ms', {}).items()):
    print(f"isolated_replay_{k}: {v}")
PY

echo "" >> "$REPORT_TXT"
echo "--- Replication (SetStation sequential) ---" >> "$REPORT_TXT"

LOAD_FILES=()
PORT_BASE=20000
for RPS in $RPS_LIST; do
    out="$TMP_DIR/load_seq_${RPS}.json"
    ./snowcast_bench replication-load --mode sequential --rps "$RPS" --duration "$LOAD_DURATION" --port-base "$PORT_BASE" > "$out"
    LOAD_FILES+=("$out")
    PORT_BASE=$((PORT_BASE + 2000))
    python3 - <<PY >> "$REPORT_TXT"
import json
with open("$out") as f:
    d = json.load(f)
sat = " WARNING: saturated" if d.get("saturated") else ""
lat = d.get("latency_ms", {})
print(f"sequential target_rps={d['target_rps']}  achieved_rps={d['achieved_rps']:.1f}  p50={lat.get('p50',0):.2f}ms p95={lat.get('p95',0):.2f}ms p99={lat.get('p99',0):.2f}ms{sat}")
PY
done

echo "" >> "$REPORT_TXT"
echo "--- Concurrency sweep (degradation vs sequential baseline) ---" >> "$REPORT_TXT"
./snowcast_bench concurrency-sweep --rps "$SWEEP_RPS" --duration "$SWEEP_DURATION" --port-base "$PORT_BASE" > "$TMP_DIR/concurrency_sweep.json"
PORT_BASE=$((PORT_BASE + 10000))
python3 - <<PY >> "$REPORT_TXT"
import json
with open("$TMP_DIR/concurrency_sweep.json") as f:
    d = json.load(f)
base = d.get("baseline_latency_ms", {})
print(f"sweep_target_rps={d.get('target_rps')}  baseline_p50={base.get('p50',0):.2f}ms  baseline_p95={base.get('p95',0):.2f}ms  degrade_threshold={d.get('degrade_threshold')}")
deg = d.get("degradation_at_concurrency", 0)
if deg:
    print(f"degradation_at_concurrency: {deg}")
else:
    print("degradation_at_concurrency: none (within threshold at all tested levels)")
for lvl in d.get("levels", []):
    lat = lvl.get("latency_ms", {})
    flag = " DEGRADED" if lvl.get("degraded") else ""
    sat = " saturated" if lvl.get("saturated") else ""
    print(f"  concurrency={lvl['concurrency']}  achieved_rps={lvl['achieved_rps']:.1f}  p50={lat.get('p50',0):.2f}ms p95={lat.get('p95',0):.2f}ms{flag}{sat}")
PY

if [ "$SKIP_FAILOVER" -eq 0 ]; then
    echo "" >> "$REPORT_TXT"
    echo "--- Failover ---" >> "$REPORT_TXT"
    ./snowcast_bench failover \
        --primary-pid "$PRIMARY_PID" \
        --preload-rps "$PRELOAD_RPS" \
        --preload-duration "$PRELOAD_DURATION" \
        --port-base "$PORT_BASE" \
        --backup-log "$BACKUP_LOG" > "$TMP_DIR/failover.json"
    python3 - <<PY >> "$REPORT_TXT"
import json
with open("$TMP_DIR/failover.json") as f:
    d = json.load(f)
for k in ["total_failover_ms", "lease_expiry_wait_ms", "detect_and_acquire_ms",
          "wal_replay_ms", "grpc_startup_ms", "promote_total_ms", "preload_ops"]:
    print(f"{k}: {d.get(k, 'n/a')}")
PY
else
    echo "null" > "$TMP_DIR/failover.json"
fi

python3 - <<PY > "$REPORT_JSON"
import json, glob
with open("$TMP_DIR/setup.json") as f:
    setup = json.load(f)
loads = []
for path in sorted(glob.glob("$TMP_DIR/load_*.json")):
    with open(path) as f:
        loads.append(json.load(f))
with open("$TMP_DIR/failover.json") as f:
    failover = json.load(f)
with open("$TMP_DIR/concurrency_sweep.json") as f:
    sweep = json.load(f)
out = {
    "config": {"lease_ttl_sec": $LEASE_TTL, "lease_poll": "$LEASE_POLL"},
    "setup_wal_replay": setup,
    "replication_load_sequential": loads,
    "concurrency_sweep": sweep,
    "failover": failover if failover else None,
}
print(json.dumps(out, indent=2))
PY

echo "" >> "$REPORT_TXT"
echo "Full JSON: $REPORT_JSON" >> "$REPORT_TXT"
cat "$REPORT_TXT"
echo "Wrote $REPORT_JSON"
