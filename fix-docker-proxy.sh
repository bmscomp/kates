#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

DOCKER_SETTINGS="$HOME/Library/Group Containers/group.com.docker/settings.json"

echo -e "${GREEN}Configuring Docker Desktop Proxy Settings...${NC}"
echo ""

# Check if Docker Desktop settings file exists
if [ ! -f "$DOCKER_SETTINGS" ]; then
    echo -e "${RED}Error: Docker Desktop settings file not found at:${NC}"
    echo "$DOCKER_SETTINGS"
    echo ""
    echo "Please ensure Docker Desktop is installed and has been run at least once."
    exit 1
fi

# Backup the current settings
BACKUP_FILE="${DOCKER_SETTINGS}.backup.$(date +%Y%m%d_%H%M%S)"
echo "Creating backup: $BACKUP_FILE"
cp "$DOCKER_SETTINGS" "$BACKUP_FILE"

# NO_PROXY entries for container registries and local registry
NO_PROXY_ENTRIES="localhost,127.0.0.1,docker.io,*.docker.io,quay.io,*.quay.io,gcr.io,*.gcr.io,registry.k8s.io,*.registry.k8s.io,litmuschaos.docker.scarf.sh,*.scarf.sh,kind-registry,hubproxy.docker.internal"

echo ""
echo "Updating proxy settings..."
echo "  - HTTP Proxy: http.docker.internal:3128 (keeping existing)"
echo "  - HTTPS Proxy: http.docker.internal:3128 (keeping existing)"
echo "  - NO_PROXY: $NO_PROXY_ENTRIES"
echo ""

# Use jq to update the settings (create it if it doesn't exist)
if command -v jq >/dev/null 2>&1; then
    # Update using jq
    jq --arg noproxy "$NO_PROXY_ENTRIES" \
       '.proxies.httpProxy = "http://host.docker.internal:3128" |
        .proxies.httpsProxy = "http://host.docker.internal:3128" |
        .proxies.exclude = $noproxy' \
       "$DOCKER_SETTINGS" > "${DOCKER_SETTINGS}.tmp" && \
       mv "${DOCKER_SETTINGS}.tmp" "$DOCKER_SETTINGS"
    
    echo -e "${GREEN}✓ Docker Desktop settings updated successfully${NC}"
else
    # Fallback: use sed/awk (less reliable but works without jq)
    echo -e "${YELLOW}Note: jq not found, using fallback method${NC}"
    echo "Please install jq for better JSON handling: brew install jq"
    echo ""
    
    # Manual JSON editing (only works if proxies section exists)
    if grep -q '"proxies"' "$DOCKER_SETTINGS"; then
        sed -i.tmp 's/"exclude"[[:space:]]*:[[:space:]]*"[^"]*"/"exclude": "'"$NO_PROXY_ENTRIES"'"/' "$DOCKER_SETTINGS"
        rm -f "${DOCKER_SETTINGS}.tmp"
        echo -e "${GREEN}✓ Updated NO_PROXY settings${NC}"
    else
        echo -e "${RED}Error: Could not update settings automatically${NC}"
        echo "Please manually add to Docker Desktop Settings → Proxies → Bypass:"
        echo "$NO_PROXY_ENTRIES"
        exit 1
    fi
fi

echo ""
echo -e "${YELLOW}⚠️  Docker Desktop needs to be restarted for changes to take effect${NC}"
echo ""
echo "To restart Docker Desktop:"
echo "  1. Quit Docker Desktop (Docker menu → Quit Docker Desktop)"
echo "  2. Start Docker Desktop again"
echo ""
echo "Or run this command to restart it automatically:"
echo "  osascript -e 'quit app \"Docker\"' && sleep 3 && open -a Docker"
echo ""

read -p "Would you like to restart Docker Desktop now? (y/N): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "Restarting Docker Desktop..."
    osascript -e 'quit app "Docker"' 2>/dev/null || true
    sleep 3
    open -a Docker
    echo ""
    echo "Waiting for Docker to start (30 seconds)..."
    sleep 30
    
    # Verify the changes
    echo ""
    echo "Verifying proxy settings..."
    docker info | grep -A2 "Proxy" || echo "Docker may still be starting..."
    echo ""
    echo -e "${GREEN}Done! You can now run 'make all'${NC}"
else
    echo ""
    echo "Please restart Docker Desktop manually when ready."
fi
