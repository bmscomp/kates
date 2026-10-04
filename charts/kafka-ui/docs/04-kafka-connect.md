# 04 — Kafka Connect

Integrate a Kafka Connect cluster so Kafka UI can list connectors, inspect their
status/tasks/config, and create or restart connectors from the browser.

## Enable

```yaml
kafkaConnect:
  enabled: true
  name: "connect-cluster"        # display name in the UI
  # Explicit REST API URL (recommended)
  url: "http://connect-cluster-rest-api.connect.svc:8083"
```

If `url` is empty, the chart auto-computes:
`http://connect-cluster-connect-api.<kafkaNamespace>.svc.<clusterDomain>:8083`.

> **Cross-namespace is common.** Connect frequently runs in its own namespace
> (e.g. `connect`), while the auto-computed URL assumes the Kafka namespace. Set
> `url` explicitly to the real Service, and add a NetworkPolicy rule (below) —
> the derived egress rule only targets the Kafka namespace.

## Authentication

For a Connect REST API secured with Basic Auth. The password is sourced from a
Secret and injected as `KAFKA_CONNECT_PASSWORD` — never in the ConfigMap.

```yaml
kafkaConnect:
  enabled: true
  url: "http://connect-cluster-rest-api.connect.svc:8083"
  auth:
    enabled: true
    username: "connect-admin"
    password: "s3cr3t"                # chart creates <fullname>-kafka-connect Secret
    # existingSecret: my-connect-secret
    # passwordKey: password
```

## Network policy

The chart derives the egress **port** from `kafkaConnect.url`, but targets the
**Kafka namespace**. When Connect runs elsewhere, allow it explicitly:

```yaml
networkPolicy:
  extraEgress:
    - to:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: connect
      ports:
        - port: 8083
          protocol: TCP
```

## Multiple Connect clusters

The chart currently configures a **single** Connect cluster. To wire additional
ones, append to the rendered config via `extraConfig` (merged into `config.yml`):

```yaml
extraConfig:
  kafka:
    clusters:
      - name: krafter
        kafkaConnect:
          - name: connect-a
            address: http://connect-a-rest-api.connect.svc:8083
          - name: connect-b
            address: http://connect-b-rest-api.connect.svc:8083
```

> Multi-cluster and multi-Connect first-class support is a planned enhancement;
> `extraConfig` is the escape hatch until then.

## Verify

```bash
# Connect REST reachable from the pod
kubectl exec deploy/kafka-ui -n kafka -- \
  wget -qO- http://connect-cluster-rest-api.connect.svc:8083/connectors
# [] or a JSON list of connector names

# In the UI: the "Kafka Connect" section lists the cluster and its connectors.
```

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| "Kafka Connect" tab empty / cluster missing | `kafkaConnect.enabled=false` or wrong `url` |
| Connection refused / timeout | Connect in another namespace + no `extraEgress` rule, or wrong port |
| 401/403 from Connect | `kafkaConnect.auth` not set or wrong credentials |
