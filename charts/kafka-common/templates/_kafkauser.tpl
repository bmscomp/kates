{{/*
A Strimzi KafkaUser. The ACL list is chart-specific and passed pre-rendered
(`acls`, a YAML list string) so the chart can keep a comment beside every
grant explaining which setting needs it.

Call with (dict "name" … "namespace" … "cluster" <strimzi.io/cluster>
"labels" <labels YAML> "keep" <bool> "authType" … "quotas" <map>
"template" <map> "authorization" (dict "enabled" <bool> "type" "simple")
"acls" <YAML list string>).
*/}}
{{- define "kafka-common.kafkaUser" -}}
{{- $authz := .authorization | default dict -}}
{{- $authType := .authType | default "scram-sha-512" -}}
{{- if not (has $authType (list "scram-sha-512" "tls" "tls-external")) -}}
{{- fail (printf "KafkaUser %s: authentication type %q is not one a Strimzi KafkaUser can have (scram-sha-512, tls, tls-external). Provision other identities outside the chart." .name $authType) -}}
{{- end -}}
apiVersion: kafka.strimzi.io/v1
kind: KafkaUser
metadata:
  name: {{ .name }}
  namespace: {{ .namespace }}
  labels:
    {{- .labels | nindent 4 }}
    strimzi.io/cluster: {{ .cluster }}
  {{- if .keep }}
  annotations:
    helm.sh/resource-policy: keep
  {{- end }}
spec:
  authentication:
    type: {{ $authType }}
  {{- with .quotas }}
  quotas:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with .template }}
  template:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- if include "kafka-common.enabled" (list $authz.enabled true) }}
  authorization:
    type: {{ $authz.type | default "simple" }}
    acls:
      {{- .acls | trim | nindent 6 }}
  {{- end }}
{{- end }}
