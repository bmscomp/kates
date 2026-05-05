#!/bin/bash
# detect-cluster-config.sh — Auto-detect cluster configuration from kubeconfig
#
# Inspects the current Kubernetes context to discover:
#   - Cluster provider (EKS, GKE, AKS, Kind, k3s, generic)
#   - Available availability zones from node labels
#   - Available StorageClasses and selects the best default
#   - Node count and resource capacity
#
# Outputs a generated Helm values file for the kafka-cluster chart.
#
# Usage:
#   ./scripts/detect-cluster-config.sh                 # Print to stdout
#   ./scripts/detect-cluster-config.sh -o values.yaml  # Write to file
#   ./scripts/detect-cluster-config.sh --dry-run       # Show what would be detected

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="${SCRIPT_DIR}/.."

# Parse arguments
OUTPUT=""
DRY_RUN=false
while [[ $# -gt 0 ]]; do
    case "$1" in
        -o|--output) OUTPUT="$2"; shift 2 ;;
        --dry-run)   DRY_RUN=true; shift ;;
        -h|--help)
            echo "Usage: $0 [-o output.yaml] [--dry-run]"
            echo ""
            echo "Auto-detect cluster configuration for kafka-cluster Helm chart."
            echo ""
            echo "Options:"
            echo "  -o, --output FILE   Write generated values to FILE"
            echo "  --dry-run           Show detected configuration without generating values"
            echo "  -h, --help          Show this help"
            exit 0
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# ── Colors ─────────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${BLUE}ℹ${NC}  $*"; }
ok()    { echo -e "${GREEN}✓${NC}  $*"; }
warn()  { echo -e "${YELLOW}⚠${NC}  $*"; }
error() { echo -e "${RED}✗${NC}  $*"; }
header(){ echo -e "\n${BOLD}${CYAN}── $* ──${NC}"; }

# ── Verify connectivity ───────────────────────────────────────────────────────
header "Kubernetes Context"
CONTEXT=$(kubectl config current-context 2>/dev/null || true)
if [ -z "${CONTEXT}" ]; then
    error "No active kubeconfig context. Set one with: kubectl config use-context <name>"
    exit 1
fi
CLUSTER=$(kubectl config view --minify -o jsonpath='{.clusters[0].name}' 2>/dev/null || echo "unknown")
SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null || echo "unknown")
ok "Context:  ${CONTEXT}"
ok "Cluster:  ${CLUSTER}"
ok "Server:   ${SERVER}"

kubectl cluster-info &>/dev/null || {
    error "Cannot connect to cluster at ${SERVER}"
    exit 1
}

# ── Detect provider ───────────────────────────────────────────────────────────
header "Cluster Provider Detection"
PROVIDER="generic"

if echo "${CONTEXT}" | grep -qi 'kind-\|kind_'; then
    PROVIDER="kind"
elif echo "${CONTEXT}" | grep -qi 'eks\|arn:aws'; then
    PROVIDER="eks"
elif echo "${CONTEXT}" | grep -qi 'gke_'; then
    PROVIDER="gke"
elif echo "${CONTEXT}" | grep -qi 'aks\|azure'; then
    PROVIDER="aks"
elif echo "${SERVER}" | grep -qi 'eks.amazonaws.com'; then
    PROVIDER="eks"
elif echo "${SERVER}" | grep -qi 'azmk8s.io'; then
    PROVIDER="aks"
elif kubectl get nodes -o jsonpath='{.items[0].spec.providerID}' 2>/dev/null | grep -qi 'aws'; then
    PROVIDER="eks"
elif kubectl get nodes -o jsonpath='{.items[0].spec.providerID}' 2>/dev/null | grep -qi 'gce'; then
    PROVIDER="gke"
elif kubectl get nodes -o jsonpath='{.items[0].spec.providerID}' 2>/dev/null | grep -qi 'azure'; then
    PROVIDER="aks"
elif kubectl get nodes -o jsonpath='{.items[0].metadata.labels}' 2>/dev/null | grep -qi 'k3s'; then
    PROVIDER="k3s"
fi
ok "Provider: ${PROVIDER}"

# ── Detect nodes ──────────────────────────────────────────────────────────────
header "Node Inventory"
NODE_COUNT=$(kubectl get nodes --no-headers 2>/dev/null | wc -l | tr -d ' ')
READY_NODES=$(kubectl get nodes --no-headers 2>/dev/null | grep -c ' Ready' || echo "0")
ok "Nodes:       ${NODE_COUNT} total, ${READY_NODES} ready"

