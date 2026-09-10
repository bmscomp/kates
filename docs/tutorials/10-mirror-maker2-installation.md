# Tutorial 10: Installing and Setting Up MirrorMaker 2

This tutorial takes you from a running Kates cluster to a verified MirrorMaker 2
deployment — installed, connected, and proven to be moving actual records. It
uses a **loopback** mirror (the `krafter` cluster replicating to itself under a
different topic name), which needs no second cluster and exercises every part of
the path.

By the end you will have replicated a real record and, more usefully, you will
know the three different questions to ask about a mirror and why only one of
them has a meaningful answer.

> For a cross-version migration from a real Kafka 2.x or 3.x cluster, do this
> tutorial first, then [Migrating Kafka 2.x to 4.x](11-migrating-kafka-2x-to-4x.md)
> or [Migrating Kafka 3.x to 4.x](12-migrating-kafka-3x-to-4x.md).

**Level:** Intermediate · **Duration:** 30 min · **Prerequisites:** Tutorial 1

## What You Will Build

```text
        ┌──────────────────────── krafter (Kafka 4.3.0) ────────────────────────┐
        │                                                                       │
        │   kates.demo  ──────►  MirrorMaker 2  ──────►  source.kates.demo      │
        │   (you produce here)   (Connect workers)       (records arrive here)  │
        └───────────────────────────────────────────────────────────────────────┘
```

Source and target are the same cluster. That is not a limitation of the chart —
it is the cheapest way to exercise the full path (connectors, internal topics,
offset syncs, ACLs) without a second Kafka.

## Prerequisites

```bash
export KAFKA_NS=kafka
export KAFKA_CLUSTER=krafter
export MM2_RELEASE=mm2
```

The Strimzi operator, the `krafter` cluster, and the `kates-mm2` KafkaUser must
exist:

```bash
kubectl get crd kafkamirrormaker2s.kafka.strimzi.io
kubectl -n "${KAFKA_NS}" get kafka "${KAFKA_CLUSTER}"
kubectl -n "${KAFKA_NS}" get secret kates-mm2
```

If any of these is missing:

```bash
make cluster && make deploy-strimzi && make deploy-kafka
```

## Step 1: Understand What You Are Installing

MirrorMaker 2 is Kafka Connect with three built-in connectors. A
`KafkaMirrorMaker2` custom resource is therefore a Connect cluster, and the chart
splits into two halves:

| Half | Values | What it controls |
|---|---|---|
| Worker | `replicas`, `jvmOptions`, `resources`, `metrics`, `networkPolicy` | The Connect runtime — same surface as `charts/connect-cluster` |
| Mirror | `target`, `mirrors[]`, `replicationPolicy`, `compatibility` | The two-cluster part, which is everything that is different |

Read the rendered CR before installing anything:

```bash
helm template mm2 charts/mirror-maker2 -n "${KAFKA_NS}" \
  -f charts/mirror-maker2/values-kind.yaml \
  | grep -A 40 'kind: KafkaMirrorMaker2'
```

The two fields to look at:

- `spec.target` — where the Connect runtime lives and which cluster owns the
  internal `configs` / `offsets` / `status` topics.
- `spec.mirrors[0].source` — where records are read from.

Both point at `krafter` here. That is the loopback.

## Step 2: Install

The CLI installs this loopback as a component of the stack — tick
"MirrorMaker 2" in the `kates deploy` wizard, or pass the flag:

```bash
kates deploy --with-mirror-maker2
```

That is the release below with both ends pointed at the primary and the
versions following the operator's; `kates deploy status` and `kates clean`
know about it. By hand, the same install is:

```bash
helm upgrade --install "${MM2_RELEASE}" charts/mirror-maker2 \
  --namespace "${KAFKA_NS}" \
  -f charts/mirror-maker2/values-kind.yaml \
  --wait --timeout 10m
```

The `values-kind.yaml` overlay drops every replication factor to 1, because the
kind cluster has one broker and RF3 internal topics would fail to create — not
at install time, but minutes later, inside the connector. It also narrows
`topicsPattern` to `kates\..*`. (Do not try to pass a regex like that through
`--set`: Helm's flag parser treats a backslash as an escape and quietly turns
`kates\..*` into `kates..*`. Regexes belong in values files.)

