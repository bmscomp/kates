# Version & Compatibility Matrix

Every version the platform pins, in one place. This table is generated from the repository's own pins — when a chapter and this matrix disagree, the matrix wins, and the chapter has drifted.

<!-- version-matrix:start -->
| Component | Version | Source |
|:----------|:--------|:-------|
| Apache Kafka | 4.3.0 | `versions.env` (`STRIMZI_KAFKA_VERSION`) |
| Strimzi operator | 1.1.0 | `versions.env` |
| LitmusChaos | 3.28.0 | `versions.env` |
| Prometheus stack chart | 82.4.3 | `versions.env` |
| Jaeger chart | 3.4.1 | `versions.env` |
| cert-manager | v1.17.1 | `versions.env` |
| Java (backend) | 21 | `kates/pom.xml` |
| Quarkus | 3.20.6 | `kates/pom.xml` |
| Go (CLI) | 1.25.7 | `cli/go.mod` |
| `apicurio-registry` chart | 0.1.5 (app 3.3.0) | `charts/apicurio-registry/Chart.yaml` |
| `connect-cluster` chart | 1.3.0 (app 3.6.0) | `charts/connect-cluster/Chart.yaml` |
| `headlamp` chart | 0.1.0 (app 0.40.1) | `charts/headlamp/Chart.yaml` |
| `kafka-cluster` chart | 0.2.0 (app 4.3.0) | `charts/kafka-cluster/Chart.yaml` |
| `kafka-ui` chart | 0.3.0 (app v1.5.0) | `charts/kafka-ui/Chart.yaml` |
| `kates-chaos` chart | 2.0.0 (app 3.28.0) | `charts/kates-chaos/Chart.yaml` |
| `kates-platform` chart | 0.2.0 (app 1.0.0) | `charts/kates-platform/Chart.yaml` |
| `kates` chart | 0.5.0 (app 1.21.0) | `charts/kates/Chart.yaml` |
| `minio` chart | 17.0.21 (app 2025.7.23) | `charts/minio/Chart.yaml` |
| `monitoring` chart | 1.0.0 (app 82.4.3) | `charts/monitoring/Chart.yaml` |
| `strimzi-operator` chart | 0.1.0 (app 1.1.0) | `charts/strimzi-operator/Chart.yaml` |
| `velero` chart | 11.3.2 (app 1.17.1) | `charts/velero/Chart.yaml` |
<!-- version-matrix:end -->

Regenerate with `scripts/gen-version-matrix.sh` and verify with `scripts/gen-version-matrix.sh --check` (CI runs the check on every book change).

Compatibility notes:

- The Strimzi operator version determines the supported Kafka version range — consult the [Strimzi supported versions table](https://strimzi.io/downloads/) before changing either independently. The upgrade order and rollback windows are covered in [Upgrade Playbook](18-upgrade-playbook.md).
- The `connect-cluster` chart tracks the same Kafka version as `kafka-cluster`; its Debezium and Apicurio converter versions are pinned in `Dockerfile.connect`.
