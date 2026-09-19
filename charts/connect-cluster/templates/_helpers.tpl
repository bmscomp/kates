{{/* Chart name (kafka-common). */}}
{{- define "connect-cluster.name" -}}
{{- include "kafka-common.name" . }}
{{- end }}

{{/* Fully qualified name — also the KafkaConnect's name (kafka-common). */}}
{{- define "connect-cluster.fullname" -}}
{{- include "kafka-common.fullname" . }}
{{- end }}

{{/* Release namespace, overridable (kafka-common). */}}
{{- define "connect-cluster.namespace" -}}
{{- include "kafka-common.namespace" . }}
{{- end }}

{{- define "connect-cluster.chart" -}}
{{- include "kafka-common.chart" . }}
{{- end }}

{{- define "connect-cluster.selectorLabels" -}}
{{- include "kafka-common.selectorLabels" . }}
{{- end }}

{{/* Standard labels (kafka-common), built as a map so no key repeats. */}}
{{- define "connect-cluster.labels" -}}
{{- include "kafka-common.labels" (dict "ctx" . "component" "connect" "partOf" "kates") }}
{{- end }}

{{/*
Standard labels with another component, and optional extras. Call with
(dict "ctx" $ "component" "connector" "extra" (dict …)).
*/}}
{{- define "connect-cluster.componentLabels" -}}
{{- include "kafka-common.labels" (dict "ctx" .ctx "component" .component "partOf" "kates" "extra" (.extra | default dict)) }}
{{- end }}

{{- define "connect-cluster.clusterDomain" -}}
{{- include "kafka-common.clusterDomain" . }}
{{- end }}

