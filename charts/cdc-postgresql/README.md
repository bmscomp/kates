# cdc-postgresql

The PostgreSQL that the Kafka Connect change-data-capture demo reads. `kates deploy --with-kafka-connect` installs it as release `postgresql` in the database namespace (`--db-ns`, default `database`).

It runs one pod of the official [`postgres`](https://hub.docker.com/_/postgres) image, version 16, with `wal_level=logical`. On first start an init script creates the user Debezium connects as (`auth.username`, with `LOGIN` and `REPLICATION`) and gives it the database (`auth.database`), so it can create its publication and replication slot.

It is lab-grade: no replication, no backups, and the passwords come from values (`postgres` and `debezium` by default). Point production connectors at a database you run yourself.

## What other components rely on

| Name | Used by |
|---|---|
| Service `<release>` on port 5432 | the connectors, as `postgresql.<namespace>.svc` |
| Pod label `app.kubernetes.io/name=postgresql` | Kafka Connect's NetworkPolicy egress, the Kates API's INTEGRATION_CDC test |
| Secret `<release>`, keys `postgres-password` and `password` | the Kates API's INTEGRATION_CDC test reads `postgres-password` |

## Values

| Key | Default | Description |
|---|---|---|
| `image.repository`, `image.tag` | `postgres`, `16` | The official image; `global.imageRegistry` is prepended with a slash for a mirror |
| `auth.postgresPassword` | `postgres` | The postgres superuser's password |
| `auth.username`, `auth.password` | `debezium`, `debezium` | The CDC user |
| `auth.database` | `orders` | The database the connectors use |
| `replication.maxWalSenders`, `replication.maxReplicationSlots` | `10`, `10` | Logical decoding limits |
| `persistence.size`, `persistence.storageClass` | `8Gi`, cluster default | The data volume |
| `resources` | 100m/128Mi requests, 512Mi limit | Container resources |
| `podLabels` | `{}` | Extra pod labels; the chart's own win on a clash |
