# Part V — Deployment & Operations

Parts I to IV use the lab as `make all` builds it. This Part is about building and running a cluster yourself: installing the Strimzi operator, Kafka and Kates, and then securing, sharing, upgrading and extending what you've built.

You don't read this Part straight through, and none of it is required for Parts II to IV. The first four chapters are for the day you stand up a cluster; the others are for the day their job comes up.

## What You'll Have at the End

It depends on the chapters you pick. Read the build chapters and you'll have the operator, a `kafka-cluster` release and the Kates stack installed and verified, sized for your environment. Read the others and you'll have the procedures for day-2 work: onboarding a tenant, upgrading each component with a way back, and running Kafka Connect and MirrorMaker 2.

## The Chapters

The chapters fall into four groups. Read the first group in install order, and reach for the others when their trigger comes up.

### Build It

Read these when you stand up a cluster, in the order you install things: the operator, then Kafka, then Kates. Installing Kafka is the step-by-step walkthrough, Kafka Deployment Engineering gives the reasons behind its choices, and the Deployment Guide is about Kates rather than Kafka.

- [Deploying the Strimzi Operator](deploying-strimzi-operator.md): how do you install, migrate and upgrade the operator that every Kafka cluster here depends on? About 30 minutes.
- [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): how do you deploy and verify a Kafka cluster with the chart, step by step? About 70 minutes.
- [Kafka Deployment Engineering](15-kafka-deployment.md): why is the `krafter` Kafka cluster built the way it is, from node pools to listeners and certificates? About 25 minutes.
- [Deployment Guide](12-deployment.md): how do you size and deploy the Kates stack, on Kind or on EKS, GKE or AKS? About 30 minutes.

### Share and Change It

Read these when other teams join the cluster, or when a new version is out. The Upgrade Playbook uses Kates to check its own work: record a baseline, upgrade, run the same tests again and compare.

- [Security & Compliance](17-security.md): who can reach the cluster, with which rights, and how do you audit that? About 30 minutes.
- [Multi-Tenancy](19-multi-tenancy.md): how do several teams share `krafter` without getting in each other's way? About 10 minutes.
- [Upgrade Playbook](18-upgrade-playbook.md): in what order do you upgrade each component, and how do you know you can still roll back? About 15 minutes.

### Extend and Replicate It

Read these when you move data into or out of Kafka, or between clusters. MirrorMaker 2 runs as a Kafka Connect cluster, so the Connect chapters come first. If you run neither Connect nor MirrorMaker 2, skip all four.

- [Kafka Connect & CDC Pipelines](21-kafka-connect.md): how do you build a change-data-capture pipeline from PostgreSQL with Kafka Connect and Debezium? About 25 minutes.
- [Operating Kafka Connect](operating-kafka-connect.md): how do you keep a Connect cluster healthy through scaling, tuning, credential rotation, upgrades and recovery? About 20 minutes.
- [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md): how do you copy records and consumer offsets from one Kafka cluster to another with MirrorMaker 2? About 10 minutes.
- [Migrating a Legacy Kafka Source](23-legacy-source-migration.md): what changes when the cluster you migrate from runs an old Kafka version? About 15 minutes.

### Put It Together

The last chapter closes this Part but draws more on Parts II and III: it combines their commands into end-to-end procedures.

- [Recipes & Patterns](14-recipes.md): how do you validate an upgrade, run a nightly regression suite or certify resilience, from start to finish? About 5 minutes.

## Practice

For the build chapters, [Tutorial 8 — Deploy, Detect & Clean](../tutorials/08-deploy-and-detect.md) checks a cluster with `kates detect`, deploys the stack with the interactive `kates deploy -i`, and removes it with `kates clean`. [Tutorial 7: Kyverno & Security](../tutorials/07-kyverno-security.md) goes with Security & Compliance: it manages the Kyverno admission policies and runs a security audit.

For Connect, [Tutorial 9: Kafka Connect Working Examples (CDC + JDBC)](../tutorials/09-kafka-connect-working-examples.md) builds a Debezium CDC pipeline and JDBC connectors, and the [Kafka Connect Source/Sink Quick Runbook](../tutorials/kafka-connect-simple-source-sink-demo.md) follows one row from PostgreSQL through Kafka to a replica table. For MirrorMaker 2, start with [Tutorial 10: Installing and Setting Up MirrorMaker 2](../tutorials/10-mirror-maker2-installation.md). Then rehearse a migration on Kind with [Tutorial 11: Migrating Kafka 2.x to 4.x with MirrorMaker 2](../tutorials/11-migrating-kafka-2x-to-4x.md) or [Tutorial 12: Migrating Kafka 3.x to 4.x with MirrorMaker 2](../tutorials/12-migrating-kafka-3x-to-4x.md).

[Tutorial 6: CI/CD Integration](../tutorials/06-cicd-integration.md) goes with Recipes & Patterns: it turns performance, integrity and chaos checks into pipeline gates, and schedules nightly runs.
