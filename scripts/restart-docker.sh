#!/bin/bash
set -e

# Colors for output
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m' # No Color

echo -e "${YELLOW}Docker Desktop Restart Script${NC}"
echo ""

echo "This script will properly restart Docker Desktop to fix daemon issues."
echo ""

echo "Step 1: Quitting Docker Desktop..."
osascript -e 'quit app "Docker"' 2>/dev/null || true

echo "Waiting for Docker to completely shut down..."
sleep 5

# Kill any remaining Docker processes
echo "Cleaning up any remaining Docker processes..."
pkill -f "Docker Desktop" 2>/dev/null || true
pkill -f "com.docker" 2>/dev/null || true
sleep 2

echo ""
echo "Step 2: Starting Docker Desktop..."
open -a Docker

echo "Waiting for Docker Desktop to start (this may take 60-90 seconds)..."
echo ""

# Wait for Docker to be ready
for i in {1..45}; do
    if docker info >/dev/null 2>&1; then
        echo -e "${GREEN}✓ Docker is ready!${NC}"
        docker version --format 'Docker version: {{.Server.Version}}'
        echo ""
        break
    fi
    printf "."
    sleep 2
    
    if [ $i -eq 45 ]; then
        echo ""
        echo -e "${RED}✗ Docker did not start within 90 seconds${NC}"
        echo ""
        echo "Please check Docker Desktop manually:"
        echo "  1. Look for the Docker whale icon in the menu bar"
        echo "  2. Click it and check the status"
        echo "  3. If it shows errors, try: Docker Desktop → Troubleshoot → Restart Docker Desktop"
        exit 1
    fi
done

echo "Verifying proxy settings..."
docker info | grep -A6 "Proxy" || echo -e "${YELLOW}No proxy info available${NC}"

echo ""
echo -e "${GREEN}Docker Desktop is ready!${NC}"
echo ""
echo "You can now run: make all"
