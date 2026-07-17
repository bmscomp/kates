#!/usr/bin/env bash
# Assert the Strimzi version pins agree across every place that declares one.
#
# WHY THIS EXISTS: charts/strimzi-operator pins the Strimzi version in THREE
# places, versions.env declares a fourth, and charts/kafka-cluster a fifth:
#
#   1. Chart.yaml  dependencies[strimzi-kafka-operator].version  → which operator chart is pulled
#   2. Chart.yaml  appVersion                                    → what the chart claims to deploy
#   3. values.yaml strimziVersion                                → builds the CRD bundle URL
#   4. versions.env STRIMZI_VERSION                              → the repo-wide pin
#   5. charts/kafka-cluster/values.yaml strimziVersion           → the Helm-test Kafka client image
#
# (5) is easy to miss: kafka-cluster no longer installs the operator, but its
# _helpers.tpl still builds `strimzi/kafka:<strimziVersion>-kafka-<kafkaVersion>`
# for the Helm tests, so the pin stays load-bearing there.
#
# Nothing else ties these together: gen-version-matrix.sh reads Chart.yaml but
# never compares it to versions.env. If (3) drifts from (1), the pre-upgrade
# hook applies CRDs for a DIFFERENT version than the operator being installed —
# the worst failure this chart can produce, and it fails silently.
#
# Usage:
#   scripts/check-versions.sh    Verify all five agree. Exits non-zero on drift.
set -euo pipefail

cd "$(dirname "$0")/.."

CHART_DIR="charts/strimzi-operator"
CHART_YAML="${CHART_DIR}/Chart.yaml"
VALUES_YAML="${CHART_DIR}/values.yaml"
KAFKA_VALUES_YAML="charts/kafka-cluster/values.yaml"

fail=0

env_val() { grep -E "^$1=" versions.env | head -1 | sed -E 's/^[^=]+="?//; s/"$//'; }

# appVersion: "1.1.0" — single unanchored line in Chart.yaml
chart_app_version=$(grep -E '^appVersion:' "$CHART_YAML" | head -1 | sed -E 's/^appVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/')

# The dependency version: the `version:` line inside the strimzi-kafka-operator
# dependency entry.
chart_dep_version=$(awk '
  /^dependencies:/        { in_deps = 1; next }
  in_deps && /^[a-z]/     { in_deps = 0 }
  in_deps && /name: strimzi-kafka-operator/ { found = 1; next }
  found && /version:/     { gsub(/^[[:space:]]*version:[[:space:]]*"?/, ""); gsub(/"?[[:space:]]*$/, ""); print; exit }
' "$CHART_YAML")

# strimziVersion: "1.1.0" — top-level key in values.yaml
values_strimzi_version=$(grep -E '^strimziVersion:' "$VALUES_YAML" | head -1 | sed -E 's/^strimziVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/')

env_strimzi_version=$(env_val STRIMZI_VERSION)

# kafka-cluster no longer installs the operator, but _helpers.tpl still uses
# this pin to build the Helm-test Kafka client image.
kafka_chart_strimzi_version=$(grep -E '^strimziVersion:' "$KAFKA_VALUES_YAML" | head -1 | sed -E 's/^strimziVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/')

echo "Strimzi version pins:"
printf '  %-46s %s\n' "${CHART_YAML} appVersion:"              "${chart_app_version:-<unset>}"
printf '  %-46s %s\n' "${CHART_YAML} dependency version:"      "${chart_dep_version:-<unset>}"
printf '  %-46s %s\n' "${VALUES_YAML} strimziVersion:"         "${values_strimzi_version:-<unset>}"
printf '  %-46s %s\n' "versions.env STRIMZI_VERSION:"          "${env_strimzi_version:-<unset>}"
printf '  %-46s %s\n' "${KAFKA_VALUES_YAML} strimziVersion:"   "${kafka_chart_strimzi_version:-<unset>}"
echo

for pair in \
  "chart_app_version:${CHART_YAML} appVersion" \
  "chart_dep_version:${CHART_YAML} dependency version" \
  "values_strimzi_version:${VALUES_YAML} strimziVersion" \
  "env_strimzi_version:versions.env STRIMZI_VERSION" \
  "kafka_chart_strimzi_version:${KAFKA_VALUES_YAML} strimziVersion"; do
  var="${pair%%:*}"
  label="${pair#*:}"
  if [[ -z "${!var}" ]]; then
    echo "ERROR: could not read ${label}" >&2
    fail=1
  fi
done

if [[ "$fail" -eq 0 ]]; then
  if [[ "$chart_app_version" == "$chart_dep_version" && \
        "$chart_app_version" == "$values_strimzi_version" && \
        "$chart_app_version" == "$env_strimzi_version" && \
        "$chart_app_version" == "$kafka_chart_strimzi_version" ]]; then
    echo "OK: all five Strimzi pins agree (${chart_app_version})."
    exit 0
  fi

  echo "DRIFT: Strimzi version pins disagree." >&2
  echo >&2
  echo "  The CRD-upgrade hook builds its bundle URL from ${VALUES_YAML}" >&2
  echo "  strimziVersion, while the operator itself comes from the Chart.yaml" >&2
  echo "  dependency version. If those differ, the hook applies CRDs for a" >&2
  echo "  different operator than the one being installed." >&2
  echo >&2
  echo "  Set all five to the same value, then re-run:" >&2
  echo "    scripts/gen-version-matrix.sh --check" >&2
  fail=1
fi

exit "$fail"
