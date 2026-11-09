#!/usr/bin/env bash
# Every chart installs in two RBAC scopes (rbac.scope, else global.rbacScope):
#   cluster   — the default; an explicit cluster renders exactly what no value does.
#   namespace — nothing cluster-scoped, so a namespace admin can install it.
# A chart whose subchart needs more switches ships values-namespace.yaml and
# refuses namespace scope without it. Charts with no cluster grant at all
# render nothing cluster-scoped in either scope. Needs chart dependencies built.
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER_KINDS='^kind: (Cluster[A-Za-z]*|CustomResourceDefinition|ValidatingWebhookConfiguration|MutatingWebhookConfiguration|PriorityClass|Namespace|StorageClass|APIService|PersistentVolume)$'
NS=team-a
rc=0
fail() { echo "::error::$1"; rc=1; bad=1; }

# chart | extra values for namespace scope (beyond the scope itself)
scoped=(
  "kafka-cluster|"
  "connect-cluster|--set priorityClass.create=true --set rack.enabled=true"
  "mirror-maker2|--set rack.enabled=true"
  "kates|"
  "kates-platform|--set connect-cluster.enabled=true --set mirror-maker2.enabled=true"
  "headlamp|"
  "strimzi-operator|"
  "kates-chaos|--set gameday.enabled=true"
  "monitoring|"
  "velero|"
)
# Charts that never need a cluster grant: no switch, nothing cluster-scoped.
plain=(kafka-ui apicurio-registry cdc-postgresql legacy-kafka)

render() { helm template r "charts/$1" -n "${NS}" "${@:2}" 2>&1; }
cluster_objects() { grep -E "${CLUSTER_KINDS}" <<<"$1" || true; }
# Generated on every render (keys, passwords, certificates and their checksums).
stable() { grep -vE '(api-key|password|access_key|secret_key|accessKey|secretKey|s3_config|tls\.(crt|key)|ca\.crt|caBundle|checksum/)' <<<"$1" || true; }

for spec in "${scoped[@]}"; do
  bad=0
  chart=${spec%%|*}; read -r -a extra <<<"${spec#*|}"
  overlay=()
  [ -f "charts/${chart}/values-namespace.yaml" ] && overlay=(-f "charts/${chart}/values-namespace.yaml")
  key=rbac.scope; [ "${chart}" = kates-platform ] && key=global.rbacScope

  # cluster: an explicit value changes nothing
  [ "$(stable "$(render "${chart}")")" = "$(stable "$(render "${chart}" --set "${key}=cluster")")" ] \
    || fail "${chart}: ${key}=cluster renders other than the default"

  # namespace, through the chart's key and through global.rbacScope
  for via in "${key}" global.rbacScope; do
    out=$(render "${chart}" "${overlay[@]}" --set "${via}=namespace" "${extra[@]}") \
      || { fail "${chart}: ${via}=namespace does not render: $(tail -3 <<<"${out}")"; continue; }
    objs=$(cluster_objects "${out}")
    [ -z "${objs}" ] || fail "${chart}: ${via}=namespace renders cluster-scoped objects: $(tr '\n' ' ' <<<"${objs}")"
  done

  # an overlay chart refuses namespace scope without its overlay
  if [ ${#overlay[@]} -gt 0 ]; then
    out=$(render "${chart}" --set rbac.scope=namespace) && fail "${chart}: rbac.scope=namespace without values-namespace.yaml renders"
    grep -q 'values-namespace.yaml' <<<"${out}" \
      || fail "${chart}: the refusal does not name values-namespace.yaml: $(tail -2 <<<"${out}")"
  fi

  # the schema refuses anything else
  render "${chart}" --set "${key}=team" >/dev/null && fail "${chart}: ${key}=team is accepted"
  [ "${bad}" = 1 ] || echo "ok  ${chart}"
done

for chart in "${plain[@]}"; do
  bad=0
  objs=$(cluster_objects "$(render "${chart}")")
  [ -z "${objs}" ] || fail "${chart}: renders cluster-scoped objects, so it needs an rbac.scope: $(tr '\n' ' ' <<<"${objs}")"
  [ "${bad}" = 1 ] || echo "ok  ${chart} (nothing cluster-scoped)"
done

# kafka-cluster refuses a NodePort listener in namespace scope: Strimzi binds it to a ClusterRole.
render kafka-cluster --set rbac.scope=namespace --set kafka.externalAccess.type=nodeport >/dev/null \
  && fail "kafka-cluster: a NodePort listener renders in namespace scope"

exit ${rc}
