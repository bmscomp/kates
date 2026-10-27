# Kafka Cluster Compatibility Report

**Date:** 2026-01-02T03:04:05Z  
**Context:** kind-\<script\>alert("ctx")\</script\> | **Provider:** unknown | **Kubernetes:** v1.24.0 | **Helm:** v3.11.0

> ❌ **INCOMPATIBLE**: 3 check(s) failed and 2 warning(s). Resolve the failures before deploying.

## Cluster Identity

- **Context:** kind-\<script\>alert("ctx")\</script\>
- **Server:** https://127.0.0.1:6443
- **Provider:** unknown
- **Kubernetes:** v1.24.0
- **Helm:** v3.11.0

## ✅ Nodes

1 node(s).

| Name | Zone | Roles | CPU | Memory | Runtime | Kubelet |
|---|---|---|---|---|---|---|
| \<script\>alert(1)\</script\> | zone-a | control-plane | 2000m | 4Gi |  |  |

## ❌ Zones

1 zone(s); a 3-AZ deployment needs at least 3.

| Zone | Nodes | CPU | Memory |
|---|---|---|---|
| zone-a | 1 | 2000m | 4Gi |

## ❌ Resource Budget

- **Resources:** ❌ insufficient (need 11000m CPU, 40Gi memory)
- **Recommended profile:** ❌ INSUFFICIENT
- **Profile:** Not enough capacity for any profile: scale the cluster to at least 3 CPU cores and 8Gi of memory.
- **CPU:** 100% (need 11000m of 2000m)
- **Memory:** 100% (need 40Gi of 4Gi)

| Component | CPU | Memory |
|---|---|---|
| Controllers | 1500m | 3Gi |
| Brokers (3) | 9000m | 36Gi |
| Operators + exporter | 500m | 1Gi |
| Total required | 11000m | 40Gi |
| Cluster allocatable | 2000m | 4Gi |

## ❌ Storage

No StorageClass found: Kafka PVCs will not bind.

## ⚠️ Strimzi Operator

- **Status:** ✅ running in kafka
- **Image:** quay.io/strimzi/operator:0.38.0
- **Replicas:** 0/1 ready
- **Health:** ⚠️ Unhealthy
- **Operator pods:** ❌ not ready
- **Missing CRDs:** ⚠️ kafkarebalances.kafka.strimzi.io

```
WARN Reconciliation #12 failed: <timeout> & retry
```

*Warning and error lines from the operator log in the last 15 minutes.*

## ⚠️ Existing Kafka

An existing Kafka deployment was found: upgrade mode is recommended.

- **Kafka clusters:** 1
- **KafkaNodePools:** 2
- **KafkaTopics:** 0
- **KafkaUsers:** 0
- **PVCs:** 3 (2 bound)
- **Helm release:** krafter
- **Cluster:** krafter (Kafka 3.9.0)
- **Replicas:** ⚠️ 1/3 ready
- **Ready condition:** ❌ False NotReady

| Listener | Type | Port | TLS |
|---|---|---|---|
| plain | internal | 9092 | no |

## Kafka Connect and CDC

- **Kafka Connect:** not installed
- **Schema Registry:** not installed
- **CDC database:** not detected

## ⚠️ Monitoring

- **PodMonitor CRD:** ⚠️ not found
- **PrometheusRule CRD:** ⚠️ not found
- **Grafana:** ⚠️ not found
- **Release label:** none

## ❌ Network and DNS

- **CNI:** kindnet
- **CoreDNS:** ❌ not detected
- **Cluster domain:** cluster.local
- **Pod CIDR:** unknown
- **Service CIDR:** unknown

*DNS probe skipped: run with --bench-dns to measure CoreDNS latency.*

## ⏭️ Inter-Zone Latency

Skipped: the inter-zone latency probe did not run (read-only run, or no zones to probe).

## ⏭️ Inter-Zone Bandwidth

Skipped: run with --bench-network to measure throughput between zones.

## ❌ Security

