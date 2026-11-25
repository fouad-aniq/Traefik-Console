#!/usr/bin/env bash
# Runs core-v2 (prebuilt binary) + a fake Traefik API + the Vite dev server.
#   ./dev/run-demo.sh            → fake Traefik API; open http://localhost:5173/#/resolvers
#   ./dev/run-demo.sh --docker   → real Traefik in Docker (dev/docker-compose.yml) so credential
#                                  mounts show "Set" and Apply can converge. Needs docker compose.
#   ./dev/run-demo.sh --crowdsec → --docker plus the CrowdSec engine from deploy/crowdsec/compose.yaml
#                                  (with dev/crowdsec.demo.yaml), registered in core-v2 with
#                                  CROWDSEC_LAPI_URL set, so the CrowdSec page can be enabled. Linux only.
# Without --crowdsec, CROWDSEC_LAPI_URL is passed through if you export it (the page stays empty otherwise).
# Ctrl-C stops everything. State lives in <repo>/.scratch/core-v2-demo (safe to delete).
set -euo pipefail
HERE="$(cd "$(dirname "$0")/.." && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
BIN="${CORE_V2_BIN:-$ROOT/.scratch/backend-build/server}"
DEMO="${DEMO_DIR:-$ROOT/.scratch/core-v2-demo}"
FRONT_PORT="${FRONT_PORT:-5173}"
USE_DOCKER=0; USE_CROWDSEC=0
for arg in "$@"; do
  case "$arg" in
    --docker) USE_DOCKER=1 ;;
    --crowdsec) USE_DOCKER=1; USE_CROWDSEC=1 ;;
    *) echo "unknown option: $arg (use --docker or --crowdsec)"; exit 1 ;;
  esac
done
CROWDSEC_COMPOSE="${CROWDSEC_COMPOSE:-$ROOT/deploy/crowdsec/compose.yaml}"
# First free TCP port at or after $1 (ports 8080/8081 are often taken by other local services).
free_port() { python3 - "$1" <<'P'
import socket, sys
p = int(sys.argv[1])
while True:
    s = socket.socket()
    try:
        s.bind(("127.0.0.1", p)); s.close(); print(p); break
    except OSError:
        p += 1
P
}
CORE_PORT="${CORE_PORT:-$(free_port 8081)}"
TRAEFIK_MOCK_PORT="${TRAEFIK_MOCK_PORT:-$(free_port 18080)}"
TRAEFIK_HTTPS_PORT="${TRAEFIK_HTTPS_PORT:-$(free_port 18443)}"
[ -x "$BIN" ] || { echo "core-v2 binary not found: $BIN (build: make -C backend build)"; exit 1; }

mkdir -p "$DEMO"/{traefik/dynamic,traefik/secrets,traefik/plugins-storage,traefik/acme,keys,data}
chmod 700 "$DEMO/keys" "$DEMO/data" "$DEMO/traefik/secrets"
[ -f "$DEMO/traefik/traefik.yml" ] || NEW_STATIC=1
[ -f "$DEMO/traefik/traefik.yml" ] || cat > "$DEMO/traefik/traefik.yml" <<'Y'
entryPoints:
  web:
    address: ":80"
  websecure:
    address: ":443"
  # One TCP and one UDP entrypoint, so TCP and UDP routers can be tried in the demo.
  postgres:
    address: ":5432"
  dns:
    address: ":5353/udp"
api:
  insecure: true
providers:
  file:
    directory: /etc/traefik/dynamic
    watch: true
certificatesResolvers:
  existing-external:
    acme:
      email: ops@example.com
      storage: /etc/traefik/acme/existing.json
      tlsChallenge: {}
Y
# CrowdSec detects attacks from Traefik's JSON access log. core-v2 owns traefik.yml once it exists,
# so the access log is only added when this script creates the file.
if [ "$USE_CROWDSEC" = 1 ]; then
  if ! grep -q '^accessLog:' "$DEMO/traefik/traefik.yml"; then
    if [ "${NEW_STATIC:-0}" = 1 ]; then
      printf 'accessLog:\n  filePath: /logs/access.log\n  format: json\n' >> "$DEMO/traefik/traefik.yml"
    else
      echo "NOTE: $DEMO/traefik/traefik.yml has no accessLog, so CrowdSec will not see any request."
      echo "      Delete $DEMO to start a fresh demo, or add: accessLog: {filePath: /logs/access.log, format: json}"
    fi
  fi
fi

pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT INT TERM

