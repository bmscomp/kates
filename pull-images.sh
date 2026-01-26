#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

REGISTRY="localhost:5001"

# Detect platform and set explicit platform for Docker pulls
# This ensures we get the correct architecture for kind nodes
ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ] || [ "$ARCH" = "aarch64" ]; then
    echo "Apple Silicon detected - using linux/arm64 platform"
    PLATFORM="--platform linux/arm64"
else
    PLATFORM="--platform linux/amd64"
fi

# Temporarily unset proxy for Docker operations to avoid timeout issues
# This ensures direct connection to Docker registries
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy

echo -e "${GREEN}Pulling and pushing images to local registry...${NC}"

# Check if registry is running
if ! curl -s http://${REGISTRY}/v2/_catalog > /dev/null 2>&1; then
    echo -e "${YELLOW}Local registry is not running. Please run ./setup-registry.sh first${NC}"
    exit 1
fi

# Function to pull, tag, and push an image
push_to_local_registry() {
    local image=$1
    local local_image="${REGISTRY}/${image}"
    
    echo -e "${BLUE}Processing: ${image}${NC}"
    
    # Check if image already exists in local registry
    if curl -s "http://${REGISTRY}/v2/${image%:*}/tags/list" | grep -q "\"${image##*:}\""; then
        echo -e "${YELLOW}  Image already in registry, skipping pull${NC}"
        return 0
    fi
    
    # Pull from public registry with platform specification for Kind compatibility
    docker pull ${PLATFORM} ${image}
    
    # Tag for local registry
    docker tag ${image} ${local_image}
    
    # Push to local registry
    docker push ${local_image}
    
    echo -e "${GREEN}✓ Pushed: ${local_image}${NC}"
}

echo ""
echo "=== Kafka UI Images ==="
push_to_local_registry "provectuslabs/kafka-ui:v0.7.2"

echo ""
echo "=== Strimzi Kafka Images ==="
# Strimzi operator and Kafka images (version 0.49.0 as seen in deployment)
push_to_local_registry "quay.io/strimzi/operator:0.49.1"
push_to_local_registry "quay.io/strimzi/kafka:0.49.1-kafka-4.1.1"

echo ""
echo "=== Prometheus Stack Images ==="
# Core monitoring components - updated to latest stable versions
push_to_local_registry "quay.io/prometheus/prometheus:v3.9.1"
push_to_local_registry "quay.io/prometheus/alertmanager:v0.28.1"
push_to_local_registry "quay.io/prometheus-operator/prometheus-operator:v0.79.2"
push_to_local_registry "quay.io/prometheus-operator/prometheus-config-reloader:v0.79.2"

# Grafana
push_to_local_registry "docker.io/grafana/grafana:12.3.1"

# Grafana sidecar (used for dashboards/datasources)
# Updated to v2.2.3 - newer version works without manifest corruption
push_to_local_registry "quay.io/kiwigrid/k8s-sidecar:2.2.3"

# Node exporter
push_to_local_registry "quay.io/prometheus/node-exporter:v1.8.2"

# Kube-state-metrics
push_to_local_registry "registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.14.0"

# Admission webhook
# Admission webhook
push_to_local_registry "quay.io/prometheus-operator/admission-webhook:v0.79.2"
push_to_local_registry "registry.k8s.io/ingress-nginx/kube-webhook-certgen:v1.6.5"

echo ""
echo "=== LitmusChaos Images ==="
# LitmusChaos chaos engineering images - updated to latest version
push_to_local_registry "litmuschaos/chaos-operator:3.25.0"
push_to_local_registry "litmuschaos/chaos-runner:3.25.0"
push_to_local_registry "litmuschaos/chaos-exporter:3.25.0"

# Additional LitmusChaos components
push_to_local_registry "litmuschaos/litmusportal-subscriber:3.24.0"
push_to_local_registry "litmuschaos/litmusportal-event-tracker:3.24.0"

# Portal Images (from scarf.sh)
# These need special handling because the source is different from standard docker hub
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0 ${REGISTRY}/litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0
docker push ${REGISTRY}/litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.24.0

docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0 ${REGISTRY}/litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0
docker push ${REGISTRY}/litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.24.0

docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0
docker tag litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0 ${REGISTRY}/litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0
docker push ${REGISTRY}/litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.24.0

# MongoDB images from scarf.sh
docker pull ${PLATFORM} litmuschaos.docker.scarf.sh/litmuschaos/mongo:6
docker tag litmuschaos.docker.scarf.sh/litmuschaos/mongo:6 ${REGISTRY}/litmuschaos.docker.scarf.sh/litmuschaos/mongo:6
docker push ${REGISTRY}/litmuschaos.docker.scarf.sh/litmuschaos/mongo:6

# Alternative MongoDB image from docker.io
push_to_local_registry "docker.io/litmuschaos/mongo:6"

# Dependencies
push_to_local_registry "docker.io/bitnami/mongodb:latest"
push_to_local_registry "docker.io/bitnamilegacy/os-shell:12-debian-12-r51"

echo ""
# Apicurio Registry
push_to_local_registry "apicurio/apicurio-registry-kafkasql:2.2.5.Final"

echo ""
echo "=== Velero Backup Images ==="
push_to_local_registry "velero/velero:v1.17.1"
push_to_local_registry "velero/velero-plugin-for-aws:v1.10.0"
push_to_local_registry "docker.io/minio/minio:RELEASE.2024-09-22T00-33-43Z"
push_to_local_registry "docker.io/minio/mc:RELEASE.2024-09-16T17-43-14Z"
push_to_local_registry "docker.io/bitnamilegacy/kubectl:1.17.1"

echo ""
echo -e "${GREEN}All images processed!${NC}"
echo ""
echo "To view all images in the registry:"
echo "  curl http://${REGISTRY}/v2/_catalog | jq"
echo ""
echo "Total images in registry:"
curl -s http://${REGISTRY}/v2/_catalog | jq '.repositories | length'
