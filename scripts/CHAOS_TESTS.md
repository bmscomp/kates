# Kafka Chaos & Load Testing Scripts

Scripts for stress-testing and chaos engineering the `krafter` Kafka cluster.
All scripts live in `scripts/` alongside the existing deploy scripts and follow
the same conventions (`common.sh`, `test-common.sh`, `versions.env`).

## Prerequisites

```bash
make kafka           # Kafka cluster must be running
make chaos           # LitmusChaos + ChaosExperiments must be deployed
```

Verify:
```bash
kubectl get kafka krafter -n kafka                          # Ready
kubectl get chaosexperiment -n kafka                        # pod-delete, pod-cpu-hog, ...
kubectl get secret kates-backend -n kafka                   # SCRAM credentials
```

---

## Scripts

| Script | What it does | Key chaos | Duration |
|---|---|---|---|
| `test-perf-load.sh` | Baseline load — no chaos | None | ~5 min |
| `test-chaos-broker-delete.sh` | Kill one broker while producers run | `pod-delete` | ~3 min |
| `test-chaos-broker-cpu-hog.sh` | Saturate broker CPU, watch ISR shrink | `pod-cpu-hog` | ~5 min |
| `test-chaos-network-partition.sh` | Partition one broker zone, observe rebalance | `pod-network-partition` | ~4 min |
| `test-chaos-disk-io-stress.sh` | Saturate broker disk I/O, observe flush latency | `pod-io-stress` | ~4 min |
| `test-chaos-gameday.sh` | Full game day: 3-phase escalating chaos | All of the above | ~12 min |

---

## Running a Test

All scripts are self-contained. Run from the **repo root**:

```bash
./scripts/test-chaos-broker-delete.sh
```

Override defaults with environment variables:

```bash
CHAOS_DURATION=120 CHAOS_INTERVAL=30 ./scripts/test-chaos-broker-delete.sh
CPU_LOAD=95 CHAOS_DURATION=180       ./scripts/test-chaos-broker-cpu-hog.sh
CHAOS_DURATION=90                    ./scripts/test-chaos-network-partition.sh
IO_WORKERS=4 CHAOS_DURATION=90       ./scripts/test-chaos-disk-io-stress.sh
DRY_RUN=true                         ./scripts/test-chaos-gameday.sh   # print plan only
```

---

## How Each Test Works

### `test-perf-load.sh` — Baseline

```
Producers (3×) ──► kates-events, kates-results, kates-metrics
Consumers (2×) ──► kates-events
```

No chaos injected. Use this to establish p99 latency and throughput before
running chaos scenarios.

---

### `test-chaos-broker-delete.sh` — Pod Delete

```
T+00s  Producer starts (acks=all, idempotent, retries=MAX)
T+15s  ChaosEngine: pod-delete on brokers-alpha (every 20s for 60s)
T+75s  Chaos ends — measure latency spike and producer errors
```

**What to observe:**
- Producer latency spike when the leader is killed and re-elected
- `kubectl get pods -n kafka -l strimzi.io/pool-name=brokers-alpha -w` — pod restarts
- ChaosResult verdict (pass = 0 data loss confirmed by Litmus probes)

**Expected:** ~10–30s latency spike per delete, 0 producer errors.

---

### `test-chaos-broker-cpu-hog.sh` — CPU Saturation

```
T+00s  6 producers start (8 KB records, snappy, acks=all)
T+20s  ChaosEngine: pod-cpu-hog on brokers-gamma (90% × 1 core for 120s)
T+140s Chaos ends — ISR should re-expand fully
```

**What to observe (ISR printed every 5s):**
- `InSyncReplicas` count on `kates-results` drops when gamma falls behind
- Recovers to 3/3 within ~30s of chaos ending
- Grafana: `kafka_server_replicamanager_isrshrinks_total`

**Expected:** ISR shrink on gamma, no data loss, p99 < 5× baseline.

