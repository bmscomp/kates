#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

CHARTS_DIR="./charts"
STRIMZI_CHART_DIR="${CHARTS_DIR}/strimzi-kafka-operator"

echo -e "${GREEN}Deploying Kafka Strimzi Cluster from local chart...${NC}"

# Check if local chart exists
if [ ! -d "${STRIMZI_CHART_DIR}" ]; then
    echo -e "${YELLOW}Error: Strimzi chart not found at ${STRIMZI_CHART_DIR}${NC}"
    echo "Please run ./download-charts.sh first to download the charts"
    exit 1
fi

# Create namespace
kubectl create namespace kafka --dry-run=client -o yaml | kubectl apply -f -

# Install Strimzi Operator from local chart
echo -e "${GREEN}Installing Strimzi Operator from local chart...${NC}"
helm upgrade --install strimzi-kafka-operator "${STRIMZI_CHART_DIR}" \
  --namespace kafka \
  --set watchAnyNamespace=true \
  --set image.imagePullPolicy=Never \
  --timeout 10m \
  --wait

# Apply Metrics Config
echo -e "${GREEN}Applying Metrics Configuration...${NC}"
kubectl apply -f config/kafka-metrics.yaml

# Apply Zone-Specific Storage Classes
echo -e "${GREEN}Creating Zone-Specific Storage Classes...${NC}"
kubectl apply -f config/storage-classes.yaml

# Apply Kafka Cluster
echo -e "${GREEN}Deploying Kafka Cluster (KRaft)...${NC}"
kubectl apply -f config/kafka.yaml

# Apply Dashboard
echo -e "${GREEN}Applying Kafka Dashboards...${NC}"
kubectl apply -f config/kafka-dashboard.yaml
kubectl apply -f config/kafka-performance-dashboard.yaml
kubectl apply -f config/kafka-jvm-dashboard.yaml
kubectl apply -f config/kafka-perf-test-dashboard.yaml
kubectl apply -f config/kafka-working-dashboard.yaml
kubectl apply -f config/kafka-comprehensive-dashboard.yaml
kubectl apply -f config/kafka-all-metrics-dashboard.yaml

# Cleanup old cluster if exists
kubectl delete kafka my-cluster -n kafka --ignore-not-found
# Cleanup old NodePool
kubectl delete kafkanodepool dual-role -n kafka --ignore-not-found
# Cleanup PVCs for fresh start (since we changed topology)
kubectl delete pvc -l strimzi.io/cluster=krafter -n kafka --ignore-not-found
kubectl delete pvc -l strimzi.io/cluster=my-cluster -n kafka --ignore-not-found

echo -e "${GREEN}Waiting for Kafka cluster to be ready (this may take a few minutes)...${NC}"
kubectl wait kafka/krafter --for=condition=Ready --timeout=300s -n kafka 

echo -e "${GREEN}Kafka deployment complete!${NC}"
echo "Check the 'Kafka Cluster Health' dashboard in Grafana."
