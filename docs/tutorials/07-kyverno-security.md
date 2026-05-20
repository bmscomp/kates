# Tutorial 7: Kyverno & Security

This tutorial walks you through managing Kyverno admission policies and running security audits against your Kafka cluster using the Kates CLI.

## Prerequisites

Before starting this tutorial, ensure:

- The full stack is deployed (`make all` + `make kates`)
- The CLI is installed and configured (`make cli-install` + `kates ctx set local --url http://localhost:30083`)
- **Kyverno** is installed in the cluster (included automatically when deploying with `make all`)
- Kyverno policies are enabled in the Helm values (`kyvernoPolicy.enabled=true`)

Verify Kyverno is running:

```bash
kubectl get pods -n kyverno
```

You should see the Kyverno controller pods in `Running` state.

## Step 1: Check Policy Status

Start by reviewing the Kyverno policies deployed in your cluster:

```bash
kates kyverno status
```

Expected output:

```
  Kyverno Policy Status
  ─────────────────────────────
  Cluster-wide admission policies

  ┌────────────────────────────────────────┬─────────┬───────┬──────────┬────────┐
  │ Policy                                 │ Mode    │ Ready │ Validate │ Mutate │
  ├────────────────────────────────────────┼─────────┼───────┼──────────┼────────┤
  │ kates-pod-security-standards           │ Audit   │ ✓     │ 6        │ 5      │
  │ kates-workload-standards               │ Audit   │ ✓     │ 3        │ 0      │
  │ kates-image-verification               │ Audit   │ ✓     │ 1        │ 1      │
  │ kates-generate-network-policies        │ Audit   │ ✓     │ 0        │ 0      │
  └────────────────────────────────────────┴─────────┴───────┴──────────┴────────┘

  ✓ 4 policies active — 10 validate rules, 6 mutate rules

  ✓ No policy violations detected
```

The table shows each `ClusterPolicy` with its current mode (Audit or Enforce), readiness state, and the number of validation and mutation rules.

> **Note:** All policies default to **Audit** mode — violations are logged but pods are not blocked.

## Step 2: Review Violations

Check for any policy violations across all namespaces:

```bash
kates kyverno violations
```

If violations exist, you'll see them grouped by namespace:

```
  Kyverno Violations
  ────────────────────
  Policy audit report

  ▸ Namespace: litmus (2 violations)
  ┌────────────────────────┬──────────────────────────────┬─────────────────────────────────┐
  │ Pod                    │ Rule                         │ Message                         │
  ├────────────────────────┼──────────────────────────────┼─────────────────────────────────┤
  │ chaos-runner-abcd1234  │ validate-readonly-rootfs     │ readOnlyRootFilesystem must ... │
  │ chaos-runner-abcd1234  │ validate-drop-capabilities   │ containers must drop ALL cap... │
  └────────────────────────┴──────────────────────────────┴─────────────────────────────────┘

  ⚠ Total: 2 violations across 1 namespace(s)
```

To filter violations for a specific namespace:

```bash
kates kyverno violations --namespace kafka
```

If the namespace is fully compliant:

```
  ✓ No policy violations found — all pods are compliant!
```

## Step 3: Switch to Enforce Mode

Once you've reviewed violations and are confident your workloads comply, switch the pod security policy to **Enforce** mode. This will **block** non-compliant pods from deploying:

```bash
kates kyverno enforce kates-pod-security-standards
```

Expected output:

```
  ✓ Policy 'kates-pod-security-standards' switched to Enforce mode
  ⚠ Non-compliant pods will now be BLOCKED from deploying
```

Verify the mode change:

```bash
kates kyverno status
```

You should see `kates-pod-security-standards` now showing `Enforce` under the Mode column.

> **Warning:** Enforce mode means the Kyverno webhook will reject any pod that doesn't meet the Pod Security Standards. Make sure your workloads are compliant before enabling this in production.

## Step 4: Test Enforcement

Deploy a deliberately non-compliant pod to observe the rejection:

```bash
kubectl run insecure-test --image=busybox \
  --restart=Never \
  --overrides='{
    "spec": {
      "containers": [{
        "name": "insecure-test",
        "image": "busybox",
        "command": ["sleep", "3600"],
        "securityContext": {
          "runAsRoot": true,
          "privileged": true
        }
      }]
    }
  }' \
  -n kates
```

The pod should be **rejected** by Kyverno:

```
Error from server: admission webhook "validate.kyverno.svc-fail" denied the request:

resource Pod/kates/insecure-test was blocked due to the following policies:

kates-pod-security-standards:
  validate-non-root: 'validation error: Running as root is not allowed. The field
    spec.containers[*].securityContext.runAsNonRoot must be set to true. rule validate-non-root
    failed at path /spec/containers/0/securityContext/'
```

