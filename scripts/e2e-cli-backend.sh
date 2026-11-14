#!/usr/bin/env bash
# The CLI against a Kates API built from this tree: installs the kates chart
# with <image> on a cluster that already runs one-broker Kafka
# (charts/kafka-cluster/values-ci.yaml), then runs the commands a pipeline
# runs and checks each exit code and the fields it reads.
#
#   KUBECONFIG=<scratch> scripts/e2e-cli-backend.sh <image> [kates binary]
#
# <image> must already be on the cluster's nodes (kind load docker-image).
# Scratch clusters only: it installs into namespace kates.
set -euo pipefail
cd "$(dirname "$0")/.."

image=$1
kates=$(cd "$(dirname "${2:-build/kates}")" && pwd)/$(basename "${2:-build/kates}")

# Strimzi writes the SCRAM password in Kafka's namespace, and a pod can only read its own.
kubectl create namespace kates --dry-run=client -o yaml | kubectl apply -f -
kubectl -n kates create secret generic kates-backend --dry-run=client -o yaml \
  --from-literal=password="$(kubectl -n kafka get secret kates-backend -o jsonpath='{.data.password}' | base64 -d)" \
  | kubectl apply -f -

# One broker: Kates' default of three replicas would refuse every topic.
helm upgrade --install kates charts/kates -n kates --wait --timeout 8m \
  --set image.repository="${image%:*}" --set image.tag="${image##*:}" --set image.pullPolicy=Never \
  --set-string defaults.replicationFactor=1,defaults.minInsyncReplicas=1 \
  --set-json 'extraEnv=[{"name":"KATES_OUTBOX_TOPIC_REPLICATION_FACTOR","value":"1"}]'

work=$(mktemp -d)
kubectl -n kates port-forward svc/kates 18080:8080 >"$work/port-forward.log" 2>&1 &
forward=$!
trap 'kill "$forward" 2>/dev/null || true; rm -rf "$work"' EXIT

# The CLI reads no config file of the runner's: only these, and HOME=$work
# for each CLI call (check below), not for kubectl, which finds its
# kubeconfig through HOME on a runner.
export KATES_URL=http://127.0.0.1:18080
KATES_API_KEY=$(kubectl -n kates get secret kates-api-key -o jsonpath='{.data.api-key}' | base64 -d)
export KATES_API_KEY
for _ in $(seq 60); do
  curl -sf "$KATES_URL/api/health" >/dev/null && break
  sleep 1
done

failures=0
# check <exit code> <what> <command...>: runs it, keeps its output in $out.
check() {
  local want=$1 what=$2
  shift 2
  set +e
  out=$(HOME="$work" "$@" 2>&1)
  local got=$?
  set -e
  if [ "$got" = "$want" ]; then
    echo "ok   $what"
  else
    echo "FAIL $what: exit $got, want $want"
    echo "$out" | tail -20 | sed 's/^/     /'
    failures=$((failures + 1))
  fi
}
# field <what> <jq filter>: the filter must hold on the last command's output.
field() {
  if echo "$out" | jq -e "$2" >/dev/null 2>&1; then
    echo "ok   $1"
  else
    echo "FAIL $1: $2 does not hold"
    echo "$out" | head -40 | sed 's/^/     /'
    failures=$((failures + 1))
  fi
}

check 0 "health answers" "$kates" health

check 0 "a LOAD run finishes" "$kates" test create --type LOAD --records 2000 --wait -o json
id=$(echo "$out" | jq -r '.id // empty' 2>/dev/null || true)
field "the run is DONE" '.status == "DONE"'

check 0 "its report reads" "$kates" report show "$id" -o json
field "the report counts the records sent" '.summary.totalRecords >= 2000'

# The grade needs throughput a kind runner does not reach: F checks that a run
# is created, followed to its end and graded.
check 0 "gate grades a run it starts" "$kates" gate --min-grade F --type LOAD --records 2000

# Every broker fault hits all of one broker, so the guard must refuse it, and
# say so with exit 1, after reading the brokers through the Kubernetes API.
check 1 "the safety guard refuses a dry run that hits every broker" \
  "$kates" disruption playbook run rolling-restart --dry-run
if echo "$out" | grep -q 'ALL 1 brokers'; then
  echo "ok   the refusal names the broker count"
else
  echo "FAIL the refusal names the broker count"
  echo "$out" | tail -20 | sed 's/^/     /'
  failures=$((failures + 1))
fi

check 0 "compliance passes against the CI reference" \
  "$kates" compliance check -f cli/examples/conformity/kind-ci.yaml

if [ "$failures" -gt 0 ]; then
  echo "$failures check(s) failed"
  kubectl -n kates logs deploy/kates --tail=100 || true
  exit 1
fi
echo "every check passed"
