#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-panda}"

echo -e "${BLUE}=== Deploy from Kind Images (No Registry) ===${NC}"
echo ""

# Check if Kind cluster exists
if ! kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
    echo -e "${RED}Error: Kind cluster '${KIND_CLUSTER_NAME}' not found${NC}"
    echo "Please create the cluster first with: ./launch.sh"
    exit 1
fi

echo -e "${GREEN}✓ Found Kind cluster: ${KIND_CLUSTER_NAME}${NC}"
echo ""

# Get control plane node for image verification
CONTROL_PLANE_NODE=$(kind get nodes --name "${KIND_CLUSTER_NAME}" | grep control-plane | head -n 1)

# Function to check if image exists in Kind
check_image_in_kind() {
    local image=$1
    if docker exec "${CONTROL_PLANE_NODE}" crictl images | grep -q "$image"; then
        return 0
    else
        return 1
    fi
}

# List of required images for deployment
REQUIRED_IMAGES=(
    # Kafka & Strimzi
    "quay.io/strimzi/operator:0.49.0"
    "quay.io/strimzi/kafka:0.49.0-kafka-4.1.1"
    "provectuslabs/kafka-ui:latest"
    
    # Prometheus & Grafana
    "quay.io/prometheus/prometheus:v3.1.0"
    "quay.io/prometheus/alertmanager:v0.28.1"
    "quay.io/prometheus/node-exporter:v1.8.2"
    "quay.io/prometheus-operator/prometheus-operator:v0.79.2"
    "quay.io/prometheus-operator/prometheus-config-reloader:v0.79.2"
    "docker.io/grafana/grafana:11.4.0"
    "registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.14.0"
    "quay.io/prometheus-operator/admission-webhook:v0.79.2"
    
    # LitmusChaos
    "litmuschaos/chaos-operator:3.23.0"
    "litmuschaos/chaos-runner:3.23.0"
    "litmuschaos/chaos-exporter:3.23.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0"
    "docker.io/bitnami/mongodb:latest"
    
    # Argo Workflows
    "quay.io/argoproj/workflow-controller:v3.5.5"
    "quay.io/argoproj/argocli:v3.5.5"
    "quay.io/argoproj/argoexec:v3.5.5"
)

echo -e "${BLUE}Verifying required images in Kind...${NC}"
echo ""

MISSING_IMAGES=()
PRESENT_IMAGES=0

for image in "${REQUIRED_IMAGES[@]}"; do
    if check_image_in_kind "$image"; then
        echo -e "${GREEN}✓${NC} $image"
        ((PRESENT_IMAGES++))
    else
        echo -e "${RED}✗${NC} $image"
        MISSING_IMAGES+=("$image")
    fi
done

echo ""
echo -e "${GREEN}Present: ${PRESENT_IMAGES}/${#REQUIRED_IMAGES[@]}${NC}"

if [ ${#MISSING_IMAGES[@]} -gt 0 ]; then
    echo -e "${RED}Missing: ${#MISSING_IMAGES[@]} images${NC}"
    echo ""
    echo -e "${YELLOW}Missing images:${NC}"
    for img in "${MISSING_IMAGES[@]}"; do
        echo "  - $img"
    done
    echo ""
    echo -e "${YELLOW}To load missing images, run:${NC}"
    echo "  ./pull-images.sh"
    echo ""
    echo -e "${YELLOW}Or import from backup:${NC}"
    echo "  ./portability/import-kind-images.sh"
    echo ""
    exit 1
fi

echo ""
echo -e "${GREEN}✓ All required images are present in Kind${NC}"
echo ""

# Deploy Kafka
echo -e "${BLUE}Deploying Kafka...${NC}"
if ./deploy-kafka.sh; then
    echo -e "${GREEN}✓ Kafka deployed successfully${NC}"
else
    echo -e "${RED}✗ Kafka deployment failed${NC}"
    exit 1
fi

echo ""

# Deploy LitmusChaos
echo -e "${BLUE}Deploying LitmusChaos...${NC}"
if ./deploy-litmuschaos.sh; then
    echo -e "${GREEN}✓ LitmusChaos deployed successfully${NC}"
else
    echo -e "${RED}✗ LitmusChaos deployment failed${NC}"
    exit 1
fi

echo ""

# Deploy Argo Workflows
echo -e "${BLUE}Deploying Argo Workflows...${NC}"
if ./deploy-argo.sh; then
    echo -e "${GREEN}✓ Argo Workflows deployed successfully${NC}"
else
    echo -e "${RED}✗ Argo Workflows deployment failed${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}=== Deployment Complete ===${NC}"
echo ""
echo "All services deployed using images from Kind cluster (no registry pulls)"
echo ""
echo "Access your services:"
echo -e "  ${BLUE}Grafana:${NC}     http://localhost:30080 (admin/admin)"
echo -e "  ${BLUE}Kafka UI:${NC}    http://localhost:30081"
echo -e "  ${BLUE}LitmusChaos:${NC} http://localhost:9091 (admin/litmus)"
echo -e "  ${BLUE}Argo:${NC}        https://localhost:2746"
echo ""
echo "Start port forwarding with:"
echo -e "  ${GREEN}make ports${NC}"
echo ""
