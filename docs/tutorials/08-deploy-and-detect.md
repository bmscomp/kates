# Tutorial 8 — Deploy, Detect & Clean

**Level**: Beginner → Intermediate | **Duration**: 25 min

## What You'll Learn

- Analyze cluster compatibility before deployment with `kates detect`
- Deploy the full Kates stack interactively with `kates deploy -i`
- Monitor deployment progress through live progress boxes
- Read and understand the deployment summary dashboard
- Tear down the entire stack cleanly with `kates clean`

## Prerequisites

- A running Kubernetes cluster (Kind, minikube, or remote)
- `kubectl` configured and pointing to the cluster
- `helm` v3.x installed
- `kates` CLI installed (see [Installation](../../README.md#installation))

---

## Step 1: Analyze Your Cluster

Before deploying, run the cluster compatibility scanner to verify your cluster meets the requirements:

```bash
kates detect
```

The detect command performs a deep analysis of your cluster:

- **Node topology** — zones, CPU/memory per node, taints and labels
- **Storage classes** — provisioners, volume binding modes, reclaim policies
- **Network** — DNS resolution, cluster domain, CoreDNS configuration
- **Existing operators** — Strimzi, Cert-Manager, Prometheus, etc.
- **Security** — RBAC, Pod Security Standards, NetworkPolicies

Review the report. If any critical issues are flagged (🔴), resolve them before proceeding.

> **Tip**: Export the report for your team: `kates detect --export report.pdf`

---

## Step 2: Deploy the Stack

Launch the interactive deployment wizard:

```bash
kates deploy -i
```

### Topology Selection

You'll be prompted to choose a namespace topology:

- **Isolated Namespaces** — Each component gets its own namespace (`kafka`, `kates`, `monitoring`, `litmus`). Best for production-parity testing.
- **Single Namespace** — Everything in one namespace (`kates-stack`). Best for quick local development.

For this tutorial, choose **Isolated Namespaces**.

### Schema Registry

Choose whether to deploy a schema registry:

- **None** — Topics use raw bytes or JSON
- **Apicurio** — Open-source, lightweight, supports Avro/Protobuf/JSON Schema

For this tutorial, choose **None** (you can add it later).

### Component Selection

Select which components to deploy. The defaults are:

- ☸️ **Strimzi Operator** — Manages the Kafka cluster via CRDs *(required)*
- 🧪 **Litmus Chaos** — Chaos engineering toolkit
- 📊 **Monitoring** — Prometheus + Grafana + Jaeger
- 🔐 **Cert-Manager** — TLS certificate automation

Keep all defaults selected and press Enter.

### Namespace Configuration

If you chose Isolated mode, you can customize the namespace names. The defaults (`kafka`, `kates`, `monitoring`, `litmus`) work well for most setups.

---

## Step 3: Watch the Deployment

After confirming, the deployment proceeds in three parallel phases:

### Phase 1 — Operators & CRDs

Strimzi, Cert-Manager, and (optionally) Kyverno are deployed in parallel. The CLI waits for their CRDs to be registered before proceeding.

### Phase 2 — Core Infrastructure

Kafka and Monitoring are deployed in parallel. The Kafka deployment shows a **live progress box**:

```
╭─ Kafka Cluster  (12m00s timeout) ──────────────────────╮
│ Brokers       [▰▰▰▰▰▰▰▰▰▰▱▱▱▱▱]  2/3   ⏳ 1 pending │
│ Controllers   [▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰]  3/3   ✔ running   │
│ Timeout       [▰▰▱▱▱▱▱▱▱▱▱▱▱▱▱]  1:24 / 12:00       │
╰────────────────────────────────────────────────────────╯
```

This updates in real-time. Wait for all brokers and controllers to show ✔.

After Kafka is ready, KafkaUsers are provisioned with individual progress bars:

```
╭─ Kafka Users  (5m00s timeout) ─────────────────────────╮
│ kates-backend    [▰▰▰▰▰▰▰▰▰▰▰▰▰▰▰]  1/1  ✔ ready   │
│ kates-connect    [▱▱▱▱▱▱▱▱▱▱▱▱▱▱▱]  0/1  ⏳ provis.  │
│ Timeout          [▰▰▰▱▱▱▱▱▱▱▱▱▱▱▱]  0:48 / 5:00     │
╰────────────────────────────────────────────────────────╯
```

### Phase 3 — Applications

The Kates backend, Schema Registry (if selected), and Litmus Chaos are deployed sequentially.

---

## Step 4: Review the Summary

Once all components are deployed, the CLI displays a grouped summary dashboard:

```
 ⎈ Kates Deployment Summary   completed in 4m30s

  Group A — Operators & CRDs
  ──────────────────────────────────────────────────────
  ☸️  Strimzi Operator    strimzi-operator   ✔ Ready
  🔐 Cert-Manager        cert-manager       ✔ Ready

  Group B — Core Infrastructure
  ──────────────────────────────────────────────────────
  📨 Kafka (krafter)     kafka              ✔ Ready
  📊 Monitoring Stack    monitoring         ✔ Ready

  Group C — Applications
  ──────────────────────────────────────────────────────
  📦 Kates Backend       kates              ✔ Ready
  🧪 Litmus Chaos        litmus             ✔ Ready

  ✅ All components deployed successfully!
```

---

## Step 5: Verify Health

Confirm everything is running:

```bash
# System health check
kates health

# One-line status
kates status

# Check all pods
kubectl get pods -A
```

You should see all pods in `Running` state and `kates health` reporting a healthy system.

---

## Step 6: Run Your First Test

Now that the stack is deployed, run a quick load test:

```bash
kates test create --type LOAD --records 10000
```

Watch the test in real-time:

```bash
kates watch <test-id>
```

View the results:

```bash
kates report show <test-id>
```

---

## Step 7: Clean Up

When you're done, tear down the entire stack:

```bash
kates clean
```

The clean command proceeds through 6 phases:

1. **Port-forward cleanup** — Kills background port-forwards
2. **Strimzi CR removal** — Deletes Kafka CRs so the operator can process finalizers
3. **Finalizer stripping** — Removes stuck finalizers to prevent namespace locks
4. **Helm uninstall** — Uninstalls all releases in dependency order
5. **Namespace deletion** — Removes all managed namespaces
6. **CRD cleanup** — Deletes custom resource definitions

For CI/CD pipelines, use `--force` to skip the confirmation prompt:

```bash
kates clean --force
```

---

## What's Next?

- [Tutorial 1 — Getting Started](01-getting-started.md) — Run your first performance test
- [Tutorial 3 — Chaos Engineering](03-chaos-engineering.md) — Inject faults during tests
- [Tutorial 6 — CI/CD Integration](06-cicd-integration.md) — Add quality gates to your pipeline
