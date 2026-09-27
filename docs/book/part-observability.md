# Part IV — Observability

Parts II and III tell you what a run measured; this Part tells you why it measured that. A report that gives you a throughput and a P99 can't say whether one broker did twice the work, whether replicas fell out of the in-sync set, or whether the brokers paused for garbage collection. The dashboards, heatmaps, trends and report comparisons in this Part can.

Read it once you have runs to explain, after [Part II — Performance Testing](part-performance-testing.md) at least. You don't need to install anything first: with isolated namespaces, `make all` deploys Prometheus and Grafana into the `monitoring` namespace along with the rest of the stack, and `MONITORING_NS=monitoring make ports` forwards Grafana to `http://localhost:30080`.

## What You'll Have at the End

You'll have an order to look in after any run: cluster health, then performance, then broker internals, then replication. You'll know which Grafana dashboard or CLI command answers which question, and you'll be able to export a latency heatmap, track a metric across runs, and diff two reports to catch a regression.

## The Chapters

This Part has one chapter, which gives the diagnostic walkthrough right after its architecture overview and then covers each tool in turn:

- [Observability & Monitoring](09-observability.md): why did a run produce the numbers it did, and where do you look to find out? About 40 minutes.

## The Payments Question

This Part adds no new run to the payments question: it explains the runs that Parts II and III produced. When a payments run misses a gate, `kates report diff <baseline-id> <id>` sets it beside the Part II baseline, and `kates report heatmap <id>` shows its latency distribution as the run went on. Across runs, `kates trend --type LOAD --phase produce --metric p99LatencyMs` follows the P99 of the `produce` row, the one the latency target is about. It takes every LOAD run of the last 30 days whatever its topic, so the Quick Start's runs sit beside the payments runs. The Grafana dashboards show what the cluster did while a run produced.

## Practice

[Tutorial 5: Heatmaps, Trends, and Exports](../tutorials/05-observability.md) works with the CLI's own views of finished runs: heatmaps, report comparisons, trends and exports. [Tutorial 13: Using the Grafana Dashboards](../tutorials/13-using-the-dashboards.md) covers the Grafana boards that watch the cluster around a run, and what to do when a panel is empty.
