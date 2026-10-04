# kafka-cluster — Installation Guide

Step-by-step installation, verification and day-2 operations. The chart reference is [README.md](README.md); upgrading from 0.4 is [docs/kafka-cluster-1.0-upgrade.md](../../docs/kafka-cluster-1.0-upgrade.md).

## Contents

1. [Prerequisites](#prerequisites)
2. [Option A: a local kind cluster](#option-a-a-local-kind-cluster)
3. [Option B: any Kubernetes cluster](#option-b-any-kubernetes-cluster)
4. [Verify](#verify)
5. [Connecting applications](#connecting-applications)
6. [Day-2 operations](#day-2-operations)
7. [Uninstalling](#uninstalling)

---

## Prerequisites

| Tool | Version |
|:---|:---|
| `kubectl` | 1.27+ |
| `helm` | 3.12+ |
| `docker`, `kind` | kind only |

| Environment | Controllers | Brokers | Nodes | Memory | CPU |
|:---|:---|:---|:---|:---|:---|
| kind (detected pools) | 3 | 3 | 1–4 | 8 GiB | 4 cores |
| `values-dev.yaml` | 1 | 1 | 1 | 4 GiB | 2 cores |
| `values-staging.yaml` | 3 | 3 | 3 | 24 GiB | 12 cores |
| `values-prod.yaml` | 3 | 9 | 3+ | 96 GiB | 36 cores |

**The Strimzi operator comes first**, from `charts/strimzi-operator` — it owns the Strimzi CRDs (and, if you want them, the drain cleaner and the Kafka dashboards):

```bash
helm dependency build charts/strimzi-operator
helm upgrade --install strimzi-operator charts/strimzi-operator \
  -n strimzi-operator --create-namespace --reset-values
kubectl wait --for=condition=Established crd/kafkas.kafka.strimzi.io --timeout=120s
```

**Chart dependencies.** kafka-cluster depends on the `kafka-common` library (in this repository) and the SeaweedFS chart:

```bash
helm repo add seaweedfs https://seaweedfs.github.io/seaweedfs/helm
helm dependency build charts/kafka-cluster   # `update` if a Chart.lock from 0.4 is in the way
```

---

## Option A: a local kind cluster

`kates deploy` does all of this, including `kates detect` for zone-pinned pools. By hand:

```bash
kind create cluster --name panda --config config/cluster.yaml
./scripts/load-images-to-kind.sh                     # optional: pre-load images
ENV=kind ./scripts/deploy-kafka.sh                   # operator + kafka-cluster
```

`ENV=kind scripts/deploy-kafka.sh` installs with `values-platform.yaml`, `values-dev.yaml` and `values-kind.yaml`: one controller and one broker (`controllers-dev`, `brokers-dev`), replication factor 1, the platform's topics and users, internal listeners only, and NetworkPolicies, Cruise Control, the exporter, alerts, PodMonitors, tiered storage and backup off.

The equivalent Helm command:

```bash
helm upgrade --install kafka-cluster charts/kafka-cluster -n kafka --create-namespace \
  -f charts/kafka-cluster/values-platform.yaml \
  -f charts/kafka-cluster/values-dev.yaml \
  -f charts/kafka-cluster/values-kind.yaml \
  --timeout 10m
kubectl wait kafka/krafter -n kafka --for=condition=Ready --timeout=600s
```

---

## Option B: any Kubernetes cluster

### 1. Choose the files

| Layer | File |
|:---|:---|
| environment | `values-staging.yaml`, `values-prod.yaml`, or your own |
| platform | `values-platform.yaml` when the cluster hosts kates (its topics, users and client grants) |
| yours | pools, zones, storage classes, listeners |

### 2. Describe the node pools

The defaults are one controller pool and one broker pool of three each, spread across zones and hosts. For zone-pinned pools on a cloud:

```yaml
controllerPools: []      # only needed when a lower layer carries 0.4-shaped pools
brokerPools: []

nodePools:
  defaults:
    storage:
      volumes:
        - { id: 0, size: 200Gi, class: gp3 }
  pools:
    - { name: controllers, roles: [controller] }
    - { name: brokers-az1, roles: [broker], zone: eu-west-1a }
    - { name: brokers-az2, roles: [broker], zone: eu-west-1b }
    - { name: brokers-az3, roles: [broker], zone: eu-west-1c }
```

Zones match the node label `nodePools.defaults.scheduling.zoneKey` (`topology.kubernetes.io/zone`, which every cloud provider sets): `kubectl get nodes -L topology.kubernetes.io/zone`. GCP zones look like `europe-west1-b`, Azure zones like `"1"`. A pool's name is its identity; pick names you will keep.

`kates detect --generate-values` writes pools for the cluster it inspects, in the 0.4 shape, which the chart translates.

### 3. Private registries

The Kafka image comes from the operator (`strimzi-kafka-operator.defaultImageRegistry` on the strimzi-operator chart) unless `kafka.image` is set. The Helm-test images are `testImages.kafka` and `testImages.kubectl`, and their pull secrets `global.imagePullSecrets`.

### 4. Install

```bash
helm upgrade --install kafka-cluster charts/kafka-cluster -n kafka --create-namespace \
  -f charts/kafka-cluster/values-prod.yaml \
  -f charts/kafka-cluster/values-platform.yaml \
  -f my-pools.yaml \
  --timeout 10m
kubectl wait kafka/krafter -n kafka --for=condition=Ready --timeout=900s
```

`values-prod.yaml` turns on `productionMode`, whose rails refuse a NodePort without TLS, `deleteClaim: true`, the placeholder SeaweedFS secret and a backup that protects no broker data. It expects a `kafka-seaweedfs-credentials` Secret (`seaweedfs.s3.existingSecret`), Velero with a `seaweedfs` BackupStorageLocation, and Kyverno.

NOTES print what was rendered, and a `DEPRECATED` list for any 0.4 key your values still use.

---

## Verify

```bash
helm test kafka-cluster -n kafka --timeout 5m     # 15m with tiered storage
```

| Test | Checks |
|:---|:---|
| profiler | pools, pod placement, PVCs, listeners (informational) |
| connectivity | Kafka CR Ready, broker pods, bootstrap DNS, every listener reachable |
| produce-consume | a SCRAM round trip as `<clusterName>-helm-test` |
| authorization | every KafkaUser Ready, SCRAM Secrets present |
| kraft-quorum | controller pools and pods, metadata state KRaft |
| topics | every KafkaTopic Ready, the first one's spec |
| listeners | bootstrap addresses, cluster CA |
| nodepools | broker pools ready, pod spread |
| cruise-control | pod running, CR configured (when enabled) |
| metrics | metrics ConfigMap, exporter, PodMonitors |
| performance | a 50 000-record producer benchmark |
| tiered-storage | segments leave local disk and are read back (when enabled) |

```bash
kubectl get kafka,kafkanodepools,kafkatopics,kafkausers,kafkarebalances -n kafka -l strimzi.io/cluster=krafter
kubectl get pods -n kafka -l strimzi.io/name=krafter-kafka -o wide
kubectl get kafka krafter -n kafka -o jsonpath='{range .status.listeners[*]}{.name}={.bootstrapServers}{"\n"}{end}'
```

---

## Connecting applications

**SCRAM, port 9092:**

```bash
PASSWORD=$(kubectl get secret kates-backend -n kafka -o jsonpath='{.data.password}' | base64 -d)
cat > client.properties <<EOF
bootstrap.servers=krafter-kafka-bootstrap.kafka.svc.cluster.local:9092
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="kates-backend" password="${PASSWORD}";
EOF
```

**mTLS, port 9093:** a `tls` user's Secret carries `user.crt`, `user.key` and `user.p12`; the cluster CA is in `krafter-cluster-ca-cert`.

**From another namespace:** add the application to `networkPolicy.clients`:

```yaml
networkPolicy:
  clients:
    - name: orders
      namespace: orders
      podSelector: { app.kubernetes.io/name: orders }
      listeners: [tls]
```

**From outside the cluster:** `kafka.externalAccess.type: nodeport` (or `loadbalancer`, `ingress`) adds a TLS listener on 9094; `allowedCidrs` restricts who may reach it.

---

## Day-2 operations

**Upgrade** — the same command as the install, with the same files. Every upgrade records a Velero pre-upgrade backup when `backup.enabled`.

**Scale** — raise a pool's `replicas` (or `nodePools.roleDefaults.broker.replicas`). Cruise Control's auto-rebalance moves partitions onto new brokers, and off removed ones before they go, using the chart's rebalance templates.

**Topics and users** — add them by name:

```yaml
topics:
  items:
    orders: { partitions: 12, config: { retention.ms: "604800000" } }
users:
  items:
    orders-app:
      authorization:
        type: simple
        acls:
          - resource: { type: topic, name: orders, patternType: literal }
            operations: [Read, Write, Describe]
```

**Rebalance on demand** — `rebalance.full.enabled: true` renders `krafter-full-rebalance`; approve its proposal with `kubectl annotate kafkarebalance krafter-full-rebalance -n kafka strimzi.io/rebalance=approve`.

**Certificates** — `kubectl get secret krafter-cluster-ca-cert -n kafka -o jsonpath='{.data.ca\.crt}' | base64 -d | openssl x509 -noout -dates`. The operator renews them `renewalDays` ahead, inside `maintenanceTimeWindows`, and strimzi-operator alerts before they expire.

**Kafka Connect** is the [connect-cluster](../connect-cluster/README.md) chart; MirrorMaker 2 is [mirror-maker2](../mirror-maker2/README.md).

---

## Uninstalling

```bash
helm uninstall kafka-cluster -n kafka
```

The Kafka CR and node pools always survive an uninstall, and topics and users do too unless `keepOnDelete: false`. To remove everything:

```bash
kubectl delete kafka krafter -n kafka
kubectl delete kafkanodepools,kafkatopics,kafkausers,kafkarebalances -n kafka -l strimzi.io/cluster=krafter
kubectl delete pvc -n kafka -l strimzi.io/cluster=krafter      # DESTROYS ALL DATA
```

The Strimzi CRDs belong to the strimzi-operator release; removing them removes every Strimzi resource in every namespace.
