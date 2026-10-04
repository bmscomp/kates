# Part I — Foundations

Before you measure a Kafka cluster, you need a picture of the tool that measures it and of the cluster it measures. This Part gives you both: what Kates is and where it fits among your tools, how its pieces work together during a test run, and the `krafter` Kafka cluster that every later chapter tests.

Read it first, whatever your role, because the rest of the book builds on it. The Introduction's Quick Start needs a running lab, so set one up first: Before You Start in the [Preface](index.qmd) lists what you need and the command that builds it.

## What You'll Have at the End

You'll have a CLI connected to the lab and a first LOAD report you know how to read. You'll be able to trace a test from `kates test create` through the Kates API to its report. You'll also know how `krafter`'s replication settings and memory budget shape every number you measure in Parts II and III.

## The Chapters

The three chapters move from the product, to its design, to the cluster it tests:

- [Introduction](01-introduction.md): what is Kates, and where does it fit among the tools you already use? About 5 minutes.
- [Architecture & Design](02-architecture.md): how do the CLI, the Kates API and the tools around them work together during a test run? About 10 minutes.
- [The Cluster Under Test](03-cluster.md): what is `krafter`, and how does its design shape the numbers you measure? About 15 minutes.

## The Payments Question

The book follows one question from this Part to Part V: is `krafter` ready for a payments workload? You run the payments platform, and before it moves onto `krafter` you have to say whether the cluster can carry it. The book turns that into targets a Kates run can check. `krafter` must take 2,000 records per second of 1 KiB with `acks=all`, and answer writes with a P99 of 100 ms or less from send to acknowledgment. It must also lose no acknowledged record when one broker or one zone fails.

This Part adds what every later run needs: the lab that `make all` builds, and a first LOAD report from the Quick Start's `kates test create --type LOAD --records 100000 --wait` and `kates report show <id>`. The Cluster Under Test shows why `acks=all` puts replication inside the latency of every write: the leader acknowledges a write only once every in-sync replica has it.

## Practice

The Quick Start in [Introduction](01-introduction.md#quick-start) is this Part's hands-on step: it connects the CLI to the lab, runs a first test and shows its report. [Tutorial 1: Getting Started with Kates](../tutorials/01-getting-started.md) goes over the same ground one step at a time, from `make all` to a first report, and then exports the report and compares two runs with `kates report diff`. The [Kates Tutorials](../tutorials/README.md) are for practice once the Quick Start works, and each of the Part pages that follow links the ones that go with its chapters.
