{{/*
Expand the name of the chart.
*/}}
{{- define "kates-platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "kates-platform.labels" -}}
helm.sh/chart: {{ include "kates-platform.name" . }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: kates-platform
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}

{{/*
imagePullSecrets for the umbrella's own pod: global.imagePullSecrets, which
the subcharts read too, then imagePullSecrets. An entry is a name or
{name: …}, as kafka-cluster takes global.imagePullSecrets.
*/}}
{{- define "kates-platform.imagePullSecrets" -}}
{{- $names := list }}
{{- range concat ((.Values.global | default dict).imagePullSecrets | default list) (.Values.imagePullSecrets | default list) }}
{{- if kindIs "map" . }}{{ $names = append $names .name }}{{ else }}{{ $names = append $names . }}{{ end }}
{{- end }}
{{- with $names | uniq -}}
imagePullSecrets:
  {{- range . }}
  - name: {{ . | quote }}
  {{- end }}
{{- end }}
{{- end }}
