# Part I — Foundations

Before you measure a Kafka cluster, you need a picture of the tool that measures it and of the cluster it measures. This Part gives you both: what Kates is and where it fits among your tools, how its pieces work together during a test run, and the `krafter` Kafka cluster that every later chapter tests.

Read it first, whatever your role, because the rest of the book builds on it. The Introduction's Quick Start needs a running lab, so set one up first: Before You Start in the [Preface](index.qmd) lists what you need and the command that builds it.

## What You'll Have at the End

You'll have a CLI connected to the lab and a first LOAD report you know how to read. You'll be able to trace a test from `kates test create` through the backend to its report. You'll also know how `krafter`'s replication settings and memory budget shape every number you measure in Parts II and III.

## The Chapters

The three chapters move from the product, to its design, to the cluster it tests:

- [Introduction](01-introduction.md): what is Kates, and where does it fit among the tools you already use? About 5 minutes.
- [Architecture & Design](02-architecture.md): how do the CLI, the backend, the infrastructure and the observability stack work together during a test run? About 10 minutes.
- [The Cluster Under Test](03-cluster.md): what is `krafter`, and how does its design shape the numbers you measure? About 15 minutes.

## Practice

The Quick Start in [Introduction](01-introduction.md#quick-start) is this Part's hands-on step: it connects the CLI to the lab, runs a first test and shows its report. The [Kates Tutorials](../tutorials/README.md) are for practice once the Quick Start works, and each of the Part pages that follow links the ones that go with its chapters.
