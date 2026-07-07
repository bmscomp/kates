# Kafka Connect Source/Sink Demo Plan (Default Cluster Context)

For the full tutorial that documents all working Kafka Connect examples (CDC topic, Debezium source, JDBC sink from CDC, generic JDBC sink, and generic JDBC source), use:

- [09-kafka-connect-working-examples.md](09-kafka-connect-working-examples.md)

This runbook demonstrates a full end-to-end flow:
- Insert one row in PostgreSQL
- Verify Debezium source connector publishes to Kafka topic
- Verify JDBC sink connector writes to a replica table

## 1. Preconditions

```bash
kubectl config current-context
kubectl get kafkaconnect connect-cluster -n connect
kubectl get pods -n connect
kubectl get pods -n database
```

Expected:
- Context is the default admin context (for example `kubernetes-admin@taz-tls-int-10`)
- `connect-cluster` is `Ready=True`
- Connect and PostgreSQL pods are running

## 2. Create/Verify Source Table

```bash
kubectl exec -n database postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=postgres /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U postgres -d orders -c \
  \"ALTER ROLE debezium WITH REPLICATION LOGIN;\""

kubectl exec -n database postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=debezium /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U debezium -d orders -c \
  \"CREATE TABLE IF NOT EXISTS public.demo_orders (id INTEGER PRIMARY KEY, customer_name TEXT NOT NULL, amount NUMERIC(10,2) NOT NULL, created_at TIMESTAMPTZ DEFAULT now());\""
```

## 3. Apply Demo Resources

```bash
# Migrated to helm test: helm test connect-cluster -n connect
# Migrated to helm test: helm test connect-cluster -n connect
# Migrated to helm test: helm test connect-cluster -n connect
```

## 4. Wait for Connectors to be Ready

```bash
kubectl wait -n connect --for=condition=Ready kafkaconnector/debezium-postgres-source-working-example --timeout=5m
kubectl wait -n connect --for=condition=Ready kafkaconnector/jdbc-sink-from-cdc-working-example --timeout=5m
kubectl get kafkaconnector -n connect
```

## 5. Insert a Record in Source DB

```bash
kubectl exec -n database postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=debezium /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U debezium -d orders -c \
  \"INSERT INTO public.demo_orders (id, customer_name, amount) VALUES (1001, 'alice', 42.50) ON CONFLICT (id) DO UPDATE SET customer_name = EXCLUDED.customer_name, amount = EXCLUDED.amount;\""
```

## 6. Verify Message in Kafka Topic

```bash
kubectl get kafkaconnector debezium-postgres-source-working-example -n connect \
  -o jsonpath='{.status.topics}{"\n"}{.status.connectorStatus.tasks[0].state}{"\n"}'

kubectl get kafkaconnector jdbc-sink-from-cdc-working-example -n connect \
  -o jsonpath='{.status.topics}{"\n"}{.status.connectorStatus.tasks[0].state}{"\n"}'
```

Expected:
- Both connectors show `RUNNING`
- `status.topics` includes `cdc.public.demo_orders`

## 7. Verify Sink Table Received the Row

```bash
kubectl exec -n database postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=debezium /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U debezium -d orders -c \
  \"SELECT id, customer_name, amount FROM public.demo_orders_replica WHERE id = 1001;\""
```

Expected:
- Row exists in `demo_orders_replica` with the same values

## 8. Optional Cleanup

```bash
kubectl delete kafkaconnector -n connect debezium-postgres-source-working-example jdbc-sink-from-cdc-working-example
kubectl delete kafkatopic -n kafka cdc-public-demo-orders
kubectl exec -n database postgresql-0 -- /bin/bash -lc \
  "PGPASSWORD=postgres /opt/bitnami/postgresql/bin/psql -h 127.0.0.1 -U postgres -d orders -c \
  \"DROP TABLE IF EXISTS public.demo_orders_replica; DROP TABLE IF EXISTS public.demo_orders;\""
```

## Extra JDBC Source Example

The repository also includes a JDBC source connector template:

- `config/kafka-connect/working-example-jdbc-source.yaml`

Use it only after adding a compatible JDBC source plugin class (`io.aiven.connect.jdbc.JdbcSourceConnector`) to the Connect image.
