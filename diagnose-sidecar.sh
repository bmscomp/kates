#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m' # No Color

IMAGE="quay.io/kiwigrid/k8s-sidecar:2.2.3"
REGISTRY="localhost:5001"
LOCAL_IMAGE="${REGISTRY}/${IMAGE}"
CLUSTER_NAME="panda"

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}K8s-Sidecar Image Diagnostics${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""

# Step 1: Check host architecture
echo -e "${GREEN}Step 1: Host Architecture${NC}"
echo "Host arch: $(uname -m)"
echo "Host platform: $(uname -s)/$(uname -m)"
echo ""

# Step 2: Check kind node architecture
echo -e "${GREEN}Step 2: Kind Node Architecture${NC}"
for node in ${CLUSTER_NAME}-control-plane ${CLUSTER_NAME}-worker ${CLUSTER_NAME}-worker2; do
    arch=$(docker exec "$node" uname -m 2>/dev/null || echo "N/A")
    echo "  $node: $arch"
done
echo ""

# Step 3: Check if image exists in local Docker
echo -e "${GREEN}Step 3: Local Docker Image Info${NC}"
if docker image inspect "$IMAGE" &>/dev/null; then
    echo -e "${YELLOW}Image exists in local Docker${NC}"
    docker image inspect "$IMAGE" --format '
Image: {{.RepoTags}}
ID: {{.Id}}
Architecture: {{.Architecture}}
OS: {{.Os}}
Size: {{.Size}} bytes
Layers: {{len .RootFS.Layers}}
'
else
    echo -e "${RED}Image not in local Docker, pulling...${NC}"
    docker pull "$IMAGE"
    docker image inspect "$IMAGE" --format '
Image: {{.RepoTags}}
ID: {{.Id}}
Architecture: {{.Architecture}}
OS: {{.Os}}
Size: {{.Size}} bytes
'
fi
echo ""

# Step 4: Check image manifest
echo -e "${GREEN}Step 4: Image Manifest (Platform Info)${NC}"
docker manifest inspect "$IMAGE" 2>/dev/null | jq -r '.manifests[] | "Platform: \(.platform.os)/\(.platform.architecture)"' || echo "Cannot inspect manifest"
echo ""

# Step 5: Check if image is in local registry
echo -e "${GREEN}Step 5: Local Registry Check${NC}"
if curl -s "http://${REGISTRY}/v2/${IMAGE%:*}/tags/list" | grep -q '"2.2.3"'; then
    echo -e "${YELLOW}Image exists in registry: ${LOCAL_IMAGE}${NC}"
    # Check what's in the registry
    curl -s "http://${REGISTRY}/v2/${IMAGE%:*}/tags/list" | jq
else
    echo -e "${RED}Image NOT in registry${NC}"
fi
echo ""

# Step 6: Pull specific platform
echo -e "${GREEN}Step 6: Pulling linux/arm64 Image${NC}"
echo "Pulling for native platform (arm64)..."
docker pull --platform linux/arm64 "$IMAGE"
echo ""

# Step 7: Tag for registry
echo -e "${GREEN}Step 7: Tagging for Local Registry${NC}"
docker tag "$IMAGE" "$LOCAL_IMAGE"
echo "Tagged: $LOCAL_IMAGE"
echo ""

# Step 8: Check image layers
echo -e "${GREEN}Step 8: Image Layer Details${NC}"
docker image inspect "$IMAGE" --format '{{range .RootFS.Layers}}{{println .}}{{end}}' | nl
echo ""

# Step 9: Attempt to load into kind
echo -e "${GREEN}Step 9: Loading into Kind Cluster${NC}"
echo "Attempting to load $IMAGE into kind..."
echo ""

# Try with verbose output
if kind load docker-image "$IMAGE" --name "$CLUSTER_NAME" 2>&1 | tee /tmp/kind-load-debug.log; then
    echo -e "${GREEN}✓ Successfully loaded!${NC}"
else
    echo -e "${RED}✗ Failed to load${NC}"
    echo ""
    echo -e "${YELLOW}Error details:${NC}"
    cat /tmp/kind-load-debug.log
    echo ""
    
    echo -e "${YELLOW}Attempting alternative: Save and import${NC}"
    # Try alternative method
    docker save "$IMAGE" | docker exec -i ${CLUSTER_NAME}-control-plane ctr --namespace=k8s.io images import --all-platforms - 2>&1 || echo "Alternative method also failed"
fi
echo ""

# Step 10: Check what's in kind nodes
echo -e "${GREEN}Step 10: Images in Kind Nodes${NC}"
for node in ${CLUSTER_NAME}-control-plane ${CLUSTER_NAME}-worker ${CLUSTER_NAME}-worker2; do
    echo -e "${BLUE}Node: $node${NC}"
    docker exec "$node" crictl images | grep k8s-sidecar || echo "  No k8s-sidecar image found"
done
echo ""

# Step 11: Recommendations
echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}Recommendations${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo "1. Image architecture should match kind nodes (aarch64/arm64)"
echo "2. If 'mismatched rootfs' error occurs:"
echo "   - Try pulling with explicit --platform linux/arm64"
echo "   - Remove and re-pull the image"
echo "   - Check if multi-arch manifest is corrupted"
echo ""
echo "3. Since k8s-sidecar is disabled in monitoring.yaml,"
echo "   you can skip loading this image entirely!"
echo ""
