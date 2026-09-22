#!/usr/bin/env bash
# Assert the version pins agree across every place that declares one.
#
# Three families are checked: the Strimzi pins (six sites, below), the Kafka
# pins (five sites, plus what the vendored operator actually supports), and
# the local-cluster toolchain (kind, its node image, kubectl).
#
# WHY THIS EXISTS: charts/strimzi-operator pins the Strimzi version in THREE
# places, versions.env declares a fourth, charts/kafka-cluster a fifth, and
# charts/mirror-maker2 a sixth:
#
#   1. Chart.yaml  dependencies[strimzi-kafka-operator].version  → which operator chart is pulled
#   2. Chart.yaml  appVersion                                    → what the chart claims to deploy
#   3. values.yaml strimziVersion                                → builds the CRD bundle URL
#   4. versions.env STRIMZI_VERSION                              → the repo-wide pin
#   5. charts/kafka-cluster/values.yaml strimziVersion           → the Helm-test Kafka client image
#   6. charts/mirror-maker2/values.yaml strimziVersion           → the client image for its pre-flight probe and data tests
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
# The Kafka pins have a second authority besides each other: the operator.
# Strimzi runs a small, explicit window of Kafka versions, and the vendored
# operator chart (charts/strimzi-operator/charts/strimzi-kafka-operator-*.tgz)
# carries that window verbatim in templates/_kafka_image_map.tpl. The default
# kafkaVersion must be the NEWEST entry of that window — so the chart's pinned
# default and the CLI's computed default ("newest the operator supports") are
# one version, and a Strimzi bump that drops or adds a Kafka line fails here
# instead of at install time. It must also be at or above the platform's own
# floor, charts/kafka-cluster/Chart.yaml `kates.io/kafka-floor` (share groups
# need Kafka >= 4.2). And the tarball itself must be the pinned release: the
# filename and its Chart.yaml both say which. The tarball is gitignored and
# fetched by `helm dependency build`; when it is absent this script fetches it
# the same way (CI runs with helm on PATH and egress to quay.io) and fails
# loudly when it cannot.
#
# The toolchain pins had the same problem in a different shape: the kind binary
# was declared twice (two workflows, duplicated verbatim), the node image three
# times inside config/cluster.yaml, and a floor in the installation guide.
# Nothing compared them, and they had already drifted — kindest/node:v1.31.4
# ships with kind v0.26.0, not the v0.27.0 the workflows install. The workflows
# now read versions.env directly; config/cluster.yaml cannot, because kind takes
# a literal config with no variable substitution, so it is asserted here.
#
# The CI toolchain (Helm, kubeconform, the Kyverno CLI, Java, Node, the
# linters) is the same story one level up: HELM_VERSION was `env:` in six
# workflows. They are pinned in versions.env and loaded by
# .github/actions/load-versions; `--workflows` fails on a workflow that
# hardcodes one again, so the count can only go down.
#
# Usage:
#   scripts/check-versions.sh              Verify the pins agree. Exits non-zero on drift.
#   scripts/check-versions.sh --workflows  Only: no workflow hardcodes a CI toolchain pin.
set -euo pipefail

cd "$(dirname "$0")/.."

