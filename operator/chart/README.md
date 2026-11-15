# kates-operator

Kates runs and compliance checks as Kubernetes resources. `KatesTest` is one run and `KafkaCompliance` a standing check. A team can commit `loadtest.yaml`, watch it with `kubectl get katestests`, and gate a pipeline on it.

The operator drives a Kates API through its REST API: one that is already running, or one it installs from a [`Kates`](#kates) resource. It needs Kates 1.38.0 or newer: older versions can't honour an idempotency key, so every `KatesTest` waits at `Synced=False`, reason `ApiTooOld`, and starts no run.

`KatesTest` and `KafkaCompliance` are deprecated in 0.2.0 and go in 0.3.0. The API server prints a warning on every request for either kind, and the operator logs one at start while any of their resources is left. Move each to a `CronJob` that runs the `kates` CLI, as [Replacing the run kinds](#replacing-the-run-kinds) shows. Both still work in 0.2.0. The `Kates` kind stays.

## Install

```bash
helm install kates-operator oci://ghcr.io/bmscomp/charts/kates-operator --version 0.2.0 \
  -n kates-operator --create-namespace
```

It needs Kubernetes 1.29 or newer and pulls `ghcr.io/bmscomp/kates-operator`, for amd64 and arm64. On a cluster that pulls from a mirror, set `global.imageRegistry` and `global.imagePullSecrets`. The image and the chart are signed with cosign:

```bash
cosign verify ghcr.io/bmscomp/kates-operator:0.2.0 \
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
helm pull oci://ghcr.io/bmscomp/charts/kates-operator --version 0.2.0 --untar
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

There is no schedule kind. Run `kates test apply` from a `CronJob`, as [Replacing the run kinds](#replacing-the-run-kinds) shows.

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

In a namespace named in `rbac.backendNamespaces`, its pod gets the rights a Helm-installed Kates gets in the Kates chart's cluster scope, for faults, topology and compliance reads. The list is empty by default. Those rights reach every namespace, and any pod in the namespace can run as a `Kates` pod's ServiceAccount, so name only namespaces where you would trust everyone who can run a pod with the role itself. The chart ships the rights as the ClusterRole `kates-backend-<release namespace>`, and the operator binds it to each such pod's ServiceAccount through a ClusterRoleBinding named `kates-backend.<namespace>.<name>`. The operator holds `bind` on that one role, never the rights themselves. A namespace cannot own a cluster-scoped binding, so a finalizer removes it when the `Kates` is deleted. `rbac.backendDirectChaos` adds the writes of the direct Kubernetes chaos provider, as the Kates chart's `rbac.directChaos` does. The backend's default provider is `litmus-crd`; set `KATES_CHAOS_PROVIDER` in `spec.env` to use another.

In any other namespace the pod gets no role, no finalizer is added, and `KATES_CHAOS_PROVIDER` defaults to `noop`, as in the Kates chart's namespace scope. Load and integrity tests need no role. A namespace taken off the list loses its bindings when the operator restarts, as it does on the `helm upgrade` that changes the list, whether or not the namespace is still watched. With `rbac.clusterRoles.create=false` nothing cluster-scoped is created and no namespace gets the role.

`kubectl get kates` shows the version every pod runs, whether the backend is Ready, its mode and whether it is drained. A change to `spec.version` replaces the pod, and `Ready` is false until the new one answers. The operator deletes the old pod itself (the StatefulSet's `updateStrategy` is `OnDelete`), so it can wait for a drain first.

### Upgrade without losing a run

A run or a fault that is live when its pod goes is lost. To upgrade without that, set `mode: Drain` with the new version, or before it:

```bash
kubectl patch kates main --type merge -p '{"spec":{"mode":"Drain","version":"1.41.0-native"}}'
kubectl wait --for=jsonpath='{.status.version}'=1.41.0-native kates/main --timeout=30m
kubectl patch kates main --type merge -p '{"spec":{"mode":"Active"}}'
```

Under `Drain` the operator sets the backend's `PUT /api/drain`: new runs, faults and resilience tests are answered 503, schedules stop firing, and live work goes on to its end. The `Drained` condition reports what is still live, and turns true once nothing is. Only then is a pod on an old version replaced, and the new pod is drained again as soon as it answers. The operator never sets the mode back to `Active`: you do, and the condition goes once the drain is lifted.

To call the API the operator reads the key `apiKeySecretRef` names, and its chart lets it read only a Secret named `kates-api-key`, as the Kates chart names it. A key in any other Secret leaves `Drained` false with reason `KeyUnreadable`, and the pod is not replaced while the mode is `Drain`. The key needs the `admin` scope. A backend without the drain switch, Kates 1.40.0 or older, gives reason `DrainUnsupported`: upgrade it under `Active`, which replaces the pod at once.

The operator never takes over an object it did not create. If a ServiceAccount, Service or StatefulSet of the resource's name already exists, nothing is applied and `Ready` is false with reason `NameTaken`: rename one of them.

### Three or five members

```yaml
spec:
  replicas: 3          # 1 (default), 3 or 5; fixed once the resource exists
  quorum:
    storage: 1Gi       # each member's volume, for its Raft term and vote
    # storageClassName: fast
```

With `replicas: 3` the members elect a leader among themselves, and keep one with a member gone; `5` keeps one with two gone. Only the leader takes writes: the others answer them 503 with `Retry-After` and serve reads. The `Kates` Service sends traffic to the leader alone, through the label `kates.io/quorum-role=leader`, which the operator moves to a new leader within about five seconds. `kubectl get kates` shows the leader. When the leader dies, a new one is elected within seconds, and before it takes a write it fails the runs the dead leader had live and removes the faults it had left.

The members talk over mutual TLS on port 9300, each reached through a Service of its own name, `<resource>-0` and so on. A Service's address stays the same when its pod restarts, so a member's peers reach it again within seconds instead of trying the old pod's address. The operator makes a CA for the quorum and a certificate for each member, valid a year, and renews each at two-thirds of its life by replacing that member's pod. They are kept in two Secrets, named `kates-quorum-ca` (never mounted) and `kates-quorum-tls`: the chart lets the operator read and change only Secrets of those names, so a namespace holds one `Kates` with a quorum, and a second gets `Ready=False`, reason `QuorumSecretTaken`.

A version change replaces one member at a time, followers first and the leader last. The next is replaced only once every member is ready, knows the leader and holds an entry of its log: a member with an empty log is no voter, so replacing another before then could leave the quorum without a majority. Under `mode: Drain` the leader is replaced once its drain ends. A `PodDisruptionBudget` lets a node drain take one member at a time. Deleting the resource deletes the members' volumes.

The members prefer one node each, so a lost node takes one member, not the majority. This places new volumes only: a member whose node-local volume already shares a node with another member stays there until its volume is replaced. A member whose volume can only be on a node that is gone, as with `local-path` and other node-local storage, waits `Pending`. Two minutes later the operator deletes its claim and its pod, and the `StatefulSet` makes both again on another node. It does this one member at a time, and only while the others follow a leader and have caught up, because the new member starts with an empty log and does not vote until it catches up. Until then `Ready` is `False`, reason `MemberLost`. The same happens when a node holding a member is drained and stays unschedulable for two minutes. The member loses nothing it needs: the quorum replicates only who leads, and everything else is in PostgreSQL.

With the majority lost (two of three members, or three of five), nothing is replaced, because there is no leader. The members' disks then hold nothing but terms and votes, so you can start the quorum over. Delete every member's claim, then their pods:

```bash
kubectl -n <namespace> delete pvc -l app.kubernetes.io/instance=<resource> --wait=false
```

```bash
kubectl -n <namespace> delete pod -l app.kubernetes.io/instance=<resource>
```

The new quorum counts terms from the start again, so a fault the old one left is removed when its deadline passes, not straight away.

What it doesn't do yet:

- It doesn't scale a quorum: `replicas` is set when the resource is made.
- It doesn't recover a quorum that lost its majority (see above).
- No PostgreSQL Secret for the `INTEGRATION_CDC` test, and no `rbac.extraRules`: the Kates chart's `rbac.cdcSecretNamespaces` and `rbac.extraRules` have no counterpart yet.
- It doesn't create PostgreSQL. A changed Secret is not noticed: delete the pod to pick it up.
- Kafka over TLS with a truststore file isn't expressible yet. `spec.env` takes any other setting of the container, except the names `database`, `kafka` and `apiKeySecretRef` set.

## Replacing the run kinds

A `CronJob` runs the `kates` CLI against the API: `kates test apply --wait` for a `KatesTest`, `kates compliance check` for a `KafkaCompliance`. The Job fails when the run misses a gate or the check fails, so the `CronJob`'s history shows each miss. The scenario file takes the keys of `kates test apply`, not the wire names of `spec.spec`: see `kates test apply --help`.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nightly-load
data:
  load.yaml: |
    name: nightly-load
    type: LOAD
    spec:
      records: 1000000
    validate:
      maxP99LatencyMs: 100
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
          restartPolicy: Never
          automountServiceAccountToken: false
          containers:
            - name: run
              image: ghcr.io/bmscomp/kates-tester:1.40.0
              env:
                - name: KATES_VERSION
                  value: "1.40.0"
                - name: KATES_URL
                  valueFrom: {secretKeyRef: {name: kates-connection, key: url}}
                - name: KATES_API_KEY
                  valueFrom: {secretKeyRef: {name: kates-connection, key: apiKey}}
              command: [/bin/sh, -ec]
              args:
                - |
                  cd /tmp
                  file=kates-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/').tar.gz
                  release=https://github.com/bmscomp/kates/releases/download/v$KATES_VERSION
                  curl -fsSL -O "$release/$file" -O "$release/checksums.txt"
                  grep " $file\$" checksums.txt | sha256sum -c -
                  tar xzf "$file" kates
                  ./kates test apply -f /scenario/load.yaml --wait --plain
              volumeMounts:
                - {name: scenario, mountPath: /scenario}
          volumes:
            - name: scenario
              configMap: {name: nightly-load}
```

For a `KafkaCompliance`, put its `spec.reference` in the ConfigMap as a `KafkaReference` file, and run `./kates compliance check -f /scenario/reference.yaml` instead. The check runs on the schedule only, not when the Strimzi `Kafka` changes. A test needs a key with `read` and `test:run`, a check `read` alone, so the `kates-connection` key the operator uses serves both.

The pod downloads the CLI from GitHub and checks it against the release's `checksums.txt`. Where pods have no route to GitHub, serve the tarball and `checksums.txt` from a mirror and point `release` at it. `kates-tester` is unsigned. Where the Kates chart's `kyvernoPolicy.cosign` is on with Enforce, its pod is refused in that release's namespace: run the `CronJob` in another namespace, or use your own signed image with the CLI in it.

Once a namespace has its `CronJob`s, delete its `KatesTest`s and `KafkaCompliance`s. Deleting a live `KatesTest` cancels its run, so wait for it to finish first.

## Operating it

| Annotation | Effect |
| --- | --- |
| `kates.io/pause-reconciliation: "true"` | No calls to the Kates API. A deleted resource is never paused |
| `kates.io/reconcile-at: <new value>` | Forces a pass, recorded in `status.lastHandledReconcileAt` |

Before `helm uninstall`, before dropping a namespace from `watchNamespaces`, and before deleting a namespace, delete its live `KatesTest`s and its `Kates` resources. A finished `KatesTest` holds no finalizer and blocks nothing.

A live one left behind needs a hand. With `watchNamespaces` or the default mode, the RoleBinding that gives the operator its rights lives in the namespace, so `kubectl delete namespace` removes those rights along with everything else. The operator can then no longer clear its finalizer, and the namespace stays Terminating until you do:

```bash
kubectl patch katestest <name> -n <namespace> --type=merge -p '{"metadata":{"finalizers":null}}'
```

Kates ends the run at its own timeout. With `watchAnyNamespace` the ClusterRoleBinding survives: if the Secret goes first, the finalizer comes off with a `CancelSkipped` Event.

A `Kates` left behind is cleared the same way, and its ClusterRoleBinding then stays: delete it too, or a ServiceAccount later created under that name in that namespace inherits its rights.

```bash
kubectl patch kates <name> -n <namespace> --type=merge -p '{"metadata":{"finalizers":null}}'
kubectl delete clusterrolebinding kates-backend.<namespace>.<name>
```

The NetworkPolicy allows egress on 8080, 443 and 6443. A Kates URL on any other port needs `networkPolicy.egressRules`, or the blocked call shows as `ApiUnavailable`.

Metrics are on `:8080/metrics` under `kates_operator_`, and `monitoring.enabled` renders a PodMonitor. `/readyz` fails on an unreachable API server or a stale served CRD, and it reports the Lease and any refused Kafka namespace in its body without failing on them.

One pod holds the Lease `kates-operator` in the release namespace. `replicas: 2` only shortens the gap after a crash, to between 5 and 15 seconds.
