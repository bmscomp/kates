# Kates — The Definitive Guide

**Kafka Advanced Testing & Engineering Suite**

A comprehensive guide to performance testing, chaos engineering, and operational resilience for Apache Kafka — from theory to practice.

## Table of Contents

Reading order is defined by [`_quarto.yml`](_quarto.yml) — filenames are stable identifiers, not ordering. Chapter numbers are assigned automatically by the rendered book. Each Part opens with a Part page that says what the Part is for and lists its chapters; the appendices are introduced at the end of the Part VI page.

**[Part I — Foundations](part-foundations.md)**

| Title | Description |
|-------|-------------|
| [Introduction](01-introduction.md) | What Kates is, why it exists, and the problems it solves |
| [Architecture & Design](02-architecture.md) | Platform architecture, component design, data model, and technology choices |
| [The Cluster Under Test](03-cluster.md) | Understanding `krafter`, the Kafka cluster under test, and its node layout |

**[Part II — Performance Testing](part-performance-testing.md)**

| Title | Description |
|-------|-------------|
| [Performance Theory](04-performance-theory.md) | Measuring performance: latency, throughput, percentiles, and statistics |
| [Test Types Deep Dive](05-test-types.md) | Every test type explained with methodology and use cases |
| [Scenario Files & SLA Gates](13-scenario-files.md) | YAML scenario format, spec fields, and automated SLA enforcement |
| [Lab — Interactive Performance Tuning](10b-lab.md) | The interactive TUI workbench for iterative tuning and result comparison |

**[Part III — Chaos & Integrity](part-chaos-integrity.md)**

| Title | Description |
|-------|-------------|
| [Chaos Engineering Theory](06-chaos-theory.md) | Principles, practices, and the Game Day methodology |
| [Chaos Engineering in Practice](07-chaos-practice.md) | Disruption types, playbooks, safety guardrails, and SLA grading |
| [Data Integrity Verification](08-data-integrity.md) | Ensuring zero message loss under fault conditions |

**[Part IV — Observability](part-observability.md)**

| Title | Description |
|-------|-------------|
| [Observability & Monitoring](09-observability.md) | Metrics, dashboards, heatmaps, and trend analysis |

**[Part V — Deployment & Operations](part-deployment-operations.md)**

| Title | Description |
|-------|-------------|
| [Deploying the Strimzi Operator](deploying-strimzi-operator.md) | The operator as its own release: the `strimzi-operator` wrapper chart, its CRD-upgrade hook, and namespace scope |
| [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) | Step-by-step Kafka deployment with prerequisites and verification |
| [Kafka Deployment Engineering](15-kafka-deployment.md) | The engineering rationale: Strimzi, KRaft, broker tuning, and operations |
| [Deployment Guide](12-deployment.md) | Deploying the Kates stack: topology decisions, sizing, and cloud guidance |
| [Security & Compliance](17-security.md) | Authentication, authorization, certificates, network policies, and Kyverno |
| [Multi-Tenancy](19-multi-tenancy.md) | Topic naming, service onboarding, quotas, and tenant isolation |
| [Upgrade Playbook](18-upgrade-playbook.md) | Step-by-step procedures for upgrading Kafka, Strimzi, and Kates |
| [Kafka Connect & CDC Pipelines](21-kafka-connect.md) | Connect concepts: architecture, Debezium CDC, transforms, and delivery semantics |
| [Operating Kafka Connect](operating-kafka-connect.md) | Day-2 operations: scaling, tuning, security rotation, upgrades, and DR |
| [Cross-Cluster Replication and Migration](22-mirror-maker2-migration.md) | MirrorMaker 2 topologies for disaster recovery and cluster migration |
| [Migrating a Legacy Kafka Source](23-legacy-source-migration.md) | Moving off a 2.x/3.x cluster onto the current Kafka line |
| [Recipes & Patterns](14-recipes.md) | Ready-to-use workflows for upgrades, nightly regressions, and tuning |

**[Part VI — Reference](part-reference.md)**

| Title | Description |
|-------|-------------|
| [CLI Reference](10-cli-reference.md) | Complete Kates CLI command reference with all subcommands and aliases |
| [REST API Reference](11-api-reference.md) | The Kates API's REST endpoints and data models |
| [gRPC API Reference](16-grpc-api.md) | Protobuf service definitions, message types, and usage examples |

**Appendices**

| Title | Description |
|-------|-------------|
| [Glossary](appendix-a-glossary.md) | Quick reference for all terms and abbreviations |
| [Troubleshooting Index](appendix-b-troubleshooting.md) | Consolidated troubleshooting procedures from across the book |
| [CI/CD Pipeline](appendix-c-cicd.md) | GitHub Actions workflows, build validation, and release automation |
| [Version & Compatibility Matrix](appendix-d-versions.md) | Every pinned version, generated from the repo's own pins |
| [References](references.md) | The works the book cites, numbered alphabetically by first author |

## Tutorials

