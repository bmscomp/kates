# Conformity Language Reference

Every field of a [`KafkaReference`](appendix-a-glossary.md#gl-kafkareference), the `conformity.kates.io/v1alpha1` language, with the values it accepts, what a [conformity check](appendix-a-glossary.md#gl-conformity-check) reads for each rule, what the report says, and every finding `kates conformity lint` reports. [Conformity Checks](conformity.md) teaches the language on [`krafter`](appendix-a-glossary.md#gl-krafter), the Kafka cluster under test; look here once you write references of your own. The JSON Schema in `cli/examples/conformity` and the [Kates API](appendix-a-glossary.md#gl-kates-api)'s reader define the language, and lint refuses what the Kates API refuses, so everything below holds in an editor, in the CLI and in the API alike.

## How to Read an Entry

The sections follow the order of a file: the document's own fields, the values a rule can take, how rules are named, the settings, then the rules section by section under `spec`. The last sections cover waivers, the report, lint's findings, the canonical form and the size limits.

Each rule section opens with a table of its fields, and the prose beside the table says how their rule ids read. The Kind column names one of the kinds in [Value Kinds](#value-kinds), which sets the forms a field accepts and how the check compares, and the Observes column says what the check reads on the cluster. The paragraphs under the table say which subjects a failing rule names, and when the rule is UNKNOWN rather than FAIL.

A path such as `spec.brokers.count` says where a field sits in the file, and lint's findings use it. A rule id drops the `spec.` prefix, so the report and a waiver say `brokers.count`.

## The Document

A reference has four top-level fields, all required, and nothing else. The fields under `metadata` are its only other fields outside `spec`:

| Field | Accepts |
|:-------------------|:----------------------------------------------------------|
| `apiVersion` | `conformity.kates.io/v1alpha1`, exactly |
| `kind` | `KafkaReference`, exactly |
| `metadata.name` | Required: a DNS label of up to 63 lower-case letters, digits and `-`. The report shows it as `reference` |
| `metadata.description` | Optional: text of up to 500 characters |
| `spec` | The settings and rules; at least one rule |

A file holds one YAML document, and lint refuses a second one. JSON is YAML too, so `kates conformity check -f` and `kates conformity lint` read a JSON file as well. Anchors and aliases work, but a merge key (`<<`) doesn't: lint reports it as an unknown field, so write the fields out. Write each key once: lint, `kates conformity check` and the Kates API all refuse a key set twice. When both values are the same, `kates conformity fmt` removes the second; when they differ, only you can say which one is meant.

`kates conformity check` sends the file to the Kates API as JSON, which takes at most 256 KiB. A YAML value keeps its text unless YAML reads it as a number, `true` or `false`, or null. That is why a version must be quoted, while a waiver's date needs no quotes.

The first line of every shipped example is a `# yaml-language-server: $schema=` comment, which points editors at the JSON Schema for completion and checks as you type. Lint warns when that comment names a file other than `kafkareference.v1alpha1.schema.json`, because the editor would then accept what the check refuses, or the reverse.

## Values and Constraints

Every rule is a constraint on one value, written bare or as a map. A field you leave out declares no rule, and a field with no value, a YAML key with nothing after it, is refused rather than dropped.

### Constraint Forms

A map names an operator, and the operator says how the observed value is compared. These are all the forms:

| Form | Means | Taken by |
|:-------------------------------------|:--------------------------------------------|:-------------------|
| `3`, `jbod`, `true` | Exactly that value | Every kind |
| `{equals: 3}` | Exactly that value; the form that can carry a severity | Every kind |
| `{min: 3}` | At least that value | Ordered kinds |
| `{max: 5}` | At most that value | Ordered kinds |
| `{min: 3, max: 5}` | Between the two, both ends included | Ordered kinds |
| `{oneOf: [jbod, persistent-claim]}` | Any one of 1 to 50 listed values | Every kind but boolean |
| `severity: warn`, inside any map form | A rule that doesn't hold is WARN, not FAIL | Every kind |

The ordered kinds are counts, ports, quantities, versions and broker settings. `equals` and `oneOf` each stand alone, while `min` and `max` combine, and a map needs at least one of the four operators. A `min` above its `max` is refused, because nothing could meet it.

### Value Kinds

Each field holds one kind of value, and the kind decides how a declared value and an observed one compare:

| Kind | Written as | Compared |
|:--------------|:------------------------------------------------|:--------------------------------------|
| Count | A whole number from 0 to 10,000 | As numbers |
| Port | A whole number from 1 to 65,535 | As numbers |
| Quantity | A Kubernetes quantity, such as `8Gi`, `500m`, `2` or `1.5T` | By amount: `8Gi` is more than `8000Mi` |
| Version | Quoted text of up to four numbers, with an optional leading `v`, such as `"4.2.0"` | Part by part, a missing part as 0 |
| Choice | One word from the field's list | As text, exactly |
| Text | 1 to 253 characters | As text, exactly |
| Boolean | `true` or `false`, unquoted | As is |
| Broker setting | Text, a number, or `true` or `false` | `equals` and `oneOf` as text; `min` and `max` as numbers |

The kinds have rules of their own:

- A count or a port is never quoted and never has a decimal point: `"3"` and `3.0` are both refused.
- A quantity takes the suffixes `m`, `k`, `M`, `G`, `T`, `P`, `E`, `Ki`, `Mi`, `Gi`, `Ti`, `Pi` and `Ei`, and no other, so `8GB` is refused. `500m` is less than `1`.
- A version compares `"4.10"` above `"4.9"` and `"4.2"` equal to `"4.2.0"`. Unquoted, YAML reads `4.10` as the number 4.1, so lint refuses it. A version the cluster reports loses its build suffix before the comparison: `v1.34.11+k3s1` is 1.34.11.
- A choice or text field takes the bare form, `equals` or `oneOf`, never `min` or `max`. A choice is not ordered, so `antiAffinity: preferred` refuses a pod whose anti-affinity is `required`; write `{oneOf: [required, preferred]}` to accept either.
- A boolean takes only the bare form or `equals`, never `oneOf`. A quoted `"true"` is text, and refused.
- A broker setting compared with `equals` or `oneOf` matches by text. `true` and `false` match in any case, and a number is sent as JSON writes it, so `2.0` matches a broker's `2`. Its `min` and `max` must be numbers, and an observed value that isn't a number never meets them.

### Severity

Every rule has the severity `fail` unless its map form says `severity: warn`. A `warn` rule that doesn't hold is WARN, which never fails the check. A required listener carries its severity on the listener, not on its properties. [Statuses and the Overall Result](conformity.md#statuses-and-the-overall-result) in Conformity Checks shows how severity, status and the [exit code](appendix-a-glossary.md#gl-exit-code) fit together.

## Rule Ids

A rule's id is its path under `spec`, so the report, a waiver and JUnit all name the rule the same way. Three cases go in brackets rather than after a dot:

| Declared at | Rule id |
|:------------------------------------------------|:--------------------------------------|
| `spec.brokers.count` | `brokers.count` |
| `spec.brokers.requests.memory` | `brokers.requests.memory` |
| The `min.insync.replicas` key under `spec.kafka.config` | `kafka.config[min.insync.replicas]` |
| A key without a dot under `spec.kafka.config` | `kafka.config.<key>` |
| The item named `tls` under `spec.kafka.listeners.require` | `kafka.listeners.require[tls]` |
| `spec.kafka.listeners.every.auth` | `kafka.listeners.every.auth` |

A required listener is one rule, whatever properties it lists, so `kafka.listeners.require[tls].port` isn't a rule id and no waiver can name it. Settings aren't rules either. Lint's paths keep the `spec.` prefix and count list items from 0, as in `spec.kafka.listeners.require[0].port` and `spec.waivers[1].until`.

## Settings

Settings say which cluster a reference is for and how to read it. They all have defaults, a waiver can't name one, and a `spec` with settings but no rule is refused:

| Setting | Accepts | Default | What it sets |
|:--------------------|:-----------------------------|:------------------------|:----------------------------------|
| `target.platform` | `strimzi` | `strimzi` | The platform; v1alpha1 knows only [Strimzi](appendix-a-glossary.md#gl-strimzi) |
| `target.namespace` | A DNS label | The Kates API's Kafka namespace | The namespace of the `Kafka` resource the file is for |
| `target.name` | A DNS label | The Kates API's Kafka cluster | The name of that `Kafka` resource |
| `target.clusterId` | 22 letters, digits, `-` or `_` | None | The cluster id the `Kafka` resource must report |
| `zoneLabel` | A Kubernetes label key | `topology.kubernetes.io/zone` | The node label that names a [zone](appendix-a-glossary.md#gl-zone) |
| `kafka.source` | `cr` or `brokers` | `cr` | Where `kafka.config` and `kafka.rackAwareness` read |
| `topics.exclude` | Up to 50 topic-name prefixes | None | Topics the `topics` rules skip |

One Kates API checks one Kafka cluster, the one its `kates.topology.kafka-namespace` and `kates.topology.kafka-cluster` properties name: `kafka` and `krafter` by default. The `kates` chart sets them from its `networkPolicy.kafka.namespace` and `kafka.clusterName` values. When `target.namespace` or `target.name` names another, when that `Kafka` resource doesn't exist, or when its `status.clusterId` differs from `target.clusterId`, the Kates API answers 409 before any rule runs, and `kates conformity check` exits 2.

`zoneLabel` is read by the `zones` and `skew` rules of both roles and by `kafka.rackAwareness`. `kafka.source` changes only `kafka.config` and `kafka.rackAwareness`: the [Kafka Settings](#kafka-settings) section shows what each value reads. Lint warns, with `setting-without-effect`, when either setting departs from its default and no rule reads it.

An `exclude` entry is the start of a topic name, up to 249 letters, digits, `.`, `_` and `-`, not a pattern: `__` skips Kafka's internal topics, and `kates-` every topic whose name starts with it.

## How the Check Reads the Cluster

The check only reads, and it reads each source once, the first time a rule needs it. A source it can't read, such as a Kubernetes call that's refused or a Kafka Admin call with no answer within 30 seconds, makes every rule that needs it UNKNOWN, with the reason in the result's `detail`. Each result's `source` names what it read, such as `Kubernetes: pods kafka/strimzi.io/cluster=krafter`. The `Kafka` resource is the exception: a check that can't read it stops before any rule, and the Kates API answers 503, naming the missing grant when the read is refused.

Installed with the `kates` chart's `rbac.scope=namespace`, the Kates API reads the cluster through one Role in the Kafka namespace, so it can list neither nodes, which are cluster-scoped, nor the PodMonitors of other namespaces. [Placement](#placement) and [Monitoring](#monitoring) say what those rules read instead, and [Running Without Cluster Admin](12-deployment.md#running-without-cluster-admin) in the Deployment Guide sets such an install up.

A role's pods are the pods in the Kafka namespace labelled `strimzi.io/cluster` with the cluster's name and `strimzi.io/kind=Kafka`, leaving out any pod being deleted. Brokers are the ones labelled `strimzi.io/broker-role=true`, and controllers `strimzi.io/controller-role=true`, so a pod of a [`KafkaNodePool`](appendix-a-glossary.md#gl-kafkanodepool) with both roles counts as both.

Most rules compare one value per subject: a pod, node, node pool, volume claim, listener, broker or topic. Such a rule holds when every subject's value meets the constraint. A subject with no value never does, and the report shows its value as `unset`. A rule with no subject at all doesn't hold, so `brokers.requests.memory` fails on a cluster with no broker pod; `perNode` and the `topics` rules are the exceptions, and the sections below say why. When a rule fails, the report names the subjects that fall short.

## Versions

The `versions` section holds the platform's three versions, each to a quoted version. It needs at least one of them, and each rule id starts with `versions.`, as in `versions.kafka`:

| Field | Kind | Observes |
|:--------------|:---------|:----------------------------------------------|
| `kubernetes` | Version | The Kubernetes API server's `/version` |
| `strimzi` | Version | The `Kafka` resource's `status.operatorLastSuccessfulVersion` |
| `kafka` | Version | The `Kafka` resource's `status.kafkaVersion` |

`versions.strimzi` reads the version of the Strimzi operator that last reconciled the cluster successfully, so right after an operator upgrade it shows the old version until a reconcile succeeds. A status field that isn't set, or a value that doesn't start with a version, makes the rule UNKNOWN.

## Brokers and Controllers

The `brokers` and `controllers` sections take the same fields, except that only `controllers` takes `dedicated`. Each rule id starts with the section's name, as in `brokers.count` or `controllers.perNode`, and each section needs at least one field:

| Field | Kind | Observes |
|:-------------------------------------|:------------------------------|:----------------------------------------------|
| `count` | Count | How many pods the role has, Ready or not |
| `requests.cpu`, `requests.memory` | Quantity | Each pod's request for its `kafka` container |
| `limits.cpu`, `limits.memory` | Quantity | Each pod's limit for its `kafka` container |
| `storage.type` | Choice: `ephemeral`, `persistent-claim`, `jbod` | Each node pool with the role: its `spec.storage.type` |
| `storage.size` | Quantity | Each pod: the capacity of its bound volume claims, added up |
| `storage.class` | Text | Each of the pods' volume claims: its storage class |
| `zones` | Count | How many zones host one of the role's pods |
| `skew` | Count | The most pods of the role in one zone, minus the fewest in another |
| `perNode` | Count | Each node: how many of the role's pods it runs |
| `antiAffinity` | Choice: `required`, `preferred`, `none` | Each pod's anti-affinity on the node hostname |
| `dedicated`, controllers only | Boolean | Whether no node pool combines the broker and [controller](appendix-a-glossary.md#gl-controller) roles |

`requests` and `limits` each need `cpu` or `memory`, and `storage` needs at least one of its three fields.

### Count and Resources

`count` counts the role's pods whether they're Ready or not, so a rolling restart doesn't fail it, and its `detail` says how many are Ready, as in `3 of 3 Ready`. Whether Kafka sees every broker is a question for `kates cluster check`.

The `requests` and `limits` rules read the container named `kafka` in each pod, and name the pods that fall short. A container that sets no request or limit for the resource observes `unset`, which fails the rule.

### Storage

`storage.type` reads the storage type each node pool with the role declares, and names the pools that fall short. A pool scaled to 0 replicas runs no node, so it isn't counted, and the `detail` says so. The rule fails when no counted pool has the role.

`storage.size` reads what each pod's volumes hold rather than what its pool asks for: it adds up `status.capacity` of each [PersistentVolumeClaim](appendix-a-glossary.md#gl-persistentvolumeclaim) the pod mounts. A claim that is missing or not Bound adds nothing, so a pod with no bound claim observes 0.

`storage.class` reads the storage class of every claim the role's pods mount, and names the claims that fall short. A role on `ephemeral` storage mounts no claim, so the rule fails, with the `detail` `no PVCs: ephemeral storage`.

### Placement

`zones` and `skew` find each pod's zone in its node's zone label, `zoneLabel`. A pod that isn't scheduled, or runs on a node without the label, fails both rules and is named, because the spread can't be shown. Their `detail` lists the pods per zone, as in `alpha: 1, gamma: 1, sigma: 1`. `skew` counts only the zones that host a pod, so one zone with pods gives a skew of 0.

When the Kates API may not list nodes, which are cluster-scoped, both rules take each pod's zone from the EndpointSlices of Strimzi's headless Service, `<cluster>-kafka-brokers`, and the `detail` ends `zones from the EndpointSlices of kafka/krafter-kafka-brokers`. Endpoints carry only `topology.kubernetes.io/zone`, so with another `zoneLabel` the rules are UNKNOWN. A pod with no address yet has no endpoint, so it counts as in no zone unless another Kafka pod runs on its node.

`perNode` names each node that runs more of the role's pods than the rule allows. A pod that isn't scheduled runs on no node, so it counts nowhere, and a role with no pods holds the rule: no node runs too many.

`antiAffinity` reads each pod's scheduling rules. It observes `required` when a required pod anti-affinity term with the topology key `kubernetes.io/hostname` selects the pod's own labels, `preferred` when only a preferred term does, and `none` otherwise. It reads the rule Kubernetes was given, while `perNode` reads where the pods run today.

`dedicated` observes `true` when no node pool lists both the `broker` and `controller` roles, leaving out pools scaled to 0 replicas, and its `detail` names any pool that does. Keeping the roles apart keeps a broker's load and restarts away from the KRaft [quorum](appendix-a-glossary.md#gl-quorum). `brokers.dedicated` is refused: only controllers take it.

## Kafka Settings

The `kafka` section reads the `Kafka` resource: its authorization, its [listeners](appendix-a-glossary.md#gl-listener), its rack and its broker settings. It needs at least one field, and `kafka.source` is a setting, not a rule:

| Field | Kind | Observes |
|:----------------------|:---------------------------|:--------------------------------------|
| `rackAwareness` | Boolean | Whether the rack comes from the zone label, and every broker has a [rack](appendix-a-glossary.md#gl-rack) |
| `authorization` | Choice: `none`, `simple`, `opa`, `keycloak`, `custom` | `spec.kafka.authorization.type`, `none` when unset |
| An item of `listeners.require` | A required listener | The listener of that name, and each property the item lists |
| `listeners.every.type` | Choice: `internal`, `cluster-ip`, `route`, `loadbalancer`, `nodeport`, `ingress` | Each listener's `type` |
| `listeners.every.tls` | Boolean | Each listener's `tls`, `false` when unset |
| `listeners.every.auth` | Choice: `none`, `tls`, `scram-sha-512`, `oauth`, `custom` | Each listener's `authentication.type`, `none` when unset |
| A key of `config` | Broker setting | The value the `Kafka` resource sets, or the value each broker uses |

Each rule id starts with `kafka.`, as in `kafka.rackAwareness`. A required listener's id carries its name, `kafka.listeners.require[<name>]`, and a broker setting's its key, `kafka.config[<key>]`. `authorization` and the listener rules always read the `Kafka` resource, whatever `kafka.source` says.

### Rack Awareness

`kafka.rackAwareness` first reads `spec.kafka.rack.topologyKey`. When it isn't the zone label, the rule observes `false`, and the `detail` says whether the rack is unset or names which key it uses. When it is, the rule then checks every broker, in one of two ways:

| `kafka.source` | Every broker must | A broker that falls short |
|:---------------|:-------------------------------------------|:------------------------------------|
| `cr`, the default | Run on a node that carries the zone label | Is named by its pod |
| `brokers` | Report a rack to the Kafka Admin API | Is named by its broker id |

With `cr`, it reads the zone label as the [placement](#placement) rules do, from the EndpointSlices when the Kates API may not list nodes. With `brokers`, a broker that a node pool's `status.nodeIds` lists but Kafka doesn't report, because it's fenced or hasn't started, makes the rule UNKNOWN when no reported broker falls short.

### Listeners

A required listener names a listener by `name` and lists the properties it must have: `type`, `port`, `tls` and `auth`, at least one of them. Each property is a constraint of its own kind: a choice from the listener types, a port, a boolean, or a choice from the authentication types. `severity` goes on the item, so a property can't say `warn`. This item requires a listener named `external` on a load balancer, whatever its port, with TLS and either mTLS or SCRAM, and only warns when it's missing:

```yaml
spec:
  kafka:
    listeners:
      require:
        - name: external
          type: loadbalancer
          tls: true
          auth: {oneOf: [tls, scram-sha-512]}
          severity: warn
```

The rule holds when `spec.kafka.listeners` has a listener of that name and each listed property meets its constraint. Without one, the rule observes `missing`, with the `detail` `no listener named external`. With one that differs, the `detail` lists each property that does, as in `tls is false`. A name is a Strimzi listener name, up to 11 lower-case letters and digits, and a file lists at most 50 required listeners, each name once.

`port` is the port inside Kafka, not the one clients reach through a route, an ingress, a load balancer or a node port. Strimzi refuses a listener port below 9092, so lint refuses a required port that allows nothing from 9092 up.

The `every` rules hold every listener in the `Kafka` resource to a type, TLS or authentication, and name the listeners that fall short. They fail when the resource declares no listener. `listeners` needs `require` or `every`, and `every` needs at least one property.

### Broker Settings

`config` maps a broker setting's name to a constraint. A name is lower-case letters and digits joined by `.`, `_` or `-`, up to 128 characters, and a file holds at most 200 of them. Where the value comes from depends on `kafka.source`:

| `kafka.source` | `config` reads | Subjects | Needs |
|:----------------|:----------------------------------------|:-------------|:-----------------------------|
| `cr`, the default | What `spec.kafka.config` sets | None: one value | The Kubernetes API only |
| `brokers` | The value each broker uses, its default included | `broker 0`, `broker 1`… | The Kates API's connection to Kafka |

With `cr`, a setting the `Kafka` resource leaves to Kafka's default observes `unset`, with the `detail` `not set in the Kafka CR`, and fails even when the default would meet the rule. Write the setting into the resource, or use `brokers`.

With `brokers`, the Kates API first checks that its Kafka connection reaches the cluster whose id the `Kafka` resource reports, and the rule is UNKNOWN when it doesn't. A sensitive setting, such as a password, is always UNKNOWN, because Kafka never returns its value. So is a rule whose brokers show no gap while some broker couldn't be read. A broker can't be read when it doesn't answer, when a node pool lists it but Kafka doesn't report it, or when it reports no value because Kafka derives the setting from another one, as `log.retention.ms` from `log.retention.hours`. When another broker does show a gap, the rule fails, and the `detail` names the brokers it couldn't read. A setting that no broker reports fails, with the `detail` `brokers do not report <key>`.

## Topics

The `topics` section holds every [topic](appendix-a-glossary.md#gl-topic) to the durability it needs, Kafka's internal topics included, because a broker default applies only to a topic created without a value of its own. It needs at least one of its two rules, `topics.replicationFactor` and `topics.minInsyncReplicas`:

| Field | Kind | Observes, per topic |
|:----------------------|:--------|:----------------------------------------------|
| `replicationFactor` | Count | The smallest replica set across the topic's partitions |
| `minInsyncReplicas` | Count | The [`min.insync.replicas`](appendix-a-glossary.md#gl-min-insync-replicas) Kafka applies to the topic: its own, or the broker default |

These rules always read through the Kates API's connection to Kafka, whatever `kafka.source` says: they list every topic, internal ones included, then describe the topics and their settings. A topic whose name starts with a prefix in `exclude` is skipped.

A failing rule names the first 50 topics that fall short, and its `detail` counts them all, as in `4 of 61 topics`. When `exclude` leaves no topic to check, both rules hold, with the `detail` `no topic to check`.

## Monitoring

The `monitoring` section checks that the cluster can be watched: its metrics, its [Kafka Exporter](appendix-a-glossary.md#gl-kafka-exporter), and a PodMonitor that scrapes its brokers. It needs at least one field, and each rule id starts with `monitoring.`, as in `monitoring.podMonitor`:

| Field | Kind | Observes |
|:----------------|:------------------------|:----------------------------------------------|
| `metrics` | Choice: `none`, `jmxPrometheusExporter`, `strimziMetricsReporter` | `spec.kafka.metricsConfig.type`, `none` when unset |
| `kafkaExporter` | Boolean | Whether the `Kafka` resource sets `spec.kafkaExporter` |
| `podMonitor` | Boolean | Whether a Prometheus Operator PodMonitor selects every broker pod |

`monitoring.podMonitor` lists the PodMonitors in every namespace. One counts when its `namespaceSelector` covers the Kafka namespace and its `selector` selects every broker pod. A `namespaceSelector` covers the namespace when it says `any: true` or its `matchNames` lists the namespace; one that names no namespace covers only the PodMonitor's own. The rule observes `false` when the PodMonitor CRD isn't installed or there's no broker pod, and the `detail` says which. When it holds, the `detail` names the PodMonitors that select the brokers.

When the Kates API may not list PodMonitors in every namespace, it lists those of the Kafka namespace. The rule holds when one there selects every broker pod, and is UNKNOWN otherwise, since one in a namespace it can't read might.

## Waivers

`spec.waivers` lists the known gaps the check tolerates for a while, at most 200 of them. A [waiver](appendix-a-glossary.md#gl-waiver) needs all four of its fields:

| Field | Accepts |
|:---------|:--------------------------------------------------------------------|
| `rule` | The id of a rule this file declares, up to 200 characters; each rule takes at most one waiver |
| `reason` | 1 to 500 characters: why the gap is accepted |
| `by` | 1 to 100 characters: who owns it |
| `until` | A date such as `2027-03-31`: the last day the waiver applies, read in UTC |

The check compares `until` with its own date, in UTC, and the result depends on whether the rule holds:

| On the check's date | The rule's status | The result also carries |
|:-------------------------------------|:------------------|:----------------------------------------------|
| On or before `until`, rule not PASS | WAIVED | `waiver`: the rule's own status, the reason, the owner and the date |
| On or before `until`, rule PASS | PASS | `note`: `waiver no longer needed` |
| After `until` | The rule's own | `note`: `waiver expired on <date>` |

A waiver covers any status but PASS, UNKNOWN included, and a WAIVED rule never fails the check. The report keeps the rule's `expected`, `observed` and `subjects`, so the gap stays visible while it's waived, and the table view shows a PASS that carries a note. A waiver for a date already past is accepted, and lint warns about it with `waiver-expired`.

## The Report

`POST /api/conformity/check` answers 200 with a report whatever the verdict, and `kates conformity check -o json` prints it. Its top-level fields describe the check:

| Field | Holds |
|:--------------|:----------------------------------------------------------------|
| `apiVersion`, `kind` | `conformity.kates.io/v1alpha1` and `ConformityReport` |
| `reference` | The reference's `metadata.name` |
| `target` | The cluster checked: `platform`, `namespace`, `name` and `clusterId` |
| `checkedAt` | When the check ran, in UTC |
| `cluster` | Context, not a rule: `ready`, `generation`, `observedGeneration` and `podsNotReady` |
| `verdict` | `PASS`, `FAIL` or `UNKNOWN` |
| `summary` | How many results ended in each status: `pass`, `fail`, `warn`, `waived`, `unknown` |
| `results` | One result per declared rule |

`cluster` says whether the `Kafka` resource is Ready, whether the operator has reconciled its latest generation, and how many of its pods aren't Ready. Results may change while such a cluster settles, and the CLI's banner warns when it isn't Ready.

Each result describes one rule. A field with nothing to say is left out:

| Field | Holds |
|:------------|:--------------------------------------------------------------------|
| `rule` | The rule id |
| `status` | `PASS`, `FAIL`, `WARN`, `WAIVED` or `UNKNOWN` |
| `severity` | `fail` or `warn` |
| `expected` | The constraint in its map form, such as `{"min": 3}` |
| `observed` | The value found; over several subjects, the distinct values of those that fall short, or of all when it holds |
| `subjects` | Up to 50 subjects that fall short, by name |
| `detail` | What else the check found, or why it couldn't read |
| `source` | What the check read for the rule |
| `waiver` | Under an active waiver: the rule's own `status`, `reason`, `by` and `until` |
| `note` | `waiver no longer needed` or `waiver expired on <date>` |

Results sort by status, FAIL first, then UNKNOWN, WARN, WAIVED and PASS, and by rule id within a status. When more than 50 subjects fall short, the `detail` says how many, and that the first 50 are listed.

The `verdict` is `FAIL` when any result is FAIL. Otherwise it is `UNKNOWN` when a rule with severity `fail` is UNKNOWN, and `PASS` in every other case. [POST /api/conformity/check](11-api-reference.md#post-apiconformitycheck) lists the error statuses for a reference the check can't run.

## Lint Findings

`kates conformity lint` reads a file as a compiler does, offline, and `kates conformity check` runs the same lint before it calls the Kates API. It works in phases: YAML syntax, duplicates, the JSON Schema, the checks the Kates API makes beyond the schema, then the cross-rule checks, which run only on a file with no error so far. It reads waivers against today's date in UTC.

Each finding is one line, `file:line:col: severity: path: message [code]`, with the source line and a caret under it, and `(kates conformity fmt removes it)` when the formatter removes the cause. An error makes lint exit 1 and stops `kates conformity check` with exit 2. A warning stops neither.

### Findings About the Language

These codes report a file the language can't read as written. Every one is an error except where the table says otherwise:

| Code | Reported when |
|:------------------------|:------------------------------------------------------------------|
| `syntax` | The file is empty, isn't YAML, or holds more than one document |
| `schema-header` | Warning: the `$schema` comment names another schema than `kafkareference.v1alpha1.schema.json` |
| `duplicate-key` | Removed by `fmt`: a key is set twice to the same value |
| `conflicting-key` | A key is set twice to different values |
| `duplicate-value` | Warning, removed by `fmt`: a value is listed twice, as in a `oneOf` or `exclude` |
| `duplicate-item` | Removed by `fmt`: a waiver or required listener is listed twice, the same both times |
| `unknown-field` | A field the language doesn't have, with the closest known name when one is within two edits |
| `required` | A required field is missing |
| `type` | A value of the wrong kind, such as an unquoted version, or a key with no value |
| `enum` | A choice outside its field's list |
| `pattern` | A name, cluster id, label key, quantity, version, rule id or prefix in the wrong shape |
| `format` | An `until` that isn't a date such as `2026-12-31` |
| `length` | Text that is empty or too long |
| `range` | A number outside its field's range, such as a count above 10,000 |
| `empty` | A section that declares nothing, or a list with no value |
| `size` | Too many keys or items, such as more than 50 values in a `oneOf` |
| `constraint` | A map without an operator, `equals` or `oneOf` not alone, or `severity` on a listener's property |
| `schema` | A wrong `apiVersion` or `kind`, or a required listener or `topics` section that names nothing to check |
| `no-rule` | `spec` declares no rule, only settings |
| `duplicate-listener` | Two required listeners share a name but list other properties |
| `duplicate-waiver` | Two different waivers name one rule |
| `unknown-rule` | A waiver names no rule the file declares |
| `min-above-max` | A constraint's `min` is above its `max` |

### Rules No Cluster Can Meet

The cross-rule checks read several constraints together. An error means no cluster can pass the file. A warning means it can, but promises less than it seems to. The constraints these checks read include a [replication factor](appendix-a-glossary.md#gl-replication-factor) and `min.insync.replicas` set under `kafka.config` or `topics`:

| Code | Severity | Reported when |
|:------------------------------------|:---------|:--------------------------------------------------------|
| `zones-unreachable` | Error | No allowed `count` and `zones` fit: each zone needs a pod, and every pod a zone |
| `skew-unreachable` | Error | No allowed count split over allowed zones gives an allowed `skew` |
| `pernode-cannot-place-count` | Error | `perNode` can't spread any allowed count over enough nodes, while another rule requires every pod scheduled |
| `controller-quorum-even` | Warning | `controllers.count` allows only even sizes, which tolerate no more failures than one fewer |
| `controller-quorum-zone-loss` | Warning | No allowed controller count keeps a majority after losing the zone with the most controllers |
| `config-value-kafka-refuses` | Error | With `kafka.source: brokers`, a constraint on a replication factor, a minimum ISR or unclean election admits no value Kafka accepts |
| `sensitive-config-from-brokers` | Error | With `kafka.source: brokers`, a password-type setting, whose value Kafka never returns |
| `replication-factor-exceeds-brokers` | Warning | A replication factor needs more brokers than `brokers.count` allows |
| `min-isr-not-below-rf` | Warning | A minimum ISR isn't below its replication factor or broker count, so `acks=all` writes get refused |
| `isr-lost-with-one-zone` | Warning | Losing one zone leaves fewer replicas than the minimum ISR, even with the most zones and replicas allowed |
| `unclean-election-required` | Warning | `unclean.leader.election.enable` must be `true`, on a cluster that may have more than one broker |
| `request-above-limit` | Error | A request's least allowed value is above its limit's greatest, and `count` requires at least one pod |
| `byte-quantity-too-small` | Warning | A memory or storage size below a mebibyte, usually a lost unit: `512m` means milli |
| `ephemeral-single-node` | Warning | `storage.type` allows only `ephemeral`, and `count` allows at most one pod |
| `strimzi-1-with-pre-kraft-kafka` | Error | `versions.strimzi` requires 1.0 or later, and `versions.kafka` allows nothing from 3.0 up |
| `listener-contradicts-every` | Error | A required listener's `type`, `tls` or `auth` shares no value with `listeners.every` |
| `listener-port-below-9092` | Error | A required listener's `port` allows nothing from 9092 up |
| `podmonitor-without-metrics` | Warning | `podMonitor: true` with `metrics: none` and no Kafka Exporter required |
| `authorization-without-authentication` | Warning | `simple` or `keycloak` authorization, with every listener's `auth` only `none` |
| `setting-without-effect` | Warning | `zoneLabel` or `kafka.source: brokers` set, with no rule that reads it |
| `waiver-expired` | Warning | A waiver's `until` has passed, so its rule counts again |
| `rule-never-passes` | Warning | One of the errors above, on a rule the check tolerates |

The durability warnings rest on how Kafka acknowledges a write: with [`acks=all`](appendix-a-glossary.md#gl-acks), a write waits for every in-sync replica, and Kafka refuses it when fewer than `min.insync.replicas` are in sync. [Unclean leader election](appendix-a-glossary.md#gl-unclean-leader-election) lets a replica that fell behind become leader, discarding writes the old leader acknowledged.

### Tolerated Rules

Every error in the cross-rule table can fire on a rule the check tolerates: one whose severity is `warn`, or that an active waiver covers. The check can then still pass, so lint reports `rule-never-passes` instead, a warning that names how each rule is tolerated. The `spec` of this file, `tolerated.yaml`, waives a zones rule that two brokers can never meet:

```yaml
spec:
  brokers:
    count: 2
    zones: {min: 3}
  waivers:
    - rule: brokers.zones
      reason: OPS-412 adds a third broker
      by: platform-team
      until: 2027-03-31
```

Output, wrapped, without the source line:

```text
tolerated.yaml:8:12: warning: spec.brokers.zones: allows at least 3, but
  spec.brokers.count allows 2: each zone that counts hosts a pod, and with any pod
  every pod must run in a zone, so no cluster can meet both [zones-unreachable];
  it is tolerated, so the check does not fail, but it can never pass:
  spec.brokers.zones is waived until 2027-03-31, a waiver no change to the cluster
  can retire [rule-never-passes]
0 errors, 1 warning in 1 file
```

The finding quotes the error it stands for, with that error's code in brackets, and lint exits 0. The waiver can never retire on its own: when the date passes, the check fails, so the fix is a third broker or a lower `zones` rule.

## Canonical Form

`kates conformity fmt` writes a reference in one form, so a reviewer reads a diff of meaning rather than layout. It changes no rule:

- Keys follow the schema's order, and a `config` section's keys are alphabetical.
- Indentation is two spaces, and a constraint map sits on one line, as in `{min: 3}`.
- Each constraint takes its shortest form: `{equals: 3}`, `{oneOf: [3]}` and `{min: 3, max: 3}` become `3`, and `severity: fail` goes.
- Exact duplicates go: a key set twice to the same value, a value repeated in a list, and a waiver or listener listed twice.
- Comments stay with what they annotate, and a comment above the first key stays at the top.

It refuses a file that doesn't parse, or one that sets a key to two different values, since only you can say which one you meant. The [conformity fmt](10-cli-reference.md#conformity-fmt) entry in the CLI Reference lists its flags.

## Size Limits

These are the bounds on a reference and its report. Lint, the JSON Schema and the Kates API hold a reference to the same ones, except the size of the whole reference, which only the Kates API measures:

| What | At most |
|:--------------------------------------------|:-------------------------------|
| The reference sent to the Kates API | 256 KiB |
| `metadata.name` | 63 characters |
| `metadata.description` | 500 characters |
| A count | 10,000 |
| Values in one `oneOf` | 50 |
| Keys under `kafka.config` | 200, each up to 128 characters |
| Required listeners | 50, each name up to 11 characters |
| `topics.exclude` | 50 prefixes, each up to 249 characters |
| Waivers | 200 |
| A waiver's `rule`, `reason` and `by` | 200, 500 and 100 characters |
| A text value, such as a storage class | 253 characters |
| Subjects named in one result | 50 |

## Shipped Examples

`cli/examples/conformity` holds the JSON Schema and four references to copy from. Each one starts with the schema comment, and CI holds each one to the JSON Schema and to the Kates API's reader:

| File | What it declares |
|:--------------------------|:----------------------------------------------------------------|
| `minimal.yaml` | Three brokers or more, in three zones or more, one per node |
| `payments-krafter.yaml` | What the payments workload needs from `krafter`, with a warning and a waiver |
| `prod-3az.yaml` | Production Kafka in three zones, dedicated controllers, TLS on every listener |
| `kind-ci.yaml` | The single-broker cluster CI deploys on [Kind](appendix-a-glossary.md#gl-kind), which must pass every rule |
| `kafkareference.v1alpha1.schema.json` | The JSON Schema editors validate against |
