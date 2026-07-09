# Kafka UI Helm Chart

A Helm chart for deploying [Kafbat Kafka UI](https://github.com/kafbat/kafka-ui) on Kubernetes with first-class support for Strimzi-managed Kafka clusters, Apicurio Schema Registry, and Kafka Connect.

> **Note** — This chart uses the actively maintained **Kafbat** fork (`ghcr.io/kafbat/kafka-ui`), which is the community continuation of the original Provectus Kafka UI. The old `provectuslabs/kafka-ui` image is deprecated and contains known security vulnerabilities.

---

## Table of Contents

- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Makefile Commands](#makefile-commands)
- [Image Configuration](#image-configuration)
- [Connecting to Kafka](#connecting-to-kafka)
- [Integrating Apicurio Schema Registry](#integrating-apicurio-schema-registry)
- [Integrating Kafka Connect](#integrating-kafka-connect)
- [Environment Profiles](#environment-profiles)
- [Accessing the UI](#accessing-the-ui)
- [Parameters Reference](#parameters-reference)
- [Uninstalling](#uninstalling)

---

## Architecture

Kafka UI serves as the central observability and management dashboard for the entire Kates streaming platform. Rather than requiring operators to interact with each component individually through CLI tools or raw API calls, Kafka UI provides a single web interface that aggregates information from three core backend services — the Kafka brokers, the Apicurio Schema Registry, and the Kafka Connect cluster — into one unified view.

### How the Components Fit Together

The Kates streaming platform is composed of several independent services, each with a clearly defined responsibility. Kafka UI sits at the top of this stack and talks to each service over its native protocol:

#### 🟢 Kafka Brokers

The Kafka brokers are the heart of the platform. They store and replicate all event streams across the three-node `krafter` cluster (brokers α, γ, and σ). Kafka UI connects to the brokers through the `krafter-kafka-bootstrap` Kubernetes Service, which load-balances TCP connections across all broker instances. This is a standard Strimzi bootstrap service — clients connect to it once, and Kafka's internal metadata protocol redirects them to individual broker addresses as needed.

Authentication between Kafka UI and the brokers uses **SCRAM-SHA-512**, a challenge-response SASL mechanism. Unlike PLAIN authentication, SCRAM never transmits the password in clear text — instead, the client proves it knows the password through a series of cryptographic exchanges. The protocol operates over port `9092` (SASL_PLAINTEXT) in development, or port `9093` (SASL_SSL) in production where TLS encryption wraps the entire connection.

The credentials that Kafka UI uses to authenticate are not manually created. Instead, the Strimzi Entity Operator watches a `KafkaUser` custom resource and automatically generates a Kubernetes Secret containing the SCRAM password. This Secret is mounted into the Kafka UI pod as an environment variable, completing the authentication chain with zero manual password management.

#### 🟠 Apicurio Schema Registry

Apicurio acts as the schema governance layer for the platform. When producers serialize messages using Avro, JSON Schema, or Protobuf, they register their schemas with Apicurio. Consumers then fetch those schemas at deserialization time, ensuring that both sides agree on the message structure. This prevents schema drift — a common source of production incidents in event-driven architectures.

Kafka UI connects to Apicurio through its **Confluent-compatible REST API** (`/apis/ccompat/v7`). This is the same API that the Confluent Schema Registry exposes, which means Kafka UI can talk to Apicurio without any special adaptation. Through this integration, the UI can:

- List all registered schemas and their versions
- Display the schema content (fields, types, nested structures)
- Show compatibility rules (BACKWARD, FORWARD, FULL, NONE)
- Decode serialized messages inline when browsing topic data

Apicurio itself stores its schema metadata in internal Kafka topics on the same broker cluster, which means it has no external database dependency — it is fully self-contained within the Kafka ecosystem.

#### 🔴 Kafka Connect

Kafka Connect provides the data integration layer. It runs worker processes that host connectors — plugins that move data between external systems and Kafka topics. The current Kates image ships with **Debezium 3.6.0 CDC connectors** (PostgreSQL, MySQL, MongoDB, SQL Server, Oracle, Db2), an **Aiven JDBC Sink connector**, and **Groovy scripting SMTs** for in-flight message transformation.

Kafka UI communicates with Connect through its **REST API on port 8083**. This is a standard interface defined by the Kafka Connect framework — every Connect cluster exposes it automatically. Through the UI, operators can:

- View all running connectors and their current state (RUNNING, PAUSED, FAILED)
- Inspect individual tasks and their error messages
- Create new connectors by submitting JSON configuration
- Pause, resume, and restart connectors and tasks
- Delete connectors

This replaces the common pattern of managing Connect via `curl` commands, giving operators a visual interface with immediate feedback.

#### 🟣 Strimzi Entity Operator

The Entity Operator is a component of the Strimzi Kafka Operator that manages two types of sub-resources: **topics** and **users**. For this chart, the relevant part is the User Operator. When the Helm chart creates a `KafkaUser` custom resource with `authentication.type: scram-sha-512`, the User Operator:

1. Detects the new `KafkaUser` resource through a Kubernetes watch
2. Registers the SCRAM credentials in the Kafka cluster's internal credential store
3. Creates a Kubernetes Secret (same name as the `KafkaUser`) containing the generated password
4. Continuously reconciles — if the Secret is deleted, it recreates it; if the `KafkaUser` is updated, it updates the credentials

The Kafka UI Deployment mounts this Secret as the `KAFKA_UI_PASSWORD` environment variable. The Spring Boot application inside the container reads this variable and uses it in the JAAS configuration for the Kafka client.

#### 🛡️ NetworkPolicy

The chart ships with a NetworkPolicy that controls all traffic flowing in and out of the Kafka UI pod. It follows a zero-trust model where all traffic is denied by default, and only specific flows are explicitly allowed:

| Direction | Allowed Traffic |
|-----------|----------------|
| **Ingress** | HTTP on port 8080 from the ingress controller namespace, NodePort access (when service type is NodePort), and Prometheus scraping from the monitoring namespace |
| **Egress** | DNS (port 53), Kafka brokers (ports 9092/9093), Schema Registry (port 8080, conditional), Kafka Connect (port 8083, conditional), and Kubernetes API (port 443 for Secret reads) |

When you enable Schema Registry or Kafka Connect in the values, the chart automatically adds the corresponding egress rules to the NetworkPolicy. You do not need to manage these rules manually.

### Integration Diagram

```mermaid
flowchart LR
    USER(["👤 User<br/>Browser"])

    USER -- "HTTP :8080<br/>NodePort :30081<br/>or Ingress" --> KAFKAUI

    subgraph KAFKA_NS["Kubernetes · kafka namespace"]
        direction TB

        KAFKAUI["🖥️ Kafka UI<br/>kafbat/kafka-ui:v1.5.0<br/>Web Dashboard"]

        KAFKAUI -- "SCRAM-SHA-512<br/>SASL_PLAINTEXT :9092<br/>SASL_SSL :9093" --> BROKERS
        KAFKAUI -- "HTTP :8080<br/>/apis/ccompat/v7<br/>Schema listing" --> APICURIO
        KAFKAUI -- "HTTP REST :8083<br/>Connector CRUD<br/>Task management" --> CONNECT

        BROKERS["🟢 Kafka Brokers<br/>α · γ · σ<br/>krafter-kafka-bootstrap<br/>Topics · Partitions · Consumer Groups"]

        APICURIO["🟠 Apicurio Registry<br/>Schema Registry<br/>Avro · JSON Schema · Protobuf<br/>Confluent-compatible API v7"]

        CONNECT["🔴 Kafka Connect<br/>REST API :8083<br/>Debezium 3.6.0 CDC<br/>PostgreSQL · MySQL · MongoDB<br/>Oracle · SQL Server · Db2<br/>JDBC Sink · Scripting SMT"]

        CONNECT -- "CDC events /<br/>Sink reads" --> BROKERS
        APICURIO -- "Schema storage<br/>in Kafka topics" --> BROKERS

        CONNECT --- DB[("🗄️ Databases<br/>PostgreSQL · MySQL<br/>Oracle · MongoDB")]

        SECRET[/"🔑 Secret: kafka-ui<br/>SCRAM-SHA-512 password<br/>Auto-generated by Strimzi"/]
        KAFKAUSER[/"📋 KafkaUser CR<br/>kafka-ui<br/>Read-only ACLs"/]
        EO["🟣 Entity Operator<br/>Strimzi<br/>Credential lifecycle"]
        NETPOL{{"🛡️ NetworkPolicy<br/>Ingress + Egress rules<br/>Zero-trust enforcement"}}

        EO -. "watches CR &<br/>creates Secret" .-> SECRET
        EO -. "manages" .-> KAFKAUSER
        SECRET -. "env mount<br/>KAFKA_UI_PASSWORD" .-> KAFKAUI
        NETPOL -. "enforces traffic<br/>policies on" .-> KAFKAUI
    end

    style KAFKAUI fill:#e3f2fd,stroke:#1565c0,stroke-width:2px,color:#0d47a1
    style BROKERS fill:#e8f5e9,stroke:#2e7d32,stroke-width:2px,color:#1b5e20
    style APICURIO fill:#fff3e0,stroke:#e65100,stroke-width:2px,color:#bf360c
    style CONNECT fill:#fce4ec,stroke:#c62828,stroke-width:2px,color:#b71c1c
    style EO fill:#f3e5f5,stroke:#6a1b9a,stroke-width:2px,color:#4a148c
    style SECRET fill:#eceff1,stroke:#546e7a,stroke-width:1px,stroke-dasharray: 5 5,color:#37474f
    style KAFKAUSER fill:#eceff1,stroke:#546e7a,stroke-width:1px,stroke-dasharray: 5 5,color:#37474f
    style USER fill:#fffde7,stroke:#f9a825,stroke-width:2px,color:#f57f17
    style DB fill:#efebe9,stroke:#4e342e,stroke-width:2px,color:#3e2723
    style NETPOL fill:#e8eaf6,stroke:#283593,stroke-width:1px,stroke-dasharray: 3 3,color:#1a237e
    style KAFKA_NS fill:#fafafa,stroke:#bdbdbd,stroke-width:1px
```

### Data Flow Summary

| # | Connection | Protocol | Port | Direction | Purpose |
|---|------------|----------|------|-----------|---------|
| 1 | User → Kafka UI | HTTP | 8080 / NodePort 30081 | Inbound | Web dashboard access from browser or Ingress controller |
| 2 | Kafka UI → Kafka Brokers | SASL_PLAINTEXT or SASL_SSL | 9092 / 9093 | Outbound | Topic browsing, partition inspection, consumer group monitoring, message sampling |
| 3 | Kafka UI → Apicurio Registry | HTTP GET | 8080 | Outbound | Schema listing, version history, compatibility checks, decoded message structure |
| 4 | Kafka UI → Kafka Connect | HTTP REST | 8083 | Outbound | Connector creation, configuration, status monitoring, task restart, pause/resume |
| 5 | Kafka Connect → Brokers | Kafka protocol | 9092 / 9093 | Internal | CDC event production and sink consumption |
| 6 | Kafka Connect → Databases | JDBC / CDC protocol | Various | Outbound | Source CDC capture and sink writes to external databases |
| 7 | Apicurio → Brokers | Kafka protocol | 9092 | Internal | Schema metadata stored in internal Kafka topics |
| 8 | Entity Operator → Secret | Kubernetes API | 443 | Internal | Auto-generates SCRAM-SHA-512 credentials from the KafkaUser CR |
| 9 | Secret → Kafka UI | Volume mount | — | Internal | Injects the broker password as the `KAFKA_UI_PASSWORD` environment variable |
| 10 | NetworkPolicy → Kafka UI | — | — | Enforcement | Restricts ingress to port 8080 and egress to brokers, registry, connect, DNS, and K8s API |

---

## Prerequisites

Before deploying, ensure the following components are available in your Kubernetes cluster:

| Component | Required | Version | Notes |
|-----------|----------|---------|-------|
| Kubernetes | Yes | ≥ 1.27 | Any conformant distribution (Kind, EKS, GKE, AKS, OpenShift) |
| Helm | Yes | ≥ 3.x | Used for chart installation and lifecycle management |
| Strimzi Kafka Operator | Yes | ≥ 0.40 | Must be installed cluster-wide; manages the Kafka cluster and KafkaUser CRDs |
| A running Kafka cluster | Yes | Managed by Strimzi | The chart connects to it via the bootstrap service |
| Apicurio Schema Registry | Optional | ≥ 3.x | Required only if you enable `schemaRegistry.enabled: true` |
| Kafka Connect (Strimzi) | Optional | ≥ 4.x | Required only if you enable `kafkaConnect.enabled: true` |

The chart expects a Strimzi `KafkaUser` with SCRAM-SHA-512 authentication. By default, the chart creates a `KafkaUser` named `kafka-ui` with read-only ACLs. If the `KafkaUser` is already managed elsewhere (e.g., by the Kafka cluster chart itself), set `kafkaUser.enabled: false` to avoid resource ownership conflicts.

---

## Quick Start

The fastest way to get Kafka UI running is to use one of the environment-specific overlays. These overlays are pre-configured with sensible defaults for each target environment.

```bash
# Install with default settings (connects to the "krafter" Kafka cluster)
helm install kafka-ui charts/kafka-ui --namespace kafka

# Install with the Kind (local development) profile
helm install kafka-ui charts/kafka-ui \
  -f charts/kafka-ui/values-kind.yaml \
  --namespace kafka

# Upgrade an existing release
helm upgrade kafka-ui charts/kafka-ui \
  -f charts/kafka-ui/values-kind.yaml \
  --namespace kafka
```

> **Tip** — If you prefer Makefile shortcuts over raw Helm commands, see the [Makefile Commands](#makefile-commands) section below.

---

## Makefile Commands

The project root `Makefile` provides shorthand targets for managing the Kafka UI chart without having to remember the full `helm` commands. These targets automatically detect and apply the correct environment overlay based on the `ENV` variable.

### Available Targets

| Command | What it does | Underlying Helm command |
|---------|-------------|------------------------|
| `make ui-deploy` | Install or upgrade Kafka UI. Creates the release if it does not exist, upgrades it otherwise. | `helm upgrade --install ... --wait` |
| `make ui-upgrade` | Upgrade an existing release while preserving previously supplied values. | `helm upgrade ... --reuse-values --wait` |
| `make ui-undeploy` | Remove the Kafka UI Helm release. The KafkaUser Secret is preserved. | `helm uninstall kafka-ui -n kafka` |
| `make ui-chart-lint` | Validate chart syntax and values before deploying. | `helm lint charts/kafka-ui` |
| `make ui-chart-template` | Render the manifests locally to stdout without applying them. | `helm template kafka-ui charts/kafka-ui ...` |
| `make ui` | Legacy target — applies raw manifests via `kubectl apply`. Prefer `ui-deploy`. | `kubectl apply -f config/kafka-ui/kafka-ui.yaml` |

### Environment Selection

All Helm-based targets respect the `ENV` variable, which defaults to `kind`. The Makefile looks for a matching `values-<ENV>.yaml` file in the chart directory and automatically applies it as an overlay on top of the base `values.yaml`:

```bash
# Deploy using the Kind profile (default)
make ui-deploy

# Deploy using the dev profile
make ui-deploy ENV=dev

# Deploy using the production profile
make ui-deploy ENV=prod
```

### How It Works

Every target follows a three-step pattern:

1. **Overlay detection** — The Makefile checks whether `charts/kafka-ui/values-<ENV>.yaml` exists. If it does, it is passed as a `-f` argument to Helm. If not, only the base `values.yaml` is used. This means you can safely run `make ui-deploy ENV=staging` on a cluster that does not have a `values-staging.yaml` — it will fall back to defaults.

2. **Helm execution** — The target runs `helm upgrade --install` (for `ui-deploy`) or `helm upgrade --reuse-values` (for `ui-upgrade`), targeting the `kafka` namespace with a 5-minute timeout and `--wait` to block until all pods are ready. The `--wait` flag ensures that the command only returns success after the Deployment has fully rolled out.

3. **Idempotent operation** — `helm upgrade --install` is safe to run repeatedly. If the release does not exist, Helm creates it. If it already exists, Helm computes the diff and applies only what changed. Running the same command twice with no value changes results in a no-op.

### Usage Examples

```bash
# Full lifecycle on a Kind cluster
make ui-deploy                   # Install with values-kind.yaml
make ui-upgrade                  # Roll out a config change
make ui-undeploy                 # Tear down

# Preview what would be deployed to production
make ui-chart-template ENV=prod

# Lint before pushing to CI
make ui-chart-lint

# Override the image tag at deploy time
make ui-deploy ENV=kind HELM_ARGS="--set image.tag=v1.4.2"
```

> **Note** — These Makefile targets are convenience wrappers. You can always use the raw `helm` commands directly if you need finer control (e.g., `--dry-run`, `--debug`, or custom `--set` overrides beyond what the targets expose).

---

## Image Configuration

The container image is fully customizable through the `image` section in `values.yaml`. By default, the chart pulls `ghcr.io/kafbat/kafka-ui` with the tag matching the chart's `appVersion` (currently `v1.5.0`).

```yaml
image:
  repository: ghcr.io/kafbat/kafka-ui    # any OCI-compliant registry
  tag: ""                                 # defaults to Chart.appVersion (v1.5.0)
  pullPolicy: IfNotPresent               # or Always for development
  digest: ""                             # set to "sha256:..." for immutable deployments

# For private registries that require authentication
imagePullSecrets:
  - name: my-registry-credentials
```

When `image.digest` is set, it takes precedence over `image.tag` and the resulting image reference becomes `repository@sha256:...`. This is recommended for production environments where you want to guarantee that the exact same binary is deployed regardless of tag mutability.

---

## Connecting to Kafka

The chart auto-computes the Kafka bootstrap servers from the `kafka.clusterName` and the release namespace. You can override this behavior with an explicit bootstrap address when the Kafka cluster lives in a different namespace or uses a non-standard service name.

### Auto-Discovery (Default)

In the simplest case, you only need to specify the Kafka cluster name. The chart constructs the full service FQDN automatically:

```yaml
kafka:
  clusterName: "krafter"          # name of the Strimzi Kafka CR
  # bootstrapServers is auto-computed as:
  #   krafter-kafka-bootstrap.<namespace>.svc.cluster.local:9092
```

The chart selects port `9092` (SASL_PLAINTEXT) by default, or port `9093` (SASL_SSL) when TLS is enabled. The `cluster.local` suffix is configurable via `kafka.clusterDomain` for clusters that use a custom DNS domain.

### Explicit Bootstrap

When the Kafka cluster lives in a different namespace, or you are connecting to an external Kafka service, provide the full bootstrap address directly:

```yaml
kafka:
  bootstrapServers: "my-kafka-bootstrap.production.svc:9093"
  tls:
    enabled: true
```

### Authentication

Kafka UI authenticates to the Kafka cluster using SCRAM-SHA-512. The chart creates a Strimzi `KafkaUser` resource, and the Strimzi Entity Operator generates a Kubernetes Secret containing the password. The Deployment mounts this Secret as the `KAFKA_UI_PASSWORD` environment variable.

The default configuration creates a read-only user with access to describe and read all topics, all consumer groups, and the cluster itself. This is intentional — Kafka UI is an observability tool, and write access should only be granted when explicitly needed.

```yaml
kafkaUser:
  enabled: true          # set to false if the KafkaUser is managed externally
  name: "kafka-ui"       # name of the KafkaUser CR and the generated Secret
  quotas:
    producerByteRate: 1048576       # 1 MB/s — limit accidental writes
    consumerByteRate: 52428800      # 50 MB/s — enough for message browsing
    requestPercentage: 10           # cap at 10% of broker request capacity
  acls:
    - resource:
        type: topic
        name: "*"
        patternType: literal
      operations: ["Describe", "Read"]
    - resource:
        type: group
        name: "*"
        patternType: literal
      operations: ["Describe", "Read"]
    - resource:
        type: cluster
      operations: ["Describe"]
```

> **Important** — If the `KafkaUser` is managed by another Helm chart (e.g., the Kafka cluster chart), set `kafkaUser.enabled: false` to avoid Helm resource ownership conflicts. The Secret must still exist with the same name.

---

## Integrating Apicurio Schema Registry

When enabled, the Kafka UI displays schema information (Avro, JSON Schema, Protobuf) for each topic directly in the browser. This gives operators immediate visibility into the structure of messages without needing to deserialize them manually or inspect the registry through its own API.

To enable, set `schemaRegistry.enabled: true` and provide the URL to Apicurio's Confluent-compatible API:

```yaml
schemaRegistry:
  enabled: true
  url: "http://apicurio-apicurio-registry.kafka.svc:8080/apis/ccompat/v7"
```

If `url` is left empty, the chart auto-computes it as:
```
http://apicurio-apicurio-registry.<namespace>.svc.cluster.local:8080/apis/ccompat/v7
```

For registries that require authentication (e.g., in multi-tenant environments):

```yaml
schemaRegistry:
  enabled: true
  url: "https://registry.example.com/apis/ccompat/v7"
  auth:
    enabled: true
    username: "admin"
    password: "secret"
```

> **Tip** — The `/apis/ccompat/v7` path is specific to Apicurio's Confluent-compatible mode. If you are using a native Confluent Schema Registry, omit this path suffix and point directly to the registry root.

---

## Integrating Kafka Connect

When enabled, the Kafka UI shows the running connectors, their status, tasks, and configuration. You can also create, update, pause, resume, and delete connectors directly from the web interface — replacing the need for manual `curl` calls to the Connect REST API.

```yaml
kafkaConnect:
  enabled: true
  name: "connect-cluster"     # display name shown in the UI sidebar
  url: "http://connect-cluster-connect-api.kafka.svc:8083"
```

If `url` is left empty, the chart auto-computes it as:
```
http://connect-cluster-connect-api.<namespace>.svc.cluster.local:8083
```

Once connected, the UI provides a dedicated **Kafka Connect** section where you can:

- Browse all connectors grouped by type (source vs. sink)
- View the configuration of each connector as JSON
- Monitor task-level status and error messages
- Restart failed tasks without recycling the entire connector
- Submit new connector configurations through a form or raw JSON editor

---

## Environment Profiles

The chart ships with four value overlays for different environments. Each overlay is designed to be applied on top of the base `values.yaml` using the `-f` flag (or the `ENV` variable with Make targets). Only the values that differ from the base are specified in each overlay — everything else inherits the defaults.

| Profile | File | Key Differences |
|---------|------|----------------|
| **Kind** | `values-kind.yaml` | NodePort on 30081, KafkaUser disabled (managed by krafter chart), Schema Registry + Connect enabled with explicit local URLs, startup probe, lower resources (100m/256Mi) |
| **Dev** | `values-dev.yaml` | Schema Registry + Connect enabled (auto-computed URLs), startup probe, lower resources (100m/256Mi) |
| **Prod** | `values-prod.yaml` | 2 replicas, TLS enabled, Ingress with nginx + TLS termination, LOGIN_FORM auth, Schema Registry + Connect enabled |
| **Generic** | `values-generic.yaml` | NetworkPolicy disabled — for clusters without a CNI that supports network policies (e.g., some managed Kubernetes services) |

### Example: Full Local Stack on Kind

```bash
# Using Makefile
make ui-deploy

# Using Helm directly
helm install kafka-ui charts/kafka-ui \
  -f charts/kafka-ui/values-kind.yaml \
  --namespace kafka
```

This deploys Kafka UI with:
- **NodePort on `30081`** for browser access without an ingress controller
- **Schema Registry** pointing to the local Apicurio instance
- **Kafka Connect** pointing to the local `connect-cluster`
- **Startup probe** with generous timings for slow-starting Kind environments
- **Reduced resources** (100m CPU / 256Mi memory) to fit alongside other services on a single-node cluster

### Example: Production Deployment

```bash
# Using Makefile
make ui-deploy ENV=prod

# Using Helm directly
helm install kafka-ui charts/kafka-ui \
  -f charts/kafka-ui/values-prod.yaml \
  --namespace kafka \
  --set ingress.hosts[0].host=kafka-ui.mycompany.com \
  --set ingress.tls[0].hosts[0]=kafka-ui.mycompany.com
```

---

## Accessing the UI

The access method depends on the `service.type` configured in your values overlay:

### NodePort (Kind and Development)

```bash
export NODE_IP=$(kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}')
echo "http://${NODE_IP}:30081"
```

### ClusterIP with Port-Forward

```bash
kubectl port-forward svc/kafka-ui 8080:8080 -n kafka
echo "http://localhost:8080"
```

### Ingress (Production)

When ingress is enabled with TLS, access the UI at the configured hostname:

```
https://kafka-ui.mycompany.com
```

---

## Parameters Reference

### Image

| Parameter | Default | Description |
|-----------|---------|-------------|
| `image.repository` | `ghcr.io/kafbat/kafka-ui` | Container image repository |
| `image.tag` | `""` (Chart.appVersion) | Image tag; leave empty to track the chart version |
| `image.pullPolicy` | `IfNotPresent` | Kubernetes image pull policy |
| `image.digest` | `""` | Image digest; overrides tag when set for immutable deployments |
| `imagePullSecrets` | `[]` | List of Kubernetes Secrets for private registry authentication |

### Kafka

| Parameter | Default | Description |
|-----------|---------|-------------|
| `kafka.clusterName` | `krafter` | Name of the Strimzi Kafka CR; used to compute the bootstrap service |
| `kafka.namespace` | `""` (release namespace) | Namespace where the Kafka cluster lives |
| `kafka.bootstrapServers` | `""` (auto-computed) | Explicit bootstrap servers; bypasses auto-computation when set |
| `kafka.clusterDomain` | `cluster.local` | Kubernetes cluster DNS domain |
| `kafka.tls.enabled` | `false` | Enable SASL_SSL on port 9093 instead of SASL_PLAINTEXT on 9092 |

### Schema Registry

| Parameter | Default | Description |
|-----------|---------|-------------|
| `schemaRegistry.enabled` | `false` | Enable Schema Registry integration in the UI |
| `schemaRegistry.url` | `""` (auto-computed) | Apicurio or Confluent Schema Registry URL |
| `schemaRegistry.auth.enabled` | `false` | Enable HTTP Basic authentication to the registry |
| `schemaRegistry.auth.username` | `""` | Registry auth username |
| `schemaRegistry.auth.password` | `""` | Registry auth password |

### Kafka Connect

| Parameter | Default | Description |
|-----------|---------|-------------|
| `kafkaConnect.enabled` | `false` | Enable Kafka Connect integration in the UI |
| `kafkaConnect.name` | `connect-cluster` | Display name for the Connect cluster in the UI sidebar |
| `kafkaConnect.url` | `""` (auto-computed) | Kafka Connect REST API URL |

### Service

| Parameter | Default | Description |
|-----------|---------|-------------|
| `service.type` | `ClusterIP` | Kubernetes Service type (`ClusterIP`, `NodePort`, `LoadBalancer`) |
| `service.port` | `8080` | Service port |
| `service.nodePort` | `""` | Fixed NodePort number (only when type is `NodePort`) |

### Resources

| Parameter | Default | Description |
|-----------|---------|-------------|
| `resources.requests.cpu` | `250m` | CPU request — guaranteed minimum allocation |
| `resources.requests.memory` | `512Mi` | Memory request — guaranteed minimum allocation |
| `resources.limits.cpu` | `1000m` | CPU limit — maximum burst capacity |
| `resources.limits.memory` | `2Gi` | Memory limit — OOMKilled if exceeded |

### Probes

| Parameter | Default | Description |
|-----------|---------|-------------|
| `livenessProbe.initialDelaySeconds` | `60` | Seconds to wait before first liveness check |
| `readinessProbe.initialDelaySeconds` | `30` | Seconds to wait before first readiness check |
| `startupProbe.enabled` | `false` | Enable startup probe for slow-starting environments |
| `startupProbe.failureThreshold` | `30` | Number of retries before the container is killed during startup |

### Security

| Parameter | Default | Description |
|-----------|---------|-------------|
| `podSecurityContext.runAsNonRoot` | `true` | Enforce non-root execution |
| `podSecurityContext.runAsUser` | `1001` | UID for the container process |
| `securityContext.allowPrivilegeEscalation` | `false` | Prevent privilege escalation |
| `networkPolicy.enabled` | `true` | Deploy a NetworkPolicy with zero-trust ingress/egress rules |

---

## Uninstalling

Remove the Kafka UI release using Helm or Make:

```bash
# Using Makefile
make ui-undeploy

# Using Helm directly
helm uninstall kafka-ui --namespace kafka
```

> **Note** — The `KafkaUser` resource has a `helm.sh/resource-policy: keep` annotation. This means the Strimzi-managed Secret will persist after uninstall, preventing accidental credential loss and avoiding disruption to other services that might reference the same user.
