#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

NAMESPACE="kafka"
CLUSTER_NAME="krafter"

info "=========================================================="
info "🔍 Verifying Kafka Generic Cluster & Policy Compliance..."
info "=========================================================="

# 1. Check for Kyverno Policy Violations
info "Step 1: Checking for Kyverno policy blocks in the ${NAMESPACE} namespace..."
if kubectl get events -n "${NAMESPACE}" | grep -i "kyverno" | grep -i "block\|reject\|fail" >/dev/null 2>&1; then
    warn "⚠️  Found potential Kyverno policy rejections in ${NAMESPACE} events:"
    kubectl get events -n "${NAMESPACE}" | grep -i "kyverno" | grep -i "block\|reject\|fail"
else
    info "✅ No Kyverno blocks detected in ${NAMESPACE} namespace events."
fi

# 2. Check Strimzi Operator Status
info "Step 2: Checking Strimzi Operator health..."
OPERATOR_POD=$(kubectl get pods -n "${NAMESPACE}" -l strimzi.io/kind=cluster-operator -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
if [ -n "$OPERATOR_POD" ]; then
    if kubectl get pod "$OPERATOR_POD" -n "${NAMESPACE}" -o jsonpath='{.status.phase}' | grep -q "Running"; then
        info "✅ Strimzi Operator is Running ($OPERATOR_POD)."
    else
        error "❌ Strimzi Operator pod ($OPERATOR_POD) is not Running. It may be blocked by a policy."
    fi
else
    error "❌ Strimzi Operator pod not found in ${NAMESPACE} namespace."
fi

# 3. Check Kafka Cluster CR Status
info "Step 3: Checking Kafka Cluster ('${CLUSTER_NAME}') readiness..."
if kubectl get kafka "${CLUSTER_NAME}" -n "${NAMESPACE}" >/dev/null 2>&1; then
    KAFKA_STATE=$(kubectl get kafka "${CLUSTER_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true)
    if [ "$KAFKA_STATE" = "True" ]; then
        info "✅ Kafka cluster '${CLUSTER_NAME}' is Ready."
    else
        warn "⚠️  Kafka cluster '${CLUSTER_NAME}' is NotReady."
        kubectl get kafka "${CLUSTER_NAME}" -n "${NAMESPACE}" -o jsonpath='{.status.conditions[?(@.type=="NotReady")].message}' 2>/dev/null || true
        echo ""
        error "❌ Cluster creation is blocked or pending."
    fi
else
    error "❌ Kafka CR '${CLUSTER_NAME}' does not exist in ${NAMESPACE}."
fi

# 4. Check NetworkPolicies & Connectivity
info "Step 4: Testing Network Connectivity (NetworkPolicy validation)..."
info "Spawning a temporary test pod in the 'default' namespace (should be blocked by default-deny)..."
if kubectl run policy-test-fail --rm -i --restart=Never --image=busybox --request-timeout=5s -n default -- nc -zv krafter-kafka-bootstrap.kafka 9092 >/dev/null 2>&1; then
    error "❌ NetworkPolicy failed: Default namespace can access Kafka brokers (Expected to be blocked)."
else
    info "✅ NetworkPolicy active: Default namespace is correctly blocked from accessing brokers."
fi

info "Spawning a temporary test pod in the 'kates' namespace (should be allowed)..."
if kubectl run policy-test-pass --rm -i --restart=Never --image=busybox --request-timeout=10s -n kates -- nc -zv krafter-kafka-bootstrap.kafka 9092 >/dev/null 2>&1; then
    info "✅ NetworkPolicy active: 'kates' namespace can successfully access Kafka brokers."
else
    warn "⚠️  'kates' namespace could not reach Kafka brokers. Ensure network policies or pods are healthy."
fi

info "=========================================================="
info "🎉 Cluster Policy Verification Completed!"
info "=========================================================="
