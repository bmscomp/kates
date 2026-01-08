#!/bin/bash
# Nuclear fix for k8s-sidecar manifest corruption
# This bypasses the corrupted manifest by using a clean image name

set -e

echo "🔧 Nuclear Fix for k8s-sidecar manifest corruption..."

# Step 1: Complete Docker cleanup
echo "Step 1: Aggressive Docker cleanup..."
docker rmi -f localhost:5001/quay.io/kiwigrid/k8s-sidecar:1.27.6 2>/dev/null || true
docker rmi -f quay.io/kiwigrid/k8s-sidecar:1.27.6 2>/dev/null || true
docker images | grep k8s-sidecar | awk '{print $3}' | xargs -r docker rmi -f 2>/dev/null || true
docker image prune -f >/dev/null 2>&1

echo "Step 2: Restarting Docker to clear manifest cache..."
echo "⚠️  MANUAL STEP REQUIRED: Please restart Docker Desktop now"
echo "   (Docker menu -> Quit Docker Desktop, then restart)"
echo ""
read -p "Press ENTER after you've restarted Docker Desktop..."

echo "Step 3: Pulling with a different tag to avoid cache..."
# Pull to a different tag first to avoid manifest cache
docker pull --platform linux/amd64 quay.io/kiwigrid/k8s-sidecar:1.27.6
docker tag quay.io/kiwigrid/k8s-sidecar:1.27.6 quay.io/kiwigrid/k8s-sidecar:fixed-amd64

# Verify
echo "Step 4: Verifying architecture..."
ARCH=$(docker image inspect quay.io/kiwigrid/k8s-sidecar:fixed-amd64 --format '{{.Architecture}} {{.Os}}')
echo "Image architecture: $ARCH"

if [[ "$ARCH" != "amd64 linux" ]]; then
    echo "❌ ERROR: Still not amd64 platform!"
    exit 1
fi

echo "Step 5: Loading into Kind with clean tag..."
kind load docker-image quay.io/kiwigrid/k8s-sidecar:fixed-amd64 --name panda

# Now tag it correctly in the cluster
echo "Step 6: Re-tagging in Kind nodes..."
for node in panda-control-plane panda-worker panda-worker2; do
    echo "  Tagging in $node..."
    docker exec $node ctr -n k8s.io images tag \
        docker.io/kiwigrid/k8s-sidecar:fixed-amd64 \
        quay.io/kiwigrid/k8s-sidecar:1.27.6 || true
done

# Also push to local registry with clean tag
echo "Step 7: Pushing to local registry..."
docker tag quay.io/kiwigrid/k8s-sidecar:fixed-amd64 localhost:5001/quay.io/kiwigrid/k8s-sidecar:1.27.6-amd64
docker push localhost:5001/quay.io/kiwigrid/k8s-sidecar:1.27.6-amd64

echo ""
echo "✅ DONE! The image is now loaded in Kind with the correct architecture."
echo ""
echo "Verification:"
docker exec panda-control-plane crictl images | grep k8s-sidecar
