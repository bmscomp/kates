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

The `make all` target is an interactive orchestrator: it verifies the cluster is reachable,
builds the Kates CLI if needed, prompts for a topology (single or isolated namespaces), and
delegates provisioning to `kates deploy`. For a manual bring-up, the individual targets below
compose the same stack step by step. Each step is idempotent and will skip work that has
already been completed, making it safe to re-run after partial failures. The stages are
ordered to satisfy infrastructure dependencies — monitoring must be operational before Kafka
is deployed, so that broker metrics are captured from the first heartbeat.

| Step | Action | Purpose |
|:----:|:-------|:--------|
| 1 | Create Kind cluster `panda` and local Docker registry | Provisions the Kubernetes control plane and a local OCI registry at `localhost:5001` to avoid external image pulls during development. |
| 2 | Pull all images to local registry | Downloads container images defined in `images.env` into the local registry, ensuring reproducible builds independent of upstream availability. |
| 3 | Load images from registry into Kind nodes | Transfers images from the local registry into the Kind node containerd cache, eliminating pull latency during pod scheduling. |
| 4 | Deploy Prometheus and Grafana | Installs the monitoring stack with pre-configured scrape targets and auto-provisioned Grafana dashboards for Kafka and JVM metrics. |
| 5 | Wait for monitoring readiness | Blocks until all monitoring pods report `Ready`, ensuring metrics collection is active before downstream services start. |
| 6 | Deploy Strimzi Kafka (KRaft mode) | Installs the Strimzi operator from `charts/strimzi-operator` as its own release, waits for the CRDs to be `Established`, and only then applies the Kafka custom resource with KRaft consensus, rack-aware broker pools, and zone-affinity storage. The operator is a separate release because a `Kafka` resource cannot validate before its CRD exists. |
| 7 | Wait for Kafka readiness | Blocks until all Kafka broker pods are `Ready` and the controller quorum is established, verifying cluster health before test workloads begin. |
| 8 | Deploy Kafka UI | Installs a web-based Kafka management interface for topic inspection, consumer group monitoring, and message browsing. |
| 9 | Deploy Apicurio Registry | Installs the Apicurio Schema Registry with KafkaSQL storage, enabling schema governance for Avro, Protobuf, and JSON Schema workloads. |
| 10 | Deploy LitmusChaos | Installs the LitmusChaos operator and applies Kafka-specific RBAC, enabling fault injection experiments against the deployed cluster. |

## Makefile targets

The Makefile provides a comprehensive set of targets for managing the platform lifecycle. Each
target is idempotent and can be invoked independently or composed via dependency chains. Run
`make help` for the authoritative list; the most commonly used targets are documented below.

### Provisioning

| Target | Description |
|:-------|:------------|
| `make all` | Interactive full provisioning: cluster checks, CLI build, topology prompt, then `kates deploy`. |
| `make cluster` | Creates the Kind Kubernetes cluster with multi-zone node labels and the local Docker registry, without deploying any services. |
| `make monitoring` | Deploys the Prometheus and Grafana monitoring stack. Detects the provider and picks the matching overlay; `make monitoring-generic` skips detection and uses `values-generic.yaml`. |
| `make kafka` | Shorthand for `make kafka-deploy`. Fetches the `kafka-cluster` dependencies, then runs `scripts/deploy-kafka-generic.sh`, which reconciles the Strimzi operator from `charts/strimzi-operator` before the Kafka cluster and layers `values-platform.yaml`. |
| `make connect-deploy` | Fetches the `connect-cluster` dependencies, then installs the Connect cluster into the `kafka` namespace with the `ENV` overlay when one exists. |
| `make ui` | Deploys the Kafka UI web interface for topic and consumer group management. |
| `make apicurio` | Deploys the Apicurio Schema Registry with KafkaSQL persistence and schema compatibility enforcement. |
| `make litmus` | Deploys the Kates Chaos stack (the LitmusChaos execution plane) with the Kind overlay. |
| `make chaos-ui` | Explains how to drive chaos — the chart ships the execution plane only, with no web portal to port-forward. Use `make chaos-status` to inspect state. |
| `make velero` | Deploys Velero backup with MinIO as the S3-compatible storage backend. |
| `make ports` | Starts port-forwarding for all core services to localhost. |
| `make status` | Displays the current status of the Kind cluster, deployed services, and pod health. |
| `make destroy` | Destroys the Kind cluster and removes all associated resources, including the local Docker registry. `FORCE=1` skips the prompt; `make clean` is an alias. |

