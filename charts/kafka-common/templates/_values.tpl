{{/*
A boolean switch with a default. `false | default true` is `true` in Helm —
`default` treats false as empty — which is how a switch documented as
"set false to disable" silently stays on. Call with (list <value> <default>);
renders "true" or nothing, so it is used as
  {{- if include "kafka-common.enabled" (list .Values.x.enabled true) }}
*/}}
{{- define "kafka-common.enabled" -}}
{{- $v := index . 0 -}}
{{- $d := index . 1 -}}
{{- if kindIs "invalid" $v -}}
{{- if $d }}true{{ end -}}
{{- else if kindIs "bool" $v -}}
{{- if $v }}true{{ end -}}
{{- else if eq (lower (toString $v)) "true" -}}
true
{{- else if eq (lower (toString $v)) "false" -}}
{{- else if $v -}}
true
{{- end -}}
{{- end }}

{{/*
Normalise a version to x.y.z so semverCompare accepts it: "2.8" -> 2.8.0,
"4.2-IV1" -> 4.2.0, "3.9.1-rhel" -> 3.9.1. Call with the version string.
*/}}
{{- define "kafka-common.semver" -}}
{{- $v := regexReplaceAll "[^0-9.].*$" (toString .) "" | trimSuffix "." -}}
{{- $p := splitList "." $v -}}
{{- if eq (len $p) 1 -}}{{- printf "%s.0.0" (index $p 0) -}}
{{- else if eq (len $p) 2 -}}{{- printf "%s.%s.0" (index $p 0) (index $p 1) -}}
{{- else -}}{{- printf "%s.%s.%s" (index $p 0) (index $p 1) (index $p 2) -}}
{{- end -}}
{{- end }}

{{/*
imagePullPolicy for pods the chart itself owns (hooks, tests). Empty renders
nothing and leaves Kubernetes' own rule. Call with the policy string.
*/}}
{{- define "kafka-common.imagePullPolicy" -}}
{{- with . }}
imagePullPolicy: {{ . }}
{{- end }}
{{- end }}
