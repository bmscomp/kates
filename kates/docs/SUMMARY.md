# KATES Documentation

Welcome to the KATES (Kafka Advanced Testing & Engineering Suite) documentation. This book is designed to be read cover-to-cover for a complete understanding of the system, or used as a reference for specific topics.

## Getting Started

| Chapter | Description |
|---------|-------------|
| [Overview](overview.md) | What Kates is, the problems it solves, and the high-level architecture |
| [Architecture](architecture.md) | Design philosophy, package structure, class responsibilities, and the execution lifecycle with Mermaid diagrams |
| [Deployment](deployment.md) | Dev mode, JVM/native builds, Kubernetes manifests, and the complete ConfigMap reference |

## Performance Testing

| Chapter | Description |
|---------|-------------|
| [Test Types](test-types.md) | Deep-dive into all 7 test types — the testing philosophy behind each one, when to use it, and worked examples |
| [Export Formats](export-formats.md) | CSV for spreadsheets, JUnit XML for CI/CD quality gates, and latency heatmaps for visualization |

## Chaos Engineering

| Chapter | Description |
|---------|-------------|
| [Disruption Guide](disruption-guide.md) | Chaos engineering theory, all 10 disruption types with Kafka failure theory, the 13-step execution pipeline, SLA grading, and a step-by-step tutorial |
| [Playbook Catalog](playbook-catalog.md) | The 6 built-in disruption playbooks — theory behind each failure scenario, YAML source, what to look for, and how to write custom playbooks |
| [Resilience Testing](resilience-testing.md) | Combining performance + chaos testing, the 9-step orchestration pipeline, impact delta interpretation, and a tutorial |

## Operations

| Chapter | Description |
|---------|-------------|
| [Observability](observability.md) | Prometheus metrics capture, Kafka-native ISR/lag tracking, SSE event streaming, and health diagnostics |
| [Troubleshooting](troubleshooting.md) | Common issues organized by symptom, root cause explanations, and a systematic debugging methodology |

## Reference

| Chapter | Description |
|---------|-------------|
| [API Reference](api-reference.md) | Complete REST API documentation — every endpoint, request/response schema, and example curl commands |
| [Testing Guide](testing.md) | Test suite architecture, mock injection patterns, and how to write new tests |
