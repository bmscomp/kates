# Kafka Chaos & Load Testing

Stress-testing and chaos engineering the `krafter` Kafka cluster: single
faults through `kates resilience run`, and the multi-fault Game Day through
`scripts/test-chaos-gameday.sh`, which follows the deploy scripts' conventions
(`common.sh`, `test-common.sh`, `versions.env`).

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

## Single-fault runs

A baseline load run and the single-fault runs go through the Kates API, so the
Kates backend must be deployed (`kates deploy`) and reachable (`kates ports`).

| Run | Command | Fault |
|---|---|---|
| Baseline load, no chaos | `kates test create --type LOAD --wait` | None |
| Kill one broker while producers run | example 02 | `POD_DELETE` |
| Saturate broker CPU, watch ISR shrink | example 03 | `CPU_STRESS` |
| Saturate broker disk I/O | example 05 | `IO_STRESS` |
| Partition one broker, observe rebalance | example 07 | `NETWORK_PARTITION` |

The examples are the entries of `kates/docs/tutorials/resilience.json`, counted
from 01. Each holds the load, the fault, the probes and the recovery wait:

```bash
jq '.examples[1]' kates/docs/tutorials/resilience.json > broker-delete.json   # example 02
kates resilience run -f broker-delete.json
```

---

## Game Day script

Run from the **repo root**:

```bash
./scripts/test-chaos-gameday.sh
DRY_RUN=true ./scripts/test-chaos-gameday.sh   # print plan only
```

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

The Game Day script prints exact cleanup commands at the end. General cleanup:

```bash
# Delete the Game Day jobs
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
