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
| 10 | [Kafka Connect Source/Sink Quick Runbook](kafka-connect-simple-source-sink-demo.md) | Intermediate | 15 min | Tutorial 1 |

## Skill Progression

The tutorials are designed to build on each other. Here's the recommended learning path:

```
Tutorial 1 (Getting Started)
    ├── Tutorial 2 (Test Types) ──── Tutorial 3 (Chaos) ──── Tutorial 4 (Integrity)
    ├── Tutorial 5 (Observability)
    ├── Tutorial 6 (CI/CD) ← requires Tutorial 2
    ├── Tutorial 7 (Security)
    ├── Tutorial 8 (Deploy & Detect)
    └── Tutorials 9–10 (Kafka Connect)
```

- **Start here:** Tutorial 1 is required for all others.
- **Performance track:** Tutorials 1 → 2 → 5 — learn test types, then understand the metrics.
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
