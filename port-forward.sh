#!/bin/bash
set -e

echo "🔌 Starting Port Forwarding..."

# Kill existing port-forwards if any
pkill -f "kubectl port-forward" || true

# Grafana (30080 -> 80)
echo "📊 Forwarding Grafana: http://localhost:30080"
kubectl port-forward svc/monitoring-grafana 30080:80 -n monitoring > /dev/null 2>&1 &

# Kafka UI (30081 -> 8080)
echo "🖥️  Forwarding Kafka UI: http://localhost:30081"
kubectl port-forward svc/kafka-ui 30081:8080 -n kafka > /dev/null 2>&1 &

# Prometheus (30090 -> 9090)
echo "🔥 Forwarding Prometheus: http://localhost:30090"
kubectl port-forward svc/monitoring-kube-prometheus-prometheus 30090:9090 -n monitoring > /dev/null 2>&1 &

# LitmusChaos Frontend (9091 -> 9091)
echo "⚡ Forwarding Litmus UI: http://localhost:9091"
kubectl port-forward svc/chaos-litmus-frontend-service 9091:9091 -n litmus > /dev/null 2>&1 &

# Apicurio Registry (30082 -> 8080)
echo "📚 Forwarding Apicurio Registry: http://localhost:30082"
kubectl port-forward svc/apicurio-registry 30082:8080 -n apicurio > /dev/null 2>&1 &

echo "✅ Port forwarding started in background!"
echo "Press Ctrl+C to stop (this script exits but forwards keep running)"
