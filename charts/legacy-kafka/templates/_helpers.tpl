{{/* Expand the name of the chart. */}}
{{- define "legacy-kafka.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Fully qualified app name. */}}
{{- define "legacy-kafka.fullname" -}}
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

{{- define "legacy-kafka.namespace" -}}
{{- .Values.namespaceOverride | default .Release.Namespace }}
{{- end }}

{{- define "legacy-kafka.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "legacy-kafka.selectorLabels" -}}
app.kubernetes.io/name: {{ include "legacy-kafka.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "legacy-kafka.labels" -}}
helm.sh/chart: {{ include "legacy-kafka.chart" . }}
{{ include "legacy-kafka.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.kafka.version | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kates
kates.io/kafka-mode: {{ include "legacy-kafka.mode" . }}
{{- with .Values.extraLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Normalise kafka.version to x.y.z so semverCompare accepts it: "2.8" becomes
2.8.0, "3.9.1-rhel" becomes 3.9.1. Call with the version string.
*/}}
{{- define "legacy-kafka.semver" -}}
{{- $v := regexReplaceAll "[^0-9.].*$" (toString .) "" | trimSuffix "." -}}
{{- $p := splitList "." $v -}}
{{- if eq (len $p) 1 -}}{{- printf "%s.0.0" (index $p 0) -}}
{{- else if eq (len $p) 2 -}}{{- printf "%s.%s.0" (index $p 0) (index $p 1) -}}
{{- else -}}{{- printf "%s.%s.%s" (index $p 0) (index $p 1) (index $p 2) -}}
{{- end -}}
{{- end }}

{{/*
The mode in force. An explicit `mode` wins; otherwise the version decides:
KRaft exists from 3.3.0 (KIP-833 marked it production-ready there; the
combined broker+controller shape this chart uses needs it), so anything older
is ZooKeeper. This is the rule the CLI's legacy provider relies on — a
`--version 3.5.2` needs no overlay.
*/}}
{{- define "legacy-kafka.mode" -}}
{{- if .Values.mode -}}
{{- .Values.mode -}}
{{- else if semverCompare "<3.3.0" (include "legacy-kafka.semver" .Values.kafka.version) -}}
zookeeper
{{- else -}}
kraft
{{- end -}}
{{- end }}

{{/*
The broker image in force (ZooKeeper, the topics Job and the Helm test run from
the same image). An explicit `kafka.image` wins; otherwise: the official
multi-arch apache/kafka:<version> exists from 3.7.0 (KIP-975), and below that
the image Dockerfile.legacy-kafka builds, <legacyImageRegistry>/kates-legacy-
kafka:<version> — `scripts/build-legacy-kafka-image.sh --version <v> --load`.
*/}}
{{- define "legacy-kafka.image" -}}
{{- if .Values.kafka.image -}}
{{- .Values.kafka.image -}}
{{- else if semverCompare ">=3.7.0" (include "legacy-kafka.semver" .Values.kafka.version) -}}
{{- printf "apache/kafka:%s" (toString .Values.kafka.version) -}}
{{- else -}}
{{- printf "%s/kates-legacy-kafka:%s" (.Values.legacyImageRegistry | default "ghcr.io/bmscomp" | trimSuffix "/") (toString .Values.kafka.version) -}}
{{- end -}}
{{- end }}

{{- define "legacy-kafka.brokerSelectorLabels" -}}
{{ include "legacy-kafka.selectorLabels" . }}
app.kubernetes.io/component: broker
{{- end }}

{{- define "legacy-kafka.zookeeperSelectorLabels" -}}
{{ include "legacy-kafka.selectorLabels" . }}
app.kubernetes.io/component: zookeeper
{{- end }}

{{- define "legacy-kafka.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- .Values.serviceAccount.name | default (include "legacy-kafka.fullname" .) }}
{{- else }}
{{- .Values.serviceAccount.name | default "default" }}
{{- end }}
{{- end }}

{{/* Service names. */}}
{{- define "legacy-kafka.headlessService" -}}
{{- printf "%s-headless" (include "legacy-kafka.fullname" .) }}
{{- end }}

{{- define "legacy-kafka.bootstrapService" -}}
{{- printf "%s-bootstrap" (include "legacy-kafka.fullname" .) }}
{{- end }}

{{- define "legacy-kafka.zookeeperService" -}}
{{- printf "%s-zookeeper" (include "legacy-kafka.fullname" .) }}
{{- end }}

{{/*
The bootstrap address a client outside this chart should use — the same string
that belongs in the mirror-maker2 chart's `mirrors[].source.bootstrapServers`.
*/}}
{{- define "legacy-kafka.bootstrapAddress" -}}
{{- $port := .Values.listeners.plaintext.port -}}
{{- if not .Values.listeners.plaintext.enabled -}}{{- $port = .Values.listeners.sasl.port -}}{{- end -}}
{{- printf "%s.%s.svc.%s:%v" (include "legacy-kafka.bootstrapService" .) (include "legacy-kafka.namespace" .) (.Values.clusterDomain | default "cluster.local") $port -}}
{{- end }}

{{/*
Client security flags for the topics Job and the Helm test — empty for
PLAINTEXT, a --command-config path when SASL is the only listener.
*/}}
{{- define "legacy-kafka.clientConfigArg" -}}
{{- if and .Values.listeners.sasl.enabled (not .Values.listeners.plaintext.enabled) -}}
--command-config /etc/kafka-client/client.properties
{{- end -}}
{{- end }}

{{/*
The `user_<name>="<password>"` entries a PlainLoginModule needs to ACCEPT
connections (as opposed to the username/password it uses to make them).
*/}}
{{- define "legacy-kafka.jaasUserEntries" -}}
{{- $parts := list -}}
{{- range .Values.listeners.sasl.users -}}
{{- $parts = append $parts (printf "user_%s=%q" .username .password) -}}
{{- end -}}
{{- join " " $parts -}}
{{- end }}

{{/*
Guardrails. Both of these are things that fail LATE and confusingly — a broker
that will not elect a controller, or an internal topic that cannot be created —
so they fail here instead.
*/}}
{{- define "legacy-kafka.validate" -}}
{{- if not (has (toString .Values.mode) (list "" "kraft" "zookeeper")) -}}
{{- fail (printf "legacy-kafka: mode must be 'kraft', 'zookeeper' or empty (derived from kafka.version), got %q" (toString .Values.mode)) -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+\\.[0-9]+" (toString .Values.kafka.version)) -}}
{{- fail (printf "legacy-kafka: kafka.version must look like x.y or x.y.z, got %q — the mode and the image are derived from it" (toString .Values.kafka.version)) -}}
{{- end -}}
{{- if gt (int .Values.kafka.replicationFactor) (int .Values.kafka.replicas) -}}
{{- fail (printf "legacy-kafka: kafka.replicationFactor (%v) exceeds kafka.replicas (%v) — the internal topics would never be creatable" .Values.kafka.replicationFactor .Values.kafka.replicas) -}}
{{- end -}}
{{- if and (eq (include "legacy-kafka.mode" .) "kraft") (not (semverCompare ">=3.3.0" (include "legacy-kafka.semver" .Values.kafka.version))) -}}
{{- fail (printf "legacy-kafka: mode=kraft needs Kafka >= 3.3.0, but kafka.version is %q — use mode=zookeeper, or leave mode empty and let the version decide (see values-kafka-2x.yaml)" (toString .Values.kafka.version)) -}}
{{- end -}}
{{- if and (not .Values.listeners.plaintext.enabled) (not .Values.listeners.sasl.enabled) -}}
{{- fail "legacy-kafka: at least one of listeners.plaintext.enabled / listeners.sasl.enabled must be true" -}}
{{- end -}}
{{- if and .Values.listeners.sasl.enabled (eq (len .Values.listeners.sasl.users) 0) -}}
{{- fail "legacy-kafka: listeners.sasl.enabled requires at least one entry in listeners.sasl.users" -}}
{{- end -}}
{{- end }}
