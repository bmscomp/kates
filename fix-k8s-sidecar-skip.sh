#!/bin/bash
# Simpler alternative: Skip the problematic image entirely
# Use a pre-built alternative or skip if not critical

set -e

echo "⚠️  ALTERNATIVE APPROACH: Checking if Grafana can work without k8s-sidecar..."
echo ""
echo "The k8s-sidecar is used by Grafana to auto-load dashboards."
echo "If you don't need automatic dashboard loading, you can:"
echo ""
echo "1. Deploy without k8s-sidecar (dashboards loaded manually)"
echo "2. Or fix the Grafana values to disable sidecar"
echo ""
echo "To disable k8s-sidecar in Grafana:"
echo "Edit config/monitoring.yaml and add:"
echo ""
echo "  grafana:"
echo "    sidecar:"
echo "      dashboards:"
echo "        enabled: false"
echo ""
read -p "Do you want to continue without k8s-sidecar? (y/n) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "✅ Continuing without k8s-sidecar. You can load dashboards manually."
    echo "   See: kubectl port-forward -n monitoring svc/monitoring-grafana 3000:80"
else
    echo "Run ./fix-k8s-sidecar-nuclear.sh for the aggressive fix."
fi
