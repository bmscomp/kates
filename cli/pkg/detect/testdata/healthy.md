# Kafka Cluster Compatibility Report

**Date:** 2026-01-02T03:04:05Z  
**Context:** prod-eu | **Provider:** eks | **Kubernetes:** v1.31.2 | **Helm:** v3.15.4

> ✅ **COMPATIBLE**: The cluster can run a 3-AZ Kafka deployment.

## Cluster Identity

- **Context:** prod-eu
- **Server:** https://api.prod-eu.example:6443
- **Provider:** eks (server version v1.31.2-eks-1234)
- **Kubernetes:** v1.31.2
- **Helm:** v3.15.4

## ✅ Nodes

3 node(s).

| Name | Zone | Roles | CPU | Memory | Runtime | Kubelet |
|---|---|---|---|---|---|---|
| ip-10-0-1-10 | eu-west-1a | worker | 16000m | 64Gi | containerd://1.7.22 | v1.31.2-eks-1234 |
| ip-10-0-2-10 | eu-west-1b | worker | 16000m | 64Gi | containerd://1.7.22 | v1.31.2-eks-1234 |
| ip-10-0-3-10 | eu-west-1c | worker | 16000m | 64Gi | containerd://1.7.22 | v1.31.2-eks-1234 |

## ✅ Zones

3 zone(s); a 3-AZ deployment needs at least 3.

| Zone | Nodes | CPU | Memory |
|---|---|---|---|
| eu-west-1a | 1 | 16000m | 64Gi |
| eu-west-1b | 1 | 16000m | 64Gi |
| eu-west-1c | 1 | 16000m | 64Gi |

## ✅ Resource Budget

- **Resources:** ✅ sufficient
- **Recommended profile:** ✅ PRODUCTION
- **Profile:** Highly available, high-throughput production workloads across at least 3 zones.
- **CPU:** 23% (need 11000m of 48000m)
- **Memory:** 21% (need 40Gi of 192Gi)

| Component | CPU | Memory |
|---|---|---|
| Controllers | 1500m | 3Gi |
| Brokers (3) | 9000m | 36Gi |
| Operators + exporter | 500m | 1Gi |
| Total required | 11000m | 40Gi |
| Cluster allocatable | 48000m | 192Gi |

## ✅ Storage

- **PersistentVolumes:** 6 total (6 bound, 600Gi capacity)
- **CSI drivers:** ebs.csi.aws.com

| Name | Provisioner | Binding | Reclaim | Default | Expand | IOPS | Latency |
|---|---|---|---|---|---|---|---|
| gp3 | ebs.csi.aws.com | WaitForFirstConsumer | Delete | yes | yes | 3000 (measured) | 0.42ms |
| io2 | ebs.csi.aws.com | WaitForFirstConsumer | Retain | no | yes | — | — |

## ✅ Strimzi Operator

- **Status:** ✅ running in strimzi-operator
- **Image:** quay.io/strimzi/operator:0.45.0
- **Replicas:** 1/1 ready
- **Health:** ✅ Healthy
- **Operator pods:** ✅ ready
- **Kafka capacity:** ✅ Sufficient

## ✅ Existing Kafka

No existing Kafka deployment: clean install.

- **Kafka clusters:** 0
- **KafkaNodePools:** 0
- **KafkaTopics:** 0
- **KafkaUsers:** 0
- **PVCs:** 0 (0 bound)
- **Helm release:** none

## ✅ Kafka Connect and CDC

- **Kafka Connect:** ✅ connect in kafka
- **Image:** quay.io/strimzi/kafka:0.45.0-kafka-3.9.0
- **Workers:** ✅ 2/2 ready
- **JVM heap:** ✅ -Xms1g -Xmx1g (25% of the 4096Mi limit)
- **TLS:** ✅ enabled
- **PodDisruptionBudget:** ✅ configured
- **Plugins:** 0
- **Tracing:** no
- **Rack awareness:** no
- **Strimzi build:** no
- **Schema Registry:** ✅ available (apicurio in registry)
- **CDC database:** ✅ accessible (pg in db, port 5432)

| Connector | Class | Tasks max | Status |
|---|---|---|---|
| pg-source | io.debezium.connector.postgresql.PostgresConnector | 1 | ✅ Running |

## ✅ Monitoring

- **PodMonitor CRD:** ✅ present
- **PrometheusRule CRD:** ✅ present
- **Grafana:** ✅ present
- **Release label:** kube-prometheus

## ✅ Network and DNS

- **CNI:** cilium
- **CoreDNS:** ✅ 2 replica(s) running
- **Cluster domain:** cluster.local
- **Pod CIDR:** 10.0.0.0/16
- **Service CIDR:** 172.20.0.0/16

| Query | Queries | Succeeded | Success rate | Avg latency | Max latency |
|---|---|---|---|---|---|
| Internal | 20 | 20 | ✅ 100.0% | 1.50ms | 3.00ms |

## ✅ Inter-Zone Latency

| Source \\ Target | eu-west-1a | eu-west-1b | eu-west-1c |
|---|---|---|---|
| **eu-west-1a** | 0.05ms | ✅ 0.90ms | ✅ 0.90ms |
| **eu-west-1b** | ✅ 0.90ms | 0.05ms | ✅ 0.90ms |
| **eu-west-1c** | ✅ 0.90ms | ✅ 0.90ms | 0.05ms |

*Average round trip of 5 pings; cross-zone above 5ms is a warning, above 15ms fails.*

## ✅ Inter-Zone Bandwidth

| Source \\ Target | eu-west-1a | eu-west-1b | eu-west-1c |
|---|---|---|---|
| **eu-west-1a** | — | 4800.0 Mbps | 4800.0 Mbps |
| **eu-west-1b** | 4800.0 Mbps | — | 4800.0 Mbps |
| **eu-west-1c** | 4800.0 Mbps | 4800.0 Mbps | — |

## ✅ Security

- **Pod Security admission:** baseline
- **Kyverno enforcing:** no
- **Namespace permissions:** ✅ sufficient
- **Wildcard RBAC:** ✅ no wildcard rules in the kafka namespace Roles
- **TLS certificates:** ✅ none expire within 30 days
- **Secret creation:** ✅ works (verified in a temporary namespace)

## Admission Controllers

- **Kyverno:** ✅ running in kyverno (v1.12.0)
- **Cluster policies:** 2 total, 1 kafka-relevant
- **OPA Gatekeeper:** not installed

| Policy | Action | Category | Rules |
|---|---|---|---|
| disallow-latest-tag | ENFORCE | Best Practices | 1 |

*Latest tag blocked: the chart pins image versions.*

## ✅ NetworkPolicies (kafka namespace)

- **Managed by Helm:** 1
- **Managed by Strimzi:** 0
- **Managed by hand:** ✅ 0
- **Default deny:** yes

| Name | Selector | Types | In/Out | Managed by |
|---|---|---|---|---|
| kafka-default-deny | {} | Ingress,Egress | 0/0 | kates |

## ✅ Compatibility Checks

| Check | Status | Detail |
|---|---|---|
| Kubernetes version ≥ 1.25 | ✅ PASS | v1.31.2 |
| ≥ 3 availability zones | ✅ PASS | 3 zone(s) |
| StorageClass available | ✅ PASS | 2 class(es) |
| AZ network latency (cross-zone) | ✅ PASS | optimal: max 0.90ms (\<5ms) |

## ✅ Remediation

Nothing to remediate.

---
*Generated by kates detect*
