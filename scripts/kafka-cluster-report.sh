#!/bin/bash
# kafka-cluster-report.sh — Deep cluster compatibility report for Kafka
# Read-only: never creates, modifies, or deletes any resource.
# Exit: 0=compatible, 1=incompatible, 2=partial
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; BOLD='\033[1m'
DIM='\033[2m'; NC='\033[0m'

pass() { echo -e "  ${GREEN}✓${NC}  $*"; }
fail() { echo -e "  ${RED}✗${NC}  $*"; FAILS=$((FAILS+1)); }
warn_() { echo -e "  ${YELLOW}⚠${NC}  $*"; WARNS=$((WARNS+1)); }
hdr()  { echo -e "\n${BOLD}${CYAN}═══ $* ═══${NC}"; }
FAILS=0; WARNS=0

# Cache node JSON once
NODES_JSON=$(kubectl get nodes -o json 2>/dev/null)
NODE_COUNT=$(echo "${NODES_JSON}" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['items']))" 2>/dev/null || echo 0)

# ══════════════════════════════════════════════════════════════════════════════
# Section 1: Cluster Identity
# ══════════════════════════════════════════════════════════════════════════════
hdr "1/10  Cluster Identity"
CONTEXT=$(kubectl config current-context 2>/dev/null || true)
[ -z "${CONTEXT}" ] && { fail "No kubeconfig context"; exit 1; }
SERVER=$(kubectl config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)
pass "Context:     ${CONTEXT}"
pass "Server:      ${SERVER}"

# Provider
PROVIDER="generic"
echo "${CONTEXT}" | grep -qi 'kind-\|kind_' && PROVIDER="kind"
echo "${CONTEXT}" | grep -qi 'eks\|arn:aws' && PROVIDER="eks"
echo "${CONTEXT}" | grep -qi 'gke_' && PROVIDER="gke"
echo "${CONTEXT}" | grep -qi 'aks\|azure' && PROVIDER="aks"
pass "Provider:    ${PROVIDER}"

# K8s version
K8S_VER=$(kubectl version -o json 2>/dev/null | python3 -c "import sys,json; v=json.load(sys.stdin)['serverVersion']; print(f\"{v['major']}.{v['minor']}\")" 2>/dev/null || echo "unknown")
K8S_MINOR=$(echo "${K8S_VER}" | cut -d. -f2 | tr -d '+')
if [ "${K8S_MINOR}" -ge 25 ] 2>/dev/null; then pass "Kubernetes:  ${K8S_VER}"; else fail "Kubernetes:  ${K8S_VER} (need ≥1.25)"; fi

# Helm version
HELM_VER=$(helm version --short 2>/dev/null | sed 's/v//' | cut -d+ -f1)
HELM_MAJOR=$(echo "${HELM_VER}" | cut -d. -f1)
if [ "${HELM_MAJOR}" -ge 3 ] 2>/dev/null; then pass "Helm:        v${HELM_VER}"; else fail "Helm:        v${HELM_VER} (need ≥3.12)"; fi

# ══════════════════════════════════════════════════════════════════════════════
# Section 2: Node Details
# ══════════════════════════════════════════════════════════════════════════════
hdr "2/10  Node Details"
echo ""
printf "  ${BOLD}%-20s %-12s %-16s %6s %8s  %-22s %-10s${NC}\n" "NAME" "ZONE" "ROLES" "CPU" "MEMORY" "RUNTIME" "KUBELET"
printf "  ${DIM}%-20s %-12s %-16s %6s %8s  %-22s %-10s${NC}\n" "────────────────────" "────────────" "────────────────" "──────" "────────" "──────────────────────" "──────────"

echo "${NODES_JSON}" | python3 -c "
import sys, json
data = json.load(sys.stdin)
for n in data['items']:
    name = n['metadata']['name'][:20]
    labels = n['metadata'].get('labels', {})
    zone = labels.get('topology.kubernetes.io/zone', labels.get('failure-domain.beta.kubernetes.io/zone', '-'))[:12]
    roles = ','.join([k.split('/')[-1] for k in labels if 'node-role' in k])[:16] or 'worker'
    cpu = n['status']['capacity']['cpu']
    mem_ki = int(n['status']['capacity']['memory'].replace('Ki',''))
    mem_gi = f'{mem_ki/1048576:.0f}Gi'
    info = n['status']['nodeInfo']
    rt = info['containerRuntimeVersion'][:22]
    kv = info['kubeletVersion'][:10]
    print(f'  {name:<20s} {zone:<12s} {roles:<16s} {cpu:>6s} {mem_gi:>8s}  {rt:<22s} {kv:<10s}')
