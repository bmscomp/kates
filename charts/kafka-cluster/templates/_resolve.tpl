{{/*
kafka-cluster.resolve — the values every template renders from.

It applies, once: the profile (profiles/<name>.yaml, merged under the values),
the 0.4 compatibility shim (legacy keys translated, a DEPRECATED line recorded
for NOTES), the defaults that depend on other values (topic replication from
the broker count, policy ports from the listeners), and the rails.

Call with (dict "ctx" $ "out" <empty dict>) and ignore the output; the result
is written into "out":

  listeners      the Kafka listeners, externalAccess included
  superUsers     kafka.authorization.superUsers ∪ the profile's
  pools          fully merged node pools (see kafka-cluster.resolvePool)
  brokers        brokers the pools render      controllers   likewise
  tiered         tiered storage is enabled (and passed its rails)
  topics         [{name, spec}] sorted by name, disabled ones dropped
  users          [{name, …}]   sorted by name, disabled ones dropped
  netpol         the normalised networkPolicy block (clients resolved)
  metrics        {enabled, type, allowList}
  podMonitor     {enabled, labels, interval, scrapeTimeout}
  kyverno        the kyvernoPolicy block
  backupVolumes  fs-backup | snapshot | none
  deprecations   [string]
*/}}
{{- define "kafka-cluster.resolve" -}}
{{- $ctx := .ctx -}}
{{- $v := $ctx.Values -}}
{{- $r := .out -}}
{{- $_ := set $r "deprecations" list -}}

{{- /* ── Profile ─────────────────────────────────────────────────────── */ -}}
{{- $prof := dict -}}
{{- with $v.profile -}}
{{- $raw := $ctx.Files.Get (printf "profiles/%s.yaml" .) -}}
{{- if not $raw -}}
{{- fail (printf "kafka-cluster: profile %q does not exist. The chart ships: platform (or leave profile empty)." .) -}}
{{- end -}}
{{- $prof = $raw | fromYaml -}}
{{- end -}}
{{- $_ := set $r "profile" $prof -}}

{{- include "kafka-cluster.resolve.compat" (dict "ctx" $ctx "r" $r) -}}
{{- include "kafka-cluster.resolve.listeners" (dict "ctx" $ctx "r" $r) -}}

{{- $su := list -}}
{{- range (concat (((($prof.kafka).authorization).superUsers) | default list) ((($v.kafka.authorization).superUsers) | default list)) -}}
{{- $su = append $su . -}}
{{- end -}}
{{- $_ := set $r "superUsers" ($su | uniq) -}}

{{- include "kafka-cluster.resolve.pools" (dict "ctx" $ctx "r" $r) -}}
{{- $_ := set $r "tiered" (include "kafka-common.enabled" (list $v.tieredStorage.enabled false) | eq "true") -}}
{{- include "kafka-cluster.resolve.topics" (dict "ctx" $ctx "r" $r) -}}
{{- include "kafka-cluster.resolve.users" (dict "ctx" $ctx "r" $r) -}}
{{- include "kafka-cluster.resolve.netpol" (dict "ctx" $ctx "r" $r) -}}
{{- include "kafka-cluster.rails" (dict "ctx" $ctx "r" $r) -}}
{{- end }}

{{/*
Apply a translated 0.4 value, unless the 1.0 key was set too. Values files
layer without telling a template which layer set what, so "set" means
"differs from the chart default": `kates detect` writes 0.4 keys into the
lowest layer, and an overlay above it that sets the 1.0 key must win.
Call with (dict "dst" <map> "key" <k> "value" <legacy value> "default" <chart default>).
*/}}
{{- define "kafka-cluster.legacySet" -}}
{{- $cur := index .dst .key -}}
{{- if or (kindIs "invalid" $cur) (eq (toJson $cur) (toJson .default)) -}}
{{- $_ := set .dst .key .value -}}
{{- end -}}
{{- end }}

{{/* Record a deprecation line. Call with (dict "r" $r "msg" "..."). */}}
{{- define "kafka-cluster.deprecate" -}}
{{- $_ := set .r "deprecations" (append .r.deprecations .msg) -}}
{{- end }}

{{/*
The 0.4 keys that are not pools, topics, users or network policy: translated
where 1.0 has an equivalent, refused where the feature moved, noted where the
key never did anything.
*/}}
{{- define "kafka-cluster.resolve.compat" -}}
{{- $v := .ctx.Values -}}
{{- $r := .r -}}

{{- /* Metrics format */ -}}
{{- $metrics := deepCopy ($v.metrics | default dict) -}}
{{- with (($v.kafka).metricsConfig) -}}
{{- if and .type (not $metrics.type) -}}{{- $_ := set $metrics "type" .type -}}{{- end -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "kafka.metricsConfig is metrics.type (and metrics.enabled, metrics.allowList)") -}}
{{- end -}}
{{- $_ := set $metrics "enabled" (include "kafka-common.enabled" (list $metrics.enabled true) | eq "true") -}}
{{- $_ := set $metrics "type" ($metrics.type | default "jmxPrometheusExporter") -}}
{{- $_ := set $r "metrics" $metrics -}}

