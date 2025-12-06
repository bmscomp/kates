#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# Parse command line arguments
VERBOSE=false
DEBUG=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -d|--debug)
            DEBUG=true
            VERBOSE=true
            set -x
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  -v, --verbose    Enable verbose output"
            echo "  -d, --debug      Enable debug mode (includes verbose)"
            echo "  -h, --help       Show this help message"
            echo ""
            echo "Environment Variables:"
            echo "  KIND_CLUSTER_NAME    Name of Kind cluster (default: panda)"
            echo "  REGISTRY             Docker registry to use (default: docker.io)"
            echo ""
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use -h or --help for usage information"
            exit 1
            ;;
    esac
done

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-panda}"
REGISTRY="${REGISTRY:-docker.io}"

echo -e "${BLUE}=== Load Images from Registry to Kind ===${NC}"
echo ""

if [ "$VERBOSE" = true ]; then
    echo -e "${CYAN}[VERBOSE] Verbose mode enabled${NC}"
fi
if [ "$DEBUG" = true ]; then
    echo -e "${CYAN}[DEBUG] Debug mode enabled${NC}"
fi
echo ""

# Check if Kind cluster exists
if ! kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
    echo -e "${RED}Error: Kind cluster '${KIND_CLUSTER_NAME}' not found${NC}"
    echo "Please create the cluster first with: ./launch.sh"
    exit 1
fi

echo -e "${GREEN}✓ Found Kind cluster: ${KIND_CLUSTER_NAME}${NC}"
echo -e "${GREEN}Registry: ${REGISTRY}${NC}"
echo ""

# Temporarily unset proxy for Docker operations
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy

# Function to pull and load image
pull_and_load() {
    local image=$1
    local full_image="${image}"
    
    # Add registry prefix if not already present
    if [[ ! "$image" =~ ^[a-z0-9.-]+\.[a-z]{2,}/ ]] && [[ ! "$image" =~ ^localhost: ]]; then
        if [ "$REGISTRY" != "docker.io" ]; then
            full_image="${REGISTRY}/${image}"
        fi
    fi
    
    echo -e "${BLUE}Processing: ${full_image}${NC}"
    
    if [ "$VERBOSE" = true ]; then
        echo -e "${CYAN}[VERBOSE] Original image: ${image}${NC}"
        echo -e "${CYAN}[VERBOSE] Full image path: ${full_image}${NC}"
        echo -e "${CYAN}[VERBOSE] Kind cluster: ${KIND_CLUSTER_NAME}${NC}"
    fi
    
    # Check if image already exists in Kind
    if [ "$VERBOSE" = true ]; then
        echo -e "${CYAN}[VERBOSE] Checking if image exists in Kind...${NC}"
    fi
    
    local check_result
    check_result=$(docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images 2>&1)
    local check_exit=$?
    
    if [ "$DEBUG" = true ]; then
        echo -e "${CYAN}[DEBUG] crictl images exit code: ${check_exit}${NC}"
        echo -e "${CYAN}[DEBUG] crictl output:${NC}"
        echo "$check_result" | head -5
    fi
    
    if echo "$check_result" | grep -q "${image}"; then
        echo -e "${YELLOW}  ⊘ Already in Kind, skipping${NC}"
        if [ "$VERBOSE" = true ]; then
            echo -e "${CYAN}[VERBOSE] Image found in Kind cluster${NC}"
        fi
        return 0
    fi
    
    # Pull image from registry
    echo "  Pulling from registry..."
    if [ "$VERBOSE" = true ]; then
        echo -e "${CYAN}[VERBOSE] Running: docker pull ${full_image}${NC}"
    fi
    
    local pull_output
    local pull_exit
    if [ "$DEBUG" = true ]; then
        pull_output=$(docker pull "${full_image}" 2>&1)
        pull_exit=$?
        echo -e "${CYAN}[DEBUG] Pull exit code: ${pull_exit}${NC}"
        echo -e "${CYAN}[DEBUG] Pull output:${NC}"
        echo "$pull_output"
    else
        if [ "$VERBOSE" = true ]; then
            docker pull "${full_image}"
            pull_exit=$?
        else
            docker pull "${full_image}" >/dev/null 2>&1
            pull_exit=$?
        fi
    fi
    
    if [ $pull_exit -eq 0 ]; then
        echo -e "${GREEN}  ✓ Pulled successfully${NC}"
    else
        echo -e "${RED}  ✗ Failed to pull${NC}"
        if [ "$DEBUG" = true ]; then
            echo -e "${CYAN}[DEBUG] Pull failed with exit code: ${pull_exit}${NC}"
        fi
        return 1
    fi
    
    # Verify image in local Docker
    if [ "$VERBOSE" = true ]; then
        echo -e "${CYAN}[VERBOSE] Verifying image in local Docker...${NC}"
        docker image inspect "${full_image}" --format '{{.RepoTags}} {{.Size}}' 2>/dev/null || echo "  Image not found in local Docker"
    fi
    
    # Load into Kind
    echo "  Loading into Kind..."
    if [ "$VERBOSE" = true ]; then
        echo -e "${CYAN}[VERBOSE] Running: kind load docker-image ${full_image} --name ${KIND_CLUSTER_NAME}${NC}"
    fi
    
    local load_output
    local load_exit
    if [ "$DEBUG" = true ]; then
        load_output=$(kind load docker-image "${full_image}" --name "${KIND_CLUSTER_NAME}" 2>&1)
        load_exit=$?
        echo -e "${CYAN}[DEBUG] Load exit code: ${load_exit}${NC}"
        echo -e "${CYAN}[DEBUG] Load output:${NC}"
        echo "$load_output"
    else
        if [ "$VERBOSE" = true ]; then
            kind load docker-image "${full_image}" --name "${KIND_CLUSTER_NAME}"
            load_exit=$?
        else
            kind load docker-image "${full_image}" --name "${KIND_CLUSTER_NAME}" >/dev/null 2>&1
            load_exit=$?
        fi
    fi
    
    if [ $load_exit -eq 0 ]; then
        echo -e "${GREEN}  ✓ Loaded into Kind${NC}"
        
        # Verify image was loaded
        if [ "$VERBOSE" = true ]; then
            echo -e "${CYAN}[VERBOSE] Verifying image in Kind...${NC}"
            docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images | grep "${image}" || echo "  Warning: Image not found in Kind after loading"
        fi
    else
        echo -e "${RED}  ✗ Failed to load into Kind${NC}"
        if [ "$DEBUG" = true ]; then
            echo -e "${CYAN}[DEBUG] Load failed with exit code: ${load_exit}${NC}"
            echo -e "${CYAN}[DEBUG] Checking Kind nodes:${NC}"
            kind get nodes --name "${KIND_CLUSTER_NAME}"
        fi
        return 1
    fi
    
    echo ""
    return 0
}

