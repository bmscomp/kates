# 03 — Schema Registry (Apicurio)

Integrate an Apicurio Registry so Kafka UI can resolve and display Avro / JSON
Schema / Protobuf schemas for messages.

Kafka UI talks to the registry through its **Confluent-compatible** API
(`/apis/ccompat/v7`), so any Confluent-compatible registry works.

## Enable

```yaml
schemaRegistry:
  enabled: true
  # Explicit URL (recommended)
  url: "http://apicurio-apicurio-registry.kafka.svc:80/apis/ccompat/v7"
```

If `url` is empty, the chart auto-computes:
`http://apicurio-apicurio-registry.<kafkaNamespace>.svc.<clusterDomain>:8080/apis/ccompat/v7`.

> **Watch the port.** Apicurio's Service in this stack listens on **`:80`**, but
> the auto-computed URL assumes `:8080`. Set `url` explicitly (with the correct
> port) whenever your Service differs — the NetworkPolicy egress port is derived
> from this URL, so a wrong port both breaks resolution *and* the egress rule.

## Authentication

For a registry that requires credentials. The password is **stored in a Secret
and injected as an environment variable** (`SCHEMA_REGISTRY_PASSWORD`) — it is
never rendered into the ConfigMap.

```yaml
schemaRegistry:
  enabled: true
  url: "https://registry.example.com/apis/ccompat/v7"
  auth:
    enabled: true
    username: "kafka-ui"
    password: "s3cr3t"              # chart creates <fullname>-schema-registry Secret
    # existingSecret: my-sr-secret  # …or reference your own
    # passwordKey: password
```

## Network policy

When `networkPolicy.enabled=true`, the chart adds an egress rule to the Kafka
namespace on the **port parsed from `schemaRegistry.url`**. If your registry
lives in a *different* namespace, add an explicit rule:

```yaml
networkPolicy:
  extraEgress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: registry-ns
      ports:
        - port: 8080
          protocol: TCP
```

## Verify

```bash
# Registry reachable from the pod
kubectl exec deploy/kafka-ui -n kafka -- \
  wget -qO- http://apicurio-apicurio-registry.kafka.svc:80/apis/ccompat/v7/subjects
# [] or a JSON list of subjects

# In the UI: a topic carrying schematized messages shows a "Schema" tab with the
# resolved value/key schema.
```

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| Messages show as raw bytes, no schema | `schemaRegistry.enabled=false`, wrong `url`, or unreachable registry |
| Registry connection refused | Wrong port (Apicurio `:80` vs `:8080`), or NetworkPolicy egress blocked |
| 401/403 from the registry | `schemaRegistry.auth` not set or wrong credentials |
