#!/usr/bin/env bash
# connect-smoke-test.sh — prove the Kafka Connect image actually works.
#
# WHY THIS EXISTS: `docker build` succeeding tells you the plugin archives
# downloaded, nothing more. Every interesting failure in this image survives a
# green build: a plugin directory whose jars cannot satisfy their own imports
# (Connect logs the LinkageError at scan time and carries on without the
# plugin), two connectors sharing a directory and shadowing each other's Avro,
# a driver that was pruned on purpose but is still referenced in a config
# example. None of that shows up until a worker scans the plugin path and a
# connector is asked to move a record. So this boots the real image as a
# distributed worker against a throwaway Kafka and MinIO, and checks:
#
#   plugin scan       every plugin directory loads — no LinkageError, no plugin
#                     silently dropped
#   connector list    the REST API offers the connectors the docs promise,
#                     and none of the ones deliberately removed
#   S3 round trip     Kafka -> S3 sink -> bucket -> S3 source -> Kafka, with
#                     the payload compared at both ends
#   CDC               a row inserted into Postgres arriving as a change event,
#                     which is the whole point of the image
#   Apicurio          a record serialised through the Avro converter against a
#                     live registry and read back
#   JDBC drivers      which jdbc: URLs the sink plugin can actually resolve.
#                     Oracle must NOT resolve — its driver is closed source and
#                     is deleted from the image on purpose, so "No suitable
#                     driver" is the passing result here, not a regression.
#
# Usage:
#   scripts/connect-smoke-test.sh [image]     default: connect:latest
#
# Environment (every fixture is pinned — see FIXTURES below):
#   CONNECT_PORT   host port for the worker REST API
#   KAFKA_IMAGE    broker image
#   MINIO_IMAGE    S3 endpoint image
#   JDK_IMAGE      JDK used for the probes
#   APICURIO_IMAGE schema registry image
#   PG_IMAGE       Postgres used for the CDC test
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

IMAGE="${1:-connect:latest}"

# FIXTURES. Pinned, not :latest. This suite asserts exact behaviour — that the
# Avro converter finds a v3 registry API at a particular path, that mc takes the
# flags used below — so on a floating tag a red run asks "which upstream release
# shipped today?" before it can say anything about the image under test. The
# registry pin is the interesting one: it matches the Apicurio converter
# distribution in Dockerfile.connect and the apicurio-registry chart's
# appVersion, so this test exercises the pairing the platform actually deploys.
KAFKA_IMAGE="${KAFKA_IMAGE:-apache/kafka:4.0.0}"
MINIO_IMAGE="${MINIO_IMAGE:-quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z}"
JDK_IMAGE="${JDK_IMAGE:-eclipse-temurin:21-jdk-alpine}"
APICURIO_IMAGE="${APICURIO_IMAGE:-apicurio/apicurio-registry:3.3.0}"
PG_IMAGE="${PG_IMAGE:-postgres:16-alpine}"
PORT="${CONNECT_PORT:-18083}"

NET="connect-smoke-$$"
KAFKA="connect-smoke-kafka-$$"
MINIO="connect-smoke-minio-$$"
WORKER="connect-smoke-worker-$$"
APICURIO="connect-smoke-apicurio-$$"
PG="connect-smoke-pg-$$"
WORKDIR="$(mktemp -d)"

BUCKET="kafka-archive"
TOPIC="orders"
REPLAY="orders-replay"
AWS_KEY="testkey"
AWS_SECRET="testsecret123"

FAILURES=0
pass() { info  "  ✅ $*"; }
fail() { error "  ❌ $*"; FAILURES=$((FAILURES + 1)); }

cleanup() {
    docker rm -f "$WORKER" "$KAFKA" "$MINIO" "$APICURIO" "$PG" >/dev/null 2>&1 || true
    docker network rm "$NET" >/dev/null 2>&1 || true
    rm -rf "$WORKDIR"
}
trap cleanup EXIT

mc() { docker run --rm --network "$NET" --entrypoint sh "$MINIO_IMAGE" -c "mc alias set s3 http://$MINIO:9000 $AWS_KEY $AWS_SECRET >/dev/null 2>&1; $*"; }

