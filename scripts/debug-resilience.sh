#!/bin/bash
echo "=== Kates Resilience EOF Debugging Script ==="

echo -e "\n1. Checking local port-forward process..."
ps aux | grep "kubectl port-forward" | grep -v grep

echo -e "\n2. Testing basic connectivity to Kates backend..."
curl -s -v http://localhost:8080/api/health

echo -e "\n\n3. Fetching latest Kates backend logs to see if it crashed or finished the test..."
# Note: Adjust namespace if kates is not in the 'kates' namespace
kubectl logs -n kates -l app.kubernetes.io/name=kates --tail=50