{{- /* Scrape */ -}}
{{- $pm := deepCopy ((($v.monitoring).podMonitor) | default dict) -}}
{{- with $v.podMonitors -}}
{{- if hasKey . "enabled" -}}{{- include "kafka-cluster.legacySet" (dict "dst" $pm "key" "enabled" "value" .enabled "default" true) -}}{{- end -}}
{{- if .labels -}}{{- include "kafka-cluster.legacySet" (dict "dst" $pm "key" "labels" "value" .labels "default" (dict "release" "kafka")) -}}{{- end -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "podMonitors is monitoring.podMonitor") -}}
{{- end -}}
{{- $_ := set $pm "enabled" (include "kafka-common.enabled" (list $pm.enabled true) | eq "true") -}}
{{- $_ := set $r "podMonitor" $pm -}}

{{- /* Kyverno */ -}}
{{- $ky := deepCopy ($v.kyvernoPolicy | default dict) -}}
{{- with $v.podSecurityPolicy -}}
{{- range $k, $d := (dict "enabled" false "action" "Audit" "mutate" false "excludeStrimziPods" true) -}}
{{- if hasKey $v.podSecurityPolicy $k -}}
{{- include "kafka-cluster.legacySet" (dict "dst" $ky "key" $k "value" (index $v.podSecurityPolicy $k) "default" $d) -}}
{{- end -}}
{{- end -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "podSecurityPolicy is kyvernoPolicy (the ClusterPolicy is now named per cluster)") -}}
{{- end -}}
{{- $_ := set $ky "enabled" (include "kafka-common.enabled" (list $ky.enabled false) | eq "true") -}}
{{- $_ := set $r "kyverno" $ky -}}

{{- /* Backup volume data */ -}}
{{- $bv := ($v.backup).volumes | default "" -}}
{{- if or (hasKey ($v.backup | default dict) "snapshotVolumes") (hasKey ($v.backup | default dict) "defaultVolumesToFsBackup") -}}
{{- $legacy := "none" -}}
{{- if include "kafka-common.enabled" (list $v.backup.defaultVolumesToFsBackup false) -}}{{- $legacy = "fs-backup" -}}
{{- else if include "kafka-common.enabled" (list $v.backup.snapshotVolumes false) -}}{{- $legacy = "snapshot" -}}
{{- end -}}
{{- /* A value set for backup.volumes always wins: `volumes` defaults to
       empty exactly so that an overlay asking for fs-backup is not turned
       back into "none" by the two keys a 0.4 release carries. */ -}}
{{- if not $bv -}}{{- $bv = $legacy -}}{{- end -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" (printf "backup.snapshotVolumes/defaultVolumesToFsBackup are backup.volumes (read as %q)" $bv)) -}}
{{- end -}}
{{- if not $bv -}}{{- $bv = "fs-backup" -}}{{- end -}}
{{- if not (has $bv (list "fs-backup" "snapshot" "none")) -}}
{{- fail (printf "kafka-cluster: backup.volumes is %q; use fs-backup, snapshot or none." $bv) -}}
{{- end -}}
{{- $_ := set $r "backupVolumes" $bv -}}

{{- /* Tiered storage, 0.4 shape: the keys of a ConfigMap nothing read. */ -}}
{{- $ts := $v.tieredStorage | default dict -}}
{{- $rsm := dict -}}
{{- $tsKafka := dict -}}
{{- with $ts.s3 -}}
{{- range $old, $new := (dict "bucketName" "storage.s3.bucket.name" "region" "storage.s3.region" "endpointUrl" "storage.s3.endpoint.url" "pathStyleAccessEnabled" "storage.s3.path.style.access.enabled") -}}
{{- if and (hasKey $ts.s3 $old) (not (eq (toString (index $ts.s3 $old)) "")) -}}{{- $_ := set $rsm $new (toString (index $ts.s3 $old)) -}}{{- end -}}
{{- end -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "tieredStorage.s3 is tieredStorage.remoteStorageManager.config (translated to storage.s3.*, the Aiven S3 plugin's keys)") -}}
{{- end -}}
{{- with $ts.retention -}}
{{- with .localRetentionMs -}}{{- $_ := set $tsKafka "log.local.retention.ms" (int64 .) -}}{{- end -}}
{{- with .taskIntervalMs -}}{{- $_ := set $tsKafka "remote.log.manager.task.interval.ms" (int64 .) -}}{{- end -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "tieredStorage.retention is tieredStorage.localRetentionMs (and kafka.config)") -}}
{{- end -}}
{{- with (($ts.metadata).partitions) -}}
{{- $_ := set $tsKafka "rlmm.config.remote.log.metadata.topic.partitions" (int64 .) -}}
{{- end -}}
{{- if or (($ts.credentials).accessKeyId) (($ts.credentials).secretAccessKey) -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "tieredStorage.credentials.accessKeyId/secretAccessKey are not rendered into a Secret any more; use credentials.existingSecret") -}}
{{- end -}}
{{- $_ := set $r "legacyRsmConfig" $rsm -}}
{{- $_ := set $r "legacyTieredKafkaConfig" $tsKafka -}}

