{{/*
kafka-cluster helpers. Names and labels come from the kafka-common library;
everything a template renders from values goes through
"kafka-cluster.resolve" (_resolve.tpl), which applies the profile and the 0.4
compatibility shim once and hands every template the same answer.
*/}}

{{- define "kafka-cluster.name" -}}
{{- include "kafka-common.name" . }}
{{- end }}

{{/* The Kafka CR name; the release name when clusterName is empty. */}}
{{- define "kafka-cluster.clusterName" -}}
{{- .Values.clusterName | default .Release.Name }}
{{- end }}

{{- define "kafka-cluster.namespace" -}}
{{- include "kafka-common.namespace" . }}
{{- end }}

{{- define "kafka-cluster.clusterDomain" -}}
{{- include "kafka-common.clusterDomain" . }}
{{- end }}

{{/*
Labels on every resource. strimzi.io/cluster is one of them, so a KafkaTopic or
KafkaUser needs nothing more to bind to the cluster. Built as a map by the
library: a key cannot appear twice.
*/}}
{{- define "kafka-cluster.labels" -}}
{{- include "kafka-common.labels" (dict "ctx" . "partOf" (include "kafka-cluster.name" .) "extra" (dict "strimzi.io/cluster" (include "kafka-cluster.clusterName" .))) }}
{{- end }}

{{/* The same labels plus app.kubernetes.io/component. */}}
{{- define "kafka-cluster.componentLabels" -}}
{{- include "kafka-common.labels" (dict "ctx" .ctx "component" .component "partOf" (include "kafka-cluster.name" .ctx) "extra" (dict "strimzi.io/cluster" (include "kafka-cluster.clusterName" .ctx))) }}
{{- end }}

{{/* Labels without strimzi.io/cluster, for templates that add it themselves. */}}
{{- define "kafka-cluster.baseLabels" -}}
{{- include "kafka-common.labels" (dict "ctx" . "partOf" (include "kafka-cluster.name" .)) }}
{{- end }}