# ── CI toolchain pins must come from versions.env ─────────────────────────────
#
# Each pattern is what a hardcoded pin looks like in a workflow; the fix is
# always `${{ env.X }}` after a load-versions step (or go-version-file for Go).
check_workflows() {
  local rc=0 hits
  local -a patterns=(
    'version: v3\.[0-9]+\.[0-9]+'          # azure/setup-helm
    'go-version: "?[0-9]'                     # setup-go: use go-version-file: cli/go.mod
    'java-version: "?[0-9]'                   # setup-java
    'node-version: "?[0-9]'                   # setup-node
    'kubeconform/releases/download/v[0-9]'    # install-tools
    'kyverno/releases/download/v[0-9]'        # install-tools
    'KIND_VERSION: v'                         # load-versions
    'KUBECTL_VERSION: v'                      # load-versions
    'HELM_VERSION: v'                         # load-versions
  )
  for pat in "${patterns[@]}"; do
    hits=$(grep -nE "$pat" .github/workflows/*.yml || true)
    if [[ -n "$hits" ]]; then
      echo "ERROR: toolchain pin hardcoded in a workflow (declare it in versions.env, load it with .github/actions/load-versions):" >&2
      echo "$hits" | sed 's/^/  /' >&2
      rc=1
    fi
  done
  # Every pin load-versions exports by default has to exist, or the action
  # fails on its first use.
  local key
  for key in $(sed -nE 's/^    default: (.*)$/\1/p' .github/actions/load-versions/action.yml); do
    if ! grep -qE "^${key}=" versions.env; then
      echo "ERROR: load-versions exports ${key} by default but versions.env does not declare it" >&2
      rc=1
    fi
  done
  [[ $rc -eq 0 ]] && echo "OK: no workflow hardcodes a CI toolchain pin."
  return $rc
}

if [[ "${1:-}" == "--workflows" ]]; then
  check_workflows
  exit $?
fi

CHART_DIR="charts/strimzi-operator"
CHART_YAML="${CHART_DIR}/Chart.yaml"
VALUES_YAML="${CHART_DIR}/values.yaml"
KAFKA_VALUES_YAML="charts/kafka-cluster/values.yaml"
MM2_VALUES_YAML="charts/mirror-maker2/values.yaml"

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

# (6) mirror-maker2 builds the image tag for its pre-flight probe and its data
# tests as strimzi/kafka:<strimziVersion>-kafka-<version>. If this drifts, the
# probe runs a DIFFERENT client from the one the workers use — which is exactly
# the mistake KIP-896 punishes, and the probe would then be proving nothing
# about the deployment it gates.
mm2_chart_strimzi_version=$(grep -E '^strimziVersion:' "$MM2_VALUES_YAML" | head -1 | sed -E 's/^strimziVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/')

echo "Strimzi version pins:"
printf '  %-46s %s\n' "${CHART_YAML} appVersion:"              "${chart_app_version:-<unset>}"
printf '  %-46s %s\n' "${CHART_YAML} dependency version:"      "${chart_dep_version:-<unset>}"
printf '  %-46s %s\n' "${VALUES_YAML} strimziVersion:"         "${values_strimzi_version:-<unset>}"
printf '  %-46s %s\n' "versions.env STRIMZI_VERSION:"          "${env_strimzi_version:-<unset>}"
printf '  %-46s %s\n' "${KAFKA_VALUES_YAML} strimziVersion:"   "${kafka_chart_strimzi_version:-<unset>}"
printf '  %-46s %s\n' "${MM2_VALUES_YAML} strimziVersion:"     "${mm2_chart_strimzi_version:-<unset>}"
echo

for pair in \
  "chart_app_version:${CHART_YAML} appVersion" \
  "chart_dep_version:${CHART_YAML} dependency version" \
  "values_strimzi_version:${VALUES_YAML} strimziVersion" \
  "env_strimzi_version:versions.env STRIMZI_VERSION" \
  "kafka_chart_strimzi_version:${KAFKA_VALUES_YAML} strimziVersion" \
  "mm2_chart_strimzi_version:${MM2_VALUES_YAML} strimziVersion"; do
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
        "$chart_app_version" == "$kafka_chart_strimzi_version" && \
        "$chart_app_version" == "$mm2_chart_strimzi_version" ]]; then
    echo "OK: all six Strimzi pins agree (${chart_app_version})."
  else
    echo "DRIFT: Strimzi version pins disagree." >&2
    echo >&2
    echo "  The CRD-upgrade hook builds its bundle URL from ${VALUES_YAML}" >&2
    echo "  strimziVersion, while the operator itself comes from the Chart.yaml" >&2
    echo "  dependency version. If those differ, the hook applies CRDs for a" >&2
    echo "  different operator than the one being installed." >&2
    echo >&2
    echo "  Set all six to the same value, then re-run:" >&2
    echo "    scripts/gen-version-matrix.sh --check" >&2
    fail=1
  fi
fi

# ---------------------------------------------------------------------------
# The Kafka line, which is the OTHER half of every client image tag.
#
# strimzi/kafka:<strimziVersion>-kafka-<kafkaVersion> is built from two pins,
# and the block above only checks the first. If mirror-maker2's `version`
# (the line its workers run, and the image its pre-flight probe uses) drifts
# from the cluster's kafkaVersion and from versions.env, the probe proves
# something about a different client than the one the mirror will use —
# which is precisely the mistake KIP-896 punishes.
# ---------------------------------------------------------------------------

env_kafka_version=$(env_val STRIMZI_KAFKA_VERSION)
env_kafka_version=${env_kafka_version##*-kafka-}
kafka_chart_kafka_version=$(grep -E '^kafkaVersion:' "$KAFKA_VALUES_YAML" | head -1 | sed -E 's/^kafkaVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)
mm2_chart_kafka_version=$(grep -E '^version:' "$MM2_VALUES_YAML" | head -1 | sed -E 's/^version:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)
mm2_chart_app_version=$(grep -E '^appVersion:' charts/mirror-maker2/Chart.yaml | head -1 | sed -E 's/^appVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)
# The fifth Kafka site: connect-cluster's `version` is the KafkaConnect CR's
# spec.version, and its image is the same strimzi/kafka:<strimzi>-kafka-
# <version> pair as everything above. Checked separately below (b).
CONNECT_VALUES_YAML="charts/connect-cluster/values.yaml"
connect_chart_kafka_version=$(grep -E '^version:' "$CONNECT_VALUES_YAML" | head -1 | sed -E 's/^version:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)

echo
echo "Kafka version pins:"
printf '  %-46s %s\n' "versions.env STRIMZI_KAFKA_VERSION (kafka):"  "${env_kafka_version:-<unset>}"
printf '  %-46s %s\n' "${KAFKA_VALUES_YAML} kafkaVersion:"           "${kafka_chart_kafka_version:-<unset>}"
printf '  %-46s %s\n' "${MM2_VALUES_YAML} version:"                  "${mm2_chart_kafka_version:-<unset>}"
printf '  %-46s %s\n' "charts/mirror-maker2/Chart.yaml appVersion:"  "${mm2_chart_app_version:-<unset>}"
printf '  %-46s %s\n' "${CONNECT_VALUES_YAML} version:"              "${connect_chart_kafka_version:-<unset>}"

if [[ -z "$env_kafka_version" || -z "$kafka_chart_kafka_version" || -z "$mm2_chart_kafka_version" || -z "$mm2_chart_app_version" ]]; then
  echo "ERROR: could not read one of the Kafka version pins" >&2
  fail=1
elif [[ "$env_kafka_version" == "$kafka_chart_kafka_version" && \
        "$env_kafka_version" == "$mm2_chart_kafka_version" && \
        "$env_kafka_version" == "$mm2_chart_app_version" ]]; then
  echo "OK: all four Kafka pins agree (${env_kafka_version})."
else
  echo "DRIFT: Kafka version pins disagree." >&2
  echo "  mirror-maker2 probes and tests its sources with strimzi/kafka:<strimzi>-kafka-<version>;" >&2
  echo "  that must be the same Kafka line the workers and the target cluster run." >&2
  fail=1
fi

# (b) The fifth Kafka site — see the read above the table. A Connect version
# outside the operator's window fails exactly like a Kafka one.
if [[ -z "$connect_chart_kafka_version" ]]; then
  echo "ERROR: could not read ${CONNECT_VALUES_YAML} version" >&2
  fail=1
elif [[ -n "$env_kafka_version" && "$connect_chart_kafka_version" == "$env_kafka_version" ]]; then
  echo "OK: connect-cluster version agrees with the Kafka pin (${connect_chart_kafka_version})."
elif [[ -n "$env_kafka_version" ]]; then
  echo "DRIFT: ${CONNECT_VALUES_YAML} version is ${connect_chart_kafka_version}, the Kafka pin is ${env_kafka_version}." >&2
  echo "  The KafkaConnect CR's spec.version runs on the same operator as the cluster;" >&2
  echo "  a Connect version outside its window fails exactly like a Kafka one." >&2
  fail=1
fi

# ---------------------------------------------------------------------------
# The Connect image every cluster runs, kind included.
#
# WHY THIS EXISTS: values.yaml names the PUBLISHED tag
# ghcr.io/bmscomp/connect:<debezium>-kafka-<kafka>, and kind runs that same
# image. The kind overlay used to pin a locally built tag instead, which made a
# Connect deploy on kind depend on `make connect-build` having run on that
# machine — and fail as ImagePullBackOff twenty minutes in when it had not.
#
# Two things keep the registry pin honest. Its Debezium line must be the one
# Dockerfile.connect builds, because publish-connect.yml tags the image from
# that ARG: a pin that disagrees names an image no release produces. And its
# Kafka line must be the repo's Kafka pin, for the same reason. The overlay
# check is the guard against the old behaviour coming back quietly.
# ---------------------------------------------------------------------------
CONNECT_KIND_VALUES="charts/connect-cluster/values-kind.yaml"
dockerfile_dbz=$(grep -E '^ARG DEBEZIUM_VERSION=' Dockerfile.connect | head -1 | cut -d= -f2)
dockerfile_tag=${dockerfile_dbz%.Final}
connect_image=$(grep -E '^image:' "$CONNECT_VALUES_YAML" | head -1 | sed -E 's/^image:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/')
connect_image_tag=${connect_image##*:}
connect_image_dbz=${connect_image_tag%%-kafka-*}
connect_image_kafka=${connect_image_tag##*-kafka-}
kind_override=$(grep -E '^image:' "$CONNECT_KIND_VALUES" | head -1 || true)

echo ""
echo "Connect image:"
printf '  %-46s %s\n' "Dockerfile.connect DEBEZIUM_VERSION:"        "${dockerfile_dbz:-<unset>}"
printf '  %-46s %s\n' "${CONNECT_VALUES_YAML} image:"              "${connect_image:-<unset>}"
printf '  %-46s %s\n' "${CONNECT_KIND_VALUES} image override:"     "${kind_override:-<none>}"

if [[ -z "$connect_image" ]]; then
  echo "ERROR: could not read ${CONNECT_VALUES_YAML} image" >&2
  fail=1
elif [[ "$connect_image" != ghcr.io/bmscomp/connect:*-kafka-* ]]; then
  echo "DRIFT: ${CONNECT_VALUES_YAML} image is ${connect_image}; it must be the published" >&2
  echo "  ghcr.io/bmscomp/connect:<debezium>-kafka-<kafka> tag, the full build identity." >&2
  fail=1
elif [[ "$connect_image_dbz" != "$dockerfile_tag" ]]; then
  echo "DRIFT: ${CONNECT_VALUES_YAML} runs Debezium ${connect_image_dbz}, Dockerfile.connect builds ${dockerfile_tag}." >&2
  echo "  publish-connect.yml tags the image from the Dockerfile, so that pin names an image no" >&2
  echo "  release produces. Move it to ghcr.io/bmscomp/connect:${dockerfile_tag}-kafka-${env_kafka_version}." >&2
  fail=1
elif [[ "$connect_image_kafka" != "$env_kafka_version" ]]; then
  echo "DRIFT: ${CONNECT_VALUES_YAML} image is built on Kafka ${connect_image_kafka}, the Kafka pin is ${env_kafka_version}." >&2
  echo "  Move it to ghcr.io/bmscomp/connect:${dockerfile_tag}-kafka-${env_kafka_version}." >&2
  fail=1
elif [[ -n "$kind_override" ]]; then
  echo "DRIFT: ${CONNECT_KIND_VALUES} overrides the image (${kind_override})." >&2
  echo "  kind runs the published image like every other cluster. A bare tag there resolves only" >&2
  echo "  after make connect-build on that machine and is ImagePullBackOff everywhere else;" >&2
  echo "  pass --set image=... for a one-off local build instead." >&2
  fail=1
else
  echo "OK: every cluster, kind included, runs ${connect_image} (Debezium ${dockerfile_tag} per Dockerfile.connect)."
fi

# connect-cluster 2.0: appVersion is the Kafka line (as mirror-maker2's is),
# and the image pin is recorded in the kates.io/connect-image annotation, which
# publish-connect.yml moves together with values.yaml.
CONNECT_CHART_YAML="charts/connect-cluster/Chart.yaml"
connect_app_version=$(grep -E '^appVersion:' "$CONNECT_CHART_YAML" | head -1 | sed -E 's/^appVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)
connect_image_annotation=$(grep -E '^  kates.io/connect-image:' "$CONNECT_CHART_YAML" | head -1 | sed -E 's/^[[:space:]]*kates\.io\/connect-image:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)
printf '  %-46s %s\n' "${CONNECT_CHART_YAML} appVersion:"            "${connect_app_version:-<unset>}"
printf '  %-46s %s\n' "${CONNECT_CHART_YAML} kates.io/connect-image:" "${connect_image_annotation:-<unset>}"
if [[ "$connect_app_version" != "$env_kafka_version" ]]; then
  echo "DRIFT: ${CONNECT_CHART_YAML} appVersion is ${connect_app_version:-<unset>}, the Kafka pin is ${env_kafka_version}." >&2
  fail=1
elif [[ "$connect_image_annotation" != "$connect_image" ]]; then
  echo "DRIFT: ${CONNECT_CHART_YAML} kates.io/connect-image is ${connect_image_annotation:-<unset>}, values.yaml runs ${connect_image}." >&2
  fail=1
else
  echo "OK: connect-cluster appVersion and image annotation agree with the pins."
fi

# ---------------------------------------------------------------------------
# What the vendored operator actually supports.
#
# The operator chart is the only authority on which Kafka versions it runs:
# templates/_kafka_image_map.tpl renders STRIMZI_KAFKA_IMAGES with one
# `X.Y.Z=<image>` line per supported version. Read it straight out of the
# tarball — no render, no cluster.
# ---------------------------------------------------------------------------

STRIMZI_CHART_DIR="charts/strimzi-operator"
KAFKA_CHART_YAML="charts/kafka-cluster/Chart.yaml"

# The tarball is gitignored (charts/*/charts/) and produced by
# `helm dependency build`. Fetch it the same way when it is absent, so this
# check does not depend on which CI step ran first — and fail loudly when
# that is impossible, because a silently skipped check is no check.
shopt -s nullglob
strimzi_tarballs=("${STRIMZI_CHART_DIR}"/charts/strimzi-kafka-operator-*.tgz)
shopt -u nullglob
if [[ "${#strimzi_tarballs[@]}" -eq 0 ]]; then
  if command -v helm >/dev/null 2>&1; then
    echo "note: no vendored operator tarball under ${STRIMZI_CHART_DIR}/charts/ — running helm dependency build"
    # Retried, with helm's own words kept. This fetch is the one part of this
    # script that needs a registry to be reachable, and a 504 from quay.io turned
    # the whole pin check red with nothing to read but "failed".
    dep_err="$(mktemp)"
    for attempt in 1 2 3; do
      if helm dependency build "${STRIMZI_CHART_DIR}" >/dev/null 2>"${dep_err}"; then
        break
      fi
      if [[ "${attempt}" -eq 3 ]]; then
        echo "ERROR: helm dependency build ${STRIMZI_CHART_DIR} failed three times (needs egress to quay.io):" >&2
        sed 's/^/  /' "${dep_err}" >&2
      else
        echo "  attempt ${attempt}/3 failed: $(tail -n 1 "${dep_err}") — retrying" >&2
        sleep $((attempt * 5))
      fi
    done
    rm -f "${dep_err}"
    shopt -s nullglob
    strimzi_tarballs=("${STRIMZI_CHART_DIR}"/charts/strimzi-kafka-operator-*.tgz)
    shopt -u nullglob
  else
    echo "ERROR: helm is not on PATH, so the vendored operator tarball cannot be fetched." >&2
  fi
fi

echo
echo "Vendored operator window:"
if [[ "${#strimzi_tarballs[@]}" -ne 1 ]]; then
  if [[ "${#strimzi_tarballs[@]}" -eq 0 ]]; then
    echo "ERROR: no strimzi-kafka-operator-*.tgz under ${STRIMZI_CHART_DIR}/charts/." >&2
    echo "  Run: helm dependency build ${STRIMZI_CHART_DIR}" >&2
  else
    echo "DRIFT: more than one operator tarball under ${STRIMZI_CHART_DIR}/charts/: ${strimzi_tarballs[*]}" >&2
    echo "  Delete the stale one; \`helm dependency build\` keeps exactly the pinned version." >&2
  fi
  fail=1
else
  strimzi_tarball="${strimzi_tarballs[0]}"

  # (c) The tarball IS the pin: its filename and its own Chart.yaml both name
  # the version, and both must equal the Strimzi pin. A stale tarball (the pin
  # moved, the build did not) would make every window check below assert the
  # wrong operator's window.
  tarball_file_version=$(basename "$strimzi_tarball" | sed -E 's/^strimzi-kafka-operator-(.+)\.tgz$/\1/')
  tarball_chart_version=$(tar -xzf "$strimzi_tarball" -O strimzi-kafka-operator/Chart.yaml 2>/dev/null \
    | grep -E '^version:' | head -1 | sed -E 's/^version:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)

  # (a) The window: every `X.Y.Z=` line inside the STRIMZI_KAFKA_IMAGES block
  # of _kafka_image_map.tpl, newest last (sort -V orders versions, not strings).
  mapfile -t operator_window < <(
    tar -xzf "$strimzi_tarball" -O strimzi-kafka-operator/templates/_kafka_image_map.tpl 2>/dev/null \
      | awk '
          /name: STRIMZI_KAFKA_IMAGES/ { inblock = 1; next }
          inblock && /name: /          { inblock = 0 }
          inblock && match($0, /^[[:space:]]*[0-9]+\.[0-9]+\.[0-9]+=/) {
            v = $0; sub(/^[[:space:]]*/, "", v); sub(/=.*$/, "", v); print v
          }
        ' | sort -V
  )
  operator_newest="${operator_window[${#operator_window[@]}-1]:-}"

  # (d) The platform's own floor, declared by the kafka-cluster chart.
  kafka_floor=$(grep -E '^[[:space:]]*kates.io/kafka-floor:' "$KAFKA_CHART_YAML" | head -1 \
    | sed -E 's/^[[:space:]]*kates.io\/kafka-floor:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)

  printf '  %-46s %s\n' "tarball:"                                   "${strimzi_tarball}"
  printf '  %-46s %s\n' "tarball filename version:"                  "${tarball_file_version:-<unset>}"
  printf '  %-46s %s\n' "tarball Chart.yaml version:"                "${tarball_chart_version:-<unset>}"
  printf '  %-46s %s\n' "STRIMZI_KAFKA_IMAGES window:"               "${operator_window[*]:-<none found>}"
  printf '  %-46s %s\n' "${KAFKA_CHART_YAML} kates.io/kafka-floor:"  "${kafka_floor:-<unset>}"
  echo

  if [[ -z "$tarball_chart_version" ]]; then
    echo "ERROR: could not read Chart.yaml version from ${strimzi_tarball}" >&2
    fail=1
  elif [[ -n "$chart_app_version" && "$tarball_file_version" == "$chart_app_version" && "$tarball_chart_version" == "$chart_app_version" ]]; then
    echo "OK: vendored operator tarball is the pinned Strimzi ${chart_app_version} (filename and Chart.yaml agree)."
  elif [[ -n "$chart_app_version" ]]; then
    echo "DRIFT: vendored operator tarball is not the pinned Strimzi ${chart_app_version}." >&2
    echo "  filename says ${tarball_file_version}, its Chart.yaml says ${tarball_chart_version}." >&2
    echo "  Run: helm dependency build ${STRIMZI_CHART_DIR}  (and delete any stale tarball)" >&2
    fail=1
  fi

  if [[ "${#operator_window[@]}" -eq 0 ]]; then
    echo "ERROR: no X.Y.Z= entries found under STRIMZI_KAFKA_IMAGES in the tarball's _kafka_image_map.tpl" >&2
    fail=1
  elif [[ -z "$kafka_chart_kafka_version" ]]; then
    echo "ERROR: could not read ${KAFKA_VALUES_YAML} kafkaVersion" >&2
    fail=1
  elif [[ "$kafka_chart_kafka_version" == "$operator_newest" ]]; then
    echo "OK: default kafkaVersion ${kafka_chart_kafka_version} is the newest the vendored operator supports (window: ${operator_window[*]})."
  else
    in_window=0
    for v in "${operator_window[@]}"; do [[ "$v" == "$kafka_chart_kafka_version" ]] && in_window=1; done
    if [[ "$in_window" -eq 1 ]]; then
      echo "DRIFT: default kafkaVersion ${kafka_chart_kafka_version} is inside the vendored operator's window but is not its newest entry (${operator_newest})." >&2
      echo "  A flagless \`kates deploy\` picks the newest version the operator supports, and a" >&2
      echo "  hand-run \`helm install\` picks the chart default; the two must be one version." >&2
    else
      echo "DRIFT: default kafkaVersion ${kafka_chart_kafka_version} is OUTSIDE the vendored operator's window (${operator_window[*]})." >&2
      echo "  Strimzi ${chart_app_version:-?} cannot run it: the Kafka CR would go NotReady." >&2
    fi
    echo "  Move the Kafka pins (versions.env STRIMZI_KAFKA_VERSION, ${KAFKA_VALUES_YAML} kafkaVersion," >&2
    echo "  ${MM2_VALUES_YAML} version + Chart.yaml appVersion, ${CONNECT_VALUES_YAML} version) to ${operator_newest}." >&2
    fail=1
  fi

  if [[ -z "$kafka_floor" ]]; then
    echo "ERROR: could not read the kates.io/kafka-floor annotation from ${KAFKA_CHART_YAML}" >&2
    fail=1
  elif [[ -n "$kafka_chart_kafka_version" ]]; then
    # sort -V: the floor must sort first (or equal) against the default.
    if [[ "$(printf '%s\n%s\n' "$kafka_chart_kafka_version" "$kafka_floor" | sort -V | head -1)" == "$kafka_floor" ]]; then
      echo "OK: default kafkaVersion ${kafka_chart_kafka_version} is at or above the platform floor (${kafka_floor})."
    else
      echo "DRIFT: default kafkaVersion ${kafka_chart_kafka_version} is below ${KAFKA_CHART_YAML} kates.io/kafka-floor (${kafka_floor})." >&2
      echo "  The primary's configuration needs the feature that set the floor (share groups);" >&2
      echo "  either raise the Kafka pins or remove that feature and lower the floor." >&2
      fail=1
    fi
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

# ---------------------------------------------------------------------------
# The tester image the Strimzi charts' Helm tests, hooks and secret-sync jobs
# run. ghcr.io/bmscomp/kates-tester is published with each Kates release, so
# every chart pins the same tag, and it is the kates chart's appVersion
# (docs/kafka-charts-refactor-plan.md, M4). A chart left behind runs test
# tooling a release older than the rest.
# ---------------------------------------------------------------------------
kates_app_version=$(grep -E '^appVersion:' charts/kates/Chart.yaml | head -1 | sed -E 's/^appVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/' || true)
echo ""
echo "Tester image (kates-tester):"
printf '  %-46s %s\n' "charts/kates/Chart.yaml appVersion:" "${kates_app_version:-<unset>}"
tester_drift=0
tester_pins=0
while IFS= read -r hit; do
  tester_pins=$((tester_pins + 1))
  file=${hit%%:*}
  tag=$(echo "$hit" | sed -E 's/.*kates-tester:([^" ]+).*/\1/')
  printf '  %-46s %s\n' "${file}:" "$tag"
  if [[ "$tag" != "$kates_app_version" ]]; then
    tester_drift=1
  fi
done < <(grep -HE '^[[:space:]]*[A-Za-z]+:[[:space:]]*"?ghcr.io/bmscomp/kates-tester:' \
           charts/strimzi-operator/values*.yaml charts/kafka-cluster/values*.yaml \
           charts/connect-cluster/values*.yaml charts/mirror-maker2/values*.yaml 2>/dev/null)
if [[ -z "$kates_app_version" ]]; then
  echo "ERROR: could not read charts/kates/Chart.yaml appVersion" >&2
  fail=1
elif [[ "$tester_pins" -lt 4 ]]; then
  # One per Strimzi chart, and kafka-cluster pins two images: fewer than four
  # means the grep stopped matching (a moved path, a renamed key), and a check
  # that silently matches nothing is no check.
  echo "ERROR: found ${tester_pins} kates-tester pin(s) in the Strimzi charts; expected at least 4." >&2
  fail=1
elif [[ "$tester_drift" -ne 0 ]]; then
  echo "DRIFT: a Strimzi chart pins kates-tester at a tag other than ${kates_app_version}." >&2
  echo "  Move every testImages pin to ghcr.io/bmscomp/kates-tester:${kates_app_version}." >&2
  fail=1
else
  echo "OK: every Strimzi chart runs kates-tester:${kates_app_version}."
fi

check_workflows || fail=1

exit "$fail"
