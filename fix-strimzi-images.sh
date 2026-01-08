#!/bin/bash
# Fix Strimzi images - re-pull with correct arm64 architecture

set -e

PLATFORM="--platform linux/arm64"
REGISTRY="localhost:5001"
KIND_CLUSTER_NAME="panda"

echo "Cleaning and re-pulling Strimzi images with arm64 architecture..."
echo ""

# List of Strimzi images
IMAGES=(
    "quay.io/strimzi/operator:0.49.0"
    "quay.io/strimzi/operator:0.49.1"
    "quay.io/strimzi/kafka:0.49.1-kafka-4.1.1"
)

for image in "${IMAGES[@]}"; do
    echo "Processing: $image"
    
    # Remove old images locally
    docker image rm "$image" "${REGISTRY}/$image" 2>/dev/null || true
    
    # Pull with correct platform
    echo "  Pulling linux/arm64..."
    docker pull $PLATFORM "$image"
    
    # Tag for local registry
    docker tag "$image" "${REGISTRY}/$image"
    
    # Push to registry
    echo "  Pushing to local registry..."
    docker push "${REGISTRY}/$image"
    
    # Remove from kind if present (so it can be re-loaded)
    echo "  Cleaning from kind cluster..."
    # We need to find the Image ID to delete it
    IMAGE_ID=$(docker exec ${KIND_CLUSTER_NAME}-control-plane crictl images -q "${image}" || true)
    if [ ! -z "$IMAGE_ID" ]; then
         docker exec ${KIND_CLUSTER_NAME}-control-plane crictl rmi "$IMAGE_ID" >/dev/null 2>&1 || true
         docker exec ${KIND_CLUSTER_NAME}-worker crictl rmi "$IMAGE_ID" >/dev/null 2>&1 || true
         docker exec ${KIND_CLUSTER_NAME}-worker2 crictl rmi "$IMAGE_ID" >/dev/null 2>&1 || true
         echo "  Removed from kind nodes"
    fi
    
    echo "  ✓ Done"
    echo ""
done

echo "All Strimzi images fixed!"
echo "Now you can run ./load-images-to-kind.sh again"