# List of images to load
IMAGES=(
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

# Allow custom image list via file
if [ -f "images.txt" ]; then
    echo -e "${GREEN}Found images.txt, loading custom image list...${NC}"
    echo ""
    CUSTOM_IMAGES=()
    while IFS= read -r line; do
        CUSTOM_IMAGES+=("$line")
    done < images.txt
    IMAGES=("${CUSTOM_IMAGES[@]}")
fi

# Process all images
TOTAL=${#IMAGES[@]}
SUCCESS=0
FAILED=0
SKIPPED=0

echo -e "${BLUE}Loading ${TOTAL} images from registry to Kind...${NC}"
echo ""

for image in "${IMAGES[@]}"; do
    # Skip empty lines and comments
    [[ -z "$image" ]] && continue
    [[ "$image" =~ ^# ]] && continue
    
    if pull_and_load "$image"; then
        ((SUCCESS++))
    else
        ((FAILED++))
    fi
done

# Summary
echo ""
echo -e "${BLUE}=== Load Summary ===${NC}"
echo -e "${GREEN}✓ Successfully loaded: ${SUCCESS} images${NC}"
if [ $FAILED -gt 0 ]; then
    echo -e "${RED}✗ Failed to load: ${FAILED} images${NC}"
fi

if [ "$VERBOSE" = true ]; then
    echo ""
    echo -e "${CYAN}[VERBOSE] Total images processed: ${TOTAL}${NC}"
    echo -e "${CYAN}[VERBOSE] Success: ${SUCCESS}, Failed: ${FAILED}${NC}"
    echo -e "${CYAN}[VERBOSE] Kind cluster: ${KIND_CLUSTER_NAME}${NC}"
fi

echo ""
echo -e "${GREEN}All images have been loaded into Kind cluster '${KIND_CLUSTER_NAME}'${NC}"
echo ""
echo "Verify images in Kind:"
echo -e "  ${BLUE}docker exec ${KIND_CLUSTER_NAME}-control-plane crictl images${NC}"

if [ "$VERBOSE" = true ]; then
    echo ""
    echo -e "${CYAN}[VERBOSE] Listing all images in Kind cluster:${NC}"
    docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images | head -20
fi

echo ""
