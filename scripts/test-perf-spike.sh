#!/bin/bash
set -e

# Spike Testing — simulates sudden large increases and decreases in load
#
# Environment variables:
#   BASELINE_RATE      Baseline msg/sec (default: 1000)
#   BASELINE_DURATION  Baseline records = rate × seconds (default: 60000 = 60s)
#   BURST_PRODUCERS    Concurrent producers during spike (default: 3)
#   BURST_RECORDS      Records per burst producer (default: 500000)
#   TOPIC              Topic name (default: spike-test)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

BASELINE_RATE="${BASELINE_RATE:-1000}"
BASELINE_DURATION="${BASELINE_DURATION:-60000}"
BURST_PRODUCERS="${BURST_PRODUCERS:-3}"
BURST_RECORDS="${BURST_RECORDS:-500000}"
TOPIC="${TOPIC:-spike-test}"

bold "SPIKE TESTING"
echo ""
echo "Configuration:"
echo "  Baseline rate:    $BASELINE_RATE msg/sec"
echo "  Baseline records: $BASELINE_DURATION"
echo "  Burst producers:  $BURST_PRODUCERS (unlimited throughput)"
echo "  Burst records:    $BURST_RECORDS per producer"
echo "  Topic:            $TOPIC"
echo ""

cleanup_previous_jobs "spike"

kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create --if-not-exists \
    --bootstrap-server localhost:9092 \
    --topic $TOPIC --partitions 3 --replication-factor 3 \
    --config min.insync.replicas=2 2>/dev/null

# Phase 1: Baseline
info "Phase 1: Baseline ($BASELINE_RATE msg/sec)"
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic $TOPIC \
    --num-records "$BASELINE_DURATION" \
    --record-size 1024 \
    --throughput "$BASELINE_RATE" \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      batch.size=65536 \
      linger.ms=5

echo ""

# Phase 2: Spike
error "Phase 2: SPIKE ($BURST_PRODUCERS concurrent producers, unlimited)"
for i in $(seq 1 "$BURST_PRODUCERS"); do
cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: spike-burst-$i
  namespace: $NAMESPACE
  labels:
    perf-test: spike
    perf-role: burst
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
      - name: producer
        image: $IMAGE
        imagePullPolicy: Never
        command: ["/bin/bash", "-c"]
        args:
        - |
          bin/kafka-producer-perf-test.sh \
            --topic $TOPIC \
            --num-records $BURST_RECORDS \
            --record-size 1024 \
            --throughput -1 \
            --producer-props \
              bootstrap.servers=$BOOTSTRAP \
              acks=all \
              batch.size=131072 \
              linger.ms=10
EOF
done

wait_for_jobs "spike" "burst" "$BURST_PRODUCERS" 300
print_job_results "spike" "burst" "$BURST_PRODUCERS"

echo ""

# Phase 3: Recovery baseline
info "Phase 3: Recovery baseline ($BASELINE_RATE msg/sec)"
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic $TOPIC \
    --num-records "$BASELINE_DURATION" \
    --record-size 1024 \
    --throughput "$BASELINE_RATE" \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all

echo ""
info "✅ Spike test completed!"
echo "Compare Phase 1 and Phase 3 latency to measure recovery."

show_cleanup_hint "spike" "$TOPIC"
