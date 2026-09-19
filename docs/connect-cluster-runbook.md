# Kafka Connect Runbook

What each alert of [charts/connect-cluster](../charts/connect-cluster/README.md) means, how to confirm it, and what to do. Every rule's `runbook_url` points at its section here; the anchor is the alert name in lower case.

The examples use the defaults: release `connect-cluster` in namespace `connect`, Kafka cluster `krafter` in `kafka`.

```bash
NS=connect; CONNECT=connect-cluster
kubectl -n $NS get kafkaconnect $CONNECT -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.message}{"\n"}{end}'
kubectl -n $NS get kafkaconnectors -l strimzi.io/cluster=$CONNECT
# The REST API, from inside a worker (the NetworkPolicy admits the workers, the operator and declared clients):
kubectl -n $NS exec $CONNECT-connect-0 -- curl -s 'http://localhost:8083/connectors?expand=status' | jq .
```

---

## The one thing to internalise

**`Ready` on a `KafkaConnect` does not mean data is moving.** It means the workers are up. A connector can be `FAILED`, or running with failed tasks, while the `KafkaConnect` and even the `KafkaConnector` report `Ready`. Read the connector state and the task states:

```bash
kubectl -n $NS get kafkaconnector <name> \
  -o jsonpath='{.status.connectorStatus.connector.state}{"\n"}{range .status.connectorStatus.tasks[*]}{.id} {.state} {.worker_id}{"\n"}{end}'
```

A failed task's stack trace is in `.status.connectorStatus.tasks[*].trace`, and in the log of the worker named by `worker_id`.

---

## Workers

### KafkaConnectWorkerDown

**Meaning.** Fewer workers answer Prometheus than the release runs (`replicas`, or `autoscaling.minReplicas`). Their tasks are rebalanced onto the others after `scheduled.rebalance.max.delay.ms` (5 minutes by default), and until then they do not run.

**Confirm.** `kubectl -n $NS get pods -l strimzi.io/name=$CONNECT-connect -o wide`. A pod that is Running but not scraped points at the PodMonitor or at a NetworkPolicy that drops the scrape (`networkPolicy.monitoring.namespace`).

