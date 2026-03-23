#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../images.env"

PLATFORM="--platform linux/arm64"

require_cluster

bold "Loading Images into Kind Cluster"
echo ""

TOTAL=0
SKIPPED=0
LOADED=0
FAILED=0

load_image() {
    local image=$1
    TOTAL=$((TOTAL + 1))

    step "[${TOTAL}] ${image}"

    local image_name="${image%%:*}"
    local image_tag="${image##*:}"
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep "${image_name}" | grep -q "${image_tag}"; then
        warn "  already in Kind, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if ! docker image inspect "${image}" >/dev/null 2>&1; then
        echo "  pulling image..."
        if ! docker pull ${PLATFORM} "${image}"; then
            error "  ✗ failed to pull — check network or image name"
            FAILED=$((FAILED + 1))
            return 1
        fi
    fi

    echo "  loading into Kind..."
    kind load docker-image "${image}" --name "${KIND_CLUSTER_NAME}"
    info "  ✓ loaded"
    LOADED=$((LOADED + 1))
}

load_scarf_image() {
    local entry=$1
    local scarf_src="${entry%%|*}"
    local canonical="${entry##*|}"
    TOTAL=$((TOTAL + 1))

    step "[${TOTAL}] ${scarf_src}"

    local image_name="${scarf_src%%:*}"
    local image_tag="${scarf_src##*:}"
    if docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null | grep "${image_name}" | grep -q "${image_tag}"; then
        warn "  already in Kind, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if ! docker image inspect "${scarf_src}" >/dev/null 2>&1; then
        echo "  pulling from scarf.sh..."
        if ! docker pull ${PLATFORM} "${scarf_src}" 2>/dev/null; then
            echo "  trying canonical name..."
            if ! docker pull ${PLATFORM} "${canonical}" 2>/dev/null; then
                error "  ✗ failed to pull from both sources"
                FAILED=$((FAILED + 1))
                return 1
            fi
            docker tag "${canonical}" "${scarf_src}"
        fi
    fi

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
