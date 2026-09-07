#!/usr/bin/env bash
# Assert the version pins agree across every place that declares one.
#
# Two families are checked: the Strimzi pins (five sites, below) and the
# local-cluster toolchain (kind, its node image, kubectl).
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
# The toolchain pins had the same problem in a different shape: the kind binary
# was declared twice (two workflows, duplicated verbatim), the node image three
# times inside config/cluster.yaml, and a floor in the installation guide.
# Nothing compared them, and they had already drifted — kindest/node:v1.31.4
# ships with kind v0.26.0, not the v0.27.0 the workflows install. The workflows
# now read versions.env directly; config/cluster.yaml cannot, because kind takes
# a literal config with no variable substitution, so it is asserted here.
#
# Usage:
#   scripts/check-versions.sh    Verify the pins agree. Exits non-zero on drift.
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
  else
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
fi

# ---------------------------------------------------------------------------
# Local-cluster toolchain: kind, its node image, kubectl.
# ---------------------------------------------------------------------------

CLUSTER_YAML="config/cluster.yaml"
INSTALL_GUIDE="docs/book/20-installation-guide.md"

env_kind_version=$(env_val KIND_VERSION)
env_node_version=$(env_val KINDEST_NODE_VERSION)
env_kubectl_version=$(env_val KUBECTL_VERSION)

# Every `image: kindest/node:vX.Y.Z` in the kind topology — one per node, and
# they must all match. A cluster whose nodes run different Kubernetes minors is
# not a thing anyone means to build.
mapfile -t cluster_node_versions < <(
  grep -Eo 'kindest/node:v[0-9]+\.[0-9]+\.[0-9]+' "$CLUSTER_YAML" | sed 's|kindest/node:||' | sort -u
)

# "| **Kind** *(optional)* | 0.22+ |" — a floor for readers, not a pin.
docs_kind_floor=$(
  grep -E '^\|[[:space:]]*\*\*Kind\*\*' "$INSTALL_GUIDE" \
    | sed -E 's/.*\|[[:space:]]*([0-9]+\.[0-9]+)\+[[:space:]]*\|.*/\1/' | head -1
)

echo
echo "Local-cluster toolchain pins:"
printf '  %-46s %s\n' "versions.env KIND_VERSION:"           "${env_kind_version:-<unset>}"
printf '  %-46s %s\n' "versions.env KINDEST_NODE_VERSION:"   "${env_node_version:-<unset>}"
printf '  %-46s %s\n' "versions.env KUBECTL_VERSION:"        "${env_kubectl_version:-<unset>}"
printf '  %-46s %s\n' "${CLUSTER_YAML} kindest/node tags:"   "${cluster_node_versions[*]:-<none found>}"
printf '  %-46s %s\n' "${INSTALL_GUIDE} Kind floor:"         "${docs_kind_floor:-<unset>}+"
echo

for pair in \
  "env_kind_version:versions.env KIND_VERSION" \
  "env_node_version:versions.env KINDEST_NODE_VERSION" \
  "env_kubectl_version:versions.env KUBECTL_VERSION" \
  "docs_kind_floor:${INSTALL_GUIDE} Kind floor"; do
  var="${pair%%:*}"
  label="${pair#*:}"
  if [[ -z "${!var}" ]]; then
    echo "ERROR: could not read ${label}" >&2
    fail=1
  fi
done

if [[ "${#cluster_node_versions[@]}" -eq 0 ]]; then
  echo "ERROR: no kindest/node image tags found in ${CLUSTER_YAML}" >&2
  fail=1
elif [[ "${#cluster_node_versions[@]}" -gt 1 ]]; then
  echo "DRIFT: ${CLUSTER_YAML} pins more than one node image: ${cluster_node_versions[*]}" >&2
  echo "  Every node in the topology must run the same Kubernetes version." >&2
  fail=1
elif [[ -n "$env_node_version" && "${cluster_node_versions[0]}" != "$env_node_version" ]]; then
  echo "DRIFT: node image pins disagree." >&2
  echo "  versions.env KINDEST_NODE_VERSION: ${env_node_version}" >&2
  echo "  ${CLUSTER_YAML} kindest/node:       ${cluster_node_versions[0]}" >&2
  echo >&2
  echo "  kind reads ${CLUSTER_YAML} literally and cannot expand a variable," >&2
  echo "  so the tags are maintained by hand and asserted here. Update every" >&2
  echo "  'image: kindest/node:' line in that file to match versions.env." >&2
  fail=1
fi

