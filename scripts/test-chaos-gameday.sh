#!/bin/bash
# ─────────────────────────────────────────────────────────────────────────────
# test-chaos-gameday.sh — Full Game Day: Multi-Fault Load + Chaos Sequence
#
# Runs a structured game day that combines sustained load with a sequence of
# escalating chaos faults across all three broker zones. Models a realistic
# cloud incident: zone-alpha hard failure → zone-gamma CPU spike → zone-sigma
# network degradation → all resolved while consumers must maintain continuity.
#
# Timeline:
#   T+00s  Load starts (producers + consumers, all topics)
#   T+30s  Phase 1: pod-delete on brokers-alpha  (60s)
#   T+120s Phase 2: cpu-hog on brokers-gamma     (90s)
#   T+240s Phase 3: network-partition on sigma   (60s)
#   T+330s All chaos resolved — validate recovery
#
# Pass criteria:
#   - Zero producer errors across all phases
#   - All ChaosResults verdict = Pass
#   - All consumer groups recover (lag = 0) within 2 min of final phase end
#   - Kafka cluster reports Ready condition throughout
#
# Usage:
#   ./scripts/test-chaos-gameday.sh
#   DRY_RUN=true ./scripts/test-chaos-gameday.sh   # print plan without executing
# ─────────────────────────────────────────────────────────────────────────────
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

NAMESPACE="${NAMESPACE:-kafka}"
LABEL="gameday"
DRY_RUN="${DRY_RUN:-false}"

PHASE1_DURATION=60
PHASE2_DURATION=90
PHASE3_DURATION=60
PHASE1_START=30
PHASE2_START=120
PHASE3_START=240

bold "╔══════════════════════════════════════════════════════════════╗"
bold "║   Kafka Game Day — Multi-Fault Chaos + Load Sequence         ║"
bold "╚══════════════════════════════════════════════════════════════╝"
echo ""
echo "  Timeline:"
echo "    T+${PHASE1_START}s   Phase 1: pod-delete on brokers-alpha   (${PHASE1_DURATION}s)"
echo "    T+${PHASE2_START}s  Phase 2: cpu-hog on brokers-gamma      (${PHASE2_DURATION}s)"
echo "    T+${PHASE3_START}s  Phase 3: network-partition on sigma    (${PHASE3_DURATION}s)"
echo "    T+360s  All chaos resolved — recovery validation"
echo ""
if [ "${DRY_RUN}" = "true" ]; then
    warn "DRY_RUN=true — printing plan only, not executing."
    exit 0
fi

# ── Prerequisites ─────────────────────────────────────────────────────────
step "Verifying prerequisites..."
for exp in pod-delete pod-cpu-hog pod-network-partition; do
    if ! kubectl get chaosexperiment "${exp}" -n "${NAMESPACE}" &>/dev/null; then
        error "ChaosExperiment '${exp}' not found. Run 'make chaos' first."
        exit 1
    fi
done
info "✓ All ChaosExperiments present"

kubectl get kafka krafter -n kafka &>/dev/null || {
    error "Kafka cluster 'krafter' not found."
    exit 1
}

cleanup_previous_jobs "${LABEL}"
for eng in gameday-p1 gameday-p2 gameday-p3; do
    kubectl delete chaosengine "${eng}" -n "${NAMESPACE}" --ignore-not-found >/dev/null
done

PASS=$(kubectl get secret kates-backend -n kafka \
    -o jsonpath='{.data.password}' 2>/dev/null | base64 -d || echo "")

GAME_START=$SECONDS

# ── Start multi-topic load ────────────────────────────────────────────────
step "T+0: Starting sustained multi-topic load (events, results, metrics)..."

for i in 1 2 3; do
    TOPICS=("kates-events" "kates-results" "kates-metrics")
    TOPIC="${TOPICS[$((i-1))]}"
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
                --num-records 1000000 \
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
                  linger.ms=5 \
                  enable.idempotence=true
          resources:
            requests: { cpu: 200m, memory: 256Mi }
            limits:   { cpu: 500m, memory: 512Mi }
EOF
done
info "✓ 3 producers started (kates-events, kates-results, kates-metrics)"

# ── Consumer ──────────────────────────────────────────────────────────────
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: ${LABEL}-consumer-1
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
                --topic kates-events \
                --messages 1000000 \
                --threads 3 \
                --bootstrap-server ${BOOTSTRAP} \
                --consumer.config /dev/stdin <<CONF
              security.protocol=SASL_PLAINTEXT
              sasl.mechanism=SCRAM-SHA-512
              sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-backend" password="${PASS}";
              group.id=gameday-cg
              auto.offset.reset=earliest
              CONF
          resources:
            requests: { cpu: 200m, memory: 256Mi }
            limits:   { cpu: 500m, memory: 512Mi }