{{- /* Moved to strimzi-operator */ -}}
{{- if include "kafka-common.enabled" (list (($v.drainCleaner).enabled) false) -}}
{{- fail "kafka-cluster: drainCleaner moved to the strimzi-operator chart in kafka-cluster 1.0 — one webhook serves every cluster, and its cluster-scoped objects cannot belong to a per-cluster release. Enable it there (drainCleaner.enabled on strimzi-operator 0.3+) after upgrading this release with drainCleaner.enabled=false, which removes the old copy. See docs/kafka-cluster-1.0-upgrade.md." -}}
{{- end -}}
{{- if hasKey $v "drainCleaner" -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "drainCleaner is ignored: the drain cleaner is part of the strimzi-operator chart") -}}
{{- end -}}
{{- if include "kafka-common.enabled" (list (($v.crdUpgrade).enabled) false) -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "crdUpgrade.enabled does nothing: the strimzi-operator chart owns the Strimzi CRDs (its crdUpgrade hook)") -}}
{{- end -}}
{{- if include "kafka-common.enabled" (list (($v.dashboards).enabled) false) -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "dashboards.enabled does nothing here: the Kafka, KRaft, Cruise Control and Kafka Exporter dashboards are the operator's own, enabled with strimzi-kafka-operator.dashboards.enabled on the strimzi-operator chart") -}}
{{- end -}}

{{- /* Keys that never reached a resource */ -}}
{{- range $key, $msg := (dict
      "zookeeper" "zookeeper is ignored: Strimzi 1.x runs KRaft only"
      "strimziOperator" "strimziOperator is ignored: install the operator with the strimzi-operator chart"
      "strimzi-kafka-operator" "strimzi-kafka-operator is ignored here: those values belong to the strimzi-operator chart"
      "kafkaConnect" "kafkaConnect is ignored: Kafka Connect is the connect-cluster chart"
      "lifecycle" "lifecycle.preStopSleepSeconds never reached a pod; set nodePools.defaults.scheduling.terminationGracePeriodSeconds") -}}
{{- if hasKey $v $key -}}{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" $msg) -}}{{- end -}}
{{- end -}}
{{- if hasKey ($v.kafka | default dict) "replicas" -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "kafka.replicas is ignored: size the cluster with nodePools") -}}
{{- end -}}
{{- if hasKey ($v.testImages | default dict) "bash" -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "testImages.bash is ignored: no test uses it") -}}
{{- end -}}
{{- end }}

