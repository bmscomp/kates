#!/bin/bash
set -e
KIND_CLUSTER_NAME="panda"

# Images that are CONFIRMED missing/needed based on pod specs
IMAGES=(
    "docker.io/mongo:8.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-frontend:3.23.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-server:3.23.0"
    "litmuschaos.docker.scarf.sh/litmuschaos/litmusportal-auth-server:3.23.0"
)

echo "Cleaning up local Docker..."
docker rmi -f "${IMAGES[@]}" 2>/dev/null || true

echo ""
echo "Pulling fresh images (linux/amd64)..."
for image in "${IMAGES[@]}"; do
    echo "Processing $image"
    if docker pull --platform linux/amd64 "$image"; then
        echo "  ✓ Pulled"
        echo "  Loading into Kind..."
        if kind load docker-image "$image" --name "${KIND_CLUSTER_NAME}"; then
            echo "  ✓ Loaded"
        else
            echo "  ✗ Failed to load"
        fi
    else
        echo "  ✗ Failed to pull"
    fi
    echo ""
done

echo "Verify loaded images:"
docker exec "${KIND_CLUSTER_NAME}-control-plane" crictl images | grep -E "mongo|litmus"
