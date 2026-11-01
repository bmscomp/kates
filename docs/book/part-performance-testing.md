# Part II — Performance Testing

Can `krafter` take 2,000 records per second of 1 KiB with `acks=all` and still answer writes with a P99 of 100 ms or less? A Kates run gives you a dozen numbers toward that answer, and two runs of the same test never agree exactly. This Part shows you which numbers to trust, which test type to run for which question, and how to keep the test in a file that fails a CI job when the cluster misses the target.

Read Performance Theory first: the other three chapters produce numbers, and it tells you which of them to distrust. The Part assumes the lab and the configured CLI from [Part I — Foundations](part-foundations.md).

- [Performance Theory](04-performance-theory.md): when is a P99 that moved between two runs a regression, and when is it noise? About 10 minutes.
- [Test Types Deep Dive](05-test-types.md): which of the eight core test types answers your question, whether that's latency at 2,000 records per second, the saturation point, a burst or a lost record? About 15 minutes.
- [Scenario Files & SLA Gates](13-scenario-files.md): how do you keep the payments test in Git and make `kates test apply` fail the build when P99 passes 100 ms? About 15 minutes.
- [Lab — Interactive Performance Tuning](10b-lab.md): which batch size and linger give `krafter` the most headroom with `acks=all`, found in one terminal UI instead of ten report IDs and a spreadsheet? About 5 minutes.

## The Payments Question

This Part turns the throughput and latency targets into a file you can run again and again, `payments-scenarios.yaml`. It's a scenario file with one LOAD scenario, which offers 2,000 records per second of 1 KiB with `acks=all` to its own topic, `payments-load`. Its `maxP99LatencyMs: 100` gate holds the latency target: it fails a run whose P99 from send to acknowledgment passes 100 ms. `minThroughputRecPerSec` sets a throughput floor below that offered rate, because the rate limiter never makes up for a late send and a run's measured rate lands under the rate it's given. `kates test apply -f payments-scenarios.yaml --wait` runs the scenario and exits 1 when a gate fails.

When a run passes, `kates test baseline set <run-id>` makes it the LOAD baseline, and `kates report regression <run-id>` compares later LOAD runs with it. Kates keeps one baseline per test type, so the payments run replaces any earlier LOAD baseline.

## Running the Tests Yourself

[Tutorial 2: Running Every Test Type](../tutorials/02-all-test-types.md) runs each test type on the lab, from flags or from the built-in templates. Use it to practice; the chapters explain what its numbers mean. When you compare runs, run them one after another, as [Multiple Runs](04-performance-theory.md#multiple-runs) does, because runs that overlap measure each other's load.