{{/* Listeners: kafka.listeners plus the externalAccess preset. */}}
{{- define "kafka-cluster.resolve.listeners" -}}
{{- $v := .ctx.Values -}}
{{- $r := .r -}}
{{- $ls := list -}}
{{- range ($v.kafka.listeners | default list) -}}
{{- $ls = append $ls (deepCopy .) -}}
{{- end -}}
{{- $ea := $v.kafka.externalAccess | default dict -}}
{{- $type := $ea.type | default "none" -}}
{{- if ne $type "none" -}}
{{- if not (has $type (list "nodeport" "loadbalancer" "ingress")) -}}
{{- fail (printf "kafka-cluster: kafka.externalAccess.type is %q; use none, nodeport, loadbalancer or ingress (or add the listener to kafka.listeners yourself)." $type) -}}
{{- end -}}
{{- $tls := include "kafka-common.enabled" (list $ea.tls true) | eq "true" -}}
{{- if and (eq $type "ingress") (not $tls) -}}
{{- fail "kafka-cluster: kafka.externalAccess.type=ingress needs tls=true — Strimzi routes ingress listeners by TLS SNI." -}}
{{- end -}}
{{- $l := dict "name" ($ea.name | default "external") "port" (int ($ea.port | default 9094)) "type" $type "tls" $tls -}}
{{- with $ea.authentication -}}{{- $_ := set $l "authentication" (deepCopy .) -}}{{- end -}}
{{- with $ea.configuration -}}{{- $_ := set $l "configuration" (deepCopy .) -}}{{- end -}}
{{- /* The preset REPLACES a listener of the same name or port rather than
       colliding with it: 0.4's base values carry an `external` nodeport on
       9094, and an overlay that asks for external access through this key is
       describing that same listener. */ -}}
{{- $kept := list -}}
{{- $replaced := false -}}
{{- range $ls -}}
{{- if or (eq .name $l.name) (eq (int .port) (int $l.port)) -}}
{{- $replaced = true -}}
{{- else -}}
{{- $kept = append $kept . -}}
{{- end -}}
{{- end -}}
{{- if $replaced -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" (printf "kafka.listeners had a listener named %q on port %v; kafka.externalAccess replaced it" $l.name $l.port)) -}}
{{- end -}}
{{- $ls = append $kept $l -}}
{{- end -}}
{{- $names := list -}}
{{- $ports := list -}}
{{- range $ls -}}
{{- if has .name $names -}}
{{- fail (printf "kafka-cluster: two listeners are named %q (kafka.listeners and kafka.externalAccess.name must not collide)." .name) -}}
{{- end -}}
{{- if has (int .port) $ports -}}
{{- fail (printf "kafka-cluster: two listeners use port %v." .port) -}}
{{- end -}}
{{- if and (has (int .port) (list 9090 9091 8443 9404)) -}}
{{- fail (printf "kafka-cluster: listener %q uses port %v, which Strimzi reserves (9090 control plane, 9091 replication, 8443 agent, 9404 metrics)." .name .port) -}}
{{- end -}}
{{- $names = append $names .name -}}
{{- $ports = append $ports (int .port) -}}
{{- end -}}
{{- $_ := set $r "listeners" $ls -}}
{{- end }}

{{/*
Node pools. The 0.4 keys (controllerPools/brokerPools and their *Defaults) are
translated to the 1.0 shape and then resolved like any other pool.
*/}}
{{- define "kafka-cluster.resolve.pools" -}}
{{- $v := .ctx.Values -}}
{{- $r := .r -}}
{{- $np := $v.nodePools | default dict -}}
{{- $legacy := or (not (empty $v.controllerPools)) (not (empty $v.brokerPools)) -}}
{{- $raw := list -}}
{{- $overlays := dict -}}
{{- if $legacy -}}
{{- $chartDefault := list (dict "name" "controllers" "roles" (list "controller")) (dict "name" "brokers" "roles" (list "broker")) -}}
{{- if ne (toJson ($np.pools | default list)) (toJson $chartDefault) -}}
{{- fail "kafka-cluster: both nodePools.pools and the 0.4 controllerPools/brokerPools are set. Use one shape: nodePools.pools replaces the 0.4 keys (docs/kafka-cluster-1.0-upgrade.md)." -}}
{{- end -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "controllerPools/brokerPools/controllerDefaults/brokerDefaults are nodePools.pools/roleDefaults/defaults (translated; pool names are kept)") -}}
{{- range $keys := (list (list "controller" "controllerPools" "controllerDefaults") (list "broker" "brokerPools" "brokerDefaults")) -}}
{{- $role := index $keys 0 -}}
{{- $keys := rest $keys -}}
{{- $ld := index $v (index $keys 1) | default dict -}}
{{- if (($ld).sysctl).enabled -}}
{{- fail (printf "kafka-cluster: %s.sysctl.enabled is no longer supported. It rendered an init container into KafkaNodePool.spec.template.pod, which Strimzi's pod template does not have, so nothing ever ran; the vm.* keys it set are node-level and cannot be changed from a pod. Tune nodes with a DaemonSet or the node image, and set namespaced sysctls with nodePools.defaults.sysctls." (index $keys 1)) -}}
{{- end -}}
{{- $ov := dict -}}
{{- if hasKey $ld "deleteClaim" -}}{{- $_ := set $ov "volume" (dict "deleteClaim" $ld.deleteClaim) -}}{{- end -}}
{{- with $ld.jvmOptions -}}{{- $_ := set $ov "jvmOptions" (deepCopy .) -}}{{- end -}}
{{- with $ld.resources -}}{{- $_ := set $ov "resources" (deepCopy .) -}}{{- end -}}
{{- $sch := dict -}}
{{- with $ld.topologySpreadConstraints -}}{{- $_ := set $sch "spread" (deepCopy .) -}}{{- end -}}
{{- with $ld.podAntiAffinity -}}{{- $_ := set $sch "antiAffinity" (deepCopy .) -}}{{- end -}}
{{- if hasKey $ld "tolerations" -}}{{- $_ := set $sch "tolerations" ($ld.tolerations | default list) -}}{{- end -}}
{{- if hasKey $ld "priorityClassName" -}}{{- $_ := set $sch "priorityClassName" ($ld.priorityClassName | default "") -}}{{- end -}}
{{- if $sch -}}{{- $_ := set $ov "scheduling" $sch -}}{{- end -}}
{{- $_ := set $overlays $role $ov -}}
{{- range (index $v (index $keys 0) | default list) -}}
{{- $p := dict "name" .name "roles" (list $role) "legacy" true -}}
{{- if hasKey . "replicas" -}}{{- $_ := set $p "replicas" .replicas -}}{{- end -}}
{{- with .zone -}}{{- $_ := set $p "zone" . -}}{{- end -}}
{{- $vol := dict "id" 0 -}}
{{- with .storageSize -}}{{- $_ := set $vol "size" . -}}{{- end -}}
{{- with .storageClass -}}{{- $_ := set $vol "class" . -}}{{- end -}}
{{- $_ := set $p "storage" (dict "volumes" (list $vol)) -}}
{{- with .resources -}}{{- $_ := set $p "resources" (deepCopy .) -}}{{- end -}}
{{- with .jvmOptions -}}{{- $_ := set $p "jvmOptions" (deepCopy .) -}}{{- end -}}
{{- $raw = append $raw $p -}}
{{- end -}}
{{- end -}}
{{- else -}}
{{- range ($np.pools | default list) -}}
{{- $raw = append $raw (deepCopy .) -}}
{{- end -}}
{{- /*
  `kates detect` writes the 0.4 *Defaults next to its pools; an overlay that
  brings its own nodePools.pools clears those pools with
  `controllerPools: []` / `brokerPools: []`, and the defaults that came with
  them no longer describe anything.
*/ -}}
{{- if or $v.controllerDefaults $v.brokerDefaults -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "controllerDefaults/brokerDefaults are ignored with nodePools.pools; set nodePools.roleDefaults.controller/broker") -}}
{{- end -}}
{{- end -}}

