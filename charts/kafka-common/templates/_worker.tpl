{{/*
The spec body shared by KafkaConnect and KafkaMirrorMaker2 — both are a Kafka
Connect worker group, and the operator reads the same fields for both. Emits
YAML at column 0; the caller adds its own kind-specific fields
(bootstrapServers/groupId/config for Connect, target/mirrors for MM2).

Call with (dict "ctx" $ "w" <worker>), where <worker> is a map the chart builds
from its own values:

  kind, name, namespace         the CR this spec belongs to
  version, image
  replicas, minReplicasDefault
  autoscaling                   {enabled, minReplicas}
  resources, jvmOptions, livenessProbe, readinessProbe, jmxOptions
  logging                       {type: inline|external, loggers, valueFrom}
  loggingConfigMap              ConfigMap the chart renders for `external`
  tracing                       {type}
  rack                          {enabled, topologyKey, clientRackInitImage}
  metrics                       {enabled, type, allowList, configMapName, configMapKey}
  podSelector                   labels selecting the worker pods
  template:
    pdb                         {enabled, maxUnavailable}
    podMetadata                 {labels, annotations}
    podSecurityContext, imagePullSecrets, priorityClassName,
    terminationGracePeriodSeconds, tolerations, dnsPolicy, dnsConfig,
    hostUsers, tmpDirSizeLimit
    nodeSelector, nodeAffinity
    podAntiAffinity             {enabled, topologyKey}
    topologySpread              {enabled, maxSkew, topologyKey, whenUnsatisfiable, additional}
    container                   {env, securityContext, volumeMounts, harden, extra}
    serviceAccount              ServiceAccount template (metadata)
    extra                       raw spec.template, deep-merged last

Why these are built as maps and not written out: a pass-through written next
to a chart-owned key produces a duplicate key, and YAML keeps the last one —
which is how mirror-maker2 0.7 lost its whole pod template to a
templateExtra.pod. Everything below is merged, then emitted once.
*/}}
{{- define "kafka-common.connectWorker.spec" -}}
{{- $ctx := .ctx -}}
{{- $w := .w -}}
{{- $as := $w.autoscaling | default dict -}}
{{- $minDefault := $w.minReplicasDefault | default 1 -}}
{{- /* replicas is REQUIRED by the v1 CRDs of both kinds. Under an HPA it is
       seeded from minReplicas on install and read back from the live CR on
       upgrade, so `helm upgrade` never scales a busy group back down.
       `lookup` is empty under `helm template` and on a fresh install. */ -}}
{{- $replicas := $w.replicas -}}
{{- if include "kafka-common.enabled" (list $as.enabled false) -}}
{{- $replicas = $as.minReplicas | default $minDefault -}}
{{- $live := lookup "kafka.strimzi.io/v1" $w.kind $w.namespace $w.name -}}
{{- if and $live $live.spec $live.spec.replicas -}}
{{- $replicas = $live.spec.replicas -}}
{{- end -}}
{{- end -}}
version: {{ $w.version | quote }}
{{- with $w.image }}
image: {{ . | quote }}
{{- end }}
replicas: {{ $replicas }}
{{- with $w.resources }}
resources:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $w.jvmOptions }}
jvmOptions:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $w.livenessProbe }}
livenessProbe:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $w.readinessProbe }}
readinessProbe:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- /* Two logging shapes. `inline` passes {type, loggers} through as the CRD
       expects. `external` points at the ConfigMap the chart renders unless a
       valueFrom is given. Chart-only keys never reach spec.logging, where the
       API server would prune them and Strimzi would then reject an external
       block with no valueFrom. */}}
{{- with $w.logging }}
{{- if eq (.type | default "") "external" }}
logging:
  type: external
  valueFrom:
    {{- if .valueFrom }}
    {{- toYaml .valueFrom | nindent 4 }}
    {{- else }}
    configMapKeyRef:
      name: {{ required "kafka-common.connectWorker.spec: loggingConfigMap is required for external logging" $w.loggingConfigMap }}
      key: log4j2.properties
    {{- end }}
{{- else if .type }}
logging:
  type: {{ .type }}
  {{- with .loggers }}
  loggers:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
{{- end }}
{{- with ($w.tracing | default dict).type }}
tracing:
  type: {{ . }}
{{- end }}
{{- $rack := $w.rack | default dict }}
{{- if $rack.enabled }}
rack:
  topologyKey: {{ $rack.topologyKey | default "topology.kubernetes.io/zone" }}
{{- /* The operator reads the node's topology label with an init container;
       on a mirrored or air-gapped registry its image needs naming. */}}
{{- with $rack.clientRackInitImage }}
clientRackInitImage: {{ . | quote }}
{{- end }}
{{- end }}
{{- with $w.jmxOptions }}
jmxOptions:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- $m := $w.metrics | default dict }}
{{- if include "kafka-common.enabled" (list $m.enabled true) }}
metricsConfig:
  {{- if eq ($m.type | default "jmxPrometheusExporter") "strimziMetricsReporter" }}
  {{- /* The operator's native path: a MetricsReporter inside the worker
         publishes the registry directly — no exporter agent, no ConfigMap. */}}
  type: strimziMetricsReporter
  values:
    allowList:
      {{- toYaml ($m.allowList | default (list "kafka_connect.*" "kafka_producer.*" "kafka_consumer.*")) | nindent 6 }}
  {{- else }}
  type: jmxPrometheusExporter
  valueFrom:
    configMapKeyRef:
      name: {{ required "kafka-common.connectWorker.spec: metrics.configMapName is required for the JMX exporter" $m.configMapName }}
      key: {{ $m.configMapKey | default "metrics-config.yml" }}
  {{- end }}
{{- end }}
{{- with (include "kafka-common.connectWorker.template" (dict "ctx" $ctx "w" $w "replicas" $replicas) | fromYaml) }}
template:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}