{{/* The test pods' ServiceAccount. */}}
{{- define "connect-cluster.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- .Values.serviceAccount.name | default (include "connect-cluster.fullname" .) }}
{{- else }}
{{- .Values.serviceAccount.name | default "default" }}
{{- end }}
{{- end }}

{{/* The account Strimzi creates for the workers: <KafkaConnect name>-connect. */}}
{{- define "connect-cluster.workerServiceAccount" -}}
{{- printf "%s-connect" (include "connect-cluster.fullname" .) }}
{{- end }}

{{/* Strimzi labels the worker pods <cluster>-connect. */}}
{{- define "connect-cluster.podSelectorLabels" -}}
strimzi.io/name: {{ include "connect-cluster.fullname" . }}-connect
strimzi.io/kind: KafkaConnect
{{- end }}

{{/* Hardened container security context for the chart's own pods. */}}
{{- define "connect-cluster.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop:
    - ALL
{{- end }}

{{- define "connect-cluster.imagePullPolicy" -}}
{{- include "kafka-common.imagePullPolicy" .Values.imagePullPolicy }}
{{- end }}

{{/* Namespace of the Kafka cluster (default: the release namespace). */}}
{{- define "connect-cluster.kafkaNamespace" -}}
{{- .Values.kafka.namespace | default (include "connect-cluster.namespace" .) -}}
{{- end -}}

{{/* Namespace of the Schema Registry (default: the Kafka namespace). */}}
{{- define "connect-cluster.schemaRegistryNamespace" -}}
{{- .Values.schemaRegistry.namespace | default (include "connect-cluster.kafkaNamespace" .) -}}
{{- end -}}

{{/*
The Kafka connection as kafka-common reads it: the release namespace is the
namespace default, the cluster CA and the credentials Secret get their
Strimzi names when unset.
*/}}
{{- define "connect-cluster.kafka" -}}
{{- $k := deepCopy .Values.kafka -}}
{{- $_ := set $k "namespace" (include "connect-cluster.kafkaNamespace" .) -}}
{{- if not $k.listenerPort }}{{ $_ := unset $k "listenerPort" }}{{ end -}}
{{- $tls := $k.tls | default dict -}}
{{- if not $tls.trustedCertificateSecret -}}
{{- $_ := set $tls "trustedCertificateSecret" (printf "%s-cluster-ca-cert" ($k.clusterName | default "krafter")) -}}
{{- end -}}
{{- $_ := set $k "tls" $tls -}}
{{- $auth := $k.authentication | default dict -}}
{{- $user := include "connect-cluster.kafkaUsername" . -}}
{{- if and $user (has ($auth.type | default "") (list "scram-sha-512" "scram-sha-256" "plain" "tls")) -}}
{{- $_ := set $auth "username" $user -}}
{{- $_ := set $auth "secretName" ($auth.secretName | default $user) -}}
{{- if eq $auth.type "tls" }}{{ $_ := set $auth "certificateSecret" $auth.secretName }}{{ end -}}
{{- end -}}
{{- $_ := set $k "authentication" $auth -}}
{{- toYaml $k -}}
{{- end -}}

{{/* The Kafka principal: the managed user's name, else the configured username. */}}
{{- define "connect-cluster.kafkaUsername" -}}
{{- if (.Values.kafkaUser).create -}}
{{- .Values.kafkaUser.name | default .Values.kafka.authentication.username | default "kates-connect" -}}
{{- else -}}
{{- .Values.kafka.authentication.username -}}
{{- end -}}
{{- end -}}

{{/* Bootstrap servers (kafka-common). Usable from testConnectors values. */}}
{{- define "connect-cluster.bootstrap" -}}
{{- include "kafka-common.bootstrap" (dict "c" (include "connect-cluster.kafka" . | fromYaml) "ctx" . "defaultCluster" "krafter" "defaultNamespace" (include "connect-cluster.namespace" .)) -}}
{{- end -}}

{{/* The three internal topics, as YAML {offsets, configs, status}. */}}
{{- define "connect-cluster.internalTopics" -}}
{{- $it := .Values.internalTopics | default dict -}}
{{- $prefix := $it.prefix | default .Values.groupId | default "kates-connect-cluster" -}}
offsets: {{ $it.offsetsName | default (printf "%s-offsets" $prefix) | quote }}
configs: {{ $it.configsName | default (printf "%s-configs" $prefix) | quote }}
status: {{ $it.statusName | default (printf "%s-status" $prefix) | quote }}
{{- end -}}

{{/* OTLP egress port, parsed from tracing.endpoint (default 4317). */}}
{{- define "connect-cluster.tracingEgressPort" -}}
{{- $hostport := .Values.tracing.endpoint | default "" | replace "https://" "" | replace "http://" "" | replace "grpc://" "" -}}
{{- $hostport = splitList "/" $hostport | first -}}
{{- $parts := splitList ":" $hostport -}}
{{- if gt (len $parts) 1 -}}{{ last $parts }}{{- else -}}4317{{- end -}}
{{- end -}}

{{/*
The Connect-worker part of the spec, as the map kafka-common's
connectWorker.spec reads (shared with mirror-maker2).
*/}}
{{- define "connect-cluster.worker" -}}
{{- $r := include "connect-cluster.resolve" . | fromJson -}}
{{- $fullname := include "connect-cluster.fullname" . -}}
{{- $env := list -}}
{{- if and .Values.tracing.enabled .Values.tracing.endpoint -}}
{{- $env = list
      (dict "name" "OTEL_SERVICE_NAME" "value" (.Values.tracing.serviceName | default $fullname))
      (dict "name" "OTEL_EXPORTER_OTLP_ENDPOINT" "value" .Values.tracing.endpoint)
      (dict "name" "OTEL_TRACES_EXPORTER" "value" "otlp") -}}
{{- end -}}
{{- $env = concat $env (.Values.env | default list) -}}
{{- $annotations := dict "checksum/logging" (include "connect-cluster.loggingConfig" . | sha256sum) -}}
{{- if $r.metrics.create -}}
{{- $_ := set $annotations "checksum/metrics" (.Files.Get "files/metrics/connect-metrics.yaml" | sha256sum) -}}
{{- end -}}
{{- $sa := dict "metadata" (dict "labels" (include "connect-cluster.labels" . | fromYaml)) -}}
{{- with .Values.serviceAccount.annotations }}{{ $_ := set $sa.metadata "annotations" . }}{{ end -}}
{{- $logging := omit (.Values.logging | default dict) "rootLevel" -}}
{{- if eq ($logging.type | default "") "external" }}{{ $logging = omit $logging "loggers" }}{{ end -}}
{{- $w := dict
      "kind" "KafkaConnect"
      "name" $fullname
      "namespace" (include "connect-cluster.namespace" .)
      "version" .Values.version
      "image" .Values.image
      "replicas" .Values.replicas
      "minReplicasDefault" 3
      "autoscaling" .Values.autoscaling
      "resources" .Values.resources
      "jvmOptions" .Values.jvmOptions
      "livenessProbe" .Values.livenessProbe
      "readinessProbe" .Values.readinessProbe
      "jmxOptions" .Values.jmxOptions
      "logging" $logging
      "loggingConfigMap" (printf "%s-logging" $fullname)
      "tracing" (ternary (dict "type" (.Values.tracing.type | default "opentelemetry")) dict (eq (include "kafka-common.enabled" (list .Values.tracing.enabled true)) "true"))
      "rack" .Values.rack
      "metrics" (dict
          "enabled" $r.metrics.enabled
          "type" $r.metrics.type
          "allowList" .Values.metrics.allowList
          "configMapName" $r.metrics.configMapName
          "configMapKey" $r.metrics.configMapKey)
      "podSelector" (include "connect-cluster.podSelectorLabels" . | fromYaml)
      "template" (dict
          "pdb" .Values.podDisruptionBudget
          "podMetadata" (dict "annotations" $annotations)
          "podSecurityContext" $r.podSecurityContext
          "imagePullSecrets" .Values.imagePullSecrets
          "priorityClassName" .Values.priorityClassName
          "terminationGracePeriodSeconds" .Values.terminationGracePeriodSeconds
          "tolerations" .Values.tolerations
          "dnsPolicy" .Values.dnsPolicy
          "dnsConfig" .Values.dnsConfig
          "nodeSelector" .Values.nodeSelector
          "nodeAffinity" .Values.nodeAffinity
          "podAntiAffinity" .Values.podAntiAffinity
          "topologySpread" .Values.topologySpreadConstraints
          "container" (dict "harden" true "env" $env "extra" .Values.connectContainer)
          "serviceAccount" $sa
          "extra" $r.templateExtra) -}}
{{- toYaml $w -}}
{{- end }}

{{/* The worker's log4j2.properties (external logging). */}}
{{- define "connect-cluster.loggingConfig" -}}
{{- $l := .Values.logging | default dict -}}
name = Config

appender.console.type = Console
appender.console.name = STDOUT
appender.console.layout.type = PatternLayout
appender.console.layout.pattern = %d{yyyy-MM-dd HH:mm:ss} %-5p %c{1}:%L - %m%n

rootLogger.level = {{ $l.rootLevel | default "INFO" }}
rootLogger.appenderRef.console.ref = STDOUT
{{- range $name, $level := ($l.loggers | default dict) }}
{{- $id := regexReplaceAll "[^A-Za-z0-9]" $name "_" }}

logger.{{ $id }}.name = {{ $name }}
logger.{{ $id }}.level = {{ $level }}
{{- end }}
{{- end }}

{{/*
Known connector classes: the config keys each needs, and whether it is a
sink. A class not listed is checked only for what every connector needs.
*/}}
{{- define "connect-cluster.connectorClasses" -}}
io.debezium.connector.postgresql.PostgresConnector:
  required: [database.hostname, database.dbname, topic.prefix]
io.debezium.connector.mysql.MySqlConnector:
  required: [database.hostname, database.server.id, topic.prefix]
io.debezium.connector.mongodb.MongoDbConnector:
  required: [mongodb.connection.string, topic.prefix]
io.debezium.connector.sqlserver.SqlServerConnector:
  required: [database.hostname, database.names, topic.prefix]
io.debezium.connector.jdbc.JdbcSinkConnector:
  sink: true
  required: [connection.url]
io.aiven.connect.jdbc.JdbcSourceConnector:
  required: [connection.url, mode]
io.aiven.connect.jdbc.JdbcSinkConnector:
  sink: true
  required: [connection.url]
io.aiven.kafka.connect.s3.AivenKafkaConnectS3SinkConnector:
  sink: true
  required: [aws.s3.bucket.name]
org.apache.kafka.connect.file.FileStreamSinkConnector:
  sink: true
  required: [file]
org.apache.kafka.connect.mirror.MirrorHeartbeatConnector:
  required: [source.cluster.alias, target.cluster.alias]
{{- end }}