{{- $pools := list -}}
{{- $names := list -}}
{{- $brokers := 0 -}}
{{- $controllers := 0 -}}
{{- range $raw -}}
{{- if not .name -}}{{- fail "kafka-cluster: every node pool needs a name." -}}{{- end -}}
{{- if has .name $names -}}
{{- fail (printf "kafka-cluster: two node pools are named %q. A pool's name is its identity (KafkaNodePool names are unique per namespace)." .name) -}}
{{- end -}}
{{- $names = append $names .name -}}
{{- $p := include "kafka-cluster.resolvePool" (dict "ctx" $.ctx "pool" . "overlays" $overlays) | fromJson -}}
{{- if hasKey $p "Error" -}}{{- fail (printf "kafka-cluster: node pool %s: %s" .name $p.Error) -}}{{- end -}}
{{- if has "broker" $p.roles -}}{{- $brokers = add $brokers $p.replicas -}}{{- end -}}
{{- if has "controller" $p.roles -}}{{- $controllers = add $controllers $p.replicas -}}{{- end -}}
{{- $pools = append $pools $p -}}
{{- end -}}
{{- if eq (len $pools) 0 -}}
{{- fail "kafka-cluster: no node pools. A Kafka cluster needs at least one pool with the controller role and one with the broker role (nodePools.pools)." -}}
{{- end -}}
{{- if eq (int $controllers) 0 -}}
{{- fail "kafka-cluster: no node pool runs a controller (roles: [controller] with replicas > 0). KRaft needs a controller quorum; the Kafka CR would never become Ready." -}}
{{- end -}}
{{- if eq (int $brokers) 0 -}}
{{- fail "kafka-cluster: no node pool runs a broker (roles: [broker] with replicas > 0). The Kafka CR would never become Ready." -}}
{{- end -}}
{{- $_ := set $r "pools" $pools -}}
{{- $_ := set $r "brokers" (int $brokers) -}}
{{- $_ := set $r "controllers" (int $controllers) -}}
{{- end }}

{{/*
One pool, merged: nodePools.defaults, then roleDefaults for each of its roles,
then the 0.4 *Defaults overlay (translated pools only), then the pool itself.
Returns JSON: {name, roles, replicas, zone, legacy, storage, resources,
jvmOptions, scheduling, podSecurityContext, containerSecurityContext,
sysctls, template}.
*/}}
{{- define "kafka-cluster.resolvePool" -}}
{{- $v := .ctx.Values -}}
{{- $np := $v.nodePools | default dict -}}
{{- $pool := .pool -}}
{{- $roles := $pool.roles | default list -}}
{{- if not $roles -}}{{- fail (printf "kafka-cluster: node pool %q needs roles (controller, broker, or both)." $pool.name) -}}{{- end -}}
{{- range $roles -}}
{{- if not (has . (list "controller" "broker")) -}}
{{- fail (printf "kafka-cluster: node pool %q has role %q; roles are controller and broker." $pool.name .) -}}
{{- end -}}
{{- end -}}
{{- $p := deepCopy ($np.defaults | default dict) -}}
{{- range (list "controller" "broker") -}}
{{- if has . $roles -}}
{{- include "kafka-cluster.merge" (dict "dst" $p "src" (index ($np.roleDefaults | default dict) .)) -}}
{{- end -}}
{{- end -}}
{{- range $roles -}}
{{- include "kafka-cluster.merge" (dict "dst" $p "src" (index $.overlays .)) -}}
{{- end -}}
{{- include "kafka-cluster.merge" (dict "dst" $p "src" $pool) -}}
{{- $_ := set $p "roles" $roles -}}
{{- $_ := set $p "replicas" (int ($p.replicas | default 0)) -}}