- **Pod Security admission:** none
- **Kyverno enforcing:** no
- **Namespace permissions:** ❌ missing
- **Wildcard RBAC:** ⚠️ wildcard rules found in the kafka namespace Roles
- **TLS certificates:** ⚠️ 1 expire within 30 days
- **Secret creation:** ❌ blocked by admission policy restrict-secrets

| Secret | Subject | Expires | Days left |
|---|---|---|---|
| krafter-cluster-ca-cert | CN=cluster-ca | 2026-01-20 | ⚠️ 18 |

## Admission Controllers

- **Kyverno:** not installed
- **OPA Gatekeeper:** ✅ running in gatekeeper-system (1 constraints)

## ⚠️ NetworkPolicies (kafka namespace)

- **Managed by Helm:** 0
- **Managed by Strimzi:** 0
- **Managed by hand:** ⚠️ 1
- **Default deny:** no

| Name | Selector | Types | In/Out | Managed by |
|---|---|---|---|---|
| legacy\|allow | app=kafka | Ingress | 1/0 | manual |

## ❌ Compatibility Checks

| Check | Status | Detail |
|---|---|---|
| Kubernetes version ≥ 1.25 | ❌ FAIL | v1.24.0 |
| ≥ 3 availability zones | ❌ FAIL | 1 zone(s) |
| StorageClass available | ❌ FAIL | 0 class(es) |
| node \<b\>bold\</b\> | ✅ PASS | a & b \<i\> |
| Existing Kafka clusters | ⚠️ WARN | 1 Kafka CR(s) already on the cluster |

## ❌ Remediation

### ❌ Only 1 zone(s) found — 3-AZ Kafka requires at least 3 zones

Check: ≥ 3 availability zones

```bash
kubectl get nodes --show-labels | grep topology.kubernetes.io/zone
kubectl label node <node-name> topology.kubernetes.io/zone=<zone>  # if labels are missing
```

### ❌ No StorageClass found — Kafka PVCs will fail to bind

Check: StorageClass available

```bash
kubectl apply -f config/storage/  # apply zone-specific storage classes
kubectl get sc  # verify
```

### ❌ Cluster does not have enough CPU/memory for the requested Kafka deployment

Check: Broker resources fit (all zones)

```bash
# Short by 9000m CPU, 36Gi memory
# Option 1: Add nodes to the cluster
# Option 2: Reduce broker resources in values.yaml:
#   brokerDefaults.resources.requests.cpu: 500m
#   brokerDefaults.resources.requests.memory: 1Gi
```

### ❌ CoreDNS not detected — Kafka brokers require DNS for discovery

Check: DNS resolution

```bash
kubectl get deployment -n kube-system | grep -i dns
kubectl rollout restart deployment/coredns -n kube-system
```

### ⚠️ PodMonitor CRD not found — Kafka metrics will not be scraped

Check: Prometheus monitoring

```bash
helm install monitoring prometheus-community/kube-prometheus-stack -n monitoring --create-namespace
```

[Documentation](https://prometheus-operator.dev/docs/getting-started/introduction/)

### ⚠️ An existing Kafka cluster is running — use helm upgrade instead of install

Check: Existing deployment

```bash
helm upgrade kafka-cluster charts/kafka-cluster -n kafka -f values.yaml --timeout 10m --wait
```

### ⚠️ Namespace Roles contain wildcard '\*' resources or verbs which violates least privilege security standards

Check: Wildcard RBAC roles avoided

```bash
# Inspect Roles with wildcard permissions in the namespace:
kubectl get roles -n kafka -o json | jq '.items[] | select(.rules[].verbs[] == "*" or .rules[].resources[] == "*") | .metadata.name'
# Scope down rules to explicit apiGroups, resources, and verbs in values.yaml
```

### ⚠️ 1 TLS certificate(s) in namespace 'kafka' are expiring within 30 days

Check: TLS certificate validity

```bash
# Cert 'krafter-cluster-ca-cert' (CN=cluster-ca) expires in 18 days
# Renew expiring TLS secrets or trigger certificate rotation:
kubectl delete secret <secret-name> -n kafka  # if managed by cert-manager to force renewal
```

---
*Generated by kates detect*
