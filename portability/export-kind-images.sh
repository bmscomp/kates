#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-panda}"
EXPORT_DIR="./kind-images-export"

echo -e "${BLUE}=== Export Images from Kind Cluster ===${NC}"
echo ""

# Check if Kind cluster exists
if ! kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
    echo -e "${RED}Error: Kind cluster '${KIND_CLUSTER_NAME}' not found${NC}"
    exit 1
fi

echo -e "${GREEN}Found Kind cluster: ${KIND_CLUSTER_NAME}${NC}"
echo ""

# Create export directory
mkdir -p "${EXPORT_DIR}"
echo -e "${GREEN}Export directory: ${EXPORT_DIR}${NC}"
echo ""

# Get the control plane node
CONTROL_PLANE_NODE=$(kind get nodes --name "${KIND_CLUSTER_NAME}" | grep control-plane | head -n 1)

if [ -z "$CONTROL_PLANE_NODE" ]; then
    echo -e "${RED}Error: Could not find control plane node${NC}"
    exit 1
fi

echo -e "${BLUE}Listing all images in Kind cluster...${NC}"
echo ""

# Get all images from the control plane node
IMAGES=$(docker exec "${CONTROL_PLANE_NODE}" crictl images --no-trunc -o json | \
    jq -r '.images[] | select(.repoTags != null) | .repoTags[]' | \
    grep -v '<none>' | \
    sort -u)

if [ -z "$IMAGES" ]; then
    echo -e "${RED}No images found in Kind cluster${NC}"
    exit 1
fi

# Count images
IMAGE_COUNT=$(echo "$IMAGES" | wc -l | tr -d ' ')
echo -e "${GREEN}Found ${IMAGE_COUNT} images in Kind cluster${NC}"
echo ""

# Create image list file
IMAGE_LIST_FILE="${EXPORT_DIR}/image-list.txt"
echo "$IMAGES" > "${IMAGE_LIST_FILE}"
echo -e "${GREEN}✓ Image list saved to: ${IMAGE_LIST_FILE}${NC}"
echo ""

# Export each image
echo -e "${BLUE}Exporting images to tar files...${NC}"
echo ""

EXPORTED=0
FAILED=0

while IFS= read -r image; do
    # Skip pause images and other system images
    if [[ "$image" =~ "pause:" ]] || [[ "$image" =~ "local-path-provisioner" ]]; then
        echo -e "${YELLOW}Skipping system image: ${image}${NC}"
        continue
    fi
    
    # Create safe filename from image name
    SAFE_NAME=$(echo "$image" | sed 's/[\/:]/_/g' | sed 's/@sha256_.*$//')
    TAR_FILE="${EXPORT_DIR}/${SAFE_NAME}.tar"
    
    echo "Exporting: ${image}"
    
    # Check if image exists in local Docker
    if docker image inspect "$image" >/dev/null 2>&1; then
        # Save from local Docker
        if docker save "$image" -o "${TAR_FILE}" 2>/dev/null; then
            echo -e "${GREEN}  ✓ Exported to: ${SAFE_NAME}.tar${NC}"
            ((EXPORTED++))
        else
            echo -e "${RED}  ✗ Failed to export${NC}"
            ((FAILED++))
        fi
    else
        echo -e "${YELLOW}  ⚠ Image not in local Docker, pulling from Kind...${NC}"
        # Export from Kind node
        if docker exec "${CONTROL_PLANE_NODE}" ctr -n k8s.io images export "/tmp/${SAFE_NAME}.tar" "$image" 2>/dev/null; then
            docker cp "${CONTROL_PLANE_NODE}:/tmp/${SAFE_NAME}.tar" "${TAR_FILE}"
            docker exec "${CONTROL_PLANE_NODE}" rm "/tmp/${SAFE_NAME}.tar"
            echo -e "${GREEN}  ✓ Exported from Kind to: ${SAFE_NAME}.tar${NC}"
            ((EXPORTED++))
        else
            echo -e "${RED}  ✗ Failed to export from Kind${NC}"
            ((FAILED++))
        fi
    fi
    echo ""
done <<< "$IMAGES"

# Summary
echo ""
echo -e "${BLUE}=== Export Summary ===${NC}"
echo -e "${GREEN}✓ Successfully exported: ${EXPORTED} images${NC}"
if [ $FAILED -gt 0 ]; then
    echo -e "${RED}✗ Failed to export: ${FAILED} images${NC}"
fi
echo ""
echo -e "${GREEN}Export directory: ${EXPORT_DIR}${NC}"
echo -e "${GREEN}Image list: ${IMAGE_LIST_FILE}${NC}"
echo ""
echo "To import these images into a new Kind cluster, run:"
echo -e "  ${BLUE}./import-kind-images.sh${NC}"
echo ""