{{- /* Storage: every JBOD volume on top of the volume defaults. */ -}}
{{- $st := $p.storage | default dict -}}
{{- $type := $st.type | default "jbod" -}}
{{- if eq $type "jbod" -}}
{{- $vols := list -}}
{{- $ids := list -}}
{{- range ($st.volumes | default list) -}}
{{- $vol := deepCopy ($p.volume | default dict) -}}
{{- include "kafka-cluster.merge" (dict "dst" $vol "src" .) -}}
{{- if and (eq ($vol.type | default "persistent-claim") "persistent-claim") (not $vol.size) -}}
{{- fail (printf "kafka-cluster: node pool %q volume %v has no size." $pool.name $vol.id) -}}
{{- end -}}
{{- if has (int $vol.id) $ids -}}{{- fail (printf "kafka-cluster: node pool %q has two volumes with id %v." $pool.name $vol.id) -}}{{- end -}}
{{- $ids = append $ids (int $vol.id) -}}
{{- $_ := set $vol "id" (int $vol.id) -}}
{{- $vols = append $vols $vol -}}
{{- end -}}
{{- if not $vols -}}{{- fail (printf "kafka-cluster: node pool %q has no storage volumes." $pool.name) -}}{{- end -}}
{{- $st = dict "type" "jbod" "volumes" $vols -}}
{{- else -}}
{{- /* A single volume (persistent-claim or ephemeral): the volume defaults
       apply to it too, and it is checked like a JBOD volume — otherwise a
       pool that opts out of JBOD also opts out of every rail. */ -}}
{{- $single := deepCopy ($p.volume | default dict) -}}
{{- include "kafka-cluster.merge" (dict "dst" $single "src" (omit $st "volumes")) -}}
{{- $_ := set $single "type" $type -}}
{{- if and (eq $type "persistent-claim") (not $single.size) -}}
{{- fail (printf "kafka-cluster: node pool %q uses persistent-claim storage with no size." $pool.name) -}}
{{- end -}}
{{- $st = $single -}}
{{- end -}}
{{- $_ := set $p "storage" $st -}}
{{- $_ := unset $p "volume" -}}

{{- /* Scheduling */ -}}
{{- $sch := $p.scheduling | default dict -}}
{{- if and $p.zone (not $sch.zoneKey) -}}
{{- fail (printf "kafka-cluster: node pool %q pins zone %q, but nodePools.defaults.scheduling.zoneKey is empty — there is no node label to pin it to." $pool.name $p.zone) -}}
{{- end -}}

{{- /* Sysctls: namespaced keys only. */ -}}
{{- range ($p.sysctls | default list) -}}
{{- $k := .name | default "" -}}
{{- $ok := or (hasPrefix "net." $k) (hasPrefix "kernel.shm" $k) (hasPrefix "kernel.msg" $k) (eq $k "kernel.sem") (hasPrefix "fs.mqueue." $k) -}}
{{- if not $ok -}}
{{- fail (printf "kafka-cluster: node pool %q sets sysctl %q, which is not namespaced — a pod cannot change it (the kubelet refuses the pod). Tune nodes with a DaemonSet or the node image; vm.max_map_count is the usual one." $pool.name $k) -}}
{{- end -}}
{{- end -}}
{{- $_ := set $p "legacy" (include "kafka-common.enabled" (list $pool.legacy false) | eq "true") -}}
{{- toJson $p -}}
{{- end }}

{{/* Topics: the profile's under the release's, defaults applied. */}}
{{- define "kafka-cluster.resolve.topics" -}}
{{- $v := .ctx.Values -}}
{{- $r := .r -}}
{{- $t := $v.topics | default dict -}}
{{- if kindIs "slice" $t.items -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "topics.items as a list is a map keyed by topic name") -}}
{{- end -}}
{{- $items := include "kafka-cluster.byName" (dict "items" ((($r.profile.topics).items) | default dict) "field" "profile topics") | fromJson -}}
{{- $mine := include "kafka-cluster.byName" (dict "items" $t.items "field" "topics.items") | fromJson -}}
{{- range $name, $item := $mine -}}
{{- if hasKey $items $name -}}
{{- include "kafka-cluster.merge" (dict "dst" (index $items $name) "src" $item) -}}
{{- else -}}
{{- $_ := set $items $name $item -}}
{{- end -}}
{{- end -}}
{{- $d := $t.defaults | default dict -}}
{{- $brokers := int $r.brokers -}}
{{- $out := list -}}
{{- if include "kafka-common.enabled" (list $t.enabled true) -}}
{{- range $name := (keys $items | sortAlpha) -}}
{{- $item := index $items $name | default dict -}}
{{- if include "kafka-common.enabled" (list $item.enabled true) -}}
{{- $rf := int ($item.replicas | default $d.replicas | default (min $brokers 3)) -}}
{{- if gt $rf $brokers -}}
{{- fail (printf "kafka-cluster: topic %q asks for %d replicas, but the node pools render %d broker(s). Lower its replicas (or topics.defaults.replicas), or add brokers." $name $rf $brokers) -}}
{{- end -}}
{{- $cfg := deepCopy ($d.config | default dict) -}}
{{- /* Derived only when neither topics.defaults.config nor the topic itself
       sets it: a value asked for explicitly is not a default to compute. */ -}}
{{- if not (hasKey $cfg "min.insync.replicas") -}}
{{- $minIsr := max 1 (min 2 (sub $rf 1)) -}}
{{- $_ := set $cfg "min.insync.replicas" (toString $minIsr) -}}
{{- end -}}
{{- if and $r.tiered (include "kafka-common.enabled" (list $v.tieredStorage.topicDefault true)) -}}
{{- $_ := set $cfg "remote.storage.enable" "true" -}}
{{- end -}}
{{- include "kafka-cluster.merge" (dict "dst" $cfg "src" ($item.config | default dict)) -}}
{{- $isr := int (index $cfg "min.insync.replicas") -}}
{{- if gt $isr $rf -}}
{{- fail (printf "kafka-cluster: topic %q has min.insync.replicas %d above its %d replicas — no write with acks=all could ever succeed." $name $isr $rf) -}}
{{- end -}}
{{- $spec := dict "partitions" (int ($item.partitions | default $d.partitions | default 3)) "replicas" $rf "config" $cfg -}}
{{- with $item.topicName -}}{{- $_ := set $spec "topicName" . -}}{{- end -}}
{{- $out = append $out (dict "name" $name "spec" $spec) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $_ := set $r "topics" $out -}}
{{- end }}

