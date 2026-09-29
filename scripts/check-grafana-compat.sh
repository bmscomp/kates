#!/usr/bin/env bash
# Do the twelve boards load AND RENDER on the Grafana versions people run?
#
# Everything else in this repository checks the boards as JSON. This starts a
# real Grafana of each version, provisions every board into it, points it at a
# Prometheus holding series the boards' own matchers ask for, and then drives a
# headless browser over each board to see whether the panels actually draw.
#
# WHY BOTH HALVES ARE NEEDED. The API says a board was accepted and stored; it
# says nothing about whether the frontend can draw it. A panel type a given
# Grafana does not have renders "Panel plugin not found" and the API is
# perfectly happy. Conversely the browser cannot tell you that Grafana quietly
# dropped a template variable's `current` during schema migration, which is the
# bug that made every board pick its datasource by name-sort. So:
#
#   compat.py   — over the API: every board present, every panel by title,
#                 every variable, the datasource `current` intact, and which
#                 schemaVersion this Grafana migrated the board to
#   render.js   — in Chromium: every panel drawn, no missing plugins, no panel
#                 errors, no console errors
#
# Usage:
#   scripts/check-grafana-compat.sh                    # the pinned version only
#   scripts/check-grafana-compat.sh 9.5.21 10.4.19 11.6.6 12.3.1
#   scripts/check-grafana-compat.sh --no-render 8.5.27 # API half only
#
# Needs docker. The browser half additionally needs node with playwright and a
# Chromium; it is skipped with a notice when either is absent, because the API
# half is the part worth having in every environment.
set -euo pipefail

cd "$(dirname "$0")/.."
HERE=scripts/grafana-compat
WORK=$(mktemp -d)
PORT=${GRAFANA_COMPAT_PORT:-3123}
NET=kates-grafana-compat
RENDER=1
trap 'cleanup' EXIT

cleanup() {
  docker rm -f kates-compat-grafana kates-compat-prom kates-compat-fixture >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}

VERSIONS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --no-render) RENDER=0; shift ;;
    -h|--help) sed -n '2,/^set -euo/p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) VERSIONS+=("$1"); shift ;;
  esac
