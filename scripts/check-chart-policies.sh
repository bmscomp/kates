#!/usr/bin/env bash
# Hold every chart under charts/ to the cluster policies in .github/kyverno/:
# Pod Security Standards "restricted", CPU and memory requests and a memory
# limit, an image tag that is not latest, and a sample rule that every Pod
# carries a team label.
#
# Each chart is rendered with its defaults and once per value set in
# optional_sets below (the values that switch optional workloads on: jobs,
# exporters, extra components), every render with the label team=ci set
# through the chart's label values (label_args below), and the Kyverno CLI
# applies all four policies to each render together with every
# PolicyException in .github/kyverno/exceptions/: the label policy proves
# those values reach every pod each render has. One line per chart, then every
# failure; exit 1 on any failure or error.
#
# Usage, from the repository root:
#   scripts/check-chart-policies.sh [chart ...]
# Needs helm, kyverno and the Helm repositories the charts depend on (the CI
# job adds them with .github/actions/helm-deps).
set -uo pipefail

policies=.github/kyverno/policies
exceptions=.github/kyverno/exceptions

# Value sets that switch a chart's optional workloads on, one render per line.
# A chart that gains an optional workload adds its switch here.
optional_sets() {
  case "$1" in
    apicurio-registry) echo "--set ui.enabled=true" ;;
    connect-cluster)
      echo "--set preflight.enabled=true --set secretSync.watch=true --set secretSync.secrets[0].name=ci-user --set secretSync.secrets[0].fromNamespace=kafka" ;;
    # Not seaweedfs.enabled: its hostPath volumes cannot pass, and it stays false under restricted.
    kafka-cluster)
      echo "--set tieredStorage.enabled=true --set tieredStorage.credentials.existingSecret=ci-s3 --set tieredStorage.image=registry.example/kafka-rsm:1.0.0 --set tieredStorage.remoteStorageManager.className=io.aiven.kafka.tieredstorage.RemoteStorageManager --set tieredStorage.remoteStorageManager.classPath=/opt/kafka/plugins/tiered-storage/*" ;;
    kates) echo "--set backup.enabled=true --set migration.enabled=true --set cleanup.enabled=true" ;;
    kates-chaos) echo "--set gameday.enabled=true --set litmus-core.exporter.enabled=true" ;;
    kates-platform)
      echo "--set apicurio-registry.enabled=true --set mirror-maker2.enabled=true --set connect-cluster.enabled=true" ;;
    legacy-kafka) echo "--set mode=zookeeper" ;;
    mirror-maker2)
      echo "--set preflight.enabled=true --set secretSync.enabled=true --set secretSync.watch=true --set secretSync.secrets[0].name=ci-user --set secretSync.secrets[0].fromNamespace=kafka --set tests.replication.enabled=true --set tests.offsetTranslation.enabled=true --set tests.failover.enabled=true" ;;
    strimzi-operator) echo "--set drainCleaner.enabled=true --set drainCleaner.tls.certManager.enabled=true" ;;
    velero) echo "--set deployNodeAgent=true --set cleanUpCRDs=true" ;;
  esac
}

# The values that label every pod a chart renders team=ci, in every render: the
# chart's podLabels, and the key of each wrapped subchart whose pods take their
# labels from one of its own.
label_args() {
  local kps=kube-prometheus-stack
  case "$1" in
    kates-chaos) echo "--set-string podLabels.team=ci,litmus-core.customLabels.team=ci" ;;
    kates-platform)
      echo "--set-string podLabels.team=ci,kafka-cluster.podLabels.team=ci,kates.podLabels.team=ci,apicurio-registry.podLabels.team=ci,mirror-maker2.podLabels.team=ci,connect-cluster.podLabels.team=ci" ;;
    monitoring)
      echo "--set-string podLabels.team=ci,$kps.commonLabels.team=ci,$kps.grafana.podLabels.team=ci,$kps.prometheusOperator.podLabels.team=ci,$kps.kube-state-metrics.podLabels.team=ci,$kps.prometheus-node-exporter.podLabels.team=ci" ;;
    strimzi-operator) echo "--set-string podLabels.team=ci,strimzi-kafka-operator.labels.team=ci" ;;
    velero) echo "--set-string podLabels.team=ci,nodeAgent.podLabels.team=ci,kubectl.labels.team=ci" ;;
    *) echo "--set-string podLabels.team=ci" ;;
  esac
}

for tool in helm kyverno; do
  command -v "$tool" > /dev/null || { echo "check-chart-policies: $tool is not on PATH" >&2; exit 2; }
