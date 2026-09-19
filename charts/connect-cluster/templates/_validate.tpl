{{/*
Render-time rails. Each names the setting and the way out. Connector-level
checks live in connect-cluster.resolveConnector; this is everything else.
Included once, from kafka-connect.yaml.
*/}}
{{- define "connect-cluster.validate" -}}
{{- $v := .Values -}}
{{- $r := include "connect-cluster.resolve" . | fromJson -}}

{{- /* Floating tags on every image the chart names. */ -}}
{{- $images := dict "image" $v.image "testImages.kubectl" $v.testImages.kubectl "secretSync.image" (($v.secretSync | default dict).image) "preflight.image" (($v.preflight | default dict).image) "rack.clientRackInitImage" (($v.rack | default dict).clientRackInitImage) -}}
{{- range $p := ($v.plugins | default list) -}}
{{- range $i, $a := ($p.artifacts | default list) -}}
{{- $_ := set $images (printf "plugins[%s].artifacts[%d].reference" $p.name $i) $a.reference -}}
{{- end -}}
{{- end -}}
{{- include "kafka-common.rails.noFloatingTag" (dict "chart" "connect-cluster" "images" $images) -}}

{{- /* Kafka connection. */ -}}
{{- $k := $v.kafka -}}
{{- $tlsOn := eq (include "kafka-common.enabled" (list ($k.tls | default dict).enabled false)) "true" -}}
{{- $type := ($k.authentication | default dict).type | default "" -}}
{{- if and (eq $type "tls") (not $tlsOn) -}}
{{- fail "connect-cluster: kafka.authentication.type=tls is mutual TLS, which needs kafka.tls.enabled=true (and the TLS listener)." -}}
{{- end -}}
{{- if and $tlsOn (has $type (list "scram-sha-512" "scram-sha-256" "plain")) (not $k.bootstrapServers) (or (not $k.listenerPort) (eq (toString $k.listenerPort) "9093")) -}}
{{- fail (printf "connect-cluster: kafka.tls.enabled=true points the workers at port 9093, which kafka-cluster serves with mutual-TLS authentication, but kafka.authentication.type is %s. Use kafka.authentication.type=tls (kafkaUser.create issues the certificate), or name a TLS listener that takes %s with kafka.listenerPort or kafka.bootstrapServers." $type $type) -}}
{{- end -}}
{{- if and $v.productionMode (not $tlsOn) -}}
{{- fail "connect-cluster: productionMode refuses an unencrypted Kafka connection — connector data and SASL exchanges would cross the network in the clear. Set kafka.tls.enabled=true (values-prod.yaml uses the TLS listener with kafka.authentication.type=tls)." -}}
{{- end -}}
{{- if and $v.productionMode (eq $type "") -}}
{{- fail "connect-cluster: productionMode refuses an unauthenticated Kafka connection (kafka.authentication.type is empty)." -}}
{{- end -}}