### Chart dependencies

Every Kafka chart now declares at least one dependency, and **nothing renders until it is fetched** — not `helm template`, not `helm upgrade`. `strimzi-operator` pulls the upstream operator chart from `quay.io`; `kafka-cluster`, `connect-cluster` and `mirror-maker2` each resolve the `kafka-common` library through a `file://` dependency. These targets are the shortest way to get that right, and the lint, template, unit-test and package targets already depend on them:

| Target | Description |
|:-------|:------------|
| `make kafka-chart-deps` | Fetches the `kafka-cluster` dependencies — the `kafka-common` library and the SeaweedFS subchart, which is fetched whether or not `seaweedfs.enabled` is set, because `condition:` gates rendering rather than resolution. |
| `make connect-chart-deps` | Builds the `connect-cluster` chart's `kafka-common` dependency. |
| `make mm2-chart-deps` | Builds the `mirror-maker2` chart's `kafka-common` dependency. |
| `make platform-chart-deps` | Fetches the `kates-platform` umbrella's dependencies, after building `kafka-cluster`'s, `connect-cluster`'s and `mirror-maker2`'s — the umbrella packages its `file://` subcharts as they are on disk, so their own dependencies have to be in place first. |

There is no `strimzi-chart-deps`; run `helm dependency build charts/strimzi-operator` directly, as `scripts/deploy-kafka.sh` does.

### Chart validation

| Target | Description |
|:-------|:------------|
| `make chart-lint` | Lints the `kates` application chart with `--strict`, plus `ct lint` when chart-testing is installed. |
| `make kafka-chart-lint` | Lints `kafka-cluster` bare and across six overlay combinations, including the platform profile layered before `values-prod.yaml`. |
| `make connect-chart-lint` / `make mm2-chart-lint` | Lint `connect-cluster` (bare, prod, kind) and `mirror-maker2` (bare only, plus `legacy-kafka`). Neither covers every overlay the chart ships — `make check-chart-matrix` is what renders the full set. |
| `make kafka-common-test` | Lints the `kafka-common` library and runs its helm-unittest suites through the harness chart in `charts/kafka-common/tests/harness`. A library chart cannot be rendered on its own, which is what the harness is for. |
| `make kafka-chart-unittest` / `make connect-chart-unittest` | Run the helm-unittest suites in `charts/<chart>/tests/`. Both require the plugin at the `HELM_UNITTEST_VERSION` pinned in `versions.env`. |
| `make check-chart-matrix` | Renders `strimzi-operator`, `kafka-cluster`, `connect-cluster` and `mirror-maker2` across every documented shape and checks each render against the pinned Strimzi CRDs, plus the safety rails. Runs offline locally. |
| `make check-metric-contract` | Verifies every series an alert or dashboard reads is one the charts' exporter rules produce. |
| `make check-versions` | Verifies the Strimzi, Kafka, Connect-image and local-toolchain pins agree across every file that carries one. |
| `make mm2-chart-guards` | Asserts the `mirror-maker2` safety rails reject bad input — and for the right reason, not by accident. |

Install the unit-test plugin once:

```bash
helm plugin install https://github.com/helm-unittest/helm-unittest \
  --version "$(grep -E '^HELM_UNITTEST_VERSION=' versions.env | cut -d'"' -f2)"
```

### Testing

| Target | Description |
|:-------|:------------|
| `make test` | Runs a standard Kafka performance test producing 1 million messages to validate cluster throughput. |
| `make test-unit` | Runs the Java and Go unit tests, with no Docker required. |
| `make gameday` | Executes an automated Game Day validation pipeline combining performance tests with chaos experiments. |
| `make helm-test-all` | Runs the Helm tests across every deployed component, with a summary. |

## Image management

All images are defined in `images.env` — the single source of truth.

```bash
./scripts/pull-images.sh               # Pull all images (skips cached)
./scripts/load-images-to-kind.sh       # Load into Kind (skips loaded)
./scripts/registry-status.sh           # Check registry contents
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