# Exit 75 (EX_TEMPFAIL) is this suite saying it could not RUN — a fixture that
# would not pull, a container that would not start — as opposed to running and
# finding something. Re-run it; it says nothing about the image under test.
INFRA=75

# `docker run` exits 125 when the daemon refuses before the container exists,
# and every start below used to send that message to /dev/null. A quay.io 504
# mid-run therefore ended four minutes of work with the bare line
# "Error: Process completed with exit code 125" and no reason. Pull first, with
# retries, and keep the daemon's own words when a start still fails.
pull_fixture() {
    local image="$1" attempt
    if docker image inspect "$image" >/dev/null 2>&1; then
        info "  present  $image"
        return 0
    fi
    for attempt in 1 2 3; do
        if docker pull -q "$image" >/dev/null 2>"$WORKDIR/pull.err"; then
            info "  pulled   $image"
            return 0
        fi
        warn "  pull failed (attempt $attempt/3): $image — $(tail -n 1 "$WORKDIR/pull.err")"
        sleep $((attempt * 5))
    done
    error "cannot pull $image after three attempts — the registry is unreachable, so this run would prove nothing about $IMAGE"
    exit "$INFRA"
}

start() {   # start <container name> <docker run arguments…>
    local name="$1"
    shift
    if ! docker run -d --name "$name" --network "$NET" "$@" >/dev/null 2>"$WORKDIR/start.err"; then
        error "could not start $name:"
        sed 's/^/    /' "$WORKDIR/start.err" >&2
        exit "$INFRA"
    fi
}

require_cmd docker
docker image inspect "$IMAGE" >/dev/null 2>&1 || {
    error "Image '$IMAGE' not found locally. Build it first:  make connect-build"
    exit 1
}
bold "🔌 Smoke-testing $IMAGE"

# ── 0. The fixtures this suite boots ─────────────────────────────────────────
# Up front on purpose: a registry outage should cost the first ten seconds of a
# run, not the last minute of one.
step "\n[0/6] Fixtures"
for fixture in "$KAFKA_IMAGE" "$MINIO_IMAGE" "$JDK_IMAGE" "$APICURIO_IMAGE" "$PG_IMAGE"; do
    pull_fixture "$fixture"
done

# ── 1. What is actually in the image ─────────────────────────────────────────
step "\n[1/6] Image contents"
PLUGIN_DIRS=$(docker run --rm --entrypoint sh "$IMAGE" -c 'ls /opt/kafka/plugins | tr "\n" " "')
info "  plugin directories: $PLUGIN_DIRS"

ORPHAN_ORACLE=$(docker run --rm --entrypoint sh "$IMAGE" -c 'find /opt/kafka/plugins -name "ojdbc*.jar" | wc -l')
[ "$ORPHAN_ORACLE" -eq 0 ] \
    && pass "no Oracle driver in the image (closed source — removed on purpose)" \
    || fail "found $ORPHAN_ORACLE ojdbc jar(s); they must be deleted from the JDBC plugins"

LOOSE=$(docker run --rm --entrypoint sh "$IMAGE" -c 'find /opt/kafka/plugins -name "*.class" | wc -l')
[ "$LOOSE" -eq 0 ] \
    && pass "no loose .class files beside the jars (S3 source archive pruned)" \
    || fail "$LOOSE loose .class files on the plugin path — the S3 source prune did not run"

# ── 2. Does every plugin directory load? ─────────────────────────────────────
step "\n[2/6] Plugin scan (connect-plugin-path)"
docker run --rm --entrypoint sh "$IMAGE" \
    -c 'cd /opt/kafka && ./bin/connect-plugin-path.sh list --plugin-path /opt/kafka/plugins 2>/dev/null' \
    > "$WORKDIR/plugins.txt" || true

grep -E '^(Total|Loadable|Compatible)' "$WORKDIR/plugins.txt" | sed 's/^/  /'
NOT_LOADABLE=$(awk -F'\t' 'NF>5 && $6=="false" {print "     " $1 "  (" $NF ")"}' "$WORKDIR/plugins.txt" || true)
if [ -n "$NOT_LOADABLE" ]; then
    fail "plugins that failed to load — a jar is missing from their plugin directory:"
    echo "$NOT_LOADABLE"
