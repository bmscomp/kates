#!/bin/bash
set -e

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KIND_CLUSTER_NAME="panda"
IMAGE_NAME="kates:latest"
NAMESPACE="kates"
BUILD_MODE="${1:-jvm}"

echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}Deploying Kates to Kind Cluster${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""

if ! kind get clusters | grep -q "^${KIND_CLUSTER_NAME}$"; then
    echo -e "${RED}Error: Kind cluster '${KIND_CLUSTER_NAME}' not found${NC}"
    echo "Please run 'make cluster' first"
    exit 1
fi

echo -e "${BLUE}[1/5] Building Kates JAR...${NC}"
cd "${SCRIPT_DIR}/kates"
if [ "$BUILD_MODE" = "native" ]; then
    echo -e "${YELLOW}  Building native image (this may take several minutes)...${NC}"
    ./mvnw package -Dnative -DskipTests -B
else
    ./mvnw package -DskipTests -B
fi
echo -e "${GREEN}  ✓ Build complete${NC}"

echo ""
echo -e "${BLUE}[2/5] Building Docker image...${NC}"
if [ "$BUILD_MODE" = "native" ]; then
    docker build -f Dockerfile.native -t "${IMAGE_NAME}" .
else
    docker build -t "${IMAGE_NAME}" .
fi
echo -e "${GREEN}  ✓ Image built: ${IMAGE_NAME}${NC}"

echo ""
echo -e "${BLUE}[3/5] Loading image into Kind...${NC}"
kind load docker-image "${IMAGE_NAME}" --name "${KIND_CLUSTER_NAME}"
echo -e "${GREEN}  ✓ Image loaded into Kind${NC}"

echo ""
echo -e "${BLUE}[4/5] Applying Kubernetes manifests...${NC}"
cd "${SCRIPT_DIR}"
kubectl apply -f kates/k8s/namespace.yaml
kubectl apply -f kates/k8s/deployment.yaml
kubectl apply -f kates/k8s/service.yaml
echo -e "${GREEN}  ✓ Manifests applied${NC}"

echo ""
echo -e "${BLUE}[5/5] Waiting for Kates to be ready...${NC}"
kubectl rollout status deployment/kates -n "${NAMESPACE}" --timeout=120s
echo -e "${GREEN}  ✓ Kates is ready${NC}"

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}✅ Kates Deployed Successfully!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "Namespace:  ${NAMESPACE}"
echo "Service:    kates.${NAMESPACE}.svc:8080"
echo ""
echo "Access locally:"
echo "  make ports          # Starts all port-forwards (includes Kates on :30083)"
echo "  make kates-logs     # Stream Kates logs"
echo ""
echo "Quick test:"
echo "  curl http://localhost:30083/api/health"
echo "  curl http://localhost:30083/api/tests/types"
