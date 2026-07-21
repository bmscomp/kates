# Kates — The Definitive Guide

**Kafka Advanced Testing & Engineering Suite**

A comprehensive guide to performance testing, chaos engineering, and operational resilience for Apache Kafka — from theory to practice.

## Table of Contents

Reading order is defined by [`_quarto.yml`](_quarto.yml) — filenames are stable identifiers, not ordering. Chapter numbers are assigned automatically by the rendered book.

**Part I — Foundations**

| Title | Description |
|-------|-------------|
| [Introduction](01-introduction.md) | What Kates is, why it exists, and the problems it solves |
| [Architecture & Design](02-architecture.md) | Platform architecture, component design, data model, and technology choices |
| [The Cluster Under Test](03-cluster.md) | Understanding the krafter Kafka cluster topology |

**Part II — Performance Testing**

| Title | Description |
|-------|-------------|
| [Performance Theory](04-performance-theory.md) | Measuring performance: latency, throughput, percentiles, and statistics |
| [Test Types Deep Dive](05-test-types.md) | Every test type explained with methodology and use cases |
| [Scenario Files & SLA Gates](13-scenario-files.md) | YAML scenario format, spec fields, and automated SLA enforcement |
| [Lab — Interactive Performance Tuning](10b-lab.md) | The interactive TUI workbench for iterative tuning and result comparison |

**Part III — Chaos & Integrity**

| Title | Description |
|-------|-------------|
| [Chaos Engineering Theory](06-chaos-theory.md) | Principles, practices, and the Game Day methodology |
| [Chaos Engineering in Practice](07-chaos-practice.md) | Disruption types, playbooks, safety guardrails, and SLA grading |
| [Data Integrity Verification](08-data-integrity.md) | Ensuring zero message loss under fault conditions |

**Part IV — Observability**

| Title | Description |
|-------|-------------|
| [Observability & Monitoring](09-observability.md) | Metrics, dashboards, heatmaps, and trend analysis |

**Part V — Deployment & Operations**

| Title | Description |
|-------|-------------|
| [Installing Kafka with the kafka-cluster Helm Chart](20-installation-guide.md) | Step-by-step Kafka deployment with prerequisites and verification |
| [kafka-cluster Chart Reference](kafka-cluster-chart-reference.md) | The chart's resource graph, listeners, topics & users, network policies, observability, and advanced features |
| [Kafka Deployment Engineering](15-kafka-deployment.md) | The engineering rationale: Strimzi, KRaft, broker tuning, and operations |
| [Deployment Guide](12-deployment.md) | Deploying the Kates stack: topology decisions, sizing, and cloud guidance |
| [Security & Compliance](17-security.md) | Authentication, authorization, certificates, network policies, and Kyverno |
| [Multi-Tenancy](19-multi-tenancy.md) | Topic naming, service onboarding, quotas, and tenant isolation |
| [Upgrade Playbook](18-upgrade-playbook.md) | Step-by-step procedures for upgrading Kafka, Strimzi, and Kates |
| [Kafka Connect & CDC Pipelines](21-kafka-connect.md) | Connect concepts: architecture, Debezium CDC, transforms, and delivery semantics |
| [Operating Kafka Connect](operating-kafka-connect.md) | Day-2 operations: scaling, tuning, security rotation, upgrades, and DR |
| [Recipes & Patterns](14-recipes.md) | Ready-to-use workflows for upgrades, nightly regressions, and tuning |

**Part VI — Reference**

| Title | Description |
|-------|-------------|
| [CLI Reference](10-cli-reference.md) | Install, configuration, workflows, and the everyday health, cluster, test, report, and trend commands |
| [Operations CLI Reference](cli-operations.md) | Disruptions, chaos history, resilience, schedules, observability, the Lab, and deployment & lifecycle |
| [Security & Analysis CLI Reference](cli-security-analysis.md) | Security, Kyverno, Kafka client, analysis, tuning, profiles, and developer tooling |
| [REST API Reference](11-api-reference.md) | Backend API endpoints and data models |
| [gRPC API Reference](16-grpc-api.md) | Protobuf service definitions, message types, and usage examples |

**Appendices**

| Title | Description |
|-------|-------------|
| [Glossary](appendix-a-glossary.md) | Quick reference for all terms and abbreviations |
| [Troubleshooting Index](appendix-b-troubleshooting.md) | Consolidated troubleshooting procedures from across the book |
| [CI/CD Pipeline](appendix-c-cicd.md) | GitHub Actions workflows, build validation, and release automation |
| [Version & Compatibility Matrix](appendix-d-versions.md) | Every pinned version, generated from the repo's own pins |

## Tutorials

Hands-on step-by-step guides for specific workflows:

| Tutorial | Description |
|----------|-------------|
| [Getting Started](../tutorials/01-getting-started.md) | First deployment and test execution |
| [All Test Types](../tutorials/02-all-test-types.md) | Walkthrough of every test type |
| [Chaos Engineering](../tutorials/03-chaos-engineering.md) | Your first chaos experiment |
| [Integrity Under Fire](../tutorials/04-integrity-under-fire.md) | Data integrity verification under fault conditions |
| [Observability](../tutorials/05-observability.md) | Setting up dashboards and alerts |
| [CI/CD Integration](../tutorials/06-cicd-integration.md) | Automated testing in pipelines |
| [Kyverno & Security](../tutorials/07-kyverno-security.md) | Policy enforcement, security auditing, and compliance checks |
| [Deploy, Detect & Clean](../tutorials/08-deploy-and-detect.md) | Interactive deployment wizard, latency detection, and lifecycle management |
| [Kafka Connect Working Examples](../tutorials/09-kafka-connect-working-examples.md) | CDC + JDBC connector setup with Debezium |
| [Kafka Connect Source/Sink Demo](../tutorials/kafka-connect-simple-source-sink-demo.md) | Minimal source-to-sink runbook |

## Who This Book Is For

- **Platform engineers** who need to validate Kafka cluster resilience before production
- **SREs** who want automated chaos testing with SLA enforcement
- **Performance engineers** who need rigorous benchmarking beyond `kafka-perf-test`
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
- [CLI Reference](10-cli-reference.md) — install, configuration, workflows, and the everyday commands
- [Operations CLI Reference](cli-operations.md) — disruption, schedule, deploy, and Lab commands
- [Security & Analysis CLI Reference](cli-security-analysis.md) — security, policy, and analysis commands
- [REST API Reference](11-api-reference.md) — backend endpoints and data models
- [gRPC API Reference](16-grpc-api.md) — protobuf service definitions
- [Glossary](appendix-a-glossary.md) — terms and abbreviations
- [Troubleshooting Index](appendix-b-troubleshooting.md) — symptom → cause → fix

