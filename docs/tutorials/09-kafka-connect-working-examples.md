# Tutorial 9: Kafka Connect Working Examples (CDC + JDBC)

This tutorial walks through deploying production-ready Kafka Connect connectors on your cluster — a CDC pipeline with Debezium and JDBC sink/source connectors. By the end, you'll see a row inserted in PostgreSQL automatically replicate to a Kafka topic and then to a replica table.

> For Kafka Connect architecture and deployment theory, see [Kafka Connect & CDC Pipelines](../book/21-kafka-connect.md).

## What You Will Deploy

The working examples are defined as `testConnectors` (and the CDC topic as `testTopics`) in [`charts/connect-cluster/values.yaml`](../../charts/connect-cluster/values.yaml). `helm test connect-cluster` creates them, waits for each connector to reach RUNNING with all its tasks, and deletes them again when the suite passes; this tutorial renders the same manifests and applies them so they keep running:

| Connector | Purpose |
|---|---|
| CDC topic (`cdc.public.demo_orders`) | Created in the Kafka namespace for the Debezium change stream |
| `debezium-postgres-source-working-example` | Debezium source: PostgreSQL `public.demo_orders` -> Kafka |
| `jdbc-sink-from-cdc-working-example` | JDBC sink: `cdc.public.demo_orders` -> PostgreSQL `demo_orders_replica` |
| `jdbc-sink-working-example` | Generic JDBC sink from `kates-results` |
| `jdbc-source-working-example` | Generic JDBC source template (Aiven JDBC source plugin) |

## Prerequisites

```bash
export CONNECT_NS=connect
export KAFKA_NS=kafka
export DB_NS=database
export CONNECT_CLUSTER=connect-cluster
export KAFKA_CLUSTER=krafter
```

Verify required resources:

```bash
kubectl get kafkaconnect -n "${CONNECT_NS}"
kubectl get secret connect-pg-credentials -n "${CONNECT_NS}"
kubectl get secret kates-connect -n "${CONNECT_NS}"
kubectl get pods -n "${DB_NS}"
```

Required secrets in `CONNECT_NS`:
- `connect-pg-credentials` with keys `username`, `password`
- `kates-connect` with key `password`

The chart grants the Connect workers `get` on these two Secrets because the example connectors' configs reference them. The Debezium example reads `kates-connect` for its SCRAM schema-history client, so the examples expect the default SCRAM connection on port 9092 (`values-prod.yaml` turns them off).

The chart depends on the `kafka-common` library; `kates deploy` builds it, otherwise run this once before any `helm template` or `helm test` below:

```bash
helm dependency build charts/connect-cluster
```

## Namespace and Cluster Name Customization

The example connectors and topic are values templates, rendered with the release:
- Connectors: the release namespace (`connect`), labelled `strimzi.io/cluster: <release>` (`connect-cluster`)
- Secret references: `${secrets:<release namespace>/...}`
- CDC topic: `kafka.namespace` (`kafka`), labelled `strimzi.io/cluster: <kafka.clusterName>` (`krafter`)
- Debezium schema-history bootstrap: the chart's computed bootstrap address

If your environment differs, set `kafka.namespace` and `kafka.clusterName` (or `kafka.bootstrapServers`) on the release rather than editing manifests. `kafka.namespace` defaults to the release namespace, so pass it whenever Connect and Kafka live in different namespaces — `kates deploy` does.

## Part A: CDC Pipeline (Topic + Debezium Source + JDBC Sink)

### 1) Create Source and Sink Tables

```bash
kubectl exec -n "${DB_NS}" postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=postgres /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U postgres -d orders -c \
  \"ALTER ROLE debezium WITH REPLICATION LOGIN;\""

kubectl exec -n "${DB_NS}" postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=debezium /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U debezium -d orders -c \
  \"CREATE TABLE IF NOT EXISTS public.demo_orders (id INTEGER PRIMARY KEY, customer_name TEXT NOT NULL, amount NUMERIC(10,2) NOT NULL, created_at TIMESTAMPTZ DEFAULT now());\""

kubectl exec -n "${DB_NS}" postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=debezium /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U debezium -d orders -c \
  \"CREATE TABLE IF NOT EXISTS public.demo_orders_replica (id INTEGER PRIMARY KEY, customer_name TEXT NOT NULL, amount NUMERIC(10,2) NOT NULL, created_at TIMESTAMPTZ);\""
```

### 2) Apply CDC Topic and Connectors

