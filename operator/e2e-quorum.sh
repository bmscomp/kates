#!/usr/bin/env bash
# A Kates resource with three members: one leader takes the traffic, a new one is elected when it
# dies and recovers its run, a version change keeps a majority, the leader replaced last, and a
# leader cut off from its peers takes no write once another leads.
#
#   KUBECONFIG=<scratch> operator/e2e-quorum.sh [namespace]
#
# On the stack operator/e2e-stack.sh leaves, as operator/e2e-kates.sh. KATES_IMAGE, KATES_TAG and
# KATES_NEXT_TAG as there.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

ns=${1:-payments}
name=q-$(date +%s)
k=(kubectl -n "$ns")
failed=0

pass() { echo "  ok    $1"; }
fail() { echo "  FAIL  $1"; failed=1; }
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then pass "$1"; else fail "$1: expected '$2', got '$3'"; fi
}
helm_kates() { kubectl -n kates get configmap kates-config -o jsonpath="{.data.$1}"; }
leader() { "${k[@]}" get kates "$name" -o jsonpath='{.status.leader}'; }
labelled() { "${k[@]}" get pods -l "app.kubernetes.io/instance=$name,kates.io/quorum-role=leader" -o jsonpath='{.items[*].metadata.name}'; }
uid() { "${k[@]}" get pod "$1" -o jsonpath='{.metadata.uid}' 2>/dev/null; }
status_of() { curl -s "$api/api/tests/$1" | sed -nE 's/.*"status":"([A-Z]+)".*/\1/p' | head -1; }

echo "== a database and the Secrets the resource names"
"${k[@]}" apply -f - >/dev/null <<EOT
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
EOT
"${k[@]}" create secret generic "$name-db" --from-literal=username=kates --from-literal=password=e2e-only \
  --from-literal=url="jdbc:postgresql://$name-db:5432/kates" >/dev/null
"${k[@]}" create secret generic "$name-kafka" >/dev/null \
  --from-literal=password="$(kubectl -n kates get secret kates-backend -o jsonpath='{.data.password}' | base64 -d)"
"${k[@]}" rollout status "deploy/$name-db" --timeout=120s >/dev/null && pass "PostgreSQL" || fail "PostgreSQL"

echo "== a Kates resource with three members"
tag=$(sed -nE 's/^appVersion:[[:space:]]*"?([^"]+)"?[[:space:]]*$/\1/p' charts/kates/Chart.yaml)
version=${KATES_TAG:-$tag-native}
"${k[@]}" apply -f - >/dev/null <<EOT
apiVersion: kates.io/v1alpha1
kind: Kates
metadata: {name: $name}
spec:
  version: "$version"
  ${KATES_IMAGE:+image: $KATES_IMAGE}
  replicas: 3
  database: {secretRef: {name: $name-db}}
  kafka:
    bootstrapServers: "$(helm_kates KATES_KAFKA_BOOTSTRAP_SERVERS)"
    securityProtocol: "$(helm_kates KATES_KAFKA_SECURITY_PROTOCOL)"
    sasl:
      mechanism: "$(helm_kates KATES_KAFKA_SASL_MECHANISM)"
      username: "$(helm_kates KATES_KAFKA_SASL_USERNAME)"
      passwordSecretRef: {name: $name-kafka, key: password}
  env:
    - {name: KATES_OUTBOX_TOPIC_REPLICATION_FACTOR, value: "1"}
    - {name: KATES_CHAOS_PROVIDER, value: noop}
EOT
"${k[@]}" wait --for=condition=Ready "kates/$name" --timeout=10m >/dev/null && pass "Ready" || fail "never Ready"
first=$(leader)
[ -n "$first" ] && pass "status.leader is $first" || fail "no leader in status"
check "only the leader's pod has the label" "$first" "$(labelled)"
check "the Service sends traffic to the leader alone" "$first" \
  "$(kubectl -n "$ns" get endpointslices -l "kubernetes.io/service-name=$name" -o jsonpath='{.items[*].endpoints[*].targetRef.name}')"
check "the CA is the resource's" Kates \
  "$("${k[@]}" get secret kates-quorum-ca -o jsonpath='{.metadata.ownerReferences[0].kind}')"
check "a certificate per member" 7 \
  "$("${k[@]}" get secret kates-quorum-tls -o json | jq '.data | length')"

api=http://localhost:18097
forward=
connect() { # connect [pod]: the Service, or one member
  [ -n "$forward" ] && kill "$forward" 2>/dev/null
  "${k[@]}" port-forward "${1:-service/$name}" 18097:8080 >/dev/null 2>&1 &
  forward=$!
  for _ in $(seq 1 15); do curl -sf "$api/q/health/ready" >/dev/null && break; sleep 1; done
}
trap 'kill $forward 2>/dev/null' EXIT