done
[ -d "$policies" ] || { echo "check-chart-policies: run from the repository root" >&2; exit 2; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

pod_policies=("$policies/pss-restricted.yaml" "$policies/require-requests-limits.yaml" "$policies/disallow-latest-tag.yaml" "$policies/require-pod-labels.yaml")
exception_args=()
for f in "$exceptions"/*.yaml; do
  [ -f "$f" ] && exception_args+=(--exception "$f")
done

if [ $# -gt 0 ]; then
  charts=("$@")
else
  # kates-platform last: it packages its file:// subcharts as they are on disk,
  # their own dependencies included.
  charts=()
  for dir in charts/*/; do
    c=$(basename "$dir")
    [ "$c" = kates-platform ] || charts+=("$c")
  done
  [ -d charts/kates-platform ] && charts+=(kates-platform)
fi

# deps_current <chart>: helm lists every dependency of the chart ok, and no
# file:// one changed on disk after its archive was built (helm compares only
# the version, which an edit leaves as it is). Then the archives the CI job's
# helm-deps step just fetched are not fetched again.
deps_current() {
  local rows name repo status archive
  rows=$(helm dependency list "charts/$1" 2> /dev/null | awk -F'\t' 'NR > 1 && NF > 1 { for (i = 1; i <= NF; i++) gsub(/^ +| +$/, "", $i); print $1 "\t" $3 "\t" $4 }') || return 1
  [ -n "$rows" ] || return 1
  while IFS=$'\t' read -r name repo status; do
    [ "$status" = ok ] || return 1
    case "$repo" in
      file://*)
        for archive in "charts/$1/charts/$name"-[0-9]*.tgz; do
          [ -f "$archive" ] || return 1
          [ -z "$(find "charts/$1/${repo#file://}" -type f -newer "$archive")" ] || return 1
        done ;;
    esac
  done <<< "$rows"
}

# render [helm args...]: render the chart with those args and its label args,
# and apply the pod policies to it.
render() {
  local rc label=defaults
  [ $# -gt 0 ] && label="$*"
  renders=$((renders + 1))
  if ! helm template "$chart" "$root/$chart" -n "$chart" ${labels[@]+"${labels[@]}"} "$@" > "$work/render.yaml" 2> "$work/helm.log"; then
    errors=$((errors + 1))
    { echo "  $label: helm template failed"; sed 's/^/    /' "$work/helm.log"; } >> "$work/report"
    return
  fi
  kyverno apply "${pod_policies[@]}" ${exception_args[@]+"${exception_args[@]}"} --resource "$work/render.yaml" > "$work/kyverno.log" 2>&1
  rc=$?
  # pass: 20, fail: 8, warn: 0, error: 0, skip: 0
  local counts
  counts=$(sed -n 's/^pass: \([0-9]*\), fail: \([0-9]*\), warn: [0-9]*, error: \([0-9]*\), skip: \([0-9]*\).*/\1 \2 \3 \4/p' "$work/kyverno.log" | tail -1)
  if [ -z "$counts" ]; then
    errors=$((errors + 1))
    { echo "  $label: kyverno apply exited $rc without a summary"; sed 's/^/    /' "$work/kyverno.log"; } >> "$work/report"
    return
  fi
  local p f e s
  read -r p f e s <<< "$counts"
  passes=$((passes + p)) fails=$((fails + f)) errors=$((errors + e)) skips=$((skips + s))
  # A policy that matches nothing passes nothing: the old CI job did exactly that.
  if [ $((p + f + s)) -eq 0 ]; then
    errors=$((errors + 1))
    echo "  $label: the policies matched no workload" >> "$work/report"
  fi
  if [ "$f" -gt 0 ] || [ "$e" -gt 0 ] || [ "$rc" -ne 0 ]; then
    [ "$f" -gt 0 ] || [ "$e" -gt 0 ] || errors=$((errors + 1))
    { echo "  $label:"; grep -vE '^(Warning:|Applying |pass: [0-9]|[[:space:]]*$)' "$work/kyverno.log" | sed 's/^/    /'; } >> "$work/report"
  fi
}

failed=0
for chart in "${charts[@]}"; do
  dir=charts/$chart
  if [ ! -f "$dir/Chart.yaml" ]; then
    echo "$chart: ERROR no chart at $dir"; failed=1; continue
  fi
  grep -q '^type: library' "$dir/Chart.yaml" && continue
  renders=0 passes=0 fails=0 errors=0 skips=0
  : > "$work/report"

  root=charts
  if grep -q '^dependencies:' "$dir/Chart.yaml"; then
    cmd=build
    # kates-platform's Chart.lock pins its file:// subcharts' versions and goes
    # stale with every bump of one, so it is updated, in a copy of charts/ to
    # leave the checkout's Chart.lock as it is.
    if [ "$chart" = kates-platform ]; then
      cmd=update root=$work/charts
      rm -rf "$root"
      cp -R charts "$work/" || { echo "$chart: ERROR could not copy charts/"; failed=1; continue; }
    elif deps_current "$chart"; then
      cmd=
    fi
    if [ -n "$cmd" ] && ! scripts/retry.sh 3 helm dependency "$cmd" --skip-refresh "$root/$chart" > "$work/deps.log" 2>&1; then
      echo "$chart: ERROR helm dependency $cmd failed"
      sed 's/^/  /' "$work/deps.log"
      failed=1; continue
    fi
  fi

  read -r -a labels <<< "$(label_args "$chart")"
  render
  while read -r -a set_args <&3; do
    [ ${#set_args[@]} -gt 0 ] && render "${set_args[@]}"
  done 3<<< "$(optional_sets "$chart")"

  status=PASS
  if [ "$fails" -gt 0 ] || [ "$errors" -gt 0 ]; then status=FAIL; failed=1; fi
  echo "$chart: $status ($renders renders; pass $passes, fail $fails, error $errors, skip $skips)"
  cat "$work/report"
done
exit "$failed"
