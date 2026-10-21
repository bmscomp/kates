{{/*
Expand the name of the chart.
*/}}
{{- define "headlamp.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "headlamp.fullname" -}}
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
{{- define "headlamp.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "headlamp.labels" -}}
helm.sh/chart: {{ include "headlamp.chart" . }}
{{ include "headlamp.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "headlamp.selectorLabels" -}}
app.kubernetes.io/name: {{ include "headlamp.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "headlamp.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "headlamp.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Labels for a pod template: the chart's own labels (pass them as .labels)
plus podLabels. The chart's own win on a clash, so a selector never changes.
Usage: include "headlamp.podLabels" (dict "ctx" . "labels" (include "headlamp.selectorLabels" .))
*/}}
{{- define "headlamp.podLabels" -}}
{{- toYaml (merge (fromYaml .labels) .ctx.Values.podLabels) }}
{{- end }}

{{/*
An image reference behind global.imageRegistry when that is set.
Usage: include "headlamp.image" (dict "ctx" . "image" "repository:tag")
*/}}
{{- define "headlamp.image" -}}
{{- with .ctx.Values.global.imageRegistry }}{{ . }}/{{ end }}{{ .image }}
{{- end }}
