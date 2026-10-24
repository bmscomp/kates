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
{{- with .Values.image.digest -}}{{- $image = printf "%s@%s" $.Values.image.repository . -}}{{- end -}}
{{- with .Values.global.imageRegistry -}}{{ printf "%s/%s" (trimSuffix "/" .) $image }}{{- else -}}{{ $image }}{{- end -}}
{{- end -}}


{{/* Pull secrets, as names or {name: …}. */}}
{{- define "cdc-postgresql.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets }}
imagePullSecrets:
  {{- range . }}
  - name: {{ if kindIs "string" . }}{{ . }}{{ else }}{{ .name }}{{ end }}
  {{- end }}
{{- end }}
{{- end -}}

{{/* The Helm test pod's labels: not app.kubernetes.io/name, which would put
     it behind the Service. */}}
{{- define "cdc-postgresql.testLabels" -}}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: test
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}
