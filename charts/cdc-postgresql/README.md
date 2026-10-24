# cdc-postgresql

The PostgreSQL that the Kafka Connect change-data-capture demo reads. `kates deploy --with-kafka-connect` installs it as release `postgresql` in the database namespace (`--db-ns`, default `database`).

It runs one pod of the official [`postgres`](https://hub.docker.com/_/postgres) image, pinned to 16.15, with `wal_level=logical`. On first start an init script creates the user Debezium connects as (`auth.username`, with `LOGIN` and `REPLICATION`) and gives it the database (`auth.database`), so it can create its publication and replication slot.

It is lab-grade: no replication, no backups, and the passwords come from values (`postgres` and `debezium` by default). Point production connectors at a database you run yourself.

`helm test <release>` checks what the connectors need. It connects through the Service as the CDC user and confirms `wal_level=logical`, the user's `REPLICATION` attribute, and that it owns the database.

## What other components rely on

| Name | Used by |
|---|---|
| Service `<release>` on port 5432 | the connectors, as `postgresql.<namespace>.svc` |
| Pod label `app.kubernetes.io/name=postgresql` | Kafka Connect's NetworkPolicy egress, the Kates API's INTEGRATION_CDC test |
| Secret `<release>`, keys `postgres-password` and `password` | the Kates API's INTEGRATION_CDC test reads `postgres-password` |

## Values

| Key | Default | Description |
|---|---|---|
| `image.repository`, `image.tag` | `postgres`, `16.15` | The official image; `global.imageRegistry` is prepended with a slash for a mirror |
| `image.digest` | empty | `sha256:…` pins the image by content, in place of the tag |
| `auth.postgresPassword` | `postgres` | The postgres superuser's password |
| `auth.username`, `auth.password` | `debezium`, `debezium` | The CDC user |
| `auth.database` | `orders` | The database the connectors use |
| `replication.maxWalSenders`, `replication.maxReplicationSlots` | `10`, `10` | Logical decoding limits |
| `parameters` | `{}` | More server settings, each passed as `-c name=value`; the schema refuses the three replication settings above |
| `persistence.enabled` | `true` | `false` keeps the data in an emptyDir, which goes with the pod |
| `persistence.size`, `persistence.storageClass` | `8Gi`, cluster default | The data volume |
| `networkPolicy.enabled` | `false` | Admit 5432 only from `networkPolicy.allowNamespaces` and `networkPolicy.allowPods` (label selectors), and the chart's Helm test |
| `resources` | 100m/128Mi requests, 512Mi limit | Container resources |
| `tests.resources` | 10m/32Mi requests, 64Mi limit | The Helm test pod's resources |
| `podLabels` | `{}` | Extra pod labels; the chart's own win on a clash |

`values.schema.json` checks the values: unknown keys, a `latest` tag, a malformed digest and `postgres` as the CDC user are refused.
