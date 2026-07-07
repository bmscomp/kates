#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

require_cmd kind
require_cmd kubectl
require_cmd docker

usage() {
    cat <<'EOF'
Usage: ./scripts/start-cluster.sh [options]

Options:
  --http-proxy URL    HTTP proxy URL (overrides env/proxy.conf)
  --https-proxy URL   HTTPS proxy URL (overrides env/proxy.conf)
  --no-proxy LIST     NO_PROXY value (overrides env/proxy.conf)
  --proxy-ca-file P   PEM/CRT file to trust inside Kind nodes for proxy TLS
  --clear-proxy       Remove managed containerd proxy config from Kind nodes
  --clear-proxy-ca    Remove managed proxy CA from Kind nodes
  -h, --help          Show this help
EOF
}

HTTP_PROXY_ARG=""
HTTPS_PROXY_ARG=""
NO_PROXY_ARG=""
PROXY_CA_FILE_ARG=""
CLEAR_PROXY="false"
CLEAR_PROXY_CA="false"
HTTP_PROXY_FROM_FLAG="false"
HTTPS_PROXY_FROM_FLAG="false"

while [ $# -gt 0 ]; do
    case "$1" in
        --http-proxy)
            [ $# -ge 2 ] || { error "Missing value for --http-proxy"; exit 1; }
            HTTP_PROXY_ARG="$2"
            HTTP_PROXY_FROM_FLAG="true"
            shift 2
            ;;
        --https-proxy)
            [ $# -ge 2 ] || { error "Missing value for --https-proxy"; exit 1; }
            HTTPS_PROXY_ARG="$2"
            HTTPS_PROXY_FROM_FLAG="true"
            shift 2
            ;;
        --no-proxy)
            [ $# -ge 2 ] || { error "Missing value for --no-proxy"; exit 1; }
            NO_PROXY_ARG="$2"
            shift 2
            ;;
        --proxy-ca-file)
            [ $# -ge 2 ] || { error "Missing value for --proxy-ca-file"; exit 1; }
            PROXY_CA_FILE_ARG="$2"
            shift 2
            ;;
        --clear-proxy)
            CLEAR_PROXY="true"
            shift
            ;;
        --clear-proxy-ca)
            CLEAR_PROXY_CA="true"
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            error "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

info "Starting Kind Cluster Setup..."

# Prefer uppercase proxy vars; fall back to lowercase if needed.
first_non_empty() {
    local first="${1:-}"
    local second="${2:-}"
    if [ -n "${first}" ]; then
        echo "${first}"
    else
        echo "${second}"
    fi
}

is_loopback_proxy_url() {
    local proxy_url="${1:-}"
    case "${proxy_url}" in
        *"://localhost"*|*"://127.0.0.1"*|*"://[::1]"*|*"@localhost"*|*"@127.0.0.1"*|*"@[::1]"*)
            return 0
            ;;
        *)
            return 1
            ;;
    esac
}

rewrite_loopback_proxy_url_for_kind() {
    local proxy_url="${1:-}"
    local rewritten="${proxy_url}"
    rewritten="${rewritten/:\/\/localhost/://host.docker.internal}"
    rewritten="${rewritten/:\/\/127.0.0.1/://host.docker.internal}"
    rewritten="${rewritten/:\/\/[::1]/://host.docker.internal}"
    rewritten="${rewritten/@localhost/@host.docker.internal}"
    rewritten="${rewritten/@127.0.0.1/@host.docker.internal}"
    rewritten="${rewritten/@[::1]/@host.docker.internal}"
    echo "${rewritten}"
}

resolve_path() {
    local input="${1:-}"
    if [ -z "${input}" ]; then
        return 0
    fi
    if [ "${input#/}" != "${input}" ]; then
        echo "${input}"
    else
        local dir
        dir="$(cd "$(dirname "${input}")" && pwd)"
        echo "${dir}/$(basename "${input}")"
    fi
}

