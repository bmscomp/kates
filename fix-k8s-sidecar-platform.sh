#!/bin/bash
# Quick fix script to correct platform architecture for k8s-sidecar image
# Run this on your corporate machine to fix the mismatched image issue

set -e

echo "🔧 Fixing k8s-sidecar image platform architecture..."

# Detect architecture
ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then
    echo "✓ Apple Silicon detected - pulling amd64 version for Kind"
    PLATFORM="--platform linux/amd64"
else
    echo "✓ AMD64 system - pulling native version"
    PLATFORM=""
fi

# Clean up existing images and manifests aggressively
echo "Removing ALL k8s-sidecar images and references..."
docker rmi -f localhost:5001/quay.io/kiwigrid/k8s-sidecar:1.27.6 2>/dev/null || true
docker rmi -f quay.io/kiwigrid/k8s-sidecar:1.27.6 2>/dev/null || true

# Remove any dangling/untagged images related to k8s-sidecar
echo "Cleaning up dangling images..."
docker images -f "dangling=true" -q --filter=reference='*kiwigrid/k8s-sidecar*' | xargs -r docker rmi -f 2>/dev/null || true
docker images -q quay.io/kiwigrid/k8s-sidecar | xargs -r docker rmi -f 2>/dev/null || true

# Prune to remove any leftover manifests
echo "Pruning Docker system..."
docker image prune -f >/dev/null 2>&1 || true

echo "Cleanup complete. Starting fresh pull..."

# Pull correct platform
echo "Pulling linux/amd64 version..."
docker pull ${PLATFORM} quay.io/kiwigrid/k8s-sidecar:1.27.6

# Verify architecture
echo "Verifying image architecture..."
docker image inspect quay.io/kiwigrid/k8s-sidecar:1.27.6 --format '{{.Architecture}} {{.Os}}'

# Tag and push to local registry
echo "Pushing to local registry..."
docker tag quay.io/kiwigrid/k8s-sidecar:1.27.6 localhost:5001/quay.io/kiwigrid/k8s-sidecar:1.27.6
docker push localhost:5001/quay.io/kiwigrid/k8s-sidecar:1.27.6

# Load into Kind
echo "Loading into Kind cluster..."
kind load docker-image quay.io/kiwigrid/k8s-sidecar:1.27.6 --name panda

echo "✅ Done! k8s-sidecar image is now the correct architecture (amd64)"
