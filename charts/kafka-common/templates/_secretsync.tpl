{{/*
Copy credential Secrets from other namespaces into this release's namespace,
optionally under another name (`as`), so a Strimzi client's passwordSecret /
trustedCertificates references resolve.

A post-install/post-upgrade hook Job, re-run on every upgrade to pick up
rotation, with ORDINARY (release-tracked) RBAC scoped by name: a pre-install
hook would need its RBAC as hooks too, and hook resources are never deleted by
`helm uninstall` — that leaked read grants into every source namespace. With
`watch`, a CronJob re-copies on a schedule; that is a standing cross-namespace
read grant, so it is opt-in.

Call with (dict "ctx" $ "fullname" … "namespace" <destination ns>
"labels" <labels YAML> "secrets" (list (dict "name" "fromNamespace" "as")) "image" …
"pullPolicy" … "timeoutSeconds" 300 "watch" false "schedule" <cron>).
Entries whose source namespace and name equal the destination are skipped.
Renders nothing when no entry is left.
*/}}
{{- define "kafka-common.secretSync" -}}
{{- $dstNs := .namespace -}}
{{- $fullname := .fullname -}}
{{- $labels := .labels -}}
{{- $secrets := list -}}
{{- range .secrets -}}
{{- $as := .as | default .name -}}
{{- if not (and (eq .fromNamespace $dstNs) (eq $as .name)) -}}
{{- $secrets = append $secrets (dict "name" .name "fromNamespace" .fromNamespace "as" $as) -}}
{{- end -}}
{{- end -}}
{{- if $secrets }}
{{- $names := list }}{{- range $secrets }}{{- $names = append $names .as }}{{- end }}
{{- $srcNamespaces := list }}{{- range $secrets }}{{- $srcNamespaces = append $srcNamespaces .fromNamespace }}{{- end }}
{{- $srcNamespaces = $srcNamespaces | uniq }}
{{- $args := dict "dstNs" $dstNs "secrets" $secrets "image" .image "pullPolicy" .pullPolicy "timeoutSeconds" (.timeoutSeconds | default 300) "fullname" $fullname "labels" $labels }}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ $fullname }}-secret-sync
  namespace: {{ $dstNs }}
  labels:
    {{- $labels | nindent 4 }}
{{- range $srcNs := $srcNamespaces }}
{{- $nsNames := list }}
{{- range $secrets }}{{- if eq .fromNamespace $srcNs }}{{- $nsNames = append $nsNames .name }}{{- end }}{{- end }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ $fullname }}-secret-sync-read
  namespace: {{ $srcNs }}
  labels:
    {{- $labels | nindent 4 }}
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: {{ $nsNames | uniq | toJson }}
    verbs: ["get", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ $fullname }}-secret-sync-read
  namespace: {{ $srcNs }}
  labels:
    {{- $labels | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ $fullname }}-secret-sync-read
subjects:
  - kind: ServiceAccount
    name: {{ $fullname }}-secret-sync
    namespace: {{ $dstNs }}
{{- end }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ $fullname }}-secret-sync-write
  namespace: {{ $dstNs }}
  labels:
    {{- $labels | nindent 4 }}
rules:
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["create"]
  - apiGroups: [""]
    resources: ["secrets"]
    resourceNames: {{ $names | uniq | toJson }}
    verbs: ["get", "update", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ $fullname }}-secret-sync-write
  namespace: {{ $dstNs }}
  labels:
    {{- $labels | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ $fullname }}-secret-sync-write
subjects:
  - kind: ServiceAccount
    name: {{ $fullname }}-secret-sync
    namespace: {{ $dstNs }}
---
apiVersion: batch/v1
kind: Job
metadata:
  name: {{ $fullname }}-secret-sync
  namespace: {{ $dstNs }}
  labels:
    {{- $labels | nindent 4 }}
  annotations:
    helm.sh/hook: post-install,post-upgrade
    helm.sh/hook-weight: "0"
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
spec:
  {{- include "kafka-common.secretSync.jobSpec" $args | nindent 2 }}
{{- if .watch }}
---
apiVersion: batch/v1
kind: CronJob
metadata:
  name: {{ $fullname }}-secret-sync
  namespace: {{ $dstNs }}
  labels:
    {{- $labels | nindent 4 }}
spec:
  schedule: {{ .schedule | default "*/15 * * * *" | quote }}
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 1
  failedJobsHistoryLimit: 3
  startingDeadlineSeconds: 300
  jobTemplate:
    spec:
      {{- include "kafka-common.secretSync.jobSpec" $args | nindent 6 }}
{{- end }}
{{- end }}
{{- end }}

{{/* The Job spec shared by the hook Job and the rotation CronJob. */}}
{{- define "kafka-common.secretSync.jobSpec" -}}
backoffLimit: 2
activeDeadlineSeconds: {{ .timeoutSeconds }}
template:
  metadata:
    labels:
      {{- .labels | nindent 6 }}
  spec:
    serviceAccountName: {{ .fullname }}-secret-sync
    restartPolicy: Never
    securityContext:
      runAsNonRoot: true
      runAsUser: 65534
      seccompProfile:
        type: RuntimeDefault
    containers:
      - name: sync
        # Needs kubectl + jq + sh.
        image: {{ required "kafka-common.secretSync: image is required" .image }}
        {{- include "kafka-common.imagePullPolicy" .pullPolicy | nindent 8 }}
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          capabilities:
            drop: ["ALL"]
        resources:
          requests: { cpu: 10m, memory: 32Mi }
          limits: { cpu: 100m, memory: 64Mi }
        env:
          - name: DST_NS
            value: {{ .dstNs | quote }}
          # Space-separated "<srcNamespace>/<name>/<targetName>" triples.
          # Namespace and Secret names cannot contain "/", so the split is
          # unambiguous.
          - name: PAIRS
            value: {{ range $i, $s := .secrets }}{{ if $i }} {{ end }}{{ $s.fromNamespace }}/{{ $s.name }}/{{ $s.as }}{{ end }}
        command:
          - /bin/sh
          - -ec
          - |
            for pair in ${PAIRS}; do
              SRC_NS="${pair%%/*}"; rest="${pair#*/}"
              NAME="${rest%%/*}"; DST_NAME="${rest#*/}"
              echo "Waiting for ${SRC_NS}/${NAME}..."
              i=0
              until kubectl -n "${SRC_NS}" get secret "${NAME}" >/dev/null 2>&1; do
                i=$((i+1)); [ "$i" -ge 48 ] && { echo "Timed out on ${SRC_NS}/${NAME}" >&2; exit 1; }
                sleep 5
              done
              echo "Copying ${SRC_NS}/${NAME} -> ${DST_NS}/${DST_NAME}"
              kubectl -n "${SRC_NS}" get secret "${NAME}" -o json \
                | jq --arg ns "${DST_NS}" --arg name "${DST_NAME}" --arg src "${SRC_NS}/${NAME}" '
                    {apiVersion:"v1", kind:"Secret", type:.type, data:.data,
                     metadata:{name:$name, namespace:$ns,
                               labels:(.metadata.labels // {}),
                               annotations:{"kates.io/synced-from":$src}}}' \
                | kubectl apply -f -
            done
            echo "All secrets synced."
{{- end }}
