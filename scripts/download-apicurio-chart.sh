#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

info "Downloading Apicurio Registry Helm Chart..."

mkdir -p "${CHARTS_DIR}"

helm repo remove touk 2>/dev/null || true
helm repo add touk https://helm-charts.touk.pl/public/

step "Fetching chart from touk repo..."
helm pull touk/apicurio-registry --untar --untardir "${CHARTS_DIR}"

APICURIO_CHART_DIR="${CHARTS_DIR}/apicurio-registry"
if [ -d "${CHARTS_DIR}/apicurio-registry-helm" ]; then
    rm -rf "${APICURIO_CHART_DIR}"
    mv "${CHARTS_DIR}/apicurio-registry-helm" "${APICURIO_CHART_DIR}"
fi

info "✅ Chart downloaded to ${APICURIO_CHART_DIR}"
ls -F "${APICURIO_CHART_DIR}"