else
    pass "every plugin loads (no LinkageError during the scan)"
fi

# ── 3. Kafka -> S3 -> Kafka ──────────────────────────────────────────────────
step "\n[3/6] Live worker: Kafka → S3 sink → bucket → S3 source → Kafka"
docker network create "$NET" >/dev/null 2>"$WORKDIR/net.err" || {
    error "could not create the docker network $NET:"
    sed 's/^/    /' "$WORKDIR/net.err" >&2
    exit "$INFRA"
}

start "$KAFKA" \
    -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller \
    -e KAFKA_LISTENERS=PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093 \
    -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://"$KAFKA":9092 \
    -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
    -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
    -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@"$KAFKA":9093 \
    -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
    -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 \
    -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
    -e KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 \
    "$KAFKA_IMAGE"
start "$MINIO" \
    -e MINIO_ROOT_USER="$AWS_KEY" -e MINIO_ROOT_PASSWORD="$AWS_SECRET" \
    "$MINIO_IMAGE" server /data
sleep 15
mc "mc mb s3/$BUCKET" >/dev/null

# offset.flush.interval.ms is deliberately short: the S3 sink writes its file on
# offset commit, and the default 60s turns this test into a minute of waiting.
start "$WORKER" -p "$PORT":8083 --entrypoint sh "$IMAGE" -c "
cat > /tmp/connect.properties <<EOF
bootstrap.servers=$KAFKA:9092
group.id=connect-smoke
key.converter=org.apache.kafka.connect.json.JsonConverter
value.converter=org.apache.kafka.connect.json.JsonConverter
key.converter.schemas.enable=false
value.converter.schemas.enable=false
offset.storage.topic=connect-smoke-offsets
offset.storage.replication.factor=1
config.storage.topic=connect-smoke-configs
config.storage.replication.factor=1
status.storage.topic=connect-smoke-status
status.storage.replication.factor=1
offset.flush.interval.ms=5000
plugin.path=/opt/kafka/plugins
listeners=HTTP://0.0.0.0:8083
EOF
exec /opt/kafka/bin/connect-distributed.sh /tmp/connect.properties"

info "  waiting for the worker REST API on :$PORT ..."
for i in $(seq 1 60); do
    curl -sf "localhost:$PORT/" >/dev/null 2>&1 && break
    sleep 2
    [ "$i" -eq 60 ] && { fail "worker never became ready"; docker logs "$WORKER" | tail -30; exit 1; }
done
pass "worker up: $(curl -s localhost:$PORT/)"

CONNECTORS=$(curl -s "localhost:$PORT/connector-plugins")
for expected in \
    io.aiven.kafka.connect.s3.AivenKafkaConnectS3SinkConnector \
    io.aiven.kafka.connect.s3.source.S3SourceConnector \
    io.aiven.connect.jdbc.JdbcSourceConnector \
    io.debezium.connector.mysql.MySqlConnector \
    io.debezium.connector.postgresql.PostgresConnector \
    io.debezium.connector.sqlserver.SqlServerConnector \
    io.debezium.connector.mongodb.MongoDbConnector \
    io.debezium.connector.jdbc.JdbcSinkConnector
do
    echo "$CONNECTORS" | grep -q "$expected" \
        && pass "offered: ${expected##*.}" \
        || fail "missing connector: $expected"
done
echo "$CONNECTORS" | grep -qiE 'connector\.oracle|connector\.db2' \
    && fail "Oracle/Db2 connector still present" \
    || pass "no Oracle or Db2 connector (removed on purpose)"

docker exec "$KAFKA" /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 \
    --create --topic "$TOPIC" --partitions 1 --replication-factor 1 >/dev/null 2>&1
printf '{"id":1,"item":"laptop","eur":1299}\n{"id":2,"item":"keyboard","eur":89}\n{"id":3,"item":"monitor","eur":349}\n' \
    | docker exec -i "$KAFKA" /opt/kafka/bin/kafka-console-producer.sh \
        --bootstrap-server localhost:9092 --topic "$TOPIC" >/dev/null 2>&1