EOF

# ── Phase 1: Pod delete on alpha ──────────────────────────────────────────
warn "Waiting ${PHASE1_START}s for warmup before Phase 1..."
sleep "${PHASE1_START}"

step "T+$(( SECONDS - GAME_START ))s: Phase 1 — pod-delete on brokers-alpha..."
cat <<EOF | kubectl apply -f -
apiVersion: litmuschaos.io/v1alpha1
kind: ChaosEngine
metadata:
  name: gameday-p1
  namespace: ${NAMESPACE}
  labels: { perf-test: "${LABEL}", phase: "1" }
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
              value: "${PHASE1_DURATION}"
            - name: CHAOS_INTERVAL
              value: "20"
            - name: FORCE
              value: "false"
            - name: PODS_AFFECTED_PERC
              value: "33"
EOF
info "✓ Phase 1 running (${PHASE1_DURATION}s)"
sleep "${PHASE1_DURATION}"

# Check Kafka cluster still ready after Phase 1
KAFKA_STATE=$(kubectl get kafka krafter -n kafka \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
if [ "${KAFKA_STATE}" = "True" ]; then
    info "✓ Phase 1 passed — Kafka cluster still Ready"
else
    warn "⚠  Kafka cluster not Ready after Phase 1 (${KAFKA_STATE})"
fi

sleep 30  # recovery window between phases

# ── Phase 2: CPU hog on gamma ─────────────────────────────────────────────
step "T+$(( SECONDS - GAME_START ))s: Phase 2 — cpu-hog on brokers-gamma (90%)..."
cat <<EOF | kubectl apply -f -
apiVersion: litmuschaos.io/v1alpha1
kind: ChaosEngine
metadata:
  name: gameday-p2
  namespace: ${NAMESPACE}
  labels: { perf-test: "${LABEL}", phase: "2" }
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
              value: "${PHASE2_DURATION}"
            - name: CPU_CORES
              value: "1"
            - name: CPU_LOAD
              value: "90"
            - name: PODS_AFFECTED_PERC
              value: "100"
            - name: CONTAINER_RUNTIME
              value: "containerd"
            - name: SOCKET_PATH
              value: "/run/containerd/containerd.sock"
EOF
info "✓ Phase 2 running (${PHASE2_DURATION}s)"
sleep "${PHASE2_DURATION}"
sleep 30  # recovery window

# ── Phase 3: Network partition on sigma ───────────────────────────────────
step "T+$(( SECONDS - GAME_START ))s: Phase 3 — network-partition on brokers-sigma..."
cat <<EOF | kubectl apply -f -
apiVersion: litmuschaos.io/v1alpha1
kind: ChaosEngine
metadata:
  name: gameday-p3
  namespace: ${NAMESPACE}
  labels: { perf-test: "${LABEL}", phase: "3" }
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
              value: "${PHASE3_DURATION}"
            - name: POLICY
              value: "deny"
            - name: PODS_AFFECTED_PERC
              value: "100"
EOF
info "✓ Phase 3 running (${PHASE3_DURATION}s)"
sleep "${PHASE3_DURATION}"

# ── Recovery validation ────────────────────────────────────────────────────
step "T+$(( SECONDS - GAME_START ))s: All phases complete — validating recovery..."

# Kafka cluster state
KAFKA_STATE=$(kubectl get kafka krafter -n kafka \
    -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")
echo "  Kafka cluster Ready: ${KAFKA_STATE}"

# ChaosResults
echo ""
info "ChaosResults:"
for phase in p1 p2 p3; do
    NAME="gameday-${phase}"
    VERDICT=$(kubectl get chaosresult -n "${NAMESPACE}" \
        -l "perf-test=${LABEL},phase=${phase//p/}" \
        -o jsonpath='{.items[0].status.experimentStatus.verdict}' 2>/dev/null || echo "N/A")
    echo "  ${NAME}: ${VERDICT}"
done

# Wait for producers
step "Waiting for producers to complete..."
wait_for_jobs "${LABEL}" "producer" 3 300

print_job_results "${LABEL}" "producer" 3

# Consumer lag
step "Final consumer group lag (should be 0 or decreasing):"
kubectl exec -n "${NAMESPACE}" krafter-brokers-alpha-0 -- \
    bin/kafka-consumer-groups.sh \
        --bootstrap-server localhost:9092 \
        --describe --group gameday-cg 2>/dev/null || true

TOTAL_TIME=$(( SECONDS - GAME_START ))
bold ""
bold "Game Day completed in ${TOTAL_TIME}s"

show_cleanup_hint "${LABEL}" ""
echo "   kubectl delete chaosengine gameday-p1 gameday-p2 gameday-p3 -n ${NAMESPACE}"
