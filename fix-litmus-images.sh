#!/bin/bash
# Fix LitmusChaos images - re-pull with correct arm64 architecture
# Unified image versions matching chaos-litmus-chaos-enable.yml manifest

set -e

PLATFORM="--platform linux/arm64"
REGISTRY="localhost:5001"

echo "Cleaning and re-pulling LitmusChaos images with arm64 architecture..."
echo ""

# Unified LitmusChaos images - versions match the manifest
IMAGES=(
    # Core LitmusChaos components (3.23.0 - matches manifest)
    "litmuschaos/chaos-operator:3.23.0"
    "litmuschaos/chaos-runner:3.23.0"
    "litmuschaos/chaos-exporter:3.23.0"
    "litmuschaos/go-runner:3.23.0"
    
    # Portal components (3.23.0 - matches manifest)
    "litmuschaos/litmusportal-subscriber:3.23.0"
    "litmuschaos/litmusportal-event-tracker:3.23.0"
    
    # Portal Images from scarf.sh (3.23.0 - matches manifest)
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0"
    
    # Workflow controller
    "litmuschaos.docker.scarf.sh/litmuschaos/workflow-controller:v3.3.1"
    
    # MongoDB
    "litmuschaos.docker.scarf.sh/litmuschaos/mongo:6"
)

for image in "${IMAGES[@]}"; do
    echo "Processing: $image"
    
    # Remove old images
    docker image rm "$image" "${REGISTRY}/$image" 2>/dev/null || true
    
    # Pull with correct platform
    echo "  Pulling linux/arm64..."
    docker pull $PLATFORM "$image"
    
    # Tag for local registry
    docker tag "$image" "${REGISTRY}/$image"
    
    # Push to registry
    echo "  Pushing to local registry..."
    docker push "${REGISTRY}/$image"
    
    echo "  ✓ Done"
    echo ""
done

echo "All LitmusChaos images fixed!"
echo "Now you can run ./load-images-to-kind.sh"
