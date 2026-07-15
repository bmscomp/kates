# Kafka UI — Documentation

Guides for deploying and configuring the `kafka-ui` chart (Kafbat UI) against a
Strimzi-managed Kafka cluster, with Schema Registry and Kafka Connect
integration.

> These guides complement the [chart README](../README.md) (the parameter
> reference). Start here for task-oriented, end-to-end walkthroughs.

## Contents

| Guide | What it covers |
|-------|----------------|
| [01 — Setup & Access](01-setup.md) | Prerequisites, install, overlays, web login, access, verification, hardening |
| [02 — Kafka Connection & ACLs](02-kafka-and-acls.md) | Bootstrap discovery, SCRAM/TLS auth, the `KafkaUser`, and **ACLs** (monitor / read-write / admin) |
| [03 — Schema Registry](03-schema-registry.md) | Apicurio (Confluent-compatible) integration, authentication, network policy |
| [04 — Kafka Connect](04-kafka-connect.md) | Connect REST integration, cross-namespace access, authentication, network policy |

## The stack these guides assume

The examples target the reference stack in this repo (adjust names to your own):

| Component | Value | Namespace |
|-----------|-------|-----------|
| Strimzi Kafka cluster | `krafter` | `kafka` |
| SCRAM listener | `:9092` (`SASL_PLAINTEXT`, `scram-sha-512`) | `kafka` |
| TLS listener | `:9093` (mutual TLS) | `kafka` |
| Authorization | `simple` (ACLs enforced) | — |
| Schema Registry (Apicurio) | `apicurio-apicurio-registry:80` | `kafka` |
| Kafka Connect REST | `connect-cluster-rest-api:8083` | `connect` |

## 60-second install

```bash
helm upgrade --install kafka-ui charts/kafka-ui -n kafka \
  --set kafka.clusterName=krafter \
  --set service.type=NodePort --set service.nodePort=30081 \
  --set auth.enabled=true --set auth.type=LOGIN_FORM \
  --wait

# Web password (auto-generated on first install):
kubectl get secret kafka-ui-web-password -n kafka -o jsonpath='{.data.password}' | base64 -d; echo

# Verify:
helm test kafka-ui -n kafka
```

See [01 — Setup & Access](01-setup.md) for the full walkthrough.
