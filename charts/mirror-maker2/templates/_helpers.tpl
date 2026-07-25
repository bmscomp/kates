{{/* Expand the name of the chart. */}}
{{- define "mirror-maker2.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Fully qualified app name. */}}
{{- define "mirror-maker2.fullname" -}}
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

{{/* Release namespace, overridable. */}}
{{- define "mirror-maker2.namespace" -}}
{{- .Values.namespaceOverride | default .Release.Namespace }}
{{- end }}

{{- define "mirror-maker2.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "mirror-maker2.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mirror-maker2.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "mirror-maker2.labels" -}}
helm.sh/chart: {{ include "mirror-maker2.chart" . }}
{{ include "mirror-maker2.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/component: mirror-maker2
app.kubernetes.io/part-of: kates
{{- with .Values.extraLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{- define "mirror-maker2.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- .Values.serviceAccount.name | default (include "mirror-maker2.fullname" .) }}
{{- else }}
{{- .Values.serviceAccount.name | default "default" }}
{{- end }}
{{- end }}

{{/* Strimzi labels the MM2 Connect pods <cluster>-mirrormaker2. */}}
{{- define "mirror-maker2.podSelectorLabels" -}}
strimzi.io/name: {{ include "mirror-maker2.fullname" . }}-mirrormaker2
strimzi.io/kind: KafkaMirrorMaker2
{{- end }}

{{/*
Bootstrap servers for a cluster entry (target or a mirror source).
Call with (dict "c" <clusterValues> "ctx" $).
- If `bootstrapServers` is set, it wins verbatim (external / inter-cluster).
- Otherwise the FQDN is computed from `clusterName` + `namespace` for an
  in-cluster Strimzi cluster: <clusterName>-kafka-bootstrap.<namespace>.svc.
  <clusterDomain>:<9093 if tls else 9092>. This makes intra-cluster (same or
  different namespace) and inter-cluster topologies configurable uniformly.
*/}}
{{- define "mirror-maker2.bootstrap" -}}
{{- $c := .c -}}{{- $ctx := .ctx -}}
{{- if $c.bootstrapServers -}}
{{- $c.bootstrapServers -}}
{{- else -}}
{{- $name := $c.clusterName | default "krafter" -}}
{{- $ns := $c.namespace | default "kafka" -}}
{{- $domain := $ctx.Values.clusterDomain | default "cluster.local" -}}
{{- $port := 9092 -}}
{{- if and $c.tls $c.tls.enabled -}}{{- $port = 9093 -}}{{- end -}}
{{- printf "%s-kafka-bootstrap.%s.svc.%s:%v" $name $ns $domain $port -}}
{{- end -}}
{{- end }}

{{/*
Render the tls + authentication blocks for a cluster entry (target or a
mirror source). Call with (dict "c" <clusterValues>). Emits at column 0; the
caller applies nindent.
*/}}
{{- define "mirror-maker2.clusterTlsAuth" -}}
{{- $c := .c -}}
{{- if and $c.tls $c.tls.enabled }}
tls:
  trustedCertificates:
    - secretName: {{ required "tls.trustedCertificateSecret is required when tls.enabled" $c.tls.trustedCertificateSecret }}
      certificate: {{ $c.tls.certificateKey | default "ca.crt" }}
{{- end }}
{{- with $c.authentication }}
{{- if .type }}
authentication:
  type: {{ .type }}
  {{- if or (eq .type "scram-sha-512") (eq .type "scram-sha-256") (eq .type "plain") }}
  username: {{ .username | quote }}
  passwordSecret:
    secretName: {{ .secretName | default .username | quote }}
    password: {{ .secretKey | default "password" }}
  {{- else if eq .type "tls" }}
  certificateAndKey:
    secretName: {{ .secretName | default (printf "%s-tls" .username) | quote }}
    certificate: {{ .certificate | default "user.crt" }}
    key: {{ .key | default "user.key" }}
  {{- end }}
{{- end }}
{{- end }}
{{- end }}