Hands-on step-by-step guides for specific workflows. The tutorials are for practice and the book is for explanation; each Part page links the tutorials that go with its chapters.

| # | Tutorial | Description |
|:-:|----------|-------------|
| 1 | [Getting Started with Kates](../tutorials/01-getting-started.md) | Deploy the stack, run a first test, and read and export its results |
| 2 | [Running Every Test Type](../tutorials/02-all-test-types.md) | Run each test type, from flags or from the built-in templates |
| 3 | [Chaos Engineering with Kates](../tutorials/03-chaos-engineering.md) | Disruption plans, a built-in playbook, and a resilience run |
| 4 | [Data Integrity Under Fire](../tutorials/04-integrity-under-fire.md) | Data integrity verification under fault conditions |
| 5 | [Heatmaps, Trends, and Exports](../tutorials/05-observability.md) | Latency heatmaps, report comparison, trends, and export formats |
| 6 | [CI/CD Integration](../tutorials/06-cicd-integration.md) | Performance, integrity, and chaos gates in a pipeline, and scheduled runs |
| 7 | [Kyverno & Security](../tutorials/07-kyverno-security.md) | Policy enforcement, security auditing, and compliance checks |
| 8 | [Deploy, Detect & Clean](../tutorials/08-deploy-and-detect.md) | Cluster analysis with `kates detect`, the interactive deployment wizard, and teardown with `kates clean` |
| 9 | [Kafka Connect Working Examples (CDC + JDBC)](../tutorials/09-kafka-connect-working-examples.md) | CDC + JDBC connector setup with Debezium |
| – | [Kafka Connect Source/Sink Quick Runbook](../tutorials/kafka-connect-simple-source-sink-demo.md) | Minimal source-to-sink runbook |
| 10 | [Installing and Setting Up MirrorMaker 2](../tutorials/10-mirror-maker2-installation.md) | A loopback mirror on `krafter`, installed and proven to move records |
| 11 | [Migrating Kafka 2.x to 4.x with MirrorMaker 2](../tutorials/11-migrating-kafka-2x-to-4x.md) | A 2.x cluster migrated onto 4.x, with a rehearsed cutover, on Kind |
| 12 | [Migrating Kafka 3.x to 4.x with MirrorMaker 2](../tutorials/12-migrating-kafka-3x-to-4x.md) | The same migration from a 3.x cluster |
| 13 | [Using the Grafana Dashboards](../tutorials/13-using-the-dashboards.md) | Install the Grafana boards, read them, and diagnose an empty panel |
| 14 | [Using Kates from an AI Agent](../tutorials/14-using-kates-from-an-ai-agent.md) | Connect an AI agent to the lab through the read-only `kates mcp` server |

## Who This Book Is For

- **Platform engineers** who need to validate Kafka cluster resilience before production
- **SREs** who want automated chaos testing with SLA enforcement
- **Performance engineers** who need rigorous benchmarking beyond `kafka-producer-perf-test`
- **Developers** building event-driven systems who want confidence in their Kafka infrastructure

## How to Read This Book

Don't read this book cover-to-cover. Pick a reading path based on what you need:

### 🎯 "I want to understand Kafka performance"
1. [Performance Theory](04-performance-theory.md) — why averages lie, percentiles, coordinated omission (~10 min)
2. [Test Types Deep Dive](05-test-types.md) — all 8 test types explained (~15 min)
3. [Observability & Monitoring](09-observability.md) — reading dashboards, heatmaps, trend analysis (~15 min)

### 🚀 "I want to deploy Kates"
1. [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) — full step-by-step with prerequisites (~30 min)
2. [Deployment Guide](12-deployment.md) — architecture decisions, resource sizing, cloud deployment (~15 min)
3. [The Cluster Under Test](03-cluster.md) — understanding what you just deployed (~10 min)

### 💥 "I want to run chaos experiments"
1. [Chaos Engineering Theory](06-chaos-theory.md) — principles and methodology (~10 min)
2. [Chaos Engineering in Practice](07-chaos-practice.md) — disruption types and playbooks (~20 min)
3. [Data Integrity Verification](08-data-integrity.md) — proving zero message loss (~10 min)

### 🔒 "I want to harden security"
1. [Security & Compliance](17-security.md) — threat model, ACLs, network policies, Kyverno (~20 min)
2. [Kafka Deployment Engineering](15-kafka-deployment.md) — broker security, certificates (~15 min)
3. [Tutorial 7: Kyverno & Security](../tutorials/07-kyverno-security.md) — hands-on policy enforcement (~20 min)

### 📋 "I just need a reference"
- [CLI Reference](10-cli-reference.md) — all commands with examples and workflows
- [REST API Reference](11-api-reference.md) — the Kates API's endpoints and data models
- [gRPC API Reference](16-grpc-api.md) — protobuf service definitions
- [Glossary](appendix-a-glossary.md) — terms and abbreviations
- [Troubleshooting Index](appendix-b-troubleshooting.md) — symptom → cause → fix

