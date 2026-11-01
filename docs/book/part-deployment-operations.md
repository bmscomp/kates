# Part V — Deployment & Operations

Parts I to IV use the lab as `make all` builds it. This Part is for the cluster you build and run yourself. You install the Strimzi operator first, then Kafka, then Kates. After that come the jobs a running cluster brings: securing it, letting other teams onto it, upgrading it, and moving data in, out or across.

You don't read this Part straight through, and Parts II to IV don't need any of it. Read the first group of chapters on the day you stand up a cluster, in the order you install things. Each of the other groups starts with the job that sends you to it.

## Build It

Read these when you stand up a cluster. The operator comes first, because without its CRDs a `Kafka` resource is inert. Installing Kafka is the step-by-step walkthrough, and Kafka Deployment Engineering gives the reason behind each of its numbers. The Deployment Guide then installs the Kates stack beside the cluster, and Compliance Checks holds what you built to the architecture you meant to build.

- [Deploying the Strimzi Operator](deploying-strimzi-operator.md): how do you install the operator that every Kafka cluster here depends on, take over one installed from the upstream chart, and upgrade or roll it back? About 30 minutes.
- [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md): how do you deploy and verify a Kafka cluster with the chart, step by step? About 70 minutes.
- [Kafka Deployment Engineering](15-kafka-deployment.md): why is the `krafter` Kafka cluster built the way it is, from node pools to listeners and certificates? About 25 minutes.
- [Deployment Guide](12-deployment.md): how do you size and deploy the Kates stack, on Kind or on EKS, GKE or AKS? About 30 minutes.
- [Compliance Checks](compliance.md): does the cluster you run still match the architecture you meant to build? About 25 minutes.

To practise the install, [Tutorial 8 — Deploy, Detect & Clean](../tutorials/08-deploy-and-detect.md) checks a cluster with `kates detect`, deploys the stack with the interactive `kates deploy -i`, and removes it with `kates clean`.

## Share and Change It

Read these when another team asks for space on the cluster, or when a new Kafka, Strimzi or Kates version is out. The Upgrade Playbook uses Kates to check its own work: you record a baseline, upgrade, run the same tests again and compare.

- [Security & Compliance](17-security.md): who can reach the cluster, with which rights, and how do you audit that? About 30 minutes.
- [Multi-Tenancy](19-multi-tenancy.md): how does the payments platform share `krafter` with the tenants already on it without getting in their way? About 10 minutes.
- [Upgrade Playbook](18-upgrade-playbook.md): in what order do you upgrade each component, and how do you know you can still roll back? About 15 minutes.

[Tutorial 7: Kyverno & Security](../tutorials/07-kyverno-security.md) goes with Security & Compliance: it manages the Kyverno admission policies and runs a security audit.

## Extend and Replicate It

Read these when you move data into or out of Kafka, or from one cluster to another. MirrorMaker 2 runs as a Kafka Connect cluster, so the Connect chapters come first. If you run neither Connect nor MirrorMaker 2, skip all four.

- [Kafka Connect & CDC Pipelines](21-kafka-connect.md): how do you build a change-data-capture pipeline from PostgreSQL with Kafka Connect and Debezium? About 25 minutes.
- [Operating Kafka Connect](operating-kafka-connect.md): how do you keep a Connect cluster running when a zone goes dark, a password expires or a Debezium release lands? About 20 minutes.
- [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md): how do you copy records and consumer offsets from one Kafka cluster to another with MirrorMaker 2? About 10 minutes.
- [Migrating a Legacy Kafka Source](23-legacy-source-migration.md): what changes when the cluster you migrate from runs an old Kafka version? About 15 minutes.

For Connect, [Tutorial 9: Kafka Connect Working Examples (CDC + JDBC)](../tutorials/09-kafka-connect-working-examples.md) builds a Debezium CDC pipeline and JDBC connectors, and the [Kafka Connect Source/Sink Quick Runbook](../tutorials/kafka-connect-simple-source-sink-demo.md) follows one row from PostgreSQL through Kafka to a replica table. For MirrorMaker 2, start with [Tutorial 10: Installing and Setting Up MirrorMaker 2](../tutorials/10-mirror-maker2-installation.md). Then rehearse a migration on Kind with [Tutorial 11: Migrating Kafka 2.x to 4.x with MirrorMaker 2](../tutorials/11-migrating-kafka-2x-to-4x.md) or [Tutorial 12: Migrating Kafka 3.x to 4.x with MirrorMaker 2](../tutorials/12-migrating-kafka-3x-to-4x.md).

## Put It Together

Read the last chapter when you need a whole procedure, such as validating an upgrade or signing `krafter` off for payments. It closes this Part but draws more on Parts II and III, because each recipe chains their commands from start to finish.

- [Recipes & Patterns](14-recipes.md): how do you validate an upgrade, run a nightly regression suite or certify resilience, from start to finish? About 5 minutes.

[Tutorial 6: CI/CD Integration](../tutorials/06-cicd-integration.md) goes with Recipes & Patterns: it turns performance, integrity and chaos checks into pipeline gates, and schedules nightly runs.

## The Payments Question

This Part moves the payments question from the lab to a cluster you run. Compliance Checks writes the architecture the payments workload needs into `payments-krafter.yaml` and holds `krafter` to it. In Multi-Tenancy a tenant is one block of `kafka-cluster` chart values in `tenants.yaml`, applied with one `helm upgrade`, and the payments workload becomes the `payments` tenant: a `payments-events` topic and a `KafkaUser` with prefix ACLs on `payments`. The Upgrade Playbook's re-test is `payments-scenarios.yaml`, run before and after an upgrade, and `kates report diff <pre-id> <post-id>` compares the two runs. On your own cluster the answer comes from the same gates, grades and verdicts as in Parts II and III, and [Recipe 8: Sign Off krafter for Payments](14-recipes.md#recipe-8-sign-off-krafter-for-payments) gathers them into the readiness sign-off.
