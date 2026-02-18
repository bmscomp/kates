# Troubleshooting

This document covers common issues, their root causes, and how to resolve them when running Kates.

## Kafka Connectivity

### "Kafka cluster is not reachable"

**Symptom:** `GET /api/health` returns `status: DOWN` and `message: "Kafka cluster is not reachable"`.

**Root cause:** The `KafkaAdminService` cannot connect to the bootstrap servers. This is typically one of:

1. **Wrong bootstrap address** — verify `KATES_KAFKA_BOOTSTRAP_SERVERS` in the ConfigMap matches the actual Kafka service name.
2. **DNS resolution failure** — the Kafka service may not be reachable from the Kates namespace. Try `kubectl exec -it deployment/kates -n kates -- nslookup krafter-kafka-bootstrap.kafka.svc`.
3. **Network policy blocking traffic** — check if a NetworkPolicy is restricting traffic between the kates and kafka namespaces.
4. **Kafka not ready** — if Kafka was just deployed, the brokers may not have finished their startup sequence. Wait 30-60 seconds and retry.

**Fix:**

```bash
kubectl get svc -n kafka
kubectl exec -it deployment/kates -n kates -- \
  curl -v telnet://krafter-kafka-bootstrap.kafka.svc:9092
```

---

### "UnknownHostException: postgres.kates.svc"

**Symptom:** Kates pod enters `CrashLoopBackOff` with a database connection error.

**Root cause:** The PostgreSQL service is not available in the expected namespace, or the JDBC URL is misconfigured.

**Fix:**

```bash
kubectl get svc -n kates | grep postgres
kubectl get pods -n kates | grep postgres
```

Verify that the `QUARKUS_DATASOURCE_JDBC_URL` environment variable matches the actual PostgreSQL service address. The default is `jdbc:postgresql://postgres.kates.svc:5432/kates`.

---

## Performance Tests

### "Topic creation failed"

**Symptom:** `POST /api/tests` returns `500 Internal Server Error` with `"error": "Topic creation failed"`.

**Root cause:** The Kafka AdminClient could not create the test topic. Common reasons:

1. **Insufficient replication factor** — you requested `replicationFactor: 3` but only 2 brokers are available.
2. **Topic already exists with different config** — the topic exists but has a different partition count or replication factor.
3. **ACL restrictions** — if Kafka has authorization enabled, the Kates service account may not have `CREATE` permission on topics.

**Fix:** Check the effective replication factor against your broker count:

```bash
kubectl exec -it krafter-kafka-0 -n kafka -- \
  kafka-topics.sh --bootstrap-server localhost:9092 --describe --topic <topic-name>
```

---

### "Backend not available: trogdor"

**Symptom:** `POST /api/tests` with `"backend": "trogdor"` returns an error about the backend not being available.

**Root cause:** The Trogdor Coordinator is not reachable at the configured URL.

**Fix:**

```bash
kubectl get pods -n kates | grep trogdor
kubectl logs deployment/trogdor-coordinator -n kates
kubectl exec -it deployment/kates -n kates -- \
  curl -s http://trogdor-coordinator.kates.svc:8889/coordinator/status
```

If Trogdor is not deployed, use the `native` backend instead. The native backend uses in-process virtual threads and does not require any external components.

---

### Test stuck in "RUNNING" status

**Symptom:** `GET /api/tests/{id}` continues to show `status: RUNNING` even after the expected duration has elapsed.

**Root cause:** The test may have completed but the status refresh failed, or the test hit an internal error that prevented completion.

**Fix:**

1. Check the Kates logs: `kubectl logs deployment/kates -n kates | grep <test-id>`
2. For Trogdor backend: check the coordinator logs: `kubectl logs deployment/trogdor-coordinator -n kates`
3. Force-stop and clean up: `curl -X DELETE http://localhost:8080/api/tests/<test-id>`

---

## Disruption Tests

### "Safety guard rejected the plan"

**Symptom:** `POST /api/disruptions` returns `422 Unprocessable Entity` with a message from the `DisruptionSafetyGuard`.

**Common reasons:**