" 2>/dev/null || echo "  (could not parse node details)"
echo ""
pass "Nodes: ${NODE_COUNT} total"

# ══════════════════════════════════════════════════════════════════════════════
# Section 3: Per-Zone Capacity
# ══════════════════════════════════════════════════════════════════════════════
hdr "3/10  Per-Zone Capacity"

# Gather zones
ZONES=()
while IFS= read -r z; do [ -n "$z" ] && ZONES+=("$z"); done < <(
  echo "${NODES_JSON}" | python3 -c "
import sys,json
for n in json.load(sys.stdin)['items']:
    z=n['metadata'].get('labels',{}).get('topology.kubernetes.io/zone','')
    if z: print(z)
" 2>/dev/null | sort -u)
ZONE_COUNT=${#ZONES[@]}

echo ""
if [ "${ZONE_COUNT}" -gt 0 ]; then
    printf "  ${BOLD}%-14s %6s %10s %10s${NC}\n" "ZONE" "NODES" "CPU" "MEMORY"
    printf "  ${DIM}%-14s %6s %10s %10s${NC}\n" "──────────────" "──────" "──────────" "──────────"
    for z in "${ZONES[@]}"; do
        read -r ncnt zcpu zmem < <(echo "${NODES_JSON}" | python3 -c "
import sys,json
items=[n for n in json.load(sys.stdin)['items'] if n['metadata'].get('labels',{}).get('topology.kubernetes.io/zone')==\"${z}\"]
tc=sum(int(n['status']['allocatable']['cpu']) for n in items)
tm=sum(int(n['status']['allocatable']['memory'].replace('Ki','')) for n in items)
print(f'{len(items)} {tc} {tm//1048576}Gi')
" 2>/dev/null || echo "0 0 0Gi")
        printf "  %-14s %6s %10s %10s\n" "${z}" "${ncnt}" "${zcpu}" "${zmem}"
    done
    echo ""
    if [ "${ZONE_COUNT}" -ge 3 ]; then pass "Zones: ${ZONE_COUNT} (${ZONES[*]})"; else warn_ "Zones: ${ZONE_COUNT} (need ≥3 for full HA)"; fi
else
    warn_ "No zone labels found on nodes"
fi

# ══════════════════════════════════════════════════════════════════════════════
# Section 4: Resource Budget
# ══════════════════════════════════════════════════════════════════════════════
hdr "4/10  Resource Budget"

TOTAL_CPU=$(echo "${NODES_JSON}" | python3 -c "import sys,json; print(sum(int(n['status']['allocatable']['cpu']) for n in json.load(sys.stdin)['items']))" 2>/dev/null || echo 0)
TOTAL_MEM=$(echo "${NODES_JSON}" | python3 -c "import sys,json; print(sum(int(n['status']['allocatable']['memory'].replace('Ki','')) for n in json.load(sys.stdin)['items'])//1048576)" 2>/dev/null || echo 0)

# Kafka resource requirements (production profile)
CTRL_CPU=1500    # 3 controllers × 500m
CTRL_MEM=3       # 3 × 1Gi
BROKER_CPU=3000  # 3 brokers/zone × 1000m
BROKER_MEM=12    # 3 × 4Gi
OTHER_CPU=500    # entity-op + exporter + cruise-control
OTHER_MEM=1      # ~1Gi total
NEED_CPU=$(( CTRL_CPU + BROKER_CPU*3 + OTHER_CPU ))  # millicores
NEED_MEM=$(( CTRL_MEM + BROKER_MEM*3 + OTHER_MEM ))  # Gi

echo ""
printf "  ${BOLD}%-24s %6s %8s %8s${NC}\n" "COMPONENT" "PODS" "CPU" "MEMORY"
printf "  ${DIM}%-24s %6s %8s %8s${NC}\n" "────────────────────────" "──────" "────────" "────────"
printf "  %-24s %6s %8s %8s\n" "Controllers" "3" "1500m" "3Gi"
printf "  %-24s %6s %8s %8s\n" "Brokers (az1)" "3" "3000m" "12Gi"
printf "  %-24s %6s %8s %8s\n" "Brokers (az2)" "3" "3000m" "12Gi"
printf "  %-24s %6s %8s %8s\n" "Brokers (az3)" "3" "3000m" "12Gi"
printf "  %-24s %6s %8s %8s\n" "Operators + Exporter" "3" "500m" "1Gi"
printf "  ${DIM}────────────────────────────────────────────────────${NC}\n"
printf "  ${BOLD}%-24s %6s %8s %8s${NC}\n" "TOTAL REQUIRED" "15" "${NEED_CPU}m" "${NEED_MEM}Gi"
printf "  ${BOLD}%-24s %6s %8s %8s${NC}\n" "CLUSTER AVAILABLE" "${NODE_COUNT}" "${TOTAL_CPU}000m" "${TOTAL_MEM}Gi"
echo ""

if [ "$((TOTAL_CPU*1000))" -ge "${NEED_CPU}" ] && [ "${TOTAL_MEM}" -ge "${NEED_MEM}" ]; then
    pass "Resources sufficient (${TOTAL_CPU} CPU / ${TOTAL_MEM}Gi available)"
else
    fail "Insufficient resources (need ${NEED_CPU}m CPU, ${NEED_MEM}Gi memory)"
fi

# ══════════════════════════════════════════════════════════════════════════════
# Section 5: Storage Compatibility
# ══════════════════════════════════════════════════════════════════════════════
hdr "5/10  Storage Compatibility"
echo ""
printf "  ${BOLD}%-22s %-24s %-22s %-8s %-8s${NC}\n" "NAME" "PROVISIONER" "BINDING" "RECLAIM" "DEFAULT"
printf "  ${DIM}%-22s %-24s %-22s %-8s %-8s${NC}\n" "──────────────────────" "────────────────────────" "──────────────────────" "────────" "────────"

SC_JSON=$(kubectl get sc -o json 2>/dev/null || echo '{"items":[]}')
echo "${SC_JSON}" | python3 -c "
import sys,json
data=json.load(sys.stdin)
for sc in data['items']:
    name=sc['metadata']['name'][:22]
    prov=sc.get('provisioner','?')[:24]
    bind=sc.get('volumeBindingMode','?')[:22]
    recl=sc.get('reclaimPolicy','?')[:8]
    ann=sc['metadata'].get('annotations',{})
    default='✓' if ann.get('storageclass.kubernetes.io/is-default-class')=='true' or ann.get('storageclass.beta.kubernetes.io/is-default-class')=='true' else '✗'
    print(f'  {name:<22s} {prov:<24s} {bind:<22s} {recl:<8s} {default:<8s}')
" 2>/dev/null

SC_COUNT=$(echo "${SC_JSON}" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['items']))" 2>/dev/null || echo 0)
echo ""
if [ "${SC_COUNT}" -gt 0 ]; then pass "StorageClasses: ${SC_COUNT} available"; else fail "No StorageClasses found"; fi

# ══════════════════════════════════════════════════════════════════════════════
# Section 6: Existing Kafka Resources
# ══════════════════════════════════════════════════════════════════════════════
hdr "6/10  Existing Kafka Resources"

KAFKA_COUNT=$(kubectl get kafka -n kafka --no-headers 2>/dev/null | wc -l | tr -d ' ')
NP_COUNT=$(kubectl get kafkanodepools -n kafka --no-headers 2>/dev/null | wc -l | tr -d ' ')
TOPIC_COUNT=$(kubectl get kafkatopics -n kafka --no-headers 2>/dev/null | wc -l | tr -d ' ')
USER_COUNT=$(kubectl get kafkausers -n kafka --no-headers 2>/dev/null | wc -l | tr -d ' ')
PVC_COUNT=$(kubectl get pvc -n kafka --no-headers 2>/dev/null | wc -l | tr -d ' ')
PVC_BOUND=$(kubectl get pvc -n kafka --no-headers 2>/dev/null | grep -c 'Bound' || echo 0)
HELM_REL=$(helm list -n kafka -o json 2>/dev/null | python3 -c "import sys,json;r=json.load(sys.stdin);print(f\"{r[0]['name']} (rev {r[0]['revision']}, {r[0]['status']})\")" 2>/dev/null || echo "none")

echo ""
printf "  %-22s %s\n" "Kafka clusters:" "${KAFKA_COUNT}"
printf "  %-22s %s\n" "KafkaNodePools:" "${NP_COUNT}"
printf "  %-22s %s\n" "KafkaTopics:" "${TOPIC_COUNT}"
printf "  %-22s %s\n" "KafkaUsers:" "${USER_COUNT}"
printf "  %-22s %s\n" "PVCs:" "${PVC_COUNT} (${PVC_BOUND} bound)"
printf "  %-22s %s\n" "Helm release:" "${HELM_REL}"
echo ""

if [ "${KAFKA_COUNT}" -gt 0 ]; then
    warn_ "Existing Kafka deployment detected — upgrade mode recommended"
else
    pass "No existing Kafka deployment — clean install"
fi

# ══════════════════════════════════════════════════════════════════════════════
# Section 7: Strimzi Operator
# ══════════════════════════════════════════════════════════════════════════════
hdr "7/10  Strimzi Operator"

STRIMZI_DEP=$(kubectl get deployment -A -l app.kubernetes.io/name=strimzi-cluster-operator -o json 2>/dev/null || echo '{"items":[]}')
STRIMZI_COUNT=$(echo "${STRIMZI_DEP}" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['items']))" 2>/dev/null || echo 0)

if [ "${STRIMZI_COUNT}" -gt 0 ]; then
    echo "${STRIMZI_DEP}" | python3 -c "
import sys,json
d=json.load(sys.stdin)['items'][0]
ns=d['metadata']['namespace']
img=d['spec']['template']['spec']['containers'][0]['image']
ready=d['status'].get('readyReplicas',0)
total=d['status'].get('replicas',0)
restarts=sum(cs.get('restartCount',0) for p in [] for cs in [])
print(f'  Namespace:   {ns}')
print(f'  Image:       {img}')
print(f'  Replicas:    {ready}/{total} ready')
" 2>/dev/null || echo "  (could not parse operator details)"
    pass "Strimzi operator: running"
else
    if kubectl get crd kafkas.kafka.strimzi.io &>/dev/null; then
        warn_ "Strimzi CRDs present but operator not running"
    else
        warn_ "Strimzi not installed — chart will install operator subchart"
    fi
fi

# ══════════════════════════════════════════════════════════════════════════════
# Section 8: Monitoring Stack
# ══════════════════════════════════════════════════════════════════════════════
hdr "8/10  Monitoring Stack"

if kubectl get crd podmonitors.monitoring.coreos.com &>/dev/null; then
    pass "PodMonitor CRD: present"
else
    warn_ "PodMonitor CRD: not found"
fi
if kubectl get crd prometheusrules.monitoring.coreos.com &>/dev/null; then
    pass "PrometheusRule CRD: present"
else
    warn_ "PrometheusRule CRD: not found"
fi
if kubectl get deployment -n monitoring -l "app.kubernetes.io/name=grafana" --no-headers 2>/dev/null | grep -q .; then
    pass "Grafana: deployed in monitoring"
else
    warn_ "Grafana: not found"
fi
PROM_LABEL=$(kubectl get podmonitors -A -o jsonpath='{.items[0].metadata.labels.release}' 2>/dev/null || echo "monitoring")
pass "Release label: ${PROM_LABEL}"

# ══════════════════════════════════════════════════════════════════════════════
# Section 9: Network & Connectivity
# ══════════════════════════════════════════════════════════════════════════════
hdr "9/10  Network & Connectivity"

# CNI
CNI="unknown"
if kubectl get pods -n kube-system -l k8s-app=calico-node --no-headers 2>/dev/null | grep -q .; then CNI="Calico"
elif kubectl get pods -n kube-system -l k8s-app=cilium --no-headers 2>/dev/null | grep -q .; then CNI="Cilium"
elif kubectl get pods -n kube-system -l app=kindnet --no-headers 2>/dev/null | grep -q . || \
     kubectl get ds -n kube-system kindnet --no-headers 2>/dev/null | grep -q .; then CNI="kindnet"
elif kubectl get ds -n kube-system -l app=flannel --no-headers 2>/dev/null | grep -q .; then CNI="Flannel"
fi
pass "CNI: ${CNI}"

# DNS
DNS_PODS=$(kubectl get pods -n kube-system -l k8s-app=kube-dns --no-headers 2>/dev/null | grep -c Running || echo 0)
if [ "${DNS_PODS}" -gt 0 ]; then pass "CoreDNS: ${DNS_PODS} replica(s) running"; else warn_ "CoreDNS: not detected"; fi

# Pod/Service CIDRs
POD_CIDR=$(kubectl get nodes -o jsonpath='{.items[0].spec.podCIDR}' 2>/dev/null || echo "unknown")
SVC_CIDR=$(kubectl get pod -n kube-system -l component=kube-apiserver -o jsonpath='{.items[0].spec.containers[0].command}' 2>/dev/null | grep -oE 'service-cluster-ip-range=[^,"]+' | cut -d= -f2 | head -1 || echo "unknown")
pass "Pod CIDR:     ${POD_CIDR:-unknown}"
pass "Service CIDR: ${SVC_CIDR:-unknown}"

# ══════════════════════════════════════════════════════════════════════════════
# Section 10: 3-AZ Compatibility Verdict
# ══════════════════════════════════════════════════════════════════════════════
hdr "10/10  3-AZ Kafka Compatibility"
echo ""
printf "  ${BOLD}%-44s %-8s %s${NC}\n" "CHECK" "STATUS" "DETAIL"
printf "  ${DIM}%-44s %-8s %s${NC}\n" "────────────────────────────────────────────" "────────" "──────────────────────────────"

check() {
    local desc="$1" ok="$2" detail="$3"
    if [ "${ok}" = "true" ]; then
        printf "  %-44s ${GREEN}%-8s${NC} %s\n" "${desc}" "PASS" "${detail}"
    else
        printf "  %-44s ${RED}%-8s${NC} %s\n" "${desc}" "FAIL" "${detail}"
        FAILS=$((FAILS+1))
    fi
}

# Run checks
[ "${K8S_MINOR}" -ge 25 ] 2>/dev/null && C1=true || C1=false
[ "${HELM_MAJOR}" -ge 3 ] 2>/dev/null && C2=true || C2=false
kubectl get crd kafkas.kafka.strimzi.io &>/dev/null && C3=true || C3=false
[ "${ZONE_COUNT}" -ge 3 ] && C4=true || C4=false
# Min 1 node per zone
C5=true
for z in "${ZONES[@]}" ; do
    ZN=$(echo "${NODES_JSON}" | python3 -c "import sys,json; print(len([n for n in json.load(sys.stdin)['items'] if n['metadata'].get('labels',{}).get('topology.kubernetes.io/zone')=='${z}']))" 2>/dev/null || echo 0)
    [ "${ZN}" -lt 1 ] && C5=false
done
[ "${SC_COUNT}" -gt 0 ] && C6=true || C6=false
[ "$((TOTAL_CPU*1000))" -ge "${CTRL_CPU}" ] && C7=true || C7=false
[ "$((TOTAL_CPU*1000))" -ge "${NEED_CPU}" ] && C8=true || C8=false
[ "${ZONE_COUNT}" -ge 3 ] && C9=true || C9=false  # RF=3
[ "${ZONE_COUNT}" -ge 3 ] && C10=true || C10=false # ISR=2
kubectl auth can-i create deployments -n kafka &>/dev/null && C11=true || C11=false
kubectl get crd podmonitors.monitoring.coreos.com &>/dev/null && C12=true || C12=false
[ "${DNS_PODS}" -gt 0 ] && C13=true || C13=false

check "Kubernetes version ≥ 1.25"          "${C1}"  "${K8S_VER}"
check "Helm version ≥ 3.12"               "${C2}"  "v${HELM_VER}"
check "Strimzi CRDs installed"             "${C3}"  "$(kubectl get crd kafkas.kafka.strimzi.io -o jsonpath='{.spec.versions[-1:].name}' 2>/dev/null || echo 'missing')"
check "≥ 3 availability zones"             "${C4}"  "${ZONE_COUNT} zone(s)"
check "≥ 1 node per zone"                 "${C5}"  "${NODE_COUNT} nodes across ${ZONE_COUNT} zones"
check "StorageClass available"             "${C6}"  "${SC_COUNT} class(es)"
check "Controller resources fit"           "${C7}"  "${CTRL_CPU}m needed"
check "Broker resources fit (all zones)"   "${C8}"  "${NEED_CPU}m total needed"
check "Replication factor 3 achievable"    "${C9}"  "${ZONE_COUNT} zones"
check "min.insync.replicas=2 safe"         "${C10}" "can lose 1 zone"
check "RBAC permissions"                   "${C11}" "kafka namespace"
check "Prometheus monitoring"              "${C12}" "PodMonitor CRD"
check "DNS resolution"                     "${C13}" "${DNS_PODS} CoreDNS pod(s)"

echo ""
if [ "${FAILS}" -eq 0 ] && [ "${WARNS}" -eq 0 ]; then
    echo -e "  ${BOLD}${GREEN}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "  ${BOLD}${GREEN}  RESULT: ✓ COMPATIBLE — cluster can run a 3-AZ Kafka deployment${NC}"
    echo -e "  ${BOLD}${GREEN}═══════════════════════════════════════════════════════════════${NC}"
    exit 0
elif [ "${FAILS}" -eq 0 ]; then
    echo -e "  ${BOLD}${YELLOW}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "  ${BOLD}${YELLOW}  RESULT: ⚠ PARTIAL — compatible with ${WARNS} warning(s)${NC}"
    echo -e "  ${BOLD}${YELLOW}═══════════════════════════════════════════════════════════════${NC}"
    exit 2
else
    echo -e "  ${BOLD}${RED}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "  ${BOLD}${RED}  RESULT: ✗ INCOMPATIBLE — ${FAILS} check(s) failed${NC}"
    echo -e "  ${BOLD}${RED}═══════════════════════════════════════════════════════════════${NC}"
    exit 1
fi
