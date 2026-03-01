{{- define "kates.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kates.fullname" -}}
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

{{- define "kates.labels" -}}
app.kubernetes.io/name: {{ include "kates.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: klster
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kates.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kates.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "kates.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kates.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "kates.postgresql.fullname" -}}
{{ include "kates.fullname" . }}-postgresql
{{- end }}

{{- define "kates.postgresql.jdbcUrl" -}}
{{- if .Values.postgresql.enabled -}}
jdbc:postgresql://{{ include "kates.postgresql.fullname" . }}.{{ .Release.Namespace }}.svc:5432/{{ .Values.postgresql.auth.database }}
{{- else -}}
jdbc:postgresql://{{ .Values.externalDatabase.host }}:{{ .Values.externalDatabase.port | default 5432 }}/{{ .Values.externalDatabase.database }}
{{- end -}}
{{- end }}

{{- define "kates.dbSecretName" -}}
{{- if .Values.postgresql.enabled -}}
  {{- if .Values.postgresql.auth.existingSecret -}}
    {{- .Values.postgresql.auth.existingSecret -}}
  {{- else -}}
    {{- include "kates.fullname" . -}}-db
  {{- end -}}
{{- else if .Values.externalDatabase.enabled -}}
  {{- if .Values.externalDatabase.existingSecret -}}
    {{- .Values.externalDatabase.existingSecret -}}
  {{- else -}}
    {{- include "kates.fullname" . -}}-db
  {{- end -}}
{{- end -}}
{{- end }}