curl -sf -X POST -H "Content-Type: application/json" "localhost:$PORT/connectors" -d "{
  \"name\": \"smoke-s3-sink\",
  \"config\": {
    \"connector.class\": \"io.aiven.kafka.connect.s3.AivenKafkaConnectS3SinkConnector\",
    \"tasks.max\": \"1\",
    \"topics\": \"$TOPIC\",
    \"aws.access.key.id\": \"$AWS_KEY\",
    \"aws.secret.access.key\": \"$AWS_SECRET\",
    \"aws.s3.bucket.name\": \"$BUCKET\",
    \"aws.s3.endpoint\": \"http://$MINIO:9000\",
    \"aws.s3.region\": \"us-east-1\",
    \"format.output.type\": \"jsonl\",
    \"format.output.fields\": \"key,value,offset,timestamp\",
    \"file.compression.type\": \"none\",
    \"key.converter\": \"org.apache.kafka.connect.storage.StringConverter\",
    \"value.converter\": \"org.apache.kafka.connect.json.JsonConverter\",
    \"value.converter.schemas.enable\": \"false\"
  }}" >/dev/null

for i in $(seq 1 30); do
    OBJECTS=$(mc "mc ls --recursive s3/$BUCKET" 2>/dev/null | tr -d '\r' || true)
    [ -n "$OBJECTS" ] && break
    sleep 2
done
if [ -n "${OBJECTS:-}" ]; then
    pass "sink wrote to S3: $(echo "$OBJECTS" | tr -s ' ' | cut -d' ' -f4- | tr '\n' ' ')"
    BODY=$(mc "mc cat s3/$BUCKET/$TOPIC-0-0" 2>/dev/null || true)
    echo "$BODY" | grep -q '"item":"keyboard"' \
        && pass "object contents match the produced records" \
        || fail "object written but payload unexpected: $(echo "$BODY" | head -c 200)"
else
    fail "S3 sink wrote nothing"
    curl -s "localhost:$PORT/connectors/smoke-s3-sink/status" | head -c 600
fi

# file.name.template must mirror how the sink named its objects, or the source
# task fails on startup with "must not be empty or not set".
curl -sf -X POST -H "Content-Type: application/json" "localhost:$PORT/connectors" -d "{
  \"name\": \"smoke-s3-source\",
  \"config\": {
    \"connector.class\": \"io.aiven.kafka.connect.s3.source.S3SourceConnector\",
    \"tasks.max\": \"1\",
    \"aws.access.key.id\": \"$AWS_KEY\",
    \"aws.secret.access.key\": \"$AWS_SECRET\",
    \"aws.s3.bucket.name\": \"$BUCKET\",
    \"aws.s3.endpoint\": \"http://$MINIO:9000\",
    \"aws.s3.region\": \"us-east-1\",
    \"topic\": \"$REPLAY\",
    \"input.format\": \"jsonl\",
    \"distribution.type\": \"object_hash\",
    \"file.name.template\": \"{{topic}}-{{partition}}-{{start_offset}}\",
    \"key.converter\": \"org.apache.kafka.connect.storage.StringConverter\",
    \"value.converter\": \"org.apache.kafka.connect.json.JsonConverter\",
    \"value.converter.schemas.enable\": \"false\"
  }}" >/dev/null
sleep 25

REPLAYED=$(docker exec "$KAFKA" /opt/kafka/bin/kafka-console-consumer.sh \
    --bootstrap-server localhost:9092 --topic "$REPLAY" --from-beginning \
    --timeout-ms 20000 2>/dev/null || true)
COUNT=$(echo "$REPLAYED" | grep -c 'item' || true)
[ "$COUNT" -eq 3 ] \
    && pass "source replayed all 3 records back into $REPLAY" \
    || { fail "source replayed $COUNT/3 records"; curl -s "localhost:$PORT/connectors/smoke-s3-source/status" | head -c 600; }

# The Debezium scripting SMT: assert the worker offers both transformations.
# It cannot be exercised through the S3 sink — Connect instantiates a transform
# inside the *connector's* plugin classloader, which cannot see the Groovy
# JSR-223 engine sitting in debezium-scripting, so any condition fails to
# compile. Debezium documents the supported layout (scripting jars beside the
# connector that uses them); until this image adopts it, the check is that both
# SMTs load and are offered, which is what the plugin scan above verifies.
for smt in io.debezium.transforms.Filter io.debezium.transforms.ContentBasedRouter; do
    grep -q "$smt" "$WORKDIR/plugins.txt" \
        && pass "scripting SMT available: ${smt##*.}" \
        || fail "scripting SMT missing: $smt"
