{{- define "kates-monitoring.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kates-monitoring.fullname" -}}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "kates-monitoring.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kates-monitoring
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}

{{/*
The deprecated Kafka and Strimzi boards (see grafana-dashboards.yaml). Removed
in kates-monitoring 2.0.
*/}}
{{- define "kates-monitoring.legacyKafkaDashboards" -}}
- kafka-all-metrics-dashboard.json
- kafka-comprehensive-dashboard.json
- kafka-dashboard.json
- kafka-jvm-dashboard.json
- kafka-perf-global-dashboard.json
- kafka-perf-test-dashboard.json
- kafka-performance-dashboard.json
- kafka-working-dashboard.json
- strimzi-operator-dashboard.json
{{- end }}
