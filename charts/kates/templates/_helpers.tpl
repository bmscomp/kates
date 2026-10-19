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
app.kubernetes.io/part-of: kates
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kates.selectorLabels" -}}
app: {{ include "kates.name" . }}
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
jdbc:postgresql://{{ include "kates.postgresql.fullname" . }}.{{ .Release.Namespace }}.svc.{{ .Values.global.clusterDomain }}:5432/{{ .Values.postgresql.auth.database }}
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

{{/*
The key that holds the database password in kates.dbSecretName: passwordKey
in a Secret you bring (existingSecret), QUARKUS_DATASOURCE_PASSWORD in the
one the chart creates. The username is never read from the Secret: it is
postgresql.auth.username or externalDatabase.username.
*/}}
{{- define "kates.dbPasswordKey" -}}
{{- $db := ternary .Values.postgresql.auth .Values.externalDatabase .Values.postgresql.enabled -}}
{{- if $db.existingSecret -}}
  {{- $db.passwordKey | default "password" -}}
{{- else -}}
  QUARKUS_DATASOURCE_PASSWORD
{{- end -}}
{{- end }}

{{- define "kates.dbUsername" -}}
{{- (ternary .Values.postgresql.auth .Values.externalDatabase .Values.postgresql.enabled).username -}}
{{- end }}

{{/*
The database credentials as env entries, for the Kates API and the
migration Job. The Secret is required: a wrong name or key stops the pod
with CreateContainerConfigError instead of logging in with no password.
*/}}
{{- define "kates.dbEnv" -}}
{{- if or .Values.postgresql.enabled .Values.externalDatabase.enabled -}}
- name: QUARKUS_DATASOURCE_USERNAME
  value: {{ include "kates.dbUsername" . | quote }}
- name: QUARKUS_DATASOURCE_PASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "kates.dbSecretName" . }}
      key: {{ include "kates.dbPasswordKey" . }}
{{- end }}
{{- end }}

{{/*
Resolve the full image reference, prepending global.imageRegistry if set.
Usage: {{ include "kates.image" . }}
*/}}
{{- define "kates.image" -}}
{{- if and .Values.global .Values.global.imageRegistry -}}
{{ .Values.global.imageRegistry }}/{{ .Values.image.repository }}:{{ .Values.image.tag }}
{{- else -}}
{{ .Values.image.repository }}:{{ .Values.image.tag }}
{{- end -}}
{{- end }}
