{{/* Chart name (kafka-common). */}}
{{- define "mirror-maker2.name" -}}
{{- include "kafka-common.name" . }}
{{- end }}

{{/* Fully qualified app name (kafka-common). */}}
{{- define "mirror-maker2.fullname" -}}
{{- include "kafka-common.fullname" . }}
{{- end }}

{{/* Release namespace, overridable (kafka-common). */}}
{{- define "mirror-maker2.namespace" -}}
{{- include "kafka-common.namespace" . }}
{{- end }}

{{- define "mirror-maker2.chart" -}}
{{- include "kafka-common.chart" . }}
{{- end }}

{{- define "mirror-maker2.selectorLabels" -}}
{{- include "kafka-common.selectorLabels" . }}
{{- end }}

{{/* Standard labels (kafka-common) — built as a map, so no key can repeat. */}}
{{- define "mirror-maker2.labels" -}}
{{- include "kafka-common.labels" (dict "ctx" . "component" "mirror-maker2" "partOf" "kates") }}
{{- end }}

{{/*
Standard labels with app.kubernetes.io/component replaced. Call with
(dict "component" "test" "ctx" $).
*/}}
{{- define "mirror-maker2.componentLabels" -}}
{{- include "kafka-common.labels" (dict "ctx" .ctx "component" .component "partOf" "kates") }}
{{- end }}

