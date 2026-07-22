{{/*
Expand the name of the chart.
*/}}
{{- define "apicurio-registry.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "apicurio-registry.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "apicurio-registry.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "apicurio-registry.labels" -}}
helm.sh/chart: {{ include "apicurio-registry.chart" . }}
{{ include "apicurio-registry.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "apicurio-registry.selectorLabels" -}}
app.kubernetes.io/name: {{ include "apicurio-registry.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "apicurio-registry.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "apicurio-registry.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Registry container image reference. Digest pins win over tags.
*/}}
{{- define "apicurio-registry.image" -}}
{{- $repo := printf "%s/%s" .Values.image.registry .Values.image.repository }}
{{- if .Values.image.digest }}
{{- printf "%s@%s" $repo .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" $repo (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}
{{- end }}

{{/*
Kafka bootstrap servers. The registry uses the external Strimzi cluster
deployed by the kafka-cluster chart in this repository (no bundled broker).
*/}}
{{- define "apicurio-registry.kafkaBootstrapServers" -}}
{{- required "Provide global.kafka bootstrap values (see the kafka-cluster chart)." (include "apicurio-registry.globalKafkaBootstrapServers" . | trim) }}
{{- end }}

{{- define "apicurio-registry.globalKafkaBootstrapServers" -}}
{{- if .Values.global.kafka.bootstrapServers }}
{{- tpl (join "," .Values.global.kafka.bootstrapServers) . }}
{{- else }}
{{- if .Values.global.kafka.fullname }}
{{- .Values.global.kafka.fullname | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- if .Values.global.kafka.name }}
{{- $name := .Values.global.kafka.name }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}
{{- if .Values.global.kafka.port }}
{{- printf ":%v" .Values.global.kafka.port }}
{{- end }}
{{- end }}
{{- end }}