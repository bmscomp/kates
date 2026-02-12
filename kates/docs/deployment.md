# Deployment Guide

## Prerequisites

KATES runs as a Quarkus application and requires two external services:

1. **Kafka Cluster** — a running Kafka cluster accessible via bootstrap servers
2. **Trogdor Coordinator** — the Trogdor framework's coordinator process with at least one agent

## Running Locally

### Dev Mode

```bash
cd kates
mvn quarkus:dev
```

This starts KATES on port 8080 with hot reload, Swagger UI at `/q/swagger-ui`, and live coding.

Override the default cluster connection in `application.properties` or via environment variables:

```bash
mvn quarkus:dev \
  -Dkates.kafka.bootstrap-servers=localhost:9092 \
  -Dkates.trogdor.coordinator-url=http://localhost:8889
```

### Building a JAR

```bash
mvn clean package -DskipTests
java -jar target/quarkus-app/quarkus-run.jar
```

## Kubernetes Deployment

### KATES Application

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: kates
  namespace: kafka
spec:
  replicas: 1
  selector:
    matchLabels:
      app: kates
  template:
    metadata:
      labels:
        app: kates
    spec:
      containers:
      - name: kates
        image: kates:latest
        ports:
        - containerPort: 8080
        env:
        - name: KATES_KAFKA_BOOTSTRAP_SERVERS
          value: "krafter-kafka-bootstrap.kafka.svc:9092"
        - name: KATES_TROGDOR_COORDINATOR_URL
          value: "http://trogdor-coordinator.kafka.svc:8889"
        readinessProbe:
          httpGet:
            path: /api/health
            port: 8080
          initialDelaySeconds: 5
        livenessProbe:
          httpGet:
            path: /q/health/live
            port: 8080
          initialDelaySeconds: 10
---
apiVersion: v1
kind: Service
metadata:
  name: kates
  namespace: kafka
spec:
  selector:
    app: kates
  ports:
  - port: 8080
    targetPort: 8080
```

### Trogdor Coordinator

The Trogdor Coordinator and Agent processes are part of the Apache Kafka distribution. Deploy them as pods in the same namespace:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: trogdor-coordinator
  namespace: kafka
spec:
  replicas: 1
  selector:
    matchLabels:
      app: trogdor-coordinator
  template:
    metadata:
      labels:
        app: trogdor-coordinator
    spec:
      containers:
      - name: coordinator
        image: apache/kafka:4.1.1
        command:
        - /opt/kafka/bin/trogdor.sh
        - coordinator
        - --node-name coordinator
        - --coordinator.config /opt/kafka/config/trogdor-coordinator.conf
        ports:
        - containerPort: 8889
---
apiVersion: v1
kind: Service
metadata:
  name: trogdor-coordinator
  namespace: kafka
spec:
  selector:
    app: trogdor-coordinator
  ports:
  - port: 8889
    targetPort: 8889
```

### Trogdor Agent

Each Trogdor Agent runs the actual workloads. Deploy one or more:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: trogdor-agent
  namespace: kafka
spec:
  replicas: 3
  selector:
    matchLabels:
      app: trogdor-agent
  template:
    metadata:
      labels:
        app: trogdor-agent
    spec:
      containers:
      - name: agent
        image: apache/kafka:4.1.1
        command:
        - /opt/kafka/bin/trogdor.sh
        - agent
        - --node-name agent
        - --agent.config /opt/kafka/config/trogdor-agent.conf
        ports:
        - containerPort: 8888
```

## Environment Variables

Quarkus maps config properties to environment variables automatically. Replace dots with underscores and use uppercase:

| Property | Environment Variable |
|----------|---------------------|
| `kates.kafka.bootstrap-servers` | `KATES_KAFKA_BOOTSTRAP_SERVERS` |
| `kates.trogdor.coordinator-url` | `KATES_TROGDOR_COORDINATOR_URL` |
| `quarkus.http.port` | `QUARKUS_HTTP_PORT` |
