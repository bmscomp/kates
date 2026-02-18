# Troubleshooting

Debugging distributed systems is hard. When a Kates test fails or produces unexpected results, the cause could be anywhere: Kafka configuration, Kubernetes networking, Kates settings, resource limits, or timing. This chapter is organized around the symptoms you observe, not the subsystem that is broken, because when something goes wrong, you usually know what happened but not why.

For each issue, you will find the symptom (what you see), the root cause (why it happens), the fix (how to resolve it), and — where relevant — the theory behind the problem, so you understand it well enough to prevent it next time.

## Kafka Connectivity Issues

### "Kafka cluster is not reachable"

**Symptom:** `GET /api/health` returns `status: DOWN` and `message: Kafka cluster is not reachable`, or test submissions fail with connection errors.

**Root cause:** Kates connects to Kafka using the `kafka.bootstrap.servers` address. If this address is wrong, if the Kafka cluster is not running, or if DNS resolution fails within the Kubernetes cluster, Kates cannot establish a connection.

This is the most common issue on initial setup. The bootstrap server address must be the Kubernetes service name that resolves to the Kafka broker pods. In a Strimzi deployment, this is typically `{cluster-name}-kafka-bootstrap.{namespace}.svc:9092`.

**Fix:** Verify the bootstrap server is reachable from the Kates pod:

```bash
kubectl exec -it deploy/kates -- nslookup krafter-kafka-bootstrap.kafka.svc
kubectl exec -it deploy/kates -- nc -zv krafter-kafka-bootstrap.kafka.svc 9092
```

If DNS resolution fails, check that the Kafka namespace exists and the Kafka cluster is deployed. If TCP connection fails (DNS works but port 9092 is unreachable), check whether a NetworkPolicy is blocking cross-namespace traffic:

```bash
kubectl get networkpolicy -n kafka
```

If NetworkPolicies are in place, you need to allow ingress from the Kates namespace to port 9092 on the Kafka pods.

**Configuration:** Set the correct bootstrap address:

```properties
kafka.bootstrap.servers=krafter-kafka-bootstrap.kafka.svc:9092
```

### Intermittent Connection Timeouts

**Symptom:** Tests occasionally fail with `TimeoutException: Failed to update metadata` or `NetworkException: The server disconnected`. The health endpoint shows `status: UP` most of the time.

**Root cause:** This is almost always a resource exhaustion issue. If the Kafka brokers are under heavy load (from Kates tests or other workloads), they may be slow to respond to metadata requests. Alternatively, the Kates pod's CPU or memory limits may be too tight, causing the JVM to pause for garbage collection during critical timeout windows.

The Kafka client library uses aggressive timeout defaults (30 seconds for `request.timeout.ms`, 60 seconds for `max.block.ms`). If a metadata update takes longer than these timeouts — due to broker overload, GC pause, or network congestion — the client throws a timeout exception.

**Fix:** First, check whether the issue is on the Kates side or the Kafka side:

```bash
kubectl top pod -n kates
kubectl top pod -n kafka
```

If Kafka brokers are using 90%+ CPU, they are overloaded — reduce the throughput in your test or add brokers. If the Kates pod is memory-constrained, increase its memory limit:

```yaml
resources:
  limits:
    memory: "1Gi"
  requests:
    memory: "512Mi"
```

## Performance Test Issues

### Tests Stuck in RUNNING State

**Symptom:** A test was submitted and shows `status: RUNNING`, but it never transitions to `DONE`. The test has been running far longer than expected.

**Root cause:** This typically indicates one of three problems:

1. **The topic cannot be created.** If the test topic already exists with different settings (different partition count or replication factor), topic creation fails silently and the test hangs waiting for data that never arrives. This is a common issue when re-running tests with different configurations.

2. **Producer throughput is unlimited and the cluster cannot keep up.** If `throughput: -1` (unlimited), the producer sends as fast as possible. If the cluster's write capacity is lower than the producer's send rate, the producer's internal buffer fills up, triggering backpressure. This can cause the producer to stall indefinitely.

3. **Consumer cannot consume from the topic.** If partitions are not assigned to consumers (perhaps because the consumer group already has active members from a previous test run), the consumer waits indefinitely for partition assignment.

**Fix:** Check the test run status for error details:

