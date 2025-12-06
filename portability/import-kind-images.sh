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
IMAGE_LIST_FILE="${EXPORT_DIR}/image-list.txt"

echo -e "${BLUE}=== Import Images into Kind Cluster ===${NC}"
echo ""

# Check if export directory exists
if [ ! -d "${EXPORT_DIR}" ]; then
    echo -e "${RED}Error: Export directory not found: ${EXPORT_DIR}${NC}"
    echo "Please run ./export-kind-images.sh first"
    exit 1
fi

# Check if image list exists
if [ ! -f "${IMAGE_LIST_FILE}" ]; then
    echo -e "${RED}Error: Image list not found: ${IMAGE_LIST_FILE}${NC}"
    echo "Please run ./export-kind-images.sh first"
    exit 1
fi

# Check if Kind cluster exists
if ! kind get clusters 2>/dev/null | grep -qx "${KIND_CLUSTER_NAME}"; then
    echo -e "${RED}Error: Kind cluster '${KIND_CLUSTER_NAME}' not found${NC}"
    echo "Please create the cluster first with: make all"
    exit 1
fi

echo -e "${GREEN}Found Kind cluster: ${KIND_CLUSTER_NAME}${NC}"
echo -e "${GREEN}Import directory: ${EXPORT_DIR}${NC}"
echo ""

# Count tar files
TAR_COUNT=$(find "${EXPORT_DIR}" -name "*.tar" | wc -l | tr -d ' ')
echo -e "${GREEN}Found ${TAR_COUNT} image archives to import${NC}"
echo ""

if [ "$TAR_COUNT" -eq 0 ]; then
    echo -e "${RED}No tar files found in ${EXPORT_DIR}${NC}"
    exit 1
fi

# Import images
echo -e "${BLUE}Importing images into Kind cluster...${NC}"
echo ""

IMPORTED=0
FAILED=0
SKIPPED=0

# Read image list
while IFS= read -r image; do
    # Skip pause images and other system images
    if [[ "$image" =~ "pause:" ]] || [[ "$image" =~ "local-path-provisioner" ]]; then
        ((SKIPPED++))
        continue
    fi
    
    # Create safe filename from image name
    SAFE_NAME=$(echo "$image" | sed 's/[\/:]/_/g' | sed 's/@sha256_.*$//')
    TAR_FILE="${EXPORT_DIR}/${SAFE_NAME}.tar"
    
    if [ ! -f "${TAR_FILE}" ]; then
        echo -e "${YELLOW}⚠ Archive not found: ${SAFE_NAME}.tar${NC}"
        ((FAILED++))
        continue
    fi
    
    echo "Importing: ${image}"
    
    # Load into Docker first
    if docker load -i "${TAR_FILE}" >/dev/null 2>&1; then
        # Load into Kind
        if kind load docker-image "$image" --name "${KIND_CLUSTER_NAME}" 2>/dev/null; then
            echo -e "${GREEN}  ✓ Loaded into Kind${NC}"
            ((IMPORTED++))
        else
            echo -e "${RED}  ✗ Failed to load into Kind${NC}"
            ((FAILED++))
        fi
    else
        echo -e "${RED}  ✗ Failed to load into Docker${NC}"
        ((FAILED++))
    fi
    echo ""
done < "${IMAGE_LIST_FILE}"

# Summary
echo ""
echo -e "${BLUE}=== Import Summary ===${NC}"
echo -e "${GREEN}✓ Successfully imported: ${IMPORTED} images${NC}"
if [ $SKIPPED -gt 0 ]; then
    echo -e "${YELLOW}⊘ Skipped system images: ${SKIPPED}${NC}"
fi
if [ $FAILED -gt 0 ]; then
    echo -e "${RED}✗ Failed to import: ${FAILED} images${NC}"
fi
echo ""
echo -e "${GREEN}All images have been loaded into Kind cluster '${KIND_CLUSTER_NAME}'${NC}"
echo ""
