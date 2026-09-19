{{/*
Every value the templates read that is derived, translated from 1.x, or
merged from defaults — resolved once, returned as JSON:

  {{- $r := include "connect-cluster.resolve" . | fromJson }}

1.x keys still read in 2.x (each adds a line to `deprecations`, printed by
NOTES). A 1.x key that is SET wins over its 2.0 equivalent, as the 1.x
chart's own flat networkPolicy keys already did — that is what an existing
`--set podMonitors.enabled=false` or a values file written for 1.x expects.
*/}}
{{- define "connect-cluster.resolve" -}}
{{- $v := .Values -}}
{{- $fullname := include "connect-cluster.fullname" . -}}
{{- $ns := include "connect-cluster.namespace" . -}}
{{- $kafkaNs := include "connect-cluster.kafkaNamespace" . -}}
{{- $dep := list -}}
{{- $r := dict -}}

{{- /* ── monitoring ─────────────────────────────────────────────────── */ -}}
{{- $pm := deepCopy (($v.monitoring | default dict).podMonitor | default dict) -}}
{{- with $v.podMonitors -}}
{{- $dep = append $dep "podMonitors → monitoring.podMonitor" -}}
{{- if hasKey . "enabled" }}{{ $_ := set $pm "enabled" .enabled }}{{ end -}}
{{- with .labels }}{{ $_ := set $pm "labels" . }}{{ end -}}
{{- end -}}
{{- $legacyMon := omit ($v.monitoring | default dict) "podMonitor" -}}
{{- if $legacyMon -}}
{{- $dep = append $dep "monitoring.{interval,scrapeTimeout,honorLabels,metricRelabelings} → monitoring.podMonitor.* (monitoring.enabled was never read, in 1.x or here)" -}}
{{- range $k, $x := $legacyMon -}}
{{- if and (ne $k "enabled") (not (kindIs "invalid" $x)) }}{{ $_ := set $pm $k $x }}{{ end -}}
{{- end -}}
{{- end -}}
{{- $_ := set $pm "enabled" (eq (include "kafka-common.enabled" (list $pm.enabled true)) "true") -}}
{{- $_ := set $r "podMonitor" $pm -}}

{{- /* ── metrics ────────────────────────────────────────────────────── */ -}}
{{- $m := $v.metrics | default dict -}}
{{- $existing := $m.existingConfigMap | default dict -}}
{{- $cmName := $existing.name -}}
{{- $cmKey := $existing.key | default "metrics-config.yml" -}}
{{- with $v.metricsConfig -}}
{{- $dep = append $dep "metricsConfig → metrics.existingConfigMap" -}}
{{- if and (hasKey . "create") (not (eq (include "kafka-common.enabled" (list .create true)) "true")) -}}
{{- $cmName = .configMapName | default "kafka-metrics" -}}
{{- $cmKey = .configMapKey | default "kafka-metrics-config.yml" -}}
{{- end -}}
{{- end -}}
{{- $mEnabled := eq (include "kafka-common.enabled" (list $m.enabled true)) "true" -}}
{{- $mType := $m.type | default "jmxPrometheusExporter" -}}
{{- $_ := set $r "metrics" (dict
      "enabled" $mEnabled
      "type" $mType
      "create" (and $mEnabled (eq $mType "jmxPrometheusExporter") (not $cmName))
      "configMapName" ($cmName | default (printf "%s-metrics" $fullname))
      "configMapKey" $cmKey
      "exporter" (and $mEnabled (eq $mType "jmxPrometheusExporter"))) -}}

