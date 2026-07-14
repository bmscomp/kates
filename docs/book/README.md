# Kates — The Definitive Guide

**Kafka Advanced Testing & Engineering Suite**

A comprehensive guide to performance testing, chaos engineering, and operational resilience for Apache Kafka — from theory to practice.

## Table of Contents

| Chapter | Title | Description |
|---------|-------|-------------|
| 1 | [Introduction](01-introduction.md) | What Kates is, why it exists, and the problems it solves |
| 2 | [Architecture & Design](02-architecture.md) | Platform architecture, component design, data model, and technology choices |
| 3 | [The Cluster Under Test](03-cluster.md) | Understanding the krafter Kafka cluster topology |
| 4 | [Performance Theory](04-performance-theory.md) | Measuring performance: latency, throughput, percentiles, and statistics |
| 5 | [Test Types Deep Dive](05-test-types.md) | All 8 test types explained with methodology and use cases |
| 6 | [Chaos Engineering Theory](06-chaos-theory.md) | Principles, practices, and the Game Day methodology |
| 7 | [Chaos Engineering in Practice](07-chaos-practice.md) | Disruption types, playbooks (with full YAML), safety guardrails, and SLA grading |
| 8 | [Data Integrity Verification](08-data-integrity.md) | Ensuring zero message loss under fault conditions |
| 9 | [Observability & Monitoring](09-observability.md) | Metrics, dashboards, heatmaps, and trend analysis |
| 10 | [CLI Reference](10-cli-reference.md) | Complete Kates CLI command reference with all subcommands and aliases |
| 10b | [Lab — Interactive Performance Tuning](10b-lab.md) | The interactive TUI workbench for iterative tuning and result comparison |
| 11 | [REST API Reference](11-api-reference.md) | Backend API endpoints and data models |
| 12 | [Deployment Guide](12-deployment.md) | Installing and operating the full stack |
| 13 | [Scenario Files & SLA Gates](13-scenario-files.md) | YAML scenario format, spec fields, and automated SLA enforcement |
| 14 | [Recipes & Patterns](14-recipes.md) | Ready-to-use workflows for upgrades, nightly regressions, chaos certification, and tuning |
| 15 | [Kafka Deployment Engineering](15-kafka-deployment.md) | Strimzi operator, KRaft architecture, broker tuning, security, and operations |
| 16 | [gRPC API Reference](16-grpc-api.md) | Protobuf service definitions, message types, and usage examples |
| 17 | [Security & Compliance](17-security.md) | Authentication, authorization, certificates, network policies, Kyverno admission control, and audit checklist |
| 18 | [Upgrade Playbook](18-upgrade-playbook.md) | Step-by-step procedures for upgrading Kafka, Strimzi, and Kates |
| 19 | [Multi-Tenancy](19-multi-tenancy.md) | Topic naming, service onboarding, quotas, and tenant isolation |
| 20 | [Installation Guide](20-installation-guide.md) | Step-by-step Kafka deployment with prerequisites, verification, and troubleshooting |
| 21 | [Kafka Connect & CDC Pipelines](21-kafka-connect.md) | Kafka Connect architecture, Debezium CDC, connector lifecycle, multi-AZ strategy, and operational procedures |
| A | [Glossary](appendix-a-glossary.md) | Quick reference for all terms and abbreviations |
| B | [Troubleshooting Index](appendix-b-troubleshooting.md) | Consolidated troubleshooting procedures from across the book |
| C | [CI/CD Pipeline](appendix-c-cicd.md) | GitHub Actions workflows, build validation, and release automation |

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
1. [Ch. 4: Performance Theory](04-performance-theory.md) — why averages lie, percentiles, coordinated omission (~10 min)
2. [Ch. 5: Test Types Deep Dive](05-test-types.md) — all 8 test types explained (~15 min)
3. [Ch. 9: Observability](09-observability.md) — reading dashboards, heatmaps, trend analysis (~15 min)

### 🚀 "I want to deploy Kates"
1. [Ch. 20: Installation Guide](20-installation-guide.md) — full step-by-step with prerequisites (~30 min)
2. [Ch. 12: Deployment Guide](12-deployment.md) — architecture decisions, resource sizing, cloud deployment (~15 min)
3. [Ch. 3: The Cluster Under Test](03-cluster.md) — understanding what you just deployed (~10 min)

### 💥 "I want to run chaos experiments"
1. [Ch. 6: Chaos Engineering Theory](06-chaos-theory.md) — principles and methodology (~10 min)
2. [Ch. 7: Chaos Engineering Practice](07-chaos-practice.md) — disruption types and playbooks (~20 min)
3. [Ch. 8: Data Integrity Verification](08-data-integrity.md) — proving zero message loss (~10 min)

### 🔒 "I want to harden security"
1. [Ch. 17: Security & Compliance](17-security.md) — threat model, ACLs, network policies, Kyverno (~20 min)
2. [Ch. 15: Kafka Deployment Engineering](15-kafka-deployment.md) — broker security, certificates (~15 min)
3. [Tutorial 7: Kyverno & Security](../tutorials/07-kyverno-security.md) — hands-on policy enforcement (~20 min)

### 📋 "I just need a reference"
- [Ch. 10: CLI Reference](10-cli-reference.md) — all commands with examples and workflows
- [Ch. 11: REST API Reference](11-api-reference.md) — backend endpoints and data models
- [Ch. 16: gRPC API Reference](16-grpc-api.md) — protobuf service definitions
- [Appendix A: Glossary](appendix-a-glossary.md) — terms and abbreviations
- [Appendix B: Troubleshooting](appendix-b-troubleshooting.md) — symptom → cause → fix

