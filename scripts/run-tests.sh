#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"
go build -o snowcast_backup ./cmd/snowcast_backup/
go build -o snowcast_server ./cmd/snowcast_server/

# Kill any leftover processes
fuser -k 16800/tcp 2>/dev/null || true
fuser -k 16800/udp 2>/dev/null || true
fuser -k 16801/tcp 2>/dev/null || true
sleep 1

rm -f /tmp/snowcast-16800.wal /tmp/snowcast-backup-16801.wal

# Start backup first (standby)
./snowcast_backup --repl-port 16801 --primary-addr 127.0.0.1:16800 mp3/*.mp3 &
BACKUP_PID=$!
sleep 1

# Start primary pointing at backup
./snowcast_server --backup-addr 127.0.0.1:16801 16800 mp3/*.mp3 &
SERVER_PID=$!
sleep 3

# Check server is alive
if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Server failed to start!"
    kill $BACKUP_PID 2>/dev/null || true
    exit 1
fi

# Run tests
go test ./tests/ -v
TEST_EXIT=$?

# Cleanup
kill $SERVER_PID 2>/dev/null || true
kill $BACKUP_PID 2>/dev/null || true
fuser -k 16801/tcp 2>/dev/null || true
exit $TEST_EXIT