```bash
curl -s http://localhost:8080/api/tests/{runId} | jq '.results[].error'
```

If the issue is a pre-existing topic, either delete the topic and re-run, or configure the test to use a unique topic name:

```bash
kubectl exec -it krafter-kafka-0 -n kafka -- kafka-topics.sh \
  --bootstrap-server localhost:9092 --delete --topic test-topic
```

### Throughput is Much Lower Than Expected

**Symptom:** You configured `throughput: 50000` but the results show only 20,000-30,000 rec/s.

**Root cause:** The configured throughput is a *target*, not a guarantee. Actual throughput depends on many factors:

- **Network bandwidth** between the Kates pod and the Kafka brokers. In a Kubernetes cluster, this is typically 1-10 Gbps, shared with other workloads. A 1024-byte record at 50,000 rec/s requires ~50 MB/s sustained network bandwidth.
- **Broker disk I/O.** Each message must be written to the leader's log segment and then replicated to followers. If the brokers' disks are slow (spinning disks, throttled cloud volumes), write latency limits throughput.
- **`acks` setting.** With `acks=all`, the producer waits for all ISR members to acknowledge the write before considering it complete. This adds replication latency to every produce call. With `acks=1`, throughput is higher but durability is weaker.
- **`batch.size` and `linger.ms`.** These control how the producer batches messages. Small batches or zero `linger.ms` mean more network round-trips and lower throughput. Larger batches are more efficient but add latency.

**Fix:** This is usually not a bug — it is the cluster's actual capacity. To increase throughput:
- Increase `batch.size` (default 16KB, try 64KB or 128KB)
- Set `linger.ms` to 5-10 (batch for a few milliseconds before sending)
- Use `compression.type=lz4` to reduce network bandwidth usage
- Add more partitions to parallelize writes across more brokers
- Use `acks=1` if you can tolerate some durability risk

## Disruption Test Issues

### "Would affect N brokers, limit is M"

**Symptom:** Disruption test is rejected with a safety guard error about exceeding the broker limit.

**Root cause:** The `maxAffectedBrokers` in your disruption plan is too low for the number of pods that match your label selector. This is a safety feature — the guard is preventing you from accidentally taking down more brokers than intended.

This often happens when using a broad label selector like `strimzi.io/component-type=kafka` (which matches all Kafka brokers) with `maxAffectedBrokers: 1`. The label selector matches all brokers, triggering the safety check.

**Fix:** Either increase `maxAffectedBrokers` to the number of brokers you actually intend to disrupt, or narrow your label selector to target a specific broker:

```json
{
  "targetPod": "krafter-kafka-0"
}
```

Or use leader-aware targeting, which always resolves to exactly one pod:

```json
{
  "targetTopic": "my-topic",
  "targetPartition": 0
}
```

### Disruption Test Hangs During Fault Injection

**Symptom:** The disruption test starts but never completes. The orchestrator appears to be waiting for the chaos provider.

**Root cause:** This typically indicates a problem with the chaos provider backend:

1. **Litmus is not installed.** If the `HybridChaosProvider` selected the Litmus path for a complex fault (network partition, CPU stress), but Litmus CRDs are not installed in the cluster, the CRD creation fails and the provider hangs waiting for a response.

2. **RBAC permissions are missing.** Even though the safety guard checks permissions, there are edge cases where the permission check passes but the actual operation fails (e.g., cluster-scoped vs. namespace-scoped permissions).

3. **The target pod does not exist.** If leader-aware targeting resolved to a broker ID that maps to a pod name that does not exist (e.g., the cluster has 3 brokers but the naming convention produces `krafter-kafka-5`), the chaos provider waits for a response from a nonexistent pod.

**Fix:** Check the Kates logs for the chaos provider output:

```bash
kubectl logs deploy/kates | grep -i chaos
```

If Litmus is not installed, either install it or explicitly set the backend:

```properties
kates.chaos.backend=kubernetes
```

This forces all faults to use the `KubernetesChaosProvider`, which does not require Litmus. Note that this limits you to simple fault types (pod kill, pod delete, scale down, rolling restart).

### Auto-Rollback Did Not Work

**Symptom:** A disruption test failed, but the fault is still active — the network partition is still in place, or the CPU stress is still running.

