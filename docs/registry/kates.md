# Kates — Kafka Advanced Testing & Engineering Suite

**A Kubernetes-native platform for performance testing, chaos engineering and operational
resilience auditing of Apache Kafka clusters.**

[![License](https://img.shields.io/badge/license-Apache%202.0-blue?logo=apache)](https://github.com/bmscomp/kates/blob/main/LICENSE)
[![Source](https://img.shields.io/badge/source-github.com%2Fbmscomp%2Fkates-181717?logo=github)](https://github.com/bmscomp/kates)

Monitoring tells you a Kafka cluster is healthy right now. Kates tells you what happens when
it isn't. It provisions a production-parity environment, runs real workloads against it,
injects real faults *while those workloads run*, and grades the result against your SLAs.

---

## What this image contains

Two things, in one container:

1. **The Kates backend** — a Quarkus service exposing a REST API, a gRPC API, Prometheus
   metrics and OpenTelemetry traces. This is the engine that plans test runs, drives
   producers and consumers, orchestrates chaos experiments and stores results.
2. **The `kates` CLI** — the Go binary, at `/usr/local/bin/kates`, with all 48 commands.
   It is bundled deliberately so that `kubectl exec` into the backend pod gives you a
   fully working control plane with no extra image to pull.

A default CLI config is written to `/home/kates/.kates.yaml` pointing at `localhost:8080`,
so `kubectl exec -it deploy/kates -- kates health` works with no setup.

## What this image is *not*

It is **not** self-contained. The backend needs two things to do anything useful:

- **A Kafka cluster** to test against.
- **A PostgreSQL database** to persist runs, trends and audit history.

Starting it with neither will produce a container that boots and then reports itself
unhealthy on `/api/health` — which is expected, not a bug. See
[Health endpoints](#health-and-probes) for why the *probes* use a different path.

---

## Tags and variants

| Tag pattern | What it is |
|---|---|
| `1.23.0`, `1.23`, `1` | JVM build — Eclipse Temurin 21 JRE |
| `1.23.0-native`, … | GraalVM native build — ahead-of-time compiled, UBI 9 minimal |
| `latest` | Latest default-branch build (JVM) |
| `<short-sha>` | Exact commit |

Both variants live in the **same repository**; the `-native` suffix is on the tag, not the
repository name. Both are published for `linux/amd64` and `linux/arm64`, and release tags
are signed with [cosign](https://docs.sigstore.dev/).

### Which variant should you run?

|  | JVM (`1.23.0`) | Native (`1.23.0-native`) |
|---|---|---|
| Start-up | Seconds | Milliseconds |
| Memory floor | Higher | Substantially lower |
| Peak throughput on long runs | Typically better (JIT warms up) | Flat |
| Heap tuning | `JAVA_TOOL_OPTIONS` / the chart's `jvm.options` | **Ignores `JAVA_TOOL_OPTIONS`** — see below |
| Runtime base | `eclipse-temurin:21-jre` | `registry.access.redhat.com/ubi9/ubi-minimal` |

**The native variant ignores `JAVA_TOOL_OPTIONS` entirely.** A native binary does not read
it, so the chart's `jvm.options` are silently inert. Its heap comes from the default
`CMD ["-XX:MaximumHeapSizePercent=75"]`, which you override with container `args:` (the
chart's `containerArgs`) or by passing arguments to `docker run`:

```
docker run --rm ghcr.io/bmscomp/kates:1.23.0-native -XX:MaximumHeapSizePercent=50
```

Rule of thumb: **JVM for sustained benchmarking**, where JIT warm-up pays for itself over a
long run; **native for CI gates and short-lived runs**, where start-up dominates.

---

## Quick start

### The intended path — Helm

The backend is designed to be deployed by its chart, which wires up Kafka credentials,
PostgreSQL, probes, RBAC and monitoring:

```
helm install kates oci://ghcr.io/bmscomp/charts/kates \
  --version 0.7.0 \
  --namespace kates --create-namespace \
  --set kafka.bootstrapServers=my-kafka-bootstrap.kafka.svc:9092
```

Or provision the whole stack — Kafka, Kates, chaos tooling, monitoring — with the CLI:

```
brew install bmscomp/tap/kates
kates deploy -i          # interactive wizard
kates health             # verify
kates test create --type LOAD
```

### Kicking the tyres with Docker

To see the API without a cluster, point it at any reachable Kafka:

```
docker run --rm -p 8080:8080 \
  -e KATES_KAFKA_BOOTSTRAP_SERVERS=host.docker.internal:9092 \
  -e KATES_KAFKA_SECURITY_PROTOCOL=PLAINTEXT \
  ghcr.io/bmscomp/kates:latest
```

Then:

```
curl -s localhost:8080/q/health/ready
curl -s localhost:8080/api/cluster | jq
```

Without a PostgreSQL database, anything that persists a run will fail. Supply one with
`QUARKUS_DATASOURCE_JDBC_URL`, `QUARKUS_DATASOURCE_USERNAME` and
`QUARKUS_DATASOURCE_PASSWORD`.

---

## Ports

| Port | Protocol | Purpose |
|---|---|---|
| `8080` | HTTP | REST API, health endpoints, Prometheus metrics |
| `9000` | gRPC | `TestService`, `ClusterService`, `HealthService` |

The chart exposes both. Server reflection is disabled outside dev mode, so bring your own
`.proto` — it is at
[`kates/src/main/proto/kates.proto`](https://github.com/bmscomp/kates/blob/main/kates/src/main/proto/kates.proto).

## Health and probes

| Path | Use |
|---|---|
| `/q/health/started` | Startup probe |
| `/q/health/ready` | Readiness probe |
| `/q/health/live` | Liveness probe (also the image's `HEALTHCHECK`) |
| `/q/metrics` | Prometheus scrape |
| `/api/health` | **Diagnostics only — do not use for probes** |

`/api/health` performs a live Kafka `AdminClient` round-trip on every request. For a tool
whose whole job is to break Kafka, a slow or down broker is a *normal* state — wiring it to
a liveness probe means the container restarts itself precisely when a chaos experiment is
working. Use `/q/health/live`.

## Configuration

Configuration is environment variables, MicroProfile-style. The most commonly set:

| Variable | Default | Purpose |
|---|---|---|
| `KATES_KAFKA_BOOTSTRAP_SERVERS` | `krafter-kafka-bootstrap.kafka.svc:9092` | Cluster under test |
| `KATES_KAFKA_SECURITY_PROTOCOL` | `SASL_PLAINTEXT` | `PLAINTEXT`, `SSL`, `SASL_SSL`, … |
| `KATES_KAFKA_SASL_MECHANISM` | `SCRAM-SHA-512` | SASL mechanism |
| `KATES_KAFKA_SASL_USERNAME` / `_PASSWORD` | `kates-backend` / — | SASL credentials |
| `KATES_KAFKA_SSL_TRUSTSTORE_LOCATION` | — | Mounted truststore path |
| `KATES_ENGINE_DEFAULT_BACKEND` | `native` | `native` or `trogdor` |
| `KATES_API_SECURITY_ENABLED` | `false` | Require an API key |
| `KATES_API_KEY` | — | **Startup fails if security is on and this is unset or `changeme`** |
| `QUARKUS_DATASOURCE_JDBC_URL` | `jdbc:postgresql://postgres.kates.svc:5432/kates` | Results database |
| `QUARKUS_OTEL_ENABLED` | — | OpenTelemetry export |

Defaults for every test type are overridable as `KATES_DEFAULTS_*` and
`KATES_TESTS_<TYPE>_*` (partitions, acks, batch size, linger, compression, record size,
throughput, durations, producer/consumer counts). The full set is generated into the
chart's ConfigMap — see
[`charts/kates/templates/configmap.yaml`](https://github.com/bmscomp/kates/blob/main/charts/kates/templates/configmap.yaml)
and
[`charts/kates/values.yaml`](https://github.com/bmscomp/kates/blob/main/charts/kates/values.yaml).

Two limits worth knowing: at most **3 concurrent test runs** (further requests get
`429` with `Retry-After`), and a **30-minute** ceiling per run. CORS is deliberately
not enabled.

## Security posture

Runs as a **non-root** user. The chart additionally sets `runAsNonRoot`,
`readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`, drops **all**
capabilities and applies the `RuntimeDefault` seccomp profile.

## REST API surface

Top-level resources under `/api`: `audit`, `cluster`, `cost`, `disruptions` (plus
`/playbooks`, `/schedules`, `/templates`), `dlq`, `events`, `health`, `kafka`, `profiles`,
`resilience`, `schedules`, `security`, `share-groups`, `tests`, `trends`, `webhooks`.

Full reference:
[REST API Reference](https://github.com/bmscomp/kates/blob/main/docs/book/11-api-reference.md)
· [gRPC API Reference](https://github.com/bmscomp/kates/blob/main/docs/book/16-grpc-api.md)

---

## Documentation

- **[Kates — The Definitive Guide](https://github.com/bmscomp/kates/blob/main/docs/book/README.md)** — the full book: performance theory, chaos engineering, security, deployment, operations
- [Tutorials](https://github.com/bmscomp/kates/blob/main/docs/tutorials/README.md) — start with [Getting Started](https://github.com/bmscomp/kates/blob/main/docs/tutorials/01-getting-started.md)
- [CLI Reference](https://github.com/bmscomp/kates/blob/main/docs/book/10-cli-reference.md)
- [Deployment Guide](https://github.com/bmscomp/kates/blob/main/docs/book/12-deployment.md)
- [Security & Compliance](https://github.com/bmscomp/kates/blob/main/docs/book/17-security.md)
- [Troubleshooting Index](https://github.com/bmscomp/kates/blob/main/docs/book/appendix-b-troubleshooting.md)
- [Version & Compatibility Matrix](https://github.com/bmscomp/kates/blob/main/docs/book/appendix-d-versions.md)
- [Chart reference](https://github.com/bmscomp/kates/blob/main/charts/kates/README.md)

## Source and build

- Repository — <https://github.com/bmscomp/kates>
- Dockerfile (JVM) — [`kates/Dockerfile`](https://github.com/bmscomp/kates/blob/main/kates/Dockerfile)
- Dockerfile (native) — [`kates/Dockerfile.native`](https://github.com/bmscomp/kates/blob/main/kates/Dockerfile.native)
- Build workflow — [`.github/workflows/publish-docker.yml`](https://github.com/bmscomp/kates/blob/main/.github/workflows/publish-docker.yml)
- Contributing — [`CONTRIBUTING.md`](https://github.com/bmscomp/kates/blob/main/CONTRIBUTING.md)
- Issues — <https://github.com/bmscomp/kates/issues>

## Related images

| Image | Purpose |
|---|---|
| [`bmscomp/connect`](https://hub.docker.com/r/bmscomp/connect) | Kafka Connect — Debezium CDC, Apicurio, Aiven JDBC & S3 |
| [`bmscomp/kates-tester`](https://hub.docker.com/r/bmscomp/kates-tester) | kubectl + Kafka CLI toolbox used by the Helm chart test hooks |

## License

[Apache License 2.0](https://github.com/bmscomp/kates/blob/main/LICENSE) — Copyright 2026 KATES Contributors.
