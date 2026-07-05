{{/*
Expand the name of the chart.
*/}}
{{- define "kafka-ui.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "kafka-ui.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Allow the release namespace to be overridden.
*/}}
{{- define "kafka-ui.namespace" -}}
{{- if .Values.namespaceOverride }}
{{- .Values.namespaceOverride }}
{{- else }}
{{- .Release.Namespace }}
{{- end }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "kafka-ui.labels" -}}
helm.sh/chart: {{ include "kafka-ui.chart" . }}
{{ include "kafka-ui.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: dashboard
app.kubernetes.io/part-of: kates
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "kafka-ui.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kafka-ui.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app: kafka-ui
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kafka-ui.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Kafka namespace — where the Kafka cluster lives.
Defaults to the release namespace if not set.
*/}}
{{- define "kafka-ui.kafkaNamespace" -}}
{{- if .Values.kafka.namespace -}}
{{- .Values.kafka.namespace -}}
{{- else -}}
{{- include "kafka-ui.namespace" . -}}
{{- end -}}
{{- end -}}

{{/*
Kafka bootstrap servers FQDN.
*/}}
{{- define "kafka-ui.bootstrapServers" -}}
{{- if .Values.kafka.bootstrapServers -}}
{{- .Values.kafka.bootstrapServers -}}
{{- else -}}
{{- $svc := printf "%s-kafka-bootstrap" (.Values.kafka.clusterName | default "krafter") -}}
{{- $clusterDomain := .Values.kafka.clusterDomain | default "cluster.local" -}}
{{- if .Values.kafka.tls.enabled -}}
{{- printf "%s.%s.svc.%s:9093" $svc (include "kafka-ui.kafkaNamespace" .) $clusterDomain -}}
{{- else -}}
{{- printf "%s.%s.svc.%s:9092" $svc (include "kafka-ui.kafkaNamespace" .) $clusterDomain -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
KafkaUser secret name — the Strimzi-generated SCRAM credential.
*/}}
{{- define "kafka-ui.secretName" -}}
{{- .Values.kafkaUser.name | default "kafka-ui" -}}
{{- end -}}

{{/*
Container security context (hardened).
*/}}
{{- define "kafka-ui.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: false
capabilities:
  drop:
    - ALL
{{- end }}
