# Monitoring

> **This document has been merged into the book.** For complete monitoring documentation, see [Observability & Monitoring](book/09-observability.md).

Chapter 9 now includes:
- All Grafana dashboard descriptions (Kafka cluster, Kates benchmark, chaos, application health)
- Full Prometheus metrics reference (BenchmarkMetrics and KatesMetrics)
- Monitoring stack deployment instructions
- Alert configuration and customization guidance

For a quick deploy:

```bash
# Kind overlay (NodePort 30080)
make monitoring

# Generic Kubernetes (ClusterIP)
make monitoring-generic
```

| Service | URL |
|---|---|
| Grafana | `http://localhost:30080` (admin / admin) |
| Prometheus | `http://localhost:9090` (port-forward) |