echo "== a follower refuses a write, the leader takes it"
follower=$("${k[@]}" get pods -l "app.kubernetes.io/instance=$name" -o name | grep -v "/$first$" | head -1)
connect "$follower"
check "a follower answers a run 503" 503 "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$api/api/tests" \
  -H 'Content-Type: application/json' -d '{"type":"LOAD","spec":{"numRecords":10,"replicationFactor":1,"minInsyncReplicas":1}}')"
connect
# About 60 s at 1000 records a second: still live when the leader dies.
id=$(curl -s -X POST "$api/api/tests" -H 'Content-Type: application/json' \
  -d '{"type":"LOAD","spec":{"numRecords":60000,"throughput":1000,"recordSize":128,"replicationFactor":1,"minInsyncReplicas":1}}' \
  | sed -nE 's/.*"id":"([^"]+)".*/\1/p')
[ -n "$id" ] && pass "run $id started on the leader" || fail "no run started"
sleep 5
check "the run is live" RUNNING "$(status_of "$id")"

echo "== the leader dies with the run live"
start=$(date +%s)
"${k[@]}" delete pod "$first" --grace-period=0 --force >/dev/null 2>&1
second=
for _ in $(seq 1 60); do
  second=$(leader)
  [ -n "$second" ] && [ "$second" != "$first" ] && [ "$(labelled)" = "$second" ] && break
  second=; sleep 1
done
[ -n "$second" ] && pass "$second leads, labelled, after $(( $(date +%s) - start )) s" || fail "no new leader within 60 s"
connect
for _ in $(seq 1 30); do [ "$(status_of "$id")" = FAILED ] && break; sleep 2; done
check "the dead leader's run is FAILED" FAILED "$(status_of "$id")"
reason=$(curl -s "$api/api/tests/$id" | jq -r '[.results[]?.error // empty] | join(" ")')
# Recovered by the new leader, or by the member itself if it came back first under its name.
case "$reason" in *"stopped leading"* | *"after server restart"*) pass "with its reason";; *) fail "no recovery reason: $reason";; esac
"${k[@]}" wait --for=condition=Ready "kates/$name" --timeout=5m >/dev/null && pass "Ready again with three members" \
  || fail "not Ready again"