TOTAL_CPU=$(kubectl get nodes -o jsonpath='{range .items[*]}{.status.capacity.cpu}{"\n"}{end}' 2>/dev/null | awk '{s+=$1} END {print s}')
TOTAL_MEM_KI=$(kubectl get nodes -o jsonpath='{range .items[*]}{.status.capacity.memory}{"\n"}{end}' 2>/dev/null | sed 's/Ki//' | awk '{s+=$1} END {print s}')
TOTAL_MEM_GI=$(( TOTAL_MEM_KI / 1048576 ))
ok "Capacity:    ${TOTAL_CPU} CPU, ${TOTAL_MEM_GI} GiB memory"

# ── Detect availability zones ─────────────────────────────────────────────────
header "Availability Zones"
# Try standard topology label first, then legacy
ZONES=()
if kubectl get nodes -o jsonpath='{.items[*].metadata.labels.topology\.kubernetes\.io/zone}' 2>/dev/null | tr ' ' '\n' | sort -u | grep -q .; then
    while IFS= read -r zone; do
        [ -n "${zone}" ] && ZONES+=("${zone}")
    done < <(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.labels.topology\.kubernetes\.io/zone}{"\n"}{end}' 2>/dev/null | sort -u | grep .)
    ok "Zone label:  topology.kubernetes.io/zone"
elif kubectl get nodes -o jsonpath='{.items[*].metadata.labels.failure-domain\.beta\.kubernetes\.io/zone}' 2>/dev/null | tr ' ' '\n' | sort -u | grep -q .; then
    while IFS= read -r zone; do
        [ -n "${zone}" ] && ZONES+=("${zone}")
    done < <(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.labels.failure-domain\.beta\.kubernetes\.io/zone}{"\n"}{end}' 2>/dev/null | sort -u | grep .)
    ok "Zone label:  failure-domain.beta.kubernetes.io/zone (legacy)"
fi

ZONE_COUNT=${#ZONES[@]}
if [ "${ZONE_COUNT}" -gt 0 ]; then
    ok "Zones found: ${ZONE_COUNT}"
    for z in "${ZONES[@]}"; do
        NODES_IN_ZONE=$(kubectl get nodes -l "topology.kubernetes.io/zone=${z}" --no-headers 2>/dev/null | wc -l | tr -d ' ')
        echo -e "   ${CYAN}•${NC} ${z} (${NODES_IN_ZONE} node(s))"
    done
else
    warn "No zone labels found on nodes"
    warn "Broker pools will be created without zone affinity"
fi

# ── Detect storage classes ────────────────────────────────────────────────────
header "Storage Classes"
DEFAULT_SC=""
ALL_SC=()

while IFS= read -r sc_name; do
    [ -n "${sc_name}" ] && ALL_SC+=("${sc_name}")
done < <(kubectl get sc --no-headers -o custom-columns=NAME:.metadata.name 2>/dev/null | grep .)

if [ ${#ALL_SC[@]} -eq 0 ]; then
    warn "No StorageClasses found — using 'standard' as fallback"
    DEFAULT_SC="standard"
else
    # Find the default StorageClass
    DEFAULT_SC=$(kubectl get sc -o jsonpath='{range .items[?(@.metadata.annotations.storageclass\.kubernetes\.io/is-default-class=="true")]}{.metadata.name}{end}' 2>/dev/null || true)
    if [ -z "${DEFAULT_SC}" ]; then
        DEFAULT_SC=$(kubectl get sc -o jsonpath='{range .items[?(@.metadata.annotations.storageclass\.beta\.kubernetes\.io/is-default-class=="true")]}{.metadata.name}{end}' 2>/dev/null || true)
    fi

    # Provider-specific preferred SC
    if [ -z "${DEFAULT_SC}" ]; then
        case "${PROVIDER}" in
            eks) DEFAULT_SC=$(echo "${ALL_SC[@]}" | tr ' ' '\n' | grep -m1 'gp3\|gp2' || echo "${ALL_SC[0]}") ;;
            gke) DEFAULT_SC=$(echo "${ALL_SC[@]}" | tr ' ' '\n' | grep -m1 'standard-rwo\|premium-rwo' || echo "${ALL_SC[0]}") ;;
            aks) DEFAULT_SC=$(echo "${ALL_SC[@]}" | tr ' ' '\n' | grep -m1 'managed-csi\|managed-premium' || echo "${ALL_SC[0]}") ;;
            kind) DEFAULT_SC=$(echo "${ALL_SC[@]}" | tr ' ' '\n' | grep -m1 'local-storage\|standard' || echo "${ALL_SC[0]}") ;;
            *)   DEFAULT_SC="${ALL_SC[0]}" ;;
        esac
    fi

    ok "StorageClasses available: ${#ALL_SC[@]}"
    for sc in "${ALL_SC[@]}"; do
        PROVISIONER=$(kubectl get sc "${sc}" -o jsonpath='{.provisioner}' 2>/dev/null)
        MARKER=""
        if [ "${sc}" = "${DEFAULT_SC}" ]; then MARKER=" ${GREEN}(selected)${NC}"; fi
        echo -e "   ${CYAN}•${NC} ${sc} — ${PROVISIONER}${MARKER}"
    done
    ok "Selected:    ${DEFAULT_SC}"