1. **Blast radius exceeded** — the plan's `maxAffectedBrokers` is lower than the number of pods matched by the label selector. Either increase `maxAffectedBrokers` or narrow the label selector.

2. **RBAC permission missing** — Kates' service account doesn't have the required Kubernetes permissions. The error message will specify which permission is needed:

   ```
   "Missing permission: pods/delete in namespace kafka"
   "Missing permission: chaosengines/create in namespace kafka"
   ```

   Fix by updating the ClusterRole (see [Deployment Guide](deployment.md)).

3. **Target pods not found** — no pods match the label selector in the target namespace. Verify the labels: `kubectl get pods -n kafka --show-labels`.

---

### "Prometheus not available"

**Symptom:** Disruption report has no `prometheusBaseline` or `prometheusImpact` sections.

**Root cause:** `PrometheusMetricsCapture.isAvailable()` returned `false`. The disruption test continues without Prometheus snapshots — this is not a failure, just reduced observability.

**Fix:**

1. Verify Prometheus is running: `kubectl get pods -n monitoring | grep prometheus`
2. Verify the URL: `kates.prometheus.url` should point to the Prometheus server service
3. Test connectivity: `kubectl exec -it deployment/kates -n kates -- curl -s http://prometheus.monitoring.svc:9090/-/healthy`

---

### Litmus experiments not working

**Symptom:** Disruption tests with `NETWORK_PARTITION`, `CPU_STRESS`, or `DISK_FILL` fail with a `ChaosProvider` error.

**Root cause:** These disruption types require Litmus ChaosEngine CRDs, which are only available if Litmus is installed.

**Fix:**

1. Check if Litmus is installed: `kubectl get crd | grep litmuschaos`
2. If not installed, switch to disruption types that work with the Kubernetes backend: `POD_KILL`, `POD_DELETE`, `SCALE_DOWN`, `ROLLING_RESTART`.
3. If Litmus is installed, check that the Litmus operator is running: `kubectl get pods -n litmus`

---

## Configuration Issues

### ConfigMap changes not taking effect

**Symptom:** You updated `kates-config` ConfigMap but `GET /api/health` shows old values.

**Root cause:** ConfigMap changes require a pod restart. Kates reads environment variables at JVM startup; changes to the ConfigMap are not picked up automatically.

**Fix:**

```bash
kubectl rollout restart deployment/kates -n kates
kubectl rollout status deployment/kates -n kates
curl -s http://localhost:8080/api/health | jq '.tests.stress'
```

---

### Which backend should I use?

| Scenario | Best Backend | Reason |
|----------|-------------|--------|
| Quick local tests | `native` | No external dependencies required |
| Distributed load generation | `trogdor` | Multiple agents can generate load from different nodes |
| CI/CD automated tests | `native` | Simpler deployment, fewer moving parts |
| Production-scale testing | `trogdor` | Agents distributed across the cluster for realistic load distribution |
| Maximum throughput testing | `trogdor` | Agents can be scaled independently from Kates |
| Low-latency measurement | `native` | No REST overhead between Kates and the Kafka clients |

---

## Debugging Tips

### View effective configuration

```bash
curl -s http://localhost:8080/api/health | jq '.tests'
```

Shows the fully resolved per-type test configuration, reflecting all three tiers (ConfigMap, `application.properties`, Java defaults).

### View available playbooks

```bash
curl -s http://localhost:8080/api/disruptions/playbooks | jq '.[].name'
```

### Dry-run a disruption before executing

```bash
curl -X POST http://localhost:8080/api/disruptions?dryRun=true \
  -H 'Content-Type: application/json' \
  -d @disruption-plan.json | jq
```

Shows which pods would be affected, which broker is the leader, and whether RBAC permissions are sufficient — without injecting any faults.

### Watch disruption events in real-time

```bash
curl -N http://localhost:8080/api/disruptions/stream
```

Streams Server-Sent Events showing step progress, fault injection, ISR changes, and SLA grading results.

### Check Kafka cluster health

```bash
curl -s http://localhost:8080/api/cluster/info | jq
curl -s http://localhost:8080/api/cluster/topics | jq
```