**Root cause:** Auto-rollback works by calling `ChaosProvider.cleanup()` when a step fails. For `LitmusChaosProvider`, this deletes the `ChaosEngine` CRD. If the CRD deletion fails (RBAC permissions, API server timeout), the fault persists.

For `KubernetesChaosProvider` with `POD_KILL` or `POD_DELETE`, rollback is a no-op — Kubernetes automatically restarts the pod. But for `NETWORK_PARTITION` or `CPU_STRESS`, rollback requires active cleanup.

**Fix:** Manually clean up the fault:

```bash
kubectl delete chaosengine --all -n kafka
kubectl delete pod -l "app.kubernetes.io/managed-by=litmus" -n kafka
```

If `CPU_STRESS` was injected via `stress-ng`, the cleanup pod should have killed the process. If it did not, restart the affected broker pod:

```bash
kubectl delete pod krafter-kafka-0 -n kafka
```

## Configuration Issues

### Test Type Defaults Not Taking Effect

**Symptom:** You configured custom defaults in `application.properties` (e.g., `kates.test-defaults.load.partitions=12`) but tests still use the old defaults.

**Root cause:** The `TestTypeDefaults` class has a specific merge order: explicit values in the request override configured defaults, which override hardcoded defaults. If your request includes a value for the field you are trying to default, the configured default is overridden.

**Fix:** Check which values your request is actually sending:

```bash
curl -s http://localhost:8080/api/health | jq '.tests'
```

This shows the resolved defaults for each test type. If the values match your configuration, the defaults are correct — the request is overriding them. Remove the field from your request body to use the configured default.

### "Backend 'trogdor' is not available"

**Symptom:** Tests submitted with `"backend": "trogdor"` fail with "Backend 'trogdor' is not available."

**Root cause:** The Trogdor backend requires a running Trogdor coordinator service. If the Trogdor coordinator is not deployed or its URL is misconfigured, the backend probe fails.

**Fix:** Either deploy Trogdor or switch to the native backend:

```json
{
  "type": "LOAD",
  "backend": "native",
  "spec": { "..." }
}
```

The native backend runs benchmarks in-process using the Kafka client library directly. It supports all the same test types as Trogdor, and for most use cases, it is the better choice — no additional infrastructure required.

## General Debugging Methodology

When something goes wrong and the symptoms do not match any of the issues above, here is a systematic approach:

### 1. Check the Health Endpoint

```bash
curl -s http://localhost:8080/api/health | jq
```

This tells you whether Kates can reach Kafka, which backends are available, and what defaults are configured. If `kafka.status` is `DOWN`, start there — nothing else will work until Kafka connectivity is restored.

### 2. Check the Logs

```bash
kubectl logs deploy/kates --tail=200
```

Kates uses structured logging with log levels. Look for `ERROR` and `WARN` entries first. The most informative log messages come from `TestOrchestrator`, `DisruptionOrchestrator`, and the chaos providers.

### 3. Check Kafka Cluster State

```bash
kubectl exec -it krafter-kafka-0 -n kafka -- kafka-topics.sh \
  --bootstrap-server localhost:9092 --describe --topic test-topic

kubectl exec -it krafter-kafka-0 -n kafka -- kafka-consumer-groups.sh \
  --bootstrap-server localhost:9092 --describe --group test-group
```

These commands show the partition layout, ISR state, and consumer group status — the most common sources of test issues.

### 4. Check Kubernetes Resources

```bash
kubectl get pods -n kafka
kubectl get pods -n kates
kubectl describe pod krafter-kafka-0 -n kafka
```

Look for pods in `CrashLoopBackOff`, `OOMKilled`, or `Pending` state. These indicate infrastructure problems that affect test results.

### 5. Check Resource Consumption

```bash
kubectl top pod -n kafka
kubectl top pod -n kates
```

High CPU or memory usage can cause timeouts, GC pauses, and degraded performance that masquerade as test failures.

### 6. Still Stuck?

If none of the above reveals the problem, try running the simplest possible test to isolate the issue:

```bash
curl -X POST http://localhost:8080/api/tests \
  -H 'Content-Type: application/json' \
  -d '{"type":"LOAD","spec":{"numRecords":100,"throughput":10}}'
```

If this tiny test works, the problem is likely in your test configuration or cluster capacity. If it fails, the problem is in Kates' connectivity or deployment.
