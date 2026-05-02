#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

cd "$PROJECT_ROOT"
go build -o snowcast_server ./cmd/snowcast_server/

# Kill any leftover server
fuser -k 16800/tcp 2>/dev/null || true
fuser -k 16800/udp 2>/dev/null || true
sleep 1

# Start server
./snowcast_server 16800 mp3/*.mp3 &
SERVER_PID=$!
sleep 3

# Check server is alive
if ! kill -0 $SERVER_PID 2>/dev/null; then
    echo "Server failed to start!"
    exit 1
fi

# Run tests
go test ./tests/ -v
TEST_EXIT=$?

# Cleanup
kill $SERVER_PID 2>/dev/null || true
exit $TEST_EXIT