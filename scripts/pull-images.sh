#!/bin/bash
# No set -e: continue past individual pull failures and report at the end

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../images.env"

MAX_PARALLEL="${PULL_PARALLEL:-4}"

require_registry

bold "Pulling Images to Local Registry"
echo ""

TOTAL=0
SKIPPED=0
PULLED=0
FAILED=0

push_to_local_registry() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    TOTAL=$((TOTAL + 1))

    step "[${TOTAL}] ${image}"

    if curl -s "http://${REGISTRY}/v2/${image%:*}/tags/list" | grep -q "\"${image##*:}\""; then
        warn "  already in registry, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if docker pull ${PLATFORM} "${image}"; then
        docker tag "${image}" "${local_image}"
        docker push "${local_image}"
        info "  ✓ pushed"
        PULLED=$((PULLED + 1))
    else
        error "  ✗ failed to pull"
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

    step "[${TOTAL}] ${scarf_src} → ${canonical}"

    if curl -s "http://${REGISTRY}/v2/${canonical%:*}/tags/list" | grep -q "\"${canonical##*:}\""; then
        warn "  already in registry, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if docker pull ${PLATFORM} "${scarf_src}"; then
        docker tag "${scarf_src}" "${local_image}"
        docker push "${local_image}"
        info "  ✓ pushed as ${canonical}"
        PULLED=$((PULLED + 1))
    else
        error "  ✗ failed to pull"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

# Pull standard images with parallelism for images not yet in registry
pull_single() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    if curl -s "http://${REGISTRY}/v2/${image%:*}/tags/list" | grep -q "\"${image##*:}\"" 2>/dev/null; then
        echo "SKIP ${image}"
        return 0
    fi
    if docker pull ${PLATFORM} "${image}" >/dev/null 2>&1; then
        docker tag "${image}" "${local_image}" 2>/dev/null
        docker push "${local_image}" >/dev/null 2>&1
        echo "OK   ${image}"
    else
        echo "FAIL ${image}"
        return 1
    fi
}
export -f pull_single
export REGISTRY PLATFORM

info "--- Standard Images (up to ${MAX_PARALLEL} in parallel) ---"
TMPFILE=$(mktemp)
printf '%s\n' "${ALL_STANDARD_IMAGES[@]}" | xargs -P "${MAX_PARALLEL}" -I{} bash -c 'pull_single "$@"' _ {} 2>&1 | tee "${TMPFILE}"
PULLED=$(grep -c "^OK " "${TMPFILE}" || true)
SKIPPED=$(grep -c "^SKIP " "${TMPFILE}" || true)
FAILED=$(grep -c "^FAIL " "${TMPFILE}" || true)
TOTAL=$((PULLED + SKIPPED + FAILED))
rm -f "${TMPFILE}"

echo ""
info "--- LitmusChaos Portal Images (scarf.sh) ---"
for entry in "${LITMUS_SCARF_IMAGES[@]}"; do
    push_scarf_image "${entry}" || true
done

echo ""
bold "Pull Complete!"
echo ""
echo "Total: ${TOTAL}  Pulled: ${PULLED}  Skipped: ${SKIPPED}  Failed: ${FAILED}"
echo ""
echo "Registry contents:"
curl -s http://${REGISTRY}/v2/_catalog | jq '.repositories | length' 2>/dev/null || echo "(jq not installed)"

if [ "${FAILED}" -gt 0 ]; then
    warn "Re-run this script to retry the ${FAILED} failed image(s)."
    exit 1
fi
