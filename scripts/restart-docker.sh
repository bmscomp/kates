#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

warn "Docker Desktop Restart Script"
echo ""
echo "This will restart Docker Desktop and optionally reconnect the cluster."
echo ""

step "Step 1: Quitting Docker Desktop..."
osascript -e 'quit app "Docker"' 2>/dev/null || true

info "Waiting for Docker to shut down..."
sleep 5

step "Cleaning up remaining Docker processes..."
pkill -f "Docker Desktop" 2>/dev/null || true
pkill -f "com.docker" 2>/dev/null || true
sleep 2

echo ""
step "Step 2: Starting Docker Desktop..."
open -a Docker

info "Waiting for Docker Desktop to start (up to 90s)..."
echo ""

for i in {1..45}; do
    if docker info >/dev/null 2>&1; then
        info "✓ Docker is ready!"
        docker version --format 'Docker version: {{.Server.Version}}'
        echo ""
        break
    fi
    printf "."
    sleep 2

    if [ $i -eq 45 ]; then
        echo ""
        error "✗ Docker did not start within 90 seconds"
        echo "Check Docker Desktop manually"
        exit 1
    fi
done

# Check if Kind cluster still exists and reconnect
if kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
    info "✓ Kind cluster '${KIND_CLUSTER_NAME}' found"

    step "Reconnecting registry to kind network..."
    docker network connect kind kind-registry 2>/dev/null || true

    step "Verifying cluster connectivity..."
    if kubectl cluster-info &>/dev/null; then
        info "✓ Cluster is reachable"
        echo ""
        echo "Services may need port-forwarding:"
        echo "  make ports"
    else
        warn "Cluster exists but is not reachable"
        echo "You may need to recreate: make all"
    fi
else
    warn "No Kind cluster found"
    echo "Run: make all"
fi
