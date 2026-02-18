#!/bin/bash
# Shared harness for Kafka performance test scripts.
# Source after common.sh and versions.env:
#   source "${SCRIPT_DIR}/test-common.sh"

NAMESPACE="${NAMESPACE:-kafka}"
IMAGE="${KAFKA_IMAGE}"
BOOTSTRAP="${BOOTSTRAP}"

cleanup_previous_jobs() {
    local label=$1
    local count
    count=$(kubectl get jobs -n "${NAMESPACE}" -l "perf-test=${label}" --no-headers 2>/dev/null | wc -l | tr -d ' ')
    if [ "${count}" -gt 0 ]; then
        warn "Cleaning up ${count} previous '${label}' test job(s)..."
        kubectl delete jobs -n "${NAMESPACE}" -l "perf-test=${label}" --ignore-not-found > /dev/null
        sleep 2
    fi
}

create_test_topic() {
    local topic=$1
    local partitions=${2:-3}
    local replicas=${3:-3}

    info "Creating topic '${topic}'..."
    kubectl exec -n "${NAMESPACE}" krafter-pool-alpha-0 -- \
      bin/kafka-topics.sh --create --if-not-exists \
        --bootstrap-server localhost:9092 \
        --topic "${topic}" \
        --partitions "${partitions}" \
        --replication-factor "${replicas}" \
        --config min.insync.replicas=2
}

wait_for_jobs() {
    local label=$1
    local role=$2
    local count=$3
    local timeout=${4:-600}

    warn "Waiting for ${role}s to complete (timeout ${timeout}s)..."
    for i in $(seq 1 "${count}"); do
        kubectl wait --for=condition=complete --timeout="${timeout}s" \
            "job/${label}-${role}-${i}" -n "${NAMESPACE}" 2>/dev/null || true
    done
}

print_job_results() {
    local label=$1
    local role=$2
    local count=$3

    info "${role^} Results:"
    for i in $(seq 1 "${count}"); do
        warn "--- ${role^} ${i} ---"
        kubectl logs -n "${NAMESPACE}" "job/${label}-${role}-${i}" 2>/dev/null | tail -5
    done
}

show_cleanup_hint() {
    local label=$1
    local topic=$2

    echo ""
    info "✅ Test completed!"
    echo ""
    echo "Cleanup:"
    echo "  kubectl delete jobs -n ${NAMESPACE} -l perf-test=${label}"
    if [ -n "${topic}" ]; then
        echo "  kubectl exec -n ${NAMESPACE} krafter-pool-alpha-0 -- bin/kafka-topics.sh --delete --bootstrap-server localhost:9092 --topic ${topic}"
    fi
}