{{/*
The name of a namespaced resource: <clusterName>-<suffix>, so two clusters can
share a namespace. Resources that existed in 0.4 keep their old name under
compatibility.legacyResourceNames. Call with (list $ "<suffix>" "<0.4 name>");
an empty 0.4 name means the resource is new and always prefixed.
*/}}
{{- define "kafka-cluster.resourceName" -}}
{{- $ctx := index . 0 -}}
{{- $legacy := index . 2 -}}
{{- if and $legacy (include "kafka-common.enabled" (list (($ctx.Values.compatibility).legacyResourceNames) false)) -}}
{{- $legacy -}}
{{- else -}}
{{- printf "%s-%s" (include "kafka-cluster.clusterName" $ctx) (index . 1) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{- define "kafka-cluster.metricsConfigMap" -}}
{{- include "kafka-cluster.resourceName" (list . "kafka-metrics" "kafka-metrics") }}
{{- end }}

{{- define "kafka-cluster.ccMetricsConfigMap" -}}
{{- include "kafka-cluster.resourceName" (list . "cruise-control-metrics" "") }}
{{- end }}

{{/* Kafka's version as x.y.z. */}}
{{- define "kafka-cluster.semver" -}}
{{- include "kafka-common.semver" . }}
{{- end }}

{{/*
Deep merge that treats false, 0 and "" as values. Sprig's mergeOverwrite skips
"empty" source values, so `spread.enabled: false` could never override a
default of true. Maps are merged key by key, lists and scalars replace, and a
null in the source leaves the destination alone. Mutates .dst; call with
(dict "dst" <map> "src" <map>) and ignore the output.
*/}}
{{- define "kafka-cluster.merge" -}}
{{- $dst := .dst -}}
{{- range $k, $v := (.src | default dict) -}}
{{- if kindIs "invalid" $v -}}
{{- else if and (kindIs "map" $v) (kindIs "map" (index $dst $k)) -}}
{{- include "kafka-cluster.merge" (dict "dst" (index $dst $k) "src" $v) -}}
{{- else if or (kindIs "map" $v) (kindIs "slice" $v) -}}
{{- $_ := set $dst $k (deepCopy $v) -}}
{{- else -}}
{{- $_ := set $dst $k $v -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
A list of {name: …} entries, or a map keyed by name, as a map keyed by name.
Call with (dict "items" <list|map> "field" "<values path>").
*/}}
{{- define "kafka-cluster.byName" -}}
{{- $out := dict -}}
{{- if kindIs "slice" .items -}}
{{- range .items -}}
{{- if not .name -}}{{- fail (printf "kafka-cluster: every entry of %s needs a name" $.field) -}}{{- end -}}
{{- $item := omit . "name" -}}
{{- $_ := set $out .name $item -}}
{{- end -}}
{{- else if kindIs "map" .items -}}
{{- $out = deepCopy .items -}}
{{- end -}}
{{- toJson $out -}}
{{- end }}

{{/*
The broker configuration: kafka.config minus the share-group keys (group.share.*,
share.*) below Kafka 4.2.0, plus the tiered-storage keys when tiered storage is
active. Call with (dict "ctx" $ "r" <resolved>). YAML at column 0.
*/}}
{{- define "kafka-cluster.kafkaConfig" -}}
{{- $v := .ctx.Values -}}
{{- $config := dict -}}
{{- $old := semverCompare "<4.2.0" (include "kafka-cluster.semver" $v.kafkaVersion) -}}
{{- range $key, $value := ($v.kafka.config | default dict) -}}
{{- if not (and $old (or (hasPrefix "group.share." $key) (hasPrefix "share." $key))) -}}
{{- $_ := set $config $key $value -}}
{{- end -}}
{{- end -}}
{{- if .r.tiered -}}
{{- $ts := $v.tieredStorage -}}
{{- range $key, $value := .r.legacyTieredKafkaConfig -}}
{{- if not (hasKey $config $key) -}}{{- $_ := set $config $key $value -}}{{- end -}}
{{- end -}}
{{- $_ := set $config "rlmm.config.remote.log.metadata.topic.replication.factor" (int ((($ts.metadata).replicationFactor) | default 3)) -}}
{{- if not (hasKey $config "log.local.retention.ms") -}}
{{- $_ := set $config "log.local.retention.ms" (int64 ($ts.localRetentionMs | default 86400000)) -}}
{{- end -}}
{{- end -}}
{{- range $key, $value := $config }}
{{ $key }}: {{ $value | toJson }}
{{- end }}
{{- end }}

{{/* Hardened security contexts, merged with the caller's overrides. */}}
{{- define "kafka-cluster.podSecurityContext" -}}
runAsNonRoot: true
runAsUser: 1001
fsGroup: 1001
seccompProfile:
  type: RuntimeDefault
{{- end }}

{{- define "kafka-cluster.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
{{- end }}

{{/* Test images. */}}
{{- define "kafka-cluster.kafkaImage" -}}
{{- if .Values.testImages.kafka -}}
{{- .Values.testImages.kafka -}}
{{- else -}}
{{- printf "%s/%s/kafka:%s-kafka-%s" .Values.global.imageRegistry .Values.global.imageRepository .Values.strimziVersion .Values.kafkaVersion -}}
{{- end -}}
{{- end }}

{{- define "kafka-cluster.kubectlImage" -}}
{{- .Values.testImages.kubectl -}}
{{- end }}

{{/* <service>.<namespace>.svc.<domain>; call with (dict "service" "namespace" "root"). */}}
{{- define "kafka-cluster.serviceFQDN" -}}
{{- printf "%s.%s.svc.%s" .service .namespace (include "kafka-cluster.clusterDomain" .root) -}}
{{- end }}

{{/* Bootstrap address of the first internal listener without TLS (9092 by default). */}}
{{- define "kafka-cluster.bootstrapServers" -}}
{{- $port := 9092 -}}
{{- range .Values.kafka.listeners }}{{- if and (eq .type "internal") (not .tls) }}{{- $port = .port }}{{- break }}{{- end }}{{- end -}}
{{- printf "%s-kafka-bootstrap.%s.svc.%s:%v" (include "kafka-cluster.clusterName" .) (include "kafka-cluster.namespace" .) (include "kafka-cluster.clusterDomain" .) $port -}}
{{- end }}

{{/* The SeaweedFS S3 endpoint, from the subchart's own Service naming. */}}
{{- define "kafka-cluster.objectStoreEndpoint" -}}
{{- $sw := .Values.seaweedfs -}}
{{- $name := $sw.nameOverride | default "seaweedfs" -}}
{{- if (($sw.filer).s3).enabled -}}
{{- printf "http://%s:%v" (include "kafka-cluster.serviceFQDN" (dict "service" (printf "%s-filer" $name) "namespace" (include "kafka-cluster.namespace" .) "root" .)) ($sw.filer.s3.port | default 8333) -}}
{{- else -}}
{{- printf "http://%s:%v" (include "kafka-cluster.serviceFQDN" (dict "service" (printf "%s-s3" $name) "namespace" (include "kafka-cluster.namespace" .) "root" .)) (($sw.s3).port | default 8333) -}}
{{- end -}}
{{- end }}

{{/* The Secret holding the SeaweedFS S3 credentials. */}}
{{- define "kafka-cluster.objectStoreSecret" -}}
{{- .Values.seaweedfs.s3.existingSecret | default (include "kafka-cluster.resourceName" (list . "seaweedfs-credentials" "kafka-seaweedfs-credentials")) -}}
{{- end }}

{{/*
The principal the Helm tests authenticate as: the chart's test user, or with
tests.user.create=false the first managed user. Call with the root context.
*/}}
{{- define "kafka-cluster.testUser" -}}
{{- if include "kafka-common.enabled" (list .Values.tests.user.create true) -}}
{{- include "kafka-cluster.resourceName" (list . "helm-test" "") -}}
{{- else -}}
{{- $r := dict -}}
{{- $_ := include "kafka-cluster.resolve" (dict "ctx" . "out" $r) -}}
{{- with $r.users }}{{ (index . 0).name }}{{ end -}}
{{- end -}}
{{- end }}

{{/* A list of ports as NetworkPolicy port entries (TCP), duplicates dropped. YAML list. */}}
{{- define "kafka-cluster.tcpPorts" -}}
{{- $out := list -}}
{{- $seen := list -}}
{{- range . }}{{- if not (has (int .) $seen) }}{{- $seen = append $seen (int .) }}{{- $out = append $out (dict "port" (int .) "protocol" "TCP") }}{{- end }}{{- end -}}
{{- toYaml $out -}}
{{- end }}

{{/* The Kyverno ClusterPolicy's name (cluster-scoped: carries namespace and cluster). */}}
{{- define "kafka-cluster.kyvernoPolicyName" -}}
{{- if include "kafka-common.enabled" (list ((.Values.compatibility).legacyResourceNames) false) -}}
kafka-pod-security-standards
{{- else -}}
{{- printf "kafka-pod-security-%s-%s" (include "kafka-cluster.namespace" .) (include "kafka-cluster.clusterName" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{/*
A scalar as a string, integers without an exponent: values files give Helm
float64 numbers, and toString renders 4194304 as "4.194304e+06".
*/}}
{{- define "kafka-cluster.scalar" -}}
{{- if and (kindIs "float64" .) (eq . (float64 (int64 .))) -}}
{{- int64 . -}}
{{- else -}}
{{- toString . -}}
{{- end -}}
{{- end }}
