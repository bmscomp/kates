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
`kates-monitoring.legacyKafkaDashboards` used to list the nine deprecated
Kafka and Strimzi boards this chart shipped, so that grafana-dashboards.yaml
could leave them out when `legacyKafkaDashboards.enabled` was false. All nine
files are gone in 1.3.0 — eight of them were subsets of the Strimzi operator's
own boards or of each other, and the ninth, kafka-perf-global, is rebuilt on
producible metric names as dashboards/kafka-performance. The helper and the
value it read are removed with them: a list that is empty gates nothing, and
leaving the switch in place would have promised a set of boards that no longer
exists. See docs/grafana-dashboards-refactor-plan.md.
*/}}