{{/*
spec.template for a Connect worker group, as YAML — see connectWorker.spec.
*/}}
{{- define "kafka-common.connectWorker.template" -}}
{{- $w := .w -}}
{{- $t := $w.template | default dict -}}
{{- $as := $w.autoscaling | default dict -}}
{{- $tpl := dict -}}
{{- /* A PDB only where more than one worker will actually run (under an HPA,
       at least minReplicas). Strimzi owns the PDB for its pods; setting
       maxUnavailable here tunes that one instead of adding a second. */ -}}
{{- $hpa := eq (include "kafka-common.enabled" (list $as.enabled false)) "true" -}}
{{- $effective := ternary ($as.minReplicas | default ($w.minReplicasDefault | default 1)) ($w.replicas | default 1) $hpa -}}
{{- $pdb := $t.pdb | default dict -}}
{{- if and (include "kafka-common.enabled" (list $pdb.enabled true)) (gt (int $effective) 1) -}}
{{- $_ := set $tpl "podDisruptionBudget" (dict "maxUnavailable" (ternary $pdb.maxUnavailable 1 (hasKey $pdb "maxUnavailable"))) -}}
{{- end -}}
{{- $pod := dict -}}
{{- $meta := dict -}}
{{- with ($t.podMetadata | default dict).labels }}{{- $_ := set $meta "labels" . }}{{- end -}}
{{- with ($t.podMetadata | default dict).annotations }}{{- $_ := set $meta "annotations" . }}{{- end -}}
{{- with $meta }}{{- $_ := set $pod "metadata" . }}{{- end -}}
{{- with $t.podSecurityContext }}{{- $_ := set $pod "securityContext" . }}{{- end -}}
{{- with $t.imagePullSecrets }}{{- $_ := set $pod "imagePullSecrets" . }}{{- end -}}
{{- with $t.priorityClassName }}{{- $_ := set $pod "priorityClassName" . }}{{- end -}}
{{- with $t.terminationGracePeriodSeconds }}{{- $_ := set $pod "terminationGracePeriodSeconds" (int .) }}{{- end -}}
{{- with $t.tolerations }}{{- $_ := set $pod "tolerations" . }}{{- end -}}
{{- with $t.dnsPolicy }}{{- $_ := set $pod "dnsPolicy" . }}{{- end -}}
{{- with $t.dnsConfig }}{{- $_ := set $pod "dnsConfig" . }}{{- end -}}
{{- if hasKey $t "hostUsers" }}{{- if not (kindIs "invalid" $t.hostUsers) }}{{- $_ := set $pod "hostUsers" $t.hostUsers }}{{- end }}{{- end -}}
{{- with $t.tmpDirSizeLimit }}{{- $_ := set $pod "tmpDirSizeLimit" . }}{{- end -}}
{{- /* Strimzi's pod template has no nodeSelector (the field is pruned), so a
       nodeSelector becomes a required node-affinity term, ANDed into every
       term nodeAffinity already requires. */ -}}
{{- $affinity := dict -}}
{{- $nodeAffinity := deepCopy ($t.nodeAffinity | default dict) -}}
{{- if $t.nodeSelector -}}
{{- $exprs := list -}}
{{- range $k, $v := $t.nodeSelector -}}
{{- $exprs = append $exprs (dict "key" $k "operator" "In" "values" (list (toString $v))) -}}
{{- end -}}
{{- $required := $nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution | default dict -}}
{{- $terms := $required.nodeSelectorTerms | default (list (dict)) -}}
{{- $merged := list -}}
{{- range $terms -}}
{{- $merged = append $merged (merge (dict "matchExpressions" (concat (.matchExpressions | default list) $exprs)) (omit . "matchExpressions")) -}}
{{- end -}}
{{- $_ := set $nodeAffinity "requiredDuringSchedulingIgnoredDuringExecution" (dict "nodeSelectorTerms" $merged) -}}
{{- end -}}
{{- with $nodeAffinity }}{{- $_ := set $affinity "nodeAffinity" . }}{{- end -}}
{{- $anti := $t.podAntiAffinity | default dict -}}
{{- if $anti.enabled -}}
{{- $term := dict "labelSelector" (dict "matchLabels" $w.podSelector) "topologyKey" ($anti.topologyKey | default "kubernetes.io/hostname") -}}
{{- $_ := set $affinity "podAntiAffinity" (dict "preferredDuringSchedulingIgnoredDuringExecution" (list (dict "weight" 100 "podAffinityTerm" $term))) -}}
{{- end -}}
{{- with $affinity }}{{- $_ := set $pod "affinity" . }}{{- end -}}
{{- $spread := $t.topologySpread | default dict -}}
{{- if $spread.enabled -}}
{{- $tsc := list (dict
      "maxSkew" (int ($spread.maxSkew | default 1))
      "topologyKey" ($spread.topologyKey | default "topology.kubernetes.io/zone")
      "whenUnsatisfiable" ($spread.whenUnsatisfiable | default "ScheduleAnyway")
      "labelSelector" (dict "matchLabels" $w.podSelector)) -}}
{{- $_ := set $pod "topologySpreadConstraints" (concat $tsc ($spread.additional | default list)) -}}
{{- end -}}
{{- with $pod }}{{- $_ := set $tpl "pod" . }}{{- end -}}
{{- /* The worker container: env, volume mounts, and a hardened security
       context under whatever the chart's pass-through sets. */ -}}
{{- $c := $t.container | default dict -}}
{{- $cont := dict -}}
{{- with $c.env }}{{- $_ := set $cont "env" . }}{{- end -}}
{{- with $c.volumeMounts }}{{- $_ := set $cont "volumeMounts" . }}{{- end -}}
{{- $sc := dict -}}
{{- if $c.harden -}}
{{- $sc = dict "allowPrivilegeEscalation" false "readOnlyRootFilesystem" true "capabilities" (dict "drop" (list "ALL")) -}}
{{- end -}}
{{- $sc = mustMergeOverwrite $sc (deepCopy ($c.securityContext | default dict)) -}}
{{- with $sc }}{{- $_ := set $cont "securityContext" . }}{{- end -}}
{{- $cont = mustMergeOverwrite $cont (deepCopy ($c.extra | default dict)) -}}
{{- with $cont }}{{- $_ := set $tpl "connectContainer" . }}{{- end -}}
{{- with $t.serviceAccount }}{{- $_ := set $tpl "serviceAccount" . }}{{- end -}}
{{- $tpl = mustMergeOverwrite $tpl (deepCopy ($t.extra | default dict)) -}}
{{- if $tpl }}{{ toYaml $tpl }}{{ end -}}
{{- end }}
