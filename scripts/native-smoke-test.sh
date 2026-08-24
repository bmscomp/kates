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
    echo "--- last 60 lines of application log ---" >&2
    docker logs --tail 60 "${APP}" 2>&1 >&2 || true
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

for _ in $(seq 1 30); do
    docker exec "${DB}" pg_isready -U kates >/dev/null 2>&1 && break
    sleep 1
done
docker exec "${DB}" pg_isready -U kates >/dev/null 2>&1 || {
    error "❌ Postgres never became ready"
    exit 1
}

step "🚀 Starting the native binary..."
docker run -d --name "${APP}" --network "${NET}" \
    -p "${APP_PORT}:8080" \
    -e QUARKUS_DATASOURCE_JDBC_URL="jdbc:postgresql://${DB}:5432/kates" \
    -e QUARKUS_DATASOURCE_USERNAME=kates \
    -e QUARKUS_DATASOURCE_PASSWORD=kates \
    -e KATES_API_SECURITY_ENABLED=false \
    -e KATES_KAFKA_BOOTSTRAP_SERVERS=localhost:9092 \
    -e KATES_HEALTH_KAFKA_REFRESH_INTERVAL=off \
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
echo "${PLAYBOOKS}" | grep -q '"name"' \
    || fail "playbook entries have no fields — PlaybookEntry is not registered for reflection"
info "  ok   /api/disruptions/playbooks (catalog loaded and serialised)"

RUNS="$(curl -sf "${BASE}/api/tests" || true)"
[ -n "${RUNS}" ] || fail "/api/tests did not respond"
info "  ok   /api/tests (domain payload serialises)"

info "✅ Native image smoke test passed: ${IMAGE}"
