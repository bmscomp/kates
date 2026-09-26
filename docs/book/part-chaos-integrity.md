# Part III — Chaos & Integrity

What happens to `krafter` when a broker dies, the network splits or a whole zone goes dark, and did any acknowledged record go missing? This Part answers both questions. It turns "the cluster is resilient" into a hypothesis you can test, runs the experiment, and then checks every record.

Every experiment here breaks something on purpose, so two rules come first: write the hypothesis before you inject anything, and keep the blast radius small with the guardrails that Chaos Engineering in Practice describes. Have a LOAD baseline from [Part II — Performance Testing](part-performance-testing.md) at hand, so that you can tell the fault's effect from the cluster's normal behavior. Some disruption types run only on LitmusChaos, which `make all` deploys with the rest of the stack; Chaos Engineering in Practice says which.

## What You'll Have at the End

You'll have a steady-state hypothesis written down with pass and fail criteria, and a disruption run whose report shows how the cluster recovered. You'll also have an integrity verdict for a run that lost a broker partway through, which tells you whether every acknowledged record arrived.

## The Chapters

The chapters go from the idea, to the tooling, to the proof that nothing was lost:

- [Chaos Engineering Theory](06-chaos-theory.md): how do you say what healthy means before you break anything, and how does Kafka behave when it breaks? About 5 minutes.
- [Chaos Engineering in Practice](07-chaos-practice.md): how does Kates inject a fault, keep it inside a safe blast radius, and grade the result? About 20 minutes.
- [Data Integrity Verification](08-data-integrity.md): did every acknowledged record arrive, and if not, which ones were lost? About 15 minutes.

## Choosing How to Inject a Fault

The chapters use three commands to inject faults, and the one you reach for depends on what you want to learn:

| Command | What it runs | Reach for it when |
|----------------------------------------|--------------------------------|----------------------------|
| `kates disruption run --config <file>` | A disruption plan: your own steps, one fault each, within limits such as `maxAffectedBrokers` | You want to choose the faults, their order and the blast radius |
| `kates disruption playbook run <name>` | A playbook: a disruption plan that ships with Kates | You want a common failure, such as losing a zone, without writing the plan |
| `kates resilience run -f <file>` | A performance test, with one fault injected after a steady-state wait | You want the fault's cost to a workload, or an INTEGRITY run through it |

A Game Day is the session your team runs around these commands, from hypothesis to follow-up, and Chaos Engineering Theory shows how to structure one.

## Practice

[Tutorial 3: Chaos Engineering with Kates](../tutorials/03-chaos-engineering.md) runs a disruption plan, a playbook and a resilience run on the lab. [Tutorial 4: Data Integrity Under Fire](../tutorials/04-integrity-under-fire.md) kills a broker while an INTEGRITY run is producing, and reads the verdict.
