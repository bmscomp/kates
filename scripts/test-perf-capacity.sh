#!/bin/bash
set -e

# Capacity Testing — finds the maximum sustained throughput within SLA
#
# Environment variables:
#   RECORDS_PER_PROBE  Records per throughput probe (default: 200000)
#   RECORD_SIZE        Record size in bytes (default: 1024)
#   THROUGHPUT_PROBES  Space-separated throughput targets (default: "5000 10000 20000 40000 80000 -1")
#   COOLDOWN           Seconds between probes (default: 10)
#   TOPIC              Topic name (default: capacity-test)

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"
source "${SCRIPT_DIR}/test-common.sh"

RECORDS_PER_PROBE="${RECORDS_PER_PROBE:-200000}"
RECORD_SIZE="${RECORD_SIZE:-1024}"
THROUGHPUT_PROBES="${THROUGHPUT_PROBES:-5000 10000 20000 40000 80000 -1}"
COOLDOWN="${COOLDOWN:-10}"
TOPIC="${TOPIC:-capacity-test}"

bold "CAPACITY TESTING"
echo ""
echo "Configuration:"
echo "  Records/probe:  $RECORDS_PER_PROBE"
echo "  Record size:    $RECORD_SIZE bytes"
echo "  Probes:         $THROUGHPUT_PROBES"
echo "  Cooldown:       ${COOLDOWN}s between probes"
echo ""
echo "Results table (fill P99 from output):"
echo "┌──────────────────┬──────────┬──────────┬──────────┐"
echo "│ Target msg/sec   │ Actual   │ Avg (ms) │ P99 (ms) │"
echo "├──────────────────┼──────────┼──────────┼──────────┤"

kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create --if-not-exists \
    --bootstrap-server localhost:9092 \
    --topic $TOPIC --partitions 3 --replication-factor 3 \
    --config min.insync.replicas=2 2>/dev/null

for THROUGHPUT in $THROUGHPUT_PROBES; do
  if [ "$THROUGHPUT" = "-1" ]; then
    LABEL="unlimited"
  else
    LABEL="$THROUGHPUT"
  fi

  warn "=== Capacity probe: $LABEL msg/sec ==="

  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
    bin/kafka-producer-perf-test.sh \
      --topic $TOPIC \
      --num-records "$RECORDS_PER_PROBE" \
      --record-size "$RECORD_SIZE" \
      --throughput "$THROUGHPUT" \
      --producer-props \
        bootstrap.servers=localhost:9092 \
        acks=all \
        batch.size=65536 \
        linger.ms=5

  echo ""
  sleep "$COOLDOWN"
done

echo "└──────────────────┴──────────┴──────────┴──────────┘"
echo ""
info "✅ Capacity test completed!"
echo ""
echo "The first throughput where P99 exceeds your SLA threshold is the capacity ceiling."
echo "The level just below it is your maximum sustainable throughput."

show_cleanup_hint "" "$TOPIC"