done

# ── 4. Real change data capture ──────────────────────────────────────────────
step "\n[4/6] Debezium CDC: a Postgres INSERT arriving as a change event"
start "$PG" \
    -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=inventory \
    "$PG_IMAGE" -c wal_level=logical
for i in $(seq 1 30); do
    docker exec "$PG" pg_isready -U postgres >/dev/null 2>&1 && break
    sleep 2
done
docker exec "$PG" psql -U postgres -d inventory -c \
    "CREATE TABLE customers (id serial PRIMARY KEY, name text); \
     ALTER TABLE customers REPLICA IDENTITY FULL; \
     INSERT INTO customers (name) VALUES ('ada'), ('grace');" >/dev/null 2>&1

curl -sf -X POST -H "Content-Type: application/json" "localhost:$PORT/connectors" -d "{
  \"name\": \"smoke-pg-cdc\",
  \"config\": {
    \"connector.class\": \"io.debezium.connector.postgresql.PostgresConnector\",
    \"tasks.max\": \"1\",
    \"database.hostname\": \"$PG\",
    \"database.port\": \"5432\",
    \"database.user\": \"postgres\",
    \"database.password\": \"postgres\",
    \"database.dbname\": \"inventory\",
    \"topic.prefix\": \"pgcdc\",
    \"table.include.list\": \"public.customers\",
    \"plugin.name\": \"pgoutput\",
    \"slot.name\": \"smoke_slot\",
    \"snapshot.mode\": \"initial\",
    \"key.converter\": \"org.apache.kafka.connect.json.JsonConverter\",
    \"key.converter.schemas.enable\": \"false\",
    \"value.converter\": \"org.apache.kafka.connect.json.JsonConverter\",
    \"value.converter.schemas.enable\": \"false\"
  }}" >/dev/null
sleep 20

CDC=$(docker exec "$KAFKA" /opt/kafka/bin/kafka-console-consumer.sh \
    --bootstrap-server localhost:9092 --topic pgcdc.public.customers \
    --from-beginning --timeout-ms 20000 2>/dev/null || true)
SNAP=$(echo "$CDC" | grep -c '"name"' || true)
if [ "$SNAP" -ge 2 ]; then
    pass "snapshot produced $SNAP change events for the two seeded rows"
else
    fail "expected 2 snapshot events, saw $SNAP"
    curl -s "localhost:$PORT/connectors/smoke-pg-cdc/status" | head -c 600
fi

# Streaming is the part a snapshot does not prove: this row is written after the
# connector is already running, so it can only arrive through the WAL.
docker exec "$PG" psql -U postgres -d inventory -c \
    "INSERT INTO customers (name) VALUES ('alan');" >/dev/null 2>&1
sleep 12
STREAMED=$(docker exec "$KAFKA" /opt/kafka/bin/kafka-console-consumer.sh \
    --bootstrap-server localhost:9092 --topic pgcdc.public.customers \
    --from-beginning --timeout-ms 20000 2>/dev/null || true)
echo "$STREAMED" | grep -q 'alan' \
    && pass "streamed a live INSERT out of the WAL (logical replication working)" \
    || fail "the post-snapshot INSERT never arrived"

# ── 5. Which JDBC drivers does the sink plugin actually have? ────────────────
step "\n[5/6] JDBC driver availability in debezium-jdbc"
CID=$(docker create "$IMAGE")
docker cp "$CID":/opt/kafka/plugins/debezium-jdbc "$WORKDIR/" >/dev/null
docker rm "$CID" >/dev/null
cat > "$WORKDIR/D.java" <<'JAVA'
import java.sql.*;
public class D {
  public static void main(String[] a) {
    String[] urls = {"jdbc:mysql://h/db", "jdbc:postgresql://h/db",
                     "jdbc:sqlserver://h", "jdbc:mariadb://h/db",
                     "jdbc:oracle:thin:@h:1521/db"};
    for (String url : urls) {
      try { DriverManager.getDriver(url); System.out.println("AVAILABLE " + url); }
      catch (SQLException e) { System.out.println("MISSING " + url); }
    }
  }
}
JAVA
DRIVERS=$(docker run --rm -v "$WORKDIR":/w -w /w "$JDK_IMAGE" \
    sh -c 'javac D.java && java -cp "/w:/w/debezium-jdbc/*" D' 2>/dev/null)
