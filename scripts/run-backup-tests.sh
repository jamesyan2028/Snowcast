#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"
go build -o snowcast_backup ./cmd/snowcast_backup/
go build -o snowcast_server ./cmd/snowcast_server/

fuser -k 16800/tcp 2>/dev/null || true
fuser -k 16800/udp 2>/dev/null || true
fuser -k 16801/tcp 2>/dev/null || true
pkill -f 'etcd --listen-client-urls http://127.0.0.1:2379' 2>/dev/null || true
sleep 1

rm -rf /tmp/snowcast-etcd
rm -f /tmp/snowcast-16800.wal /tmp/snowcast-backup-16801.wal

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

./snowcast_server \
  --etcd-endpoints 127.0.0.1:2379 \
  --backup-addr 127.0.0.1:16801 \
  --repl-port 16801 \
  --lease-ttl 3 \
  16800 mp3/*.mp3 &
SERVER_PID=$!
export PRIMARY_PID=$SERVER_PID
sleep 2

./snowcast_backup \
  --etcd-endpoints 127.0.0.1:2379 \
  --repl-port 16801 \
  --client-port 16800 \
  --backup-addr 127.0.0.1:16801 \
  --lease-ttl 3 \
  --lease-poll 500ms \
  mp3/*.mp3 &
BACKUP_PID=$!
sleep 2

if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Server failed to start!"
    kill $BACKUP_PID $ETCD_PID 2>/dev/null || true
    exit 1
fi

go test ./tests/ -v -run '^TestBackupReplication'
REPL_EXIT=$?

FAILOVER_EXIT=0
if [ $REPL_EXIT -eq 0 ]; then
    go test ./tests/ -v -run '^TestBackupFailoverPromotion'
    FAILOVER_EXIT=$?
fi

kill $BACKUP_PID 2>/dev/null || true
kill $ETCD_PID 2>/dev/null || true
fuser -k 16800/tcp 16800/udp 16801/tcp 2>/dev/null || true

if [ $REPL_EXIT -ne 0 ] || [ $FAILOVER_EXIT -ne 0 ]; then
    exit 1
fi
exit 0