{{/* Users: the profile's under the release's. */}}
{{- define "kafka-cluster.resolve.users" -}}
{{- $v := .ctx.Values -}}
{{- $r := .r -}}
{{- $u := $v.users | default dict -}}
{{- if kindIs "slice" $u.items -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "users.items as a list is a map keyed by user name") -}}
{{- end -}}
{{- $items := include "kafka-cluster.byName" (dict "items" ((($r.profile.users).items) | default dict) "field" "profile users") | fromJson -}}
{{- $mine := include "kafka-cluster.byName" (dict "items" $u.items "field" "users.items") | fromJson -}}
{{- range $name, $item := $mine -}}
{{- if hasKey $items $name -}}
{{- include "kafka-cluster.merge" (dict "dst" (index $items $name) "src" $item) -}}
{{- else -}}
{{- $_ := set $items $name $item -}}
{{- end -}}
{{- end -}}
{{- if and $v.kafkaUI (hasKey $v.kafkaUI "managedUser") -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "kafkaUI.managedUser is users.items.kafka-ui.enabled") -}}
{{- if and (not (include "kafka-common.enabled" (list $v.kafkaUI.managedUser true))) (hasKey $items "kafka-ui") -}}
{{- $_ := set (index $items "kafka-ui") "enabled" false -}}
{{- end -}}
{{- end -}}
{{- $out := list -}}
{{- if include "kafka-common.enabled" (list $u.enabled true) -}}
{{- range $name := (keys $items | sortAlpha) -}}
{{- $item := index $items $name | default dict -}}
{{- if include "kafka-common.enabled" (list $item.enabled true) -}}
{{- $out = append $out (merge (dict "name" $name) (omit $item "enabled")) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $_ := set $r "users" $out -}}
{{- end }}

