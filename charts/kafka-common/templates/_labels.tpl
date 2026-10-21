{{/*
Selector labels: the stable pair every chart selects its own objects by.
Call with the chart's root context.
*/}}
{{- define "kafka-common.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kafka-common.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Standard labels, built as a map so a key can never appear twice (a duplicate
label is accepted by the API server — last wins — and rejected by kubeconform).
Precedence, lowest first: the standard set, then `component` and `partOf`,
then the chart's own `extra` map (e.g. strimzi.io/cluster), then the user's
.Values.extraLabels.

Call with (dict "ctx" $ "component" "connect" "partOf" "kates" "extra" (dict …)).
*/}}
{{- define "kafka-common.labels" -}}
{{- $ctx := .ctx -}}
{{- $l := dict
      "helm.sh/chart" (include "kafka-common.chart" $ctx)
      "app.kubernetes.io/name" (include "kafka-common.name" $ctx)
      "app.kubernetes.io/instance" $ctx.Release.Name
      "app.kubernetes.io/managed-by" $ctx.Release.Service -}}
{{- with $ctx.Chart.AppVersion }}{{- $_ := set $l "app.kubernetes.io/version" (toString .) }}{{- end }}
{{- with .component }}{{- $_ := set $l "app.kubernetes.io/component" . }}{{- end }}
{{- with .partOf }}{{- $_ := set $l "app.kubernetes.io/part-of" . }}{{- end }}
{{- $l = mustMergeOverwrite $l (deepCopy (.extra | default dict)) (deepCopy ($ctx.Values.extraLabels | default dict)) -}}
{{- toYaml $l }}
{{- end }}

{{/*
The labels of a pod the chart renders (a test pod, a Job's pod template): the
chart's own `labels` (YAML, as the helpers above emit it) over the user's
`podLabels` map. A podLabels key adds a label and never replaces one of the
chart's, so no selector changes. `testPod` adds kates.io/test-pod: "true",
which the Helm tests' NetworkPolicies select.

Call with (dict "labels" <YAML> "podLabels" <map> "testPod" <bool>).
*/}}
{{- define "kafka-common.podLabels" -}}
{{- $own := .labels | fromYaml -}}
{{- if .testPod }}{{- $_ := set $own "kates.io/test-pod" "true" }}{{- end -}}
{{- toYaml (merge $own (.podLabels | default dict)) }}
{{- end }}
