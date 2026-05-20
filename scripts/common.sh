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
    local auto_approve="${1:-false}"
    local resolv_content=""
    
    # Attempt 1: Get from an already running pod in any namespace (fastest, most reliable)
    local pod=$(kubectl get pods --all-namespaces --field-selector=status.phase=Running -o jsonpath='{.items[0].metadata.namespace} {.items[0].metadata.name}' 2>/dev/null || true)
    if [ -n "$pod" ]; then
        local ns=$(echo "$pod" | awk '{print $1}')
        local name=$(echo "$pod" | awk '{print $2}')
        resolv_content=$(kubectl exec -n "$ns" "$name" -- cat /etc/resolv.conf 2>/dev/null || true)
    fi

    # Attempt 2: If no running pods, spin up a temporary pod
    if [ -z "$resolv_content" ]; then
        resolv_content=$(kubectl run --rm -i --image=busybox:1.36 dns-detect --restart=Never -- cat /etc/resolv.conf 2>/dev/null || true)
    fi
    
    local search_line=$(echo "$resolv_content" | grep -i "^search" || true)
    if [ -z "$search_line" ]; then
        echo "cluster.local"
        return
    fi

    # Print /etc/resolv.conf to stderr so it's visible but doesn't affect the captured return value
    echo "" >&2
    echo "${BLUE}--- Cluster DNS Context (/etc/resolv.conf) ---${NC}" >&2
    echo "$resolv_content" >&2
    echo "${BLUE}----------------------------------------------${NC}" >&2
    echo "" >&2

    local domains=($(echo "$search_line" | sed 's/^search//'))
    local valid_domains=()
    
    # Priority: Find the entry that starts exactly with "svc."
    for d in "${domains[@]}"; do
        if [[ "$d" == svc.* ]]; then
            local clean="${d#svc.}"
            if [ -n "$clean" ] && [[ ! " ${valid_domains[@]:-} " =~ " ${clean} " ]]; then
                valid_domains+=("$clean")
            fi
        fi
    done

    # Fallback: if no "svc.*" found, clean up the standard way
    if [ ${#valid_domains[@]} -eq 0 ]; then
        for d in "${domains[@]}"; do
            local clean=$(echo "$d" | sed -E 's/^[^\.]+\.svc\.//' | sed -E 's/^svc\.//')
            if [ -n "$clean" ] && [[ ! " ${valid_domains[@]:-} " =~ " ${clean} " ]]; then
                valid_domains+=("$clean")
            fi
        done
    fi

    if [ ${#valid_domains[@]} -eq 0 ]; then
        echo "cluster.local"
        return
    fi
    
    # Automatically select the first valid domain (prioritizing the one starting with svc.)
    echo "${valid_domains[0]}"
}
