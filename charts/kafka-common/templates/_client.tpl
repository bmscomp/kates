{{/*
Bootstrap servers for a Kafka cluster entry.

Call with (dict "c" <cluster values> "ctx" $ "defaultCluster" "krafter"
"defaultNamespace" "<ns>").
- `c.bootstrapServers` wins verbatim (external or cross-cluster).
- Otherwise the in-cluster Strimzi FQDN is computed:
  <clusterName>-kafka-bootstrap.<namespace>.svc.<domain>:<port>, where the port
  is `c.listenerPort` if set, else 9093 when `c.tls.enabled`, else 9092.
The namespace default is the CALLER's to choose and document — the charts
differ on it (the release namespace, or the platform's `kafka`).
*/}}
{{- define "kafka-common.bootstrap" -}}
{{- $c := .c | default dict -}}
{{- if $c.bootstrapServers -}}
{{- $c.bootstrapServers -}}
{{- else -}}
{{- $name := $c.clusterName | default .defaultCluster | default "krafter" -}}
{{- $ns := $c.namespace | default .defaultNamespace -}}
{{- $port := 9092 -}}
{{- if ($c.tls).enabled -}}{{- $port = 9093 -}}{{- end -}}
{{- with $c.listenerPort -}}{{- $port = . -}}{{- end -}}
{{- printf "%s-kafka-bootstrap.%s.svc.%s:%v" $name $ns (include "kafka-common.clusterDomain" .ctx) $port -}}
{{- end -}}
{{- end }}

{{/*
The `tls` and `authentication` blocks of a Strimzi Kafka client (KafkaConnect
spec, a KafkaMirrorMaker2 target or source). Emits YAML at column 0.

Call with (dict "c" <cluster values> "field" "<values path, for messages>"
"chart" "<chart name>" "defaultUser" "<username>").

authentication.type:
  scram-sha-512 | scram-sha-256 | plain   username + passwordSecret
  tls                                     certificateAndKey
  custom                                  sasl + config (the v1 home of OAuth
                                          and every other SASL mechanism)
  "" (or unset)                           no authentication block
*/}}
{{- define "kafka-common.clientTlsAuth" -}}
{{- $c := .c | default dict -}}
{{- $field := .field | default "kafka" -}}
{{- $chart := .chart | default "chart" -}}
{{- if ($c.tls).enabled }}
tls:
  trustedCertificates:
    - secretName: {{ required (printf "%s: %s.tls.trustedCertificateSecret is required when %s.tls.enabled" $chart $field $field) $c.tls.trustedCertificateSecret | quote }}
      certificate: {{ $c.tls.certificateKey | default "ca.crt" }}
{{- end }}
{{- $auth := $c.authentication | default dict -}}
{{- $type := $auth.type | default "" -}}
{{- if $type }}
authentication:
  type: {{ $type }}
  {{- if has $type (list "scram-sha-512" "scram-sha-256" "plain") }}
  {{- $user := $auth.username | default .defaultUser }}
  {{- if not $user }}
  {{- fail (printf "%s: %s.authentication.type=%s needs %s.authentication.username" $chart $field $type $field) }}
  {{- end }}
  username: {{ $user | quote }}
  passwordSecret:
    secretName: {{ $auth.secretName | default $user | quote }}
    password: {{ $auth.secretKey | default "password" }}
  {{- else if eq $type "tls" }}
  certificateAndKey:
    secretName: {{ $auth.certificateSecret | default $auth.secretName | default (printf "%s-tls" ($auth.username | default .defaultUser)) | quote }}
    certificate: {{ $auth.certificate | default "user.crt" }}
    key: {{ $auth.key | default "user.key" }}
  {{- else if eq $type "custom" }}
  {{- $custom := $auth.custom | default dict }}
  {{- $cfg := $custom.config | default dict }}
  {{- $saslKeys := 0 }}{{- $keystoreKeys := 0 }}
  {{- range $k, $_ := $cfg }}
  {{- if hasPrefix "sasl." $k }}{{ $saslKeys = add1 $saslKeys }}{{ end }}
  {{- if hasPrefix "ssl.keystore." $k }}{{ $keystoreKeys = add1 $keystoreKeys }}{{ end }}
  {{- end }}
  {{- if and $custom.sasl (eq (int $saslKeys) 0) }}
  {{- fail (printf "%s: %s.authentication.custom.sasl is true but custom.config carries no sasl.* property (sasl.mechanism, sasl.jaas.config …)" $chart $field) }}
  {{- end }}
  {{- if and (not $custom.sasl) (eq (int $keystoreKeys) 0) }}
  {{- fail (printf "%s: %s.authentication.type=custom needs %s.authentication.custom — either sasl: true with sasl.* properties in config (OAuth, AWS IAM …), or ssl.keystore.* properties for a client certificate. Strimzi's custom authentication carries its whole configuration there." $chart $field $field) }}
  {{- end }}
  sasl: {{ and $custom.sasl true }}
  config:
    {{- toYaml $cfg | nindent 4 }}
  {{- else }}
  {{- fail (printf "%s: %s.authentication.type %q is not one the Strimzi v1 API accepts (scram-sha-512, scram-sha-256, plain, tls, custom)" $chart $field $type) }}
  {{- end }}
{{- end }}
{{- end }}