{{/*
networkPolicy, with the 0.4 networkPolicies block translated over it and the
clients list resolved: the profile's first, the release's merged by name, each
with the ports of its listeners.
*/}}
{{- define "kafka-cluster.resolve.netpol" -}}
{{- $v := .ctx.Values -}}
{{- $r := .r -}}
{{- $np := deepCopy ($v.networkPolicy | default dict) -}}
{{- $nsOverrides := dict -}}
{{- with $v.networkPolicies -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "networkPolicies is networkPolicy (clients replace the per-consumer namespace keys)") -}}
{{- if hasKey . "enabled" -}}{{- include "kafka-cluster.legacySet" (dict "dst" $np "key" "enabled" "value" .enabled "default" true) -}}{{- end -}}
{{- $dd := deepCopy ($np.defaultDeny | default dict) -}}
{{- if hasKey . "defaultDeny" -}}{{- include "kafka-cluster.legacySet" (dict "dst" $dd "key" "enabled" "value" .defaultDeny "default" true) -}}{{- end -}}
{{- with .defaultDenySelector -}}{{- include "kafka-cluster.legacySet" (dict "dst" $dd "key" "selector" "value" . "default" dict) -}}{{- end -}}
{{- $_ := set $np "defaultDeny" $dd -}}
{{- $dns := deepCopy ($np.dns | default dict) -}}
{{- if hasKey . "allowDNS" -}}{{- include "kafka-cluster.legacySet" (dict "dst" $dns "key" "enabled" "value" .allowDNS "default" true) -}}{{- end -}}
{{- with .allowDNSSelector -}}{{- include "kafka-cluster.legacySet" (dict "dst" $dns "key" "selector" "value" . "default" dict) -}}{{- end -}}
{{- $_ := set $np "dns" $dns -}}
{{- with .monitoringNamespace -}}
{{- $mon := deepCopy ($np.monitoring | default dict) -}}
{{- include "kafka-cluster.legacySet" (dict "dst" $mon "key" "namespace" "value" . "default" "monitoring") -}}
{{- $_ := set $np "monitoring" $mon -}}
{{- end -}}
{{- with .operatorNamespace -}}{{- include "kafka-cluster.legacySet" (dict "dst" $np "key" "operatorNamespace" "value" . "default" "strimzi-operator") -}}{{- end -}}
{{- range $key, $client := (dict "connectNamespace" "connect" "mirrorMaker2Namespace" "mirror-maker2" "kafkaUINamespace" "kafka-ui" "apicurioNamespace" "apicurio-registry") -}}
{{- with (index $v.networkPolicies $key) -}}{{- $_ := set $nsOverrides $client . -}}{{- end -}}
{{- end -}}
{{- if include "kafka-common.enabled" (list ((.operatorPolicy).enabled) false) -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "networkPolicies.operatorPolicy does nothing: the Cluster Operator's NetworkPolicy is the strimzi-operator chart's (operatorPolicy.enabled there)") -}}
{{- end -}}
{{- if hasKey . "kafkaUI" -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "networkPolicies.kafkaUI does nothing: the kafka-ui chart renders its own NetworkPolicy") -}}
{{- end -}}
{{- if .allowedClientNamespaces -}}
{{- include "kafka-cluster.deprecate" (dict "r" $r "msg" "networkPolicies.allowedClientNamespaces never did anything; list clients in networkPolicy.clients") -}}
{{- end -}}
{{- end -}}
{{- $_ := set $np "enabled" (include "kafka-common.enabled" (list $np.enabled true) | eq "true") -}}
{{- $_ := set $np "defaultDenyEnabled" (include "kafka-common.enabled" (list (($np.defaultDeny).enabled) true) | eq "true") -}}
{{- $_ := set $np "dnsEnabled" (include "kafka-common.enabled" (list (($np.dns).enabled) true) | eq "true") -}}

{{- /* Listener name → port; internal ones are what a client gets by default. */ -}}
{{- $byName := dict -}}
{{- $internal := list -}}
{{- $external := list -}}
{{- range $r.listeners -}}
{{- $_ := set $byName .name (int .port) -}}
{{- if has .type (list "internal" "cluster-ip") -}}
{{- $internal = append $internal (int .port) -}}
{{- else -}}
{{- $external = append $external . -}}
{{- end -}}
{{- end -}}
{{- $_ := set $np "internalPorts" $internal -}}
{{- $_ := set $np "externalListeners" $external -}}
{{- $_ := set $np "nsOverrides" $nsOverrides -}}

{{- $order := list -}}
{{- $clients := dict -}}
{{- range (concat ((($r.profile.networkPolicy).clients) | default list) ($np.clients | default list)) -}}
{{- if not .name -}}{{- fail "kafka-cluster: every networkPolicy.clients entry needs a name." -}}{{- end -}}
{{- if hasKey $clients .name -}}
{{- include "kafka-cluster.merge" (dict "dst" (index $clients .name) "src" .) -}}
{{- else -}}
{{- $_ := set $clients .name (deepCopy .) -}}
{{- $order = append $order .name -}}
{{- end -}}
{{- end -}}
{{- $resolved := list -}}
{{- $relNs := include "kafka-cluster.namespace" $.ctx -}}
{{- range $name := $order -}}
{{- $c := index $clients $name -}}
{{- if include "kafka-common.enabled" (list $c.enabled true) -}}
{{- $ports := list -}}
{{- if $c.listeners -}}
{{- range $c.listeners -}}
{{- if not (hasKey $byName .) -}}
{{- fail (printf "kafka-cluster: networkPolicy.clients %q names listener %q, which kafka.listeners does not have (listeners: %s)." $name . (keys $byName | sortAlpha | join ", ")) -}}
{{- end -}}
{{- $ports = append $ports (index $byName .) -}}
{{- end -}}
{{- else -}}
{{- $ports = $internal -}}
{{- end -}}
{{- $ns := index $nsOverrides $name | default $c.namespace | default $relNs -}}
{{- if not $c.podSelector -}}
{{- fail (printf "kafka-cluster: networkPolicy.clients %q needs a podSelector (an empty one would admit every pod of namespace %s)." $name $ns) -}}
{{- end -}}
{{- $resolved = append $resolved (dict "name" $name "namespace" $ns "podSelector" $c.podSelector "ports" ($ports | uniq)) -}}
{{- end -}}
{{- end -}}
{{- $_ := set $np "resolvedClients" $resolved -}}
{{- $_ := set $r "netpol" $np -}}
{{- end }}
