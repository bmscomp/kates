#!/bin/bash

# Colors for output
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
NC='\033[0m' # No Color

echo -e "${YELLOW}Docker Proxy Configuration Check${NC}"
echo ""

# Check if Docker has proxy configured
PROXY_INFO=$(docker info 2>/dev/null | grep -i "proxy" | grep -v "No Proxy")

if [ -n "$PROXY_INFO" ]; then
    echo -e "${RED}⚠️  Docker is configured to use a proxy:${NC}"
    echo "$PROXY_INFO"
    echo ""
    echo -e "${YELLOW}This may cause issues when pulling images if the proxy is not accessible.${NC}"
    echo ""
    echo -e "${GREEN}RECOMMENDED SOLUTION:${NC}"
    echo "Configure Docker to bypass the proxy for container registries and local registry:"
    echo ""
    echo "1. Open Docker Desktop"
    echo "2. Go to Settings → Resources → Proxies"
    echo "3. In the 'Bypass proxy settings for these hosts & domains' field, add:"
    echo ""
    echo -e "${GREEN}   localhost,127.0.0.1,docker.io,*.docker.io,quay.io,*.quay.io,gcr.io,*.gcr.io,registry.k8s.io,*.registry.k8s.io,litmuschaos.docker.scarf.sh,*.scarf.sh,kind-registry${NC}"
    echo ""
    echo "4. Click 'Apply & Restart'"
    echo ""
    echo "This ensures:"
    echo "  ✓ Local registry (localhost:5001) is accessed directly"
    echo "  ✓ Docker Hub, Quay.io, GCR, and K8s registries bypass the proxy"
    echo "  ✓ No timeout issues when pulling images"
    echo ""
    
    # Test proxy connectivity
    PROXY_HOST=$(echo "$PROXY_INFO" | grep "HTTP Proxy" | awk '{print $3}')
    if [ -n "$PROXY_HOST" ]; then
        echo "Testing proxy connectivity to $PROXY_HOST..."
        if timeout 3 bash -c "echo > /dev/tcp/${PROXY_HOST%%:*}/${PROXY_HOST##*:}" 2>/dev/null; then
            echo -e "${GREEN}✓ Proxy is accessible${NC}"
            echo ""
            echo "However, you should still add the NO_PROXY entries above to avoid routing"
            echo "container registry traffic through the proxy unnecessarily."
        else
            echo -e "${RED}✗ Proxy is NOT accessible - this WILL cause image pull failures${NC}"
            echo ""
            echo -e "${YELLOW}ACTION REQUIRED:${NC} Add the NO_PROXY entries above before running 'make all'"
        fi
    fi
else
    echo -e "${GREEN}✓ No proxy configured in Docker${NC}"
fi
