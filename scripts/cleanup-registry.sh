#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

REGISTRY_NAME="kind-registry"

echo -e "${YELLOW}Cleaning up local Docker registry...${NC}"

# Stop and remove registry container
if [ "$(docker ps -aq -f name=${REGISTRY_NAME})" ]; then
    echo "Stopping registry container..."
    docker stop ${REGISTRY_NAME} 2>/dev/null || true
    echo "Removing registry container..."
    docker rm ${REGISTRY_NAME} 2>/dev/null || true
    echo -e "${GREEN}✓ Registry container removed${NC}"
else
    echo -e "${YELLOW}Registry container does not exist${NC}"
fi

# Optionally remove the volume (commented out by default to preserve data)
# Uncomment the following lines to also remove the registry data volume
# echo "Removing registry data volume..."
# docker volume rm kind-registry-data 2>/dev/null || true
# echo -e "${GREEN}✓ Registry data volume removed${NC}"

# Restore Kafka UI configuration to use Docker Hub instead of local registry
echo ""
echo "Restoring Kafka UI configuration to use Docker Hub..."
KAFKA_UI_CONFIG="config/kafka-ui.yaml"

if [ -f "$KAFKA_UI_CONFIG" ]; then
    # Replace localhost:5001/provectuslabs/kafka-ui:latest with provectuslabs/kafka-ui:latest
    if grep -q "localhost:5001/provectuslabs/kafka-ui:latest" "$KAFKA_UI_CONFIG"; then
        sed -i.bak 's|localhost:5001/provectuslabs/kafka-ui:latest|provectuslabs/kafka-ui:latest|g' "$KAFKA_UI_CONFIG"
        rm -f "${KAFKA_UI_CONFIG}.bak"
        echo -e "${GREEN}✓ Kafka UI configuration restored to use Docker Hub${NC}"
    else
        echo -e "${YELLOW}Kafka UI is already configured to use Docker Hub${NC}"
    fi
else
    echo -e "${YELLOW}Warning: Kafka UI config file not found at $KAFKA_UI_CONFIG${NC}"
fi

echo ""
echo -e "${GREEN}Registry cleanup complete${NC}"
echo ""
echo "Configuration changes:"
echo "  ✓ Local registry container removed"
echo "  ✓ Kafka UI restored to pull from Docker Hub"
echo ""
echo "To remove the registry data volume as well, run:"
echo "  docker volume rm kind-registry-data"
