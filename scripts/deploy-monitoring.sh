#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

MONITORING_CHART_DIR="${CHARTS_DIR}/kube-prometheus-stack"

info "Deploying Monitoring Stack (Prometheus & Grafana)..."

require_chart "${MONITORING_CHART_DIR}" "kube-prometheus-stack"

ensure_namespace monitoring

info "Installing Prometheus and Grafana from local chart..."
helm upgrade --install monitoring "${MONITORING_CHART_DIR}" \
  --namespace monitoring \
  --values config/monitoring.yaml \
  --timeout 10m \
  --wait

info "Applying custom dashboards..."
kubectl apply -f config/custom-dashboard.yaml

info "Deploying KATES benchmark dashboards..."
kubectl create configmap kates-grafana-dashboards \
  --from-file=config/monitoring/kates-benchmark-dashboard.json \
  --from-file=config/monitoring/kates-phase-dashboard.json \
  --from-file=config/monitoring/kates-trend-dashboard.json \
  --namespace monitoring \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl label configmap kates-grafana-dashboards \
  --namespace monitoring \
  grafana_dashboard="1" \
  --overwrite

info "✅ Monitoring deployment complete!"