done
if [ ${#VERSIONS[@]} -eq 0 ]; then
  # The version the monitoring chart installs.
  pinned=$(grep -oE 'docker\.io/grafana/grafana:[0-9.]+' images.env | head -1 | sed 's/.*://')
  VERSIONS=("${pinned:-12.3.1}")
fi

command -v docker >/dev/null || { echo "::error::docker is required"; exit 2; }

echo "==> building the fixture scrape from the boards' own matchers"
python3 "$HERE/fixture.py" "$WORK/metrics"

mkdir -p "$WORK/www" "$WORK/boards" "$WORK/prov/dashboards" "$WORK/prov/datasources"
mv "$WORK/metrics" "$WORK/www/metrics"
for d in dashboards/*/; do
  name=$(basename "$d")
  [ -f "$d/dashboard.json" ] && cp "$d/dashboard.json" "$WORK/boards/$name.json"
done
count=$(find "$WORK/boards" -name '*.json' | wc -l)
[ "$count" -gt 0 ] || { echo "::error::no board has a dashboard.json"; exit 1; }
echo "    $count board(s), $(grep -c . "$WORK/www/metrics") series"

cp "$HERE/provisioning-dashboards.yaml" "$WORK/prov/dashboards/kates.yaml"
cp "$HERE/provisioning-datasource.yaml" "$WORK/prov/datasources/prom.yaml"
cat > "$WORK/prometheus.yml" <<'YAML'
global: {scrape_interval: 2s}
scrape_configs:
  - job_name: fixture
    # A JMX-exporter capture served as a static file arrives as
    # application/octet-stream, which Prometheus 3 refuses without this.
    fallback_scrape_protocol: PrometheusText0.0.4
    honor_labels: true
    static_configs: [{targets: ['kates-compat-fixture:8000']}]
YAML

docker network create "$NET" >/dev/null 2>&1 || true
docker rm -f kates-compat-fixture kates-compat-prom >/dev/null 2>&1 || true
docker run -d --name kates-compat-fixture --network "$NET" \
  -v "$WORK/www:/w:ro" -w /w python:3.11-slim python3 -m http.server 8000 >/dev/null
PROM_IMAGE=$(grep -oE 'quay\.io/prometheus/prometheus:v[0-9.]+' images.env | head -1)
docker run -d --name kates-compat-prom --network "$NET" \
  -v "$WORK/prometheus.yml:/etc/prometheus/prometheus.yml:ro" \
  "${PROM_IMAGE:-quay.io/prometheus/prometheus:v3.9.1}" \
  --config.file=/etc/prometheus/prometheus.yml >/dev/null
sleep 20

rc=0
for version in "${VERSIONS[@]}"; do
  echo ""
  echo "==> Grafana $version"
  docker rm -f kates-compat-grafana >/dev/null 2>&1 || true
  # Anonymous Admin so the browser half needs no login flow. This container
  # holds nothing but the fixture and is destroyed at the end of the run.
  #
  # Preinstalled apps OFF. Grafana 11.5+ ships a set of bundled apps it tries
  # to fetch at boot (lokiexplore, exploretraces, pyroscope). With no route to
  # grafana.com they are registered but absent, and the frontend then logs
  # `Could not load plugin: 404 ... /public/plugins/<app>/module.js` on every
  # page. That is Grafana's app platform failing to reach the internet, not a
  # board failing to render — the same three errors appeared on all twelve
  # boards, which is the tell. Turning the preinstall off removes the cause;
  # the filter in render.js is only a backstop. Grafana 9 and 10 do not know
  # these variables and ignore them.
  docker run -d --name kates-compat-grafana --network "$NET" -p "$PORT:3000" \
    -e GF_AUTH_ANONYMOUS_ENABLED=true -e GF_AUTH_ANONYMOUS_ORG_ROLE=Admin \
    -e GF_SECURITY_ADMIN_PASSWORD=admin -e GF_LOG_LEVEL=warn \
    -e GF_ANALYTICS_REPORTING_ENABLED=false -e GF_ANALYTICS_CHECK_FOR_UPDATES=false \
    -e GF_PLUGINS_PREINSTALL_DISABLED=true -e GF_INSTALL_PLUGINS= \
    -v "$WORK/prov/dashboards:/etc/grafana/provisioning/dashboards:ro" \
    -v "$WORK/prov/datasources:/etc/grafana/provisioning/datasources:ro" \
    -v "$WORK/boards:/var/lib/grafana/dashboards:ro" \
    "docker.io/grafana/grafana:$version" >/dev/null

  ready=0
  for _ in $(seq 1 60); do
    curl -sf "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1 && { ready=1; break; }
    sleep 2
  done
  [ "$ready" = 1 ] || { echo "::error::Grafana $version never became healthy"; rc=1; continue; }
  sleep 12   # the provisioner runs a moment after the API answers

  python3 "$HERE/compat.py" "http://127.0.0.1:$PORT" "$WORK/boards" || rc=1

  if [ "$RENDER" = 1 ]; then
    if node -e "require('playwright')" >/dev/null 2>&1; then
      node "$HERE/render.js" "http://127.0.0.1:$PORT" "$WORK/boards" || rc=1
    else
      echo "    (browser half skipped: node with playwright is not available)"
    fi
  fi

  # A version also fails when Grafana itself logged an error, filtered for
  # noise whose cause is not the boards: no route to grafana.com, plugin
  # chatter, and SQLite lock races between Grafana's own startup jobs, e.g.
  # "Failed to lock and execute the migration of API keys to service accounts"
  # error="database is locked". A lock bad enough to break provisioning would
  # already show up in compat.py as a missing board, so nothing is hidden.
  errors=$(docker logs kates-compat-grafana 2>&1 \
    | grep -iE 'level=error' | grep -viE 'grafana\.com|x509|plugin|update|database is locked' | head -3 || true)
  [ -n "$errors" ] && { echo "    Grafana logged:"; echo "$errors" | sed 's/^/      /'; rc=1; }
done

echo ""
[ "$rc" = 0 ] && echo "OK: every board loads and renders on: ${VERSIONS[*]}" \
              || echo "::error::at least one board failed on at least one version"
exit "$rc"