This confirms Enforce mode is working — Kyverno blocked the non-compliant pod at admission time.

## Step 5: Create a Policy Exception

In development environments, some workloads (like chaos testing tools) may need to run with elevated privileges. Instead of disabling the entire policy, create a **PolicyException** for a specific namespace:

```yaml
# dev-exception.yaml
apiVersion: kyverno.io/v2
kind: PolicyException
metadata:
  name: dev-pod-security-exception
  namespace: dev
spec:
  exceptions:
    - policyName: kates-pod-security-standards
      ruleNames:
        - validate-readonly-rootfs
        - validate-drop-capabilities
  match:
    any:
      - resources:
          namespaces: [dev]
          kinds: [Pod, Deployment, StatefulSet, ReplicaSet]
```

Apply the exception:

```bash
kubectl apply -f dev-exception.yaml
```

```
policyexception.kyverno.io/dev-pod-security-exception created
```

Pods in the `dev` namespace will now be exempted from the `validate-readonly-rootfs` and `validate-drop-capabilities` rules while all other validation rules remain enforced.

> **Tip:** Kates manages these exceptions via Helm values under `kyvernoPolicy.policyExceptions`. For production deployments, use the Helm-managed approach instead of applying YAML directly. See [Chapter 17: Security & Compliance](../book/17-security.md) for details.

## Step 6: Run a Security Audit

The Kates CLI includes a comprehensive security audit that scans your Kafka cluster's configuration and assigns an **A–F grade**:

```bash
kates security audit
```

Expected output:

```
  Security Audit
  ────────────────
  Grade: A  │  Kafka Cluster Posture Scan

  ▸ Authentication
  ┌───┬───────┬─────────────────────────────┬──────────┬──────────────────────────────┐
  │   │ CIS   │ Check                       │ Severity │ Detail                       │
  ├───┼───────┼─────────────────────────────┼──────────┼──────────────────────────────┤
  │ ✓ │ 4.1.1 │ SASL authentication enabled │ CRITICAL │ SCRAM-SHA-512 on port 9092   │
  │ ✓ │ 4.1.2 │ No anonymous listeners      │ HIGH     │ All listeners require auth   │
  └───┴───────┴─────────────────────────────┴──────────┴──────────────────────────────┘

  ▸ Authorization
  ┌───┬───────┬─────────────────────────────┬──────────┬──────────────────────────────┐
  │   │ CIS   │ Check                       │ Severity │ Detail                       │
  ├───┼───────┼─────────────────────────────┼──────────┼──────────────────────────────┤
  │ ✓ │ 4.2.1 │ ACL authorization active    │ HIGH     │ SimpleAclAuthorizer enabled  │
  │ ✓ │ 4.2.2 │ Minimal super-users         │ MEDIUM   │ 1 super-user (kates-backend) │
  └───┴───────┴─────────────────────────────┴──────────┴──────────────────────────────┘

  ▸ Transport Security
  ┌───┬───────┬─────────────────────────────┬──────────┬──────────────────────────────┐
  │   │ CIS   │ Check                       │ Severity │ Detail                       │
  ├───┼───────┼─────────────────────────────┼──────────┼──────────────────────────────┤
  │ ✓ │ 4.3.1 │ TLS enabled on port 9093    │ CRITICAL │ TLSv1.3 configured           │
  └───┴───────┴─────────────────────────────┴──────────┴──────────────────────────────┘

  Total Checks    18
  Passed          18
  Warnings        0
  Failures        0
  Grade           A
```

### Understanding the Grading Scale

| Grade | Meaning | Criteria |
|:-----:|---------|----------|
| **A** | Excellent | All critical and high-severity checks pass |
| **B** | Good | No critical failures; minor warnings allowed |
| **C** | Fair | Some high-severity issues need attention |
| **D** | Poor | Critical security gaps detected |
| **F** | Failing | Multiple critical vulnerabilities present |

The audit checks span nine categories: **Authentication**, **Authorization**, **Transport Security**, **Topic Health**, **Broker Configuration**, **Data Durability**, **Network & Threading**, **DoS Protection**, and **Resource Limits**.

> **Tip:** Export the audit report for compliance records: `kates security audit --export report.pdf`

## Step 7: TLS Inspection

Inspect the TLS configuration across your Kafka listeners:

```bash
kates security tls-inspect
```

Expected output:

