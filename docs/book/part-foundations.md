# Part I — Foundations

A latency number from `krafter` means little until you know what produced it and what it ran against. This Part covers both: the `kates` CLI and the Kates API that run a test, and the `krafter` Kafka cluster that every later chapter tests. By the end of the Introduction you have a LOAD run of your own and its report.

Read it first, whatever your role. The Introduction's Quick Start needs a running lab, so set one up before you start: Before You Start in the [Preface](index.qmd) lists what you need and the command that builds it.

The three chapters go from the tool, to how it works, to the cluster it tests:

- [Introduction](01-introduction.md): what does Kates test that a generic load tool can't, and how do you get a first LOAD report out of it? About 5 minutes.
- [Architecture & Design](02-architecture.md): where does each part of Kates run, and what happens between `kates test create` and the report? About 10 minutes.
- [The Cluster Under Test](03-cluster.md): how do `krafter`'s replication settings and its ~2Gi page cache budget shape every latency number you measure? About 15 minutes.

If you'd rather take the [Quick Start](01-introduction.md#quick-start) one step at a time, [Tutorial 1: Getting Started with Kates](../tutorials/01-getting-started.md) goes from `make all` to a first report, then exports the report and compares two runs with `kates report diff`.

## The Payments Question

The book follows one question from this Part to Part V: is `krafter` ready for a payments workload? You run the payments platform, and before it moves onto `krafter` you have to say whether the cluster can carry it. `krafter` must take 2,000 records per second of 1 KiB with `acks=all`, and answer writes with a P99 of 100 ms or less from send to acknowledgment. It must also lose no acknowledged record when one broker or one zone fails.

This Part gives you what every later run needs: the lab that `make all` builds, and a first LOAD report from the Quick Start's `kates test create --type LOAD --records 100000 --wait` and `kates report show <id>`. The Cluster Under Test shows why `acks=all` puts replication inside the latency of every write: the leader acknowledges a write only once every in-sync replica has it.
