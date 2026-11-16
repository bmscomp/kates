#!/usr/bin/env bash
# A quorum member whose node is gone gets a new volume on another node, and the quorum is whole
# again, with no one acting. Deletes a Kind worker node, so it needs a Kind cluster of three
# nodes or more, not CI's one; the node is started again at the end.
#
#   KUBECONFIG=<scratch> operator/e2e-node-loss.sh <namespace> <kates>
#
# On a Ready Kates resource with replicas 3, as operator/e2e-quorum.sh makes, under an operator
# built from this tree.
set -uo pipefail

ns=${1:?namespace}
name=${2:?Kates resource}
k=(kubectl -n "$ns")
failed=0

pass() { echo "  ok    $1"; }
fail() { echo "  FAIL  $1"; failed=1; }
node_of() { "${k[@]}" get pod "$1" -o jsonpath='{.spec.nodeName}' 2>/dev/null && echo; }
claim_uid() { "${k[@]}" get pvc "quorum-$1" -o jsonpath='{.metadata.uid}' 2>/dev/null; }
leader() { "${k[@]}" get kates "$name" -o jsonpath='{.status.leader}'; }
ready() { "${k[@]}" get kates "$name" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'; }

echo "== three members, one a node"
"${k[@]}" wait --for=condition=Ready "kates/$name" --timeout=5m >/dev/null || { fail "not Ready"; exit 1; }
members=("$name-0" "$name-1" "$name-2")
spread() { for pod in "${members[@]}"; do node_of "$pod"; done | sort -u | wc -l | tr -d ' '; }
# Two on the lost node would take the majority, and nothing would be replaced: by design. The
# anti-affinity is preferred, and a local volume pins a member to its node: on two nodes, a
# follower sharing one gets a new volume, which the scheduler places on the third.
if [ "$(spread)" != 3 ]; then
  shared=
  for pod in "${members[@]}"; do
    [ "$pod" = "$(leader)" ] && continue
    for other in "${members[@]}"; do
      [ "$other" != "$pod" ] && [ "$(node_of "$other")" = "$(node_of "$pod")" ] && shared=$pod
    done
    [ -n "$shared" ] && break
  done
  if [ -n "$shared" ]; then
    "${k[@]}" delete pvc "quorum-$shared" --wait=false >/dev/null
    "${k[@]}" delete pod "$shared" >/dev/null
    for _ in $(seq 1 90); do
      [ "$(spread)" = 3 ] && [ "$(ready)" = True ] && break
      sleep 2
    done
  fi
fi
nodes=$(spread)
[ "$nodes" = 3 ] && pass "spread over three nodes${shared:+, $shared on a new volume}" \
  || { fail "on $nodes nodes after giving ${shared:-no member} a new volume"; exit 1; }

# A follower on a worker: the control plane's node holds the API server. Not on the database's
# node either: without PostgreSQL, the leader hands over by design.
lead=$(leader)
db_node=$("${k[@]}" get pods -l "app=$name-db" -o jsonpath='{.items[0].spec.nodeName}' 2>/dev/null)
victim=
for pod in "${members[@]}"; do
  node=$(node_of "$pod")
  [ "$pod" != "$lead" ] && [ "$node" != "$db_node" ] \
    && [[ "$(kubectl get node "$node" -o jsonpath='{.metadata.labels}')" != *control-plane* ]] \
    && victim=$pod && break
done
[ -n "$victim" ] || { fail "no follower on a worker node without the database ($db_node)"; exit 1; }
node=$(node_of "$victim")
container=$(kubectl get node "$node" -o jsonpath='{.spec.providerID}' | sed 's|.*/||')
before=$(claim_uid "$victim")
trap 'docker start "$container" >/dev/null 2>&1' EXIT

echo "== $node, holding $victim and its volume, is gone"
start=$(date +%s)
docker stop "$container" >/dev/null
kubectl delete node "$node" --wait=false >/dev/null
moved=
changed=
for _ in $(seq 1 240); do
  # Sampled while the victim is not back: once back on an empty log, it has the leader step down.
  current=$(leader)
  [ -n "$current" ] && [ "$current" != "$lead" ] && changed=$current
  now=$(claim_uid "$victim")
  [ -n "$now" ] && [ "$now" != "$before" ] && [ -n "$(node_of "$victim")" ] && [ "$(node_of "$victim")" != "$node" ] \
    && [ "$("${k[@]}" get pod "$victim" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}')" = True ] \
    && moved=1 && break
  sleep 2
done
[ -n "$moved" ] && pass "$victim runs on $(node_of "$victim") with a new volume, $(( $(date +%s) - start )) s after the loss" \
  || fail "$victim was not replaced within 8 minutes ($("${k[@]}" get pod "$victim" -o jsonpath='{.status.phase}'))"
[ -z "$changed" ] && pass "$lead led while $victim was gone" || fail "the leader changed from $lead to '$changed'"
for _ in $(seq 1 60); do [ "$(ready)" = True ] && break; sleep 2; done
[ "$(ready)" = True ] && pass "Ready with three members" \
  || fail "not Ready: $("${k[@]}" get kates "$name" -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}')"
# Ready before caught up: the pod's readiness does not wait for the log. Under the same leader,
# the member waits 15 s, then has it step down (EmptyLogCatchUp), and the next leader sends the log.
for _ in $(seq 1 45); do
  applied=$(kubectl get --raw "/api/v1/namespaces/$ns/services/$victim:8080/proxy/api/health" | jq '.quorum.appliedIndex')
  [ "${applied:--1}" -ge 0 ] && break
  sleep 2
done
if [ "${applied:--1}" -ge 0 ]; then
  pass "$victim caught up (applied index $applied), $(leader) leads"
else
  fail "$victim has not caught up: '$applied'"
  for pod in "$lead" "$victim"; do
    echo "--- $pod"
    "${k[@]}" logs "$pod" --since=300s | grep -E 'ratis|Quorum' | cut -c1-800 | tail -60
  done
fi

echo "== $node comes back"
docker start "$container" >/dev/null
trap - EXIT
for _ in $(seq 1 60); do kubectl get node "$node" >/dev/null 2>&1 && break; sleep 2; done
kubectl wait --for=condition=Ready "node/$node" --timeout=3m >/dev/null && pass "$node registered again" \
  || fail "$node did not come back"

exit $failed
