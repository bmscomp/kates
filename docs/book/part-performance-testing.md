# Part II — Performance Testing

How much can the `krafter` Kafka cluster take, and how fast does it answer? This Part teaches you to measure both in a way you can defend: which numbers to trust, which test answers which question, and how to keep a test so that it runs the same way every time.

Read it before you run a performance test or judge someone else's results. It assumes the lab and the configured CLI from [Part I — Foundations](part-foundations.md).

## What You'll Have at the End

You'll be able to tell a real regression from run-to-run noise and pick the test type that answers the question you're asking. You'll keep your tests in a scenario file whose SLA gates fail a CI job when a run misses them. The Lab adds a way to tune one setting at a time and to save the session as a baseline for later runs.

## The Chapters

Performance Theory comes first because it tells you which numbers to distrust; the other three chapters produce those numbers:

- [Performance Theory](04-performance-theory.md): which numbers can you trust, and when is the difference between two runs real? About 10 minutes.
- [Test Types Deep Dive](05-test-types.md): which test type answers the question you're asking, and how do you configure it? About 15 minutes.
- [Scenario Files & SLA Gates](13-scenario-files.md): how do you keep a test suite in version control and make a missed threshold fail the build? About 15 minutes.
- [Lab — Interactive Performance Tuning](10b-lab.md): how do you tune one setting at a time and compare the runs side by side? About 5 minutes.

## The Payments Question

This Part turns the throughput and latency targets into a file you can run again and again: `payments-scenarios.yaml`, a scenario file with one LOAD scenario that writes 1 KiB records with `acks=all` to its own topic, `payments-load`. Its gates hold the two targets. `maxP99LatencyMs: 100` fails a run whose P99 from send to acknowledgment passes 100 ms, and `minThroughputRecPerSec` sets the throughput floor. `kates test apply -f payments-scenarios.yaml --wait` runs the scenario and exits 1 when a gate fails.

When a run passes, `kates test baseline set <run-id>` makes it the LOAD baseline, and `kates report regression <run-id>` compares later LOAD runs with it. Kates keeps one baseline per test type, so the payments run replaces any earlier LOAD baseline.

## Practice

[Tutorial 2: Running Every Test Type](../tutorials/02-all-test-types.md) runs each test type on the lab, from flags or from the built-in templates. The tutorial is for practice; the chapters explain what its numbers mean.

When you compare runs, run them one after another. The Kates API runs at most three tests at once and refuses a fourth, and runs of one type that set no topic share that type's default topic (`load-test` for LOAD), so runs that overlap measure each other's load.