# kind chooses the kubeadm API version from the NODE IMAGE, not from its own
# release: below Kubernetes 1.36 it renders v1beta3, from 1.36 up it renders
# v1beta4 (kind/pkg/cluster/internal/kubeadm/config.go). The two are not
# compatible — kubeletExtraArgs is a map in one and a list of name/value in the
# other, and timeoutForControlPlane moved onto a timeouts struct — and the
# mismatch does NOT surface at config load. It surfaces minutes later, inside
# `kubeadm init`, as
#
#   cannot unmarshal array into Go struct field ... of type map[string]string
#
# after kind has already pulled images and started containers. This assertion is
# here because that is an expensive way to learn it.
if [[ -n "$env_node_version" ]] && [[ -f "$CLUSTER_YAML" ]]; then
  node_minor_for_api="$(echo "${env_node_version#v}" | cut -d. -f2)"
  patch_has_list_args=0
  patch_has_map_args=0
  # Inside the `kubeletExtraArgs:` block of each patch, a "- name:" item means
  # the v1beta4 list shape; a plain "key: value" means the v1beta3 map.
  awk '
    /kubeletExtraArgs:/ { inargs = 1; next }
    inargs && /^[[:space:]]*-[[:space:]]*name:/ { print "LIST"; inargs = 0; next }
    inargs && /^[[:space:]]*[A-Za-z][A-Za-z0-9_-]*:[[:space:]]*[^[:space:]]/ { print "MAP"; inargs = 0; next }
    inargs && /^[[:space:]]*$/ { inargs = 0 }
  ' "$CLUSTER_YAML" > /tmp/.kubeadm_arg_shapes.$$ 2>/dev/null || true
  grep -q LIST /tmp/.kubeadm_arg_shapes.$$ && patch_has_list_args=1
  grep -q MAP  /tmp/.kubeadm_arg_shapes.$$ && patch_has_map_args=1
  rm -f /tmp/.kubeadm_arg_shapes.$$

  patch_has_v4_timeouts=0
  grep -qE '^[[:space:]]*timeouts:' "$CLUSTER_YAML" && patch_has_v4_timeouts=1
  patch_has_v3_timeout=0
  grep -qE '^[[:space:]]*timeoutForControlPlane:' "$CLUSTER_YAML" && patch_has_v3_timeout=1

  if [[ "$node_minor_for_api" -ge 36 ]]; then
    expected_api="v1beta4"
    if [[ "$patch_has_map_args" -eq 1 || "$patch_has_v3_timeout" -eq 1 ]]; then
      echo "DRIFT: ${CLUSTER_YAML} uses kubeadm v1beta3 patches, but node ${env_node_version} renders v1beta4." >&2
      echo "  kubeletExtraArgs must be a list of name/value pairs, and" >&2
      echo "  timeoutForControlPlane must become timeouts.controlPlaneComponentHealthCheck." >&2
      fail=1
    fi
  else
    expected_api="v1beta3"
    if [[ "$patch_has_list_args" -eq 1 || "$patch_has_v4_timeouts" -eq 1 ]]; then
      echo "DRIFT: ${CLUSTER_YAML} uses kubeadm v1beta4 patches, but node ${env_node_version} renders v1beta3." >&2
      echo "  kubeletExtraArgs must be a map, and the timeouts struct must go back" >&2
      echo "  to apiServer.timeoutForControlPlane on ClusterConfiguration." >&2
      echo "  kind only renders v1beta4 for Kubernetes 1.36 and above." >&2
      fail=1
    fi
  fi
  printf '  %-46s %s\n' "kubeadm API for node ${env_node_version}:" "${expected_api}"
fi

# The docs figure is a MINIMUM, so it may legitimately trail the pin — but it
# must never exceed it, or the guide asks readers for a kind newer than the one
# CI proves works. sort -V orders versions, not strings, so 0.9 < 0.27.
if [[ -n "$env_kind_version" && -n "$docs_kind_floor" ]]; then
  kind_bare="${env_kind_version#v}"
  if [[ "$(printf '%s\n%s\n' "$kind_bare" "$docs_kind_floor" | sort -V | head -1)" != "$docs_kind_floor" ]]; then
    echo "DRIFT: ${INSTALL_GUIDE} requires Kind ${docs_kind_floor}+, above the pinned ${env_kind_version}." >&2
    echo "  Lower the documented floor, or raise KIND_VERSION to what the docs promise." >&2
    fail=1
  fi
fi

if [[ "$fail" -eq 0 ]]; then
  echo "OK: toolchain pins agree (kind ${env_kind_version}, node ${env_node_version})."
fi

# kubectl is supported within one minor of the API server. This was a warning
# while the pins sat two minors apart — failing then would have blocked every
# chart PR on an unrelated upgrade. They now agree, so it fails.
if [[ -n "$env_kubectl_version" && -n "$env_node_version" ]]; then
  kubectl_minor="$(echo "${env_kubectl_version#v}" | cut -d. -f2)"
  node_minor="$(echo "${env_node_version#v}" | cut -d. -f2)"
  skew=$(( kubectl_minor - node_minor )); [[ "$skew" -lt 0 ]] && skew=$(( -skew ))
  if [[ "$skew" -gt 1 ]]; then
    echo "DRIFT: kubectl ${env_kubectl_version} is ${skew} minors from the node image ${env_node_version}." >&2
    echo "  Supported kubectl skew against the API server is ±1." >&2
    echo "  Move KUBECTL_VERSION and KINDEST_NODE_VERSION together." >&2
    fail=1
  else
    echo "OK: kubectl ${env_kubectl_version} is within ±1 minor of ${env_node_version}."
  fi
fi

exit "$fail"
