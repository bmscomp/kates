# Kates Tutorials

Hands-on tutorials for learning Kates — from your first test to advanced chaos engineering.

## Tutorial List

| # | Tutorial | Level | Duration | Prerequisites |
|:-:|----------|:---:|:---:|:---:|
| 1 | [Getting Started](01-getting-started.md) | Beginner | 15 min | None |
| 2 | [Running Every Test Type](02-all-test-types.md) | Beginner | 30 min | Tutorial 1 |
| 3 | [Chaos Engineering with Kates](03-chaos-engineering.md) | Intermediate | 45 min | Tutorials 1–2 |
| 4 | [Data Integrity Under Fire](04-integrity-under-fire.md) | Intermediate | 30 min | Tutorial 3 |
| 5 | [Heatmaps, Trends, and Exports](05-observability.md) | Intermediate | 20 min | Tutorial 1 |
| 6 | [CI/CD Integration](06-cicd-integration.md) | Advanced | 30 min | Tutorials 1–2 |
| 7 | [Kyverno & Security](07-kyverno-security.md) | Intermediate | 30 min | Tutorial 1 |
| 8 | [Deploy, Detect & Clean](08-deploy-and-detect.md) | Beginner → Intermediate | 25 min | Tutorial 1 |
| 9 | [Kafka Connect Working Examples (CDC + JDBC)](09-kafka-connect-working-examples.md) | Intermediate | 35 min | Tutorial 1 |
| – | [Kafka Connect Source/Sink Quick Runbook](kafka-connect-simple-source-sink-demo.md) | Intermediate | 15 min | Tutorial 1 |
| 10 | [Installing and Setting Up MirrorMaker 2](10-mirror-maker2-installation.md) | Intermediate | 30 min | Tutorial 1 |
| 11 | [Migrating Kafka 2.x to 4.x](11-migrating-kafka-2x-to-4x.md) | Advanced | 60 min | Tutorial 10 |
| 12 | [Migrating Kafka 3.x to 4.x](12-migrating-kafka-3x-to-4x.md) | Intermediate | 40 min | Tutorial 10 |
| 13 | [Using the Grafana Dashboards](13-using-the-dashboards.md) | Intermediate | 45 min | Tutorial 1 |
| 14 | [Using Kates from an AI Agent](14-using-kates-from-an-ai-agent.md) | Intermediate | 20 min | Tutorial 1 |

Tutorials 10–12 rehearse a migration on Kind, where nothing costs anything if
it goes wrong. When you are ready to migrate a real cluster, the chart's own
[migration guides](../../charts/mirror-maker2/docs/) cover the decisions,
credentials and checks that a lab does not have.

## Skill Progression

The tutorials are designed to build on each other. Here's the recommended learning path:

```
Tutorial 1 (Getting Started)
    ├── Tutorial 2 (Test Types) ──── Tutorial 3 (Chaos) ──── Tutorial 4 (Integrity)
    ├── Tutorial 5 (Observability) ──── Tutorial 13 (Grafana Dashboards)
    ├── Tutorial 6 (CI/CD) ← requires Tutorial 2
    ├── Tutorial 7 (Security)
    ├── Tutorial 8 (Deploy & Detect)
    ├── Tutorial 9 (Kafka Connect)
    ├── Tutorial 14 (AI agent over MCP, read-only)
    └── Tutorial 10 (MirrorMaker 2) ──── Tutorials 11–12 (cross-version migration)
```

- **Start here:** Tutorial 1 is required for all others.
- **Performance track:** Tutorials 1 → 2 → 5 — learn test types, then understand the metrics.
- **Observability track:** Tutorials 1 → 5 → 13 — the CLI's own views of a run first, then the twelve Grafana boards that watch the cluster around it.
- **Chaos track:** Tutorials 1 → 2 → 3 → 4 — build up to chaos engineering and data integrity.
- **Operations track:** Tutorials 1 → 8 → 7 — deployment, security, and lifecycle management.

## Prerequisites

All tutorials assume:
- The full stack is deployed (`make all` + `make kates`)
- The CLI is installed (`make cli-install`)
- The CLI is configured (`kates ctx set local --url http://localhost:30083`)

## Resource Requirements

All tutorials run on the default Kind cluster. Minimum resources:
- **CPU:** 6 cores available to Docker
- **Memory:** 16 GB available to Docker
- **Disk:** 30 GB free space