{{- define "mirror-maker2.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- .Values.serviceAccount.name | default (include "mirror-maker2.fullname" .) }}
{{- else }}
{{- .Values.serviceAccount.name | default "default" }}
{{- end }}
{{- end }}

{{/* Strimzi labels the MM2 Connect pods <cluster>-mirrormaker2. */}}
{{- define "mirror-maker2.podSelectorLabels" -}}
strimzi.io/name: {{ include "mirror-maker2.fullname" . }}-mirrormaker2
strimzi.io/kind: KafkaMirrorMaker2
{{- end }}

{{/*
Bootstrap servers for a cluster entry (target or a mirror source), via
kafka-common. Call with (dict "c" <clusterValues> "ctx" $). An explicit
`bootstrapServers` wins; otherwise the in-cluster FQDN is computed from
`clusterName` + `namespace` (default: krafter in kafka) and the TLS port.
*/}}
{{- define "mirror-maker2.bootstrap" -}}
{{- include "kafka-common.bootstrap" (dict "c" .c "ctx" .ctx "defaultCluster" "krafter" "defaultNamespace" "kafka") -}}
{{- end }}

{{/*
Normalise a Kafka version for semverCompare ("2.8" -> 2.8.0), via kafka-common.
*/}}
{{- define "mirror-maker2.semver" -}}
{{- include "kafka-common.semver" . -}}
{{- end }}

{{/*
The replication policy class in force. `class` wins if set; otherwise `mode`
selects one. Empty means "leave it to MirrorMaker's default", which is
DefaultReplicationPolicy — the one that renames topics to <alias>.<topic>.
*/}}
{{- define "mirror-maker2.replicationPolicyClass" -}}
{{- $rp := .Values.replicationPolicy | default dict -}}
{{- if $rp.class -}}
{{- $rp.class -}}
{{- else if eq ($rp.mode | default "default") "identity" -}}
org.apache.kafka.connect.mirror.IdentityReplicationPolicy
{{- end -}}
{{- end }}

{{/*
Whether the effective policy preserves topic names. Drives the internal-topic
exclusions below and the wording in NOTES/tests.
*/}}
{{- define "mirror-maker2.isIdentityPolicy" -}}
{{- $class := include "mirror-maker2.replicationPolicyClass" . -}}
{{- if and $class (contains "Identity" $class) -}}true{{- end -}}
{{- end }}

{{/*
topicsExcludePattern for one mirror. Call with (dict "m" <mirror> "ctx" $).
An explicit per-mirror value wins verbatim; otherwise the chart computes one.

WHY THIS IS NOT JUST A PASSTHROUGH. Two ways a mirror eats its own output:

1. Under an IDENTITY policy the replicated topics keep their names, so
   `heartbeats`, `checkpoints`, the offset-syncs topic and the Connect
   internal topics all become eligible for replication. MirrorMaker's stock
   exclusions (`.*[\-\.]internal`, `.*\.replica`, `__.*`) do not cover them,
   because under the default policy those topics are already renamed out of
   the way.

2. Under the DEFAULT policy on a loopback (source == target, which is what
   the chart ships so it smoke-tests without a second cluster), `orders`
   lands as `source.orders` on the same cluster, still matches `.*`, and
   MirrorMaker's cycle detection does not catch it because the target alias
   differs from the source alias — so it is mirrored again as
   `source.source.orders`, every refresh interval, forever.

So the computed pattern always carries the stock exclusions plus
`<alias>\..*` (anything already wearing this source's prefix) and
`<groupId>-.*` (the Connect internal topics), and identity mode adds MM2's
own topic names on top. Set `topicsExcludePattern` on the mirror to take
over entirely.
*/}}
{{- define "mirror-maker2.topicsExcludePattern" -}}
{{- $m := .m -}}{{- $ctx := .ctx -}}
{{- if $m.topicsExcludePattern -}}
{{- $m.topicsExcludePattern -}}
{{- else if (($ctx.Values.replicationPolicy).excludeInternalTopics | default true) -}}
{{- $gid := $ctx.Values.target.groupId | default "mirror-maker2" -}}
{{- $alias := $m.source.alias | default "source" -}}
{{- $sep := ($ctx.Values.replicationPolicy).separator | default "." -}}
{{- $base := printf ".*[\\-\\.]internal,.*\\.replica,__.*,%s-.*,%s%s.*" $gid $alias (regexQuoteMeta $sep) -}}
{{- if include "mirror-maker2.isIdentityPolicy" $ctx -}}
{{- printf "%s,mm2-.*,heartbeats,checkpoints,.*\\.heartbeats,.*\\.checkpoints\\.internal" $base -}}
{{- else -}}
{{- $base -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Connector config with the replication policy and the offset-syncs location
merged in. Call with (dict "cfg" <user config> "ctx" $ "m" <mirror>). User
keys win — an explicit `replication.policy.class` in values is never
overwritten by `mode`.

`offset-syncs.topic.location` is derived rather than hand-set on both
connectors because Kafka requires the two to agree; validate refuses a
hand-set value that disagrees with the mirror's.
*/}}
{{- define "mirror-maker2.connectorConfig" -}}
{{- $ctx := .ctx -}}
{{- $user := deepCopy (.cfg | default dict) -}}
{{- $derived := dict -}}
{{- $class := include "mirror-maker2.replicationPolicyClass" $ctx -}}
{{- if $class -}}
{{- $_ := set $derived "replication.policy.class" $class -}}
{{- end -}}
{{- with ($ctx.Values.replicationPolicy).separator -}}
{{- $_ := set $derived "replication.policy.separator" . -}}
{{- end -}}
{{- with .m -}}
{{- $loc := include "mirror-maker2.offsetSyncsLocation" (dict "m" . "ctx" $ctx) -}}
{{- $_ := set $derived "offset-syncs.topic.location" $loc -}}
{{- end -}}
{{- $merged := merge $user $derived -}}
{{- if $merged -}}
{{- toYaml $merged -}}
{{- end -}}
{{- end }}

{{/*
Shell lines that write a client.properties for a cluster entry (kafka-common).
Call with (dict "c" <cluster> "env" "<ENV VAR holding the password>" "file" "<path>").
*/}}
{{- define "mirror-maker2.testClientProps" -}}
{{- include "kafka-common.test.clientProps" . }}
{{- end }}

{{/* True when a cluster entry is beyond what the test pods can configure (kafka-common). */}}
{{- define "mirror-maker2.testAuthUnsupported" -}}
{{- include "kafka-common.test.authUnsupported" . -}}
{{- end }}

{{/*
The name a source topic takes on the target, under the policy in force. Call
with (dict "topic" <name> "alias" <source alias> "ctx" $).
*/}}
{{- define "mirror-maker2.replicatedTopic" -}}
{{- if include "mirror-maker2.isIdentityPolicy" .ctx -}}
{{- .topic -}}
{{- else -}}
{{- printf "%s%s%s" .alias ((.ctx.Values.replicationPolicy).separator | default ".") .topic -}}
{{- end -}}
{{- end }}

{{/*
Guardrails, evaluated at render time.

Both of the things checked here fail LATE and misleadingly at runtime:
a broker below the KIP-896 floor answers UNSUPPORTED_VERSION, which the
connector reports as a connection problem an hour after install; and a
replication factor above the target's broker count fails inside topic
creation, long after `helm install` has reported success. Neither is
discoverable from `kubectl get kafkamirrormaker2`, which will happily say
Ready while nothing replicates.
*/}}
{{- define "mirror-maker2.validate" -}}
{{- $ctx := . -}}
{{- /* The whole dashboard block is gone. The mirror and migration boards
       are delivered by charts/monitoring for the entire cluster, and their
       $namespace/$cluster variables pick a release — so the per-release
       ConfigMaps, the uid/title rewrite and the no-slo/identity variants
       this block configured have nothing left to configure. Refused rather
       than ignored, because a values key that is silently ignored is the
       failure this whole branch is about. The two migration thresholds it
       once held were never movable from here anyway: they are baked into
       the generated JSON (a threshold colour, and the `< bool 5000` inside
       the go/no-go query, where a Grafana variable does not parse). */ -}}
{{- if hasKey .Values "dashboard" -}}
{{- fail "mirror-maker2: the dashboard block was removed in 0.11.0. The mirror and migration boards are delivered by charts/monitoring (dashboards.enabled there) for every release in the cluster, and the board's $namespace/$cluster variables select a release — there is no per-release copy, uid rewrite, or no-slo/identity variant to configure any more. The migration thresholds are baked into the board: edit DRAINED_MS or FRESH_MS in dashboards/mirror-maker2-migration/board.py and run scripts/gen-dashboards.py. Remove the dashboard block to proceed." -}}
{{- end -}}
{{- /* `latest` is not a version. A mirror pinned to a moving tag changes
       Kafka client version on any pod restart — a rolling restart on a
       Tuesday can move the workers across the KIP-896 floor that every other
       rail in this file is checking, and nothing in the CR would record that
       it happened. Refused for the workers and for every helper image. */ -}}
{{- include "kafka-common.rails.noFloatingTag" (dict "chart" "mirror-maker2" "images" (dict "image" (.Values.image | default "") "preflight.image" ((.Values.preflight).image | default "") "secretSync.image" ((.Values.secretSync).image | default "") "testImages.kubectl" ((.Values.testImages).kubectl | default ""))) -}}
{{- $c := .Values.compatibility | default dict -}}
{{- $floor := include "mirror-maker2.semver" ($c.minSourceVersion | default "2.1.0") -}}
{{- $workers := include "mirror-maker2.semver" (.Values.version | default "4.3.0") -}}
{{- $brokers := int (.Values.target.brokerCount | default 0) -}}
{{- range .Values.mirrors -}}
{{- $alias := .source.alias | default "source" -}}
{{- $v := .source.kafkaVersion | default "" -}}
{{- if $c.enforce -}}
  {{- if eq $v "" -}}
    {{- if $c.requireDeclaredVersion -}}
      {{- fail (printf "mirror-maker2: mirrors[%s].source.kafkaVersion is not declared and compatibility.requireDeclaredVersion is true. Declare the source's Kafka version so the KIP-896 floor (>= %s) can be checked before deploy rather than after." $alias $floor) -}}
    {{- end -}}
  {{- else -}}
    {{- $sv := include "mirror-maker2.semver" $v -}}
    {{- if not (semverCompare (printf ">=%s" $floor) $sv) -}}
      {{- fail (printf "mirror-maker2: source %q declares Kafka %s, below the %s floor. Kafka %s clients — which is what these MirrorMaker 2 workers are — removed the older protocol API versions (KIP-896), so this mirror cannot read that broker at all. Migrate via an intermediate 3.x cluster, or run MM2 on an older Kafka line. Set compatibility.enforce=false only if you have verified the wire yourself." $alias $v $floor $workers) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if gt $brokers 0 -}}
  {{- range $key, $val := ((.sourceConnector).config | default dict) -}}
    {{- if hasSuffix "replication.factor" $key -}}
      {{- if gt (int $val) $brokers -}}
        {{- fail (printf "mirror-maker2: mirrors[%s].sourceConnector.config.%s is %v but target.brokerCount is %v. The connector would fail inside topic creation, minutes after a successful install." $alias $key $val $brokers) -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
  {{- range $key, $val := ((.checkpointConnector).config | default dict) -}}
    {{- if hasSuffix "replication.factor" $key -}}
      {{- if gt (int $val) $brokers -}}
        {{- fail (printf "mirror-maker2: mirrors[%s].checkpointConnector.config.%s is %v but target.brokerCount is %v." $alias $key $val $brokers) -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}
{{- if gt $brokers 0 -}}
{{- range $key, $val := ($ctx.Values.target.config | default dict) -}}
  {{- if hasSuffix "replication.factor" $key -}}
    {{- if gt (int $val) $brokers -}}
      {{- fail (printf "mirror-maker2: target.config.%s is %v but target.brokerCount is %v. The Connect internal topics would never be creatable." $key $val $brokers) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{- /* ── Aliases must be unique ─────────────────────────────────────────
       Two mirrors sharing an alias share their replicated topic prefix,
       their offset-syncs topic and their checkpoints topic. Under the
       default policy that silently interleaves two sources into one set of
       target topics; MirrorMaker does not complain. */ -}}
{{- $seen := dict -}}
{{- range $ctx.Values.mirrors -}}
{{- $alias := .source.alias | default "source" -}}
{{- if hasKey $seen $alias -}}
{{- fail (printf "mirror-maker2: two mirrors both use the source alias %q. Aliases name the replicated topic prefix, the offset-syncs topic and the checkpoints topic, so duplicates interleave two sources into one set of target topics without any error. Give each source its own alias." $alias) -}}
{{- end -}}
{{- $_ := set $seen $alias true -}}
{{- end -}}

{{- /* ── Identity fan-in must be provably disjoint ───────────────────────
       Under IdentityReplicationPolicy replicated topics keep their source
       names, so two sources whose patterns can match one topic write to the
       SAME target topic — interleaved records, one offset space, no error.
       Regex disjointness is not decidable here; what IS decidable is the two
       cases that certainly overlap: identical patterns, and `.*`. Anything
       subtler is the author's assertion, made with allowIdentityFanIn. */ -}}
{{- if and (include "mirror-maker2.isIdentityPolicy" $ctx) (gt (len $ctx.Values.mirrors) 1) -}}
{{- if not (($ctx.Values.replicationPolicy).allowIdentityFanIn) -}}
{{- $pats := dict -}}
{{- range $ctx.Values.mirrors -}}
{{- $alias := .source.alias | default "source" -}}
{{- $pat := include "mirror-maker2.pattern" (dict "list" .topics "pattern" .topicsPattern "default" ".*") -}}
{{- if eq $pat ".*" -}}
{{- fail (printf "mirror-maker2: mirror %q matches every topic (topicsPattern .*) and the replication policy is identity, so its topics land on the target under their original names — colliding with every other source. Narrow the pattern (topics: [...] or topicsPattern), use the default policy, or set replicationPolicy.allowIdentityFanIn=true if the sources really are disjoint." $alias) -}}
{{- end -}}
{{- if hasKey $pats $pat -}}
{{- fail (printf "mirror-maker2: mirrors %q and %q select the same topics (%s) under an identity policy, so both would write the same target topic names. Give them disjoint patterns, use the default policy, or set replicationPolicy.allowIdentityFanIn=true." (get $pats $pat) $alias $pat) -}}
{{- end -}}
{{- $_ := set $pats $pat $alias -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* ── The two connectors must agree on the offset-syncs location ──────
       Kafka reads `offset-syncs.topic.location` on BOTH the source and the
       checkpoint connector; a mismatch means the checkpoint connector reads
       a topic the source connector never writes, and translation silently
       produces nothing. The chart derives the value, so the only way to
       disagree is to hand-set one. */ -}}
{{- range $ctx.Values.mirrors -}}
{{- $alias := .source.alias | default "source" -}}
{{- $want := include "mirror-maker2.offsetSyncsLocation" (dict "m" . "ctx" $ctx) -}}
{{- if not (has $want (list "source" "target")) -}}
{{- fail (printf "mirror-maker2: mirror %q sets offsetSyncs.location=%q; Kafka accepts only \"source\" or \"target\"." $alias $want) -}}
{{- end -}}
{{- range $which, $conn := (dict "sourceConnector" .sourceConnector "checkpointConnector" .checkpointConnector) -}}
{{- with (($conn).config) -}}
{{- with (index . "offset-syncs.topic.location") -}}
{{- if ne . $want -}}
{{- fail (printf "mirror-maker2: mirror %q sets %s.config.offset-syncs.topic.location=%q, but the mirror resolves to %q. Both connectors must agree, so set it once with offsetSyncs.location (or readOnlySource: true) and let the chart put it on both." $alias $which . $want) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* ── Exactly-once has to be coherent ─────────────────────────────────
       Connect's exactly-once source support needs the source consumer to
       read only committed records. The chart sets that; an explicit
       contradiction in values would produce duplicates while the release
       claimed EOS. */ -}}
{{- if (($ctx.Values.target).exactlyOnce).enabled -}}
{{- range $ctx.Values.mirrors -}}
{{- $alias := .source.alias | default "source" -}}
{{- with (.source.config) -}}
{{- with (index . "consumer.isolation.level") -}}
{{- if ne . "read_committed" -}}
{{- fail (printf "mirror-maker2: target.exactlyOnce.enabled is true but mirror %q sets source.config.consumer.isolation.level=%q. Exactly-once requires the source consumer to read only committed records; remove the override or turn exactly-once off." $alias .) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* ── An autoscaler cannot create work ────────────────────────────────
       Connect distributes at most sum(tasksMax) tasks. Scaling past that
       adds workers with nothing to do — and the CPU average that triggered
       the scale-up falls, which the autoscaler reads as success. */ -}}
{{- if and ($ctx.Values.autoscaling).enabled (not ($ctx.Values.autoscaling).allowIdleWorkers) -}}
{{- $tasks := int (include "mirror-maker2.totalTasks" $ctx) -}}
{{- $max := int (($ctx.Values.autoscaling).maxReplicas | default 0) -}}
{{- if gt $max $tasks -}}
{{- fail (printf "mirror-maker2: autoscaling.maxReplicas is %d but this release has only %d connector tasks (sum of tasksMax across %d mirror(s)). Connect never runs more tasks than that, so workers beyond %d would idle while the CPU average that scaled them up falls. Raise tasksMax (its own ceiling is the source's partition count), lower maxReplicas, or set autoscaling.allowIdleWorkers=true." $max $tasks (len $ctx.Values.mirrors) $tasks) -}}
{{- end -}}
{{- end -}}

{{- /* ── A source must SAY where it is ──────────────────────────────────
       `mirror-maker2.bootstrap` defaults an unset clusterName to krafter/kafka
       so the shipped loopback renders out of the box. That default is a trap
       for any overlay whose source is meant to be filled in — values-failback
       is exactly one — because the render succeeds and produces a mirror
       pointed at the target itself. Declaring neither field is never
       intentional, so it is refused rather than guessed. */ -}}
{{- range $ctx.Values.mirrors -}}
{{- $alias := .source.alias | default "source" -}}
{{- if and (not .source.clusterName) (not .source.bootstrapServers) -}}
{{- fail (printf "mirror-maker2: mirror %q declares neither source.clusterName nor source.bootstrapServers, so the chart would fall back to krafter/kafka — which is the target. Name the source cluster (--set mirrors[0].source.clusterName=...) or its bootstrap address." $alias) -}}
{{- end -}}
{{- end -}}

{{- /* ── A loopback under identity eats its own output ──────────────────
       The shipped default IS a loopback (source bootstrap == target
       bootstrap) because that is the smoke-test shape, and under the DEFAULT
       policy it is safe: `orders` becomes `source.orders`, which the computed
       topicsExcludePattern then excludes. Under IDENTITY the replicated topic
       keeps its name, so `orders` is mirrored to `orders` on the same
       cluster — the connector re-reads what it just wrote, forever, and
       nothing in MirrorMaker's own exclusions covers a plain data topic. */ -}}
{{- if include "mirror-maker2.isIdentityPolicy" $ctx -}}
{{- $targetBootstrap := include "mirror-maker2.bootstrap" (dict "c" $ctx.Values.target "ctx" $ctx) -}}
{{- range $ctx.Values.mirrors -}}
{{- $alias := .source.alias | default "source" -}}
{{- if eq (include "mirror-maker2.bootstrap" (dict "c" .source "ctx" $ctx)) $targetBootstrap -}}
{{- fail (printf "mirror-maker2: mirror %q reads from %s, which is also the target — and the replication policy is identity, so every topic is mirrored onto itself and the connector re-reads its own output, forever. A loopback is only safe under the default policy, where the replicated topic is renamed. Point the source at another cluster, or use replicationPolicy.mode=default." $alias $targetBootstrap) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* ── Do not mirror in both directions under identity ─────────────────
       A reverse mirror re-reads what the forward one wrote, under the same
       topic names: an infinite loop. `lookup` returns nothing during a dry
       render or without cluster access, so this catches the live case and
       stays silent otherwise. */ -}}
{{- if and (include "mirror-maker2.isIdentityPolicy" $ctx) (not (($ctx.Values.replicationPolicy).allowBidirectional)) -}}
{{- $ns := include "mirror-maker2.namespace" $ctx -}}
{{- $selfName := include "mirror-maker2.fullname" $ctx -}}
{{- $targetAlias := $ctx.Values.target.alias | default "target" -}}
{{- range $existing := ((lookup "kafka.strimzi.io/v1" "KafkaMirrorMaker2" $ns "").items | default list) -}}
{{- if ne $existing.metadata.name $selfName -}}
{{- range (($existing.spec).mirrors | default list) -}}
{{- if eq ((.source).alias | default "") $targetAlias -}}
{{- fail (printf "mirror-maker2: %s/%s already mirrors FROM the alias %q, which is this release's target, and the replication policy is identity — the two would re-read each other's output under the same topic names, forever. Run the reverse direction only after stopping the forward one (values-failback.yaml), or set replicationPolicy.allowBidirectional=true if the patterns really are disjoint." $ns $existing.metadata.name $targetAlias) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
The tls + authentication blocks for a cluster entry (target or a mirror
source), via kafka-common: scram-*, plain, tls and custom (OAuth and other
SASL mechanisms). Call with (dict "c" <clusterValues> "field" <values path>).
*/}}
{{- define "mirror-maker2.clusterTlsAuth" -}}
{{- include "kafka-common.clientTlsAuth" (dict "c" .c "chart" "mirror-maker2" "field" (.field | default "cluster")) }}
{{- end }}

{{/*
Where MirrorMaker keeps its offset-syncs topic, per mirror.

WHY THIS IS A FIRST-CLASS SETTING: MirrorMaker writes
`mm2-offset-syncs.<target-alias>.internal` to the SOURCE cluster by default,
so mirroring a cluster you may only READ fails — late, inside the connector,
as a TopicAuthorizationException. KIP-716 (Kafka 3.0) added
`offset-syncs.topic.location` for exactly that case. Kafka requires the
source and checkpoint connectors to agree on it, which mirror-maker2.validate
enforces rather than leaving to a runtime mismatch.

Call with (dict "m" <mirror> "ctx" $). Precedence: the mirror's
readOnlySource shorthand, then its own offsetSyncs.location, then the
chart-level default.
*/}}
{{- define "mirror-maker2.offsetSyncsLocation" -}}
{{- $m := .m -}}{{- $ctx := .ctx -}}
{{- if $m.readOnlySource -}}
target
{{- else if ($m.offsetSyncs).location -}}
{{- ($m.offsetSyncs).location -}}
{{- else -}}
{{- (($ctx.Values.offsetSyncs).location | default "source") -}}
{{- end -}}
{{- end }}

{{/*
True when ANY mirror keeps its offset-syncs topic on the target — the
condition under which the target principal needs the mm2-offset-syncs ACL.
With the default (source) that grant is inert: the topic is not on this
cluster, and the SOURCE principal needs Create/Write instead.
*/}}
{{- define "mirror-maker2.anyOffsetSyncsOnTarget" -}}
{{- $ctx := . -}}
{{- range $ctx.Values.mirrors -}}
{{- if eq (include "mirror-maker2.offsetSyncsLocation" (dict "m" . "ctx" $ctx)) "target" -}}yes{{- end -}}
{{- end -}}
{{- end }}

{{/*
A topic/group pattern from either a list or a regex. Lists are the ergonomic
form — hand-escaping a Java regex inside YAML is where these releases go
wrong — and are compiled to an anchored alternation with the metacharacters
quoted. An explicit pattern always wins.

Call with (dict "list" <[]string> "pattern" <string> "default" <string>).
*/}}
{{- define "mirror-maker2.pattern" -}}
{{- if .pattern -}}
{{- .pattern -}}
{{- else if .list -}}
{{- $parts := list -}}
{{- range .list -}}
{{- $parts = append $parts (regexQuoteMeta .) -}}
{{- end -}}
{{- join "|" $parts -}}
{{- else -}}
{{- .default -}}
{{- end -}}
{{- end }}

{{/*
The total number of connector tasks this release can distribute:
sum(tasksMax) over every mirror's two connectors.

Connect hands out at most this many tasks across the workers, so a replica
count above it buys idle pods — and the CPU average that triggered the
scale-up drops, which reads as success. NOTES prints it; validate refuses an
autoscaler that would exceed it.
*/}}
{{- define "mirror-maker2.totalTasks" -}}
{{- $total := 0 -}}
{{- range .Values.mirrors -}}
{{- $total = add $total (int ((.sourceConnector).tasksMax | default 1)) -}}
{{- $total = add $total (int ((.checkpointConnector).tasksMax | default 1)) -}}
{{- end -}}
{{- $total -}}
{{- end }}

{{/*
The config additions a mirror's SOURCE cluster entry needs beyond the user's
own: read_committed when exactly-once is on (Connect's exactly-once source
support requires the source consumer to read only committed records), and the
rack the workers fetch from when rack awareness is configured.

Call with (dict "m" <mirror> "ctx" $). User keys win.
*/}}
{{- define "mirror-maker2.sourceConfig" -}}
{{- $m := .m -}}{{- $ctx := .ctx -}}
{{- $user := deepCopy ($m.source.config | default dict) -}}
{{- $derived := dict -}}
{{- if (($ctx.Values.target).exactlyOnce).enabled -}}
{{- $_ := set $derived "consumer.isolation.level" "read_committed" -}}
{{- end -}}
{{- if ($ctx.Values.rack).clientRack -}}
{{- $_ := set $derived "consumer.client.rack" ($ctx.Values.rack).clientRack -}}
{{- end -}}
{{- $merged := merge $user $derived -}}
{{- if $merged -}}
{{- toYaml $merged -}}
{{- end -}}
{{- end }}

{{/*
The Connect worker config for the target, with exactly-once merged in.
`exactly.once.source.support` is a WORKER property (spec.target.config), not
a connector one — the connector half is the isolation level above.
*/}}
{{- define "mirror-maker2.targetConfig" -}}
{{- $ctx := . -}}
{{- $user := deepCopy ($ctx.Values.target.config | default dict) -}}
{{- $derived := dict -}}
{{- if (($ctx.Values.target).exactlyOnce).enabled -}}
{{- $_ := set $derived "exactly.once.source.support" "enabled" -}}
{{- end -}}
{{- $merged := merge $user $derived -}}
{{- if $merged -}}
{{- toYaml $merged -}}
{{- end -}}
{{- end }}

{{/*
Image pull policy for the pods THIS CHART owns: the preflight Job, the
secret-sync Job and CronJob, and the Helm test pods. Deliberately not the MM2
workers — their pull policy is an operator-wide setting
(STRIMZI_IMAGE_PULL_POLICY), and a value here that silently did nothing to the
workers would be worse than none.
*/}}
{{- define "mirror-maker2.imagePullPolicy" -}}
{{- include "kafka-common.imagePullPolicy" .Values.imagePullPolicy }}
{{- end }}
