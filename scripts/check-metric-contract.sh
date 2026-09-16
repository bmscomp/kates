#!/usr/bin/env bash
# Can every series a chart's alerts, recording rules and dashboard read be
# produced by that chart's JMX exporter rules?
#
# Until this existed, the answer was assumed. A PrometheusRule installs
# whether or not the series in its `expr` will ever exist, and a Grafana panel
# renders "No data" for a typo exactly as it does for an idle cluster — so an
# alert on a name no rule produces is not a failed check, it is an alert that
# never fires and a runbook that says it is covered. The Kafka chart shipped
# three of those; this is the gate that would have caught them.
#
# The contract is scripts/metric-contract/<chart>.yaml: which renders to read
# the references from, and a catalogue of the MBeans the workload registers.
# contract.py simulates the exporter over the catalogue (first matching rule
# wins, $N substitution, the unsafe-character rewrite, lowercaseOutputName) and
# compares the names that come out with every name the PromQL reads. Static;
# no cluster; seconds.
#
# With --scrape FILE a real /metrics capture is checked too: every reference
# must be in it, and the catalogue is diffed against it both ways so a Kafka
# upgrade that renames an attribute shows up as a NOTE before it shows up as
# an empty panel. ci-mirror-maker2.yml's live job produces that capture.
#
# Usage: scripts/check-metric-contract.sh <chart> [--scrape FILE] [--quiet]
#        scripts/check-metric-contract.sh --list
#
# Needs helm and python3 with PyYAML. Also run in CI (ci-mirror-maker2.yml,
# Chart validation) and by `make check-metric-contract`.
set -euo pipefail

cd "$(dirname "$0")/.."

CONTRACTS=scripts/metric-contract

usage() {
  sed -n '/^# Usage:/,/^#$/p' "$0" | sed 's/^# \{0,1\}//'
  exit 2
}

CHART="" SCRAPE="" QUIET=""
while [ $# -gt 0 ]; do
  case "$1" in
    --list)  for f in "$CONTRACTS"/*.yaml; do basename "$f" .yaml; done; exit 0 ;;
    --scrape) SCRAPE="${2:?--scrape needs a file}"; shift 2 ;;
    --quiet) QUIET="--quiet"; shift ;;
    -h|--help) usage ;;
    -*) echo "unknown option: $1" >&2; usage ;;
    *) [ -z "$CHART" ] || usage; CHART="$1"; shift ;;
  esac
done
[ -n "$CHART" ] || usage

CONTRACT="$CONTRACTS/$CHART.yaml"
if [ ! -f "$CONTRACT" ]; then
  echo "no contract for '$CHART' — expected $CONTRACT (--list shows the charts that have one)" >&2
  exit 2
fi
if [ -n "$SCRAPE" ] && [ ! -s "$SCRAPE" ]; then
  echo "--scrape: $SCRAPE is missing or empty" >&2
  exit 2
fi
command -v helm >/dev/null 2>&1 || { echo "helm is required" >&2; exit 2; }
python3 -c 'import yaml' 2>/dev/null || { echo "python3 with PyYAML is required (pip install pyyaml)" >&2; exit 2; }

CHART_DIR=$(python3 -c 'import sys,yaml; print(yaml.safe_load(open(sys.argv[1]))["chart"])' "$CONTRACT")
[ -d "$CHART_DIR" ] || { echo "$CONTRACT names $CHART_DIR, which does not exist" >&2; exit 2; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# One render per entry in the contract's `renders`, each with the contract's
# global `set` plus its own values files (relative to the chart) and sets.
# Rendering is done here rather than in Python so the helm invocation is the
# same one the workflow's other steps use and the same one a contributor
# would paste to reproduce a failure.
RENDERS=()
while IFS='|' read -r name values sets; do
  args=()
  for f in $values; do args+=(-f "$CHART_DIR/$f"); done
  for s in $sets; do args+=(--set "$s"); done
  out="$WORK/$(echo "$name" | tr -c 'A-Za-z0-9_.-' '_').yaml"
  if ! helm template contract "$CHART_DIR" -n contract \
        --api-versions monitoring.coreos.com/v1 "${args[@]}" > "$out" 2> "$WORK/helm.err"; then
    echo "helm template failed for render '$name':" >&2
    cat "$WORK/helm.err" >&2
    exit 1
  fi
  RENDERS+=("$name=$out")
done < <(python3 - "$CONTRACT" <<'PY'
import sys, yaml
c = yaml.safe_load(open(sys.argv[1]))
common = c.get("set") or []
for r in c.get("renders") or [{"name": "base"}]:
    print("|".join([r["name"], " ".join(r.get("values") or []), " ".join(common + (r.get("set") or []))]))
PY
)

ARGS=()
[ -n "$SCRAPE" ] && ARGS+=(--scrape "$SCRAPE")
[ -n "$QUIET" ] && ARGS+=("$QUIET")
python3 "$CONTRACTS/contract.py" "$CONTRACT" "${RENDERS[@]}" "${ARGS[@]}"
