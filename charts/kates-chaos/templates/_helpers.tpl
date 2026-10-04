{{- define "kates-chaos.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "kates-chaos.fullname" -}}
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

{{- define "kates-chaos.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Selector labels — the stable subset used in matchLabels/selectors.
Never add version-varying labels here.
*/}}
{{- define "kates-chaos.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kates-chaos.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Common labels applied to every resource, including user-supplied commonLabels.
*/}}
{{- define "kates-chaos.labels" -}}
{{ include "kates-chaos.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kates
helm.sh/chart: {{ include "kates-chaos.chart" . }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Common annotations (empty unless commonAnnotations is set).
*/}}
{{- define "kates-chaos.annotations" -}}
{{- with .Values.commonAnnotations }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Resolve an image reference from an image dict ({repository, tag, digest}) and
the global block. Prefers digest over tag; prepends global.imageRegistry.
Usage: include "kates-chaos.image" (dict "img" .Values.images.kubectl "global" .Values.global)
*/}}
{{- define "kates-chaos.image" -}}
{{- $img := .img -}}
{{- $global := .global | default dict -}}
{{- $registry := $global.imageRegistry | default "" -}}
{{- $repo := $img.repository -}}
{{- if $registry -}}{{- $repo = printf "%s/%s" $registry $repo -}}{{- end -}}
{{- if $img.digest -}}
{{- printf "%s@%s" $repo $img.digest -}}
{{- else -}}
{{- printf "%s:%s" $repo ($img.tag | toString) -}}
{{- end -}}
{{- end }}

{{/*
kubectl image used by the installer Job, gameday, and tests.
Honors, in order: experiments.installer.image (string), the deprecated
experiments.image (string), then the images.kubectl dict.
*/}}
{{- define "kates-chaos.kubectlImage" -}}
{{- $override := "" -}}
{{- if .Values.experiments.installer -}}{{- $override = .Values.experiments.installer.image | default "" -}}{{- end -}}
{{- if not $override -}}{{- $override = .Values.experiments.image | default "" -}}{{- end -}}
{{- if $override -}}
{{- $override -}}
{{- else -}}
{{- include "kates-chaos.image" (dict "img" .Values.images.kubectl "global" .Values.global) -}}
{{- end -}}
{{- end }}

{{/*
go-runner image used as the default for ChaosExperiment definitions.
*/}}
{{- define "kates-chaos.goRunnerImage" -}}
{{- include "kates-chaos.image" (dict "img" .Values.images.goRunner "global" .Values.global) -}}
{{- end }}

{{/*
Chaos service account name (shared by RBAC and ChaosEngines).
*/}}
{{- define "kates-chaos.serviceAccountName" -}}
{{- (.Values.serviceAccount).name | default "litmus-admin" -}}
{{- end }}

{{/*
Normalized target-namespace list as a YAML array of {name, role}.
Prefers rbac.targetNamespaces; falls back to the deprecated
rbac.kafkaNamespace / rbac.katesNamespace pair.
Consume with: include "kates-chaos.targetNamespaces" . | fromYamlArray
*/}}
{{- define "kates-chaos.targetNamespaces" -}}
{{- if .Values.rbac.targetNamespaces -}}
{{- toYaml .Values.rbac.targetNamespaces -}}
{{- else -}}
- name: {{ .Values.rbac.kafkaNamespace | default "kafka" }}
  role: target
- name: {{ .Values.rbac.katesNamespace | default "kates" }}
  role: coordinator
{{- end -}}
{{- end }}

{{/*
Primary "target"-role namespace (where experiments install and most faults
run). Honors the deprecated kafkaNamespace first for backward compatibility.
*/}}
{{- define "kates-chaos.kafkaNamespace" -}}
{{- if .Values.rbac.kafkaNamespace -}}
{{- .Values.rbac.kafkaNamespace -}}
{{- else -}}
{{- $ns := "" -}}
{{- range (include "kates-chaos.targetNamespaces" . | fromYamlArray) -}}
{{- if and (eq (.role | default "target") "target") (not $ns) -}}{{- $ns = .name -}}{{- end -}}
{{- end -}}
{{- $ns | default "kafka" -}}
{{- end -}}
{{- end }}

{{/*
Primary "coordinator"-role namespace. Honors deprecated katesNamespace first.
*/}}
{{- define "kates-chaos.katesNamespace" -}}
{{- if .Values.rbac.katesNamespace -}}
{{- .Values.rbac.katesNamespace -}}
{{- else -}}
{{- $ns := "" -}}
{{- range (include "kates-chaos.targetNamespaces" . | fromYamlArray) -}}
{{- if and (eq (.role | default "target") "coordinator") (not $ns) -}}{{- $ns = .name -}}{{- end -}}
{{- end -}}
{{- $ns | default "kates" -}}
{{- end -}}
{{- end }}

{{/*
Whether the NetworkPolicy is enabled. Enabled if EITHER the new
networkPolicy.enabled or the deprecated networkPolicies.enabled is true.
*/}}
{{- define "kates-chaos.networkPolicyEnabled" -}}
{{- $new := and (hasKey .Values "networkPolicy") .Values.networkPolicy.enabled -}}
{{- $old := and (hasKey .Values "networkPolicies") .Values.networkPolicies.enabled -}}
{{- if or $new $old -}}true{{- else -}}false{{- end -}}
{{- end }}
