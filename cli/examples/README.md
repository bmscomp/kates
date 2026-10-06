# `cli/examples` — Kates Test Example Files

Ready-to-run YAML examples for every test type and disruption type.
A performance file's fields are the ones `kates test apply` reads (`apply.go`).
A resilience file is the body of `POST /api/resilience`, sent as written.

---

## Commands

```bash
# Performance tests  →  kates test apply
kates test apply -f cli/examples/perf-load.yaml
kates test apply -f cli/examples/perf-load.yaml --wait   # block until done

# Resilience tests  →  kates resilience run
kates resilience run -f cli/examples/resilience-test.yaml
kates resilience run -f cli/examples/resilience-test.yaml --dry-run
```

> **Note:** `kates test run` does not exist. Use `kates test apply` for file-based
> execution and `kates test create` for flag-based one-liners.

---

## Performance Tests (`kates test apply -f`)

Schema: `scenarios: []` with `spec:` (flat map) and `validate:` block.

| File | `type` | What it tests | Key `spec` settings |
|---|---|---|---|
| `perf-load.yaml` | `LOAD` | Baseline at 10k msg/s | 3P/2C, lz4, acks=all |
| `perf-stress.yaml` | `STRESS` | Saturation curve in 4 steps | 5k → 15k → 30k → unlimited |
| `perf-spike.yaml` | `SPIKE` | Burst after idle | idle → unlimited → recovery |
| `perf-endurance.yaml` | `ENDURANCE` | 10-min soak at 6k msg/s | 3P/3C, duration=600 |
| `perf-volume.yaml` | `VOLUME` | 64 KB payloads | 1 MB batch, snappy |
| `perf-capacity.yaml` | `CAPACITY` | Max throughput discovery | 6P, 12 partitions, 3 steps |
| `perf-round-trip.yaml` | `ROUND_TRIP` | E2E latency, p99 < 50ms | linger=0, no compression |
| `perf-integrity.yaml` | `INTEGRITY` | Exactly-once + CRC | transactions=true, enableCrc=true |

### `spec` field reference

| YAML key | Type | Notes |
|---|---|---|
| `topic` | string | Must exist or be auto-created by the test |
| `numRecords` | int | Total records to produce |
| `recordSize` | int | Bytes per record |
| `throughput` | int | Max msg/s; `-1` = unlimited |
| `numProducers` | int | Parallel producer threads |
| `numConsumers` | int | Parallel consumer threads |
| `consumerGroup` | string | Consumer group name; `LOAD`, `ENDURANCE` and `INTEGRITY` only, the backend refuses it for any other type |
| `duration` | int | Duration in **seconds** (ENDURANCE) |
| `acks` | string | `"0"`, `"1"`, or `"all"` |
| `batchSize` | int | Producer batch size (bytes) |
| `lingerMs` | int | Producer linger (ms) |
| `compressionType` | string | `none`, `gzip`, `snappy`, `lz4`, `zstd` |
| `partitions` | int | Partition count |
| `replicationFactor` | int | Replication factor |
| `minInsyncReplicas` | int | `min.insync.replicas` |
| `fetchMinBytes` | int | Consumer `fetch.min.bytes`; same three types |
| `fetchMaxWaitMs` | int | Consumer `fetch.max.wait.ms`; same three types |
| `enableIdempotence` | bool | Idempotent producer |
| `enableTransactions` | bool | Transactional EOS |
| `enableCrc` | bool | CRC payload verification; `INTEGRITY` only |

### `validate` field reference

| YAML key | Type | Fails when… |
|---|---|---|
| `maxP99LatencyMs` | float | p99 > threshold |
| `maxAvgLatencyMs` | float | avg > threshold |
| `minThroughputRecPerSec` | float | throughput < threshold |
| `maxErrorRate` | float | error rate > threshold (%) |
| `maxDataLossPercent` | float | data loss > threshold |
| `maxRtoMs` | float | recovery time > threshold |
| `maxRpoMs` | float | recovery point > threshold |
| `maxOutOfOrder` | int | out-of-order records > threshold |
| `maxCrcFailures` | int | CRC failures > threshold |

---

## Resilience Tests (`kates resilience run -f`)

Schema: `testRequest` + `chaosSpec` + `steadyStateSec` (default 30) +
`maxRecoveryWaitSec` (default 120) + `probes`.

The file is the body of `POST /api/resilience`, and `kates resilience run` sends
it as written (`--dry-run` prints it). So its keys are the Kates API's field
names: the API ignores a field it doesn't have, and gives each field the file
leaves out its default. `testRequest` is a `POST /api/tests` body: a LOAD or
ENDURANCE run starts one producer and one consumer whatever `numProducers` and
`numConsumers` say, and only STRESS and CAPACITY start one producer per
`numProducers`. `ResilienceExamplesTest` (`kates/src/test`) fails an example
that sets a field its run doesn't read, or that the API would refuse.

