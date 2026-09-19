{{/*
Names. Every template takes the CALLING chart's root context, so the names are
the caller's (.Chart.Name, .Release.Name, .Values.nameOverride …), never the
library's.
*/}}

{{/* Chart name, overridable with nameOverride. */}}
{{- define "kafka-common.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Fully qualified name: fullnameOverride, else <release>-<chart> unless the release already contains the chart name. */}}
{{- define "kafka-common.fullname" -}}
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

{{/* The release namespace, overridable with namespaceOverride. */}}
{{- define "kafka-common.namespace" -}}
{{- .Values.namespaceOverride | default .Release.Namespace }}
{{- end }}

{{/* <chart>-<version> for the helm.sh/chart label. */}}
{{- define "kafka-common.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
The Kubernetes DNS domain: global.clusterDomain, then clusterDomain, then
cluster.local. Charts moved from a top-level key to global.* at different
times; both are honoured.
*/}}
{{- define "kafka-common.clusterDomain" -}}
{{- (.Values.global).clusterDomain | default .Values.clusterDomain | default "cluster.local" }}
{{- end }}
