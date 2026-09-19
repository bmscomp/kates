{{/*
A HorizontalPodAutoscaler on a Strimzi CR's scale subresource.

Call with (dict "ctx" $ "name" <cr name> "namespace" <ns> "kind" <CR kind>
"labels" <labels YAML> "autoscaling" <values> "minDefault" <int> "maxDefault" <int>).
The CR keeps rendering spec.replicas (see connectWorker.spec) because the v1
CRDs require it.
*/}}
{{- define "kafka-common.hpa" -}}
{{- $as := .autoscaling | default dict -}}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ .name }}
  namespace: {{ .namespace }}
  labels:
    {{- .labels | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: kafka.strimzi.io/v1
    kind: {{ .kind }}
    name: {{ .name }}
  minReplicas: {{ $as.minReplicas | default .minDefault }}
  maxReplicas: {{ $as.maxReplicas | default .maxDefault }}
  metrics:
    {{- if $as.targetCPUUtilizationPercentage }}
    - type: Resource
      resource:
        name: cpu
        target:
          type: Utilization
          averageUtilization: {{ $as.targetCPUUtilizationPercentage }}
    {{- end }}
    {{- if $as.targetMemoryUtilizationPercentage }}
    - type: Resource
      resource:
        name: memory
        target:
          type: Utilization
          averageUtilization: {{ $as.targetMemoryUtilizationPercentage }}
    {{- end }}
    {{- with $as.extraMetrics }}
    {{- toYaml . | nindent 4 }}
    {{- end }}
  {{- with $as.behavior }}
  behavior:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
