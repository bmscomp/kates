#!/usr/bin/env bash
# Runs the Testcontainers integration tests (*IT) locally and writes a full
# log to kates/target/it-test-output.log. That path is inside the folder
# shared with the assistant, so after this finishes the assistant can read the
# log and help debug any failure.
#
# Requires: local Docker running (Testcontainers spins up its own Postgres +
# Kafka containers — your kind cluster is NOT used). Java is provided by ./mvnw.
#
# Usage:
#   ./scripts/run-it-tests.sh            # run PersistenceIT + KafkaAdminIT
#   ./scripts/run-it-tests.sh PersistenceIT   # run a single IT
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1
mkdir -p kates/target
LOG=kates/target/it-test-output.log
IT_PATTERN="${1:-*IT}"

# Probe a unix socket for the Docker Engine API (/info -> 200). Docker Desktop
# for Mac exposes several sockets; only the raw engine one answers /info. The
# CLI proxy (docker-cli.sock) returns 400, so the `docker` CLI can work while
# Testcontainers cannot. Prints exists/HTTP-code per candidate for diagnosis.
probe_socket() {
  local sock="$1"
  [ -n "${sock}" ] || return 1
  if [ ! -S "${sock}" ]; then
    echo "   probe ${sock}: not a socket / missing"
    return 1
  fi
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 --unix-socket "${sock}" http://localhost/info 2>/dev/null || echo "err")
  echo "   probe ${sock}: /info -> HTTP ${code}"
  [ "${code}" = "200" ]
}

configure_docker_socket() {
  local dh="${DOCKER_HOST:-}"; dh="${dh#unix://}"
  local ctx; ctx=$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null | sed 's|^unix://||')
  # Candidate list: current DOCKER_HOST, context endpoint, the known Docker
  # Desktop raw socket, the default socket, the user socket, plus any *.sock
  # discovered under the Docker Desktop data dir and ~/.docker.
  local globbed=()
  local d
  for d in "$HOME/Library/Containers/com.docker.docker/Data" "$HOME/.docker/run" "$HOME/.docker"; do
    [ -d "${d}" ] || continue
    for s in "${d}"/*.sock; do [ -S "${s}" ] && globbed+=("${s}"); done
  done
  local candidates=("${dh}" "${ctx}" \
    "$HOME/Library/Containers/com.docker.docker/Data/docker.raw.sock" \
    "/var/run/docker.sock" "$HOME/.docker/run/docker.sock" "${globbed[@]:-}")

  echo "--- Docker socket probe ---"
  local seen=" "
  local sock
  for sock in "${candidates[@]}"; do
    [ -n "${sock}" ] || continue
    case "${seen}" in *" ${sock} "*) continue ;; esac
    seen="${seen}${sock} "
    if probe_socket "${sock}"; then
      export DOCKER_HOST="unix://${sock}"
      export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE="${sock}"
      echo "==> Using Docker Engine socket: ${sock}"
      return 0
    fi
  done
  echo "==> No socket answered /info with HTTP 200."
  echo "    The docker CLI can still work via a proxy socket Testcontainers can't use."
  echo "    Fastest fix: Docker Desktop -> Settings -> Advanced ->"
  echo "    'Allow the default Docker socket to be used (requires password)' -> Apply & Restart."
  return 1
}

{
  echo "=== Environment ==="
  date
  echo "--- java ---";   (cd kates && ./mvnw -q -version 2>&1 | head -3)
  echo "--- docker ---"; docker version --format 'client={{.Client.Version}} server={{.Server.Version}}' 2>&1 || echo "docker not reachable"
  docker info --format 'storage={{.Driver}} mem={{.MemTotal}}' 2>&1 || true
  echo "docker context: $(docker context show 2>/dev/null) -> $(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null)"
  echo

  configure_docker_socket || true
  echo "DOCKER_HOST=${DOCKER_HOST:-<default>}"
  echo "TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE=${TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE:-<unset>}"
  echo

  echo "=== Running integration tests: ${IT_PATTERN} ==="
  (cd kates && ./mvnw -B -ntp verify -Dit.test="${IT_PATTERN}")
  RC=$?

  echo
  echo "=== Result (exit ${RC}) ==="
  if [ "${RC}" -eq 0 ]; then echo "INTEGRATION TESTS PASSED"; else echo "INTEGRATION TESTS FAILED (exit ${RC})"; fi
  exit "${RC}"
} 2>&1 | tee "${LOG}"

echo
echo "Log written to: ${LOG}"
echo "Tell the assistant it can read kates/target/it-test-output.log"
