# `cli/examples` — Kates Test Example Files

Ready-to-run YAML examples for every test type and disruption type supported
by the Kates API. Each file maps directly to the Java domain model
(`TestSpec`, `FaultSpec`, `SlaDefinition`, `TestScenario`, `ScenarioPhase`)
so the fields are authoritative — not illustrative.

```
kates test run     -f cli/examples/perf-*.yaml
kates resilience run -f cli/examples/resilience-*.yaml
```

---

## Performance Tests

| File | `type` | What it tests | Key settings |
|---|---|---|---|
| `perf-load.yaml` | `LOAD` | Baseline throughput at fixed rate | 3P/2C, lz4, acks=all |
| `perf-stress.yaml` | `STRESS` | Saturation curve (5k → unlimited) | 6-phase scenario, rampSteps |
| `perf-spike.yaml` | `SPIKE` | Burst after idle | idle → unlimited → cooldown |
| `perf-endurance.yaml` | `ENDURANCE` | 10-min soak | 6k msg/s, 3P/3C |
| `perf-volume.yaml` | `VOLUME` | 64 KB large payloads | 1 MB batch, snappy |
| `perf-capacity.yaml` | `CAPACITY` | Max sustainable throughput | 6P, 12 partitions, ramp |
| `perf-round-trip.yaml` | `ROUND_TRIP` | End-to-end latency | linger=0, fetchMinBytes=1 |
| `perf-integrity.yaml` | `INTEGRITY` | Exactly-once delivery | transactions=true, CRC=true |

### Choosing a Performance Test

```
                 ┌─────────────────────────────────────────────────────┐
                 │   What question are you answering?                  │
                 └───────────────────┬─────────────────────────────────┘
                                     │
          ┌──────────────────────────┼──────────────────────────┐
          │                          │                          │
    "How fast can                "How long does           "Is it correct?"
    we go?"                      it sustain?"
          │                          │                          │
    ┌─────┴──────┐           ┌───────┴──────┐           ┌──────┴──────┐
    │            │           │              │           │             │
  LOAD        STRESS      ENDURANCE      VOLUME      INTEGRITY   ROUND_TRIP
  (known      (find       (10+ min       (large      (exactly-   (latency
  target)     ceiling)    soak)          records)    once)       focus)
```

---

## Resilience Tests

One file per `DisruptionType`. Each combines:
- A `testRequest` (running load that must survive the fault)
- A `chaosSpec` (the fault to inject)
- `steadyStateSec` warmup before chaos
- `maxRecoveryWaitSec` to wait for ISR/cluster recovery
- `probes` evaluated continuously during chaos

| File | `disruptionType` | Fault simulated | Target pool |
|---|---|---|---|
| `resilience-test.yaml` | `POD_KILL` | Hard crash (SIGKILL, gracePeriod=0) | brokers-alpha |
| `resilience-pod-delete.yaml` | `POD_DELETE` | Graceful shutdown (SIGTERM, 30s grace) | brokers-alpha |
| `resilience-network-partition.yaml` | `NETWORK_PARTITION` | Zone isolation via NetworkPolicy | brokers-sigma |
| `resilience-network-latency.yaml` | `NETWORK_LATENCY` | 200ms egress latency via tc netem | brokers-alpha |
| `resilience-cpu-stress.yaml` | `CPU_STRESS` | 1 core @ 90%, ISR shrink/expand | brokers-gamma |
| `resilience-memory-stress.yaml` | `MEMORY_STRESS` | 500 MB native memory consumption | brokers-sigma |
| `resilience-io-stress.yaml` | `IO_STRESS` | 80% disk saturation, 2 dd workers | brokers-alpha |
| `resilience-dns-error.yaml` | `DNS_ERROR` | CoreDNS resolution failures | all brokers |
| `resilience-rolling-restart.yaml` | `ROLLING_RESTART` | Rolling upgrade simulation, 30s delay | all brokers |
| `resilience-node-drain.yaml` | `NODE_DRAIN` | Kubernetes node maintenance eviction | brokers-gamma |
| `resilience-leader-election.yaml` | `LEADER_ELECTION` | Forced re-election on all partitions | all brokers |
| `resilience-scale-down.yaml` | `SCALE_DOWN` | Emergency pool contraction to 0 | brokers-sigma |

