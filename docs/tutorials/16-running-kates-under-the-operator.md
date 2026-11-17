# Tutorial 16: Running Kates Under the Kates Operator

This tutorial runs the Kates API on `panda` as a quorum of three members under
the Kates operator. You then take its members away one at a time and watch it
keep answering. You will see the leader move, drain the API before an upgrade,
and rebuild a member whose node is unavailable, all through the same `kates` CLI
you used in Tutorial 1.

**Level:** Intermediate · **Duration:** 30 min · **Prerequisites:** Tutorial 1

> [!NOTE]
> The standalone mode, which every other tutorial uses, is still the default
> and is unchanged: `kates deploy` with no new flag installs the `kates`
> chart as before. Nothing in this tutorial is needed to keep an existing
> install working. [Running Kates Under the Kates Operator](../book/kates-operator.md)
> in the book explains both modes in full.

## What You'll Learn

- Deploy Kates under the operator with three members
- Read the `Kates` resource, its leader and its conditions
- Use the same CLI, Service and API key as in the standalone mode
- Watch a new leader take over when the old one dies
- Drain the Kates API so an upgrade loses no live run
- Watch the operator rebuild a member whose node is unavailable

## What You Will End Up With

The operator runs three members, and the `kates` Service sends your traffic to
whichever one leads:

```text
   kates CLI ──► localhost:8080 ──► Service kates ──► the leader (kates.io/quorum-role=leader)
                                                        │
                       kates-0 ◄──── Raft over mTLS ────┼────► kates-2
                       (alpha)          port 9300       │      (gamma)
                                                     kates-1
                                                     (sigma)
                                                        │
                                                        ▼
                                          PostgreSQL kates-db: runs, results, schedules
```

## Prerequisites

You need what Tutorial 1 set up: the tools, the CLI and `panda`, the
three-node Kind cluster. You also need cluster admin, because the operator
brings a CRD and a ClusterRole, and `jq` for one step.

If Kates is already installed standalone, remove only the Kates API: deploy
refuses to install one mode over the other, since both would claim the
Service `kates`. Kafka and the rest of the stack stay as they are.

```bash
helm uninstall kates -n kates
```