```bash
# Optional: validate the release. The suite creates the examples, checks that
# each connector reaches RUNNING, and deletes them again when it passes.
helm test connect-cluster -n "${CONNECT_NS}" --logs

# Deploy the CDC topic and all working example connectors persistently
helm template "${CONNECT_CLUSTER}" charts/connect-cluster -n "${CONNECT_NS}" \
  --set kafka.namespace="${KAFKA_NS}" \
  --set kafka.clusterName="${KAFKA_CLUSTER}" \
  -s templates/tests/test-02-topics.yaml \
  -s templates/tests/test-02-connectors.yaml | kubectl apply -f -
```

> [!TIP]
> The manifests are packaged as Helm test hooks (`templates/tests/test-02-topics.yaml` and `test-02-connectors.yaml`), which `helm test` deletes once it passes. Rendering them with `helm template -s` and applying them with `kubectl apply` keeps them running. They keep their hook annotations, so a later `helm test` replaces them and removes them again.

### 3) Wait Until Ready

```bash
kubectl wait -n "${KAFKA_NS}" --for=condition=Ready kafkatopic/cdc-public-demo-orders --timeout=180s
kubectl wait -n "${CONNECT_NS}" --for=condition=Ready kafkaconnector/debezium-postgres-source-working-example --timeout=300s
kubectl wait -n "${CONNECT_NS}" --for=condition=Ready kafkaconnector/jdbc-sink-from-cdc-working-example --timeout=300s
kubectl get kafkaconnector -n "${CONNECT_NS}"
```

### 4) Insert Test Data

```bash
kubectl exec -n "${DB_NS}" postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=debezium /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U debezium -d orders -c \
  \"INSERT INTO public.demo_orders (id, customer_name, amount) VALUES (1001, 'alice', 42.50) ON CONFLICT (id) DO UPDATE SET customer_name = EXCLUDED.customer_name, amount = EXCLUDED.amount;\""
```

### 5) Verify Replication to Sink Table

```bash
kubectl exec -n "${DB_NS}" postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=debezium /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U debezium -d orders -c \
  \"SELECT id, customer_name, amount FROM public.demo_orders_replica WHERE id = 1001;\""
```

Expected result: one row for `id=1001` in `demo_orders_replica`.

## Part B: Generic JDBC Sink Example

Step 2 of Part A already applied the standalone sink example:

```bash
kubectl wait -n "${CONNECT_NS}" --for=condition=Ready kafkaconnector/jdbc-sink-working-example --timeout=300s
kubectl describe kafkaconnector -n "${CONNECT_NS}" jdbc-sink-working-example
```

Notes:
- This connector consumes `kates-results` by default.
- It is useful as a baseline JDBC sink template when you want automatic table creation/evolution.

## Part C: Generic JDBC Source Example

Step 2 of Part A already applied the source template:

```bash
kubectl wait -n "${CONNECT_NS}" --for=condition=Ready kafkaconnector/jdbc-source-working-example --timeout=300s
kubectl describe kafkaconnector -n "${CONNECT_NS}" jdbc-source-working-example
```

Important:
- This uses plugin class `io.aiven.connect.jdbc.JdbcSourceConnector`, which the bundled `ghcr.io/bmscomp/connect` image ships (Aiven JDBC connector).
- If you run a custom Connect image without that plugin, connector status will show class-not-found or failed state.

## Troubleshooting

Quick checks:

```bash
kubectl get kafkaconnector -n "${CONNECT_NS}"
kubectl describe kafkaconnector -n "${CONNECT_NS}" debezium-postgres-source-working-example
kubectl describe kafkaconnector -n "${CONNECT_NS}" jdbc-sink-from-cdc-working-example
kubectl logs -n "${CONNECT_NS}" -l strimzi.io/name="${CONNECT_CLUSTER}-connect" --tail=200
```

Most common issues:
- `kafka.namespace` or `kafka.clusterName` not matching your Kafka cluster — the CDC topic is created in the wrong place, and the Debezium schema-history client dials the wrong bootstrap
- A connector `FAILED` with `Forbidden` reading a Secret — the chart grants only the Secrets its connector configs reference; list any others in `rbac.secretNames`
- Missing JDBC source plugin class for the `jdbc-source-working-example` connector
- `helm test` failing in its last pod (`<release>-test-connectors`): `--logs` prints each connector's state and the first lines of any task trace

## Cleanup

```bash
kubectl delete kafkaconnector -n "${CONNECT_NS}" \
  debezium-postgres-source-working-example \
  jdbc-sink-from-cdc-working-example \
  jdbc-sink-working-example \
  jdbc-source-working-example --ignore-not-found

kubectl delete kafkatopic -n "${KAFKA_NS}" cdc-public-demo-orders --ignore-not-found

kubectl exec -n "${DB_NS}" postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=postgres /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U postgres -d orders -c \
  \"DROP TABLE IF EXISTS public.demo_orders_replica; DROP TABLE IF EXISTS public.demo_orders;\""
```
