#!/usr/bin/env bash
# Smoke-test a Kates native image.
#
# WHY THIS EXISTS: the native image is compiled ahead of time, and the failures
# that introduces are invisible to every JVM test we have. A class Jackson can
# no longer introspect, a classpath resource that was not shipped, a class
# initialised at build time that needed a runtime initialiser — all of it
# compiles, boots, and only misbehaves when the endpoint is actually called.
# The JVM suite stays green throughout. So this boots the real binary against a
# throwaway Postgres and calls the paths where those failures show up:
#
#   /q/health/live      the binary starts at all
#   /q/health/ready     Flyway migrated (V20 runs outside a transaction, and its
#                       setting lives in a .conf sidecar that a native build can
#                       silently drop)
#   /q/openapi          the REST layer is wired
#   /api/disruptions/playbooks
#                       the catalog is loaded from classpath YAML — EMPTY in a
#                       native image whose resource patterns missed it, which is
#                       a 200 with an empty list, not an error
#   /api/tests          a domain payload actually serialises, rather than coming
#                       back as {} because nothing registered it for reflection
#
# Usage:
#   scripts/native-smoke-test.sh [image]     default: kates:native-local
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/common.sh"

IMAGE="${1:-kates:native-local}"
NET="kates-native-smoke-$$"
DB="kates-smoke-db-$$"
APP="kates-smoke-app-$$"
APP_PORT="${APP_PORT:-18080}"
BOOT_TIMEOUT="${BOOT_TIMEOUT:-90}"

require_cmd docker
require_cmd curl

cleanup() {
    docker rm -f "${APP}" "${DB}" >/dev/null 2>&1 || true
    docker network rm "${NET}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() {
    error "❌ $*"
    # A boot that blocks before Quarkus's own startup line logs NOTHING —
    # every boot-time category is at WARN. The first version of this printed
    # "last 60 lines" followed by nothing at all, which reads like a lost log
    # rather than the symptom it is, so say so explicitly and add the state
    # that distinguishes a hung boot from a crashed one.
    echo "--- container state ---" >&2
    docker inspect -f 'running={{.State.Running}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}} err={{.State.Error}}' \
        "${APP}" >&2 2>&1 || true
    echo "--- last 60 lines of application log ---" >&2
    LOGS="$(docker logs --tail 60 "${APP}" 2>&1 || true)"
    if [ -n "${LOGS}" ]; then
        echo "${LOGS}" >&2
    else
        echo "(the binary logged nothing — it never got as far as its startup line," >&2
        echo " so boot is blocked, not failing. Check StartupEvent observers.)" >&2
        # Deep tail on purpose: the frame that says WHERE it is blocked sits at
        # the TOP of the main thread's stack, and every other thread's dump is
        # printed after it. A tail of 80 cut off exactly the frames worth
        # having and left only idle pool threads.
        echo "--- stacks of the blocked process ---" >&2
        docker exec "${APP}" sh -c 'kill -QUIT 1' >/dev/null 2>&1 && sleep 2 || true
        docker logs --tail 400 "${APP}" >&2 2>&1 || true
    fi
    exit 1
}

info "🔍 Smoke-testing native image: ${IMAGE}"
docker image inspect "${IMAGE}" >/dev/null 2>&1 || {
    error "❌ image not found: ${IMAGE}"
    error "   Build it first: make kates-image-native-local"
    exit 1
}

docker network create "${NET}" >/dev/null

step "🗄  Starting throwaway Postgres..."
docker run -d --name "${DB}" --network "${NET}" \
    -e POSTGRES_USER=kates -e POSTGRES_PASSWORD=kates -e POSTGRES_DB=kates \
    postgres:16-alpine >/dev/null

# -h forces the TCP path, and that is the whole point of it.
#
# The postgres entrypoint runs initdb against a TEMPORARY server started with
# listen_addresses='' — reachable on the container's Unix socket, invisible over
# the network. `pg_isready -U kates` with no host talks to that socket, so it
# answered "accepting connections" while the real server had not started
# listening yet. The binary then launched against a closed port and Flyway
# killed the boot with "Connection refused", which reads like a broken image
# rather than a race in this script.
db_accepting_tcp() {
    docker exec "${DB}" pg_isready -h 127.0.0.1 -p 5432 -U kates >/dev/null 2>&1
}

for _ in $(seq 1 60); do
    db_accepting_tcp && break
    sleep 1
done
db_accepting_tcp || {
    error "❌ Postgres never accepted a TCP connection"
    docker logs --tail 30 "${DB}" >&2 2>&1 || true
    exit 1
}

# initdb's temporary server is torn down and restarted once initialisation
# finishes, so "accepting connections" is true, briefly, of a server that is
# about to go away. A real query on the real database is the check that cannot
# pass early.
for _ in $(seq 1 30); do
    docker exec "${DB}" psql -U kates -d kates -c 'SELECT 1' >/dev/null 2>&1 && break
    sleep 1
done
docker exec "${DB}" psql -U kates -d kates -c 'SELECT 1' >/dev/null 2>&1 || {
    error "❌ Postgres accepted TCP but the kates database never answered a query"
    docker logs --tail 30 "${DB}" >&2 2>&1 || true
    exit 1
}

