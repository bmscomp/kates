#!/bin/bash
# Fix LitmusChaos images - re-pull with correct arm64 architecture

set -e

PLATFORM="--platform linux/arm64"
REGISTRY="localhost:5001"

echo "Cleaning and re-pulling LitmusChaos images with arm64 architecture..."
echo ""

# List of LitmusChaos images
IMAGES=(
    "litmuschaos/chaos-operator:3.23.0"
    "litmuschaos/chaos-runner:3.23.0"
    "litmuschaos/chaos-exporter:3.23.0"
    "litmuschaos/litmusportal-subscriber:3.23.0"
    "litmuschaos/litmusportal-event-tracker:3.23.0"
    "litmuschaos/go-runner:3.23.0"
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
