{{/*
Expand the name of the chart.
*/}}
{{- define "kafka-cluster.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified cluster name.
Falls back to the release name when clusterName is not set.
*/}}
{{- define "kafka-cluster.clusterName" -}}
{{- .Values.clusterName | default .Release.Name }}
{{- end }}

{{/*
Common labels applied to every resource, including user-supplied extraLabels
(the CLI stamps kates.io/lab and kates.io/lab-role through them).
*/}}
{{- define "kafka-cluster.labels" -}}
helm.sh/chart: {{ include "kafka-cluster.name" . }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "kafka-cluster.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ include "kafka-cluster.name" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
strimzi.io/cluster: {{ include "kafka-cluster.clusterName" . }}
{{- with .Values.extraLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Normalise a version to x.y.z so semverCompare accepts it: "4.2" and "4.2-IV1"
both become 4.2.0, "4.1.2-rc1" becomes 4.1.2. Call with the version string.
*/}}
{{- define "kafka-cluster.semver" -}}
{{- $v := regexReplaceAll "[^0-9.].*$" (toString .) "" | trimSuffix "." -}}
{{- $p := splitList "." $v -}}
{{- if eq (len $p) 1 -}}{{- printf "%s.0.0" (index $p 0) -}}
{{- else if eq (len $p) 2 -}}{{- printf "%s.%s.0" (index $p 0) (index $p 1) -}}
{{- else -}}{{- printf "%s.%s.%s" (index $p 0) (index $p 1) (index $p 2) -}}
{{- end -}}
{{- end }}

{{/*
The Kafka broker configuration to render: kafka.config, minus the share-group
keys (group.share.*, share.*) when kafkaVersion is below 4.2.0 — share groups
are the feature behind Chart.yaml's kates.io/kafka-floor, and a 4.1.x cluster
must be able to start without them. Emits YAML at column 0; the caller nindents.
*/}}
{{- define "kafka-cluster.kafkaConfig" -}}
{{- $config := .Values.kafka.config | default dict -}}
{{- if semverCompare "<4.2.0" (include "kafka-cluster.semver" .Values.kafkaVersion) -}}
{{- /* A filtered COPY — .Values is never mutated. */ -}}
{{- $filtered := dict -}}
{{- range $key, $value := $config -}}
{{- if not (or (hasPrefix "group.share." $key) (hasPrefix "share." $key)) -}}
{{- $_ := set $filtered $key $value -}}
{{- end -}}
{{- end -}}
{{- $config = $filtered -}}
{{- end -}}
{{- range $key, $value := $config }}
{{ $key }}: {{ $value | toJson }}
{{- end }}
{{- end }}

{{/*
Render-time guardrail: a metadata version newer than the Kafka version can
never work — the brokers would refuse to start — and Strimzi only reports it
minutes later as a NotReady Kafka. Fail here instead.
*/}}
{{- define "kafka-cluster.validateVersions" -}}
{{- if .Values.kafka.metadataVersion -}}
{{- $kafka := include "kafka-cluster.semver" .Values.kafkaVersion -}}
{{- $meta := include "kafka-cluster.semver" .Values.kafka.metadataVersion -}}
{{- $kafkaMinor := printf "%s.%s.0" (index (splitList "." $kafka) 0) (index (splitList "." $kafka) 1) -}}
{{- if semverCompare (printf ">%s" $kafkaMinor) $meta -}}
{{- fail (printf "kafka-cluster: kafka.metadataVersion %q is newer than kafkaVersion %q — a broker cannot run metadata from a release it does not have. Set kafka.metadataVersion to %s or older (the CLI derives one step behind the Kafka version)." (toString .Values.kafka.metadataVersion) (toString .Values.kafkaVersion) (printf "%s.%s" (index (splitList "." $kafka) 0) (index (splitList "." $kafka) 1))) -}}
{{- end -}}
{{- end -}}
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
runAsUser: 1001
fsGroup: 1001
seccompProfile:
  type: RuntimeDefault
{{- end }}

{{/*
Resolve the Kafka client image used in Helm tests.
Default: quay.io/strimzi/kafka:1.1.0-kafka-4.3.0
Override via: images.kafka or global.imageRegistry + global.imageRepository
*/}}
{{- define "kafka-cluster.kafkaImage" -}}
{{- if .Values.testImages.kafka -}}
  {{- .Values.testImages.kafka -}}
{{- else -}}
  {{- printf "%s/%s/kafka:%s-kafka-%s" .Values.global.imageRegistry .Values.global.imageRepository .Values.strimziVersion .Values.kafkaVersion -}}
{{- end -}}
{{- end }}

{{/*
Resolve the kubectl image used in Helm tests and CRD upgrade hooks.
Default: bitnami/kubectl:1.33.0
Override via: images.kubectl
*/}}
{{- define "kafka-cluster.kubectlImage" -}}
{{- .Values.testImages.kubectl -}}
{{- end }}

{{/*
Cluster DNS domain (e.g. cluster.local).
*/}}
{{- define "kafka-cluster.clusterDomain" -}}
{{- .Values.global.clusterDomain | default "cluster.local" -}}
{{- end }}

{{/*
Build a fully qualified service name:
  <service>.<namespace>.svc.<clusterDomain>
Usage: {{ include "kafka-cluster.serviceFQDN" (dict "service" "my-svc" "namespace" .Release.Namespace "root" .) }}
*/}}
{{- define "kafka-cluster.serviceFQDN" -}}
{{- printf "%s.%s.svc.%s" .service .namespace (include "kafka-cluster.clusterDomain" .root) -}}
{{- end }}

{{/*
Kafka bootstrap servers FQDN (SASL_PLAINTEXT port 9092).
Usage: {{ include "kafka-cluster.bootstrapServers" . }}
*/}}
{{- define "kafka-cluster.bootstrapServers" -}}
{{- $svc := printf "%s-kafka-bootstrap" (include "kafka-cluster.clusterName" .) -}}
{{- printf "%s.%s.svc.%s:9092" $svc (include "kafka-cluster.namespace" .) (include "kafka-cluster.clusterDomain" .) -}}
{{- end }}

{{/*
Kafka bootstrap servers FQDN (TLS port 9093).
Usage: {{ include "kafka-cluster.bootstrapServersTLS" . }}
*/}}
{{- define "kafka-cluster.bootstrapServersTLS" -}}
{{- $svc := printf "%s-kafka-bootstrap" (include "kafka-cluster.clusterName" .) -}}
{{- printf "%s.%s.svc.%s:9093" $svc (include "kafka-cluster.namespace" .) (include "kafka-cluster.clusterDomain" .) -}}
{{- end }}
