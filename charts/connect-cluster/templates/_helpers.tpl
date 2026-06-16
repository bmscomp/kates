{{/*
Expand the name of the chart.
*/}}
{{- define "connect-cluster.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "connect-cluster.fullname" -}}
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
Allow the release namespace to be overridden
*/}}
{{- define "connect-cluster.namespace" -}}
{{- if .Values.namespaceOverride }}
{{- .Values.namespaceOverride }}
{{- else }}
{{- .Release.Namespace }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "connect-cluster.labels" -}}
helm.sh/chart: {{ include "connect-cluster.chart" . }}
{{ include "connect-cluster.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: connect
app.kubernetes.io/part-of: kates
{{- include "connect-cluster.extraLabels" . }}
{{- end }}

{{/*
Extra labels from values
*/}}
{{- define "connect-cluster.extraLabels" -}}
{{- with .Values.extraLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "connect-cluster.selectorLabels" -}}
app.kubernetes.io/name: {{ include "connect-cluster.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "connect-cluster.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Kubernetes cluster domain (for FQDN computation).
*/}}
{{- define "connect-cluster.clusterDomain" -}}
{{- .Values.clusterDomain | default "cluster.local" }}
{{- end }}

{{/*
ServiceAccount name
*/}}
{{- define "connect-cluster.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- .Values.serviceAccount.name | default (include "connect-cluster.fullname" .) }}
{{- else }}
{{- .Values.serviceAccount.name | default "default" }}
{{- end }}
{{- end }}

{{/*
Container security context (hardened)
*/}}
{{- define "connect-cluster.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop:
    - ALL
{{- end }}

{{/*
Kafka namespace — where the Kafka cluster lives.
Defaults to the Connect cluster namespace if not set.
*/}}
{{- define "connect-cluster.kafkaNamespace" -}}
{{- if .Values.kafka.namespace -}}
{{- .Values.kafka.namespace -}}
{{- else -}}
{{- include "connect-cluster.namespace" . -}}
{{- end -}}
{{- end -}}

{{/*
Kafka bootstrap servers FQDN (SASL_PLAINTEXT port 9092).
Usage: {{ include "connect-cluster.bootstrapServers" . }}
*/}}
{{- define "connect-cluster.bootstrapServers" -}}
{{- if .Values.kafka.bootstrapServers -}}
{{- .Values.kafka.bootstrapServers -}}
{{- else -}}
{{- $svc := printf "%s-kafka-bootstrap" (.Values.kafka.clusterName | default "krafter") -}}
{{- if .Values.kafka.tls.enabled -}}
{{- printf "%s.%s.svc.%s:9093" $svc (include "connect-cluster.kafkaNamespace" .) (include "connect-cluster.clusterDomain" .) -}}
{{- else -}}
{{- printf "%s.%s.svc.%s:9092" $svc (include "connect-cluster.kafkaNamespace" .) (include "connect-cluster.clusterDomain" .) -}}
{{- end -}}
{{- end -}}
{{- end -}}

