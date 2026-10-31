# Part IV — Observability

Parts II and III tell you what a run measured; this Part tells you why it measured that. A report that gives you a throughput and a P99 can't say whether one broker did twice the work, whether replicas fell out of the in-sync set, or whether the brokers paused for garbage collection. The Grafana dashboards that watched the cluster can, and so can the latency heatmap the run left behind.

Read it once you have runs to explain, after [Part II — Performance Testing](part-performance-testing.md) at least. You don't need to install anything first. With isolated namespaces, `make all` deploys Prometheus and Grafana into the `monitoring` namespace along with the rest of the stack, and `MONITORING_NS=monitoring make ports` forwards Grafana to `http://localhost:30080`.

- [Observability & Monitoring](09-observability.md): the order to look in after any run, from cluster health to performance, broker internals and replication, and which dashboard or command answers each question. About 40 minutes.

To practice, [Tutorial 5: Heatmaps, Trends, and Exports](../tutorials/05-observability.md) works from the CLI on runs that have finished. [Tutorial 13: Using the Grafana Dashboards](../tutorials/13-using-the-dashboards.md) covers the boards that watch the cluster during a run, and what to do when a panel is empty.

## The Payments Question

This Part adds no run of its own. When a payments run misses a gate, `kates report diff <baseline-id> <id>` sets it beside the Part II baseline, and `kates report export <id> --format heatmap` exports its latency heatmap. Export it soon after the run. The Kates API keeps heatmap rows in memory for its 50 most recent native runs, so a restart loses them.
