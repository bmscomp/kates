#!/usr/bin/env bash
# A Kates backend installed from a Kates resource answers and runs a test.
#
#   KUBECONFIG=<scratch> operator/e2e-kates.sh [namespace]
#
# On the stack operator/e2e-stack.sh leaves, in a namespace the operator
# watches. It brings its own PostgreSQL, and reads the Kafka settings from the
# Helm-installed Kates, so both backends talk to the same brokers.
set -uo pipefail
cd "$(dirname "$0")/.."

ns=${1:-payments}
name=main-$(date +%s)
k=(kubectl -n "$ns")
failed=0

pass() { echo "  ok    $1"; }
fail() { echo "  FAIL  $1"; failed=1; }
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then pass "$1"; else fail "$1: expected '$2', got '$3'"; fi
}
helm_kates() { kubectl -n kates get configmap kates-config -o jsonpath="{.data.$1}"; }

echo "== a database and the Secrets the resource names"
"${k[@]}" apply -f - >/dev/null <<EOF
apiVersion: apps/v1
kind: Deployment
metadata: {name: $name-db}
spec:
  selector: {matchLabels: {app: $name-db}}
  template:
    metadata: {labels: {app: $name-db}}
    spec:
      containers:
        - name: postgres
          image: postgres:17-alpine
          env:
            - {name: POSTGRES_DB, value: kates}
            - {name: POSTGRES_USER, value: kates}
            - {name: POSTGRES_PASSWORD, value: e2e-only}
          readinessProbe: {exec: {command: [pg_isready, -U, kates]}, periodSeconds: 2}
---
apiVersion: v1
kind: Service
metadata: {name: $name-db}
spec: {selector: {app: $name-db}, ports: [{port: 5432}]}
EOF
"${k[@]}" create secret generic "$name-db" --from-literal=username=kates --from-literal=password=e2e-only \
  --from-literal=url="jdbc:postgresql://$name-db:5432/kates" >/dev/null
"${k[@]}" create secret generic "$name-kafka" >/dev/null \
  --from-literal=password="$(kubectl -n kates get secret kates-backend -o jsonpath='{.data.password}' | base64 -d)"
"${k[@]}" rollout status "deploy/$name-db" --timeout=120s >/dev/null && pass "PostgreSQL" || fail "PostgreSQL"

echo "== a Kates resource"
tag=$(sed -nE 's/^appVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/p' charts/kates/Chart.yaml)
"${k[@]}" apply -f - >/dev/null <<EOF
apiVersion: kates.io/v1alpha1
kind: Kates
metadata: {name: $name}
spec:
  version: "${KATES_TAG:-$tag-native}"
  database: {secretRef: {name: $name-db}}
  kafka:
    bootstrapServers: "$(helm_kates KATES_KAFKA_BOOTSTRAP_SERVERS)"
    securityProtocol: "$(helm_kates KATES_KAFKA_SECURITY_PROTOCOL)"
    sasl:
      mechanism: "$(helm_kates KATES_KAFKA_SASL_MECHANISM)"
      username: "$(helm_kates KATES_KAFKA_SASL_USERNAME)"
      passwordSecretRef: {name: $name-kafka, key: password}
  env:
    # One broker: the outbox topic's default of three replicas could not be created.
    - {name: KATES_OUTBOX_TOPIC_REPLICATION_FACTOR, value: "1"}
EOF
"${k[@]}" wait --for=condition=Ready "kates/$name" --timeout=6m >/dev/null && pass "Ready" || fail "never Ready"
check "status.version" "${KATES_TAG:-$tag-native}" "$("${k[@]}" get kates "$name" -o jsonpath='{.status.version}')"
check "the StatefulSet is the resource's" Kates \
  "$("${k[@]}" get statefulset "$name" -o jsonpath='{.metadata.ownerReferences[0].kind}')"

echo "== the API answers and runs a test"
"${k[@]}" port-forward "service/$name" 18099:8080 >/dev/null 2>&1 &
forward=$!
trap 'kill $forward 2>/dev/null' EXIT
api=http://localhost:18099
for _ in $(seq 1 15); do curl -sf "$api/q/health/ready" >/dev/null && break; sleep 1; done
check "GET /api/health" 200 "$(curl -s -o /dev/null -w '%{http_code}' "$api/api/health")"
id=$(curl -s -X POST "$api/api/tests" -H 'Content-Type: application/json' \
  -d '{"type":"LOAD","spec":{"numRecords":2000,"recordSize":128,"replicationFactor":1,"minInsyncReplicas":1}}' | sed -nE 's/.*"id":"([^"]+)".*/\1/p')
[ -n "$id" ] && pass "run $id started" || fail "no run started"
for _ in $(seq 1 60); do
  status=$(curl -s "$api/api/tests/$id" | sed -nE 's/.*"status":"([A-Z]+)".*/\1/p')
  [ "$status" = DONE ] || [ "$status" = FAILED ] && break
  sleep 2
done
check "the run" DONE "$status"

echo "== a pod that dies comes back"
"${k[@]}" delete pod "$name-0" --wait=false >/dev/null
"${k[@]}" wait --for=condition=Ready=false "kates/$name" --timeout=60s >/dev/null && pass "not Ready meanwhile" || fail "stayed Ready"
"${k[@]}" wait --for=condition=Ready "kates/$name" --timeout=4m >/dev/null && pass "Ready again" || fail "did not come back"

echo "== a name already taken is left alone"
taken=taken-$(date +%s)
"${k[@]}" create serviceaccount "$taken" >/dev/null
printf 'apiVersion: kates.io/v1alpha1\nkind: Kates\nmetadata: {name: %s}\nspec: {version: "1", database: {secretRef: {name: none}}, kafka: {bootstrapServers: none}}\n' "$taken" \
  | "${k[@]}" apply -f - >/dev/null
"${k[@]}" wait --for=jsonpath='{.status.conditions[?(@.type=="Ready")].reason}'=NameTaken "kates/$taken" --timeout=60s >/dev/null \
  && pass "Ready is False, NameTaken" || fail "not refused"
check "the ServiceAccount is not adopted" "" "$("${k[@]}" get serviceaccount "$taken" -o jsonpath='{.metadata.ownerReferences}')"
check "no StatefulSet" "" "$("${k[@]}" get statefulset "$taken" -o name 2>/dev/null)"
"${k[@]}" delete kates "$taken" --timeout=30s >/dev/null
check "the ServiceAccount outlives the resource" "serviceaccount/$taken" "$("${k[@]}" get serviceaccount "$taken" -o name 2>/dev/null)"
"${k[@]}" delete serviceaccount "$taken" >/dev/null

echo "== deleting the resource removes what it made"
"${k[@]}" delete kates "$name" --timeout=60s >/dev/null
"${k[@]}" wait --for=delete "statefulset/$name" "service/$name" "serviceaccount/$name" --timeout=60s >/dev/null \
  && pass "StatefulSet, Service and ServiceAccount gone" || fail "something was left"
"${k[@]}" delete deploy,service,secret "$name-db" >/dev/null 2>&1
"${k[@]}" delete secret "$name-kafka" >/dev/null 2>&1

exit $failed
