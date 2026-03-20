{{/*
Expand the name of the chart.
*/}}
{{- define "klster-platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "klster-platform.labels" -}}
helm.sh/chart: {{ include "klster-platform.name" . }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: klster-platform
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
