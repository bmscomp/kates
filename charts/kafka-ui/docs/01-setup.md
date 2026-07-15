# 01 — Setup & Access

End-to-end install of Kafka UI against a Strimzi Kafka cluster.

## Prerequisites

- A Kubernetes cluster (1.27+) with the **Strimzi** operator and a running
  `Kafka` custom resource. Verify:

  ```bash
  kubectl get kafka -A
  # NAMESPACE   NAME      READY
  # kafka       krafter   True
  ```

- The Kafka cluster must expose a **SCRAM-SHA-512** listener (Kafka UI
  authenticates with SCRAM). Check its listeners:

  ```bash
  kubectl get kafka krafter -n kafka \
    -o jsonpath='{range .spec.kafka.listeners[*]}{.name}{" :"}{.port}{" auth="}{.authentication.type}{"\n"}{end}'
  # plain :9092 auth=scram-sha-512
  # tls   :9093 auth=tls
  ```

- Optional integrations: an Apicurio **Schema Registry** and a **Kafka Connect**
  cluster (see guides [03](03-schema-registry.md) and [04](04-kafka-connect.md)).

## Where to install it

Kafka UI needs a SCRAM `KafkaUser` and its generated Secret. There are two
patterns:

1. **Chart-managed user (default).** Deploy Kafka UI **in the same namespace as
   Kafka** and leave `kafkaUser.enabled=true`. The chart creates a `KafkaUser`
   and Strimzi's User Operator generates the credential Secret. See
   [02 — Kafka Connection & ACLs](02-kafka-and-acls.md).

2. **Externally-managed user.** If the `KafkaUser` is owned by another release
   (e.g. a platform chart), set `kafkaUser.enabled=false` and Kafka UI consumes
   the existing Secret named after `kafkaUser.name`. Trying to adopt a
   `KafkaUser` owned by another Helm release fails with an ownership error.

## Install

```bash
helm upgrade --install kafka-ui charts/kafka-ui \
  --namespace kafka \
  --set kafka.clusterName=krafter \
  --set service.type=NodePort --set service.nodePort=30081 \
  --set auth.enabled=true --set auth.type=LOGIN_FORM \
  --wait --timeout 3m
```

### Environment overlays

Pre-built value files ship with the chart:

| Overlay | Use |
|---------|-----|
| `values-kind.yaml` | Local Kind (NodePort 30081, `kafkaUser.enabled=false`, SR/Connect on) |
| `values-dev.yaml` | Dev (SR + Connect integration) |
| `values-prod.yaml` | Production (2 replicas, PDB, topology spread, ServiceMonitor, TLS, Ingress) |

```bash
helm upgrade --install kafka-ui charts/kafka-ui -n kafka -f charts/kafka-ui/values-prod.yaml --wait
```

## Web login

When `auth.enabled=true` and `auth.type=LOGIN_FORM`, the UI requires a login.
The password is **generated randomly on first install and preserved across
upgrades** — it is never a well-known default.

- **Username:** `auth.username` (defaults to `admin`).
- **Password:** stored in the Secret named by `auth.passwordSecret` (defaults to
  `kafka-ui-web-password`), under the key `password`.

### Get the password

The same one-liner works whether the password was auto-generated, set via
`auth.password`, or later rotated — always read it back from the Secret rather
than assuming a value:

```bash
kubectl get secret kafka-ui-web-password -n kafka \
  -o jsonpath='{.data.password}' | base64 -d; echo
```

Adjust the Secret name if you changed `auth.passwordSecret`, or the namespace if
you installed elsewhere. Print username and password together:

```bash
NS=kafka
echo "user: $(helm get values kafka-ui -n $NS -o json | \
  python3 -c 'import sys,json;print(json.load(sys.stdin).get("auth",{}).get("username","admin"))')"
echo "pass: $(kubectl get secret kafka-ui-web-password -n $NS -o jsonpath='{.data.password}' | base64 -d)"
```

If you use your own Secret (`auth.existingSecret`), read the password from that
Secret instead (key `password`).

### Rotate the password, then read the new one

Delete the Secret so the chart mints a fresh random password, upgrade, restart
the pod to load it, then read it back:

```bash
# 1. Remove the current Secret (it carries helm.sh/resource-policy: keep,
#    so a plain `helm upgrade` will NOT rotate it on its own)
kubectl delete secret kafka-ui-web-password -n kafka

# 2. Regenerate (a new random password is created because none exists now)
helm upgrade kafka-ui charts/kafka-ui -n kafka --reuse-values --wait

# 3. Restart so the pod picks up the new value (env is read at startup)
kubectl rollout restart deploy/kafka-ui -n kafka
kubectl rollout status  deploy/kafka-ui -n kafka

# 4. Read the rotated password
kubectl get secret kafka-ui-web-password -n kafka \
  -o jsonpath='{.data.password}' | base64 -d; echo
```

To rotate to a **specific** value instead of a random one, skip the delete and
set it explicitly:

```bash
helm upgrade kafka-ui charts/kafka-ui -n kafka --reuse-values \
  --set auth.password='my-new-password' --wait
kubectl rollout restart deploy/kafka-ui -n kafka
```

### Manage the password yourself

```yaml
auth:
  enabled: true
  type: LOGIN_FORM
  username: admin
  password: "my-password"          # chart creates the Secret from this
  # existingSecret: my-ui-secret   # …or bring your own (key: password)
```

## Access the UI

```bash
# NodePort
echo "http://$(kubectl get node -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}'):30081"

# Port-forward (works anywhere)
kubectl port-forward svc/kafka-ui 8080:8080 -n kafka
#   → http://localhost:8080
```

For production, use an Ingress:

```yaml
ingress:
  enabled: true
  className: nginx
  hosts:
    - host: kafka-ui.example.com
      paths: [{ path: /, pathType: Prefix }]
  tls:
    - secretName: kafka-ui-tls
      hosts: [kafka-ui.example.com]
```

## Verify

```bash
# Health endpoint
kubectl exec deploy/kafka-ui -n kafka -- wget -qO- http://localhost:8080/actuator/health
# {"status":"UP","groups":["liveness","readiness"]}

# Chart test suite (probes /actuator/health)
helm test kafka-ui -n kafka

# Logs — confirm the cluster is being scraped
kubectl logs deploy/kafka-ui -n kafka | grep -i "Metrics updated for cluster"
```

## Security hardening (defaults)

The chart runs hardened out of the box:

- Dedicated `ServiceAccount` with `automountServiceAccountToken: false` (Kafka UI does not call the Kubernetes API).
- `readOnlyRootFilesystem: true` with an `emptyDir` mounted at `/tmp`.
- Non-root (`runAsUser: 1001`), all capabilities dropped, no privilege escalation.
- A scoped `NetworkPolicy` (browser via the ingress controller, Prometheus scrape, egress only to Kafka + enabled integrations + DNS; **no** Kubernetes API-server egress unless `networkPolicy.apiServerEgress=true`).

## Observability

Kafka UI exposes Prometheus metrics at `/actuator/prometheus`.

```yaml
metrics:
  serviceMonitor:
    enabled: true          # requires the Prometheus Operator CRDs
    labels:
      release: monitoring  # match your Prometheus selector
```

## Uninstall

```bash
helm uninstall kafka-ui -n kafka
```

A chart-managed `KafkaUser` carries `helm.sh/resource-policy: keep`, so the
Strimzi Secret survives uninstall to avoid disrupting other consumers. Delete it
manually if you truly want it gone.