Both ends of this loopback read and write as the same `kates-mm2` user, which
the `kafka-cluster` chart provisions — so no extra Secret is needed. A real
source needs its own principal on its own cluster; that is the credential
contract in the chart README.

Watch it come up:

```bash
kubectl -n "${KAFKA_NS}" wait "kafkamirrormaker2/${MM2_RELEASE}-mirror-maker2" \
  --for=condition=Ready --timeout=300s
```

## Step 3: Ask the Right Question

Here is the part that matters more than the install.

**Question 1 — are the workers up?** This is what `Ready` answers:

```bash
kubectl -n "${KAFKA_NS}" get kafkamirrormaker2 "${MM2_RELEASE}-mirror-maker2"
```

Output:

```text
NAME                 DESIRED REPLICAS   READY
mm2-mirror-maker2    1                  True
```

`True` here is compatible with a completely dead mirror. It says the Connect
workers started. It says nothing about whether a single record has crossed.

**Question 2 — are the connectors running?** The operator writes the real answer
onto the CR:

```bash
kubectl -n "${KAFKA_NS}" get kafkamirrormaker2 "${MM2_RELEASE}-mirror-maker2" \
  -o jsonpath='{range .status.connectors[*]}{.name}{"\t"}{.connector.state}{"\n"}{end}'
```

Output:

```text
source->target.MirrorSourceConnector       RUNNING
source->target.MirrorCheckpointConnector   RUNNING
```

Better. But a connector reading nothing is also `RUNNING`.

**Question 3 — is data arriving?** The only answer that cannot be faked. Produce
something:

```bash
kubectl -n "${KAFKA_NS}" run producer --rm -i --restart=Never \
  --image=quay.io/strimzi/kafka:1.1.0-kafka-4.3.0 \
  --labels="kates.io/test-pod=true" \
  --env=LOG_DIR=/tmp --command -- /bin/sh -c '
    echo "security.protocol=SASL_PLAINTEXT" > /tmp/c.properties
    echo "sasl.mechanism=SCRAM-SHA-512" >> /tmp/c.properties
    echo "sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"kates-mm2\" password=\"'"$(kubectl -n "${KAFKA_NS}" get secret kates-mm2 -o jsonpath='{.data.password}' | base64 -d)"'\";" >> /tmp/c.properties
    for i in 1 2 3 4 5; do echo "hello-$i"; done | \
      /opt/kafka/bin/kafka-console-producer.sh \
        --bootstrap-server krafter-kafka-bootstrap:9092 \
        --producer.config /tmp/c.properties --topic kates.demo'
```

> **Three flags are load-bearing.** `kates.io/test-pod=true` is the selector the
> `kafka-cluster` NetworkPolicy allows through to the brokers — without it this
> pod times out against a perfectly healthy cluster. `--command` makes the
> arguments replace the image's ENTRYPOINT rather than being appended to it.
> `LOG_DIR=/tmp` keeps the CLI's log4j output off the image's read-only tree.
> (The password is interpolated into the command here for brevity; the repo's
> scripts inject it from the Secret instead, so it never lands in a pod spec.)

Wait for the refresh interval (60 seconds by default), then look for the
replicated topic:

```bash
make mm2-topics | grep 'source.kates.demo'
```

`make mm2-topics` asks the brokers, as the `kates-mm2` user. It has to: the
Strimzi Topic Operator is unidirectional, so topics MirrorMaker creates directly
in Kafka never become `KafkaTopic` resources, and `kubectl get kafkatopics`
would show you nothing.

The name is `source.kates.demo`, not `kates.demo`. That prefix is the
`DefaultReplicationPolicy` at work, and it is the next thing to understand.

## Step 4: Replication Policies

The default policy prefixes every replicated topic with the source alias. That
is correct for disaster recovery and aggregation, where a record's origin
cluster is information you need. It is **wrong for a migration**, where the whole
point is that consumers repoint at a new cluster and find the topics they
already know.

See what the other policy would render — render, not apply:

```bash
diff <(helm template mm2 charts/mirror-maker2 -n "${KAFKA_NS}" -f charts/mirror-maker2/values-kind.yaml) \
     <(helm template mm2 charts/mirror-maker2 -n "${KAFKA_NS}" -f charts/mirror-maker2/values-kind.yaml \
         --set replicationPolicy.mode=identity)
```

