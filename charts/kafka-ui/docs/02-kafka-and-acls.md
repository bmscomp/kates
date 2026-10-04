# 02 — Kafka Connection & ACLs

How Kafka UI connects to the brokers, authenticates with SCRAM, and what
**authorization (ACLs)** it needs.

## Connection

### Auto-discovery (default)

The chart computes the bootstrap address from `kafka.clusterName` and the
namespace:

```yaml
kafka:
  clusterName: krafter          # the Strimzi Kafka CR name
  namespace: ""                 # defaults to the release namespace
  clusterDomain: cluster.local
```

Resolves to `krafter-kafka-bootstrap.<namespace>.svc.cluster.local:9092`
(`:9092` for plaintext SCRAM, `:9093` when `kafka.tls.enabled=true`).

### Explicit bootstrap

```yaml
kafka:
  bootstrapServers: "my-kafka-bootstrap.prod.svc:9092"
```

## Authentication (SCRAM-SHA-512)

Kafka UI authenticates as a Strimzi `KafkaUser` using SCRAM-SHA-512. The User
Operator generates a Secret (named after the user) containing the password; the
chart injects it into the pod as `KAFKA_UI_PASSWORD` and references it in the
client `sasl.jaas.config`. The password is **never** written to the ConfigMap.

The rendered client config (plaintext SCRAM listener):

```yaml
security.protocol: SASL_PLAINTEXT     # SASL_SSL when kafka.tls.enabled
sasl.mechanism: SCRAM-SHA-512
sasl.jaas.config: >-
  org.apache.kafka.common.security.scram.ScramLoginModule required
  username="kafka-ui" password="${KAFKA_UI_PASSWORD}";
```

### TLS to Kafka (SASL_SSL)

Only if your cluster exposes a **SASL_SSL** listener (SCRAM over TLS). Note: a
Strimzi `tls` listener with `authentication.type: tls` is **mutual TLS**, not
SASL — do not point Kafka UI at it with SCRAM.

```yaml
kafka:
  tls:
    enabled: true
    trustedCertificateSecret: krafter-cluster-ca-cert   # required when enabled
    certificateKey: ca.crt
```

The chart mounts the CA at `/etc/kafka-ui/certs/<certificateKey>` and sets
`ssl.truststore.location`. Enabling TLS without the Secret fails the render.

## The KafkaUser

### Chart-managed (same namespace as Kafka)

With `kafkaUser.enabled=true` and Kafka UI deployed in the Kafka namespace, the
chart renders a `KafkaUser`:

```yaml
kafkaUser:
  enabled: true
  name: kafka-ui
  quotas:
    producerByteRate: 1048576       # 1 MiB/s
    consumerByteRate: 52428800      # 50 MiB/s
    requestPercentage: 10           # cap broker request-handler usage
  acls: [ ... ]                     # see below
```

### Externally-managed

If another release owns the `KafkaUser` (e.g. a platform chart), set
`kafkaUser.enabled=false`. Kafka UI then consumes the pre-existing Secret named
`kafkaUser.name`. (Helm refuses to adopt a resource owned by another release.)

## ACLs

When the Kafka cluster has `authorization.type: simple` (or `opa`/`keycloak`),
every operation is checked against the user's ACLs. Strimzi maps chart ACL
entries to `AclRule`s. Grant **least privilege** for what you use Kafka UI for.

### Read-only monitor (chart default)

Enough to list/describe topics and consumer groups and to browse messages:

```yaml
kafkaUser:
  acls:
    - resource: { type: topic,   name: "*", patternType: literal }
      operations: ["Describe", "Read"]
    - resource: { type: group,   name: "*", patternType: literal }
      operations: ["Describe", "Read"]
    - resource: { type: cluster }
      operations: ["Describe"]
```

| UI capability | Required operations |
|---------------|---------------------|
| List / describe topics, view configs | `topic:Describe`, `topic:DescribeConfigs` |
| Browse messages (consume) | `topic:Read` + `group:Read` |
| List / describe consumer groups | `group:Describe`, `group:Read` |
| Cluster overview (brokers, controller) | `cluster:Describe` |

> `Read` on a topic implies `Describe`; the explicit entries above are harmless
> and make intent clear.

### Read-write operator

Add topic management and produce access. Prefer **prefix** patterns to scope by
team/domain rather than `*`:

```yaml
kafkaUser:
  acls:
    - resource: { type: topic, name: "team-a.", patternType: prefix }
      operations: ["Describe", "DescribeConfigs", "Read", "Write", "Create", "Delete", "Alter", "AlterConfigs"]
    - resource: { type: group, name: "*", patternType: literal }
      operations: ["Describe", "Read"]
    - resource: { type: cluster }
      operations: ["Describe"]
```

### Admin (create topics cluster-wide)

Creating brand-new topics from the UI needs a cluster-level `Create` (or a
topic `Create` on the target pattern):

```yaml
kafkaUser:
  acls:
    - resource: { type: cluster }
      operations: ["Describe", "Create", "Alter", "AlterConfigs", "DescribeConfigs"]
    - resource: { type: topic, name: "*", patternType: literal }
      operations: ["Describe", "DescribeConfigs", "Read", "Write", "Create", "Delete", "Alter", "AlterConfigs"]
    - resource: { type: group, name: "*", patternType: literal }
      operations: ["Describe", "Read"]
```

### Supported operations & resource types

- **Operations:** `Read`, `Write`, `Create`, `Delete`, `Alter`, `Describe`,
  `AlterConfigs`, `DescribeConfigs`, `ClusterAction`, `IdempotentWrite`, `All`.
- **Resource types:** `topic`, `group`, `cluster`, `transactionalId`.
- **patternType:** `literal` (exact / `*`) or `prefix`.

### Viewing the effective ACLs

```bash
# From the KafkaUser CR
kubectl get kafkauser kafka-ui -n kafka \
  -o jsonpath='{range .spec.authorization.acls[*]}{.resource.type}{" "}{.resource.name}{" -> "}{.operations}{"\n"}{end}'

# From the broker (authoritative)
kubectl exec krafter-<pool>-0 -n kafka -c kafka -- \
  bin/kafka-acls.sh --bootstrap-server localhost:9092 \
  --command-config /tmp/admin.properties \
  --list --principal 'User:kafka-ui'
```

### Web-UI RBAC vs. Kafka ACLs — don't confuse them

- **Kafka ACLs** (this page) authorize the *service account* Kafka UI uses to
  talk to the brokers. They apply to everyone using the UI.
- **Web-UI RBAC** (`auth.rbac`) authorizes *human logins* inside the UI
  (who may view vs. edit). It needs multiple identities — a single Basic-Auth
  user cannot exercise multiple RBAC roles. Use OAuth/LDAP for real RBAC.

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| UI shows the cluster but no topics/messages | Missing `topic:Read` / `group:Read` ACLs |
| `TopicAuthorizationException` in logs | ACL too narrow for the topic pattern in use |
| `SaslAuthenticationException` | Wrong `kafkaUser.name`, missing Secret, or no SCRAM listener |
| `disconnected` / timeouts | `NetworkPolicy` blocking egress, or wrong bootstrap port (9092 vs 9093) |
