#!/usr/bin/env bash
# End-to-end checks of a running operator, against a real Kates API and Kafka.
#
#   KUBECONFIG=<scratch> operator/e2e.sh [namespace] [kafka namespace] [kafka name]
#
# The namespace must be watched and hold a working kates-connection, as
# operator/e2e-stack.sh leaves it. Each check
# creates its own resources under a unique name and asserts what the plan says.
set -uo pipefail

ns=${1:-payments}
kafka_ns=${2:-kafka}
kafka_name=${3:-krafter}
k=(kubectl -n "$ns")
failed=0
suffix=$(date +%s)

pass() { echo "  ok    $1"; }
fail() { echo "  FAIL  $1"; failed=1; }
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then pass "$1"; else fail "$1: expected '$2', got '$3'"; fi
}
get() { "${k[@]}" get "$1" "$2" -o jsonpath="$3" 2>/dev/null; }
condition() { get katestest "$1" "{.status.conditions[?(@.type=='$2')].status}/{.status.conditions[?(@.type=='$2')].reason}"; }
create() { # create <name> <spec as flow yaml>
  printf 'apiVersion: kates.io/v1alpha1\nkind: KatesTest\nmetadata: {name: %s}\nspec: %s\n' "$1" "$2" | "${k[@]}" apply -f - >/dev/null
}
finished() { "${k[@]}" wait --for=jsonpath='{.status.finishedAt}' "katestest/$1" --timeout="${2:-180s}" >/dev/null; }

echo "== a run that meets its SLA"
name=ok-$suffix
create "$name" '{type: LOAD, spec: {numRecords: 2000, recordSize: 128}, sla: {minRecordsProcessed: 100}}'
finished "$name"
check "phase" Succeeded "$(get katestest "$name" '{.status.phase}')"
check "Ready" True/RunCompleted "$(condition "$name" Ready)"
check "SlaPassed" True/SlaPassed "$(condition "$name" SlaPassed)"
check "no finalizer left" "" "$(get katestest "$name" '{.metadata.finalizers}')"
[ -n "$(get katestest "$name" '{.status.summary.totalRecords}')" ] && pass "summary" || fail "summary"
"${k[@]}" wait --for=condition=Ready "katestest/$name" --timeout=0 >/dev/null 2>&1
check "the gate exits 0" 0 $?

echo "== a missed SLA fails the gate"
name=sla-$suffix
create "$name" '{type: LOAD, spec: {numRecords: 2000, recordSize: 128}, sla: {minThroughputRecPerSec: 99999999}}'
finished "$name"
check "phase" Failed "$(get katestest "$name" '{.status.phase}')"
check "Stalled" True/SlaFailed "$(condition "$name" Stalled)"
check "SlaPassed" False/SlaFailed "$(condition "$name" SlaPassed)"
"${k[@]}" wait --for=condition=Ready "katestest/$name" --timeout=0 >/dev/null 2>&1
check "the gate exits 1" 1 $?

echo "== a request the API refuses, then edited"
name=rejected-$suffix
create "$name" '{type: LOAD, spec: {numRecords: -5}}'
finished "$name" 30s
check "Stalled" True/SpecRejected "$(condition "$name" Stalled)"
check "no run" "" "$(get katestest "$name" '{.status.runId}')"
"${k[@]}" patch katestest "$name" --type=merge -p '{"spec":{"spec":{"numRecords":1000}}}' >/dev/null
"${k[@]}" wait --for=condition=Ready "katestest/$name" --timeout=120s >/dev/null
check "the edit runs" Succeeded "$(get katestest "$name" '{.status.phase}')"
check "Stalled cleared" "/" "$(condition "$name" Stalled)"

