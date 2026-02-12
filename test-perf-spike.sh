#!/bin/bash
set -e

# Spike Testing — simulates sudden large increases and decreases in load
# Runs baseline → burst (concurrent Jobs) → baseline to measure stability
#
# Environment variables:
#   BASELINE_RATE      Baseline msg/sec (default: 1000)
#   BASELINE_DURATION  Baseline records = rate × seconds (default: 60000 = 60s)
#   BURST_PRODUCERS    Concurrent producers during spike (default: 3)
#   BURST_RECORDS      Records per burst producer (default: 500000)
#   TOPIC              Topic name (default: spike-test)

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

BASELINE_RATE="${BASELINE_RATE:-1000}"
BASELINE_DURATION="${BASELINE_DURATION:-60000}"
BURST_PRODUCERS="${BURST_PRODUCERS:-3}"
BURST_RECORDS="${BURST_RECORDS:-500000}"
TOPIC="${TOPIC:-spike-test}"
NAMESPACE="kafka"
IMAGE="quay.io/strimzi/kafka:0.49.0-kafka-4.1.1"
BOOTSTRAP="krafter-kafka-bootstrap.kafka.svc:9092"

echo -e "${GREEN}╔════════════════════════════════════════╗${NC}"
echo -e "${GREEN}║        SPIKE TESTING                   ║${NC}"
echo -e "${GREEN}╚════════════════════════════════════════╝${NC}"
echo ""
echo "Configuration:"
echo "  Baseline rate:    $BASELINE_RATE msg/sec"
echo "  Baseline records: $BASELINE_DURATION"
echo "  Burst producers:  $BURST_PRODUCERS (unlimited throughput)"
echo "  Burst records:    $BURST_RECORDS per producer"
echo "  Topic:            $TOPIC"
echo ""

# Create topic
kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- \
  bin/kafka-topics.sh --create --if-not-exists \
    --bootstrap-server localhost:9092 \
    --topic $TOPIC --partitions 3 --replication-factor 3 \
    --config min.insync.replicas=2 2>/dev/null

# Phase 1: Baseline
echo -e "${GREEN}Phase 1: Baseline ($BASELINE_RATE msg/sec)${NC}"
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
echo -e "${RED}Phase 2: SPIKE ($BURST_PRODUCERS concurrent producers, unlimited)${NC}"
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

echo -e "${YELLOW}Waiting for burst producers to complete...${NC}"
for i in $(seq 1 "$BURST_PRODUCERS"); do
  kubectl wait --for=condition=complete --timeout=300s "job/spike-burst-$i" -n $NAMESPACE 2>/dev/null || true
done

echo -e "${GREEN}Burst Results:${NC}"
for i in $(seq 1 "$BURST_PRODUCERS"); do
  echo -e "${YELLOW}--- Burst Producer $i ---${NC}"
  kubectl logs -n $NAMESPACE "job/spike-burst-$i" | tail -3
done

echo ""

# Phase 3: Recovery baseline
echo -e "${GREEN}Phase 3: Recovery baseline ($BASELINE_RATE msg/sec)${NC}"
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
echo -e "${GREEN}Spike test completed!${NC}"
echo "Compare Phase 1 and Phase 3 latency to measure recovery."
echo ""
echo "Cleanup:"
echo "  kubectl delete jobs -n $NAMESPACE -l perf-test=spike"
echo "  kubectl exec -n $NAMESPACE krafter-pool-alpha-0 -- bin/kafka-topics.sh --delete --bootstrap-server localhost:9092 --topic $TOPIC"