| File | `disruptionType` | Fault | Target |
|---|---|---|---|
| `resilience-test.yaml` | `POD_KILL` | Hard crash, no grace period | brokers-alpha |
| `resilience-pod-delete.yaml` | `POD_DELETE` | Graceful shutdown, 30s grace (forced on `litmus-crd`) | brokers-gamma |
| `resilience-network-partition.yaml` | `NETWORK_PARTITION` | Zone isolation | brokers-sigma |
| `resilience-network-latency.yaml` | `NETWORK_LATENCY` | 200ms egress latency (needs `pod-network-latency`) | brokers-alpha |
| `resilience-cpu-stress.yaml` | `CPU_STRESS` | 1 core | brokers-gamma |
| `resilience-memory-stress.yaml` | `MEMORY_STRESS` | 500 MB native memory | brokers-sigma |
| `resilience-io-stress.yaml` | `IO_STRESS` | 80% disk saturation | brokers-alpha |
| `resilience-dns-error.yaml` | `DNS_ERROR` | CoreDNS failures | one broker, at random |
| `resilience-rolling-restart.yaml` | `ROLLING_RESTART` | Strimzi rolling update, one broker at a time | all brokers |
| `resilience-node-drain.yaml` | `NODE_DRAIN` | Node maintenance eviction | node gamma (`TARGET_NODE`) |
| `resilience-leader-election.yaml` | `LEADER_ELECTION` | Force-delete a broker; its partitions elect new leaders | one broker, at random |
| `resilience-scale-down.yaml` | `SCALE_DOWN` | Pool contraction to 0 | brokers-sigma |

### `chaosSpec` field reference

| YAML key | Type | Notes |
|---|---|---|
| `experimentName` | string | Unique name for this experiment |
| `disruptionType` | string | See table above |
| `targetNamespace` | string | Kubernetes namespace |
| `targetLabel` | string | Pod label selector. A pod fault hits one pod it matches, at random, unless `targetPod`, `targetAll` or `targetBrokerId` picks. `strimzi.io/cluster=krafter` alone matches the KRaft controllers too: add `strimzi.io/broker-role=true` to hit brokers only |
| `targetAll` | bool | Hit every pod `targetLabel` matches |
| `targetBrokerId` | int | Hit this broker, by node ID, among the brokers `targetLabel` matches |
| `targetPod` | string | Specific pod name (optional) |
| `chaosDurationSec` | int | How long to run the fault |
| `delayBeforeSec` | int | Wait before injecting; only the `kubernetes` chaos provider waits it |
| `gracePeriodSec` | int | Grace period of a `POD_DELETE` on the `kubernetes` chaos provider; `litmus-crd` forces every pod deletion |
| `cpuCores` | int | Cores to hog (`CPU_STRESS`) |
| `memoryMb` | int | MB to consume (`MEMORY_STRESS`) |
| `fillPercentage` | int | Disk fill % (`IO_STRESS`) |
| `ioWorkers` | int | Parallel I/O workers (`IO_STRESS`) |
| `networkLatencyMs` | int | Added latency ms (`NETWORK_LATENCY`) |
| `targetTopic` | string | A disruption plan aims a fault at the leader of this topic's `targetPartition`. A resilience run doesn't, but a `DNS_ERROR` on `litmus-crd` takes it as `TARGET_HOSTNAMES` |
| `targetPartition` | int | Partition of `targetTopic`, in a disruption plan only |
| `envOverrides` | map | Env vars for the Litmus experiment (`litmus-crd` only). `NODE_DRAIN` needs `TARGET_NODE`, the node to drain: Kates sets none |

### `probes` field reference

Every probe runs before the fault and after it, until all pass or
`maxRecoveryWaitSec` is up; a `Continuous` one runs while the fault lasts too.

| YAML key | Type | Notes |
|---|---|---|
| `name` | string | Probe name |
| `type` | string | `kafkaProbe` runs the check `command` names, over the Kates API's own Kafka connection. A `k8sProbe` whose `command` names `kafka` and `Ready` prints the Kafka CR's readiness, `Ready=True` when it is ready. `cmdProbe` (the default) runs `command` with `sh -c` in the first Ready broker pod by name, which needs `get` and `create` on `pods/exec` in the target namespace: the kates chart grants neither. Any other type runs as a `cmdProbe` |
| `mode` | string | `Edge` (the default) or `Continuous` |
| `command` | string | A `kafkaProbe`'s check: `under-replicated-partitions`, `unavailable-partitions`, `produce <topic>` (prints the acknowledged records per second; the platform profile creates `kates-probe-topic`) or `consumer-lag [<group>]`. A `cmdProbe`'s shell command, or the `k8sProbe` query |
| `expectedOutput` | string | What `comparator` compares the output with; default `""` |
| `comparator` | string | `equal`, `contains` (the default), `notContains`, or `==`, `!=`, `>=`, `<=`, `>`, `<`, which compare numbers. The probe fails when its comparator is none of these, when its check or command fails or runs out of time, and when a numeric comparator meets output that is not one number |
| `intervalSec` | int | Seconds between `Continuous` runs: the first `Continuous` probe's paces them all; default 10 |
| `timeoutSec` | int | Seconds to wait for `command`; default 30 |

---

## Quick Reference

```bash
# List available test types
kates test types

# Dry-run a resilience config (no execution)
kates resilience run -f cli/examples/resilience-cpu-stress.yaml --dry-run

# Run with output format
kates test apply -f cli/examples/perf-load.yaml --wait -o json | jq '.runId'

# Watch a running test
kates test watch <run-id>

# Get report + advisor recommendations
kates test report <run-id>
kates advisor <run-id>
```
