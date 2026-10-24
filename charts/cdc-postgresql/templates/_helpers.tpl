{{/* Resources take the release name: kates deploy names it postgresql. */}}
{{- define "cdc-postgresql.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* app.kubernetes.io/name is postgresql whatever the release: Kafka Connect's
     NetworkPolicy and the Kates API's INTEGRATION_CDC test select on it. */}}
{{- define "cdc-postgresql.selectorLabels" -}}
app.kubernetes.io/name: postgresql
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "cdc-postgresql.labels" -}}
{{ include "cdc-postgresql.selectorLabels" . }}
app.kubernetes.io/component: database
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "cdc-postgresql.image" -}}
{{- $image := printf "%s:%s" .Values.image.repository .Values.image.tag -}}
{{- with .Values.global.imageRegistry -}}{{ printf "%s/%s" (trimSuffix "/" .) $image }}{{- else -}}{{ $image }}{{- end -}}
{{- end -}}
