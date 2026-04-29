# `cli/examples` — Kates Test Example Files

Ready-to-run YAML examples for every test type and disruption type.
Fields are taken directly from the Go CLI structs (`apply.go`, `resilience.go`).

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
| `perf-round-trip.yaml` | `ROUND_TRIP` | E2E latency, p99 < 50ms | linger=0, fetchMinBytes=1 |
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
| `consumerGroup` | string | Consumer group name |
| `duration` | int | Duration in **seconds** (ENDURANCE) |
| `acks` | string | `"0"`, `"1"`, or `"all"` |
| `batchSize` | int | Producer batch size (bytes) |
| `lingerMs` | int | Producer linger (ms) |
| `compressionType` | string | `none`, `gzip`, `snappy`, `lz4`, `zstd` |
| `partitions` | int | Partition count |
| `replicationFactor` | int | Replication factor |
| `minInsyncReplicas` | int | `min.insync.replicas` |
| `fetchMinBytes` | int | Consumer `fetch.min.bytes` |
| `fetchMaxWaitMs` | int | Consumer `fetch.max.wait.ms` |
| `enableIdempotence` | bool | Idempotent producer |
| `enableTransactions` | bool | Transactional EOS |
| `enableCrc` | bool | CRC payload verification |

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

Schema: `testRequest` + `chaosSpec` + `steadyStateSec` + `probes`.

| File | `disruptionType` | Fault | Target |
|---|---|---|---|
| `resilience-test.yaml` | `POD_KILL` | Hard crash, gracePeriod=0 | brokers-alpha |
| `resilience-pod-delete.yaml` | `POD_DELETE` | Graceful shutdown, 30s grace | brokers-alpha |
| `resilience-network-partition.yaml` | `NETWORK_PARTITION` | Zone isolation | brokers-sigma |
| `resilience-network-latency.yaml` | `NETWORK_LATENCY` | 200ms egress latency | brokers-alpha |
| `resilience-cpu-stress.yaml` | `CPU_STRESS` | 1 core @ 90% | brokers-gamma |
| `resilience-memory-stress.yaml` | `MEMORY_STRESS` | 500 MB native memory | brokers-sigma |
| `resilience-io-stress.yaml` | `IO_STRESS` | 80% disk saturation | brokers-alpha |
| `resilience-dns-error.yaml` | `DNS_ERROR` | CoreDNS failures | all brokers |
| `resilience-rolling-restart.yaml` | `ROLLING_RESTART` | Rolling upgrade, 30s delay | all brokers |
| `resilience-node-drain.yaml` | `NODE_DRAIN` | Node maintenance eviction | brokers-gamma |
| `resilience-leader-election.yaml` | `LEADER_ELECTION` | Force re-election all partitions | all brokers |
| `resilience-scale-down.yaml` | `SCALE_DOWN` | Pool contraction to 0 | brokers-sigma |

### `chaosSpec` field reference

| YAML key | Type | Notes |
|---|---|---|
| `experimentName` | string | Unique name for this experiment |
| `disruptionType` | string | See table above |
| `targetNamespace` | string | Kubernetes namespace |
| `targetLabel` | string | Pod selector label |
| `targetPod` | string | Specific pod name (optional) |
| `chaosDurationSec` | int | How long to run the fault |
| `delayBeforeSec` | int | Wait before injecting |
| `gracePeriodSec` | int | SIGTERM grace period (0 = SIGKILL) |
| `cpuCores` | int | Cores to hog (`CPU_STRESS`) |
| `memoryMb` | int | MB to consume (`MEMORY_STRESS`) |
| `fillPercentage` | int | Disk fill % (`IO_STRESS`) |
| `ioWorkers` | int | Parallel dd workers (`IO_STRESS`) |
| `networkLatencyMs` | int | Added latency ms (`NETWORK_LATENCY`) |
| `targetTopic` | string | Topic for `LEADER_ELECTION` |
| `targetPartition` | int | Partition index (-1 = all) |
| `envOverrides` | map | Extra env vars for the chaos runner |

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