The standalone run history is in the chart's own PostgreSQL, which that
command removes. To keep it, dump it first, as
[Switching Modes](../book/kates-operator.md#switching-modes) explains.

## Step 1: Deploy Under the Operator

Ask for the operator mode and three members. On a cluster where the rest of
the stack is already installed, deploy keeps those components, upgrading any
that are out of date, and adds what this mode needs:

```bash
kates deploy --kates-mode operator --kates-replicas 3
```

Deploy installs the Kates operator in the namespace `kates-operator`, a
PostgreSQL for Kates as the release `kates-db`, and the `Kates` resource
`kates`. It then waits until the operator reports the resource `Ready`, which
means three members are up and one of them leads.

With `kates deploy -i`, the wizard asks the same thing after the components:
**How should Kates be installed?**, then the number of members.

## Step 2: Read the Kates Resource

Everything the operator knows is on the `Kates` resource:

```bash
kubectl get kates -n kates
```

Output (on `panda`):

```text
NAME    VERSION         REPLICAS   LEADER    READY   MODE     DRAINED   AGE
kates   1.41.2-native   3          kates-1   True    Active             7m11s
```

`LEADER` is the member that takes the writes. The pod label shows the same
thing, and `-o wide` shows that each member has a node of its own:

```bash
kubectl get pods -n kates -L kates.io/quorum-role -o wide
```

Output (trimmed to the columns that matter here):

```text
NAME         READY   STATUS    NODE    QUORUM-ROLE
kates-0      1/1     Running   alpha
kates-1      1/1     Running   sigma   leader
kates-2      1/1     Running   gamma
kates-db-0   1/1     Running   gamma
```

Each member also reports the quorum in its health check:

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

The request went through the Service `kates`, so the leader answered it. On a
standalone install the same request shows `"enabled": false`.

## Step 3: Use It as You Always Do

The Service is still `kates` and the key is still in the Secret
`kates-api-key`, so the commands from Tutorial 1 work unchanged:

```bash
kates ports
kates health
kates test create --type LOAD --records 100000 --wait
```

Nothing in your scenario files, scripts or CI jobs needs to know which mode
runs the Kates API.

## Step 4: Kill the Leader

Start a longer run in one terminal, so the leader has work live when it dies:

```bash
kates test create --type LOAD --records 5000000
```

In a second terminal, watch the resource:

```bash
kubectl get kates -n kates -w
```

In a third, delete the leader's pod. Use the name `LEADER` showed, `kates-1`
here:

```bash
kubectl delete pod -n kates kates-1
```

What to look for:

1. `LEADER` changes to another member within seconds, and `READY` may show
   `False` with reason `NoLeader` while the election runs.
2. The Service follows the label `kates.io/quorum-role=leader`, which the
   operator moves to the new leader within about five seconds. `kates health`
   answers again through the same `localhost:8080`.
3. The run the old leader had live is reported failed: `kates test list` shows
   it. The new leader fails it before taking any write, and removes any fault
   it had injected, so nothing is left behind.
4. The deleted pod comes back as a follower, and `READY` returns to `True`.

Run the same test again: it succeeds on the new leader.

## Step 5: Drain Before an Upgrade

An upgrade replaces the members one at a time, followers first and the
leader last. Under `mode: Drain` the operator first lets live work finish, so
no run is lost. You can try the drain without changing the version. Set the
mode:

```bash
kubectl patch kates kates -n kates --type merge -p '{"spec":{"mode":"Drain"}}'
```

Then try to start a run:

```bash
kates test create --type LOAD --records 100000
```

The Kates API refuses it with `503`: under a drain, new runs, faults and
resilience runs are refused, schedules stop firing, and live work goes on to
its end. The `DRAINED` column turns `True` once nothing is live. For a real
upgrade you would set the new version in the same patch,
`{"spec":{"mode":"Drain","version":"<new-version>-native"}}`, and wait for
`VERSION` to show it.

The operator never lifts the drain itself. Lift it:

```bash
kubectl patch kates kates -n kates --type merge -p '{"spec":{"mode":"Active"}}'
```

New runs are taken again.

## Step 6: Lose a Node

On `panda` each member's volume lives on its node, so a member whose node is
unavailable can't start anywhere else. Make the leader's node unschedulable,
then delete the leader's pod, so it can't come back there. Use the node and pod
from Step 2:

```bash
kubectl cordon sigma
kubectl delete pod -n kates kates-1
```

Watch the resource and the pods:

```bash
kubectl get kates,pods -n kates -w
```

What to look for:

1. A new leader is elected among the two members left, as in Step 4. The
   quorum keeps serving, because two of three is still a majority.
2. The deleted member waits `Pending`, and `READY` shows `False` with reason
   `MemberLost`.
3. About two minutes later the operator deletes that member's volume claim and
   pod. The StatefulSet makes both again on another node.
4. The new member starts with an empty log, catches up and votes again, and
   `READY` returns to `True`. Nothing was lost: the quorum holds only who
   leads, and every run and result is in PostgreSQL.

Make the node schedulable again:

```bash
kubectl uncordon sigma
```

The rebuilt member stays on the node it moved to until its volume is
replaced again.

## Step 7: Go Back to Standalone, or Clean Up

To go back to the standalone mode, delete the `Kates` resource. The operator
removes the members, their Services and their volumes. Then deploy without
the flag. Deploy hands the API key Secret it wrote for the operator mode to the
`kates` chart, so the key, and your `ports` context, stay the same:

```bash
kubectl delete kates kates -n kates
kates deploy
```

> [!NOTE]
> A `kates` CLI of v1.41.2 or older doesn't hand the Secret over, and the
> standalone install then fails with `invalid ownership metadata` on
> `kates-api-key`. With such a CLI, run
> `kubectl delete secret kates-api-key -n kates` before `kates deploy`, and
> `kates ports` after it to pick up the new key.

The operator and `kates-db` stay installed and unused. Remove them with
`helm uninstall kates-operator -n kates-operator` and
`helm uninstall kates-db -n kates`. To remove the whole stack instead,
`kates clean` handles either mode: it deletes the `Kates` resource while the
operator still runs, then the operator.

## What's Next?

- [Running Kates Under the Kates Operator](../book/kates-operator.md): the
  conditions, losing the majority, installing with Helm, and the limits.
- [The operator chart's README](../../operator/chart/README.md): its RBAC modes
  and every field of the `Kates` resource.
- [Tutorial 3: Chaos Engineering with Kates](03-chaos-engineering.md): runs
  faults against Kafka. Under the operator, a fault a dead leader injected is
  removed by the next leader.
