#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"
source "${SCRIPT_DIR}/../versions.env"

info "Downloading Helm charts for offline installation..."

mkdir -p "${CHARTS_DIR}"

step "Downloading Litmus chart (v${LITMUS_CHART_VERSION})..."
helm repo add litmuschaos https://litmuschaos.github.io/litmus-helm/ 2>/dev/null || true
helm repo update litmuschaos
rm -rf "${CHARTS_DIR}/litmus"
helm pull litmuschaos/litmus --version "${LITMUS_CHART_VERSION}" --untar --untardir "${CHARTS_DIR}"

step "Downloading Strimzi Kafka Operator chart (v${STRIMZI_VERSION})..."
helm repo add strimzi https://strimzi.io/charts/ 2>/dev/null || true
helm repo update strimzi
rm -rf "${CHARTS_DIR}/strimzi-kafka-operator"
helm pull strimzi/strimzi-kafka-operator --version "${STRIMZI_VERSION}" --untar --untardir "${CHARTS_DIR}"

step "Downloading kube-prometheus-stack chart (v${PROMETHEUS_STACK_VERSION})..."
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts 2>/dev/null || true
helm repo update prometheus-community
rm -rf "${CHARTS_DIR}/kube-prometheus-stack"
helm pull prometheus-community/kube-prometheus-stack --version "${PROMETHEUS_STACK_VERSION}" --untar --untardir "${CHARTS_DIR}"

echo ""
info "Downloaded charts:"
ls -la "${CHARTS_DIR}"

echo ""
info "✅ Chart download complete!"
echo "Charts saved to: ${CHARTS_DIR}/"
