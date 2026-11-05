{{- define "kates-operator.fullname" -}}
{{- printf "kates-operator-%s" .Release.Namespace | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kates-operator.selectorLabels" -}}
app.kubernetes.io/name: kates-operator
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kates-operator.labels" -}}
{{ include "kates-operator.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "kates-operator.image" -}}
{{- $image := printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- with .Values.image.digest -}}{{- $image = printf "%s@%s" $.Values.image.repository . -}}{{- end -}}
{{- with .Values.global.imageRegistry -}}{{ printf "%s/%s" (trimSuffix "/" .) $image }}{{- else -}}{{ $image }}{{- end -}}
{{- end -}}

{{- define "kates-operator.imagePullSecrets" -}}
{{- $secrets := concat (.Values.global.imagePullSecrets | default list) (.Values.imagePullSecrets | default list) -}}
{{- with $secrets }}
imagePullSecrets:
  {{- range . }}
  - name: {{ if kindIs "string" . }}{{ . }}{{ else }}{{ .name }}{{ end }}
  {{- end }}
{{- end }}
{{- end -}}

{{/* The namespaces this release reconciles, or * for all of them. */}}
{{- define "kates-operator.namespaces" -}}
{{- if .Values.watchAnyNamespace -}}*
{{- else if .Values.watchNamespaces -}}{{ join "," .Values.watchNamespaces }}
{{- else -}}{{ .Release.Namespace }}
{{- end -}}
{{- end -}}

{{/* What the operator may do where it reconciles. */}}
{{- define "kates-operator.rules" -}}
- apiGroups: ["kates.io"]
  resources: ["katestests", "kafkacompliances"]
  verbs: ["get", "list", "watch", "patch", "update", "delete"]
- apiGroups: ["kates.io"]
  resources: ["katestests/status", "kafkacompliances/status", "katestests/finalizers", "kafkacompliances/finalizers"]
  verbs: ["get", "patch", "update"]
- apiGroups: ["events.k8s.io"]
  resources: ["events"]
  verbs: ["create", "patch"]
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get"]
  resourceNames: ["kates-connection"]
{{- end -}}

{{- define "kates-operator.podSecurityContext" -}}
runAsNonRoot: true
runAsUser: 65532
runAsGroup: 65532
seccompProfile:
  type: RuntimeDefault
{{- end -}}

{{- define "kates-operator.securityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop:
    - ALL
{{- end -}}
