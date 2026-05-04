{{/*
Expand the name of the chart.
*/}}
{{- define "kafka-cluster.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified cluster name.
*/}}
{{- define "kafka-cluster.clusterName" -}}
{{- .Values.clusterName }}
{{- end }}

{{/*
Common labels applied to every resource.
*/}}
{{- define "kafka-cluster.labels" -}}
helm.sh/chart: {{ include "kafka-cluster.name" . }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "kafka-cluster.name" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
strimzi.io/cluster: {{ include "kafka-cluster.clusterName" . }}
{{- end }}

{{/*
Strimzi cluster label (used for topic/user binding).
*/}}
{{- define "kafka-cluster.strimziLabel" -}}
strimzi.io/cluster: {{ include "kafka-cluster.clusterName" . }}
{{- end }}

{{/*
Namespace helper.
*/}}
{{- define "kafka-cluster.namespace" -}}
{{- .Release.Namespace }}
{{- end }}

{{/*
Security context defaults for Kafka containers.
*/}}
{{- define "kafka-cluster.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
{{- end }}

{{/*
Security context defaults for pods.
*/}}
{{- define "kafka-cluster.podSecurityContext" -}}
runAsNonRoot: true
fsGroup: 1001
seccompProfile:
  type: RuntimeDefault
{{- end }}

{{/*
Resolve the Kafka client image used in Helm tests.
Priority: images.kafka > global.imageRegistry/strimzi/kafka:strimziVersion-kafka-kafkaVersion > default
*/}}
{{- define "kafka-cluster.kafkaImage" -}}
{{- if .Values.images.kafka -}}
  {{- .Values.images.kafka -}}
{{- else if .Values.global.imageRegistry -}}
  {{- printf "%s/strimzi/kafka:%s-kafka-%s" .Values.global.imageRegistry .Values.strimziVersion .Values.kafkaVersion -}}
{{- else -}}
  {{- printf "quay.io/strimzi/kafka:%s-kafka-%s" .Values.strimziVersion .Values.kafkaVersion -}}
{{- end -}}
{{- end }}

{{/*
Resolve the kubectl image used in Helm tests and CRD upgrade hooks.
Priority: images.kubectl > global.imageRegistry/bitnami/kubectl:latest > default
*/}}
{{- define "kafka-cluster.kubectlImage" -}}
{{- if .Values.global.imageRegistry -}}
  {{- printf "%s/bitnami/kubectl:latest" .Values.global.imageRegistry -}}
{{- else -}}
  {{- .Values.images.kubectl | default "bitnami/kubectl:latest" -}}
{{- end -}}
{{- end }}
