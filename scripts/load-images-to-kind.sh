#!/bin/bash
set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/../images.env"

REGISTRY="localhost:5001"
KIND_CLUSTER_NAME="panda"

ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then
    PLATFORM="--platform linux/arm64"
else
    PLATFORM="--platform linux/amd64"
fi

unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Loading Images into Kind Cluster${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

if ! kind get clusters | grep -q "^${KIND_CLUSTER_NAME}$"; then
    echo -e "${RED}Error: Kind cluster '${KIND_CLUSTER_NAME}' not found${NC}"
    echo "Please run 'make cluster' or './start-cluster.sh' first"
    exit 1
fi

if ! curl -s http://${REGISTRY}/v2/_catalog > /dev/null 2>&1; then
    echo -e "${RED}Error: Local registry is not running at ${REGISTRY}${NC}"
    echo "Please run './setup-registry.sh' first"
    exit 1
fi

TOTAL=0
SKIPPED=0
LOADED=0
FAILED=0

load_image() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    TOTAL=$((TOTAL + 1))

    echo -e "${BLUE}[${TOTAL}] ${image}${NC}"

    local image_name="${image%%:*}"
    local image_tag="${image##*:}"
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep "${image_name}" | grep -q "${image_tag}"; then
        echo -e "  ${YELLOW}already in Kind, skipping${NC}"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    echo "  pulling from local registry..."
    if ! docker pull ${PLATFORM} "${local_image}"; then
        echo -e "  ${RED}✗ NOT in local registry — run ./pull-images.sh first${NC}"
        FAILED=$((FAILED + 1))
        return 1
    fi

    docker tag "${local_image}" "${image}"

    echo "  loading into Kind..."
    kind load docker-image "${image}" --name "${KIND_CLUSTER_NAME}"
    echo -e "  ${GREEN}✓ loaded${NC}"
    LOADED=$((LOADED + 1))
}

load_scarf_image() {
    local entry=$1
    local scarf_src="${entry%%|*}"
    local canonical="${entry##*|}"
    local registry_image="${REGISTRY}/${canonical}"
    TOTAL=$((TOTAL + 1))

    echo -e "${BLUE}[${TOTAL}] ${scarf_src}${NC}"

    local image_name="${scarf_src%%:*}"
    local image_tag="${scarf_src##*:}"
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep "${image_name}" | grep -q "${image_tag}"; then
        echo -e "  ${YELLOW}already in Kind, skipping${NC}"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    echo "  pulling canonical name from local registry..."
    if ! docker pull ${PLATFORM} "${registry_image}" 2>/dev/null; then
        echo -e "  ${RED}✗ NOT in local registry — run ./pull-images.sh first${NC}"
        FAILED=$((FAILED + 1))
        return 1
    fi

    docker tag "${registry_image}" "${scarf_src}"

    echo "  loading into Kind..."
    kind load docker-image "${scarf_src}" --name "${KIND_CLUSTER_NAME}"
    echo -e "  ${GREEN}✓ loaded${NC}"
    LOADED=$((LOADED + 1))
}

echo -e "${GREEN}--- Standard Images ---${NC}"
for img in "${ALL_STANDARD_IMAGES[@]}"; do
    load_image "${img}"
done

echo ""
echo -e "${GREEN}--- LitmusChaos Portal Images (scarf.sh) ---${NC}"
for entry in "${LITMUS_SCARF_IMAGES[@]}"; do
    load_scarf_image "${entry}"
done

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Image Loading Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Total: ${TOTAL}  Loaded: ${LOADED}  Skipped: ${SKIPPED}  Failed: ${FAILED}"
echo ""
echo "Verify images in cluster:"
echo "  docker exec -it ${KIND_CLUSTER_NAME}-control-plane crictl images"
echo ""
echo "Next steps:"
echo "  - Deploy monitoring: make monitoring"
echo "  - Deploy full stack: make deploy-all"