echo "== what the CRD refuses"
out=$("${k[@]}" patch katestest "ok-$suffix" --type=merge -p '{"spec":{"spec":{"numRecords":1}}}' 2>&1)
[[ $out == *"immutable"* ]] && pass "a started spec is frozen" || fail "frozen: $out"
out=$(create typo-$suffix '{type: LOAD, spec: {numRecordz: 5}}' 2>&1)
[[ $out == *"unknown field"* ]] && pass "a mistyped key" || fail "typo: $out"
out=$(create both-$suffix '{type: LOAD, spec: {numRecords: 1}, scenario: {name: s}}' 2>&1)
[[ $out == *"exactly one"* ]] && pass "spec and scenario together" || fail "both: $out"

echo "== the TTL deletes a finished resource"
name=ttl-$suffix
create "$name" '{type: LOAD, spec: {numRecords: 500}, ttlSecondsAfterFinished: 5}'
finished "$name"
sleep 9
check "gone" "" "$(get katestest "$name" '{.metadata.name}')"

echo "== deleting a live run cancels it"
name=live-$suffix
create "$name" '{type: LOAD, spec: {numRecords: 500000000, recordSize: 1024}}'
for _ in $(seq 1 30); do
  id=$(get katestest "$name" '{.status.runId}')
  [ -n "$id" ] && [ "$(get katestest "$name" '{.status.phase}')" = Running ] && break
  sleep 2
done
[ -n "$id" ] && pass "running as $id" || fail "never ran"
"${k[@]}" delete katestest "$name" --timeout=90s >/dev/null
check "deleted" "" "$(get katestest "$name" '{.metadata.name}')"
"${k[@]}" get events --field-selector reason=Cancelled -o jsonpath='{.items[*].message}' | grep -q "$id" \
  && pass "Cancelled event" || fail "no Cancelled event for $id"

echo "== pause"
name=paused-$suffix
printf 'apiVersion: kates.io/v1alpha1\nkind: KatesTest\nmetadata: {name: %s, annotations: {kates.io/pause-reconciliation: "true"}}\nspec: {type: LOAD, spec: {numRecords: 500}}\n' "$name" | "${k[@]}" apply -f - >/dev/null
sleep 6
check "no run while paused" "" "$(get katestest "$name" '{.status.runId}')"
check "Reconciling" False/Paused "$(condition "$name" Reconciling)"
"${k[@]}" annotate katestest "$name" kates.io/pause-reconciliation- >/dev/null
finished "$name"
check "runs once resumed" Succeeded "$(get katestest "$name" '{.status.phase}')"

echo "== a compliance check, then its spec edited"
name=rules-$suffix
verdict() { "${k[@]}" wait --for=jsonpath="{.status.verdict}=$2" "kafkacompliance/$1" --timeout=120s >/dev/null; }
compliant() { get kafkacompliance "$1" "{.status.conditions[?(@.type=='Compliant')].status}/{.status.conditions[?(@.type=='Compliant')].reason}"; }
printf 'apiVersion: kates.io/v1alpha1\nkind: KafkaCompliance\nmetadata: {name: %s}\nspec: {interval: 1h, reference: {target: {namespace: %s, name: %s}, brokers: {count: {min: 1}}}}\n' \
  "$name" "$kafka_ns" "$kafka_name" | "${k[@]}" apply -f - >/dev/null
verdict "$name" PASS
check "Compliant" True/PASS "$(compliant "$name")"
[ -n "$(get kafkacompliance "$name" '{.status.nextCheckAt}')" ] && pass "next check set" || fail "no next check"
"${k[@]}" patch kafkacompliance "$name" --type=merge -p '{"spec":{"reference":{"brokers":{"count":{"min":9999}}}}}' >/dev/null
verdict "$name" FAIL
check "Compliant after the edit" False/FAIL "$(compliant "$name")"
[ -n "$(get kafkacompliance "$name" '{.status.failed[0].rule}')" ] && pass "failed rule named" || fail "no failed rule"
"${k[@]}" get events --field-selector reason=ComplianceFailed,involvedObject.name="$name" -o name | grep -q . \
  && pass "ComplianceFailed event" || fail "no ComplianceFailed event"
"${k[@]}" delete kafkacompliance "$name" --timeout=30s >/dev/null

exit $failed
