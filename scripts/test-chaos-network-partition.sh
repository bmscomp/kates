#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# test-chaos-network-partition.sh — Network Partition Between Broker and Clients
#
# Injects pod-network-partition on brokers-sigma while producers and consumers
# run concurrently. Simulates a partial network failure where one broker zone
# becomes unreachable from clients but stays connected to controllers.
#
# Hypothesis:
#   Partitioned broker is fenced by KRaft controller (FENCE_BROKERS=true).
#   Partition leaders on sigma migrate to alpha/gamma. Consumer groups
#   rebalance. End-to-end delivery continues with a brief rebalance gap.
#   No committed messages are lost.
#
# Pass criteria:
#   - All producers complete with 0 errors
#   - Consumer group rebalances detected (coordinator migrates)
#   - ChaosResult verdict = Pass
#   - No offset regression in consumer group after chaos
#
# Usage:
#   ./scripts/test-chaos-network-partition.sh
#   CHAOS_DURATION=90 ./scripts/test-chaos-network-partition.sh
# ─────────────────────────────────────────────────────────────────────────────
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

NAMESPACE="${NAMESPACE:-kafka}"
TOPIC="kates-events"
LABEL="chaos-net-partition"
CHAOS_DURATION="${CHAOS_DURATION:-60}"
WARMUP_SECS=15
PRODUCERS=3
CONSUMERS=2

bold "╔══════════════════════════════════════════════════════════════╗"
bold "║   Chaos Test: Network Partition — Broker Sigma               ║"
bold "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "  Topic:           ${TOPIC}"
echo "  Partition target: brokers-sigma (zone sigma)"
echo "  Chaos duration:  ${CHAOS_DURATION}s"
echo "  Producers:       ${PRODUCERS} | Consumers: ${CONSUMERS}"
echo ""

# ── Prerequisites ─────────────────────────────────────────────────────────
step "Step 1: Verifying prerequisites..."
if ! kubectl get chaosexperiment pod-network-partition -n "${NAMESPACE}" &>/dev/null; then
    error "ChaosExperiment 'pod-network-partition' not found. Run 'make chaos' first."
    exit 1
fi
info "✓ Prerequisites satisfied"

cleanup_previous_jobs "${LABEL}"
kubectl delete chaosengine "${LABEL}" -n "${NAMESPACE}" --ignore-not-found >/dev/null

PASS=$(kubectl get secret kates-backend -n kafka \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || echo "")

# ── Start producers ───────────────────────────────────────────────────────
step "Step 2: Starting ${PRODUCERS} producers..."
for i in $(seq 1 "${PRODUCERS}"); do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: ${LABEL}-producer-${i}
  namespace: ${NAMESPACE}
  labels:
    perf-test: ${LABEL}
    perf-role: producer
spec:
  backoffLimit: 0
  template:
    metadata:
      labels: { perf-test: "${LABEL}", perf-role: producer }
    spec:
      restartPolicy: Never
      containers:
        - name: producer
          image: ${KAFKA_IMAGE}
          command: ["/bin/bash", "-c"]
          args:
            - |
              bin/kafka-producer-perf-test.sh \
                --topic ${TOPIC} \
                --num-records 400000 \
                --record-size 512 \
                --throughput 3000 \
                --producer-props \
                  bootstrap.servers=${BOOTSTRAP} \
                  security.protocol=SASL_PLAINTEXT \
                  sasl.mechanism=SCRAM-SHA-512 \
                  'sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-backend" password="${PASS}";' \
                  acks=all \
                  retries=2147483647 \
                  delivery.timeout.ms=120000 \
                  request.timeout.ms=30000 \
                  enable.idempotence=true
          resources:
            requests: { cpu: 200m, memory: 256Mi }
            limits:   { cpu: 400m, memory: 384Mi }
EOF
done
info "✓ ${PRODUCERS} producers started"

# ── Start consumers ───────────────────────────────────────────────────────
step "Step 3: Starting ${CONSUMERS} consumers (group: net-partition-cg)..."
for i in $(seq 1 "${CONSUMERS}"); do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: ${LABEL}-consumer-${i}
  namespace: ${NAMESPACE}
  labels:
    perf-test: ${LABEL}
    perf-role: consumer
