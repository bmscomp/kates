#!/bin/bash
set -e

# Stress Testing — ramps throughput beyond normal limits to find the breaking point
# Runs the same workload at increasing throughput targets, then validates recovery
#
# Environment variables:
#   RECORDS_PER_STEP   Records per throughput step (default: 500000)
#   RECORD_SIZE        Record size in bytes (default: 1024)
#   THROUGHPUT_STEPS   Space-separated throughput targets (default: "10000 25000 50000 100000 -1")
#   COOLDOWN           Seconds between steps (default: 30)

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

RECORDS_PER_STEP="${RECORDS_PER_STEP:-500000}"
RECORD_SIZE="${RECORD_SIZE:-1024}"
THROUGHPUT_STEPS="${THROUGHPUT_STEPS:-10000 25000 50000 100000 -1}"
COOLDOWN="${COOLDOWN:-30}"
NAMESPACE="kafka"

echo -e "${GREEN}╔════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║        STRESS TESTING                  ║${NC}"
echo -e "${GREEN}╚════════════════════════════════════════╝${NC}"
echo ""
echo "Configuration:"
echo "  Records/step:   $RECORDS_PER_STEP"
echo "  Record size:    $RECORD_SIZE bytes"
echo "  Throughput steps: $THROUGHPUT_STEPS"
echo "  Cooldown:       ${COOLDOWN}s between steps"
echo ""

for THROUGHPUT in $THROUGHPUT_STEPS; do
  TOPIC="stress-$THROUGHPUT"

  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
    bin/kafka-topics.sh --create --if-not-exists \
      --bootstrap-server localhost:9092 \
      --topic "$TOPIC" --partitions 3 --replication-factor 3 2>/dev/null

  if [ "$THROUGHPUT" = "-1" ]; then
    echo -e "${RED}=== Throughput target: UNLIMITED (max) ===${NC}"
  else
    echo -e "${YELLOW}=== Throughput target: $THROUGHPUT msg/sec ===${NC}"
  fi

  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
    bin/kafka-producer-perf-test.sh \
      --topic "$TOPIC" \
      --num-records "$RECORDS_PER_STEP" \
      --record-size "$RECORD_SIZE" \
      --throughput "$THROUGHPUT" \
      --producer-props \
        bootstrap.servers=localhost:9092 \
        acks=all \
        batch.size=131072 \
        linger.ms=10

  echo ""
  echo -e "${YELLOW}--- Cooling down ${COOLDOWN}s ---${NC}"
  sleep "$COOLDOWN"
done

# Recovery validation
echo -e "${GREEN}=== Recovery Validation ===${NC}"
echo "Running 100K messages at 10,000 msg/sec to verify cluster baseline..."

kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create --if-not-exists \
    --bootstrap-server localhost:9092 \
    --topic stress-recovery --partitions 3 --replication-factor 3 2>/dev/null

kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-producer-perf-test.sh \
    --topic stress-recovery \
    --num-records 100000 \
    --record-size "$RECORD_SIZE" \
    --throughput 10000 \
    --producer-props \
      bootstrap.servers=localhost:9092 \
      acks=all

echo ""
echo -e "${GREEN}Stress test completed!${NC}"
echo ""
echo "Cleanup:"
echo "  for t in $THROUGHPUT_STEPS recovery; do kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- bin/kafka-topics.sh --delete --bootstrap-server localhost:9092 --topic stress-\$t 2>/dev/null; done"
