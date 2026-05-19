#!/bin/bash
# Shared constants, colors, and utility functions for all klster scripts.
# Source this file at the top of every script:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "${SCRIPT_DIR}/common.sh"

GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
BOLD='\033[1m'
NC='\033[0m'

KIND_CLUSTER_NAME="panda"
CHARTS_DIR="${SCRIPT_DIR}/../charts"


info()  { echo -e "${GREEN}$*${NC}"; }
warn()  { echo -e "${YELLOW}$*${NC}"; }
error() { echo -e "${RED}$*${NC}" >&2; }
step()  { echo -e "${BLUE}$*${NC}"; }
bold()  { echo -e "${BOLD}$*${NC}"; }

require_cmd() {
    command -v "$1" >/dev/null 2>&1 || { error "❌ $1 is required but not installed."; exit 1; }
}

require_cluster() {
    if ! kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
        error "Kind cluster '${KIND_CLUSTER_NAME}' not found. Run 'make cluster' first."
        exit 1
    fi
}


require_chart() {
    local chart_dir="$1"
    local chart_name="$2"
    if [ ! -d "${chart_dir}" ]; then
        error "Chart not found at ${chart_dir}"
        echo "Please run ./download-charts.sh first"
        exit 1
    fi
}

ensure_namespace() {
    if ! kubectl get namespace "$1" > /dev/null 2>&1; then
        kubectl create namespace "$1"
    fi
}

svc_exists() {
    kubectl get svc "$1" -n "$2" > /dev/null 2>&1
}

deployment_exists() {
    kubectl get deployment "$1" -n "$2" > /dev/null 2>&1
}

elapsed() {
    local secs=$SECONDS
    printf '%dm %ds' $((secs / 60)) $((secs % 60))
}

get_cluster_domain() {
    # Attempt 1: CoreDNS config
    local domain=$(kubectl get configmap coredns -n kube-system -o jsonpath='{.data.Corefile}' 2>/dev/null | grep -o 'kubernetes [^ ]*' | head -n1 | awk '{print $2}' || true)
    
    # Attempt 2: Temp pod
    if [ -z "$domain" ]; then
        domain=$(kubectl run --rm -i --image=busybox:1.36 dns-detect --restart=Never -- cat /etc/resolv.conf 2>/dev/null | grep "search" | awk '{print $2}' | sed 's/default\.svc\.//' || true)
    fi
    
    # Fallback
    if [ -z "$domain" ]; then
        domain="cluster.local"
    fi
    echo "$domain"
}
