{{/*
READING THE SUBCHART'S VALUES BLOCK — READ THIS BEFORE EDITING ANY TEMPLATE.

`.Values.strimzi-kafka-operator.watchAnyNamespace` is a Go template PARSE
ERROR. Hyphens are illegal in field selectors, so that expression does not
merely return empty — it fails the whole chart at parse time with
"bad character U+002D", breaking every helm template/install/lint/CI render.

`index` is the only way to read a hyphenated key from a template. Any template
needing the block opens with:

  {{- $op := index .Values "strimzi-kafka-operator" -}}

and then uses `$op.watchAnyNamespace` normally. No helper is defined for this
on purpose: `index .Values "strimzi-kafka-operator"` already yields a real map,
whereas a define would have to round-trip through toYaml/fromYaml to hand one
back.

Note `--set strimzi-kafka-operator.foo=bar` on the command line and the YAML
key itself are both FINE; only template field access breaks. A Chart.yaml
`alias:` would fix the access but relocate the values key, invalidating the
schema, the README rename table, and the `--set strimzi-kafka-operator.*` UX
this chart is built around.
*/}}

{{- define "strimzi-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "strimzi-operator.fullname" -}}
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

{{- define "strimzi-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Selector labels — the stable subset used in matchLabels/selectors.
Never add version-varying labels here.
*/}}
{{- define "strimzi-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "strimzi-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Common labels applied to every resource, including user-supplied commonLabels.
*/}}
{{- define "strimzi-operator.labels" -}}
{{ include "strimzi-operator.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kates
app.kubernetes.io/component: operator
helm.sh/chart: {{ include "strimzi-operator.chart" . }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Common annotations (empty unless commonAnnotations is set).
*/}}
{{- define "strimzi-operator.annotations" -}}
{{- with .Values.commonAnnotations }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
kubectl image used by the CRD-upgrade hook Job and the Helm tests.
Flat string, matching kafka-cluster/_helpers.tpl and connect-cluster — the two
charts that already own a kubectl image.
*/}}
{{- define "strimzi-operator.kubectlImage" -}}
{{- .Values.testImages.kubectl -}}
{{- end }}

{{/*
Pod security context for this chart's OWN resources (the hook Job and tests).
Not the operator's — that is strimzi-kafka-operator.podSecurityContext.
*/}}
{{- define "strimzi-operator.podSecurityContext" -}}
runAsNonRoot: true
runAsUser: 65534
seccompProfile:
  type: RuntimeDefault
{{- end }}

{{/*
Container security context for this chart's OWN resources.
readOnlyRootFilesystem is false: the hook Job writes the downloaded CRD bundle
to /tmp.
*/}}
{{- define "strimzi-operator.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: false
capabilities:
  drop: ["ALL"]
{{- end }}

{{/*
Resolved CRD bundle URL: crdUpgrade.url when set, else the GitHub release
bundle for .Values.strimziVersion.
*/}}
{{- define "strimzi-operator.crdUrl" -}}
{{- if .Values.crdUpgrade.url -}}
{{- .Values.crdUpgrade.url -}}
{{- else -}}
{{- printf "https://github.com/strimzi/strimzi-kafka-operator/releases/download/%s/strimzi-crds-%s.yaml" .Values.strimziVersion .Values.strimziVersion -}}
{{- end -}}
{{- end }}
