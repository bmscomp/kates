{{/*
NetworkPolicy rule fragments. Each emits ONE list item at column 0; callers
place it under `ingress:` or `egress:` with nindent.
*/}}

{{/*
DNS egress. Call with (dict "namespaceSelector" <map> "podSelector" <map>).
Empty selectors allow any destination; pin kube-dns on strict clusters.
*/}}
{{- define "kafka-common.netpol.dnsEgress" -}}
- to:
    {{- if or .namespaceSelector .podSelector }}
    - {{- if .namespaceSelector }}
      namespaceSelector:
        matchLabels:
          {{- toYaml .namespaceSelector | nindent 10 }}
      {{- end }}
      {{- if .podSelector }}
      podSelector:
        matchLabels:
          {{- toYaml .podSelector | nindent 10 }}
      {{- end }}
    {{- else }}
    - namespaceSelector: {}
    {{- end }}
  ports:
    - port: 53
      protocol: UDP
    - port: 53
      protocol: TCP
{{- end }}

{{/*
Kubernetes API server egress. Call with (dict "ports" <list> "ipBlock" <map>).
*/}}
{{- define "kafka-common.netpol.apiServerEgress" -}}
{{- $ports := list -}}
{{- range (.ports | default (list 443 6443)) }}{{- $ports = append $ports (dict "port" (int .) "protocol" "TCP") }}{{- end -}}
{{- $rule := dict "ports" $ports -}}
{{- with .ipBlock }}{{- $_ := set $rule "to" (list (dict "ipBlock" .)) }}{{- end -}}
{{- toYaml (list $rule) -}}
{{- end }}

{{/*
Traffic between the workers of one group on the Connect REST port (leader
forwarding). Call with (dict "direction" "from"|"to" "selector" <map> "port" 8083).
*/}}
{{- define "kafka-common.netpol.workers" -}}
- {{ .direction }}:
    - podSelector:
        matchLabels:
          {{- toYaml .selector | nindent 10 }}
  ports:
    - port: {{ .port | default 8083 }}
      protocol: TCP
{{- end }}

{{/*
Ingress from the Strimzi Cluster Operator to the Connect REST API, which it
uses to create, poll and restart connectors. Redundant with a default
operator's own policies — and not redundant at all with
STRIMZI_NETWORK_POLICY_GENERATION=false, where the operator is otherwise
locked out and no connector is ever created while the CR reports Ready.
Call with (dict "namespace" <operator ns> "port" 8083).
*/}}
{{- define "kafka-common.netpol.operatorIngress" -}}
- from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: {{ .namespace }}
      podSelector:
        matchLabels:
          strimzi.io/kind: cluster-operator
  ports:
    - port: {{ .port | default 8083 }}
      protocol: TCP
{{- end }}

{{/*
Metrics scrape ingress from the monitoring namespace.
Call with (dict "namespace" <ns> "port" 9404).
*/}}
{{- define "kafka-common.netpol.monitoringIngress" -}}
- from:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: {{ .namespace }}
  ports:
    - port: {{ .port | default 9404 }}
      protocol: TCP
{{- end }}

{{/*
The ports a bootstrap string dials, as strings. Call with the bootstrap string.
*/}}
{{- define "kafka-common.bootstrapPorts" -}}
{{- $ports := list -}}
{{- range (splitList "," .) -}}
{{- $ports = append $ports (. | trim | splitList ":" | last) -}}
{{- end -}}
{{- $ports | uniq | join "," -}}
{{- end }}

{{/*
Rail: every port a bootstrap string dials must be one the Kafka egress rule
admits, or the workers are dropped at the network layer while every pod
reports Running. Call with (dict "chart" … "bootstrap" … "ports" <list>
"field" "<values path of the ports>").
*/}}
{{- define "kafka-common.rails.bootstrapPorts" -}}
{{- $allowed := list -}}
{{- range .ports }}{{- $allowed = append $allowed (toString .) }}{{- end -}}
{{- range (splitList "," (include "kafka-common.bootstrapPorts" .bootstrap)) -}}
{{- if not (has . $allowed) -}}
{{- fail (printf "%s: the Kafka bootstrap %q dials port %s, but %s allows only %s — the workers would be dropped at the network layer. Add %s to %s, or point the bootstrap at an allowed listener." $.chart $.bootstrap . $.field (join ", " $allowed) . $.field) -}}
{{- end -}}
{{- end -}}
{{- end }}
