# KATES Documentation

Welcome to the KATES (Kafka Advanced Testing & Engineering Suite) documentation. This book covers everything from getting started to advanced chaos engineering.

## Core Documentation

| Chapter | Description |
|---------|-------------|
| [Overview](overview.md) | What Kates is, what problems it solves, high-level architecture |
| [Architecture](architecture.md) | Package structure, class responsibilities, execution lifecycles, SPI design |
| [Test Types](test-types.md) | All 7 performance test types with parameters, defaults, and interpretation |
| [API Reference](api-reference.md) | REST endpoint reference with request/response examples |

## Chaos Engineering

| Chapter | Description |
|---------|-------------|
| [Disruption Guide](disruption-guide.md) | Deep-dive into disruption types, FaultSpec, safety guardrails, SLA grading |
| [Playbook Catalog](playbook-catalog.md) | All 6 built-in playbooks with YAML source and expected outcomes |
| [Resilience Testing](resilience-testing.md) | Combined performance + chaos testing with impact analysis |

## Operations

| Chapter | Description |
|---------|-------------|
| [Deployment](deployment.md) | Local development, Kubernetes deployment, ConfigMaps, RBAC |
| [Observability](observability.md) | Prometheus metrics, ISR tracking, lag monitoring, SSE events |
| [Export Formats](export-formats.md) | CSV, JUnit XML, and heatmap export for CI/CD and visualization |

## Reference

| Chapter | Description |
|---------|-------------|
| [Testing](testing.md) | Unit, integration, and API test architecture |
| [Troubleshooting](troubleshooting.md) | Common issues, their root causes, and how to fix them |
