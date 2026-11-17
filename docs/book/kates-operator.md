# Running Kates Under the Kates Operator

A standalone install runs the [Kates API](appendix-a-glossary.md#gl-kates-api) as one pod. If the node under that pod dies, every run and fault it had live is lost, and nothing answers until Kubernetes schedules the pod again. An upgrade has the same effect: the rollout replaces the pod whether or not a run is halfway through. For a lab that is fine. For a cluster where a nightly suite or a Game Day depends on Kates, it is a single point of failure.

The [Kates operator](appendix-a-glossary.md#gl-kates-operator) removes it. You describe the Kates API as a `Kates` resource, and the operator runs it as one member or as a quorum of three or five. The quorum keeps a leader when members are lost, an upgrade waits for live runs to finish, and a member whose node is gone is rebuilt on another node. You keep the same CLI, the same Service and the same API key.

This chapter shows how to choose between the two modes, deploy under the operator, read what it reports and get through a node loss or an upgrade. It also shows what stays exactly as it was when you keep the standalone mode.

- Choose between the standalone and operator modes
- Deploy Kates under the operator with `kates deploy`
- Read the `Kates` resource's conditions and the quorum's leader
- Upgrade without losing a live run, and recover from a lost node or majority

## Two Modes, One API

`kates deploy` installs the Kates API in one of two modes, chosen with `--kates-mode` or in the wizard right after the components. The table compares what each one runs. The last two rows are why you would pick one over the other:

| | `standalone` (default) | `operator` |
|---|---|---|
| What runs the Kates API | The `kates` chart: a Deployment of one pod | The Kates operator, from a `Kates` resource: a StatefulSet |
| PostgreSQL | The chart's own | The `kates-db` release, or your own with `--kates-db-secret` |
| Members | One | One, or three or five: a quorum whose leader takes the writes |
| A lost node | Live runs are lost until the pod is rescheduled | A follower takes over as leader within seconds |
| Upgrades | A rollout that replaces the pod at once | Followers first, the leader last, and under `Drain` only once live runs end |
| Needs cluster admin | No (`--adminless` works) | Yes: the operator brings a CRD and a ClusterRole |

Everything you use the Kates API through is the same in both modes. The Service is `kates` in the Kates namespace, on ports 8080 and 9000, and the API key is in the Secret `kates-api-key`. So `kates ports`, `kates health`, `kates test apply`, the dashboards, the MCP server and every tutorial work unchanged in either mode.

## Standalone Stays the Default

The operator mode is an addition. Nothing in it changes a standalone install, and a standalone install is what you get unless you ask for the operator. The table lists what that means in practice:

| Question | Answer |
|---|---|
| What does `kates deploy` install with no new flag? | The standalone `kates` chart |
| Does a running standalone install change on upgrade? | No: the Kates API runs its quorum only when the operator configures peers |
| Do older scenario files, scripts and CI jobs still work? | Yes: the REST, gRPC and CLI interfaces are the same in both modes |
| Does `--adminless` still work? | Yes, with the standalone mode; the operator mode refuses it |
| Is anything new in the Kates API itself? | Two additions: a drain switch (`PUT /api/drain`) and a `quorum` block in `/api/health` |

The quorum's code ships in every Kates API image, but it stays off unless the setting that lists the members is present. Only the operator sets it, so a standalone pod's `/api/health` shows `"quorum":{"enabled":false}` and serves every request itself, as one standalone pod. The drain switch is new and needs the `admin` scope. Nothing calls it unless you or the operator do, so an install that never drains sees no difference.

`kates deploy` also guards the boundary between the modes. It refuses to install one mode over the other, because both would claim the Service `kates`. The flags that only make sense under the operator (`--kates-replicas` above 1 and `--kates-db-secret`) are refused with the standalone mode, before anything reaches the cluster.

::: {.callout-important title="The one change for earlier operator users"}
The operator runs the `Kates` kind only. If you ran an early operator with `KatesTest` or `KafkaCompliance` resources, move them to a `CronJob` that runs the CLI before you upgrade the operator, as [Upgrading to 0.3.0](../../operator/chart/README.md#upgrading-to-030) shows. A standalone install never had those kinds and needs nothing.
:::

The usual upgrade notes still apply to a standalone install, whatever the operator does. The [Upgrade Playbook](18-upgrade-playbook.md) lists them per component, such as the bundled PostgreSQL's change of image.

## Deploying Under the Operator

One flag switches the mode, and a second asks for a quorum. With three members the quorum keeps a leader when one is lost, and with five when two are:

```bash
kates deploy --kates-mode operator --kates-replicas 3
```

Deploy then installs three things in place of the `kates` chart, in this order:

1. The Kates operator, as the [Helm release](appendix-a-glossary.md#gl-helm-release) `kates-operator` in the namespace `kates-operator` (`--operator-ns` changes it). It watches the Kates namespace and may bind the Kates API's cluster role there, for faults, topology and compliance reads.
2. A PostgreSQL for Kates, the release `kates-db` of the repository's `cdc-postgresql` chart, open to the Kates pods alone. With `--kates-db-secret <name>`, deploy skips it and uses an existing Secret with the keys `url` (`jdbc:postgresql://…`), `username` and `password`.
3. The `Kates` resource `kates`, with its API key, its Kafka connection to [`krafter`](appendix-a-glossary.md#gl-krafter), the Kafka cluster under test, and the image version this CLI pins. Deploy then waits up to 10 minutes for the resource to report `Ready`.

Deploy stops at once rather than waiting out the 10 minutes when the operator reports a member that can never join, as [Reading the Kates Resource](#reading-the-kates-resource) explains. The number of members is fixed once the resource exists. To change it, remove Kates and deploy again.

The wizard (`kates deploy -i`) asks the same questions after the components: standalone or operator, then the number of members, the operator's namespace and an existing database.

## Reading the Kates Resource

The operator reports everything on the `Kates` resource, so `kubectl get` is the first place to look. On [`panda`](appendix-a-glossary.md#gl-panda), the book's three-node Kind cluster, after `kates deploy --kates-mode operator --kates-replicas 3`:

```bash
kubectl get kates -n kates
```

Output:

```text
NAME    VERSION         REPLICAS   LEADER    READY   MODE     DRAINED   AGE
kates   1.41.2-native   3          kates-1   True    Active             7m11s
```

Read it left to right:

1. `VERSION` is the version every member runs. During an upgrade it shows the old version until the last member is replaced.
2. `LEADER` is the member that takes the writes, here `kates-1`.
3. `READY` is the operator's verdict: every member is up and, with a quorum, a leader is elected.
4. `MODE` is `Active` or `Drain`, and `DRAINED` fills in only under `Drain`.

When `READY` is `False`, the condition's reason says why. These are the reasons you can meet, with what each one asks of you:

| Condition and reason | What it means | What to do |
|---|---|---|
| `Ready=False`, `NoLeader` | The quorum is still electing | Wait: an election takes seconds |
| `Ready=False`, `NoQuorum` | A member runs an image older than the quorum, so it can never lead or catch up | Set `spec.version` to an image with the quorum |
| `Ready=False`, `MemberLost` | A member's node is gone and the operator is rebuilding it elsewhere | Wait for it to catch up |
| `Ready=False`, `NameTaken` | A Service, ServiceAccount or StatefulSet of that name exists and the operator did not create it | Rename one of them |
| `Ready=False`, `QuorumSecretTaken` | Another `Kates` in the namespace already runs a quorum | Use one quorum per namespace |
| `Drained=False`, `KeyUnreadable` | The operator cannot read the API key from `kates-api-key` | Put the key in that Secret |
| `Drained=False`, `DrainUnsupported` | The Kates API is older than the drain switch | Upgrade it under `Active` |

To print every condition with its reason and message:

```bash
kubectl get kates kates -n kates -o jsonpath=\
'{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
```

## The Quorum

The members of a quorum elect a leader among themselves with Raft, the consensus algorithm KRaft also uses [@ongaro2014search]. They keep only who leads in their quorum. Runs, results and schedules stay in PostgreSQL, so a member loses nothing it needs when its volume goes. Only the leader takes writes: a follower answers a write with `503` and a `Retry-After` header, and serves reads.

You never pick a member yourself. The Service `kates` sends traffic to the leader alone, through the pod label `kates.io/quorum-role=leader`, which the operator moves to a new leader within about five seconds. The label shows which pod leads:

```bash
kubectl get pods -n kates -L kates.io/quorum-role
```

Output:

```text
NAME         READY   STATUS    RESTARTS   AGE     QUORUM-ROLE
kates-0      1/1     Running   0          7m10s
kates-1      1/1     Running   0          7m10s   leader
kates-2      1/1     Running   0          7m10s
kates-db-0   1/1     Running   0          9m26s
```

Each member also answers its own health check, whose `quorum` block names the leader and the Raft term. It is the same `/api/health` the CLI reads, so you can see it through the API server's proxy without a port-forward:

```bash
kubectl get --raw \
  /api/v1/namespaces/kates/services/http:kates:8080/proxy/api/health | jq .quorum
```

Output:

```text
{
  "enabled": true,
  "state": "RUNNING",
  "memberId": "kates-1",
  "leader": true,
  "leaderId": "kates-1",
  "term": 1,
  "appliedIndex": 0
}
```

When the leader dies, the others elect a new one within seconds. Before the new leader takes a write, it fails the runs the dead leader had live and removes the faults it left behind, so no fault outlives the member that injected it. A run that was live on the dead leader is reported failed, not lost: run it again.

The members reach each other over mutual TLS on port 9300, each through a Service of its own name (`kates-0`, `kates-1` and so on). The operator makes their CA and certificates and renews each certificate at two-thirds of its year-long life. A `PodDisruptionBudget` lets a node drain take one member at a time.

::: {.callout-tip title="Try it: does a run survive the leader?"}
Start a long run, delete the leader's pod, and watch `LEADER` change: `kubectl delete pod -n kates kates-1`, then `kubectl get kates -n kates -w`. The run the old leader had live ends as failed; a new run started after the election succeeds through the same `kates` Service.
:::

## Upgrading Without Losing a Run

An upgrade replaces one member at a time, followers first and the leader last. The operator replaces the next member only once every member is ready, knows the leader and holds an entry of its log, so a roll never leaves the quorum without a majority. A plain version change does that under `Active`, but runs live on the leader when its turn comes are lost with it.

To lose none, set `mode: Drain` with the new version. The three commands below drain, wait for the new version and lift the drain:

```bash
kubectl patch kates kates -n kates --type merge \
  -p '{"spec":{"mode":"Drain","version":"<new-version>-native"}}'
kubectl wait --for=jsonpath='{.status.version}'=<new-version>-native \
  kates/kates -n kates --timeout=30m
kubectl patch kates kates -n kates --type merge -p '{"spec":{"mode":"Active"}}'
```

Under `Drain`, the operator calls the drain switch on every member. New runs, faults and resilience runs are answered `503`, schedules stop firing, and live work goes on to its end. The `Drained` condition lists what is still live and turns `True` once nothing is. Only then is a member on the old version replaced, and the new member is drained as soon as it answers.

The operator never sets the mode back to `Active`. You do, with the last command, and new runs are taken again from then on. Running `kates deploy --kates-mode operator` from a newer CLI also upgrades: it applies the version that CLI pins and leaves `mode` as you set it.

## When a Node Goes

The members prefer one node each, so a lost node takes one member, not the majority. On storage tied to a node, such as Kind's `local-path` class, a member whose node is gone cannot start anywhere else: its volume is on that node. The operator handles it in four steps:

1. The member's pod waits `Pending`, and `Ready` is `False` with reason `MemberLost`.
2. After two minutes, the operator deletes the member's claim and its pod. It does this only while the other members follow a leader and have caught up.
3. The StatefulSet makes both again, on another node.
4. The new member starts with an empty log, catches up, and votes again. `Ready` turns `True`.

The operator rebuilds one member at a time, because a member that is catching up does not vote. A node you drain and leave unschedulable for two minutes is handled the same way. The quorum keeps serving throughout, since the remaining members still hold the majority.

## Losing the Majority

With two of three members lost, or three of five, there is no leader, and the operator replaces nothing. Nothing on the members' volumes matters beyond terms and votes, so you start the quorum over. Delete every member's claim, then the pods:

```bash
kubectl -n kates delete pvc -l app.kubernetes.io/instance=kates --wait=false
kubectl -n kates delete pod -l app.kubernetes.io/instance=kates
```

The new quorum counts terms from the start again. Your runs and results are in PostgreSQL and are all still there. A fault the old quorum left behind is removed when its deadline passes, not straight away.

## Installing With Helm Instead

`kates deploy` is the shortest path, but the operator is an ordinary chart, published as `oci://ghcr.io/bmscomp/charts/kates-operator` and signed with cosign. With the chart installed, a minimal `Kates` resource names a version, a database Secret and the Kafka bootstrap address:

```yaml
apiVersion: kates.io/v1alpha1
kind: Kates
metadata:
  name: main
spec:
  version: <version>-native
  replicas: 3                  # 1 (default), 3 or 5; fixed once created
  database:
    secretRef:
      name: kates-db           # keys: url (jdbc:postgresql://...), username, password
  kafka:
    bootstrapServers: krafter-kafka-bootstrap.kafka.svc:9092
```

The chart's README, [kates-operator](../../operator/chart/README.md), covers what this chapter leaves out. That includes the four RBAC modes (one namespace, several, every namespace, or nothing cluster-scoped), which namespaces get the Kates API's cluster role, SASL to Kafka and every field of the resource.

## Switching Modes

You switch modes by removing Kates and deploying again, because `kates deploy` refuses to install one mode over the other. `kates clean` removes either mode. Under the operator, it deletes the `Kates` resource while the operator still runs, so the operator removes what it made, then removes the operator. To leave the rest of the stack in place, remove only Kates: `helm uninstall kates -n kates` for the standalone mode, or, for the operator mode, `kubectl delete kates kates -n kates` and the Secret `kates-api-key`, which the `kates` chart then makes again with a new key.

The two modes keep their run history in different databases: the `kates` chart's own PostgreSQL, or `kates-db`. A switch therefore starts with an empty history unless you carry it over. To keep it, dump the old database before you remove it and restore the dump into the new one, as [A Database That Survives a Node](12-deployment.md#a-database-that-survives-a-node) does for a move to CloudNativePG.

## Limits

The operator mode has these limits:

- The number of members is fixed when the resource is created. The operator doesn't scale a quorum.
- The operator doesn't recover a quorum that lost its majority: you start it over, as [Losing the Majority](#losing-the-majority) shows.
- The operator doesn't support the INTEGRATION_CDC test yet. It has no counterpart to the chart's `rbac.cdcSecretNamespaces` or `rbac.extraRules`.
- The operator doesn't create a highly available PostgreSQL. The quorum protects the Kates API, not its database.
- The operator doesn't notice a changed database Secret. Delete the pods to pick it up.
- The operator mode needs cluster admin, so it doesn't work with `--adminless`.
- One `Kates` resource per namespace can run a quorum.

## Summary

- The standalone mode is the default and is unchanged: older installs, files and jobs keep working.
- The operator mode runs the Kates API as one member or a quorum, behind the same Service and key.
- A quorum keeps a leader through a lost member, and rebuilds a member whose node is gone.
- Draining before an upgrade lets live runs finish before any member is replaced.

The operator keeps the Kates API standing, but it says nothing about whether `krafter` is the cluster you meant to build: [Compliance Checks](compliance.md) writes that architecture down as a file and holds `krafter` to it.