echo "== a version change keeps a majority and replaces the leader last"
next=${KATES_NEXT_TAG:-$version-next}
leader_at_start=$(leader)
declare -A before
for pod in "$name-0" "$name-1" "$name-2"; do before[$pod]=$(uid "$pod"); done
order=()
fewest=3
"${k[@]}" patch kates "$name" --type merge -p "{\"spec\":{\"version\":\"$next\"}}" >/dev/null
for _ in $(seq 1 300); do
  ready=$("${k[@]}" get pods -l "app.kubernetes.io/instance=$name" \
    -o jsonpath='{range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' | grep -c True)
  [ "$ready" -lt "$fewest" ] && fewest=$ready
  for pod in "$name-0" "$name-1" "$name-2"; do
    now=$(uid "$pod")
    if [ "$now" != "${before[$pod]}" ] && [[ " ${order[*]} " != *" $pod "* ]]; then order+=("$pod"); fi
  done
  [ "$("${k[@]}" get kates "$name" -o jsonpath='{.status.version}')" = "$next" ] \
    && [ "$("${k[@]}" get kates "$name" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = True ] && break
  sleep 2
done
check "running $next" "$next" "$("${k[@]}" get kates "$name" -o jsonpath='{.status.version}')"
[ "$fewest" -ge 2 ] && pass "at least two members ready at every step ($fewest)" || fail "only $fewest ready at one step"
check "every member replaced" 3 "${#order[@]}"
check "the leader of the start replaced last (order: ${order[*]})" "$leader_at_start" "${order[${#order[@]}-1]:-}"

echo "== the leader cut off from its peers takes nothing more"
"${k[@]}" wait --for=condition=Ready "kates/$name" --timeout=5m >/dev/null
cut=$(leader)
cut_ip=$("${k[@]}" get pod "$cut" -o jsonpath='{.status.podIP}')
connect
cut_term=$(curl -s "$api/api/health" | jq '.quorum.term')
id=$(curl -s -X POST "$api/api/tests" -H 'Content-Type: application/json' \
  -d '{"type":"LOAD","spec":{"numRecords":60000,"throughput":1000,"recordSize":128,"replicationFactor":1,"minInsyncReplicas":1}}' \
  | sed -nE 's/.*"id":"([^"]+)".*/\1/p')
[ -n "$id" ] && pass "run $id started on $cut, term $cut_term" || fail "no run started on $cut"
# Port-forwards reach each member on 8080 whatever the cut, as the operator does.
declare -A port
forwards=()
n=18101
for pod in "$name-0" "$name-1" "$name-2"; do
  port[$pod]=$n
  "${k[@]}" port-forward "pod/$pod" "$n:8080" >/dev/null 2>&1 &
  forwards+=($!)
  n=$((n + 1))
done
trap 'kill $forward "${forwards[@]}" 2>/dev/null' EXIT
sleep 3
# Every packet to or from port 9300 of the leader dropped on every node, established streams too:
# a NetworkPolicy stops only new connections. Its database, Kafka and port 8080 stay reachable.
# Kind's node containers, from kind://docker/<cluster>/<container>.
nodes=$(kubectl get nodes -o jsonpath='{.items[*].spec.providerID}' | tr ' ' '\n' | sed 's|.*/||')
partition() { # partition -I|-D
  for node in $nodes; do
    for dir in -s -d; do
      docker exec "$node" iptables "$1" FORWARD "$dir" "$cut_ip" -p tcp -m multiport --ports 9300 -j DROP
    done
  done
}
health() { curl -s -m 2 "http://localhost:${port[$1]}/api/health"; }
partition -I
start=$(date +%s)
claims=$(mktemp)
elected=
unsafe=
second=
for _ in $(seq 1 60); do
  for pod in "$name-0" "$name-1" "$name-2"; do
    view=$(health "$pod" | jq -r '"\(.quorum.leader) \(.quorum.term) \(.quorum.memberId)"')
    read -r leads term member <<<"$view"
    [ "$leads" = true ] && echo "$term $member" >>"$claims"
    [ "$pod" != "$cut" ] && [ "$leads" = true ] && [ "$term" -gt "$cut_term" ] && second=$pod \
      && elected=${elected:-$(( $(date +%s) - start ))}
  done
  # An empty body: the leader answers 400, a member that no longer leads 503, before validation.
  code=$(curl -s -m 2 -o /dev/null -w '%{http_code}' -X POST "http://localhost:${port[$cut]}/api/tests" \
    -H 'Content-Type: application/json' -d '{}')
  [ -n "$second" ] && [ "$code" != 503 ] && unsafe="$unsafe $code"
  [ -n "$second" ] && [ "$(labelled)" = "$second" ] && break
  sleep 1
done
[ -n "$second" ] && pass "$second leads a later term $elected s after the cut, labelled after $(( $(date +%s) - start )) s" \
  || fail "no new leader within 60 s of the cut"
[ -z "$unsafe" ] && pass "$cut refused every write once another member led" || fail "$cut answered$unsafe with a new leader"
connect
for _ in $(seq 1 30); do [ "$(status_of "$id")" = FAILED ] && break; sleep 2; done
check "the cut-off leader's run is FAILED" FAILED "$(status_of "$id")"
# 60 s of load from its start: the cut-off member has finished it, and must not have marked it DONE.
sleep 60
check "and stays FAILED once the cut-off member ends it" FAILED "$(status_of "$id")"
check "$cut still refuses a write" 503 "$(curl -s -m 2 -o /dev/null -w '%{http_code}' -X POST \
  "http://localhost:${port[$cut]}/api/tests" -H 'Content-Type: application/json' -d '{}')"

partition -D
agreed=
for _ in $(seq 1 60); do
  agreed=$(for pod in "$name-0" "$name-1" "$name-2"; do health "$pod" | jq -r '.quorum.leaderId'; done | sort -u)
  [ "$(wc -l <<<"$agreed" | tr -d ' ')" = 1 ] && [ "$agreed" != null ] && break
  agreed=; sleep 1
done
[ -n "$agreed" ] && pass "healed, all three follow $agreed" || fail "the members disagree on the leader after the heal"
check "$cut rejoined as a follower" false "$(health "$cut" | jq '.quorum.leader')"
dup=$(sort -u "$claims" | awk '{print $1}' | uniq -d)
[ -z "$dup" ] && pass "one leader per term in every member's health" || fail "two leaders in term(s) $dup"
dup=$(for pod in "$name-0" "$name-1" "$name-2"; do
  "${k[@]}" logs "$pod" | sed -nE 's/.*Member ([^ ]+) leads term ([0-9]+).*/\2 \1/p'
done | sort -u | awk '{print $1}' | uniq -d)
[ -z "$dup" ] && pass "one leader per term in every member's log" || fail "two members took writes in term(s) $dup"
kill "${forwards[@]}" 2>/dev/null
rm -f "$claims"

echo "== deleting the resource removes what it made"
"${k[@]}" delete kates "$name" --timeout=120s >/dev/null
"${k[@]}" wait --for=delete "statefulset/$name" "secret/kates-quorum-ca" "secret/kates-quorum-tls" "pdb/$name" \
  "service/$name-0" "service/$name-1" "service/$name-2" --timeout=120s >/dev/null \
  && pass "StatefulSet, Secrets, budget and member Services gone" || fail "something was left"
for _ in $(seq 1 30); do [ -z "$("${k[@]}" get pvc -l "app.kubernetes.io/instance=$name" -o name)" ] && break; sleep 2; done
check "the members' volumes are gone" "" "$("${k[@]}" get pvc -l "app.kubernetes.io/instance=$name" -o name)"
"${k[@]}" delete deploy,service,secret "$name-db" >/dev/null 2>&1
"${k[@]}" delete secret "$name-kafka" >/dev/null 2>&1

exit $failed
