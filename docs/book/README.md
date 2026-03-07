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
| 11 | [REST API Reference](11-api-reference.md) | Backend API endpoints and data models |
| 12 | [Deployment Guide](12-deployment.md) | Installing and operating the full stack |
| 13 | [Scenario Files & SLA Gates](13-scenario-files.md) | YAML scenario format, spec fields, and automated SLA enforcement |
| 14 | [Recipes & Patterns](14-recipes.md) | Ready-to-use workflows for upgrades, nightly regressions, chaos certification, and tuning |
| 15 | [Kafka Deployment Engineering](15-kafka-deployment.md) | Strimzi operator, KRaft architecture, broker tuning, security, and operations |
| 16 | [gRPC API Reference](16-grpc-api.md) | Protobuf service definitions, message types, and usage examples |
| 17 | [Security & Compliance](17-security.md) | Authentication, authorization, certificates, network policies, and audit checklist |
| 18 | [Upgrade Playbook](18-upgrade-playbook.md) | Step-by-step procedures for upgrading Kafka, Strimzi, and Kates |
| 19 | [Multi-Tenancy](19-multi-tenancy.md) | Topic naming, service onboarding, quotas, and tenant isolation |
| A | [Glossary](appendix-a-glossary.md) | Quick reference for all terms and abbreviations |
| B | [Troubleshooting Index](appendix-b-troubleshooting.md) | Consolidated troubleshooting procedures from across the book |

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

## Who This Book Is For

- **Platform engineers** who need to validate Kafka cluster resilience before production
- **SREs** who want automated chaos testing with SLA enforcement
- **Performance engineers** who need rigorous benchmarking beyond `kafka-perf-test`
- **Developers** building event-driven systems who want confidence in their Kafka infrastructure

## How to Read This Book

Start with chapters 1–3 for context. If you're focused on **performance testing**, read chapters 4–5 then 13 for scenario files. For **chaos engineering**, read chapters 6–8. Chapters 9–12 are reference material you'll return to repeatedly. Chapter 14 provides practical recipes for common workflows. Chapter 15 is the **Kafka operations manual** — read it when deploying, upgrading, or troubleshooting the cluster. Chapters 16–19 cover advanced topics: gRPC integration, security hardening, upgrade procedures, and multi-tenant operations. The appendices provide quick reference for terminology and troubleshooting.
