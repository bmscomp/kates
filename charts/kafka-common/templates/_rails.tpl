{{/*
Refuse floating image tags. A worker or helper pinned to `:latest` (or to no
tag at all) changes what runs whenever a pod restarts — including the Kafka
client version the charts' compatibility rails exist to hold still.

Call with (dict "chart" "<chart name>" "images" (dict "<values path>" "<image>" …)).
The tag is what follows the last colon of the last path segment, so a
registry port (registry:5000/kafka) is not mistaken for one; a digest
reference (…@sha256:…) is always accepted; an empty image is skipped.
*/}}
{{- define "kafka-common.rails.noFloatingTag" -}}
{{- $chart := .chart -}}
{{- range $field, $img := .images -}}
{{- if $img -}}
{{- $lastSegment := $img | splitList "/" | last -}}
{{- $untagged := and (not (contains "@" $img)) (not (contains ":" $lastSegment)) -}}
{{- if or (hasSuffix ":latest" $img) $untagged -}}
{{- fail (printf "%s: %s is %q, which resolves to a floating tag. What runs then depends on when a pod last restarted rather than on anything recorded here — for a Kafka image that is the client version itself. Use an explicit version tag, or better a @sha256: digest." $chart $field $img) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end }}