DOCKER_ENV=()
if [ "$USE_DOCKER" = 1 ]; then
  command -v docker >/dev/null || { echo "docker not found"; exit 1; }
  export DEMO_DIR="$DEMO" TRAEFIK_API_PORT="$TRAEFIK_MOCK_PORT" TRAEFIK_HTTPS_PORT DEMO_UID="$(id -u)" DEMO_GID="$(id -g)"
  # Files left by an earlier root-run Traefik would stay unreadable for core-v2: hand them back.
  docker run --rm --user 0:0 --entrypoint chown -v "$DEMO/traefik/acme:/a" -v "$DEMO/traefik:/t" "${TRAEFIK_IMAGE:-traefik:v3.7.14}" -R "$DEMO_UID:$DEMO_GID" /a /t >/dev/null 2>&1 || true
  TRAEFIK_COMPOSE=(-f "$HERE/dev/docker-compose.yml")
  CROWDSEC_ENV=()
  if [ "$USE_CROWDSEC" = 1 ]; then
    [ -f "$CROWDSEC_COMPOSE" ] || { echo "CrowdSec compose file not found: $CROWDSEC_COMPOSE"; exit 1; }
    # Traefik and CrowdSec are started by two compose calls of one project: each sees the other as an orphan.
    export COMPOSE_IGNORE_ORPHANS=1
    # The log volume is external in both compose files; Traefik (host user) must be able to write it.
    docker volume create traefik-access-logs >/dev/null
    docker run --rm --user 0:0 --entrypoint chown -v traefik-access-logs:/logs "${TRAEFIK_IMAGE:-traefik:v3.7.14}" "$DEMO_UID:$DEMO_GID" /logs >/dev/null
    # Same Compose project as Traefik: core-v2 registers one project for both containers.
    docker compose -p tcdemo -f "$CROWDSEC_COMPOSE" -f "$HERE/dev/crowdsec.demo.yaml" up -d
    CSID="$(docker compose -p tcdemo -f "$CROWDSEC_COMPOSE" -f "$HERE/dev/crowdsec.demo.yaml" ps -q crowdsec)"
    CSID="$(docker inspect -f '{{.Id}}' "$CSID")"
    # Docker chose the subnet; the engine's address on it is the LAPI address for core-v2 and Traefik.
    CROWDSEC_DEMO_IP="$(docker inspect -f '{{(index .NetworkSettings.Networks "tcdemo-lapi").IPAddress}}' "$CSID")"
    [ -n "$CROWDSEC_DEMO_IP" ] || { echo "CrowdSec has no address on tcdemo-lapi; check: docker logs $CSID"; exit 1; }
    echo "CrowdSec container: $CSID (LAPI http://$CROWDSEC_DEMO_IP:8080)"
    TRAEFIK_COMPOSE+=(-f "$HERE/dev/docker-compose.crowdsec.yml")
    CROWDSEC_ENV=(CROWDSEC_LAPI_URL="http://$CROWDSEC_DEMO_IP:8080" CROWDSEC_CONTAINER="$CSID")
    # The first start downloads the Traefik collection before the LAPI answers.
    for i in $(seq 1 90); do curl -fs -o /dev/null "http://$CROWDSEC_DEMO_IP:8080/health" 2>/dev/null && break; sleep 1; done
    curl -fs -o /dev/null "http://$CROWDSEC_DEMO_IP:8080/health" 2>/dev/null || echo "NOTE: the CrowdSec LAPI did not answer yet; check: docker logs $CSID"
  fi
  docker compose "${TRAEFIK_COMPOSE[@]}" up -d --force-recreate
  CID="$(docker compose "${TRAEFIK_COMPOSE[@]}" ps -q traefik)"
  CID="$(docker inspect -f '{{.Id}}' "$CID")"
  echo "Traefik container: $CID"
  DOCKER_ENV=(DOCKER_HOST="${DOCKER_HOST:-unix:///var/run/docker.sock}" TRAEFIK_CONTAINER="$CID"
    TRAEFIK_SECRETS_DIR=/etc/traefik/secrets ALLOW_TRAEFIK_RESTART=true
    # The domain probe connects to websecure through the port published in docker-compose.yml.
    TRAEFIK_PROBE_TARGETS="websecure=127.0.0.1:$TRAEFIK_HTTPS_PORT"
    # A static change restarts Traefik, and a new plugin is downloaded on that start: 15s (the default) is too short.
    TRAEFIK_CONVERGENCE_TIMEOUT_SEC="${TRAEFIK_CONVERGENCE_TIMEOUT_SEC:-60}"
    "${CROWDSEC_ENV[@]}")
  for i in $(seq 1 30); do curl -fs "http://127.0.0.1:$TRAEFIK_MOCK_PORT/api/rawdata" >/dev/null 2>&1 && break; sleep 1; done
else
  python3 "$HERE/dev/mock_traefik.py" "$TRAEFIK_MOCK_PORT" & pids+=($!)
fi

cd "$DEMO"
BIND_ADDRESS=127.0.0.1 PORT="$CORE_PORT" \
CONSOLE_ORIGIN="http://localhost:$FRONT_PORT" CONSOLE_HOST="" \
DB_PATH="$DEMO/data/console.db" MASTER_KEY_FILE="$DEMO/keys/master.key" \
STATIC_FILE="$DEMO/traefik/traefik.yml" DYNAMIC_DIR="$DEMO/traefik/dynamic" \
SECRETS_DIR="$DEMO/traefik/secrets" \
TRAEFIK_API_URL="http://127.0.0.1:$TRAEFIK_MOCK_PORT" \
env "${DOCKER_ENV[@]}" "$BIN" server & pids+=($!)

for i in $(seq 1 40); do curl -fs http://127.0.0.1:$CORE_PORT/api/v1/health >/dev/null 2>&1 && break; sleep 0.5; done
curl -fs http://127.0.0.1:$CORE_PORT/api/v1/health || { echo "core-v2 did not start"; exit 1; }
echo; echo "core-v2 ready on :$CORE_PORT → starting frontend on http://localhost:$FRONT_PORT/#/resolvers"

cd "$HERE"
[ -e node_modules ] || npm install --no-audit --no-fund
CORE_V2_URL="http://127.0.0.1:$CORE_PORT" VITE_CACHE_DIR="${VITE_CACHE_DIR:-/tmp/vite-v2}" npx vite dev --host localhost --port "$FRONT_PORT" --strictPort
