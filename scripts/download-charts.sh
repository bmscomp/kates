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

step "Building kafka-cluster chart dependencies (Strimzi v${STRIMZI_VERSION})..."
helm dependency build "${CHARTS_DIR}/kafka-cluster"

echo ""
info "Downloaded charts:"
ls -la "${CHARTS_DIR}"

echo ""
info "✅ Chart download complete!"
echo "Charts saved to: ${CHARTS_DIR}/"