{{- /* ── alerts ─────────────────────────────────────────────────────── */ -}}
{{- /* The defaults are restated here because `thresholds:` written with
       nothing under it is null, and Helm drops a null key rather than
       merging the chart's map into it — which left `expr: … > ` in every
       rule that reads one, and a PrometheusRule the operator rejects. */ -}}
{{- $th := dict "heapUsagePercent" 85 "errorRatePerSecond" 1 "rebalanceRatePerSecond" 0.1 "sourceIdleMinutes" 15 "sinkLagRecords" 100000 -}}
{{- range $k, $x := (($v.alerts | default dict).thresholds | default dict) -}}
{{- if not (kindIs "invalid" $x) }}{{ $_ := set $th $k $x }}{{ end -}}
{{- end -}}
{{- if hasKey $th "sourceLagMinutes" -}}
{{- $dep = append $dep "alerts.thresholds.sourceLagMinutes → alerts.thresholds.sourceIdleMinutes" -}}
{{- $_ := set $th "sourceIdleMinutes" $th.sourceLagMinutes -}}
{{- end -}}
{{- $_ := set $r "thresholds" $th -}}

{{- /* ── pod template ───────────────────────────────────────────────── */ -}}
{{- $extra := dict -}}
{{- with $v.template -}}
{{- $dep = append $dep "template → templateExtra" -}}
{{- $extra = deepCopy . -}}
{{- end -}}
{{- $extra = mustMergeOverwrite $extra (deepCopy ($v.templateExtra | default dict)) -}}
{{- $_ := set $r "templateExtra" $extra -}}
{{- $psc := deepCopy ($v.podSecurityContext | default dict) -}}
{{- if kindIs "string" $psc.seccompProfile -}}
{{- $_ := set $psc "seccompProfile" (dict "type" $psc.seccompProfile) -}}
{{- end -}}
{{- $_ := set $r "podSecurityContext" $psc -}}

{{- /* ── secret sync ────────────────────────────────────────────────── */ -}}
{{- $sync := deepCopy ($v.secretSync | default dict) -}}
{{- with ($v.kafkaUser | default dict).secretSync -}}
{{- $dep = append $dep "kafkaUser.secretSync → secretSync" -}}
{{- range $k, $x := . }}{{ $_ := set $sync $k $x }}{{ end -}}
{{- end -}}
{{- $syncSecrets := list -}}
{{- $crossNs := ne $kafkaNs $ns -}}
{{- $syncOn := and $crossNs (eq (include "kafka-common.enabled" (list $sync.enabled true)) "true") -}}
{{- $auth := $v.kafka.authentication | default dict -}}
{{- $user := include "connect-cluster.kafkaUsername" . -}}
{{- if and $syncOn ($v.kafkaUser).create (eq ($sync.method | default "job") "job") -}}
{{- $syncSecrets = append $syncSecrets (dict "name" $user "fromNamespace" $kafkaNs) -}}
{{- end -}}
{{- if and $syncOn (eq (include "kafka-common.enabled" (list ($v.kafka.tls | default dict).enabled false)) "true") (not $v.kafka.bootstrapServers) (not ($v.kafka.tls).trustedCertificateSecret) -}}
{{- $syncSecrets = append $syncSecrets (dict "name" (printf "%s-cluster-ca-cert" ($v.kafka.clusterName | default "krafter")) "fromNamespace" $kafkaNs) -}}
{{- end -}}
{{- $syncSecrets = concat $syncSecrets ($sync.secrets | default list) -}}
{{- $_ := set $sync "resolvedSecrets" $syncSecrets -}}
{{- $_ := set $sync "reflector" (and $crossNs ($v.kafkaUser).create (eq (include "kafka-common.enabled" (list $sync.enabled true)) "true") (eq ($sync.method | default "job") "reflector")) -}}
{{- $_ := set $r "secretSync" $sync -}}

