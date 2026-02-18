#!/bin/bash
set -e

# Volume Testing — tests performance with large amounts of data
#
# Environment variables:
#   LARGE_MSG_RECORDS  Number of large-message records (default: 50000)
#   LARGE_MSG_SIZE     Large message size in bytes (default: 102400)
#   HIGH_COUNT_RECORDS Number of small records (default: 5000000)
#   SMALL_MSG_SIZE     Small message size in bytes (default: 1024)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

LARGE_MSG_RECORDS="${LARGE_MSG_RECORDS:-50000}"
LARGE_MSG_SIZE="${LARGE_MSG_SIZE:-102400}"
HIGH_COUNT_RECORDS="${HIGH_COUNT_RECORDS:-5000000}"
SMALL_MSG_SIZE="${SMALL_MSG_SIZE:-1024}"

bold "VOLUME TESTING"
echo ""
echo "Sub-test 1: $LARGE_MSG_RECORDS × ${LARGE_MSG_SIZE}B = ~$((LARGE_MSG_RECORDS * LARGE_MSG_SIZE / 1024 / 1024)) MB"
echo "Sub-test 2: $HIGH_COUNT_RECORDS × ${SMALL_MSG_SIZE}B = ~$((HIGH_COUNT_RECORDS * SMALL_MSG_SIZE / 1024 / 1024)) MB"
echo ""

for TOPIC in volume-test volume-test-count; do
  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
    bin/kafka-topics.sh --create --if-not-exists \
      --bootstrap-server localhost:9092 \
      --topic $TOPIC --partitions 3 --replication-factor 3 \
      --config min.insync.replicas=2 \
      --config retention.ms=1800000 \
      --config max.message.bytes=1048576 2>/dev/null
done

info "=== Sub-test 1: Large Messages ($LARGE_MSG_RECORDS × ${LARGE_MSG_SIZE}B) ==="
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic volume-test \
    --num-records "$LARGE_MSG_RECORDS" \
    --record-size "$LARGE_MSG_SIZE" \
    --throughput -1 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      max.request.size=1048576 \
      batch.size=131072

echo ""

info "=== Sub-test 2: High Count ($HIGH_COUNT_RECORDS × ${SMALL_MSG_SIZE}B) ==="
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic volume-test-count \
    --num-records "$HIGH_COUNT_RECORDS" \
    --record-size "$SMALL_MSG_SIZE" \
    --throughput -1 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all \
      batch.size=131072 \
      linger.ms=10 \
      compression.type=lz4

echo ""

warn "Broker disk usage after volume test:"
for BROKER in krafter-pool-alpha-0 krafter-pool-sigma-0 krafter-pool-gamma-0; do
  SIZE=$(kubectl exec -n $NAMESPACE "$BROKER" -- du -sh /var/lib/kafka/data 2>/dev/null | awk '{print $1}') || SIZE="unknown"
  echo "  $BROKER: $SIZE"
done

echo ""
info "✅ Volume test completed!"
echo ""
echo "Cleanup:"
echo "  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- bin/kafka-topics.sh --delete --bootstrap-server localhost:9092 --topic volume-test"
echo "  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- bin/kafka-topics.sh --delete --bootstrap-server localhost:9092 --topic volume-test-count"