---

### `test-chaos-network-partition.sh` — Network Partition

```
T+00s  3 producers + 2 consumers start (acks=all, group: net-partition-cg)
T+15s  ChaosEngine: pod-network-partition on brokers-sigma (60s)
T+75s  Chaos ends — consumer group must rebalance and recover
```

**What to observe (consumer group printed every 10s):**
- `COORDINATOR` migrates from sigma partition to alpha/gamma
- Consumer group transitions through `Rebalancing` → `Stable`
- Producer retries absorb the sigma leader migration

**Expected:** Consumer rebalance < 30s, 0 producer errors, lag returns to 0.

---

### `test-chaos-disk-io-stress.sh` — Disk I/O Saturation

```
T+00s  4 producers start (16 KB records, no compression)
T+20s  ChaosEngine: pod-io-stress on brokers-alpha (80% disk util, 90s)
T+110s Chaos ends — check log flush latency recovery
```

**What to observe (leader state printed every 5s):**
- Leader elections on partitions where alpha was the leader
- `UnderReplicatedPartitions` count spikes then recovers
- Grafana: `kafka_log_flush_rate_and_time_ms_99thpercentile`

**Expected:** Leader migration off alpha, ISR recovers, 0 data loss.

---

### `test-chaos-gameday.sh` — Full Game Day

Three sequential chaos phases with 30s recovery windows between each:

```
T+00s   Load: 3 producers (events/results/metrics) + 1 consumer
T+30s   Phase 1: pod-delete on brokers-alpha       (60s)
T+120s  [30s recovery]
T+150s  Phase 2: cpu-hog on brokers-gamma          (90s)
T+270s  [30s recovery]
T+300s  Phase 3: network-partition on brokers-sigma (60s)
T+390s  Recovery validation: Kafka Ready, ChaosResults, consumer lag
```

**Pass criteria:**
- All 3 ChaosResult verdicts = `Pass`
- Kafka cluster `Ready=True` after each phase
- Producer jobs complete with 0 errors
- Consumer group lag = 0 within 2 min of Phase 3 ending

---

## Cleanup

Each script prints exact cleanup commands at the end. General cleanup:

```bash
# Delete all chaos test jobs
kubectl delete jobs -n kafka -l perf-test=chaos-broker-delete
kubectl delete jobs -n kafka -l perf-test=chaos-cpu-hog
kubectl delete jobs -n kafka -l perf-test=chaos-net-partition
kubectl delete jobs -n kafka -l perf-test=chaos-io-stress
kubectl delete jobs -n kafka -l perf-test=gameday

# Delete ChaosEngines
kubectl delete chaosengine --all -n kafka

# Delete ChaosResults
kubectl delete chaosresult --all -n kafka
```

---

## Observability

Open the Grafana dashboards while tests run:

```bash
make monitoring-ui           # Grafana at http://localhost:3000
```

Key panels to watch:

| Panel | Dashboard | What to expect during chaos |
|---|---|---|
| `kafka_server_replicamanager_isrshrinks_total` | Broker | Increments on ISR shrink |
| `kafka_server_replicamanager_isrexpands_total` | Broker | Increments on recovery |
| `kafka_network_request_totaltime_99thpercentile` | Broker | Latency spike then recovery |
| `kafka_controller_active_controller_count` | KRaft | Must stay at 1 |
| `kafka_server_replica_manager_under_replicated_partitions` | Broker | Spikes then returns to 0 |

---

## Adding a New Chaos Test

1. Copy the closest existing script as a template
2. Source `common.sh`, `versions.env`, `test-common.sh` at the top
3. Use `cleanup_previous_jobs`, `wait_for_jobs`, `print_job_results`, `show_cleanup_hint`
4. Write the `ChaosEngine` inline with `cat <<EOF | kubectl apply -f -`
5. Name the script `test-chaos-<fault-type>.sh`
