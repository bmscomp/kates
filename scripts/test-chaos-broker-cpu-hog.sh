#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# test-chaos-broker-cpu-hog.sh — Broker CPU Saturation Under Load
#
# Pegs the broker-gamma pod CPU at 90% for 120s while 6 parallel producers
# write 8 KB records to kates-results. Observes ISR shrink/expand behaviour
# and producer latency inflation.
#
# Hypothesis:
#   A CPU-starved follower falls behind replication. If lag exceeds
#   replica.lag.time.max.ms (30s), it is evicted from ISR. With min.isr=2
#   and acks=all, producers stall until ISR recovers. No data loss expected.
#
# What to watch in Grafana (open with: make monitoring-ui):
#   kafka_server_replicamanager_isrshrinks_total
#   kafka_server_replicamanager_isrexpands_total
#   kafka_network_request_totaltime_99thpercentile
#
# Usage:
#   ./scripts/test-chaos-broker-cpu-hog.sh
#   CPU_LOAD=95 CHAOS_DURATION=180 ./scripts/test-chaos-broker-cpu-hog.sh
# ─────────────────────────────────────────────────────────────────────────────
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

NAMESPACE="${NAMESPACE:-kafka}"
TOPIC="kates-results"
LABEL="chaos-cpu-hog"
CHAOS_DURATION="${CHAOS_DURATION:-120}"
CPU_CORES="${CPU_CORES:-1}"
CPU_LOAD="${CPU_LOAD:-90}"
PRODUCERS=6
WARMUP_SECS=20

bold "╔══════════════════════════════════════════════════════════════╗"
bold "║   Chaos Test: Broker CPU Hog Under Heavy Load                ║"
bold "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "  Topic:           ${TOPIC}"
echo "  Producers:       ${PRODUCERS} parallel (8 KB records, snappy)"
echo "  CPU target:      ${CPU_LOAD}% on ${CPU_CORES} core(s) of brokers-gamma"
echo "  Chaos duration:  ${CHAOS_DURATION}s"
echo ""

# ── Prerequisites ──────────────────────────────────────────────────────────
step "Step 1: Verifying prerequisites..."
if ! kubectl get chaosexperiment pod-cpu-hog -n "${NAMESPACE}" &>/dev/null; then
    error "ChaosExperiment 'pod-cpu-hog' not found. Run 'make chaos' first."
    exit 1
fi
info "✓ Prerequisites satisfied"

cleanup_previous_jobs "${LABEL}"
kubectl delete chaosengine "${LABEL}" -n "${NAMESPACE}" --ignore-not-found >/dev/null

# ── Retrieve SCRAM credentials ─────────────────────────────────────────────
PASS=$(kubectl get secret kates-backend -n kafka \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || echo "")

# ── Start parallel producers ───────────────────────────────────────────────
step "Step 2: Starting ${PRODUCERS} parallel producers (8 KB, snappy)..."
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
                --num-records 300000 \
                --record-size 8192 \
                --throughput 1000 \
                --producer-props \
                  bootstrap.servers=${BOOTSTRAP} \
                  security.protocol=SASL_PLAINTEXT \
                  sasl.mechanism=SCRAM-SHA-512 \
                  'sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-backend" password="${PASS}";' \
                  acks=all \
                  retries=2147483647 \
                  delivery.timeout.ms=120000 \
                  linger.ms=10 \
                  compression.type=snappy \
                  enable.idempotence=true
          resources:
            requests: { cpu: 300m, memory: 256Mi }
            limits:   { cpu: 600m, memory: 512Mi }
EOF
done
info "✓ ${PRODUCERS} producers started"

warn "Warming up for ${WARMUP_SECS}s..."
sleep "${WARMUP_SECS}"

# ── Inject CPU hog ────────────────────────────────────────────────────────
step "Step 3: Injecting pod-cpu-hog on brokers-gamma (${CPU_LOAD}% × ${CPU_CORES} cores for ${CHAOS_DURATION}s)..."
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
    applabel: "strimzi.io/pool-name=brokers-gamma"
    appkind: statefulset
  chaosServiceAccount: litmus-admin
  experiments:
    - name: pod-cpu-hog
      spec:
        components:
          env:
            - name: TOTAL_CHAOS_DURATION
              value: "${CHAOS_DURATION}"
            - name: CPU_CORES
              value: "${CPU_CORES}"
            - name: CPU_LOAD
              value: "${CPU_LOAD}"
            - name: PODS_AFFECTED_PERC
              value: "100"
            - name: CONTAINER_RUNTIME
              value: "containerd"
            - name: SOCKET_PATH
              value: "/run/containerd/containerd.sock"
EOF
info "✓ CPU hog injected"

# ── Watch ISR state every 5s ─────────────────────────────────────────────
step "Step 4: Monitoring ISR state during chaos (every 5s for ${CHAOS_DURATION}s)..."
ELAPSED=0
while [ "${ELAPSED}" -lt "${CHAOS_DURATION}" ]; do
    echo ""
    warn "─── $(date +%T) — ${ELAPSED}s elapsed ───"
    kubectl exec -n "${NAMESPACE}" krafter-brokers-sigma-2 -- \
        bin/kafka-topics.sh \
            --bootstrap-server localhost:9092 \
            --describe --topic "${TOPIC}" 2>/dev/null | \
        grep -E "Leader|Isr|UnderReplicated" || true
    sleep 5
    ELAPSED=$((ELAPSED + 5))
done

# ── ChaosResult ──────────────────────────────────────────────────────────
step "Step 5: Checking ChaosResult..."
RESULT=$(kubectl get chaosresult "${LABEL}-pod-cpu-hog" -n "${NAMESPACE}" \
    -o jsonpath='{.status.experimentStatus.verdict}' 2>/dev/null || echo "Pending")
if [ "${RESULT}" = "Pass" ]; then
    info "✓ ChaosResult: ${RESULT}"
else
    warn "⚠  ChaosResult: ${RESULT}"
fi

# ── Wait for producers ────────────────────────────────────────────────────
step "Step 6: Waiting for all producers to complete..."
wait_for_jobs "${LABEL}" "producer" "${PRODUCERS}" 300

print_job_results "${LABEL}" "producer" "${PRODUCERS}"

show_cleanup_hint "${LABEL}" ""
echo "   kubectl delete chaosengine ${LABEL} -n ${NAMESPACE}"
echo "   kubectl delete chaosresult ${LABEL}-pod-cpu-hog -n ${NAMESPACE} --ignore-not-found"