{{- /* ── connectors ─────────────────────────────────────────────────── */ -}}
{{- $defaults := deepCopy ($v.connectorDefaults | default dict) -}}
{{- if hasKey $v "autoRestart" -}}
{{- $dep = append $dep "autoRestart → connectorDefaults.autoRestart" -}}
{{- $_ := set $defaults "autoRestart" (mustMergeOverwrite (deepCopy ($defaults.autoRestart | default dict)) (deepCopy ($v.autoRestart | default dict))) -}}
{{- end -}}
{{- $items := list -}}
{{- if kindIs "slice" $v.connectors -}}
{{- if $v.connectors }}{{ $dep = append $dep "connectors as a list → a map keyed by name" }}{{ end -}}
{{- $items = $v.connectors -}}
{{- else -}}
{{- range $name, $c := ($v.connectors | default dict) -}}
{{- $items = append $items (merge (dict "name" $name) (deepCopy ($c | default dict))) -}}
{{- end -}}
{{- end -}}
{{- $classes := include "connect-cluster.connectorClasses" . | fromYaml -}}
{{- $connectors := list -}}
{{- range $c := $items -}}
{{- if eq (include "kafka-common.enabled" (list $c.enabled true)) "true" -}}
{{- $connectors = append $connectors (include "connect-cluster.resolveConnector" (dict "ctx" $ "c" $c "defaults" $defaults "classes" $classes) | fromJson) -}}
{{- end -}}
{{- end -}}
{{- $_ := set $r "connectors" $connectors -}}
{{- /* Test connectors are values templates, rendered here. */ -}}
{{- $tests := list -}}
{{- with $v.testConnectors -}}
{{- $rendered := tpl (toYaml .) $ | fromYamlArray -}}
{{- range $c := $rendered -}}
{{- $tests = append $tests (include "connect-cluster.resolveConnector" (dict "ctx" $ "c" $c "defaults" $defaults "classes" $classes "test" true) | fromJson) -}}
{{- end -}}
{{- end -}}
{{- $_ := set $r "testConnectors" $tests -}}

{{- /* ── secret references → RBAC ───────────────────────────────────── */ -}}
{{- $refs := dict -}}
{{- range $c := (concat $connectors $tests) -}}
{{- range $key, $val := $c.config -}}
{{- range $m := regexFindAll "\\$\\{secrets:[^}]+\\}" (toString $val) -1 -}}
{{- $path := regexReplaceAll "^\\$\\{secrets:([^:}]+)(:[^}]*)?\\}$" $m "${1}" -}}
{{- $parts := splitList "/" $path -}}
{{- if eq (len $parts) 2 -}}
{{- $_ := set $refs (index $parts 0) (append (get $refs (index $parts 0) | default list) (index $parts 1) | uniq | sortAlpha) -}}
{{- else -}}
{{- fail (printf "connect-cluster: connector %s references %s — the KubernetesSecretConfigProvider needs ${secrets:<namespace>/<secret>:<key>}" $c.name $m) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- range (($v.rbac | default dict).secretNames | default list) -}}
{{- $parts := splitList "/" . -}}
{{- $sns := ternary (index $parts 0) $ns (eq (len $parts) 2) -}}
{{- $sname := last $parts -}}
{{- $_ := set $refs $sns (append (get $refs $sns | default list) $sname | uniq | sortAlpha) -}}
{{- end -}}
{{- $_ := set $r "secretRefs" $refs -}}

