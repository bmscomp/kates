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

# Clean up existing images
echo "Removing existing k8s-sidecar images..."
docker rmi localhost:5001/quay.io/kiwigrid/k8s-sidecar:1.27.6 2>/dev/null || true
docker rmi quay.io/kiwigrid/k8s-sidecar:1.27.6 2>/dev/null || true

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
