# KATES Tutorials

Hands-on tutorials for learning KATES — from your first test to advanced chaos engineering.

## Tutorial List

| # | Tutorial | Level | Duration |
|:-:|----------|:---:|:---:|
| 1 | [Getting Started](01-getting-started.md) | Beginner | 15 min |
| 2 | [Running Every Test Type](02-all-test-types.md) | Beginner | 30 min |
| 3 | [Chaos Engineering with KATES](03-chaos-engineering.md) | Intermediate | 45 min |
| 4 | [Data Integrity Under Fire](04-integrity-under-fire.md) | Intermediate | 30 min |
| 5 | [Heatmaps, Trends, and Exports](05-observability.md) | Intermediate | 20 min |
| 6 | [CI/CD Integration](06-cicd-integration.md) | Advanced | 30 min |

## Prerequisites

All tutorials assume:
- The full stack is deployed (`make all` + `make kates`)
- The CLI is installed (`make cli-install`)
- The CLI is configured (`kates ctx set local --url http://localhost:30083`)
