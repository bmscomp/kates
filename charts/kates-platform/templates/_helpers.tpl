{{/*
Expand the name of the chart.
*/}}
{{- define "kates-platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "kates-platform.labels" -}}
helm.sh/chart: {{ include "kates-platform.name" . }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kates-platform
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
