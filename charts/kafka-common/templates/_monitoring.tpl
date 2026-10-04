{{/*
A PodMonitor.

Call with (dict "name" … "namespace" … "labels" <YAML> "selector" <map>
"namespaces" <list, optional> "endpoint" (dict "port" "path" "interval"
"scrapeTimeout" "honorLabels" "relabelings" "metricRelabelings")), or with
"endpoints" (a list of such dicts) for a pod that serves metrics on several
ports.
*/}}
{{- define "kafka-common.podMonitor" -}}
{{- $eps := .endpoints | default (list (.endpoint | default dict)) -}}
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: {{ .name }}
  namespace: {{ .namespace }}
  labels:
    {{- .labels | nindent 4 }}
spec:
  selector:
    matchLabels:
      {{- toYaml .selector | nindent 6 }}
  {{- with .namespaces }}
  namespaceSelector:
    matchNames:
      {{- toYaml . | nindent 6 }}
  {{- end }}
  podMetricsEndpoints:
    {{- range $ep := $eps }}
    - port: {{ $ep.port | default "tcp-prometheus" }}
      path: {{ $ep.path | default "/metrics" }}
      {{- with $ep.interval }}
      interval: {{ . }}
      {{- end }}
      {{- with $ep.scrapeTimeout }}
      scrapeTimeout: {{ . }}
      {{- end }}
      {{- if $ep.honorLabels }}
      honorLabels: true
      {{- end }}
      {{- with $ep.relabelings }}
      relabelings:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with $ep.metricRelabelings }}
      metricRelabelings:
        {{- toYaml . | nindent 8 }}
      {{- end }}
    {{- end }}
{{- end }}

{{/*
The relabelings Strimzi's own dashboards expect on every operand scrape:
strimzi_io_* labels from the pod, plus namespace, kubernetes_pod_name,
node_name and node_ip. Emits a YAML list at column 0.
*/}}
{{- define "kafka-common.strimziRelabelings" -}}
- separator: ;
  regex: __meta_kubernetes_pod_label_(strimzi_io_.+)
  replacement: $1
  action: labelmap
- sourceLabels: [__meta_kubernetes_namespace]
  separator: ;
  regex: (.*)
  targetLabel: namespace
  replacement: $1
  action: replace
- sourceLabels: [__meta_kubernetes_pod_name]
  separator: ;
  regex: (.*)
  targetLabel: kubernetes_pod_name
  replacement: $1
  action: replace
- sourceLabels: [__meta_kubernetes_pod_node_name]
  separator: ;
  regex: (.*)
  targetLabel: node_name
  replacement: $1
  action: replace
- sourceLabels: [__meta_kubernetes_pod_host_ip]
  separator: ;
  regex: (.*)
  targetLabel: node_ip
  replacement: $1
  action: replace
{{- /*
The node pools set a `zone` pod label and nothing carried it into the series,
so every legacy dashboard legend that said `{{`{{`}}zone{{`}}`}}` rendered as
`()`. The regex is `(.+)` rather than `(.*)` on purpose: a pod with no zone
label keeps no zone label, instead of gaining an empty one that groups every
unzoned pod together.
*/}}
- sourceLabels: [__meta_kubernetes_pod_label_zone]
  separator: ;
  regex: (.+)
  targetLabel: zone
  replacement: $1
  action: replace
{{- end }}

{{/*
The PromQL matcher that scopes an expression to one release's pods:
namespace="<ns>", pod=~"<regex>". Call with (dict "namespace" … "pod" …).
Every chart-shipped alert and panel carries one — without it two releases in
one Prometheus fire each other's alerts.
*/}}
{{- define "kafka-common.promSelector" -}}
{{- printf "namespace=%q, pod=~%q" .namespace .pod -}}
{{- end }}

{{/*
A Grafana dashboard ConfigMap. Call with (dict "name" … "namespace" …
"labels" <YAML> "label" "grafana_dashboard" "labelValue" "1" "folder" "Kafka"
"file" "<key>.json" "json" <dashboard JSON string>).
*/}}
{{- define "kafka-common.dashboardConfigMap" -}}
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .name }}
  namespace: {{ .namespace }}
  labels:
    {{- .labels | nindent 4 }}
    {{ .label | default "grafana_dashboard" }}: {{ .labelValue | default "1" | quote }}
  {{- with .folder }}
  annotations:
    grafana_folder: {{ . | quote }}
  {{- end }}
data:
  {{ .file }}: |-
    {{- .json | nindent 4 }}
{{- end }}