Three things change in the CR: `replication.policy.class` appears on **both**
connectors, and the computed `topicsExcludePattern` grows to keep MM2's own
`heartbeats`, `checkpoints` and offset-syncs topics out of a flow that would
otherwise consume them.

> **Why this is a render and not an upgrade.** On a loopback, identity mode
> makes source and target the *same topic*: the mirror reads back what it
> writes, an infinite loop of its own records. The computed exclusions keep the
> internal topics safe (and, under the default policy, keep `source.*` from
> being re-mirrored as `source.source.*`), but nothing can stop
> `kates.demo → kates.demo` on a single cluster. Identity mode needs a
> genuinely separate source, which is what the migration tutorials provide.

Notice, too, what the *default* policy already excludes on this loopback:
`source\..*`. Without it, `source.kates.demo` matches `.*` on the next refresh
and is mirrored again as `source.source.kates.demo`, and again after that.

## Step 5: Run the Tests

```bash
helm test "${MM2_RELEASE}" -n "${KAFKA_NS}" --logs
```

Two tests run by default (the pod names are what `--logs` prints):

- `…-test-ready` — the CR reaches `Ready`.
- `…-test-connectors` — every connector is `RUNNING` (or `PAUSED`/`STOPPED`,
  which is a cutover rather than a fault) with no `FAILED` tasks.

The second exists because the first passes on a broken mirror. Enable the data
test to close the loop entirely:

```bash
helm upgrade "${MM2_RELEASE}" charts/mirror-maker2 \
  --namespace "${KAFKA_NS}" --reuse-values \
  --set tests.replication.enabled=true \
  --set tests.replication.topic=kates.demo \
  --set tests.replication.messages=50

helm test "${MM2_RELEASE}" -n "${KAFKA_NS}" --logs
```

That one creates the topic if needed, produces 50 records to the source, and
reads them back off the target under the policy-correct name — counting
*distinct* records, because MirrorMaker 2 is at-least-once and a retry can
duplicate. It is slower, needs credentials, and is the only one of the three
whose failure means something unambiguous.

## Step 6: Turn On Observability

```bash
helm upgrade "${MM2_RELEASE}" charts/mirror-maker2 \
  --namespace "${KAFKA_NS}" --reuse-values \
  --set metrics.enabled=true \
  --set podMonitors.enabled=true \
  --set alerts.enabled=true \
  --set dashboard.enabled=true
```

`alerts` and `podMonitors` both require `metrics.enabled` and the chart says so
loudly if you forget: Strimzi only opens the scrape port when `metricsConfig` is
set, so rules evaluated without it never fire and everything looks fine.

The metric worth knowing by name is
`kafka_connect_mirror_source_connector_replication_latency_ms_max` — end-to-end
latency from a source append to a target write. It is the number that decides
whether a cutover is safe.

## Step 7: Clean Up

```bash
make mm2-undeploy MM2_RELEASE="${MM2_RELEASE}"
# equivalent to:
#   helm uninstall mm2 -n kafka
#   kubectl -n kafka delete kafkamirrormaker2 mm2-mirror-maker2
```

`keepOnDelete` leaves the CR running on purpose, so the second command is not
optional. That default is deliberate: in production, `helm uninstall` should not tear down
replication as a side effect. The cost is that a later install with the same
release name fails on the kept object, so tear-down is explicit.

## What You Learned

- A `KafkaMirrorMaker2` is a Connect cluster spanning two Kafka clusters, and
  `spec.target` / `spec.mirrors[].source` is the whole model.
- `Ready` means the workers started. `RUNNING` means the connectors started.
  Only record counts mean it is working.
- The replication policy decides whether topic names survive, and that choice is
  what separates a DR mirror from a migration.
- Replication lag is the metric that gates a cutover.

## Next

- [Migrating Kafka 2.x to 4.x](11-migrating-kafka-2x-to-4x.md) — a real legacy
  source, and the protocol floor that makes 2.x interesting.
- [Migrating Kafka 3.x to 4.x](12-migrating-kafka-3x-to-4x.md) — the same
  migration without the protocol risk.
- [MirrorMaker 2 Runbook](../mirror-maker2-runbook.md) — cutover, rollback, and
  the failure table.