step "🚀 Starting the native binary..."
# No broker runs here, and no SASL secret exists to reach one with. The shipped
# default is SASL_PLAINTEXT/SCRAM against the Strimzi cluster, so leaving it
# alone means the reactive-messaging channels build SASL clients with no
# credentials — which is a hard boot failure, not a background connection
# warning, because SmallRye constructs those clients from a StartupEvent
# observer. Say PLAINTEXT, and the channels fail to connect asynchronously the
# way this test already assumes they will.
docker run -d --name "${APP}" --network "${NET}" \
    -p "${APP_PORT}:8080" \
    -e QUARKUS_DATASOURCE_JDBC_URL="jdbc:postgresql://${DB}:5432/kates" \
    -e QUARKUS_DATASOURCE_USERNAME=kates \
    -e QUARKUS_DATASOURCE_PASSWORD=kates \
    -e KATES_API_SECURITY_ENABLED=false \
    -e KATES_KAFKA_BOOTSTRAP_SERVERS=localhost:9092 \
    -e KATES_HEALTH_KAFKA_REFRESH_INTERVAL=off \
    -e KATES_KAFKA_SECURITY_PROTOCOL=PLAINTEXT \
    "${IMAGE}" >/dev/null

BASE="http://localhost:${APP_PORT}"
step "⏳ Waiting for liveness (up to ${BOOT_TIMEOUT}s)..."
for _ in $(seq 1 "${BOOT_TIMEOUT}"); do
    if curl -sf "${BASE}/q/health/live" >/dev/null 2>&1; then
        break
    fi
    docker inspect -f '{{.State.Running}}' "${APP}" 2>/dev/null | grep -q true \
        || fail "the container exited during startup"
    sleep 1
done
curl -sf "${BASE}/q/health/live" >/dev/null 2>&1 || fail "never became live within ${BOOT_TIMEOUT}s"
info "  ok   /q/health/live"

# Readiness gates on the database, so this passing means Flyway ran every
# migration — including the non-transactional V20.
curl -sf "${BASE}/q/health/ready" >/dev/null 2>&1 || fail "readiness failed — check Flyway migrations"
info "  ok   /q/health/ready (migrations applied)"

curl -sf "${BASE}/q/openapi" >/dev/null 2>&1 || fail "/q/openapi did not respond"
info "  ok   /q/openapi"

PLAYBOOKS="$(curl -sf "${BASE}/api/disruptions/playbooks" || true)"
[ -n "${PLAYBOOKS}" ] || fail "/api/disruptions/playbooks did not respond"
case "${PLAYBOOKS}" in
    ""|"[]")
        fail "playbook catalog is EMPTY — playbooks/*.yaml were not shipped in the image.
   Check quarkus.native.resources.includes in application.properties."
        ;;
esac
info "  ok   /api/disruptions/playbooks (catalog loaded from classpath YAML)"

# Reflection check that actually exercises a domain type. Creating a run
# serialises a TestRun back to the caller; an unregistered class comes back as
# {} rather than an error, so assert on a field that only a real TestRun has.
# The engine will fail asynchronously without Kafka — irrelevant here, the
# response body is what is being tested.
CREATED="$(curl -sf -X POST "${BASE}/api/tests" \
    -H 'Content-Type: application/json' \
    -d '{"type":"LOAD","spec":{"topic":"native-smoke","numRecords":1,"recordSize":128}}' || true)"
[ -n "${CREATED}" ] || fail "POST /api/tests did not respond"
echo "${CREATED}" | grep -q '"testType"' \
    || fail "TestRun came back without its fields — a domain type is not registered for reflection.
   Response was: ${CREATED}"
info "  ok   POST /api/tests (domain payload serialises)"

RUN_ID="$(echo "${CREATED}" | sed -n 's/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
[ -n "${RUN_ID}" ] || fail "could not read the run id out of the create response"

FETCHED="$(curl -sf "${BASE}/api/tests/${RUN_ID}" || true)"
echo "${FETCHED}" | grep -q '"testType"' \
    || fail "GET /api/tests/{id} lost the payload — check reflection registration and the DB round-trip"
info "  ok   GET /api/tests/{id} (round-trips through Postgres)"

# The report path pulls in the widest object graph — summary, phases, SLA
# verdict — so it is the best single check that nested types are registered.
REPORT="$(curl -sf "${BASE}/api/tests/${RUN_ID}/report" || true)"
[ -n "${REPORT}" ] || fail "/api/tests/{id}/report did not respond"
echo "${REPORT}" | grep -q '"summary"' \
    || fail "report is missing its nested objects — a report type is not registered for reflection.
   Response was: ${REPORT}"
info "  ok   /api/tests/{id}/report (nested payloads serialise)"

# An error body is its own payload type, and ApiError being unregistered would
# turn every 4xx in the product into an empty object.
NOT_FOUND="$(curl -s "${BASE}/api/tests/does-not-exist" || true)"
echo "${NOT_FOUND}" | grep -q '"message"' \
    || fail "404 body is empty — ApiError is not registered for reflection.
   Response was: ${NOT_FOUND}"
info "  ok   error bodies serialise"

info "✅ Native image smoke test passed: ${IMAGE}"
