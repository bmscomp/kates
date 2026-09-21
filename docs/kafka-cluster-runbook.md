# Kafka Cluster Runbook

What each alert of [charts/kafka-cluster](../charts/kafka-cluster/README.md) and [charts/strimzi-operator](../charts/strimzi-operator/README.md) means, how to confirm it, and what to do. Every rule's `runbook_url` points at its section here; the anchor is the alert name in lower case.

The examples use the defaults: cluster `krafter` in namespace `kafka`, operator in `strimzi-operator`.

```bash
NS=kafka; CLUSTER=krafter
kubectl -n $NS get kafka $CLUSTER -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason} {.message}{"\n"}{end}'
kubectl -n $NS get kafkanodepools,pods -l strimzi.io/cluster=$CLUSTER
# A client shell with the platform's backend credentials:
kubectl -n $NS run kcat --rm -it --restart=Never --labels kates.io/test-pod=true \
  --image=ghcr.io/bmscomp/kates-tester:1.23.0 -- bash
```

Inside that shell, `/opt/kafka/bin/kafka-topics.sh --bootstrap-server $CLUSTER-kafka-bootstrap:9092 --command-config client.properties …` answers most partition questions; `kafka-metadata-quorum.sh … describe --status` answers quorum questions.

---

## Availability

### KafkaOfflinePartitions

**Meaning.** Partitions have no leader. Every produce and fetch to them fails.

**Confirm.** `kafka-topics.sh --describe --unavailable-partitions`. Note which brokers host the replicas.

**Act.**
1. Find the brokers: `kubectl -n $NS get pods -l strimzi.io/name=$CLUSTER-kafka`. A pod not Running, or a node gone, is the usual cause; recover it.
2. If every in-sync replica of a partition is lost and the data can be written off, an unclean election brings it back with data loss — decide explicitly, per topic (`kafka-leader-election.sh --election-type unclean`). The chart keeps `unclean.leader.election.enable: false`.

### KafkaActiveControllerCount

**Meaning.** The quorum does not have exactly one active controller. With none, nothing that changes metadata happens: topic creation, leader elections, broker fencing.

**Confirm.** `kafka-metadata-quorum.sh --bootstrap-controller <controller>:9090 describe --status` (from a controller pod), or the KRaft dashboard.

**Act.** Check the controller pods (`strimzi.io/controller-role=true`). A quorum needs a majority: with three controllers, two must be up. Restore the missing ones before anything else; do not delete controller PVCs.

### KafkaUnderMinIsrPartitions

**Meaning.** Partitions have fewer in-sync replicas than `min.insync.replicas`. Producers with `acks=all` get `NOT_ENOUGH_REPLICAS`.

**Confirm.** `kafka-topics.sh --describe --under-min-isr-partitions`.

**Act.** Same as under-replicated partitions, with urgency: bring the missing brokers back. Lowering `min.insync.replicas` restores writes at the cost of durability; do it per topic and put it back afterwards.

### KafkaOfflineLogDirectory

**Meaning.** A broker marked a log directory offline, usually after an I/O error. Its replicas there are gone until the disk returns.

**Confirm.** `kubectl -n $NS logs <pod> | grep -i 'offline\|IOException'`; `kubectl describe pvc` for the volume.