**Act.**
1. A pod in `CrashLoopBackOff`: read `kubectl logs --previous`. The usual causes are a Kafka connection it cannot make (run the chart's preflight: `--set preflight.enabled=true`), a missing credentials Secret, and heap exhaustion.
2. A pod that is `Pending`: the scheduling constraints (`topologySpreadConstraints`, `nodeSelector`, `priorityClassName`) cannot be met.
3. A pod that is Running and unscraped: check the PodMonitor's labels against your Prometheus' selector.

### KafkaConnectRebalanceStorm

**Meaning.** The workers rebalance continuously. No task runs during a rebalance, so this reads as intermittent lag with no failed task.

**Confirm.** The dashboard's **Workers** row. Worker logs show `Rebalance started` repeatedly.

**Act.** A worker that restarts (see KafkaConnectWorkerDown), a worker whose heartbeats time out under GC pressure (see KafkaConnectWorkerHeapHigh), or a connector whose `taskConfigs` change on every poll. Raise `session.timeout.ms` in `extraConfig` only after ruling out the first two.

### KafkaConnectRebalanceTooLong

**Meaning.** A rebalance has not finished in 5 minutes. Tasks are stopped meanwhile.

**Confirm.** `kafka_connect_worker_rebalance_metrics_rebalancing` is 1 on some worker. The worker logs say what the leader waits for.

**Act.** Usually a worker that joined and is unreachable on 8083 from the leader (worker-to-worker traffic, `networkPolicy.workerToWorker`), or a task that does not stop within `task.shutdown.graceful.timeout.ms`. Restart the stuck worker pod.

### KafkaConnectWorkerHeapHigh

**Meaning.** A worker's heap is above `alerts.thresholds.heapUsagePercent` for 5 minutes. Long GC pauses follow, then missed heartbeats and rebalances.

**Confirm.** The dashboard's heap panels, per pod. One worker high and the rest low means one heavy task; all high means the group is undersized.

**Act.** Raise `jvmOptions.-Xmx` together with `resources.limits.memory` (leave a quarter for off-heap), add workers, or lower the connector's batch sizes (`consumer.override.max.poll.records`, `producer.override.batch.size`).

---

## Connectors

### KafkaConnectConnectorFailed

**Meaning.** The connector itself is `FAILED`: it could not start or its configuration was rejected. None of its tasks run.

**Confirm.** `kubectl -n $NS get kafkaconnector <name> -o jsonpath='{.status.connectorStatus.connector.trace}'`.

**Act.** Fix the cause the trace names (a class that is not installed, an unreachable database, a Secret the config provider cannot read — see [Secrets](#a-connector-cannot-read-a-secret)), then restart it:

```bash
kubectl -n $NS annotate kafkaconnector <name> strimzi.io/restart=true
```

### KafkaConnectTaskFailed

**Meaning.** Tasks of a connector are `FAILED`. Their share of the work stops; the connector and the CR may still say `RUNNING` and `Ready`.

**Confirm.** The task traces (above). `autoRestart` restarts failed tasks with a back-off up to `maxRestarts`; this alert fires when they stay failed for 2 minutes.

**Act.** Fix the cause, then restart the tasks:

```bash
kubectl -n $NS annotate kafkaconnector <name> strimzi.io/restart-task=<id>
```

A task that fails on one bad record should not stop the pipeline: give sink connectors a `deadLetterQueue`.

### KafkaConnectTasksNotRunning

**Meaning.** Tasks of a connector have been neither running nor paused for 5 minutes: failed, unassigned or restarting.

**Confirm.** Task states, as above. `UNASSIGNED` tasks with all workers up point at a rebalance that does not finish.

**Act.** Failed tasks: see KafkaConnectTaskFailed. Unassigned tasks: see KafkaConnectRebalanceTooLong. Restarting tasks that never settle: the restart loop is hiding a persistent error; read the trace.

### KafkaConnectErrorsLogged

**Meaning.** A connector logs more record errors per second than `alerts.thresholds.errorRatePerSecond`. With `errors.tolerance=all` these records are skipped or dead-lettered, not retried forever.

**Confirm.** The dashboard's **Errors and dead letter queue** row, and the worker log (`errors.log.include.messages` adds the record).

**Act.** A converter mismatch (JSON with `schemas.enable` against schemaless data, or Avro without the registry) is the usual cause for sources and sinks alike. Fix the converter or the producer, then decide whether the skipped records need replaying.

### KafkaConnectDeadLetterWrites

**Meaning.** A sink connector sends records to its dead letter queue. The pipeline continues; those records did not reach the sink.

**Confirm.** Read the queue. The headers say why each record failed (`errors.deadletterqueue.context.headers.enable`):

```bash
kafka-console-consumer.sh --bootstrap-server krafter-kafka-bootstrap.kafka:9092 \
  --topic kates-connect-cluster-dlq-<connector> --from-beginning \
  --property print.headers=true --max-messages 10 --consumer.config client.properties
```

**Act.** Fix the cause, then replay the queue into the source topic or into a one-off sink connector reading the queue.

### KafkaConnectDeadLetterFailures

**Meaning.** A sink connector cannot write to its dead letter queue. Records that fail are lost.

**Confirm.** The worker log shows the producer error: usually `TopicAuthorizationException` or an unknown topic.

**Act.**
1. The topic: with `deadLetterQueue.createTopic` the chart renders a `KafkaTopic` in the Kafka namespace; check it is `Ready`.
2. The ACL: a chart-managed KafkaUser grants the queue; a user provisioned elsewhere needs `Write` and `Describe` on it.
3. Until fixed, consider pausing the connector (`state: paused`) rather than losing records.

### KafkaConnectOffsetCommitFailures

**Meaning.** A connector's offset commits fail. It keeps processing, but a restart resumes from the last committed offset and reprocesses everything since.

**Confirm.** The dashboard's **Offset commits** row. For a source, the worker log names the offsets topic error; for a sink, the consumer group commit error.

**Act.** A source connector needs `Write` on the offsets topic (`<groupId>-offsets`) and, under exactly-once, its transactional ID; a sink connector needs `Read` on its group (`connect-<connector>`). Slow commits (`offset.flush.timeout.ms`) under broker pressure are the other cause.

### KafkaConnectSourceIdle

**Meaning.** (Opt-in, `alerts.sourceIdle.enabled`.) A running source connector has polled no records for `alerts.thresholds.sourceIdleMinutes`.

**Confirm.** Whether the source had changes in that window. For Debezium, check the replication slot or binlog position on the database.

**Act.** A quiet source is not a fault: raise the window or leave this alert off for it. A source with changes and no polls: the connector lost its position (a dropped replication slot, a purged binlog) and needs a new snapshot; or it waits on a lock it cannot get.

### KafkaConnectSinkLag

**Meaning.** A sink connector's consumer group (`connect-<connector>`) is more than `alerts.thresholds.sinkLagRecords` behind, per kafka-cluster's Kafka Exporter.

**Confirm.** The dashboard's sink lag panel, and the group itself:

```bash
kafka-consumer-groups.sh --bootstrap-server krafter-kafka-bootstrap.kafka:9092 \
  --describe --group connect-<connector> --command-config client.properties
```

**Act.** Raise `tasksMax` (up to the topic's partition count), speed up the sink (batch sizes, the target system's capacity), or add workers when tasks already saturate them.

### KafkaConnectTaskAvailabilitySLOBurning

**Meaning.** (`alerts.slo.enabled`.) The fraction of declared tasks that run has been low for long enough to spend the `alerts.slo.target` error budget `burnRate` times too fast, over both a 1 h and a 5 m window (or 6 h and 30 m).

**Confirm.** `connect:tasks_running:ratio{cluster="<release>"}` over the last day.

**Act.** This alert says how much it matters, not why. Find the connectors with tasks that do not run (KafkaConnectTasksNotRunning, KafkaConnectTaskFailed) and work those.

---

## Common causes

### A connector cannot read a Secret

The workers resolve `${secrets:<namespace>/<name>:<key>}` as the `<release>-connect` ServiceAccount, and the chart grants `get` on exactly the Secrets the connector configs in its values reference. A connector applied outside the chart (by hand, by `kates deploy`) needs its Secrets listed in `rbac.secretNames`. The trace reads `Forbidden` or `secrets "…" is forbidden`.

```bash
kubectl -n $NS get role -l app.kubernetes.io/instance=$CONNECT -o yaml | grep -A3 resourceNames
kubectl auth can-i get secret/<name> -n <namespace> --as=system:serviceaccount:$NS:$CONNECT-connect
```

### The workers cannot reach Kafka

Run the preflight (`--set preflight.enabled=true`) and read its verdict: `kubectl -n $NS logs job/$CONNECT-preflight`. A `NETWORK` verdict with the pods Running is usually `networkPolicy.kafka.ports`, or kafka-cluster's broker policy lacking a `networkPolicy.clients` entry for this namespace.