```
  TLS Inspection
  ────────────────
  Certificate & Protocol Analysis

  ┌───┬──────────────────────────────┬────────────────────────────────────────────┐
  │   │ Check                        │ Detail                                     │
  ├───┼──────────────────────────────┼────────────────────────────────────────────┤
  │ ✓ │ TLS version                  │ TLSv1.3 (recommended)                      │
  │ ✓ │ Cipher suites                │ TLS_AES_256_GCM_SHA384, TLS_CHACHA20_P...  │
  │ ✓ │ Client authentication        │ Required (mTLS on port 9093)               │
  │ ✓ │ Certificate expiry           │ Cluster CA: 1640 days remaining            │
  │ ✓ │ Endpoint identification      │ HTTPS hostname verification enabled        │
  │ ✓ │ Weak protocols disabled      │ SSLv3, TLSv1.0, TLSv1.1 disabled          │
  └───┴──────────────────────────────┴────────────────────────────────────────────┘
```

This command verifies protocol versions, cipher suite strength, certificate expiry, and mutual TLS configuration — all without requiring direct access to the broker JVMs.

## Step 8: ACL Testing

Verify that a specific Kafka user has the expected (least-privilege) ACL permissions:

```bash
kates security auth-test --user kafka-ui
```

Expected output:

```
  ACL Auth Test
  ───────────────
  User: kafka-ui

  ┌───┬───────────────────────────────┬──────────────────────────────────────────┐
  │   │ Check                         │ Detail                                   │
  ├───┼───────────────────────────────┼──────────────────────────────────────────┤
  │ ✓ │ Read access on topics         │ Allowed on all topics (read-only)        │
  │ ✓ │ Write access restricted       │ Produce capped at 1MB/s                  │
  │ ✓ │ Group access                  │ Allowed on all consumer groups           │
  │ ✓ │ Cluster describe only         │ No alter/create permissions              │
  └───┴───────────────────────────────┴──────────────────────────────────────────┘

  ▸ ACL Rules for User:kafka-ui (4)
  ┌──────────┬──────┬──────────┬───────────┬────────────┐
  │ Resource │ Name │ Pattern  │ Operation │ Permission │
  ├──────────┼──────┼──────────┼───────────┼────────────┤
  │ Topic    │ *    │ LITERAL  │ Read      │ ALLOW      │
  │ Topic    │ *    │ LITERAL  │ Describe  │ ALLOW      │
  │ Group    │ *    │ LITERAL  │ Read      │ ALLOW      │
  │ Cluster  │ *    │ LITERAL  │ Describe  │ ALLOW      │
  └──────────┴──────┴──────────┴───────────┴────────────┘
```

Test other users to verify their access boundaries:

```bash
# Test the Kates backend (super-user)
kates security auth-test --user kates-backend

# Test the chaos testing user
kates security auth-test --user litmus-chaos
```

## Step 9: Revert to Audit Mode

When you're done testing enforcement, switch the policy back to **Audit** mode so violations are logged but no longer block deployments:

```bash
kates kyverno audit kates-pod-security-standards
```

Expected output:

```
  ✓ Policy 'kates-pod-security-standards' switched to Audit mode
  💡 Violations will be logged but NOT blocked
```

Confirm the change:

```bash
kates kyverno status
```

The policy should now show `Audit` under the Mode column.

## Step 10: Clean Up

Remove the test resources created during this tutorial:

```bash
# Remove the policy exception (if you created one)
kubectl delete policyexception dev-pod-security-exception -n dev --ignore-not-found

# The insecure-test pod was already rejected, so no cleanup needed
```

## Command Reference

| Command | Aliases | Description |
|---------|---------|-------------|
| `kates kyverno status` | `st`, `list` | List all ClusterPolicies with mode, readiness, and rule counts |
| `kates kyverno violations` | `viol`, `fails` | Show violations grouped by namespace, with `--namespace` filter |
| `kates kyverno enforce <policy>` | — | Switch a policy's `validationFailureAction` to `Enforce` |
| `kates kyverno audit <policy>` | — | Switch a policy's `validationFailureAction` to `Audit` |
| `kates security audit` | `scan` | Run a full security posture audit with A–F grading |
| `kates security tls-inspect` | `tls` | Inspect TLS configuration, protocols, and cipher suites |
| `kates security auth-test --user <name>` | `auth` | Probe ACL rules for a specific user |

## What's Next?

- [Chapter 17: Security & Compliance](../book/17-security.md) — full reference for authentication, authorization, certificates, and network policies
- [Tutorial 6: CI/CD Integration](06-cicd-integration.md) — automate security gates in your pipeline with `kates security gate --min-grade B`
- [Tutorial 3: Chaos Engineering](03-chaos-engineering.md) — test your cluster's resilience under failure conditions
