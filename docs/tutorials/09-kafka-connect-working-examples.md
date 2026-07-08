# Tutorial 9: Kafka Connect Working Examples (CDC + JDBC)

This tutorial walks through deploying production-ready Kafka Connect connectors on your cluster — a CDC pipeline with Debezium and JDBC sink/source connectors. By the end, you'll see a row inserted in PostgreSQL automatically replicate to a Kafka topic and then to a replica table.

> For Kafka Connect architecture and deployment theory, see [Chapter 21: Kafka Connect & CDC Pipelines](../book/21-kafka-connect.md).

## What You Will Deploy

| Manifest | Purpose |
|---|---|
| `config/kafka-connect/working-example-cdc-topic.yaml` | Creates the CDC topic `cdc.public.demo_orders` |
| `config/kafka-connect/working-example-debezium-postgres-source.yaml` | Debezium source: PostgreSQL `public.demo_orders` -> Kafka |
| `config/kafka-connect/working-example-jdbc-sink-from-cdc.yaml` | JDBC sink: `cdc.public.demo_orders` -> PostgreSQL `demo_orders_replica` |
| `config/kafka-connect/working-example-jdbc-sink.yaml` | Generic JDBC sink from `kates-results` |
| `config/kafka-connect/working-example-jdbc-source.yaml` | Generic JDBC source template (Aiven JDBC source plugin) |

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

## Namespace and Cluster Name Customization

The manifests are committed with defaults:
- Connect namespace: `connect`
- Kafka namespace: `kafka`
- Connect cluster label: `strimzi.io/cluster: connect-cluster`
- Kafka cluster label: `strimzi.io/cluster: krafter`
- Secret references: `${secrets:connect/...}`

If your environment differs (for example Connect runs in `kafka` or another namespace), copy the files and update:
- `metadata.namespace`
- `metadata.labels["strimzi.io/cluster"]`
- Secret references from `${secrets:connect/...}` to `${secrets:<your-connect-namespace>/...}`

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
# Deploy all working example connectors via helm test
helm test connect-cluster -n "${CONNECT_NS}"
```

> [!TIP]
> The connector manifests are packaged as Helm test hooks. Running `helm test` applies the KafkaTopic and KafkaConnector CRDs that were previously applied manually with `kubectl apply`.

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

Apply the standalone sink example:

```bash
# Deploy the standalone JDBC sink via helm test
helm test connect-cluster -n "${CONNECT_NS}"
kubectl wait -n "${CONNECT_NS}" --for=condition=Ready kafkaconnector/jdbc-sink-working-example --timeout=300s
kubectl describe kafkaconnector -n "${CONNECT_NS}" jdbc-sink-working-example
```

Notes:
- This connector consumes `kates-results` by default.
- It is useful as a baseline JDBC sink template when you want automatic table creation/evolution.

## Part C: Generic JDBC Source Example

Apply the source template:

```bash
# Deploy the JDBC source via helm test
helm test connect-cluster -n "${CONNECT_NS}"
kubectl wait -n "${CONNECT_NS}" --for=condition=Ready kafkaconnector/jdbc-source-working-example --timeout=300s
kubectl describe kafkaconnector -n "${CONNECT_NS}" jdbc-source-working-example
```

Important:
- This requires plugin class `io.aiven.connect.jdbc.JdbcSourceConnector` in your Connect image.
- If the plugin is missing, connector status will show class-not-found or failed state.

## Troubleshooting

Quick checks:

```bash
kubectl get kafkaconnector -n "${CONNECT_NS}"
kubectl describe kafkaconnector -n "${CONNECT_NS}" debezium-postgres-source-working-example
kubectl describe kafkaconnector -n "${CONNECT_NS}" jdbc-sink-from-cdc-working-example
kubectl logs -n "${CONNECT_NS}" -l strimzi.io/name="${CONNECT_CLUSTER}-connect" --tail=200
```

Most common issues:
- Wrong namespace or cluster label in manifest metadata
- Secret references still pointing to `connect` after namespace change
- Missing JDBC source plugin class for `working-example-jdbc-source.yaml`

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
