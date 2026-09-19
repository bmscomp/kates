{{/*
Rails: settings that are knowably wrong from the values alone fail the render
and say what to do, instead of producing a Kafka that is NotReady an hour later
(refactor plan §4.10). Called by kafka-cluster.resolve with
(dict "ctx" $ "r" <resolved>).
*/}}
{{- define "kafka-cluster.rails" -}}
{{- $ctx := .ctx -}}
{{- $v := $ctx.Values -}}
{{- $r := .r -}}
{{- $prod := include "kafka-common.enabled" (list $v.productionMode false) -}}
{{- $cfg := $v.kafka.config | default dict -}}
{{- $brokers := int $r.brokers -}}

{{- /* Metadata version ≤ Kafka version */ -}}
{{- if $v.kafka.metadataVersion -}}
{{- $kafka := include "kafka-cluster.semver" $v.kafkaVersion -}}
{{- $meta := include "kafka-cluster.semver" $v.kafka.metadataVersion -}}
{{- $minor := printf "%s.%s" (index (splitList "." $kafka) 0) (index (splitList "." $kafka) 1) -}}
{{- if semverCompare (printf ">%s.0" $minor) $meta -}}
{{- fail (printf "kafka-cluster: kafka.metadataVersion %q is newer than kafkaVersion %q — a broker cannot run metadata from a release it does not have. Set kafka.metadataVersion to %s or older (the CLI derives one step behind the Kafka version)." (toString $v.kafka.metadataVersion) (toString $v.kafkaVersion) $minor) -}}
{{- end -}}
{{- end -}}

{{- /* Internal topics must fit the brokers */ -}}
{{- range $key := (list "offsets.topic.replication.factor" "transaction.state.log.replication.factor" "default.replication.factor") -}}
{{- if hasKey $cfg $key -}}
{{- if gt (int (index $cfg $key)) $brokers -}}
{{- fail (printf "kafka-cluster: kafka.config %s is %v, but the node pools render %d broker(s). Kafka cannot create its internal topics (consumer groups and transactions stop working). Lower it or add brokers." $key (index $cfg $key) $brokers) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if and (hasKey $cfg "min.insync.replicas") (hasKey $cfg "default.replication.factor") -}}
{{- $isr := int (index $cfg "min.insync.replicas") -}}
{{- $rf := int (index $cfg "default.replication.factor") -}}
{{- if gt $isr $rf -}}
{{- fail (printf "kafka-cluster: kafka.config min.insync.replicas (%d) is above default.replication.factor (%d): no write with acks=all can succeed." $isr $rf) -}}
{{- end -}}
{{- if and (eq $isr $rf) (gt $rf 1) -}}
{{- fail (printf "kafka-cluster: kafka.config min.insync.replicas equals default.replication.factor (%d): every rolling restart stops writes with acks=all. Use %d." $rf (sub $rf 1)) -}}
{{- end -}}
{{- end -}}
{{- if and (hasKey $cfg "transaction.state.log.min.isr") (hasKey $cfg "transaction.state.log.replication.factor") -}}
{{- if gt (int (index $cfg "transaction.state.log.min.isr")) (int (index $cfg "transaction.state.log.replication.factor")) -}}
{{- fail "kafka-cluster: kafka.config transaction.state.log.min.isr is above transaction.state.log.replication.factor: transactions could never commit." -}}
{{- end -}}
{{- end -}}

{{- /* Tiered storage */ -}}
{{- if $r.tiered -}}
{{- $ts := $v.tieredStorage -}}
{{- $rsm := $ts.remoteStorageManager | default dict -}}
{{- if not $ts.image -}}
{{- fail "kafka-cluster: tieredStorage.enabled needs tieredStorage.image — a Kafka image that carries a remote storage manager plugin. Strimzi's stock image has none, so the brokers would fail to start (docs/kafka-cluster-1.0-upgrade.md, \"Tiered storage\")." -}}
{{- end -}}
{{- if hasPrefix "quay.io/strimzi/kafka:" $ts.image -}}
{{- fail (printf "kafka-cluster: tieredStorage.image is %q, Strimzi's stock Kafka image, which carries no remote storage manager plugin. Build an image with one (e.g. the Aiven tiered-storage plugin) and name it here." $ts.image) -}}
{{- end -}}
{{- if or (not $rsm.className) (not $rsm.classPath) -}}
{{- fail "kafka-cluster: tieredStorage.enabled needs remoteStorageManager.className and remoteStorageManager.classPath (the plugin's class and where the image keeps its jars)." -}}
{{- end -}}
{{- if and (not (($ts.credentials).existingSecret)) (not $v.seaweedfs.enabled) -}}
{{- fail "kafka-cluster: tieredStorage.enabled needs credentials: set tieredStorage.credentials.existingSecret, or enable seaweedfs (its credentials Secret is used)." -}}
{{- end -}}
{{- end -}}

