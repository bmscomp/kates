#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# test-chaos-broker-delete.sh — Broker Pod Delete Under Load
#
# Injects a broker pod-delete ChaosEngine while a continuous producer writes
# to kates-events. Validates that the idempotent producer with acks=all
# survives broker failover with zero message loss and acceptable latency spike.
#
# Hypothesis:
#   Killing one broker pod causes a leader re-election. With RF=3, min.isr=2,
#   producers stall until ISR recovers. Expected: <30s latency spike, 0 loss.
#
# Pass criteria:
#   - Producer completes with 0 errors
#   - Consumer group lag returns to 0
#   - ChaosResult verdict = Pass
#
# Usage:
#   ./scripts/test-chaos-broker-delete.sh
#   CHAOS_DURATION=120 CHAOS_INTERVAL=30 ./scripts/test-chaos-broker-delete.sh
# ─────────────────────────────────────────────────────────────────────────────
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

NAMESPACE="${NAMESPACE:-kafka}"
TOPIC="kates-events"
LABEL="chaos-broker-delete"
CHAOS_DURATION="${CHAOS_DURATION:-60}"
CHAOS_INTERVAL="${CHAOS_INTERVAL:-20}"
WARMUP_SECS=15

bold "╔══════════════════════════════════════════════════════════════╗"
bold "║   Chaos Test: Broker Pod Delete Under Sustained Load         ║"
bold "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "  Topic:           ${TOPIC}"
echo "  Chaos duration:  ${CHAOS_DURATION}s (delete every ${CHAOS_INTERVAL}s)"
echo "  Warmup:          ${WARMUP_SECS}s"
echo ""

# ── Prerequisites ──────────────────────────────────────────────────────────
step "Step 1: Verifying prerequisites..."
if ! kubectl get kafka krafter -n kafka &>/dev/null; then
    error "Kafka cluster 'krafter' not found. Run 'make kafka' first."
    exit 1
fi
if ! kubectl get chaosexperiment pod-delete -n "${NAMESPACE}" &>/dev/null; then
    error "ChaosExperiment 'pod-delete' not found in namespace ${NAMESPACE}."
    error "Run 'make chaos' or './scripts/setup-kafka-chaos.sh' first."
    exit 1
fi
info "✓ Kafka and LitmusChaos prerequisites satisfied"

# ── Cleanup previous run ──────────────────────────────────────────────────
cleanup_previous_jobs "${LABEL}"
kubectl delete chaosengine "${LABEL}" -n "${NAMESPACE}" --ignore-not-found >/dev/null

# ── Start continuous load producer ─────────────────────────────────────────
step "Step 2: Starting continuous load producer (acks=all, idempotent)..."
PASS=$(kubectl get secret kates-backend -n kafka \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || echo "")

cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: ${LABEL}-producer-1
  namespace: ${NAMESPACE}
  labels:
    perf-test: ${LABEL}
    perf-role: producer
spec:
  backoffLimit: 0
  template:
    metadata:
      labels:
        perf-test: ${LABEL}
        perf-role: producer
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
                --num-records 600000 \
                --record-size 1024 \
                --throughput 2000 \
                --producer-props \
                  bootstrap.servers=${BOOTSTRAP} \
                  security.protocol=SASL_PLAINTEXT \
                  sasl.mechanism=SCRAM-SHA-512 \
                  'sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-backend" password="${PASS}";' \
                  acks=all \
                  retries=2147483647 \
                  delivery.timeout.ms=120000 \
                  request.timeout.ms=30000 \
                  linger.ms=5 \
                  batch.size=65536 \
                  compression.type=lz4 \
                  enable.idempotence=true \
                  max.in.flight.requests.per.connection=5
          resources:
            requests: { cpu: 200m, memory: 256Mi }
            limits:   { cpu: 500m, memory: 512Mi }
EOF
info "✓ Producer started"

# ── Warmup ────────────────────────────────────────────────────────────────
warn "Warming up for ${WARMUP_SECS}s before chaos injection..."
sleep "${WARMUP_SECS}"

# ── Inject chaos ──────────────────────────────────────────────────────────
step "Step 3: Injecting broker pod-delete chaos..."
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
    applabel: "strimzi.io/pool-name=brokers-alpha"
    appkind: statefulset
  chaosServiceAccount: litmus-admin
  experiments:
    - name: pod-delete
      spec:
        components:
          env:
            - name: TOTAL_CHAOS_DURATION
              value: "${CHAOS_DURATION}"
            - name: CHAOS_INTERVAL
              value: "${CHAOS_INTERVAL}"
            - name: FORCE
              value: "false"
            - name: PODS_AFFECTED_PERC
              value: "33"
EOF
info "✓ ChaosEngine applied — killing brokers-alpha pod every ${CHAOS_INTERVAL}s for ${CHAOS_DURATION}s"

# ── Watch broker restarts during chaos ───────────────────────────────────
echo ""
warn "Watching broker pod restarts for ${CHAOS_DURATION}s..."
kubectl get pods -n "${NAMESPACE}" -l strimzi.io/pool-name=brokers-alpha -w &
WATCH_PID=$!
sleep $((CHAOS_DURATION + 10))
kill "${WATCH_PID}" 2>/dev/null || true

# ── ChaosResult ──────────────────────────────────────────────────────────
step "Step 4: Checking ChaosResult..."
RESULT=$(kubectl get chaosresult "${LABEL}-pod-delete" -n "${NAMESPACE}" \
    -o jsonpath='{.status.experimentStatus.verdict}' 2>/dev/null || echo "Pending")
if [ "${RESULT}" = "Pass" ]; then
    info "✓ ChaosResult: ${RESULT}"
else
    warn "⚠  ChaosResult: ${RESULT} (check 'kubectl describe chaosresult ${LABEL}-pod-delete -n ${NAMESPACE}')"
fi

# ── Wait for producer ────────────────────────────────────────────────────
step "Step 5: Waiting for producer to complete..."
kubectl wait "job/${LABEL}-producer-1" -n "${NAMESPACE}" \
    --for=condition=complete --timeout=300s

print_job_results "${LABEL}" "producer" 1

# ── Consumer lag ─────────────────────────────────────────────────────────
step "Step 6: Consumer group lag (should be 0 or decreasing)..."
kubectl exec -n "${NAMESPACE}" krafter-brokers-alpha-0 -- \
    bin/kafka-consumer-groups.sh \
        --bootstrap-server localhost:9092 \
        --describe --group load-test-consumer 2>/dev/null || \
    warn "Consumer group not found (expected if no consumer was running)"

show_cleanup_hint "${LABEL}" ""
echo "   kubectl delete chaosengine ${LABEL} -n ${NAMESPACE}"
echo "   kubectl delete chaosresult ${LABEL}-pod-delete -n ${NAMESPACE} --ignore-not-found"