{{- /* ── network policy ─────────────────────────────────────────────── */ -}}
{{- $np := $v.networkPolicy | default dict -}}
{{- $dbs := (($np.egress | default dict).databases | default list) -}}
{{- if hasKey $v "databaseEgress" -}}
{{- if $v.databaseEgress -}}
{{- $dep = append $dep "databaseEgress → networkPolicy.egress.databases" -}}
{{- $dbs = $v.databaseEgress -}}
{{- end -}}
{{- end -}}
{{- $_ := set $r "databases" $dbs -}}
{{- $explicitPorts := $np.kafkaPorts | default (($np.kafka | default dict).ports) -}}
{{- if $np.kafkaPorts }}{{ $dep = append $dep "networkPolicy.kafkaPorts → networkPolicy.kafka.ports" }}{{ end -}}
{{- if $np.monitoringNamespace }}{{ $dep = append $dep "networkPolicy.monitoringNamespace → networkPolicy.monitoring.namespace" }}{{ end -}}
{{- if $np.restApiClients }}{{ $dep = append $dep "networkPolicy.restApiClients → networkPolicy.restApi.clients" }}{{ end -}}
{{- $bootstrap := include "connect-cluster.bootstrap" . -}}
{{- $ports := list -}}
{{- if $explicitPorts -}}
{{- range $explicitPorts }}{{ $ports = append $ports (toString .) }}{{ end -}}
{{- else -}}
{{- $ports = splitList "," (include "kafka-common.bootstrapPorts" $bootstrap) -}}
{{- end -}}
{{- $_ := set $r "kafkaPorts" $ports -}}
{{- $_ := set $r "kafkaPortsExplicit" (not (empty $explicitPorts)) -}}
{{- $_ := set $r "bootstrap" $bootstrap -}}

{{- /* ── plugins ────────────────────────────────────────────────────── */ -}}
{{- $expected := $v.tests.expectedPlugins | default list -}}
{{- range ($v.plugins | default list) }}{{ $expected = concat $expected (.expect | default list) }}{{ end -}}
{{- $_ := set $r "expectedPlugins" ($expected | uniq) -}}

{{- $_ := set $r "deprecations" $dep -}}
{{- toJson $r -}}
{{- end }}

