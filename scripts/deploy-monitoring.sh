#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

info "Deploying Monitoring Stack (Prometheus & Grafana)..."

ensure_namespace monitoring

info "Adding Helm repository..."
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update prometheus-community

info "Installing kube-prometheus-stack v${PROMETHEUS_STACK_VERSION}..."
helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
  --version "${PROMETHEUS_STACK_VERSION}" \
  --namespace monitoring \
  --values config/monitoring.yaml \
  --timeout 10m \
  --wait

info "Deploying Kates dashboards..."
kubectl create configmap kates-grafana-dashboards \
  --from-file=config/monitoring/kates-benchmark-dashboard.json \
  --from-file=config/monitoring/kates-trend-dashboard.json \
  --from-file=config/monitoring/kates-application-dashboard.json \
  --from-file=config/monitoring/grafana-chaos-dashboard.json \
  --namespace monitoring \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl label configmap kates-grafana-dashboards \
  --namespace monitoring \
  grafana_dashboard="1" \
  --overwrite

info "✅ Monitoring deployment complete!"