fi

# ── Determine sizing ──────────────────────────────────────────────────────────
header "Sizing Recommendation"

BROKER_REPLICAS=1
CONTROLLER_REPLICAS=1
BROKER_STORAGE="50Gi"
CONTROLLER_STORAGE="5Gi"
BROKER_MEM_REQ="1Gi"
BROKER_CPU_REQ="500m"

if [ "${TOTAL_CPU}" -ge 24 ] && [ "${TOTAL_MEM_GI}" -ge 48 ]; then
    PROFILE="production"
    BROKER_REPLICAS=3
    CONTROLLER_REPLICAS=3
    BROKER_STORAGE="200Gi"
    CONTROLLER_STORAGE="20Gi"
    BROKER_MEM_REQ="4Gi"
    BROKER_CPU_REQ="1000m"
elif [ "${TOTAL_CPU}" -ge 12 ] && [ "${TOTAL_MEM_GI}" -ge 24 ]; then
    PROFILE="staging"
    BROKER_REPLICAS=1
    CONTROLLER_REPLICAS=3
    BROKER_STORAGE="100Gi"
    CONTROLLER_STORAGE="10Gi"
    BROKER_MEM_REQ="2Gi"
    BROKER_CPU_REQ="500m"
elif [ "${TOTAL_CPU}" -ge 4 ] && [ "${TOTAL_MEM_GI}" -ge 8 ]; then
    PROFILE="development"
    BROKER_REPLICAS=1
    CONTROLLER_REPLICAS=1
    BROKER_STORAGE="50Gi"
    CONTROLLER_STORAGE="5Gi"
else
    PROFILE="minimal"
    BROKER_REPLICAS=1
    CONTROLLER_REPLICAS=1
    BROKER_STORAGE="10Gi"
    CONTROLLER_STORAGE="1Gi"
    BROKER_MEM_REQ="512Mi"
    BROKER_CPU_REQ="250m"
fi
ok "Profile:     ${PROFILE} (${TOTAL_CPU} CPU / ${TOTAL_MEM_GI} GiB)"

# ── Summary ───────────────────────────────────────────────────────────────────
header "Configuration Summary"
echo ""
echo -e "  ${BOLD}Provider:${NC}      ${PROVIDER}"
echo -e "  ${BOLD}Context:${NC}       ${CONTEXT}"
echo -e "  ${BOLD}Zones:${NC}         ${ZONE_COUNT} (${ZONES[*]:-none})"
echo -e "  ${BOLD}StorageClass:${NC}  ${DEFAULT_SC}"
echo -e "  ${BOLD}Profile:${NC}       ${PROFILE}"
echo -e "  ${BOLD}Controllers:${NC}   ${CONTROLLER_REPLICAS} × ${CONTROLLER_STORAGE}"
echo -e "  ${BOLD}Brokers:${NC}       ${BROKER_REPLICAS} per zone × ${BROKER_STORAGE}"
echo ""

if [ "${DRY_RUN}" = true ]; then
    info "Dry run — no values file generated"
    exit 0
fi

# ── Generate values file ──────────────────────────────────────────────────────
header "Generating Values"

# Build broker pools YAML
BROKER_POOLS_YAML=""
if [ "${ZONE_COUNT}" -ge 3 ]; then
    # Use top 3 zones for 3-pool setup
    for i in 0 1 2; do
        ZONE="${ZONES[$i]}"
        POOL_NAME="brokers-az$((i+1))"
        BROKER_POOLS_YAML+="  - name: ${POOL_NAME}
    zone: \"${ZONE}\"
    replicas: ${BROKER_REPLICAS}
    storageSize: ${BROKER_STORAGE}
    storageClass: \"${DEFAULT_SC}\"