{{- /* SeaweedFS */ -}}
{{- if $v.seaweedfs.enabled -}}
{{- $s3 := $v.seaweedfs.s3 | default dict -}}
{{- if and (not $s3.existingSecret) (eq ($s3.secretAccessKey | default "") "change-me-in-prod") -}}
{{- if (($v.seaweedfs.filer).s3).enableAuth -}}
{{- fail "kafka-cluster: seaweedfs.filer.s3.enableAuth is true but seaweedfs.s3.secretAccessKey is the placeholder. Set seaweedfs.s3.existingSecret (recommended) or real credentials." -}}
{{- end -}}
{{- if $prod -}}
{{- fail "kafka-cluster: productionMode with seaweedfs enabled and the placeholder seaweedfs.s3.secretAccessKey. Set seaweedfs.s3.existingSecret." -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* Backup must protect broker data somehow in production */ -}}
{{- if and $prod $v.backup.enabled (eq $r.backupVolumes "none") (not $r.tiered) -}}
{{- fail "kafka-cluster: productionMode with backup.volumes=none and no tiered storage: nothing protects broker data beyond replication. Set backup.volumes to fs-backup or snapshot, or enable tieredStorage." -}}
{{- end -}}

{{- /* Cruise Control */ -}}
{{- $cc := include "kafka-common.enabled" (list $v.cruiseControl.enabled true) -}}
{{- if and (not $cc) (include "kafka-common.enabled" (list ((($v.rebalance).full).enabled) false)) -}}
{{- fail "kafka-cluster: rebalance.full.enabled needs cruiseControl.enabled — a KafkaRebalance is a request to Cruise Control, and without it the CR stays NotReady." -}}
{{- end -}}
{{- if $cc -}}
{{- range ($v.cruiseControl.autoRebalance | default list) -}}
{{- if not (has .mode (list "add-brokers" "remove-brokers")) -}}
{{- fail (printf "kafka-cluster: cruiseControl.autoRebalance mode %q; Strimzi supports add-brokers and remove-brokers." .mode) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* Observability */ -}}
{{- if and (include "kafka-common.enabled" (list $v.alerts.enabled true)) (not $r.metrics.enabled) -}}
{{- fail "kafka-cluster: alerts.enabled needs metrics.enabled — without spec.kafka.metricsConfig the brokers expose no metrics and every rule would evaluate against nothing." -}}
{{- end -}}
{{- if not (has $r.metrics.type (list "jmxPrometheusExporter" "strimziMetricsReporter")) -}}
{{- fail (printf "kafka-cluster: metrics.type is %q; use jmxPrometheusExporter or strimziMetricsReporter." $r.metrics.type) -}}
{{- end -}}
{{- if and (include "kafka-common.enabled" (list $v.alerts.enabled true)) (eq $r.metrics.type "strimziMetricsReporter") (not $v.alerts.allowReporterMetrics) -}}
{{- fail "kafka-cluster: alerts.enabled with metrics.type=strimziMetricsReporter: the rules are written against the JMX exporter's series names (files/metrics), which the reporter does not produce, so they would never fire. Keep the exporter, or port the rules and set alerts.allowReporterMetrics=true." -}}
{{- end -}}

{{- /* Floating tags */ -}}
{{- include "kafka-common.rails.noFloatingTag" (dict "chart" "kafka-cluster" "images" (dict "kafka.image" $v.kafka.image "tieredStorage.image" (ternary $v.tieredStorage.image "" $r.tiered) "testImages.kafka" $v.testImages.kafka "testImages.kubectl" $v.testImages.kubectl)) -}}

{{- /* Production */ -}}
{{- if $prod -}}
{{- range $r.listeners -}}
{{- if and (eq .type "nodeport") (not .tls) -}}
{{- fail (printf "kafka-cluster: productionMode with listener %q: a NodePort without TLS exposes credentials and data on every node's address. Set tls: true." .name) -}}
{{- end -}}
{{- end -}}
{{- range $r.pools -}}
{{- $pool := . -}}
{{- /* Both storage shapes: a pool that is not JBOD has its settings on
       `storage` itself rather than under `volumes`. */ -}}
{{- range (($pool.storage).volumes | default (list $pool.storage)) -}}
{{- if .deleteClaim -}}
{{- fail (printf "kafka-cluster: productionMode with deleteClaim: true on node pool %q (volume %v) — scaling the pool down or deleting the cluster would delete its data." $pool.name (.id | default "-")) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if not (include "kafka-common.enabled" (list $r.netpol.enabled true)) -}}
{{- fail "kafka-cluster: productionMode with networkPolicy.enabled=false: every pod in the cluster could reach the brokers." -}}
{{- end -}}
{{- end -}}

{{- /* 0.4 named its client namespaces and hardcoded the clients themselves;
       1.0 keeps the clients in the platform profile. A release that carries
       the namespace keys and no matching client would render a default-deny
       with nothing admitted through it — Connect and MirrorMaker 2 dropped
       at the network layer while both CRs stay Ready. */ -}}
{{- if $r.netpol.enabled -}}
{{- $declared := list -}}
{{- range $r.netpol.resolvedClients -}}{{- $declared = append $declared .name -}}{{- end -}}
{{- range $name, $ns := ($r.netpol.nsOverrides | default dict) -}}
{{- if not (has $name $declared) -}}
{{- fail (printf "kafka-cluster: networkPolicies.%sNamespace names %q, but no networkPolicy.clients entry is called %q, so nothing is admitted for it. 0.4 hardcoded these clients; 1.0 keeps them in the platform profile — add -f values-platform.yaml (or --set profile=platform), or declare the client yourself:\n  networkPolicy:\n    clients:\n      - name: %s\n        namespace: %s\n        podSelector: { app.kubernetes.io/name: %s }" (regexReplaceAll "-([a-z])" $name "$1" | camelcase | untitle) $ns $name $name $ns $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end }}
