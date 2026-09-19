#!/usr/bin/env bash
# full-local-test.sh — run everything this repo can verify on one machine.
set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
source "${SCRIPT_DIR}/common.sh"
DBZ="$(grep '^ARG DEBEZIUM_VERSION=' "${ROOT}/Dockerfile.connect" | head -n1 | cut -d= -f2)"
CONNECT_IMAGE="${CONNECT_IMAGE:-connect:${DBZ%.Final}}"
KATES_IMAGE="${KATES_IMAGE:-kates:latest}"
APP_PORT="${APP_PORT:-18080}"
PHASES=("$@")
[ ${#PHASES[@]} -eq 0 ] && PHASES=(guards charts unit images connect kates)
wanted() { for p in "${PHASES[@]}"; do [ "$p" = "$1" ] && return 0; done; return 1; }
PASS=0; FAIL=0; SKIP=0; FAILED_LIST=""

# Ctrl-C used to be recorded as a failed check: the interrupted step returned
# non-zero, the phase called fail(), and the verdict blamed the tool rather than
# the interruption. Leave immediately with the conventional 130 instead.
interrupted() { echo; warn "  interrupted — stopping here, nothing was concluded about the remaining phases"; exit 130; }
trap interrupted INT
pass() { info  "  OK   $*"; PASS=$((PASS+1)); }
fail() { error "  FAIL $*"; FAIL=$((FAIL+1)); FAILED_LIST="${FAILED_LIST}\n    - $*"; }
skip() { warn  "  --   $*"; SKIP=$((SKIP+1)); }
phase_guards() {
    step "\n== guards =="
    for t in check-versions check-chart-tests check-help check-cli-compat; do
        if (cd "${ROOT}" && make "$t" >/dev/null 2>&1); then pass "make $t"; else fail "make $t"; fi
    done
}
phase_charts() {
    step "\n== charts =="
    command -v helm >/dev/null || { skip "helm not installed"; return; }
    # Dependencies first, the umbrella last: kates-platform packages its
    # file:// subcharts as they are on disk, including their own built
    # dependencies (mirror-maker2 on the kafka-common library).
    for dir in "${ROOT}"/charts/*/; do
        [ "$(basename "$dir")" = kates-platform ] && continue
        if grep -q '^dependencies:' "$dir/Chart.yaml" 2>/dev/null; then
            # `update` when a stale Chart.lock makes `build` refuse
            helm dependency build "$dir" >/dev/null 2>&1 || helm dependency update "$dir" >/dev/null 2>&1
        fi
    done
    helm dependency update "${ROOT}/charts/kates-platform" >/dev/null 2>&1
    for dir in "${ROOT}"/charts/*/; do
        c="$(basename "$dir")"
        if ! helm lint "$dir" >/dev/null 2>&1; then fail "chart $c: helm lint"; continue; fi
        # A library chart renders nothing on its own; its harness does.
        if grep -q '^type: library' "$dir/Chart.yaml"; then
            if helm plugin list 2>/dev/null | grep -q unittest && [ -d "$dir/tests/harness" ]; then
                helm dependency build "$dir/tests/harness" >/dev/null 2>&1
                if helm unittest "$dir/tests/harness" >/dev/null 2>&1; then pass "library $c: unit tests"
                else fail "library $c: helm unittest"; fi
            else
                skip "library $c: helm-unittest not installed"
            fi
            continue
        fi
        objs="$(helm template test "$dir" 2>/dev/null | grep -c '^kind:')"
        if [ "${objs:-0}" -gt 0 ]; then pass "chart $c renders $objs objects"
        else fail "chart $c: helm template produced no objects"; fi
    done
}
phase_unit() {
    step "\n== unit =="
    if (cd "${ROOT}" && make test-java >/tmp/kates-java-tests.log 2>&1); then
        pass "Java suite ($(grep -oE 'Tests run: [0-9]+' /tmp/kates-java-tests.log | tail -1))"
    else fail "Java suite - see /tmp/kates-java-tests.log"; fi
    if (cd "${ROOT}/cli" && go test ./... -timeout 300s >/tmp/kates-go-tests.log 2>&1); then
        pass "Go CLI suite ($(grep -c '^ok' /tmp/kates-go-tests.log) packages)"
    else fail "Go CLI suite - see /tmp/kates-go-tests.log"; fi
}
phase_images() {
    step "\n== images =="
    [ "${SKIP_IMAGES:-0}" = "1" ] && { skip "SKIP_IMAGES=1"; return; }
    bi() {
        docker build -q -f "$2" -t "$4" "$3" >/dev/null 2>/tmp/kates-build-$1.log
        rc=$?
        if [ $rc -eq 0 ]; then pass "image $1 -> $4"
        elif [ $rc -ge 128 ]; then interrupted
        else fail "image $1 - see /tmp/kates-build-$1.log"; fi
    }
    bi kates "${ROOT}/kates/Dockerfile" "${ROOT}" "${KATES_IMAGE}"
    bi tester "${ROOT}/tester/Dockerfile" "${ROOT}/tester" "kates-tester:latest"
    bi connect "${ROOT}/Dockerfile.connect" "${ROOT}" "${CONNECT_IMAGE}"
    bi legacy-kafka "${ROOT}/Dockerfile.legacy-kafka" "${ROOT}" "legacy-kafka:latest"
}
phase_connect() {
    step "\n== connect =="
    if docker image inspect "${CONNECT_IMAGE}" >/dev/null 2>&1; then
        "${SCRIPT_DIR}/connect-smoke-test.sh" "${CONNECT_IMAGE}" >/tmp/kates-connect-smoke.log 2>&1
        rc=$?
        # 75 is the smoke test reporting that it could not RUN — a fixture that
        # would not pull, a container the daemon refused. It reached no verdict
        # about the image, so this must not record one.
        if   [ $rc -eq 0 ];   then pass "connect smoke test"
        elif [ $rc -eq 75 ];  then skip "connect smoke test could not run (fixtures unavailable) - see /tmp/kates-connect-smoke.log"
        elif [ $rc -ge 128 ]; then interrupted
        else fail "connect smoke test - see /tmp/kates-connect-smoke.log"; fi
    else skip "${CONNECT_IMAGE} not built"; fi
}
phase_kates() {
    step "\n== kates =="
    docker image inspect "${KATES_IMAGE}" >/dev/null 2>&1 || { skip "${KATES_IMAGE} not built"; return; }
    NET="kates-full-$$"; DB="kates-full-db-$$"; KFK="kates-full-kafka-$$"; APP="kates-full-app-$$"
    B="http://localhost:${APP_PORT}"
    docker network create "$NET" >/dev/null
    docker run -d --name "$DB" --network "$NET" -e POSTGRES_USER=kates -e POSTGRES_PASSWORD=kates -e POSTGRES_DB=kates postgres:16-alpine >/dev/null
    docker run -d --name "$KFK" --network "$NET" -e KAFKA_NODE_ID=1 -e KAFKA_PROCESS_ROLES=broker,controller \
        -e KAFKA_LISTENERS=PLAINTEXT://0.0.0.0:9092,CONTROLLER://0.0.0.0:9093 \
        -e KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://"$KFK":9092 -e KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER \
        -e KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=CONTROLLER:PLAINTEXT,PLAINTEXT:PLAINTEXT \
        -e KAFKA_CONTROLLER_QUORUM_VOTERS=1@"$KFK":9093 -e KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1 \
        -e KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1 -e KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1 \
        -e KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0 apache/kafka:4.0.0 >/dev/null
    for _ in $(seq 1 60); do docker exec "$DB" psql -U kates -d kates -c 'SELECT 1' >/dev/null 2>&1 && break; sleep 2; done
    docker run -d --name "$APP" --network "$NET" -p "${APP_PORT}:8080" \
        -e QUARKUS_DATASOURCE_JDBC_URL="jdbc:postgresql://${DB}:5432/kates" \
        -e QUARKUS_DATASOURCE_USERNAME=kates -e QUARKUS_DATASOURCE_PASSWORD=kates \
        -e KATES_API_SECURITY_ENABLED=false -e KATES_KAFKA_BOOTSTRAP_SERVERS="${KFK}:9092" \
        -e KATES_KAFKA_SECURITY_PROTOCOL=PLAINTEXT "${KATES_IMAGE}" >/dev/null
    up=0; for _ in $(seq 1 60); do curl -sf "$B/q/health/live" >/dev/null 2>&1 && { up=1; break; }; sleep 2; done
    if [ $up -eq 1 ]; then pass "backend live"; else
        fail "backend never became live"; docker logs --tail 20 "$APP" 2>&1 | sed 's/^/       /'
        docker rm -f "$APP" "$KFK" "$DB" >/dev/null 2>&1; docker network rm "$NET" >/dev/null 2>&1; return
    fi
    curl -sf "$B/q/health/ready" >/dev/null 2>&1 && pass "backend ready (migrations applied)" || fail "readiness failed"
    curl -sf "$B/q/openapi" >/dev/null 2>&1 && pass "/q/openapi served" || fail "/q/openapi did not respond"
    pb="$(curl -sf "$B/api/disruptions/playbooks" 2>/dev/null)"
    case "$pb" in ""|"[]") fail "playbook catalog empty";; *) pass "playbook catalog loaded";; esac
    curl -sf --max-time 25 "$B/api/cluster/info" 2>/dev/null | grep -q brokerCount \
        && pass "/api/cluster/info reached the broker" || fail "/api/cluster/info could not reach Kafka"
    resp="$(curl -s --max-time 30 -X POST -H 'Content-Type: application/json' "$B/api/tests" -d '{"type":"LOAD","spec":{"topic":"kates-full-smoke","numRecords":5000,"recordSize":256,"numProducers":1,"acks":"1","partitions":1,"replicationFactor":1,"compressionType":"none"}}' 2>/dev/null)"
    id="$(echo "$resp" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')"
    if [ -n "$id" ]; then
        status=""
        for _ in $(seq 1 30); do
            status="$(curl -sf "$B/api/tests/$id" 2>/dev/null | sed -n 's/.*"status":"\([^"]*\)".*/\1/p' | head -1)"
            case "$status" in DONE|COMPLETED|FAILED|ERROR) break;; esac
            sleep 5
        done
        case "$status" in DONE|COMPLETED) pass "LOAD test $id finished: $status";;
                          *) fail "LOAD test $id ended as ${status:-unknown}";; esac
        offsets="$(docker exec "$KFK" /opt/kafka/bin/kafka-get-offsets.sh --bootstrap-server localhost:9092 --topic kates-full-smoke 2>/dev/null | cut -d: -f3)"
        if [ "${offsets:-0}" -ge 5000 ] 2>/dev/null; then pass "broker holds ${offsets} records"
        else fail "expected 5000 records, broker reports ${offsets:-none}"; fi
    else fail "could not create a test: $(echo "$resp" | head -c 160)"; fi
    docker rm -f "$APP" "$KFK" "$DB" >/dev/null 2>&1; docker network rm "$NET" >/dev/null 2>&1
}
require_cmd docker
bold "Kates full local test - phases: ${PHASES[*]}"
for p in guards charts unit images connect kates; do wanted "$p" && "phase_$p"; done
echo
bold "== verdict =="
info "  passed:  $PASS"
[ $SKIP -gt 0 ] && warn "  skipped: $SKIP"
if [ $FAIL -eq 0 ]; then info "  failed:  0"; info "\nEverything that ran, passed"; exit 0
else error "  failed:  $FAIL"; echo -e "${FAILED_LIST}" >&2; exit 1; fi
