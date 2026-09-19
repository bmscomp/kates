# kates-tester — the Kates chart toolbox

**kubectl, the Apache Kafka CLI and a network-diagnostics kit in one small image. The
container the Kates Helm charts run their test hooks in.**

[![License](https://img.shields.io/badge/license-Apache%202.0-blue?logo=apache)](https://github.com/bmscomp/kates/blob/main/LICENSE)
[![Source](https://img.shields.io/badge/source-github.com%2Fbmscomp%2Fkates-181717?logo=github)](https://github.com/bmscomp/kates)

---

## Read this first — the name is misleading

**`kates-tester` does not run tests and generates no load.** It is not a workload generator,
not a benchmark harness, and not a smaller version of the Kates backend.

It is a **toolbox**: a shell with `kubectl` and the Kafka CLI scripts already on the
`PATH`. The Kates Helm charts mount it into their `helm test` pods and their pre-install
hook Jobs, and *those charts* supply the commands. On its own it starts a bash prompt and
waits.

If you are looking for the thing that produces load and grades the result, that is
[`bmscomp/kates`](https://hub.docker.com/r/bmscomp/kates).

---

## What's inside

| Tool | Version | Notes |
|---|---|---|
| `kubectl` | latest stable at build time | From `dl.k8s.io/release/stable.txt` — **not pinned** |
| Apache Kafka CLI | **3.7.0** (Scala 2.13) | `/opt/kafka/bin` on `PATH` — `kafka-topics.sh`, `kafka-console-producer.sh`, `kafka-consumer-groups.sh`, `kafka-producer-perf-test.sh`, … |
| `kcat` | Debian bookworm | The Kafka swiss-army knife |
| `jq` | Debian bookworm | JSON parsing for `kubectl -o json` |
| `netcat-openbsd` | Debian bookworm | `nc -zv` port probes |
| `dnsutils` | Debian bookworm | `dig`, `nslookup` — service discovery debugging |
| `curl`, `ca-certificates`, `bash` | Debian bookworm | |
| `default-jre-headless` | Debian bookworm | Required by the Kafka shell scripts |

**Base:** `debian:bookworm-slim` · **User:** UID 1000 (`kates`) · **Workdir:** `/app` ·
**Entrypoint:** none; `CMD ["bash"]`

Only the Kafka CLI version is pinned (via the `KAFKA_VERSION` build arg, which also feeds
the image's own OCI label so the two cannot drift). `kubectl` tracks upstream stable and
the Debian packages are whatever bookworm ships — this is a diagnostics image, and
freshness matters more than reproducibility.

> The Kafka **CLI** being 3.7.0 does not limit which brokers you can talk to. The 3.7 tools
> speak to modern brokers fine; they are here to drive tests, not to define a support
> matrix.

---

## What it actually does, in practice

Every use is a chart mounting it and supplying a command. Four real examples from this repo:

**1. Waiting for a custom resource to go Ready** — `mirror-maker2` chart test:

```
kubectl wait kafkamirrormaker2/${MM2} -n ${NS} --for=condition=Ready --timeout=240s
```

**2. Checking Kafka cluster health** — `kafka-cluster` chart test:

```
kubectl get kafka $CLUSTER -n $NAMESPACE \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}'
```

**3. A real produce/consume round-trip over SASL** — the one place the whole toolchain is
used at once: `kubectl` discovers the listener, `nc` probes it, a `client.properties` is
written with `SCRAM-SHA-512`, then

```
echo "${MSG}" | kafka-console-producer.sh ...
RECEIVED=$(timeout 20 kafka-console-consumer.sh ...)
```

**4. A CRD-upgrade hook Job** — `pre-install`/`pre-upgrade`, applying updated Strimzi CRDs
before the chart's own resources land. Not a test at all.

## Where the charts wire it in

Referenced as `testImages.kubectl` (and `testImages.kafka`) in:

| Chart | Used by |
|---|---|
| [`kafka-cluster`](https://github.com/bmscomp/kates/blob/main/charts/kafka-cluster/README.md) | The numbered test suite in `templates/tests/` — connectivity, produce/consume, authorization, KRaft quorum, topics, listeners, node pools, Cruise Control, metrics, performance, tiered storage — plus the profiler pod. (`test-00-egress.yaml` is the tests' NetworkPolicy, not a pod, so it uses no image.) |
| [`strimzi-operator`](https://github.com/bmscomp/kates/blob/main/charts/strimzi-operator/README.md) | Operator test, CRD-upgrade hook |
| [`connect-cluster`](https://github.com/bmscomp/kates/blob/main/charts/connect-cluster/README.md) | Connect health test, KafkaUser secret-sync Job |
| [`mirror-maker2`](https://github.com/bmscomp/kates/blob/main/charts/mirror-maker2/README.md) | MM2 readiness test, secret-sync Job |

Override it like any other value:

```yaml
testImages:
  kubectl: ghcr.io/bmscomp/kates-tester:1.22.0
  kafka:   ghcr.io/bmscomp/kates-tester:1.22.0
```

---

## Using it directly

### As an in-cluster debug pod

This is the most useful thing you can do with it:

```
kubectl run kafka-debug -n kafka --rm -it --restart=Never \
  --image=ghcr.io/bmscomp/kates-tester:latest -- bash
```

Then, inside:

```
dig krafter-kafka-bootstrap.kafka.svc.cluster.local
nc -zv krafter-kafka-bootstrap.kafka.svc 9092
kafka-topics.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc:9092 --list
kcat -b krafter-kafka-bootstrap.kafka.svc:9092 -L
```

For an authenticated cluster, write a properties file first:

```
cat > /tmp/client.properties <<'EOF'
security.protocol=SASL_PLAINTEXT
sasl.mechanism=SCRAM-SHA-512
sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="user" password="pass";
EOF
kafka-topics.sh --bootstrap-server krafter-kafka-bootstrap.kafka.svc:9092 \
  --command-config /tmp/client.properties --list
```

### Locally

```
docker run --rm -it ghcr.io/bmscomp/kates-tester:latest bash
```

`kubectl` will have no kubeconfig unless you mount one:

```
docker run --rm -it -v "$HOME/.kube:/home/kates/.kube:ro" \
  ghcr.io/bmscomp/kates-tester:latest bash
```

---

## Tags

| Tag | Meaning |
|---|---|
| `1.22.0` | Release version — what the charts pin |
| `latest` | Latest default-branch build |
| `sha-<short-sha>` | Exact commit |

`linux/amd64` and `linux/arm64`.

Charts pin an exact version rather than `latest` on purpose: a test hook that silently
changes tooling underneath you turns a green suite red for reasons unrelated to your change.

## Security notes

Runs as **UID 1000**, non-root. The chart test pods tighten this further — several run as
UID 65534 with `readOnlyRootFilesystem: true` and all capabilities dropped, and the
`connect-cluster` chart applies a `NetworkPolicy` restricting test pods to port 8083 and DNS.

Because it ships `kubectl`, give it only the RBAC a given hook needs. The charts' test
ServiceAccounts are scoped to the specific resources their checks read.

---

## Documentation

- [Kates — The Definitive Guide](https://github.com/bmscomp/kates/blob/main/docs/book/README.md)
- [Troubleshooting Index](https://github.com/bmscomp/kates/blob/main/docs/book/appendix-b-troubleshooting.md) — where this image earns its keep
- [Deployment Guide](https://github.com/bmscomp/kates/blob/main/docs/book/12-deployment.md)
- [Installing Kafka with the kafka-cluster chart](https://github.com/bmscomp/kates/blob/main/docs/book/20-installation-guide.md)
- [Local development](https://github.com/bmscomp/kates/blob/main/docs/local-development.md)

## Source and build

- Repository — <https://github.com/bmscomp/kates>
- Dockerfile — [`tester/Dockerfile`](https://github.com/bmscomp/kates/blob/main/tester/Dockerfile)
- Build workflow — [`.github/workflows/publish-tester.yml`](https://github.com/bmscomp/kates/blob/main/.github/workflows/publish-tester.yml)
- Issues — <https://github.com/bmscomp/kates/issues>

## Related images

| Image | Purpose |
|---|---|
| [`bmscomp/kates`](https://hub.docker.com/r/bmscomp/kates) | The Kates backend — Kafka performance and chaos testing |
| [`bmscomp/connect`](https://hub.docker.com/r/bmscomp/connect) | Kafka Connect — Debezium CDC, Apicurio, Aiven JDBC & S3 |

## License

[Apache License 2.0](https://github.com/bmscomp/kates/blob/main/LICENSE) — Copyright 2026 KATES Contributors.
Bundled tools carry their own licences.
