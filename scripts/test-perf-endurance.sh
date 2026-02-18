#!/bin/bash
set -e

# Endurance (Soak) Testing — sustained load over extended period
# Detects memory leaks, GC degradation, and performance drift
#
# Environment variables:
#   DURATION_MINUTES   Test duration in minutes (default: 60)
#   THROUGHPUT         Messages per second (default: 5000)
#   RECORD_SIZE        Record size in bytes (default: 1024)
#   TOPIC              Topic name (default: endurance-test)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

DURATION_MINUTES="${DURATION_MINUTES:-60}"
THROUGHPUT="${THROUGHPUT:-5000}"
RECORD_SIZE="${RECORD_SIZE:-1024}"
TOPIC="${TOPIC:-endurance-test}"
NUM_RECORDS=$((THROUGHPUT * DURATION_MINUTES * 60))

bold "ENDURANCE (SOAK) TESTING"
echo ""
echo "Configuration:"
echo "  Duration:       $DURATION_MINUTES minutes"
echo "  Throughput:     $THROUGHPUT msg/sec"
echo "  Record size:    $RECORD_SIZE bytes"
echo "  Total records:  $NUM_RECORDS"
echo "  Data volume:    ~$((NUM_RECORDS * RECORD_SIZE / 1024 / 1024)) MB"
echo "  Topic:          $TOPIC"
echo ""

RETENTION_MS=$(((DURATION_MINUTES + 30) * 60 * 1000))
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create --if-not-exists \
    --bootstrap-server localhost:9092 \
    --topic $TOPIC --partitions 3 --replication-factor 3 \
    --config min.insync.replicas=2 \
    --config retention.ms=$RETENTION_MS 2>/dev/null

warn "Pre-test JVM snapshot:"
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bash -c 'echo "Heap:"; cat /proc/$(pgrep -f kafka.Kafka)/status 2>/dev/null | grep -E "VmRSS|VmSize" || echo "  (unable to read)"; echo "Threads: $(ls /proc/$(pgrep -f kafka.Kafka)/task 2>/dev/null | wc -l || echo unknown)"' 2>/dev/null || true

echo ""
info "Starting ${DURATION_MINUTES}-minute sustained load at $THROUGHPUT msg/sec..."
warn "Monitor Grafana JVM dashboard at http://localhost:30080 for degradation trends."
echo ""

kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic $TOPIC \
    --num-records "$NUM_RECORDS" \
    --record-size "$RECORD_SIZE" \
    --throughput "$THROUGHPUT" \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      batch.size=65536 \
      linger.ms=5 \
      compression.type=lz4

echo ""

warn "Post-test JVM snapshot:"
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bash -c 'echo "Heap:"; cat /proc/$(pgrep -f kafka.Kafka)/status 2>/dev/null | grep -E "VmRSS|VmSize" || echo "  (unable to read)"; echo "Threads: $(ls /proc/$(pgrep -f kafka.Kafka)/task 2>/dev/null | wc -l || echo unknown)"' 2>/dev/null || true

echo ""
info "✅ Endurance test completed!"
echo "Compare pre-test and post-test JVM snapshots for drift."
echo ""
echo "Cleanup:"
echo "  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- bin/kafka-topics.sh --delete --bootstrap-server localhost:9092 --topic $TOPIC"
