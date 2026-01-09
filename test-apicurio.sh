#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

REGISTRY_PORT=30082
REGISTRY_URL="http://localhost:${REGISTRY_PORT}/apis/registry/v2"

echo -e "${GREEN}Testing Apicurio Registry Integration...${NC}"

# Start port forwarding
echo "Starting port-forwarding to Apicurio..."
kubectl port-forward -n apicurio svc/apicurio-registry ${REGISTRY_PORT}:8080 > /dev/null 2>&1 &
PF_PID=$!

# Ensure cleanup on exit
cleanup() {
    echo "Stopping port-forward (PID: $PF_PID)..."
    kill $PF_PID
}
trap cleanup EXIT

# Wait for port to be ready
echo "Waiting for port-forward..."
sleep 5

# Check health
echo "Checking Registry Health..."
if curl -s "${REGISTRY_URL}/system/info" | grep -q "name"; then
    echo -e "${GREEN}✓ Registry is healthy and reachable${NC}"
else
    echo -e "${RED}✗ Registry is not reachable at ${REGISTRY_URL}${NC}"
    exit 1
fi

# Register a schema
echo "Registering a test schema..."
SCHEMA='{"type":"record","name":"User","fields":[{"name":"name","type":"string"}]}'
RESPONSE=$(curl -s -X POST "${REGISTRY_URL}/groups/default/artifacts" \
    -H "Content-Type: application/json" \
    -H "X-Registry-ArtifactId: test-schema-1" \
    -d "$SCHEMA")

if echo "$RESPONSE" | grep -q "test-schema-1"; then
    echo -e "${GREEN}✓ Schema registered successfully${NC}"
else
    # It might fail if already exists, checking that case
    if echo "$RESPONSE" | grep -q "ArtifactAlreadyExistsException"; then
         echo -e "${GREEN}✓ Schema already exists (Test Passed)${NC}"
    else
         echo -e "${RED}✗ Failed to register schema: $RESPONSE${NC}"
         exit 1
    fi
fi

echo ""
echo -e "${GREEN}Integration Test Passed!${NC}"
