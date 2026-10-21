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

{{/*
An image reference behind a mirror registry: <registry>/<image> when registry
is set, as charts/kates does with global.imageRegistry; the image unchanged
otherwise, and an empty image stays empty. Call with (dict "image" <ref>
"registry" <registry>).
*/}}
{{- define "kafka-common.image" -}}
{{- if and .registry .image -}}
{{- printf "%s/%s" (trimSuffix "/" .registry) .image -}}
{{- else -}}
{{- .image -}}
{{- end -}}
{{- end }}

{{/*
The security contexts Pod Security Standards "restricted" asks for, and no
more — what Strimzi's restricted security provider sets. Strimzi operands get
them through their CR templates, so the pods pass on a shared operator that
is not configured with that provider. readOnlyRootFilesystem is not part of
the standard and is left to the caller. YAML.
*/}}
{{- define "kafka-common.restricted.pod" -}}
runAsNonRoot: true
seccompProfile:
  type: RuntimeDefault
{{- end }}

{{- define "kafka-common.restricted.container" -}}
allowPrivilegeEscalation: false
capabilities:
  drop: ["ALL"]
{{- end }}
