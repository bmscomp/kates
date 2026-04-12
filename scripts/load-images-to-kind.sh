#!/bin/bash
# load-images-to-kind.sh
# Pulls all images directly into Kind cluster nodes via 'ctr pull'.
# No local Docker image pull required — images are fetched straight from
# the registry into each node's containerd.
# Usage: ./scripts/load-images-to-kind.sh [--platform PLATFORM]
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../images.env"

PLATFORM="${CTR_PLATFORM:-linux/arm64}"

require_cluster

bold "Loading Images into Kind Cluster"
info "Platform: ${PLATFORM}"
echo ""

TOTAL=0
SKIPPED=0
LOADED=0
FAILED=0

# Get all Kind node names
NODES=($(kind get nodes --name "${KIND_CLUSTER_NAME}" 2>/dev/null))
if [ ${#NODES[@]} -eq 0 ]; then
    error "No Kind nodes found for cluster '${KIND_CLUSTER_NAME}'"
    exit 1
fi
info "Nodes: ${NODES[*]}"
echo ""

# Check if image already present on the control-plane node
_already_in_kind() {
    local image=$1
    local image_name="${image%%:*}"
    local image_tag="${image##*:}"
    docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>/dev/null \
        | grep "${image_name}" | grep -q "${image_tag}"
}

# Pull image into all Kind nodes via ctr
_ctr_pull() {
    local image=$1
    local plat_flag=$2   # e.g. "--platform linux/arm64" or ""
    local all_ok=true
    for node in "${NODES[@]}"; do
        if ! docker exec "${node}" ctr --namespace=k8s.io images pull \
            ${plat_flag} "${image}" 2>&1; then
            all_ok=false
        fi
    done
    $all_ok
}

load_image() {
    local image=$1
    TOTAL=$((TOTAL + 1))
    step "[${TOTAL}] ${image}"

    if _already_in_kind "${image}"; then
        warn "  already in Kind, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    echo "  pulling into Kind nodes..."
    if _ctr_pull "${image}" "--platform ${PLATFORM}"; then
        info "  ✓ loaded"
        LOADED=$((LOADED + 1))
    else
        error "  ✗ failed"
        FAILED=$((FAILED + 1))
    fi
}

# Scarf images: try scarf URL first, fall back to canonical and tag
load_scarf_image() {
    local entry=$1
    local scarf_src="${entry%%|*}"
    local canonical="${entry##*|}"
    TOTAL=$((TOTAL + 1))
    step "[${TOTAL}] ${scarf_src}"

    if _already_in_kind "${scarf_src}"; then
        warn "  already in Kind, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    echo "  pulling into Kind nodes (scarf.sh)..."
    if _ctr_pull "${scarf_src}" "--platform ${PLATFORM}"; then
        info "  ✓ loaded"
        LOADED=$((LOADED + 1))
    else
        echo "  → retrying via canonical: ${canonical}"
        if _ctr_pull "${canonical}" "--platform ${PLATFORM}"; then
            # Tag canonical → scarf URL on every node so pod image refs match
            for node in "${NODES[@]}"; do
                docker exec "${node}" ctr --namespace=k8s.io images tag \
                    "${canonical}" "${scarf_src}" 2>/dev/null || true
            done
            info "  ✓ loaded (via canonical)"
            LOADED=$((LOADED + 1))
        else
            error "  ✗ failed"
            FAILED=$((FAILED + 1))
        fi
    fi
}

# amd64-only images: no --platform flag
load_amd64_image() {
    local image=$1
    TOTAL=$((TOTAL + 1))
    step "[${TOTAL}] ${image}"

    if _already_in_kind "${image}"; then
        warn "  already in Kind, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    echo "  pulling into Kind nodes (amd64, no platform filter)..."
    if _ctr_pull "${image}" ""; then
        info "  ✓ loaded"
        LOADED=$((LOADED + 1))
    else
        error "  ✗ failed"
        FAILED=$((FAILED + 1))
    fi
}

# ── Standard images ────────────────────────────────────────────────────────────
info "--- Standard Images ---"
for img in "${ALL_STANDARD_IMAGES[@]}"; do
    load_image "${img}"
done

# ── Scarf portal images ────────────────────────────────────────────────────────
echo ""
info "--- LitmusChaos Portal Images (scarf.sh) ---"
for entry in "${LITMUS_SCARF_IMAGES[@]}"; do
    load_scarf_image "${entry}"
done

# ── amd64-only images (empty on arm64 Kind) ────────────────────────────────────
if [ ${#AMD64_ONLY_IMAGES[@]} -gt 0 ]; then
    echo ""
    info "--- amd64-only Images ---"
    for img in "${AMD64_ONLY_IMAGES[@]}"; do
        load_amd64_image "${img}"
    done
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
bold "✅ Done!"
echo ""
printf "  Total: %d  Loaded: %d  Skipped: %d  Failed: %d\n" \
    "${TOTAL}" "${LOADED}" "${SKIPPED}" "${FAILED}"
echo ""
echo "  Verify: docker exec -it ${KIND_CLUSTER_NAME}-control-plane crictl images"

[ "${FAILED}" -eq 0 ] || exit 1
