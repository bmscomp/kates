#!/bin/bash
# No set -e: continue past individual pull failures and report at the end

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/images.env"

REGISTRY="localhost:5001"

ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then
    echo -e "${BLUE}Apple Silicon detected — using linux/arm64${NC}"
    PLATFORM="--platform linux/arm64"
else
    PLATFORM="--platform linux/amd64"
fi

unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Pulling Images to Local Registry${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

if ! curl -s http://${REGISTRY}/v2/_catalog > /dev/null 2>&1; then
    echo -e "${RED}Local registry is not running. Please run ./setup-registry.sh first${NC}"
    exit 1
fi

TOTAL=0
SKIPPED=0
PULLED=0
FAILED=0

push_to_local_registry() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    TOTAL=$((TOTAL + 1))

    echo -e "${BLUE}[${TOTAL}] ${image}${NC}"

    if curl -s "http://${REGISTRY}/v2/${image%:*}/tags/list" | grep -q "\"${image##*:}\""; then
        echo -e "  ${YELLOW}already in registry, skipping${NC}"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if docker pull ${PLATFORM} "${image}"; then
        docker tag "${image}" "${local_image}"
        docker push "${local_image}"
        echo -e "  ${GREEN}✓ pushed${NC}"
        PULLED=$((PULLED + 1))
    else
        echo -e "  ${RED}✗ failed to pull${NC}"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

push_scarf_image() {
    local entry=$1
    local scarf_src="${entry%%|*}"
    local canonical="${entry##*|}"
    local local_image="${REGISTRY}/${canonical}"
    TOTAL=$((TOTAL + 1))

    echo -e "${BLUE}[${TOTAL}] ${scarf_src} → ${canonical}${NC}"

    if curl -s "http://${REGISTRY}/v2/${canonical%:*}/tags/list" | grep -q "\"${canonical##*:}\""; then
        echo -e "  ${YELLOW}already in registry, skipping${NC}"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if docker pull ${PLATFORM} "${scarf_src}"; then
        docker tag "${scarf_src}" "${local_image}"
        docker push "${local_image}"
        echo -e "  ${GREEN}✓ pushed as ${canonical}${NC}"
        PULLED=$((PULLED + 1))
    else
        echo -e "  ${RED}✗ failed to pull${NC}"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

echo -e "${GREEN}--- Standard Images ---${NC}"
for img in "${ALL_STANDARD_IMAGES[@]}"; do
    push_to_local_registry "${img}" || true
done

echo ""
echo -e "${GREEN}--- LitmusChaos Portal Images (scarf.sh) ---${NC}"
for entry in "${LITMUS_SCARF_IMAGES[@]}"; do
    push_scarf_image "${entry}" || true
done

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Pull Complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Total: ${TOTAL}  Pulled: ${PULLED}  Skipped: ${SKIPPED}  Failed: ${FAILED}"
echo ""
echo "Registry contents:"
curl -s http://${REGISTRY}/v2/_catalog | jq '.repositories | length' 2>/dev/null || echo "(jq not installed)"

if [ "${FAILED}" -gt 0 ]; then
    echo -e "${YELLOW}Re-run this script to retry the ${FAILED} failed image(s).${NC}"
    exit 1
fi
