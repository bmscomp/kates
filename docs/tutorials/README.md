# Kates Tutorials

Hands-on tutorials for learning Kates — from your first test to advanced chaos engineering.

## Tutorial List

| # | Tutorial | Level | Duration |
|:-:|----------|:---:|:---:|
| 1 | [Getting Started](01-getting-started.md) | Beginner | 15 min |
| 2 | [Running Every Test Type](02-all-test-types.md) | Beginner | 30 min |
| 3 | [Chaos Engineering with Kates](03-chaos-engineering.md) | Intermediate | 45 min |
| 4 | [Data Integrity Under Fire](04-integrity-under-fire.md) | Intermediate | 30 min |
| 5 | [Heatmaps, Trends, and Exports](05-observability.md) | Intermediate | 20 min |
| 6 | [CI/CD Integration](06-cicd-integration.md) | Advanced | 30 min |
| 7 | [Kyverno & Security](07-kyverno-security.md) | Intermediate | 30 min |
| 8 | [Deploy, Detect & Clean](08-deploy-and-detect.md) | Beginner → Intermediate | 25 min |
| 9 | [Kafka Connect Working Examples (CDC + JDBC)](09-kafka-connect-working-examples.md) | Intermediate | 35 min |
| 10 | [Kafka Connect Source/Sink Quick Runbook](kafka-connect-simple-source-sink-demo.md) | Intermediate | 15 min |

## Prerequisites

All tutorials assume:
- The full stack is deployed (`make all` + `make kates`)
- The CLI is installed (`make cli-install`)
- The CLI is configured (`kates ctx set local --url http://localhost:30083`)