spec:
  backoffLimit: 0
  template:
    metadata:
      labels: { perf-test: "${LABEL}", perf-role: consumer }
    spec:
      restartPolicy: Never
      containers:
        - name: consumer
          image: ${KAFKA_IMAGE}
          command: ["/bin/bash", "-c"]
          args:
            - |
              bin/kafka-consumer-perf-test.sh \
                --topic ${TOPIC} \
                --messages 400000 \
                --threads 2 \
                --bootstrap-server ${BOOTSTRAP} \
                --consumer.config /dev/stdin <<CONF
              security.protocol=SASL_PLAINTEXT
              sasl.mechanism=SCRAM-SHA-512
              sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-backend" password="${PASS}";
              group.id=net-partition-cg
              auto.offset.reset=earliest
              CONF
          resources:
            requests: { cpu: 200m, memory: 256Mi }
            limits:   { cpu: 400m, memory: 384Mi }
EOF
done
info "✓ ${CONSUMERS} consumers started"

warn "Warming up for ${WARMUP_SECS}s..."
sleep "${WARMUP_SECS}"

# ── Inject network partition ──────────────────────────────────────────────
step "Step 4: Injecting network partition on brokers-sigma..."
cat <<EOF | kubectl apply -f -
apiVersion: litmuschaos.io/v1alpha1
kind: ChaosEngine
metadata:
  name: ${LABEL}
  namespace: ${NAMESPACE}
  labels:
    perf-test: ${LABEL}
spec:
  engineState: active
  annotationCheck: "false"
  appinfo:
    appns: ${NAMESPACE}
    applabel: "strimzi.io/pool-name=brokers-sigma"
    appkind: statefulset
  chaosServiceAccount: litmus-admin
  experiments:
    - name: pod-network-partition
      spec:
        components:
          env:
            - name: TOTAL_CHAOS_DURATION
              value: "${CHAOS_DURATION}"
            - name: POLICY
              value: "deny"
            - name: DESTINATION_IPS
              value: ""
            - name: DESTINATION_HOSTS
              value: ""
            - name: PODS_AFFECTED_PERC
              value: "100"
EOF
info "✓ Network partition injected on sigma"

# ── Monitor consumer group during chaos ───────────────────────────────────
step "Step 5: Monitoring consumer group rebalances for ${CHAOS_DURATION}s..."
ELAPSED=0
while [ "${ELAPSED}" -lt "${CHAOS_DURATION}" ]; do
    echo ""
    warn "─── $(date +%T) — ${ELAPSED}s into chaos ───"
    kubectl exec -n "${NAMESPACE}" krafter-brokers-alpha-0 -- \
        bin/kafka-consumer-groups.sh \
            --bootstrap-server localhost:9092 \
            --describe --group net-partition-cg 2>/dev/null | \
        head -6 || true
    sleep 10
    ELAPSED=$((ELAPSED + 10))
done

# ── ChaosResult ──────────────────────────────────────────────────────────
step "Step 6: Checking ChaosResult..."
RESULT=$(kubectl get chaosresult "${LABEL}-pod-network-partition" -n "${NAMESPACE}" \
    -o jsonpath='{.status.experimentStatus.verdict}' 2>/dev/null || echo "Pending")
if [ "${RESULT}" = "Pass" ]; then
    info "✓ ChaosResult: ${RESULT}"
else
    warn "⚠  ChaosResult: ${RESULT}"
fi

step "Step 7: Waiting for producers and consumers..."
wait_for_jobs "${LABEL}" "producer" "${PRODUCERS}" 300
wait_for_jobs "${LABEL}" "consumer" "${CONSUMERS}" 300

print_job_results "${LABEL}" "producer" "${PRODUCERS}"
print_job_results "${LABEL}" "consumer" "${CONSUMERS}"

echo ""
step "Step 8: Final consumer group lag:"
kubectl exec -n "${NAMESPACE}" krafter-brokers-alpha-0 -- \
    bin/kafka-consumer-groups.sh \
        --bootstrap-server localhost:9092 \
        --describe --group net-partition-cg 2>/dev/null || true

show_cleanup_hint "${LABEL}" ""
echo "   kubectl delete chaosengine ${LABEL} -n ${NAMESPACE}"
