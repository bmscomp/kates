<div align="center">
  <picture>
    <source media="(prefers-color-scheme: light)" srcset="docs/assets/kates-wordmark.svg" />
    <img src="docs/assets/kates-wordmark-dark.svg" width="620" alt="Kates — Kafka advanced testing and engineering suite" />
  </picture>

  <p>
    Performance testing, chaos engineering and security auditing<br/>
    for Apache Kafka on Kubernetes.
  </p>

  <p>
    <a href="https://github.com/bmscomp/kates/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/bmscomp/kates/ci.yml?branch=main&label=backend%20CI&logo=github" alt="Backend CI status" /></a>
    <a href="https://github.com/bmscomp/kates/releases/latest"><img src="https://img.shields.io/github/v/release/bmscomp/kates?label=release&logo=github" alt="Latest release" /></a>
    <a href="https://github.com/marketplace/actions/kates-quality-gate"><img src="https://img.shields.io/badge/marketplace-kates--quality--gate-blue?logo=githubactions&logoColor=white" alt="GitHub Marketplace" /></a>
    <a href="LICENSE"><img src="https://img.shields.io/github/license/bmscomp/kates" alt="License" /></a>
    <a href="https://bmscomp.github.io/kates/"><img src="https://img.shields.io/badge/docs-the%20book-1A5BCC" alt="The Kates book" /></a>
  </p>
</div>

You find out how a Kafka cluster handles a broker failure on the day a broker fails — unless you have already tried it. Kates puts load on Apache Kafka, injects faults into its brokers, measures what they cost in throughput, P99 latency, recovery time and lost records, and audits broker security configuration. It is for teams that run Kafka on Kubernetes and want those measurements to be repeatable, on a local Kind cluster or as pass/fail gates in a CI pipeline.

Performance tests work against any Kafka cluster the Kates API can reach; fault injection targets Strimzi-managed brokers, by default the cluster `krafter` in namespace `kafka`. Kates has three parts: the `kates` CLI (Go), the Kates API (Quarkus) that runs the tests and faults and keeps the results in PostgreSQL, and the signed Helm charts that install Kafka and everything around it.

