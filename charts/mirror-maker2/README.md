# mirror-maker2

Deploys a Strimzi **KafkaMirrorMaker2** cluster for cross-cluster replication —
active/passive DR, cluster migration, or aggregation. MM2 runs on Kafka Connect,
so the worker surface (replicas, JVM, resources, metrics, NetworkPolicy) matches
the `connect-cluster` chart. The difference: MM2 spans **two Kafka clusters**.

## Model (Strimzi v1 API)

- **`target`** — the cluster MM2's Connect runtime runs against; it owns the
  Connect internal config/offset/status topics. Defaults to the in-repo
  `krafter` cluster.
- **`mirrors[]`** — one entry per replication flow, each with its own
  **`source`** cluster (bootstrap + tls + auth) plus `topicsPattern`,
  `groupsPattern`, and the `sourceConnector` / `checkpointConnector` config
  (replication factors, offset sync).

Out of the box the chart is a **loopback** (source == target == `krafter`) so it
renders and smoke-tests without a second cluster. Replace `mirrors[].source`
with the real external cluster for a genuine mirror.

### Configuring source & target clusters

Both `target` and each `mirrors[].source` accept **either** an in-cluster
reference (`clusterName` + `namespace`, bootstrap FQDN computed —
`<clusterName>-kafka-bootstrap.<namespace>.svc.<clusterDomain>:<9092|9093>`)
**or** an explicit `bootstrapServers` (external / inter-cluster), which wins
when set. The port follows `tls.enabled` (9093) for computed addresses. This
covers every topology with one model:

| Topology | source | target |
|---|---|---|
| Same cluster (loopback / rename) | `clusterName: krafter, namespace: kafka` | `clusterName: krafter, namespace: kafka` |
| Same cluster, different namespaces | `namespace: kafka-a` | `namespace: kafka-b` |
| Two in-cluster Strimzi clusters | `clusterName: krafter-dr, namespace: kafka-dr` | `clusterName: krafter, namespace: kafka` |
| External / cross-datacenter | `bootstrapServers: kafka-east.example.com:9094` | `clusterName: krafter, namespace: kafka` |

## The credential contract (read this)

MM2 needs a `KafkaUser` on **both** ends:

| End | Who provisions it | ACLs |
|---|---|---|
| **target** | `kafka-cluster` chart (`kates-mm2`), or this chart with `kafkaUser.create=true` | write replicated topics, own MM2 internal + offset-sync + checkpoint topics, Connect groups, cluster Describe/Create |
| **source** | **you, out-of-band** — it's an external cluster this chart cannot touch | READ + DESCRIBE on the mirrored topics and consumer groups |

A missing **source** ACL does not error — the `MirrorSourceConnector` just
stalls and nothing replicates. Grant it before expecting data.

### Cross-namespace / inter-cluster checklist

MM2 reads each cluster's `passwordSecret` and TLS cert **from its own
namespace**. When a source or target Kafka cluster is in a different namespace
(or an external cluster), do all of:

1. **Credentials present here.** The credential Secret (and CA cert for TLS)
   must exist in the MM2 release namespace. Either enable `secretSync` to copy
   them from their home namespaces on every install/upgrade:

   ```yaml
   secretSync:
     enabled: true
     secrets:
       - { name: kates-mm2, fromNamespace: kafka }             # target creds
       - { name: kates-mm2-source, fromNamespace: kafka-dr }   # source creds
       - { name: krafter-cluster-ca-cert, fromNamespace: kafka }  # target CA (TLS)
   ```

   …or annotate the KafkaUser Secrets for kubernetes-reflector. For a truly
   external source, create its Secret in this namespace by hand.
2. **CA certs for TLS.** Set `tls.enabled: true` + `trustedCertificateSecret`
   per cluster and make sure that Secret is one of the synced/created ones.
3. **NetworkPolicy egress.** The default policy allows Kafka egress to any IP on
   `networkPolicy.kafka.ports`. For an external source on a non-standard port,
   add it there or via `networkPolicy.extraEgress`.
4. **ACLs on both ends** (see the table above) — target managed in-repo, source
   granted out-of-band.

## Durability

`sourceConnector.config.replication.factor` and the offset-syncs / checkpoints
topic factors must not exceed the target's broker count. The defaults are RF3
(production); the `values-kind.yaml` / `values-dev.yaml` overlays drop to RF1 for
single-broker clusters. RF1 is **not** durable — never use it for real DR.
`checkpointConnector.config.sync.group.offsets.enabled: true` translates consumer
offsets to the target so consumers can fail over.

## Common overrides

```yaml
mirrors:
  - source:
      alias: dc-east
      bootstrapServers: kafka-east.example.com:9093
      tls: { enabled: true, trustedCertificateSecret: dc-east-ca }
      authentication: { type: scram-sha-512, username: mm2-reader, secretName: mm2-east }
    topicsPattern: "orders\\..*|payments\\..*"
```

### mTLS (turnkey, no external PKI)

`-f values-mtls.yaml` runs MM2 over the `kafka-cluster` mTLS listener (9093)
using Strimzi's built-in Clients CA — it provisions a `type: tls` KafkaUser
(`kates-mm2-tls`, certs issued + auto-rotated by the operator), trusts the
Cluster CA via `krafter-cluster-ca-cert`, and narrows egress to 9093. No
cert-manager or external CA needed for the in-repo cluster.

```bash
helm install mm2 charts/mirror-maker2 -n kafka -f charts/mirror-maker2/values-mtls.yaml
```

Assumes MM2 is in the same namespace as the cluster (`kafka`); if elsewhere,
also enable `secretSync` for `kates-mm2-tls` + `krafter-cluster-ca-cert`. A real
**external** source needs its own `type: tls` KafkaUser issued by *that*
cluster's Clients CA, with its Cluster CA cert trusted here — for federated
trust across clusters, front Strimzi with cert-manager (bring-your-own-CA).

Production overlay: `-f values-prod.yaml` (HA, RF3, monitoring). The PodMonitor is
capability-guarded, so enabling metrics on a cluster without the Prometheus
Operator never fails the install — but `podMonitors.enabled` requires
`metrics.enabled=true` (Strimzi only opens the scrape port then), which the chart
enforces with a loud failure.

**`keepOnDelete` (default true)** annotates the MM2 CR and target KafkaUser with
`helm.sh/resource-policy: keep`, so `helm uninstall` leaves replication running.
The flip side: a later `helm install` with the same release name fails on the
kept objects — delete them manually first, or set `keepOnDelete: false`.

**Autoscaling:** `autoscaling.enabled=true` renders an HPA targeting the MM2 CR
(which exposes the scale subresource); `spec.replicas` is still seeded from
`minReplicas` because the v1 CRD requires it.
