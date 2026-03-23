#!/bin/bash
# No set -e: continue past individual pull failures and report at the end

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../images.env"

MAX_PARALLEL="${PULL_PARALLEL:-4}"
PLATFORM="--platform linux/arm64"

bold "Pulling Images"
echo ""

TOTAL=0
SKIPPED=0
PULLED=0
FAILED=0

pull_image() {
    local image=$1
    TOTAL=$((TOTAL + 1))

    step "[${TOTAL}] ${image}"

    if docker image inspect "${image}" >/dev/null 2>&1; then
        warn "  already pulled, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if docker pull ${PLATFORM} "${image}"; then
        info "  ✓ pulled"
        PULLED=$((PULLED + 1))
    else
        error "  ✗ failed to pull"
        FAILED=$((FAILED + 1))
        return 1
    fi
}

pull_scarf_image() {
    local entry=$1
    local scarf_src="${entry%%|*}"
    local canonical="${entry##*|}"
    TOTAL=$((TOTAL + 1))

    step "[${TOTAL}] ${scarf_src}"

    if docker image inspect "${scarf_src}" >/dev/null 2>&1; then
        warn "  already pulled, skipping"
        SKIPPED=$((SKIPPED + 1))
        return 0
    fi

    if docker pull ${PLATFORM} "${scarf_src}"; then
        info "  ✓ pulled"
        PULLED=$((PULLED + 1))
    else
        echo "  trying canonical name: ${canonical}..."
        if docker pull ${PLATFORM} "${canonical}"; then
            docker tag "${canonical}" "${scarf_src}"
            info "  ✓ pulled (via canonical)"
            PULLED=$((PULLED + 1))
        else
            error "  ✗ failed to pull"
            FAILED=$((FAILED + 1))
            return 1
        fi
    fi
}

pull_single() {
    local image=$1
    if docker image inspect "${image}" >/dev/null 2>&1; then
        echo "SKIP ${image}"
        return 0
    fi
    if docker pull ${PLATFORM} "${image}" >/dev/null 2>&1; then
        echo "OK   ${image}"
    else
        echo "FAIL ${image}"
        return 1
    fi
}
export -f pull_single
export PLATFORM

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
    pull_scarf_image "${entry}" || true
done

echo ""
bold "✅ Pull Complete!"
echo ""
echo "Total: ${TOTAL}  Pulled: ${PULLED}  Skipped: ${SKIPPED}  Failed: ${FAILED}"
echo ""
echo "Verify local images:"
echo "  docker images | head -30"

if [ "${FAILED}" -gt 0 ]; then
    warn "Re-run this script to retry the ${FAILED} failed image(s)."
    exit 1
fi