"
    done
elif [ "${ZONE_COUNT}" -eq 2 ]; then
    for i in 0 1; do
        ZONE="${ZONES[$i]}"
        POOL_NAME="brokers-az$((i+1))"
        BROKER_POOLS_YAML+="  - name: ${POOL_NAME}
    zone: \"${ZONE}\"
    replicas: ${BROKER_REPLICAS}
    storageSize: ${BROKER_STORAGE}
    storageClass: \"${DEFAULT_SC}\"
"
    done
    # Add a 3rd pool without zone pinning for quorum
    BROKER_POOLS_YAML+="  - name: brokers-az3
    zone: \"\"
    replicas: ${BROKER_REPLICAS}
    storageSize: ${BROKER_STORAGE}
    storageClass: \"${DEFAULT_SC}\"
"
elif [ "${ZONE_COUNT}" -eq 1 ]; then
    BROKER_POOLS_YAML+="  - name: brokers-az1
    zone: \"${ZONES[0]}\"
    replicas: ${BROKER_REPLICAS}
    storageSize: ${BROKER_STORAGE}
    storageClass: \"${DEFAULT_SC}\"
  - name: brokers-az2
    zone: \"\"
    replicas: ${BROKER_REPLICAS}
    storageSize: ${BROKER_STORAGE}
    storageClass: \"${DEFAULT_SC}\"
  - name: brokers-az3
    zone: \"\"
    replicas: ${BROKER_REPLICAS}
    storageSize: ${BROKER_STORAGE}
    storageClass: \"${DEFAULT_SC}\"
"
else
    # No zones — single pool
    BROKER_POOLS_YAML+="  - name: brokers
    zone: \"\"
    replicas: $((BROKER_REPLICAS * 3))
    storageSize: ${BROKER_STORAGE}
    storageClass: \"${DEFAULT_SC}\"
"
fi

# Disable zone-aware scheduling if no zones
ZONE_SCHEDULING="true"
TOPOLOGY_KEY="topology.kubernetes.io/zone"
if [ "${ZONE_COUNT}" -eq 0 ]; then
    ZONE_SCHEDULING="false"
    TOPOLOGY_KEY="kubernetes.io/hostname"
fi

VALUES_CONTENT="# Auto-generated by detect-cluster-config.sh
# Provider: ${PROVIDER}
# Context:  ${CONTEXT}
# Date:     $(date -u +%Y-%m-%dT%H:%M:%SZ)
# Profile:  ${PROFILE}
#
# Detected: ${ZONE_COUNT} zone(s), ${NODE_COUNT} node(s), ${TOTAL_CPU} CPU, ${TOTAL_MEM_GI} GiB RAM
# StorageClass: ${DEFAULT_SC}

controllers:
  replicas: ${CONTROLLER_REPLICAS}
  storage:
    size: ${CONTROLLER_STORAGE}
    class: \"${DEFAULT_SC}\"
  topologySpreadConstraints:
    enabled: ${ZONE_SCHEDULING}
    topologyKey: ${TOPOLOGY_KEY}
  podAntiAffinity:
    enabled: ${ZONE_SCHEDULING}
    topologyKey: ${TOPOLOGY_KEY}

brokerPools:
${BROKER_POOLS_YAML}
brokerDefaults:
  resources:
    requests:
      memory: ${BROKER_MEM_REQ}
      cpu: ${BROKER_CPU_REQ}
  topologySpreadConstraints:
    enabled: ${ZONE_SCHEDULING}
    topologyKey: ${TOPOLOGY_KEY}
  podAntiAffinity:
    enabled: ${ZONE_SCHEDULING}
    topologyKey: ${TOPOLOGY_KEY}

kafka:
  rack:
    topologyKey: ${TOPOLOGY_KEY}
"

if [ -n "${OUTPUT}" ]; then
    mkdir -p "$(dirname "${OUTPUT}")"
    echo "${VALUES_CONTENT}" > "${OUTPUT}"
    ok "Values written to: ${OUTPUT}"
    echo ""
    info "Deploy with:"
    echo "  helm dependency build charts/kafka-cluster"
    echo "  helm upgrade --install kafka-cluster charts/kafka-cluster \\"
    echo "    --namespace kafka --create-namespace \\"
    echo "    -f ${OUTPUT} \\"
    echo "    --timeout 10m --wait"
else
    echo ""
    echo "${VALUES_CONTENT}"
fi