containerd_proxy_reconcile() {
    local http_proxy_value="${1:-}"
    local https_proxy_value="${2:-}"
    local no_proxy_value="${3:-}"
    local proxy_ca_file="${4:-}"
    local clear_proxy_ca="${5:-false}"
    local nodes=()

    while IFS= read -r node; do
        [ -n "${node}" ] && nodes+=("${node}")
    done < <(kind get nodes --name "${KIND_CLUSTER_NAME}" 2>/dev/null)
    if [ ${#nodes[@]} -eq 0 ]; then
        warn "No Kind nodes found for proxy reconciliation"
        return 0
    fi

    if [ -n "${http_proxy_value}" ] || [ -n "${https_proxy_value}" ]; then
        info "Proxy detected; configuring containerd proxy on Kind nodes..."
    else
        info "No HTTP/HTTPS proxy configured; clearing containerd proxy vars on Kind nodes..."
    fi

    if [ "${clear_proxy_ca}" = "true" ]; then
        info "Clearing managed proxy CA from Kind nodes..."
    elif [ -n "${proxy_ca_file}" ]; then
        info "Installing proxy CA into Kind nodes from ${proxy_ca_file}..."
        for node in "${nodes[@]}"; do
            docker cp "${proxy_ca_file}" "${node}:/root/kates-proxy-ca.crt"
        done
    fi

    for node in "${nodes[@]}"; do
        docker exec \
            -e HTTP_PROXY="${http_proxy_value}" \
            -e HTTPS_PROXY="${https_proxy_value}" \
            -e NO_PROXY="${no_proxy_value}" \
            -e CLEAR_PROXY_CA="${clear_proxy_ca}" \
            "${node}" /bin/bash -lc '
set -e
dropin_dir="/etc/systemd/system/containerd.service.d"
dropin_file="${dropin_dir}/10-kates-proxy.conf"
tmp_file="${dropin_file}.tmp"
managed_ca="/usr/local/share/ca-certificates/kates-proxy-ca.crt"
staged_ca="/root/kates-proxy-ca.crt"
mkdir -p "${dropin_dir}"
{
  echo "[Service]"
  printf "Environment=\"HTTP_PROXY=%s\"\n" "${HTTP_PROXY:-}"
  printf "Environment=\"http_proxy=%s\"\n" "${HTTP_PROXY:-}"
  printf "Environment=\"HTTPS_PROXY=%s\"\n" "${HTTPS_PROXY:-}"
  printf "Environment=\"https_proxy=%s\"\n" "${HTTPS_PROXY:-}"
  printf "Environment=\"NO_PROXY=%s\"\n" "${NO_PROXY:-}"
  printf "Environment=\"no_proxy=%s\"\n" "${NO_PROXY:-}"
} > "${tmp_file}"
mv "${tmp_file}" "${dropin_file}"

if [ "${CLEAR_PROXY_CA:-false}" = "true" ]; then
  rm -f "${staged_ca}"
  if [ -f "${managed_ca}" ]; then
    rm -f "${managed_ca}"
    update-ca-certificates
  fi
elif [ -f "${staged_ca}" ]; then
  mv "${staged_ca}" "${managed_ca}"
  chmod 0644 "${managed_ca}"
  update-ca-certificates
fi

systemctl daemon-reload
systemctl restart containerd
'
    done
}

# Source proxy configuration if it exists
if [ -f "${SCRIPT_DIR}/../proxy/proxy.conf" ]; then
    info "Loading proxy configuration..."
    set -a
    source "${SCRIPT_DIR}/../proxy/proxy.conf"
    set +a
fi

HTTP_PROXY_VALUE="$(first_non_empty "${HTTP_PROXY:-}" "${http_proxy:-}")"
HTTPS_PROXY_VALUE="$(first_non_empty "${HTTPS_PROXY:-}" "${https_proxy:-}")"
NO_PROXY_VALUE="$(first_non_empty "${NO_PROXY:-}" "${no_proxy:-}")"
PROXY_CA_FILE_VALUE="${PROXY_CA_FILE:-}"
if [ -f "${SCRIPT_DIR}/../proxy/ca.crt" ] && [ -z "${PROXY_CA_FILE_VALUE}" ]; then
    PROXY_CA_FILE_VALUE="${SCRIPT_DIR}/../proxy/ca.crt"
fi
if [ -f "${SCRIPT_DIR}/../proxy/ca.pem" ] && [ -z "${PROXY_CA_FILE_VALUE}" ]; then
    PROXY_CA_FILE_VALUE="${SCRIPT_DIR}/../proxy/ca.pem"
fi

# CLI flags override environment/proxy.conf.
HTTP_PROXY_VALUE="$(first_non_empty "${HTTP_PROXY_ARG}" "${HTTP_PROXY_VALUE}")"
HTTPS_PROXY_VALUE="$(first_non_empty "${HTTPS_PROXY_ARG}" "${HTTPS_PROXY_VALUE}")"
NO_PROXY_VALUE="$(first_non_empty "${NO_PROXY_ARG}" "${NO_PROXY_VALUE}")"
PROXY_CA_FILE_VALUE="$(first_non_empty "${PROXY_CA_FILE_ARG}" "${PROXY_CA_FILE_VALUE}")"

if [ -n "${PROXY_CA_FILE_VALUE}" ]; then
    PROXY_CA_FILE_VALUE="$(resolve_path "${PROXY_CA_FILE_VALUE}")"
    if [ ! -f "${PROXY_CA_FILE_VALUE}" ]; then
        error "Proxy CA file not found: ${PROXY_CA_FILE_VALUE}"
        exit 1
    fi
fi

if [ "${HTTP_PROXY_FROM_FLAG}" != "true" ] && is_loopback_proxy_url "${HTTP_PROXY_VALUE}"; then
    warn "Ignoring loopback HTTP_PROXY from environment/proxy.conf (${HTTP_PROXY_VALUE})"
    warn "Pass --http-proxy explicitly if you really want a loopback proxy for Kind nodes"
    HTTP_PROXY_VALUE=""
elif [ "${HTTP_PROXY_FROM_FLAG}" = "true" ] && is_loopback_proxy_url "${HTTP_PROXY_VALUE}"; then
    HTTP_PROXY_VALUE="$(rewrite_loopback_proxy_url_for_kind "${HTTP_PROXY_VALUE}")"
    warn "Rewriting --http-proxy loopback host to '${HTTP_PROXY_VALUE}' for Kind node reachability"
fi

if [ "${HTTPS_PROXY_FROM_FLAG}" != "true" ] && is_loopback_proxy_url "${HTTPS_PROXY_VALUE}"; then
    warn "Ignoring loopback HTTPS_PROXY from environment/proxy.conf (${HTTPS_PROXY_VALUE})"
    warn "Pass --https-proxy explicitly if you really want a loopback proxy for Kind nodes"
    HTTPS_PROXY_VALUE=""
elif [ "${HTTPS_PROXY_FROM_FLAG}" = "true" ] && is_loopback_proxy_url "${HTTPS_PROXY_VALUE}"; then
    HTTPS_PROXY_VALUE="$(rewrite_loopback_proxy_url_for_kind "${HTTPS_PROXY_VALUE}")"
    warn "Rewriting --https-proxy loopback host to '${HTTPS_PROXY_VALUE}' for Kind node reachability"
fi

if [ "${CLEAR_PROXY}" = "true" ]; then
    HTTP_PROXY_VALUE=""
    HTTPS_PROXY_VALUE=""
    NO_PROXY_VALUE=""
fi

if [ "${CLEAR_PROXY_CA}" = "true" ]; then
    PROXY_CA_FILE_VALUE=""
fi

# Create cluster only if it doesn't exist; recover kubeconfig if stale
if kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
    if kubectl cluster-info --context "kind-${KIND_CLUSTER_NAME}" >/dev/null 2>&1; then
        info "Cluster '${KIND_CLUSTER_NAME}' already running — skipping creation"
    else
        warn "Cluster '${KIND_CLUSTER_NAME}' exists but API unreachable — refreshing kubeconfig"
        kind export kubeconfig --name "${KIND_CLUSTER_NAME}"
    fi
else
    info "Creating Kind cluster..."
    if [ -n "${HTTP_PROXY_VALUE}" ] || [ -n "${HTTPS_PROXY_VALUE}" ] || [ -n "${NO_PROXY_VALUE}" ]; then
        HTTP_PROXY="${HTTP_PROXY_VALUE}" \
        HTTPS_PROXY="${HTTPS_PROXY_VALUE}" \
        NO_PROXY="${NO_PROXY_VALUE}" \
        kind create cluster --config "${SCRIPT_DIR}/../config/cluster.yaml" --name "${KIND_CLUSTER_NAME}"
    else
        kind create cluster --config "${SCRIPT_DIR}/../config/cluster.yaml" --name "${KIND_CLUSTER_NAME}"
    fi
fi

containerd_proxy_reconcile "${HTTP_PROXY_VALUE}" "${HTTPS_PROXY_VALUE}" "${NO_PROXY_VALUE}" "${PROXY_CA_FILE_VALUE}" "${CLEAR_PROXY_CA}"
kubectl wait --for=condition=Ready node --all --timeout=180s >/dev/null 2>&1 || \
    warn "Node readiness check timed out after containerd proxy reconciliation"

# Untaint control-plane node so Kafka pods can schedule on all AZ nodes
# (Kind uses one of the 3 worker nodes as control-plane)
for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
    kubectl taint nodes "${node}" node-role.kubernetes.io/control-plane:NoSchedule- 2>/dev/null || true
done

echo ""
info "✅ Kind Cluster Setup Complete!"
echo ""
echo "Cluster '${KIND_CLUSTER_NAME}' is ready!"
echo ""
echo "Next steps:"
echo "  - Deploy monitoring: make monitoring"
echo "  - Deploy full stack: make deploy-all"
echo "  - Check cluster: kubectl get nodes"