{{/*
One connector, normalised: defaults merged under it, the dead letter queue
expanded into config, and every rule a connector can be checked against at
render time applied. Returns JSON.
*/}}
{{- define "connect-cluster.resolveConnector" -}}
{{- $ctx := .ctx -}}
{{- $v := $ctx.Values -}}
{{- $c := .c -}}
{{- $d := .defaults -}}
{{- $where := printf "connectors.%s" ($c.name | default "?") -}}
{{- if .test }}{{ $where = printf "testConnectors[%s]" ($c.name | default "?") }}{{ end -}}
{{- if not $c.name -}}
{{- fail "connect-cluster: a connector has no name" -}}
{{- end -}}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$" $c.name) -}}
{{- fail (printf "connect-cluster: %s — the connector name %q is the KafkaConnector's name and must be a DNS-1123 subdomain (lower-case letters, digits, '-' and '.')" $where $c.name) -}}
{{- end -}}
{{- if gt (len $c.name) 253 -}}
{{- fail (printf "connect-cluster: %s — the connector name is longer than 253 characters" $where) -}}
{{- end -}}
{{- if not $c.class -}}
{{- fail (printf "connect-cluster: %s has no class — name the connector class (for example io.debezium.connector.postgresql.PostgresConnector)" $where) -}}
{{- end -}}
{{- $state := $c.state | default "" -}}
{{- if and $state (not (has $state (list "running" "paused" "stopped"))) -}}
{{- fail (printf "connect-cluster: %s.state is %q; it must be running, paused or stopped" $where $state) -}}
{{- end -}}
{{- $tasks := 1 -}}
{{- if hasKey $c "tasksMax" -}}
{{- if not (regexMatch "^[0-9]+$" (toString $c.tasksMax)) -}}
{{- fail (printf "connect-cluster: %s.tasksMax is %v; it must be a whole number of at least 1" $where $c.tasksMax) -}}
{{- end -}}
{{- $tasks = int $c.tasksMax -}}
{{- if lt $tasks 1 -}}
{{- fail (printf "connect-cluster: %s.tasksMax is %d; a connector needs at least one task" $where $tasks) -}}
{{- end -}}
{{- end -}}
{{- if and $c.version (semverCompare "<4.1.0-0" (include "kafka-common.semver" $v.version)) -}}
{{- fail (printf "connect-cluster: %s.version selects a plugin version, which Kafka Connect supports from 4.1; the workers run %s" $where $v.version) -}}
{{- end -}}
{{- $known := get .classes $c.class | default dict -}}
{{- $sink := $known.sink | default false -}}
{{- if hasKey $c "type" }}{{ $sink = eq $c.type "sink" }}{{ else if not $known }}{{ $sink = contains "Sink" $c.class }}{{ end -}}
{{- $cfg := mustMergeOverwrite (deepCopy ($d.config | default dict)) (deepCopy ($c.config | default dict)) -}}
{{- range ($known.required | default list) -}}
{{- if not (get $cfg .) -}}
{{- fail (printf "connect-cluster: %s (%s) needs config.%s" $where $c.class .) -}}
{{- end -}}
{{- end -}}
{{- if and $sink (not (or (get $cfg "topics") (get $cfg "topics.regex"))) -}}
{{- fail (printf "connect-cluster: %s is a sink connector and needs config.topics or config.topics.regex" $where) -}}
{{- end -}}
{{- if $v.productionMode -}}
{{- range $k, $x := $cfg -}}
{{- if and (regexMatch "(^|[._-])password$" $k) (not (hasPrefix "${" (toString $x))) -}}
{{- fail (printf "connect-cluster: %s.config.%s is a plaintext password, which productionMode refuses. Reference a Secret instead: ${secrets:<namespace>/<secret>:<key>}" $where $k) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- /* Dead letter queue: sink connectors only (Connect ignores the setting
       on a source, which would leave errors.tolerance=all dropping records
       with nowhere to put them). */ -}}
{{- $dlq := $c.deadLetterQueue | default dict -}}
{{- $dlqOut := dict -}}
{{- if eq (include "kafka-common.enabled" (list $dlq.enabled false)) "true" -}}
{{- if not $sink -}}
{{- fail (printf "connect-cluster: %s.deadLetterQueue is for sink connectors; %s is not one (set type: sink if it is). A source connector ignores the dead letter queue, and errors.tolerance=all would then drop failed records silently." $where $c.class) -}}
{{- end -}}
{{- $topic := $dlq.topic | default (printf "%s-dlq-%s" $v.groupId $c.name) -}}
{{- $rf := int ($dlq.replicationFactor | default $v.config.replicationFactor | default 3) -}}
{{- $_ := set $cfg "errors.tolerance" "all" -}}
{{- $_ := set $cfg "errors.log.enable" "true" -}}
{{- $_ := set $cfg "errors.deadletterqueue.topic.name" $topic -}}
{{- $_ := set $cfg "errors.deadletterqueue.topic.replication.factor" (toString $rf) -}}
{{- $_ := set $cfg "errors.deadletterqueue.context.headers.enable" (toString (eq (include "kafka-common.enabled" (list $dlq.contextHeaders true)) "true")) -}}
{{- $dlqOut = dict "topic" $topic "replicationFactor" $rf "partitions" (int ($dlq.partitions | default 1)) "createTopic" (eq (include "kafka-common.enabled" (list $dlq.createTopic true)) "true") -}}
{{- end -}}
{{- $ar := mustMergeOverwrite (deepCopy ($d.autoRestart | default dict)) (deepCopy ($c.autoRestart | default dict)) -}}
{{- $out := dict
      "name" $c.name
      "class" $c.class
      "tasksMax" $tasks
      "state" $state
      "version" ($c.version | default "")
      "sink" $sink
      "autoRestart" (dict "enabled" (eq (include "kafka-common.enabled" (list $ar.enabled true)) "true") "maxRestarts" (int ($ar.maxRestarts | default 10)))
      "config" $cfg
      "labels" ($c.labels | default dict)
      "annotations" ($c.annotations | default dict)
      "listOffsets" ($c.listOffsets | default dict)
      "alterOffsets" ($c.alterOffsets | default dict)
      "deadLetterQueue" $dlqOut -}}
{{- toJson $out -}}
{{- end }}
