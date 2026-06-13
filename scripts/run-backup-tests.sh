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
sleep 1

rm -f /tmp/snowcast-16800.wal /tmp/snowcast-backup-16801.wal

./snowcast_backup \
  --repl-port 16801 \
  --primary-addr 127.0.0.1:16800 \
  --heartbeat-interval 500ms \
  --heartbeat-timeout 2s \
  mp3/*.mp3 &
BACKUP_PID=$!
sleep 1

./snowcast_server --backup-addr 127.0.0.1:16801 16800 mp3/*.mp3 &
SERVER_PID=$!
export PRIMARY_PID=$SERVER_PID
sleep 3

if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Server failed to start!"
    kill $BACKUP_PID 2>/dev/null || true
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
fuser -k 16800/tcp 2>/dev/null || true
fuser -k 16800/udp 2>/dev/null || true
fuser -k 16801/tcp 2>/dev/null || true

if [ $REPL_EXIT -ne 0 ] || [ $FAILOVER_EXIT -ne 0 ]; then
    exit 1
fi
exit 0
