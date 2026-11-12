# kates-operator

Kates runs and compliance checks as Kubernetes resources. `KatesTest` is one run and `KafkaCompliance` a standing check. A team can commit `loadtest.yaml`, watch it with `kubectl get katestests`, and gate a pipeline on it.

The operator drives a Kates API through its REST API: one that is already running, or one it installs from a [`Kates`](#kates) resource. It needs Kates 1.38.0 or newer: older versions can't honour an idempotency key, so every `KatesTest` waits at `Synced=False`, reason `ApiTooOld`, and starts no run.

## Install

```bash
helm install kates-operator oci://ghcr.io/bmscomp/charts/kates-operator --version 0.1.1 \
  -n kates-operator --create-namespace
```

It needs Kubernetes 1.29 or newer and pulls `ghcr.io/bmscomp/kates-operator`, for amd64 and arm64. On a cluster that pulls from a mirror, set `global.imageRegistry` and `global.imagePullSecrets`. The image and the chart are signed with cosign:

```bash
cosign verify ghcr.io/bmscomp/kates-operator:0.1.1 \
  --certificate-identity-regexp 'https://github.com/bmscomp/kates/.github/workflows/publish-operator.yml@refs/tags/operator-v.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

To run your own build, from the repository root:

```bash
docker build -f operator/Dockerfile -t <registry>/kates-operator:dev .
docker push <registry>/kates-operator:dev
helm install kates-operator operator/chart -n kates-operator --create-namespace \
  --set image.repository=<registry>/kates-operator --set image.tag=dev
```

| Mode | Values | Rendered |
| --- | --- | --- |
| Its own namespace | (default) | A ClusterRole, a RoleBinding in the release namespace |
| Some namespaces | `watchNamespaces: [a, b]` | The same ClusterRole, a RoleBinding in each |
| Every namespace | `watchAnyNamespace: true` | The same ClusterRole, a ClusterRoleBinding |
| No cluster-scoped rights | `rbac.clusterRoles.create: false` | A Role in the release namespace, and nothing cluster-scoped |

For the last mode a cluster admin applies the CRDs and the aggregated roles once, and the namespace admin installs with `--skip-crds`:

```bash
helm pull oci://ghcr.io/bmscomp/charts/kates-operator --version 0.1.1 --untar
kubectl apply --server-side -f kates-operator/crds/
helm template kates-operator ./kates-operator -n team --show-only templates/aggregate-roles.yaml | kubectl apply -f -
helm install kates-operator ./kates-operator -n team --skip-crds \
  --set rbac.clusterRoles.create=false --set rbac.aggregateRoles.create=false
```

CRDs are applied by a pre-install and pre-upgrade hook that never downgrades: with several releases on a cluster, the newest wins. On a cluster that already has the CRDs, pass `--skip-crds`. A conflict on `customresourcedefinitions.apiextensions.k8s.io` during install means the flag was missed, and `--force-conflicts` is a downgrade, not the fix.

Don't let two releases watch the same namespace: each would reconcile the same resources.

## Connect a namespace

Each watched namespace holds a Secret named `kates-connection`, with the keys `url`, `apiKey` and optional `caBundle`:

```bash
kubectl create secret generic kates-connection -n payments \
  --from-literal=url=http://kates.kates.svc:8080 --from-literal=apiKey=<key>
```

The key needs `read` and `test:run`. Use a named key from the Kates keys file, so the audit log names the team. `connection.fallback: true` lends the Secret `connection.existingSecret` to every watched namespace that has no `kates-connection`. With `watchAnyNamespace`, that lends the key to everyone who can create a `KatesTest`.

To rotate a key, add the new hash to the Kates keys file, swap the Secret, then remove the old hash. The operator reads the key on every pass, so it needs no restart.

## KatesTest

```yaml
apiVersion: kates.io/v1alpha1
kind: KatesTest
metadata:
  name: nightly-load
spec:
  type: LOAD
  spec:
    numRecords: 100000
    acks: all
  sla:
    maxP99LatencyMs: 100
```

`spec.spec` takes the wire names of `POST /api/tests`, and the CRD refuses a mistyped key. `spec.sla` is graded by the Kates API, the same way the report and the JUnit export grade it. Once the run has started, the whole `spec` is immutable, because the resource is the evidence of what the run was judged against.

A CI gate is two commands, race-free on either outcome:

```bash
kubectl wait --for=jsonpath='{.status.finishedAt}' katestest/nightly-load --timeout=30m
kubectl wait --for=condition=Ready katestest/nightly-load --timeout=0
```

The first returns on every ending, and the second exits non-zero on a failed run. `kubectl describe` shows the failing key. The value-less `jsonpath` form needs kubectl 1.29 or newer.

Deleting a `KatesTest` whose run is live cancels the run first. The run's record stays in Kates. A deleted and re-created `KatesTest` is a new run.

### On a schedule

There is no schedule kind. A `CronJob` creates one `KatesTest` per firing, and waits for it so `Forbid` holds across firings:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: nightly-load
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: nightly-load
rules:
  - apiGroups: [kates.io]
    resources: [katestests]
    verbs: [create, get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: nightly-load
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: nightly-load
subjects:
  - kind: ServiceAccount
    name: nightly-load
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: nightly-load
spec:
  schedule: "0 2 * * *"
  timeZone: Etc/UTC
  concurrencyPolicy: Forbid
  startingDeadlineSeconds: 600
  jobTemplate:
    spec:
      backoffLimit: 0
      template:
        spec:
          serviceAccountName: nightly-load
          restartPolicy: Never
          containers:
            - name: run
              image: ghcr.io/bmscomp/kates-tester:1.38.0
              command: [/bin/sh, -ec]
              args:
                - |
                  run=$(kubectl create -o name -f - <<EOF
                  apiVersion: kates.io/v1alpha1
                  kind: KatesTest
                  metadata:
                    generateName: nightly-load-
                  spec:
                    type: LOAD
                    spec: {numRecords: 1000000}
                    sla: {maxP99LatencyMs: 100}
                    ttlSecondsAfterFinished: 86400
                  EOF
                  )
                  kubectl wait --for=jsonpath='{.status.finishedAt}' "$run" --timeout=30m
                  kubectl wait --for=condition=Ready "$run" --timeout=0
```

The Job fails when the run does, so the `CronJob`'s history shows the missed gates. The API's own `/api/schedules` is still there, but it needs an `admin` key and leaves no resource per firing.

`kates-tester` is unsigned. Where the Kates chart's `kyvernoPolicy.cosign` is on with Enforce, its pod is refused in that release's namespace: run the `CronJob` in another namespace and bind its ServiceAccount in the target, or use your own image with a shell and kubectl.

### GitOps

To rerun a `KatesTest`, rename it: the new name is a new run. With pruning on, the old resource is deleted, and its run is cancelled first if it is still live.

- A spec edit under the same name, after the run started, is refused at dry-run. Flux dry-runs every object first, so that one refusal fails the whole Kustomization. Keep `KatesTest`s in their own Kustomization or Application.
- Don't use Flux `spec.force: true`, or Argo CD `Replace=true,Force=true`. A refused edit then deletes and re-creates the resource, which starts a new run with no review.
- Never set `ttlSecondsAfterFinished` on a resource a GitOps tool holds: the tool re-creates it, and the run repeats every TTL. The TTL is for one-shot resources a pipeline creates under a unique name.

## KafkaCompliance

```yaml
apiVersion: kates.io/v1alpha1
kind: KafkaCompliance
metadata:
  name: krafter-rules
spec:
  interval: 1h
  reference:
    target: {namespace: kafka, name: krafter}
    brokers:
      count: {min: 3}
      zones: {min: 3}
```

`spec.reference` is a `KafkaReference` `.spec`, inline, and the API's strict reader checks it. The resource's name becomes the stored check's name, so it must be a DNS label. Intervals under a minute are raised to one minute, and intervals over a year lowered to one year.

A check also runs when the spec changes, and when the Strimzi `Kafka` it names has finished reconciling a change, if that namespace is in `kafka.namespaces`. Any other target is checked on its interval alone, and `KafkaWatched=False` says why. `spec.suspend: true` stops new checks.

## Kates

A `Kates` resource is one Kates backend. The operator turns it into a StatefulSet of one pod, a Service of the same name on ports 8080 and 9000, and a ServiceAccount, and removes them when the resource is deleted.

```yaml
apiVersion: kates.io/v1alpha1
kind: Kates
metadata:
  name: main
spec:
  version: 1.39.1-native
  database:
    secretRef:
      name: kates-db          # keys: url (jdbc:postgresql://...), username, password
  kafka:
    bootstrapServers: krafter-kafka-bootstrap.kafka.svc:9092
```

`kubectl get kates` shows the version every pod runs and whether the backend is Ready. A change to `spec.version` replaces the pod, and `Ready` is false until the new one answers.

The operator never takes over an object it did not create. If a ServiceAccount, Service or StatefulSet of the resource's name already exists, nothing is applied and `Ready` is false with reason `NameTaken`: rename one of them.

What it doesn't do yet:

- It runs one pod. More than one waits for Kates' own quorum.
- The pod's ServiceAccount is bound to no role, so the backend injects no fault and reads nothing from Kubernetes: `KATES_CHAOS_PROVIDER` defaults to `noop`. Load and integrity tests need neither.
- It doesn't create PostgreSQL. A changed Secret is not noticed: delete the pod to pick it up.
- Kafka over TLS with a truststore file isn't expressible yet. `spec.env` takes any other setting of the container, except the names `database`, `kafka` and `apiKeySecretRef` set.

## Operating it

| Annotation | Effect |
| --- | --- |
| `kates.io/pause-reconciliation: "true"` | No calls to the Kates API. A deleted resource is never paused |
| `kates.io/reconcile-at: <new value>` | Forces a pass, recorded in `status.lastHandledReconcileAt` |

Before `helm uninstall`, before dropping a namespace from `watchNamespaces`, and before deleting a namespace, delete its live `KatesTest`s. A finished `KatesTest` holds no finalizer and blocks nothing.

A live one left behind needs a hand. With `watchNamespaces` or the default mode, the RoleBinding that gives the operator its rights lives in the namespace, so `kubectl delete namespace` removes those rights along with everything else. The operator can then no longer clear its finalizer, and the namespace stays Terminating until you do:

```bash
kubectl patch katestest <name> -n <namespace> --type=merge -p '{"metadata":{"finalizers":null}}'
```

Kates ends the run at its own timeout. With `watchAnyNamespace` the ClusterRoleBinding survives: if the Secret goes first, the finalizer comes off with a `CancelSkipped` Event.

The NetworkPolicy allows egress on 8080, 443 and 6443. A Kates URL on any other port needs `networkPolicy.egressRules`, or the blocked call shows as `ApiUnavailable`.

Metrics are on `:8080/metrics` under `kates_operator_`, and `monitoring.enabled` renders a PodMonitor. `/readyz` fails on an unreachable API server or a stale served CRD, and it reports the Lease and any refused Kafka namespace in its body without failing on them.

One pod holds the Lease `kates-operator` in the release namespace. `replicas: 2` only shortens the gap after a crash, to between 5 and 15 seconds.
