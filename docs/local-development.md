# Local Development Stack

This guide covers provisioning the full Kates stack on a local Kind cluster with `make`,
managing container images, and working behind a corporate proxy. For the kates-CLI-driven
deployment path (recommended for most users), see the [README Quick Start](../README.md#quick-start)
and the [Installation Guide](book/20-installation-guide.md).

## The `make all` pipeline

Bring up the entire production-grade stack with one command:

```bash
make all
```

The `make all` target executes a deterministic, ten-step provisioning pipeline. Each step is
idempotent and will skip work that has already been completed, making it safe to re-run after
partial failures. The pipeline stages are ordered to satisfy infrastructure dependencies —
monitoring must be operational before Kafka is deployed, so that broker metrics are captured
from the first heartbeat.

| Step | Action | Purpose |
|:----:|:-------|:--------|
| 1 | Create Kind cluster `panda` and local Docker registry | Provisions the Kubernetes control plane and a local OCI registry at `localhost:5001` to avoid external image pulls during development. |
| 2 | Pull all images to local registry | Downloads container images defined in `images.env` into the local registry, ensuring reproducible builds independent of upstream availability. |
| 3 | Load images from registry into Kind nodes | Transfers images from the local registry into the Kind node containerd cache, eliminating pull latency during pod scheduling. |
| 4 | Deploy Prometheus and Grafana | Installs the monitoring stack with pre-configured scrape targets and auto-provisioned Grafana dashboards for Kafka and JVM metrics. |
| 5 | Wait for monitoring readiness | Blocks until all monitoring pods report `Ready`, ensuring metrics collection is active before downstream services start. |
| 6 | Deploy Strimzi Kafka (KRaft mode) | Installs the Strimzi operator and applies the Kafka custom resource with KRaft consensus, rack-aware broker pools, and zone-affinity storage. |
| 7 | Wait for Kafka readiness | Blocks until all Kafka broker pods are `Ready` and the controller quorum is established, verifying cluster health before test workloads begin. |
| 8 | Deploy Kafka UI | Installs a web-based Kafka management interface for topic inspection, consumer group monitoring, and message browsing. |
| 9 | Deploy Apicurio Registry | Installs the Apicurio Schema Registry with KafkaSQL storage, enabling schema governance for Avro, Protobuf, and JSON Schema workloads. |
| 10 | Deploy LitmusChaos | Installs the LitmusChaos operator and applies Kafka-specific RBAC, enabling fault injection experiments against the deployed cluster. |

## Makefile targets

The Makefile provides a comprehensive set of targets for managing the platform lifecycle. Each
target is idempotent and can be invoked independently or composed via dependency chains. Run
`make help` for the authoritative list; the most commonly used targets are documented below.

| Target | Description |
|:-------|:------------|
| `make all` | Executes the full provisioning pipeline: cluster creation, image loading, and deployment of all services in dependency order. |
| `make cluster` | Creates the Kind Kubernetes cluster with multi-zone node labels and the local Docker registry, without deploying any services. |
| `make images` | Pulls all container images defined in `images.env` and loads them into the Kind node cache. |
| `make monitoring` | Deploys the Prometheus and Grafana monitoring stack with auto-provisioned dashboards and alert rules. |
| `make kafka` | Deploys the Strimzi operator and applies the Kafka cluster custom resource in KRaft mode with rack-aware broker pools. |
| `make ui` | Deploys the Kafka UI web interface for topic and consumer group management. |
| `make apicurio` | Deploys the Apicurio Schema Registry with KafkaSQL persistence and schema compatibility enforcement. |
| `make litmus` | Deploys the LitmusChaos operator with Kafka-specific RBAC and pre-built experiment templates. |
| `make chaos-ui` | Establishes a port-forward to the LitmusChaos web interface on `localhost:9091`. |
| `make chaos-experiments` | Applies all pre-configured chaos experiment custom resources to the cluster. |
| `make velero` | Deploys Velero backup with MinIO as the S3-compatible storage backend. |
| `make test` | Runs a standard Kafka performance test producing 1 million messages to validate cluster throughput. |
| `make gameday` | Executes an automated GameDay validation pipeline combining performance tests with chaos experiments. |
| `make chart-lint` | Runs Helm lint validation against all Kates Helm charts to detect template errors. |
| `make ports` | Starts port-forwarding for all core services to localhost. |
| `make status` | Displays the current status of the Kind cluster, deployed services, and pod health. |
| `make destroy` | Destroys the Kind cluster and removes all associated resources, including the local Docker registry. |

## Image management

All images are defined in `images.env` — the single source of truth.

```bash
./scripts/pull-images.sh               # Pull all images (skips cached)
./scripts/load-images-to-kind.sh       # Load into Kind (skips loaded)
make registry-status                   # Check registry contents
```

## Working behind a corporate proxy

Define `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` either in your shell or in
`proxy/proxy.conf` before running `./scripts/load-images-to-kind.sh`. The script forwards
these variables into Kind node `ctr pull` commands.

If you do not use the load script, run `make cluster` (or `./scripts/start-cluster.sh`) after
setting proxy variables. Cluster startup reconciles containerd proxy settings on all Kind
nodes so normal pod image pulls work through the proxy as well.

You can also pass proxy params directly:

```bash
./scripts/start-cluster.sh \
  --http-proxy http://proxy.example.com:8080 \
  --https-proxy http://proxy.example.com:8080 \
  --no-proxy "localhost,127.0.0.1,.svc,.cluster.local"
```

Note: loopback proxies from environment/proxy.conf (for example `http://127.0.0.1:9000`) are
ignored by default because Kind nodes cannot reach their own loopback as your host proxy. If
you pass a loopback URL explicitly via `--http-proxy/--https-proxy`, it is rewritten to
`host.docker.internal`.
