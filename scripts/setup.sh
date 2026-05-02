#!/bin/bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[INFO]${NC} $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*"; exit 1; }

# Navigate to project root (directory containing go.mod)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR" && while [ ! -f go.mod ]; do cd ..; done && pwd)"
cd "$PROJECT_ROOT"
info "Project root: $PROJECT_ROOT"

# Check Go is installed
if ! command -v go &> /dev/null; then
    error "Go is not installed. Please install Go first: https://go.dev/dl/"
fi
info "Go found: $(go version)"

# Install protoc (Protocol Buffers compiler)
if ! command -v protoc &> /dev/null; then
    info "Installing protoc..."
    PROTOC_VERSION="25.1"
    ARCH=$(uname -m)
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')

    if [ "$ARCH" = "x86_64" ]; then
        PROTOC_ARCH="x86_64"
    elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
        PROTOC_ARCH="aarch_64"
    else
        error "Unsupported architecture: $ARCH"
    fi

    if [ "$OS" = "linux" ]; then
        PROTOC_OS="linux"
    elif [ "$OS" = "darwin" ]; then
        PROTOC_OS="osx"
    else
        error "Unsupported OS: $OS"
    fi

    PROTOC_ZIP="protoc-${PROTOC_VERSION}-${PROTOC_OS}-${PROTOC_ARCH}.zip"
    PROTOC_URL="https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/${PROTOC_ZIP}"

    TMP_DIR=$(mktemp -d)
    curl -fsSL -o "${TMP_DIR}/${PROTOC_ZIP}" "$PROTOC_URL"
    sudo unzip -o "${TMP_DIR}/${PROTOC_ZIP}" -d /usr/local bin/protoc
    sudo unzip -o "${TMP_DIR}/${PROTOC_ZIP}" -d /usr/local 'include/*'
    rm -rf "$TMP_DIR"
    info "protoc installed"
else
    info "protoc found: $(protoc --version)"
fi

# Install Go protobuf plugins
info "Installing protoc-gen-go and protoc-gen-go-grpc..."
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Make sure GOPATH/bin is on PATH
GOBIN=$(go env GOPATH)/bin
if [[ ":$PATH:" != *":$GOBIN:"* ]]; then
    warn "Add this to your shell profile (e.g. ~/.bashrc):"
    warn "  export PATH=\"\$PATH:$(go env GOPATH)/bin\""
    export PATH="$PATH:$GOBIN"
fi

# Install Go module dependencies
info "Installing Go module dependencies..."
go get google.golang.org/grpc
go get google.golang.org/protobuf
go mod tidy

info "Setup complete! All gRPC dependencies are installed."