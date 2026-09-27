# Part III — Chaos & Integrity

What happens to the `krafter` Kafka cluster when a broker dies, the network splits or a whole zone goes dark, and did any acknowledged record go missing? This Part answers both questions. It turns "the cluster is resilient" into a hypothesis you can test, runs the experiment, and then checks every record.

Every experiment here breaks something on purpose, so two rules come first: write the hypothesis before you inject anything, and keep the blast radius small with the guardrails that Chaos Engineering in Practice describes. Have a LOAD baseline from [Part II — Performance Testing](part-performance-testing.md) at hand, so that you can tell the fault's effect from the cluster's normal behavior. Some disruption types run only on LitmusChaos, which `make all` deploys with the rest of the stack; Chaos Engineering in Practice says which.

## What You'll Have at the End

You'll have a steady-state hypothesis written down with pass and fail criteria, and a disruption run whose report shows how the cluster recovered. You'll also have an integrity verdict for a run that lost a broker partway through, which tells you whether every acknowledged record arrived.

## The Chapters

The chapters go from the idea, to the tooling, to the proof that nothing was lost:

- [Chaos Engineering Theory](06-chaos-theory.md): how do you say what healthy means before you break anything, and how does Kafka behave when it breaks? About 5 minutes.
- [Chaos Engineering in Practice](07-chaos-practice.md): how does Kates inject a fault, keep it inside a safe blast radius, and grade the result? About 20 minutes.
- [Data Integrity Verification](08-data-integrity.md): did every acknowledged record arrive, and if not, which ones were lost? About 15 minutes.

## Choosing How to Inject a Fault

The chapters inject faults with three `kates` commands, and the one you reach for depends on what you want to learn:

| Command | What it runs | Reach for it when |
|----------------------------------------|--------------------------------|----------------------------|
| `kates disruption run --config <file>` | A disruption plan: your own steps, one fault each, within limits such as `maxAffectedBrokers` | You want to choose the faults, their order and the blast radius |
| `kates disruption playbook run <name>` | A playbook: a disruption plan that ships with Kates | You want a common failure, such as losing a zone, without writing the plan |
| `kates resilience run -f <file>` | A performance test, with one fault injected after a steady-state wait | You want the fault's cost to a workload, or an INTEGRITY run through it |

A Game Day is the session your team runs around these commands, from hypothesis to follow-up, and Chaos Engineering Theory shows how to structure one.

## The Payments Question

This Part checks the loss target: `krafter` loses no acknowledged record when one broker or one zone fails. Two disruption plans kill a broker (`payments-broker-loss.json`) and every Kafka pod in zone `alpha` (`payments-zone-loss.json`), and `kates disruption run --config <plan> --fail-on-sla-breach` grades each against its `sla` block. A plan runs no workload of its own: it reads the brokers' metrics from Prometheus. So submit the Part II scenario file with `kates test apply -f payments-scenarios.yaml`, without `--wait`, and run the plan while that LOAD run produces.

A plan can't measure loss, so the verdicts come from two resilience files, `payments-integrity-broker.yaml` and `payments-integrity-zone.yaml`. Each runs an INTEGRITY test through one of the same two faults with `kates resilience run -f <file>`, which can return before its INTEGRITY run ends. Find that run with `kates test list --type INTEGRITY`, wait until it shows `DONE`, and read its `Lost`, `RPO` and `Verdict` with `kates test get <id>`. The target holds when `kates resilience run` reported `Status COMPLETED` and the INTEGRITY run shows `Lost` 0 with a measured RPO. When `Lost` is 0, an RPO of `not measured` means the fault never reached the run.

Run the zone file only once the broker file's INTEGRITY run is `DONE`, because the two runs share a topic and a consumer group. A resilience run's fault skips the safety guard, so first preview the zone's pods with `kates disruption playbook run az-failure --dry-run`.

## Practice

[Tutorial 3: Chaos Engineering with Kates](../tutorials/03-chaos-engineering.md) runs a disruption plan, a playbook and a resilience run on the lab. [Tutorial 4: Data Integrity Under Fire](../tutorials/04-integrity-under-fire.md) kills a broker while an INTEGRITY run is producing, and reads the verdict.