**Act.** Fix or replace the volume, then restart the broker. A full disk is the most common cause — see [KafkaBrokerDiskUsageCritical](#kafkabrokerdiskusagecritical).

### KafkaUncleanLeaderElection

**Meaning.** An out-of-sync replica became leader. Records the old leader had committed may be gone, and consumers may see offsets move backwards.

**Act.** Find the topic (`kafka.controller` logs), tell its owners what window may be lost, and find out who enabled unclean elections (`unclean.leader.election.enable`, per topic or cluster). Turn it back off.

### KafkaNodesMissing

**Meaning.** Fewer brokers and controllers are scraped than the node pools declare, for ten minutes. Either pods are down or the scrape is failing.

**Confirm.** `kubectl -n $NS get pods -l strimzi.io/name=$CLUSTER-kafka` against `kubectl -n $NS get kafkanodepools`. If every pod is Running, check the PodMonitor target in Prometheus (`up{namespace="kafka", strimzi_io_cluster="krafter"}`) and the NetworkPolicy's metrics rule (`networkPolicy.monitoring.namespace`).

**Act.** Recover the pods (scheduling, PVC binding, image pulls), or fix the scrape.

### KafkaFencedBrokers

**Meaning.** Brokers are registered but fenced: the controller does not consider them alive, so they lead nothing.

**Act.** A fenced broker has lost its session with the controller. Check its logs for heartbeat errors and the NetworkPolicy between brokers and controllers (port 9090). Restart it if it does not unfence.

## Replication

### KafkaUnderReplicatedPartitions

**Meaning.** Followers are out of sync for five minutes. Expected briefly during rolls, not beyond.

**Confirm.** `kafka-topics.sh --describe --under-replicated-partitions`. If every one has the same broker missing from its ISR, that broker is the problem.

**Act.** Check that broker (pod, disk, network, GC). If brokers are healthy but slow, see [KafkaRequestHandlerSaturated](#kafkarequesthandlersaturated) and `num.replica.fetchers`.

### KafkaISRShrinkRate

**Meaning.** In-sync replica sets keep shrinking for ten minutes: followers fall behind repeatedly.

**Act.** Look for a slow or flapping broker (disk latency, CPU throttling, GC pauses) and for network trouble between zones. `replica.lag.time.max.ms` is the threshold Kafka uses.

## KRaft

### KafkaRaftLeaderElections

**Meaning.** The controller quorum elected a leader more often than `alerts.thresholds.raftElectionsPer15m` in fifteen minutes. The quorum is unstable.

**Act.** Check controller pods for restarts and their network (9090 between controllers). Controllers under memory pressure or CPU throttling miss fetch deadlines (`controller.quorum.fetch.timeout.ms`).

### KafkaRaftUnknownVoters

**Meaning.** A node has voter connections it cannot resolve for ten minutes.

**Act.** DNS or NetworkPolicy between nodes. Check `<cluster>-kafka-brokers` Service endpoints and the `<cluster>-allow-dns` policy.

### KafkaBrokerMetadataLag

**Meaning.** A broker applies metadata more than `alerts.thresholds.metadataLagMs` after the controller wrote it. It serves stale leadership and configuration.

**Act.** Check the broker's connection to the active controller and its load. A broker that stays behind should be restarted.

## Performance

### KafkaRequestLatencyHigh

**Meaning.** p99 total time of produce or fetch requests is above `alerts.thresholds.requestLatencyP99Ms` on a broker.

**Act.** The Kafka dashboard's request panels split total time into queue, local, remote and response time. Queue time points at [saturation](#kafkarequesthandlersaturated); local time at disk; remote time at slow followers (`acks=all`).

### KafkaRequestHandlerSaturated

**Meaning.** Request handler threads are idle less than `alerts.thresholds.handlerIdleRatio` of the time.

**Act.** Raise `num.io.threads` if CPU allows, otherwise add brokers or reduce load. Check for a client sending unusually many small requests.

### KafkaRequestQueueSaturated

**Meaning.** More than `alerts.thresholds.requestQueueSize` requests wait on a broker for ten minutes.

**Act.** As for handler saturation; `queued.max.requests` caps the queue.

### KafkaLogFlushLatencyHigh

**Meaning.** p99 log flush time is above `alerts.thresholds.logFlushP99Ms`. The disk is slow.

**Act.** Check the volume's IOPS and throughput limits and noisy neighbours. Consider a faster StorageClass for the pool (a new pool; volumes cannot change class in place).

## Storage

### KafkaBrokerDiskUsageHigh

**Meaning.** A Kafka volume has less than `alerts.thresholds.diskFreeWarning` of its capacity free.

**Act.** Shorten retention for the largest topics, expand the volume (larger `size` on the pool, if the StorageClass allows expansion), or add brokers and rebalance. `kafka.quotas` with `minAvailableRatioPerVolume` throttles producers before a disk fills (the prod overlay sets 0.1).

### KafkaBrokerDiskUsageCritical

**Meaning.** Less than `alerts.thresholds.diskFreeCritical` free. A full log directory goes offline and takes its replicas with it.

**Act.** As above, now. Deleting records (`kafka-delete-records.sh`) frees space fastest for a topic whose data can go.

### KafkaTieredStorageCopyErrors

**Meaning.** Brokers fail to copy closed segments to object storage. Segments stay on local disk, which then fills.

**Act.** Check the broker logs for the storage plugin's errors: credentials (`tieredStorage.credentials.existingSecret`), endpoint and bucket (`remoteStorageManager.config`), and the NetworkPolicy's object-store egress (`tieredStorage.egress`).

## Consumers

### KafkaConsumerGroupLag

**Meaning.** A consumer group is more than `alerts.thresholds.consumerLagWarning` messages behind (Kafka Exporter).

**Act.** `kafka-consumer-groups.sh --describe --group <group>`: lag on every partition means the consumers are slow or down; on some, a stuck partition or a skewed key. Scale the consumers or fix the stuck one.

### KafkaConsumerGroupLagCritical

**Meaning.** More than `alerts.thresholds.consumerLagCritical` messages behind. Retention may delete records before they are read.

**Act.** As above, and compare the lag with the topic's retention. Extend retention temporarily if the backlog is at risk.

## Cruise Control

### CruiseControlAnomalyDetected

**Meaning.** Cruise Control's anomaly detector reported goal violations or disk failures in the last ten minutes.

**Act.** `kubectl -n $NS logs deploy/$CLUSTER-cruise-control | grep -i anomaly`. Goal violations usually follow a broker loss or uneven growth; a rebalance fixes them (`kubectl -n $NS get kafkarebalance`, or enable `rebalance.full`).

### CruiseControlNoLoadModel

**Meaning.** Cruise Control has had no valid metric window for an hour, so it cannot compute proposals, and auto-rebalancing does nothing.

**Act.** Cruise Control reads the brokers' metrics reporter topic (`strimzi.cruisecontrol.metrics`). Check that the topic exists and has data, and that Cruise Control reaches the brokers on 9091.

## SLO

### KafkaAvailabilitySLOBurning

**Meaning.** With `alerts.slo.enabled`: the share of client produce and fetch requests failing for server-side reasons (not enough replicas, timeouts, storage errors, no leader) is burning the error budget too fast, over one hour and five minutes, or six hours and thirty minutes.

**Act.** The error code breakdown is in `kafka_network_requestmetrics_errors_total` by `error`. `NOT_ENOUGH_REPLICAS` points at [KafkaUnderMinIsrPartitions](#kafkaunderminisrpartitions); `REQUEST_TIMED_OUT` at [saturation](#kafkarequesthandlersaturated) or slow followers; `KAFKA_STORAGE_ERROR` at [disks](#kafkaofflinelogdirectory).

## Operator (strimzi-operator chart)

### StrimziOperatorDown

**Meaning.** No Cluster Operator pod has been scraped for five minutes. Nothing reconciles Kafka, Connect or MirrorMaker 2 resources: no rolls, no certificate renewal, no topic or user changes.

**Act.** `kubectl -n strimzi-operator get deploy strimzi-cluster-operator` and its logs. OOM kills are the usual cause (`strimzi-kafka-operator.resources`). If the pod runs, check the operator's PodMonitor and NetworkPolicy (port 8080).

### StrimziReconciliationsFailing

**Meaning.** Reconciliations of a resource kind keep failing for thirty minutes.

**Act.** `kubectl get <kind> -A -o wide` and the failing resource's status conditions; the operator log names the resource and the reason. An invalid spec (for example a CR written for another Strimzi version) is common.

### KafkaCertificateExpiringSoon

**Meaning.** A cluster or clients CA certificate expires within `alerts.thresholds.certificateWarningDays`. The operator renews certificates `renewalDays` before expiry, inside `maintenanceTimeWindows`; one this close was not renewed.

**Act.** Check the operator is running and reconciling the cluster, and that the maintenance windows are not too narrow. `kubectl -n $NS get secret $CLUSTER-cluster-ca-cert -o jsonpath='{.data.ca\.crt}' | base64 -d | openssl x509 -noout -enddate`.

### KafkaCertificateExpiryCritical

**Meaning.** Within `alerts.thresholds.certificateCriticalDays`. When it expires, brokers and clients stop trusting each other.

**Act.** Force the renewal now: annotate the CA secret with `strimzi.io/force-renew=true` (Strimzi rolls the cluster). Plan client trust-store updates for a replaced CA.
