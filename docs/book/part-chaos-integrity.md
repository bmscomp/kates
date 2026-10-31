# Part III — Chaos & Integrity

You run the payments platform, and Part II asks whether `krafter` can take 2,000 records per second of 1 KiB with `acks=all`. One target is left: `krafter` must lose no acknowledged record when one broker or one zone fails. This Part kills a broker, then a zone, while records flow, and then counts every acknowledged record.

Every experiment here breaks something on purpose. Write the hypothesis before you inject anything, keep the blast radius inside the guardrails that [Chaos Engineering in Practice](07-chaos-practice.md) describes, and have a LOAD baseline from [Part II — Performance Testing](part-performance-testing.md) at hand, so you can tell the fault's effect from the cluster's normal behavior. `make all` installs LitmusChaos with the rest of the lab, and five disruption types, `NODE_DRAIN` among them, run only through its experiments. The lab installs no experiment for `DISK_FILL` or `NETWORK_LATENCY`, so those two fail until you add them.

Read the chapters in order:

- [Chaos Engineering Theory](06-chaos-theory.md): what does healthy mean in numbers before you break anything, and what does Kafka do during a leader election, an ISR shrink and a consumer group rebalance? About 5 minutes.
- [Chaos Engineering in Practice](07-chaos-practice.md): should a fault run as a disruption plan or a resilience run, how far can it reach before the safety guard refuses it, and what SLA grade does it earn? About 20 minutes.
- [Data Integrity Verification](08-data-integrity.md): did every acknowledged record arrive, and when the verdict is DATA_LOSS, which records went missing? About 15 minutes.

Two tutorials run the same steps on the lab. [Tutorial 3: Chaos Engineering with Kates](../tutorials/03-chaos-engineering.md) runs a disruption plan, a playbook and a resilience run. [Tutorial 4: Data Integrity Under Fire](../tutorials/04-integrity-under-fire.md) kills a broker while an INTEGRITY run is producing, and reads the verdict.

## Choosing How to Inject a Fault

The chapters inject faults with three `kates` commands. Pick the one that answers what you want to learn:

| Command | What it runs | Reach for it when |
|----------------------------------------|--------------------------------|----------------------------|
| `kates disruption run --config <file>` | A disruption plan: your own steps, one fault each, within limits such as `maxAffectedBrokers` | You want to choose the faults, their order and the blast radius |
| `kates disruption playbook run <name>` | A playbook: a disruption plan that ships with Kates | You want a common failure, such as losing a zone, without writing the plan |
| `kates resilience run -f <file>` | A performance test, with one fault injected after a steady-state wait | You want the fault's cost to a workload, or an INTEGRITY run through it |

Only a resilience run carries a workload, so it's the one that can tell you whether records were lost. A Game Day is the session your team runs around these commands, and [The Game Day Methodology](06-chaos-theory.md#the-game-day-methodology) shows how to structure one.

## The Payments Question

Two disruption plans and two resilience files test the loss target. The plans kill a broker (`payments-broker-loss.json`) and every Kafka pod in zone `alpha` (`payments-zone-loss.json`), and `kates disruption run --config <plan> --fail-on-sla-breach` grades each against its `sla` block. A plan runs no workload of its own: it reads the brokers' metrics from Prometheus. So submit the Part II scenario file with `kates test apply -f payments-scenarios.yaml`, without `--wait`, and run the plan while that LOAD run produces.

A plan can't measure loss, so the verdicts come from the resilience files, `payments-integrity-broker.yaml` and `payments-integrity-zone.yaml`. Each runs an INTEGRITY test through one of the same two faults with `kates resilience run -f <file>`, which prints that run's ID as `Test Run` and can return before the run ends. Wait until `kates test get <id>` shows the run `DONE`, and read its `Lost`, `RPO` and `Verdict` there. The target holds when `kates resilience run` reported `Status COMPLETED` and the INTEGRITY run shows `Lost` 0 with a measured RPO. When `Lost` is 0, an RPO of `not measured` means the fault never reached the run.

Run the zone file only once the broker file's INTEGRITY run is `DONE`, because the two runs share a topic and a consumer group. The safety guard counts a resilience run's brokers but holds it to no `maxAffectedBrokers`, so preview the zone's pods first with `kates disruption playbook run az-failure --dry-run`.
