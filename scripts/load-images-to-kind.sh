#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../images.env"

require_cluster
require_registry

bold "Loading Images into Kind Cluster"
echo ""

TOTAL=0
SKIPPED=0
LOADED=0
FAILED=0

load_image() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    TOTAL=$((TOTAL + 1))

    step "[${TOTAL}] ${image}"

    local image_name="${image%%:*}"
    local image_tag="${image##*:}"
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep "${image_name}" | grep -q "${image_tag}"; then
        warn "  already in Kind, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    echo "  pulling from local registry..."
    if ! docker pull ${PLATFORM} "${local_image}"; then
        error "  ✗ NOT in local registry — run ./pull-images.sh first"
        FAILED=$((FAILED + 1))
        return 1
    fi

    docker tag "${local_image}" "${image}"

    echo "  loading into Kind..."
    kind load docker-image "${image}" --name "${KIND_CLUSTER_NAME}"
    info "  ✓ loaded"
    LOADED=$((LOADED + 1))
}

load_scarf_image() {
    local entry=$1
    local scarf_src="${entry%%|*}"
    local canonical="${entry##*|}"
    local registry_image="${REGISTRY}/${canonical}"
    TOTAL=$((TOTAL + 1))

    step "[${TOTAL}] ${scarf_src}"

    local image_name="${scarf_src%%:*}"
    local image_tag="${scarf_src##*:}"
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep "${image_name}" | grep -q "${image_tag}"; then
        warn "  already in Kind, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    echo "  pulling canonical name from local registry..."
    if ! docker pull ${PLATFORM} "${registry_image}" 2>/dev/null; then
        error "  ✗ NOT in local registry — run ./pull-images.sh first"
        FAILED=$((FAILED + 1))
        return 1
    fi

    docker tag "${registry_image}" "${scarf_src}"

    echo "  loading into Kind..."
    kind load docker-image "${scarf_src}" --name "${KIND_CLUSTER_NAME}"
    info "  ✓ loaded"
    LOADED=$((LOADED + 1))
}

info "--- Standard Images ---"
for img in "${ALL_STANDARD_IMAGES[@]}"; do
    load_image "${img}"
done

echo ""
info "--- LitmusChaos Portal Images (scarf.sh) ---"
for entry in "${LITMUS_SCARF_IMAGES[@]}"; do
    load_scarf_image "${entry}"
done

echo ""
bold "✅ Image Loading Complete!"
echo ""
echo "Total: ${TOTAL}  Loaded: ${LOADED}  Skipped: ${SKIPPED}  Failed: ${FAILED}"
echo ""
echo "Verify: docker exec -it ${KIND_CLUSTER_NAME}-control-plane crictl images"