### Probe Reference

All probes map to the `ProbeSpec` record and `KafkaProbes.java` factory:

| Probe name | Mode | What it checks |
|---|---|---|
| `isr-health-check` | Continuous | Under-replicated partition count ≤ threshold |
| `min-isr-check` | Edge | Unavailable partitions = 0 |
| `cluster-ready` | Edge | Kafka CR `Ready=True` (k8sProbe) |
| `producer-throughput` | Continuous | kates-probe-topic write succeeds (> 0 msg/s) |
| `consumer-lag` | Continuous | Consumer group lag below threshold |
| `partition-availability` | Continuous | Unavailable partitions = 0 |

---

## SLA Fields Reference

All `sla:` blocks map to `SlaDefinition.java`:

| Field | Type | Meaning |
|---|---|---|
| `maxP99LatencyMs` | Double | p99 produce latency ceiling |
| `maxP999LatencyMs` | Double | p99.9 produce latency ceiling |
| `maxAvgLatencyMs` | Double | average produce latency ceiling |
| `minThroughputRecPerSec` | Double | minimum required throughput |
| `maxErrorRate` | Double | max producer error rate (%) |
| `minRecordsProcessed` | Long | minimum records consumed |
| `maxDataLossPercent` | Double | maximum tolerated data loss (0.0 = zero-loss) |
| `maxRtoMs` | Long | max recovery time objective (ms) |
| `maxRpoMs` | Long | max recovery point objective (ms) |

---

## Running Examples

```bash
# Start kates locally
make kates-ui                        # http://localhost:8080

# Performance tests
kates test run -f cli/examples/perf-load.yaml
kates test run -f cli/examples/perf-stress.yaml
kates test list

# Resilience tests
kates resilience run -f cli/examples/resilience-cpu-stress.yaml
kates resilience run -f cli/examples/resilience-test.yaml

# Check a report
kates test report <run-id>
kates advisor <run-id>
```

---

## Open Questions for Review

> [!IMPORTANT]
> **Please review the following decisions before merging:**

1. **`resilience-test.yaml` is the renamed original** — the old file had
   `numProducers`/`numRecords` field names that don't match `TestSpec`
   (`numRecords` is correct but `numProducers` maps to `spec.numProducers`).
   Confirm the CLI YAML → JSON field mapping before running.

2. **Probe `targetBrokerId` vs `targetLabel`** — `LEADER_ELECTION` and
   `SCALE_DOWN` may require `targetBrokerId` rather than `targetLabel` to
   precisely target a single pod. Verify with the `LitmusChaosProvider`
   implementation.

3. **`resilience-test.yaml` overwrites the original stub** — the original file
   had no `probes:` block and used `targetLabel: strimzi.io/component-type=kafka`
   which doesn't match any Strimzi-managed label. Updated to
   `strimzi.io/pool-name=brokers-alpha`. Confirm this is the right label for
   the `POD_KILL` target scope.

4. **`DISK_FILL` DisruptionType** — present in `DisruptionType.java` but no
   example file was created (it overlaps heavily with `IO_STRESS`). Should a
   dedicated `resilience-disk-fill.yaml` be added, or is `io-stress` sufficient?

5. **`TUNE_*` TestTypes** — `TUNE_REPLICATION`, `TUNE_ACKS`, `TUNE_BATCHING`,
   `TUNE_COMPRESSION`, `TUNE_PARTITIONS` are not yet covered by example files.
   Should tuning examples go in this same directory or in a separate
   `cli/examples/tuning/` subdirectory?
