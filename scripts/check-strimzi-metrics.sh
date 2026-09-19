#!/usr/bin/env bash
# Are the JMX exporter rules vendored in charts/kafka-cluster/files/metrics
# still Strimzi's own, for the Strimzi version this repository pins?
#
# kafka-cluster 1.0 ships Strimzi's example rule sets unchanged, because the
# operator's Grafana dashboards (enabled through charts/strimzi-operator) read
# exactly the series those rules produce. A hand edit, or a Strimzi bump that
# changes the upstream rules, would quietly break that pairing; this compares
# each vendored file (below its header) with the upstream example.
#
# Usage: scripts/check-strimzi-metrics.sh [--update]
#   --update  rewrite the vendored files from upstream (header refreshed)
#
# Needs curl and python3 with PyYAML, and network access to
# raw.githubusercontent.com. Run by .github/workflows/ci-kafka-charts.yml.
set -euo pipefail

cd "$(dirname "$0")/.."

UPDATE=""
case "${1:-}" in
  --update) UPDATE=1 ;;
  "") ;;
  *) sed -n '/^# Usage:/,/^#$/p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
esac

VERSION=$(grep -E '^STRIMZI_VERSION=' versions.env | sed -E 's/^[^=]+="?([^"]*)"?.*/\1/')
[ -n "$VERSION" ] || { echo "STRIMZI_VERSION not found in versions.env" >&2; exit 2; }
BASE="https://raw.githubusercontent.com/strimzi/strimzi-kafka-operator/${VERSION}/packaging/examples/metrics"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

rc=0
# vendored file | upstream example | ConfigMap name | data key
while IFS='|' read -r file example cm key; do
  curl -fsSL "$BASE/$example" -o "$WORK/$example"
  python3 - "$file" "$WORK/$example" "$cm" "$key" "$VERSION" "$example" "$UPDATE" <<'PY' || rc=1
import sys, yaml
path, upstream, cm, key, version, example, update = sys.argv[1:]
body = None
for doc in yaml.safe_load_all(open(upstream)):
    if doc and doc.get("kind") == "ConfigMap" and doc["metadata"]["name"] == cm:
        body = doc["data"][key]
if body is None:
    sys.exit("%s: no ConfigMap %s with key %s in upstream %s" % (path, cm, key, example))
lines = open(path).read().splitlines(keepends=True)
end = next(i for i, l in enumerate(lines) if l.startswith("# every series")) + 1
header, vendored = "".join(lines[:end]), "".join(lines[end:])
if update:
    old = header.splitlines(keepends=True)
    old[0] = "# VENDORED — do not edit. Strimzi %s packaging/examples/metrics/%s\n" % (version, example)
    open(path, "w").write("".join(old) + body)
    print("UPDATED: %s from Strimzi %s" % (path, version))
elif vendored != body:
    import difflib
    print("DRIFT: %s differs from Strimzi %s %s (run with --update):" % (path, version, example))
    sys.stdout.writelines(difflib.unified_diff(body.splitlines(True), vendored.splitlines(True), "upstream", path))
    sys.exit(1)
elif ("Strimzi %s " % version) not in header.splitlines()[0]:
    print("STALE HEADER: %s names another Strimzi version than %s (run with --update)" % (path, version))
    sys.exit(1)
else:
    print("OK: %s is Strimzi %s's %s" % (path, version, example))
PY
done <<'EOF'
charts/kafka-cluster/files/metrics/kafka-metrics.yaml|kafka-metrics.yaml|kafka-metrics|kafka-metrics-config.yml
charts/kafka-cluster/files/metrics/cruise-control-metrics.yaml|kafka-cruise-control-metrics.yaml|cruise-control-metrics|metrics-config.yml
EOF
exit $rc