for db in mysql postgresql sqlserver mariadb; do
    echo "$DRIVERS" | grep -q "AVAILABLE jdbc:$db" \
        && pass "$db driver present" \
        || fail "$db driver missing — it should ship with the image"
done
echo "$DRIVERS" | grep -q "MISSING jdbc:oracle" \
    && pass "oracle driver absent (expected: closed source, removed)" \
    || fail "oracle driver is present — it must not ship in this image"

# ── 6. Do the Apicurio converters actually talk to a registry? ───────────────
step "\n[6/6] Apicurio Avro converter against a live registry"
start "$APICURIO" "$APICURIO_IMAGE"
REGISTRY_UP=""
for i in $(seq 1 30); do
    docker run --rm --network "$NET" --entrypoint sh "$IMAGE" \
        -c "curl -sf http://$APICURIO:8080/apis/registry/v3/system/info >/dev/null" 2>/dev/null \
        && { REGISTRY_UP=yes; break; }
    sleep 3
done
# Without this the next 40 lines still run and the phase fails as "round trip
# failed: no output", which reads like a broken converter in THIS image when the
# truth is that the fixture never answered.
[ -n "$REGISTRY_UP" ] || {
    error "the registry fixture ($APICURIO_IMAGE) never served /apis/registry/v3 — last 20 lines:"
    docker logs "$APICURIO" 2>&1 | tail -20 | sed 's/^/    /' >&2
    exit "$INFRA"
}
CID=$(docker create "$IMAGE")
docker cp "$CID":/opt/kafka/plugins/apicurio-converter "$WORKDIR/" >/dev/null
docker cp "$CID":/opt/kafka/libs "$WORKDIR/kafka-libs" >/dev/null
docker rm "$CID" >/dev/null
cat > "$WORKDIR/A.java" <<JAVA
import io.apicurio.registry.utils.converter.AvroConverter;
import org.apache.kafka.connect.data.*;
import java.util.*;
public class A {
  public static void main(String[] a) {
    AvroConverter c = new AvroConverter();
    Map<String,Object> cfg = new HashMap<>();
    cfg.put("apicurio.registry.url", "http://$APICURIO:8080/apis/registry/v3");
    cfg.put("apicurio.registry.auto-register", "true");
    c.configure(cfg, false);
    Schema s = SchemaBuilder.struct().name("Order")
        .field("id", Schema.INT32_SCHEMA).field("item", Schema.STRING_SCHEMA).build();
    Struct v = new Struct(s).put("id", 7).put("item", "laptop");
    byte[] bytes = c.fromConnectData("orders", s, v);
    SchemaAndValue back = c.toConnectData("orders", bytes);
    System.out.println("ROUNDTRIP " + back.value());
  }
}
JAVA
AVRO=$(docker run --rm --network "$NET" -v "$WORKDIR":/w -w /w "$JDK_IMAGE" sh -c \
    'javac -cp "/w/apicurio-converter/*:/w/kafka-libs/*" A.java >/dev/null 2>&1 && java -cp "/w:/w/apicurio-converter/*:/w/kafka-libs/*" A 2>/dev/null' || true)
echo "$AVRO" | grep -q "ROUNDTRIP Struct{id=7,item=laptop}" \
    && pass "Avro converter registered a schema and round-tripped a record" \
    || fail "Apicurio Avro converter round trip failed: ${AVRO:-no output}"

# ── Verdict ──────────────────────────────────────────────────────────────────
echo
if [ "$FAILURES" -eq 0 ]; then
    info "✅ All checks passed for $IMAGE"
else
    error "❌ $FAILURES check(s) failed for $IMAGE"
fi
exit "$FAILURES"