**[The book](https://bmscomp.github.io/kates/)** · [Why Kates](#why-kates) · [Quick start](#quick-start) · [CLI at a glance](#cli-at-a-glance) · [GitHub Action](#github-action) · [Architecture](#architecture) · [Documentation](#documentation) · [Helm charts](#helm-charts) · [Building from source](#building-from-source) · [Contributing](#contributing)

<p align="center">
  <img src="docs/assets/demo.gif" width="900" alt="Animated terminal demo of kates health, kates test create --type LOAD --records 100000 --wait, kates gate --min-grade C, and kates security audit running against a live Kafka cluster" />
</p>

## What it does

- **Performance tests.** Eight workload types (`LOAD`, `STRESS`, `SPIKE`, `ENDURANCE`, `VOLUME`, `CAPACITY`, `ROUND_TRIP`, `INTEGRITY`); `kates test types` lists every type the Kates API accepts. The default `native` benchmark backend runs Kafka producers and consumers inside the Kates API on virtual threads; `--backend trogdor` hands the workload to a Trogdor coordinator you run yourself. Reports give throughput, latency percentiles (`P50`, `P95`, `P99`) and error rate, and export as CSV, JUnit XML, Markdown, HTML or latency heatmaps.
- **Fault injection.** Thirteen disruption types, from broker pod kills and network partitions to CPU, memory and I/O stress, DNS errors, disk fill and node drain (`kates disruption types`). The default install runs eleven of them through LitmusChaos. Network latency and disk fill time out, because the `kates-chaos` chart ships no Litmus experiment for them (see `experiments.definitions` in its values). The direct Kubernetes chaos provider covers eight.
- **Resilience runs and disruption plans.** `kates resilience run` pairs one load test with one fault and reports the before/after change and the recovery time. `kates disruption run` executes multi-step disruption plans; `kates disruption playbook list` shows the built-in playbooks. Plans get blast-radius checks, a dry run, ISR and consumer-lag tracking, and an A–F SLA grade against absolute latency, throughput and recovery-time thresholds.
- **Security audit.** `kates security audit` grades broker configuration A–F. Related commands map the checks to CIS, SOC 2 and PCI DSS, save a baseline and report drift from it.
- **CI gates.** `kates gate --min-grade B` runs a load test and exits non-zero below the performance grade, `kates test apply -f <scenario> --wait` fails when a scenario violates its SLA gates, and `kates security gate` does the same for the security grade. Finished tests can notify webhooks (`kates webhook add`).
- **A multi-zone lab on one machine.** A three-node Kind cluster (`panda`) whose nodes (`alpha`, `sigma`, `gamma`) sit in separate zones. On it, `kates deploy` gives each zone its own StorageClass and broker pool and turns on rack awareness, so a full zone failure can be rehearsed locally.
- **Metrics and dashboards.** The Kates API exposes Prometheus metrics, the `monitoring` chart adds twelve Grafana dashboards for Kafka, Kates and chaos runs, and the charts ship Prometheus alert rules. The Kates API also exports OTLP traces, though the default install has no Jaeger to receive them; [Observability](docs/book/09-observability.md) covers the dashboards, the alerts and tracing.

## Why Kates

Most teams validate Kafka resilience by running `kafka-producer-perf-test` in one terminal, deleting a pod in another, and eyeballing a Grafana board. The table below compares that workflow and standalone benchmark tools against Kates:

| Capability | `kafka-producer-perf-test` | OpenMessaging Benchmark | Manual Litmus / `kubectl` | Kates |
|:-----------|:---------------------------|:------------------------|:--------------------------|:------|
| Load, stress, spike and round-trip workloads | Producer or consumer only | Yes | No | Yes (`native` virtual threads or Trogdor) |
| Coordinated fault injection under load | No | No | Manual | Yes (`kates resilience` and `kates disruption`) |
| Blast-radius safety guard and rollback | No | No | No | Yes (`maxAffectedBrokers`, auto-rollback) |
| Zero-loss, ordering and gap verification | No | No | No | Yes (`INTEGRITY` test type) |
| Broker security and compliance audit | No | No | No | Yes (A–F grade, CIS / SOC 2 / PCI DSS) |
| Repeatable CI gates and historical trends | No | No | No | Yes (`kates gate`, JUnit XML, PostgreSQL history) |

## Quick start

This path builds the whole stack on a local Kind cluster. You need:

- a clone of this repository, for the `make` targets, which are read relative to the working directory, so run every command from the repository root;
- Docker (any daemon the `docker` CLI talks to), [Kind](https://kind.sigs.k8s.io/), `kubectl` and Helm, at the minimum versions in the book's [Prerequisites](docs/book/12-deployment.md#prerequisites);
- about 16 GB of RAM, with at least 10 GB of it available to Docker, on macOS or Linux (amd64 or arm64).

```bash
# 1. The CLI (macOS or Linux), and a checkout of the same release for the make targets
brew install bmscomp/tap/kates
git clone https://github.com/bmscomp/kates && cd kates
git checkout "$(git describe --tags --abbrev=0 --match 'v*')"

# 2. A three-zone Kind cluster named panda, and the release's native backend image loaded into it
make cluster
make kates-native

# 3. The stack: a wizard on a terminal; --dry-run prints the plan and installs nothing
kates deploy

# 4. Port-forwards in the background; a CLI context named ports, with the API URL and key, becomes current
kates ports

# 5. A first load test
kates health
kates test create --type LOAD --records 100000 --wait
```

Run a CI quality gate against a minimum grade:

```bash
kates gate --min-grade C --records 100000
```

<details>
<summary><strong>Notes on releases, cluster contexts, images and API keys</strong></summary>

- **Release or `main`.** Homebrew, the release tarballs and `make kates-native` all give the latest release, so step 1 checks out that release's charts to match. Without Homebrew, download `kates-<os>-<arch>.tar.gz` from the [latest release](https://github.com/bmscomp/kates/releases/latest); it holds a single `kates` binary. The Homebrew `kates` installs the charts its release published, the same versions as the tag you check out; a `kates` built from source installs the checkout's charts. To run `main` instead, skip the `git checkout` and build both halves from the checkout: `make cli-install` for the CLI and `make kates-image-native-local` for the Kates API image (see [Building from source](#building-from-source)).
- **Which cluster.** `kates deploy` installs into kubectl's current context. `make cluster` switches that to `kind-panda` only when it creates the cluster, so check `kubectl config current-context` and run `kubectl config use-context kind-panda` if needed. Before deploying, `kates deploy` probes every context in your kubeconfig: with none reachable it offers to create the Kind cluster, and with several it asks which one, but it does not switch to the one you pick. `-y` never prompts, so it fails where it would have asked.
- **The backend image.** On Kind, `kates deploy` runs a local native image, `kates:native-local` if present and otherwise `kates:native`, and never pulls one. Without either, the deploy stops at the backend step, after Kafka and monitoring are installed. `make kates-native` pulls the published image of the release that `charts/kates/Chart.yaml` names, and compiles one only if the pull fails; it keeps a `kates:native` already on the machine, so run `docker rmi kates:native` first when you change releases. `make kates-image-native-local` compiles your working tree instead (several minutes, about 8 GB of Docker memory). Both load the image into `panda`, so create the cluster first.
- **The API key.** Every `/api` call except `/api/health` needs the key from Secret `kates-api-key` in namespace `kates` (`kates-stack` with `--topology single`). `kates deploy` writes it into the active CLI context unless that context already holds a key kates did not put there, and `kates ports` into a context of its own, `ports`; a context you create yourself needs `--api-key`. Because `/api/health` is public, a green `kates health` does not prove the key works; `kates test list` does.
- **`make all`** is an interactive wrapper: it creates the Kind cluster if no cluster is reachable, asks for a topology, runs `kates deploy` and then the `make ports` script, which uses the second port map below. With the single-namespace topology that script finds nothing to forward, so run `kates ports` afterwards. It uses the `kates` on your `PATH` and does not fetch the backend image, so do step 1 and `make cluster && make kates-native` first.

</details>

### What `kates deploy` installs

With the default isolated topology:

| Component | Helm release | Namespace |
|:--|:--|:--|
| Strimzi operator | `strimzi-operator` | `strimzi-operator` |
| cert-manager | `cert-manager` | `cert-manager` |
| Kafka | `krafter` | `kafka` |
| Prometheus and Grafana | `monitoring` | `monitoring` |
| Apicurio Registry | `apicurio` | `kafka` |
| Kates backend, with its PostgreSQL | `kates` | `kates` |
| Kafka UI | `kafka-ui` | `kafka` |
| LitmusChaos | `chaos` | `litmus` |

Kafka Connect, MirrorMaker 2, Kyverno and the secret manager are opt-in (`--with-kafka-connect`, `--with-mirror-maker2`, `--with-kyverno`, `--with-secret-manager`). `--topology single` puts everything except the operators in one namespace, `kates-stack`. After a failure, fix the cause and run `kates deploy` again: it skips or upgrades the components already installed.

### Reach the services

Kind publishes no NodePorts on the host, so every `localhost` address is a port-forward. There are two forwarders, with different port maps; the sign-ins are those of a Kind install.

| Service | `kates ports` | `make ports` | Sign-in |
|:--|:--|:--|:--|
| Kates API | http://localhost:8080 | http://localhost:30083 | API key |
| Kafka UI | http://localhost:8085 | http://localhost:30081 | `admin`, password in Secret `kafka-ui-web-password` (namespace `kafka`) |
| Grafana | http://localhost:3000 | http://localhost:30080 ¹ | `admin` / `admin` |
| Prometheus | http://localhost:30090 | http://localhost:30090 ¹ | none |

¹ `make ports` looks for Grafana and Prometheus in namespace `kafka`. With the default isolated topology they are in `monitoring`, so run `MONITORING_NS=monitoring make ports`. With `--topology single` everything is in `kates-stack`: set `KAFKA_NS`, `KATES_NS` and `MONITORING_NS` to `kates-stack`, or use `kates ports`, which finds the services in any namespace.

`kates ports` first stops every `kubectl port-forward` on the machine, then forwards in the background, points the CLI context `ports` at `http://localhost:8080` with the key from the Secret, and makes `ports` the current context. It changes no other context, keeps a key you set in `ports` yourself, and says when the API rejects the key. `make ports` leaves contexts alone; the CLI switches between `localhost:8080` and `localhost:30083` when only one of them answers. Stop either set with `pkill -f kubectl.port-forward`. To read the credentials yourself:

```bash
kubectl get secret kates-api-key -n kates -o jsonpath='{.data.api-key}' | base64 -d           # Kates API key
kubectl get secret kafka-ui-web-password -n kafka -o jsonpath='{.data.password}' | base64 -d  # Kafka UI, user admin
```

Kafka UI can only read: its Kafka user has Describe and Read ACLs, so the brokers reject writes. Apicurio Registry has no web console, and neither forwarder finds its API; forward it with `kubectl -n kafka port-forward svc/apicurio-apicurio-registry 8081:80`. Litmus has no UI either: faults run through `kates disruption` and `kates resilience`, and `make chaos-status` lists the ChaosExperiments, ChaosEngines and ChaosResults in namespace `kafka`. The book's [Access Points](docs/book/03-cluster.md#access-points) section has more.

### Tear down

```bash
kates clean --force   # uninstall the stack and keep the cluster; without --force it asks first
make destroy          # delete the Kind cluster panda (FORCE=1 skips the prompt)
```

`kates clean` works on kubectl's current context and names it before asking. It removes the Strimzi, Litmus, Prometheus Operator, cert-manager and Kyverno CRDs, and the cert-manager, Kyverno and Strimzi operators, unless objects of their kinds exist outside the namespaces it deletes; it keeps a namespace that also holds a Helm release it does not install. It lists what it keeps and why. Once you confirm, it stops the `kubectl port-forward` processes into the namespaces it deletes.

## CLI at a glance

```bash
kates deploy -i                                             # Deployment wizard
kates ports                                                 # Port-forward services; point the ports context at the API
kates health                                                # Backend health and Kafka connectivity
kates kafka tui                                             # Full-screen Kafka explorer
kates test create --type LOAD --records 100000 --wait       # Run a load test and print the results
kates benchmark                                             # LOAD, STRESS and SPIKE, each graded A–F
kates resilience run -f cli/examples/resilience-test.yaml   # Load test with a broker pod kill
kates disruption playbook list                              # Built-in multi-step fault plans
kates report show <id>                                      # Throughput, latency percentiles, error rate
kates test apply -f scenarios/ci-gate.yaml --wait           # Scenarios with SLA checks; exits 1 on a violation
kates gate --min-grade B                                    # CI gate: run a LOAD test, exit 1 below grade B
kates security audit                                        # Security configuration audit, graded A–F
kates dashboard                                             # Live full-screen dashboard
kates mcp --context ports --allow-cluster <clusterId>       # Read-only MCP server for AI agents (experimental)
```

`kates gate` grades average throughput and p99 latency against fixed bands, the same for every test type. The SLA verdict in `kates report show` checks only thresholds stored with the run, and runs from `kates test create` carry none; to check thresholds, put them in a scenario's `validate:` block and run it with `kates test apply`.

`kates ctx` points the CLI at other Kates instances. Contexts are Kates API profiles kept in `~/.kates.yaml`, not Kubernetes contexts; each takes a URL and an `--api-key`, and `kates ctx show` lists them. `kates <command> --help` documents every command.

## GitHub Action

This repository is also a GitHub Action, **Kates Quality Gate**. It installs the `kates` CLI on the runner, runs `kates gate` against your Kates API, and fails the step when the grade is below `min-grade`. The grade, throughput, p99 latency and run ID go to the job summary and to the step's outputs.

```yaml
- name: Kafka performance gate
  id: gate
  uses: bmscomp/kates@v1.31.0
  with:
    api-url: ${{ vars.KATES_URL }}
    api-key: ${{ secrets.KATES_API_KEY }}
    min-grade: B
    records: 100000

- if: always()
  run: echo "Grade ${{ steps.gate.outputs.grade }}, passed ${{ steps.gate.outputs.passed }}"
```

| Input | Default | What it sets |
|---|---|---|
| `api-url` | `http://localhost:8080` | The Kates API the runner calls |
| `api-key` | none | API key, from Secret `kates-api-key` |
| `min-grade` | `C` | Lowest passing grade, `A` to `F` |
| `type` | `LOAD` | Workload: `LOAD`, `STRESS`, `SPIKE`, `ENDURANCE`, `VOLUME`, `CAPACITY`, `ROUND_TRIP` or `INTEGRITY` |
| `records` | `50000` | Records to produce |
| `backend` | the API's default | `native` or `trogdor` |
| `timeout` | `180` | Seconds to wait for the run |
| `version` | `latest` | CLI release to install; a `kates` already on the `PATH` is used instead |

Outputs: `run-id`, `grade`, and `passed` (`true` or `false`).

The runner has to reach the Kates API. The default `http://localhost:8080` suits a self-hosted runner beside the cluster after `kates ports`; a GitHub-hosted runner needs the API exposed to it. The action runs on Linux and macOS runners, amd64 or arm64, and needs `curl`, `tar` and `python3`. Pin a release tag such as `v1.31.0`; there is no moving `v1` tag.

## Other clusters

`kates deploy` also installs on any cluster your kubeconfig reaches, and there the Homebrew `kates` needs no checkout: it pulls the charts its release published. There the backend runs the published `ghcr.io/bmscomp/kates` image and the charts use their generic overlays, so Grafana gets a random password, stored in Secret `monitoring-grafana` under `admin-password`. The generated Kafka values also add an external listener on port 9094 with TLS and SCRAM-SHA-512. It sits behind a load balancer with no internal-only annotation on EKS and GKE, an internal load balancer on AKS, and a NodePort elsewhere. `kates deploy --dry-run` writes those values without installing anything and prints where, so you can review the listener first: `.build/values-detected.yaml` in a checkout, `~/.local/state/kates/deploy/values-detected.yaml` anywhere else. The book's [Deployment](docs/book/12-deployment.md#cloud-deployment) chapter covers the EKS, GKE and AKS overlays.

## Architecture

```mermaid
flowchart LR
    cli["kates CLI"] -->|"REST, API key"| backend["Kates backend"]
    backend --> db[("PostgreSQL")]
    backend -->|"producers, consumers, AdminClient"| kafka["Strimzi Kafka cluster"]
    backend -.->|"optional"| trogdor["Trogdor coordinator"] -.->|"agents"| kafka
    backend -->|"ChaosEngine resources"| litmus["Litmus operator"] -->|"faults"| kafka
    backend -->|"Kubernetes API"| kafka
    prom["Prometheus"] -->|"scrapes /q/metrics"| backend
```

The CLI and its full-screen views talk to the backend over REST. The backend also streams events as Server-Sent Events and serves a smaller, unary-only gRPC API on the same port. Load comes from a `BenchmarkBackend` (the in-process engine or Trogdor) and faults from a `ChaosProvider` (Litmus by default, the Kubernetes API, or a hybrid that picks Litmus when its CRDs are present). The [Architecture](docs/book/02-architecture.md) chapter covers the orchestrators, the disruption pipeline and the data model.

## Documentation

| Where | What |
|:--|:--|
| [The book](https://bmscomp.github.io/kates/) ([source](docs/book/README.md)) | Concepts, the CLI, the REST and gRPC APIs, deployment, security, upgrades and troubleshooting |
| [Tutorials](docs/tutorials/README.md) | Step-by-step labs: a first test, every test type, chaos, CI integration, Kafka Connect, MirrorMaker 2 migrations, dashboards |
| [CLI reference](docs/book/10-cli-reference.md) | Commands, flags and contexts |
| [REST API](docs/book/11-api-reference.md) · [gRPC API](docs/book/16-grpc-api.md) | Endpoints, authentication, exports |
| [Chaos in practice](docs/book/07-chaos-practice.md) | Disruption types, providers, playbooks, guardrails and grading |
| [Deployment](docs/book/12-deployment.md) · [Installing Kafka with Helm](docs/book/20-installation-guide.md) | Topologies, sizing, cloud overlays, chart-by-chart installs |
| [Troubleshooting Index](docs/book/appendix-b-troubleshooting.md) | Symptoms mapped to causes and chapters |
| [Version matrix](docs/book/appendix-d-versions.md) | Every pinned version (Kafka, Strimzi, Quarkus, Java, Go, charts), generated and checked in CI |
| [Local development](docs/local-development.md) | Makefile targets, image management, working behind a proxy |
| [Dashboards](dashboards/USING.md) | Which Grafana board answers which question |
| [Export formats](kates/docs/export-formats.md) | CSV, JUnit XML and latency heatmap layouts |

## Helm charts

Each chart carries its own version. A release publishes every chart to `oci://ghcr.io/bmscomp/charts`, at the versions in the [version matrix](docs/book/appendix-d-versions.md); the `monitoring` chart is published as `kates-monitoring`. `kates deploy` installs them as separate releases: a release build of the CLI pulls them at the versions of its tag, and one built from source, or given `--checkout <dir>` or `KATES_CHECKOUT`, installs a checkout's. The `kates-platform` umbrella combines `kafka-cluster` and `kates`, with Apicurio, MirrorMaker 2 and Kafka Connect as options, and expects the Strimzi operator to be installed first. Without access to GHCR, deploy pulls from a mirror (`KATES_CHART_REGISTRY`) or reads a bundle that `kates charts pull --to DIR` wrote, as [Deploying Without GHCR](docs/book/12-deployment.md#deploying-without-ghcr) describes.

When you install charts with Helm directly:

- Install `charts/strimzi-operator` first, as its own release. It owns the Strimzi CRDs through a pre-install/pre-upgrade hook Job that downloads the CRD bundle from the Strimzi GitHub release, so the cluster needs egress to GitHub, or `crdUpgrade.url` must point at a mirror.
- From a checkout, run `helm dependency build charts/<chart>` before rendering or installing a chart; a published chart carries its dependencies. `kafka-cluster` also pulls SeaweedFS from an HTTPS repository; after the first build, later builds fail unless you have run `helm repo add seaweedfs https://seaweedfs.github.io/seaweedfs/helm`.
- Use one install path per cluster. `kates deploy` names the Kafka release `krafter`, `make kafka` names it `kafka-cluster`, and both create the Kafka resource `krafter` in namespace `kafka`.

[Deploying the Strimzi Operator](docs/book/deploying-strimzi-operator.md) and [Installing Kafka with the kafka-cluster Helm Chart](docs/book/20-installation-guide.md) walk through both releases.

Every published chart is signed with cosign and carries a build provenance attestation. `kates deploy` runs both checks below on every chart it pulls, with cosign and no GitHub login. Check both before you install one yourself:

```bash
cosign verify ghcr.io/bmscomp/charts/kates:<version> \
  --certificate-identity-regexp '^https://github\.com/bmscomp/kates/\.github/workflows/publish-charts\.yml@refs/(heads/main|tags/v.+)$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

```bash
gh attestation verify oci://ghcr.io/bmscomp/charts/kates:<version> \
  --repo bmscomp/kates --signer-workflow bmscomp/kates/.github/workflows/publish-charts.yml
```

```bash
helm install kates oci://ghcr.io/bmscomp/charts/kates --version <version> --namespace kates --create-namespace
```

A published version is never pushed again, so its digest does not change.

## Building from source

You need Go and a JDK at the versions in the [version matrix](docs/book/appendix-d-versions.md), plus Docker for images and integration tests. Under the default `GOTOOLCHAIN=auto`, Go downloads the toolchain that `cli/go.mod` names, and `./mvnw` downloads Maven.

```bash
make cli-install               # Build the CLI with version info; installs to /usr/local/bin (CLI_INSTALL_DIR to change)
make test-unit                 # Java unit tests (no Docker) and Go tests
make test-java-it              # Java integration tests with Testcontainers (needs Docker)
make check                     # The fast CI checks: linters, formatting, version pins
make kates-image-native-local  # Native image kates:native-local, loaded into Kind for kates deploy (about 8 GB of Docker memory)
make kates-local               # JVM image kates:local (quicker to build), loaded into Kind and deployed in place of the native one
```

- `make` on its own lists every target.
- On Linux, `make cli-install` ends with a `codesign` error, a macOS-only step, after the binary is already installed. The binary works, but the target stops before checking whether another `kates` comes earlier on your `PATH`.
- On Apple Silicon, local `./mvnw` builds need Rosetta 2 (`softwareupdate --install-rosetta`): the gRPC code generator this Quarkus version uses ships only an x86-64 macOS binary. Docker builds are not affected.
- `(cd kates && ./mvnw quarkus:dev)` runs the backend with API-key authentication off, but it still needs a reachable PostgreSQL (`QUARKUS_DATASOURCE_JDBC_URL`) and Kafka (`KATES_KAFKA_BOOTSTRAP_SERVERS`).
- Both Dockerfiles take the repository root as build context, as in `docker build -f kates/Dockerfile.native -t kates:native-local .`.

## Contributing

Branch from `main` and write commit messages in the [Conventional Commits](CONTRIBUTING.md#commit-conventions) format, such as `fix(cli): …` or `docs(book): …`; pull requests are squash-merged. Run `make check` before you push. When a change alters what users see, update the matching chapter in `docs/book/`. CI also checks every relative link and anchor in this README. [CONTRIBUTING.md](CONTRIBUTING.md#pull-request-process) describes the review process, and the [Code of Conduct](CODE_OF_CONDUCT.md) covers participation.

## License

Apache License 2.0. See [LICENSE](LICENSE).
