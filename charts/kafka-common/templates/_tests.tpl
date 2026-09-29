{{/*
Shell lines that WRITE a Kafka client.properties for a cluster entry, for test
and preflight pods. Call with (dict "c" <cluster> "env" "<ENV VAR holding the
password>" "file" "<path>").

`echo`/`printf` lines rather than a heredoc: a heredoc rendered through nindent
has its terminator indented too, and an indented terminator does not close it
(`<<-` strips tabs, never spaces), so the rest of the script would be
swallowed and the pod would exit 0 having done nothing. The JAAS line uses
printf, because dash's echo interprets backslashes, and the password is
escaped for the JAAS string. Scope: SASL over plaintext (none / plain /
scram-*). Callers check `test.authUnsupported` first.
*/}}
{{- define "kafka-common.test.clientProps" -}}
{{- $c := .c -}}{{- $envVar := .env -}}{{- $file := .file -}}
{{- $auth := $c.authentication | default dict -}}
{{- $t := $auth.type | default "" -}}
{{- if eq $t "" }}
echo 'security.protocol=PLAINTEXT' > {{ $file }}
{{- else if eq $t "plain" }}
ESC_{{ $envVar }}=$(printf '%s' "${{ $envVar }}" | sed 's/[\\"]/\\&/g')
echo 'security.protocol=SASL_PLAINTEXT' > {{ $file }}
echo 'sasl.mechanism=PLAIN' >> {{ $file }}
printf '%s\n' "sasl.jaas.config=org.apache.kafka.common.security.plain.PlainLoginModule required username=\"{{ $auth.username }}\" password=\"$ESC_{{ $envVar }}\";" >> {{ $file }}
{{- else }}
ESC_{{ $envVar }}=$(printf '%s' "${{ $envVar }}" | sed 's/[\\"]/\\&/g')
echo 'security.protocol=SASL_PLAINTEXT' > {{ $file }}
echo 'sasl.mechanism={{ upper $t }}' >> {{ $file }}
printf '%s\n' "sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username=\"{{ $auth.username }}\" password=\"$ESC_{{ $envVar }}\";" >> {{ $file }}
{{- end }}
{{- end }}

{{/*
True when a cluster entry is beyond what the test pods can configure: TLS,
mutual TLS, or custom (OAuth and friends) authentication.
*/}}
{{- define "kafka-common.test.authUnsupported" -}}
{{- $c := .c -}}
{{- $auth := $c.authentication | default dict -}}
{{- if or (($c.tls | default dict).enabled) (has ($auth.type | default "") (list "tls" "custom")) -}}true{{- end -}}
{{- end }}