{{- /* Worker configuration. */ -}}
{{- if hasKey ($v.extraConfig | default dict) "exactly.once.source.support" -}}
{{- fail "connect-cluster: extraConfig sets exactly.once.source.support. It is exactlyOnce.enabled now: the switch also decides the transactional-ID grants of a managed KafkaUser, and every worker of the group must agree. Remove it from extraConfig." -}}
{{- end -}}
{{- $topics := include "connect-cluster.internalTopics" . | fromYaml -}}
{{- if or (eq $topics.offsets $topics.configs) (eq $topics.offsets $topics.status) (eq $topics.configs $topics.status) -}}
{{- fail (printf "connect-cluster: the internal topics must be three different topics; internalTopics gives offsets=%s configs=%s status=%s" $topics.offsets $topics.configs $topics.status) -}}
{{- end -}}
{{- with $k.brokerCount -}}
{{- $rf := int ($v.config.replicationFactor | default 3) -}}
{{- if gt $rf (int .) -}}
{{- fail (printf "connect-cluster: config.replicationFactor is %d but kafka.brokerCount is %d — the workers could not create their internal topics. Lower it to %d or fewer." $rf (int .) (int .)) -}}
{{- end -}}
{{- range $r.connectors -}}
{{- if and .deadLetterQueue (gt (int .deadLetterQueue.replicationFactor) (int $k.brokerCount)) -}}
{{- fail (printf "connect-cluster: connectors.%s.deadLetterQueue.replicationFactor is %d but kafka.brokerCount is %d" .name (int .deadLetterQueue.replicationFactor) (int $k.brokerCount)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- /* Connectors. */ -}}
{{- $names := list -}}
{{- $sum := 0 -}}
{{- range $r.connectors -}}
{{- if has .name $names -}}
{{- fail (printf "connect-cluster: connector %s is declared twice" .name) -}}
{{- end -}}
{{- $names = append $names .name -}}
{{- $sum = add $sum .tasksMax -}}
{{- end -}}
{{- $as := $v.autoscaling | default dict -}}
{{- if and (eq (include "kafka-common.enabled" (list $as.enabled false)) "true") $r.connectors (not $as.allowIdleWorkers) (gt (int ($as.maxReplicas | default 10)) (int $sum)) -}}
{{- fail (printf "connect-cluster: autoscaling.maxReplicas is %d, but the connectors declared here run at most %d task(s) in total. Connect gives a worker nothing to do beyond that, so the extra replicas idle while the CPU average that scaled them falls. Raise tasksMax, lower maxReplicas, or set autoscaling.allowIdleWorkers=true." (int ($as.maxReplicas | default 10)) (int $sum)) -}}
{{- end -}}

{{- /* Database egress: each entry also names an ingress policy in the
       database namespace after its port, so two entries for one namespace
       and port would collide. */ -}}
{{- $seen := list -}}
{{- range $r.databases -}}
{{- $key := printf "%s/%v" .namespace (.port | default 5432) -}}
{{- if has $key $seen -}}
{{- fail (printf "connect-cluster: networkPolicy.egress.databases lists %s twice. Merge the entries (one podSelector per namespace and port), or add the second flow with networkPolicy.extraEgress." $key) -}}
{{- end -}}
{{- $seen = append $seen $key -}}
{{- end -}}

{{- /* Secrets. */ -}}
{{- if and $v.productionMode ($v.rbac | default dict).allSecrets -}}
{{- fail "connect-cluster: productionMode refuses rbac.allSecrets — it lets every connector read every Secret in the release and Kafka namespaces, CA keys and other users' passwords included. The chart grants the Secrets connector configs reference; list others in rbac.secretNames." -}}
{{- end -}}

{{- /* Plugins. */ -}}
{{- if $v.plugins -}}
{{- if not (($v.imageVolumes | default dict).acknowledged) -}}
{{- fail "connect-cluster: plugins mounts OCI images as volumes, which needs the Kubernetes ImageVolume feature on every node a worker may run on (alpha in 1.31, beta from 1.33). The chart cannot see a feature gate. Check it, then set imageVolumes.acknowledged=true — or bake the plugins into `image`." -}}
{{- end -}}
{{- if semverCompare "<1.31.0-0" .Capabilities.KubeVersion.Version -}}
{{- fail (printf "connect-cluster: plugins needs the ImageVolume feature, which Kubernetes has from 1.31; this cluster is %s. Bake the plugins into `image` instead." .Capabilities.KubeVersion.Version) -}}
{{- end -}}
{{- end -}}

{{- /* Observability. */ -}}
{{- if $v.alerts.enabled -}}
{{- if not $r.metrics.enabled -}}
{{- fail "connect-cluster: alerts.enabled needs metrics.enabled — every rule reads the workers' metrics. Turn both on, or alerts off." -}}
{{- end -}}
{{- if and (eq $r.metrics.type "strimziMetricsReporter") (not $v.alerts.allowReporterMetrics) -}}
{{- fail "connect-cluster: the alerts read the JMX exporter's metric names, which metrics.type=strimziMetricsReporter does not publish, so they would never fire. Use jmxPrometheusExporter, or set alerts.allowReporterMetrics=true after checking the names in your Prometheus." -}}
{{- end -}}
{{- end -}}
{{- end }}
