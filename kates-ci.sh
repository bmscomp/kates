#!/usr/bin/env bash
set -euo pipefail

KATES_URL="${KATES_URL:-http://localhost:8080}"
SCENARIO_FILE="${1:-scenarios/ci-gate.yaml}"
EXIT_CODE=0

echo "╭──────────────────────────────────╮"
echo "│   Kates — CI Performance Gate    │"
echo "╰──────────────────────────────────╯"
echo ""
echo "API:      ${KATES_URL}"
echo "Scenario: ${SCENARIO_FILE}"
echo ""

if ! command -v kates &>/dev/null; then
  echo "ERROR: kates CLI not found in PATH"
  echo "Install: go install github.com/klster/kates-cli@latest"
  exit 1
fi

if [ ! -f "${SCENARIO_FILE}" ]; then
  echo "ERROR: Scenario file not found: ${SCENARIO_FILE}"
  echo "Run 'kates init' to generate default scenarios"
  exit 1
fi

echo "Running performance gate..."
echo ""

kates test apply \
  --url "${KATES_URL}" \
  -f "${SCENARIO_FILE}" \
  --wait

EXIT_CODE=$?

if [ ${EXIT_CODE} -eq 0 ]; then
  echo ""
  echo "✓ Performance gate passed"
else
  echo ""
  echo "✖ Performance gate FAILED (exit code ${EXIT_CODE})"
  echo "  Review: kates test list --status FAILED --url ${KATES_URL}"
fi

exit ${EXIT_CODE}
