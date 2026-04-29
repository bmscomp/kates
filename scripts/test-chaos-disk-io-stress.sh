#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# test-chaos-disk-io-stress.sh — Broker Disk I/O Saturation Under Load
#
# Stresses the disk I/O subsystem on brokers-alpha to simulate a slow-disk
# scenario (e.g. a degraded NVMe or HDD under compaction). Combines with a
# high-throughput producer to observe segment flush latency, potential leader
# moves, and consumer lag accumulation.
#
# Hypothesis:
#   Disk I/O saturation causes log segment flush latency to spike. The broker
#   may fall behind replication and be removed from ISR. If it is the leader,
#   a leader election occurs. Producers with acks=all stall until ISR recovers.
#   No data is lost if the producer uses idempotent writes with high retries.
#
# Usage:
#   ./scripts/test-chaos-disk-io-stress.sh
#   IO_WORKERS=4 CHAOS_DURATION=90 ./scripts/test-chaos-disk-io-stress.sh
# ─────────────────────────────────────────────────────────────────────────────
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

NAMESPACE="${NAMESPACE:-kafka}"
TOPIC="kates-audit"
LABEL="chaos-io-stress"
CHAOS_DURATION="${CHAOS_DURATION:-90}"
IO_WORKERS="${IO_WORKERS:-2}"
WARMUP_SECS=20
PRODUCERS=4

bold "╔══════════════════════════════════════════════════════════════╗"
bold "║   Chaos Test: Disk I/O Stress on Broker Alpha                ║"
bold "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "  Topic:           ${TOPIC}"
echo "  I/O workers:     ${IO_WORKERS} (dd-based sequential I/O)"
echo "  Chaos duration:  ${CHAOS_DURATION}s"
echo "  Producers:       ${PRODUCERS} (large records, no compression)"
echo ""

# ── Prerequisites ─────────────────────────────────────────────────────────
step "Step 1: Verifying prerequisites..."
if ! kubectl get chaosexperiment pod-io-stress -n "${NAMESPACE}" &>/dev/null; then
    error "ChaosExperiment 'pod-io-stress' not found. Run 'make chaos' first."
    exit 1
fi
info "✓ Prerequisites satisfied"

cleanup_previous_jobs "${LABEL}"
kubectl delete chaosengine "${LABEL}" -n "${NAMESPACE}" --ignore-not-found >/dev/null

PASS=$(kubectl get secret kates-backend -n kafka \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || echo "")

# ── Start high-throughput large-record producers ───────────────────────────
step "Step 2: Starting ${PRODUCERS} large-record producers (16 KB, no compression)..."
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
                --num-records 200000 \
                --record-size 16384 \
                --throughput 500 \
                --producer-props \
                  bootstrap.servers=${BOOTSTRAP} \
                  security.protocol=SASL_PLAINTEXT \
                  sasl.mechanism=SCRAM-SHA-512 \
                  'sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-backend" password="${PASS}";' \
                  acks=all \
                  retries=2147483647 \
                  delivery.timeout.ms=120000 \
                  compression.type=none \
                  enable.idempotence=true
          resources:
            requests: { cpu: 200m, memory: 256Mi }
            limits:   { cpu: 400m, memory: 512Mi }
EOF
done
info "✓ ${PRODUCERS} producers started"

warn "Warming up for ${WARMUP_SECS}s..."
sleep "${WARMUP_SECS}"

# ── Inject disk I/O stress ────────────────────────────────────────────────
step "Step 3: Injecting pod-io-stress on brokers-alpha (${IO_WORKERS} workers for ${CHAOS_DURATION}s)..."
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
    - name: pod-io-stress
      spec:
        components:
          env:
            - name: TOTAL_CHAOS_DURATION
              value: "${CHAOS_DURATION}"
            - name: NUMBER_OF_WORKERS
              value: "${IO_WORKERS}"
            - name: FILESYSTEM_UTILIZATION_PERCENTAGE
              value: "80"
            - name: PODS_AFFECTED_PERC
              value: "100"
            - name: CONTAINER_RUNTIME
              value: "containerd"
            - name: SOCKET_PATH
              value: "/run/containerd/containerd.sock"
EOF
info "✓ Disk I/O stress injected on brokers-alpha"

# ── Watch log flush latency ───────────────────────────────────────────────
step "Step 4: Watching log flush latency + leader state for ${CHAOS_DURATION}s..."
ELAPSED=0
while [ "${ELAPSED}" -lt "${CHAOS_DURATION}" ]; do
    echo ""
    warn "─── $(date +%T) — ${ELAPSED}s elapsed ───"
    kubectl exec -n "${NAMESPACE}" krafter-brokers-alpha-0 -- \
        bin/kafka-topics.sh \
            --bootstrap-server localhost:9092 \
            --describe --topic "${TOPIC}" 2>/dev/null | \
        grep -E "Leader|Isr|UnderReplicated" || true
    sleep 5
    ELAPSED=$((ELAPSED + 5))
done

step "Step 5: Checking ChaosResult..."
RESULT=$(kubectl get chaosresult "${LABEL}-pod-io-stress" -n "${NAMESPACE}" \
    -o jsonpath='{.status.experimentStatus.verdict}' 2>/dev/null || echo "Pending")
if [ "${RESULT}" = "Pass" ]; then
    info "✓ ChaosResult: ${RESULT}"
else
    warn "⚠  ChaosResult: ${RESULT}"
fi

step "Step 6: Waiting for producers..."
wait_for_jobs "${LABEL}" "producer" "${PRODUCERS}" 300

print_job_results "${LABEL}" "producer" "${PRODUCERS}"

show_cleanup_hint "${LABEL}" ""
echo "   kubectl delete chaosengine ${LABEL} -n ${NAMESPACE}"
