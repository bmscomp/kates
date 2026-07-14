#!/usr/bin/env bash
# Keep the book's Version & Compatibility Matrix in sync with the repo pins.
#
# Usage:
#   scripts/gen-version-matrix.sh           Print the matrix generated from
#                                           versions.env, kates/pom.xml,
#                                           cli/go.mod, and charts/*/Chart.yaml.
#   scripts/gen-version-matrix.sh --check   Verify the table between the
#                                           <!-- version-matrix:start/end -->
#                                           markers in
#                                           docs/book/appendix-d-versions.md
#                                           matches the generated one. Exits
#                                           non-zero on drift (used by CI).
set -euo pipefail

cd "$(dirname "$0")/.."
APPENDIX=docs/book/appendix-d-versions.md

env_val() { grep -E "^$1=" versions.env | head -1 | sed -E 's/^[^=]+="?//; s/"$//'; }
pom_val() { grep -oE "<$1>[^<]+</$1>" kates/pom.xml | head -1 | sed -E "s/<\/?$1>//g"; }
chart_field() { grep -E "^$2:" "charts/$1/Chart.yaml" | head -1 | sed -E "s/^$2:[[:space:]]*//; s/^[\"']//; s/[\"']$//"; }

print_table() {
  local strimzi_kafka kafka
  strimzi_kafka=$(env_val STRIMZI_KAFKA_VERSION)
  kafka=${strimzi_kafka##*-kafka-}
  echo "| Component | Version | Source |"
  echo "|:----------|:--------|:-------|"
  echo "| Apache Kafka | $kafka | \`versions.env\` (\`STRIMZI_KAFKA_VERSION\`) |"
  echo "| Strimzi operator | $(env_val STRIMZI_VERSION) | \`versions.env\` |"
  echo "| LitmusChaos | $(env_val LITMUS_VERSION) | \`versions.env\` |"
  echo "| Prometheus stack chart | $(env_val PROMETHEUS_STACK_VERSION) | \`versions.env\` |"
  echo "| Jaeger chart | $(env_val JAEGER_CHART_VERSION) | \`versions.env\` |"
  echo "| cert-manager | $(env_val CERT_MANAGER_VERSION) | \`versions.env\` |"
  echo "| Java (backend) | $(pom_val maven.compiler.release) | \`kates/pom.xml\` |"
  echo "| Quarkus | $(pom_val quarkus.platform.version) | \`kates/pom.xml\` |"
  echo "| Go (CLI) | $(grep -E '^go ' cli/go.mod | awk '{print $2}') | \`cli/go.mod\` |"
  local dir name
  for dir in charts/*/; do
    name=$(basename "$dir")
    echo "| \`$name\` chart | $(chart_field "$name" version) (app $(chart_field "$name" appVersion)) | \`charts/$name/Chart.yaml\` |"
  done
}

check_table() {
  local expected actual
  expected=$(print_table)
  actual=$(sed -n '/<!-- version-matrix:start -->/,/<!-- version-matrix:end -->/p' "$APPENDIX" | sed '1d;$d')
  if [[ "$expected" != "$actual" ]]; then
    echo "STALE: $APPENDIX version matrix differs from the generated one:" >&2
    diff <(echo "$actual") <(echo "$expected") >&2 || true
    echo "Regenerate with: scripts/gen-version-matrix.sh (paste between the markers)" >&2
    return 1
  fi
  echo "OK: $APPENDIX version matrix is in sync with the repo pins"
}

case "${1:-}" in
  --check) check_table ;;
  "")      print_table ;;
  *)       echo "Usage: $0 [--check]" >&2; exit 2 ;;
esac
