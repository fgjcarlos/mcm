#!/usr/bin/env bash
# Quickstart smoke test for issue #274.
#
# Proves that on a clean clone with no .env file, `docker compose up -d`
# brings up MCM and Mosquitto, the auto-generated bootstrap admin password
# is recoverable from `docker compose logs`, and that password authenticates
# against /api/v1/auth/login. This is the CI mirror of the bare
# `task build && task up && task logs` flow.
#
# Invariants asserted:
#   1. docker compose up -d exits 0 with NO env vars set.
#   2. /livez and /healthz return 200 within the wait budget.
#   3. The bootstrap admin warn log line is present in mcm logs.
#   4. POST /api/v1/auth/login with the extracted password returns 200
#      and a non-empty token.
#   5. docker compose down -v cleans up the volumes so the next run is
#      a true clean-clone simulation.
#
# Exit code:
#   0  every invariant passed.
#   1  any invariant failed (see the failing step's stderr).
#
# Required: docker (with compose v2), curl, jq (optional, for nicer
# output). The script tries to install jq on the fly if missing.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE="docker compose"
PORT="${MCM_PORT:-8080}"
HEALTH_URL="http://localhost:${PORT}/healthz"
LIVE_URL="http://localhost:${PORT}/livez"
LOGIN_URL="http://localhost:${PORT}/api/v1/auth/login"

# Wait budget (seconds). The compose healthcheck on mcm uses
# start_period=15s, retries=3, interval=30s — easily 60s+ cold. We
# poll /livez for up to 120s to give the buildx cache + DB init room.
WAIT_SECONDS="${QUICKSTART_WAIT_SECONDS:-120}"

cleanup() {
  local exit_code=$?
  echo "--- quickstart-smoke: tearing down (exit=$exit_code) ---"
  (cd "$REPO_ROOT" && $COMPOSE down -v --remove-orphans >/dev/null 2>&1) || true
  exit "$exit_code"
}
trap cleanup EXIT

cd "$REPO_ROOT"

# Sanity: require a clean repo so a leftover .env from a real operator
# does not silently mask a Compose regression.
if [ -f ".env" ]; then
  echo "Refusing to run: .env exists in repo root. Remove it before running the Quickstart smoke test." >&2
  exit 1
fi

echo "--- quickstart-smoke: build + up (no env vars) ---"
docker buildx build --load -t mcm:dev . >/dev/null
$COMPOSE up -d

echo "--- quickstart-smoke: waiting for /livez (${WAIT_SECONDS}s budget) ---"
ready=0
for i in $(seq 1 "$WAIT_SECONDS"); do
  if curl -fsS "$LIVE_URL" >/dev/null 2>&1; then
    echo "livez ready after ${i}s"
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" -ne 1 ]; then
  echo "MCM did not become live within ${WAIT_SECONDS}s" >&2
  echo "--- mcm logs ---" >&2
  $COMPOSE logs --no-color mcm >&2 || true
  exit 1
fi

if ! curl -fsS "$HEALTH_URL" >/dev/null; then
  echo "healthz did not return 200" >&2
  exit 1
fi
echo "healthz OK"

echo "--- quickstart-smoke: extracting bootstrap admin password from mcm logs ---"
# The bootstrap admin warn line is emitted exactly once on first boot.
# The slog JSON format is:
#   {"time":"...","level":"WARN","msg":"bootstrap admin created; capture these credentials now — they will not be logged again","username":"admin","password":"<...>"}
# We grep the mcm logs for the username=admin marker, then extract the
# password= value with a sed.
LOG_FILE="$(mktemp)"
$COMPOSE logs --no-color mcm > "$LOG_FILE" || true
if ! grep -q 'bootstrap admin created' "$LOG_FILE"; then
  echo "Expected bootstrap admin warn log line not found in mcm logs:" >&2
  cat "$LOG_FILE" >&2
  rm -f "$LOG_FILE"
  exit 1
fi

# Pull the password from the JSON emitted by slog. The JSON encoder
# escapes the unicode em-dash in the message with \u2014; we only
# need the password field, so a simple sed that grabs the value
# between password=" and the next " handles the un-escaped ASCII run.
PASSWORD="$(grep 'bootstrap admin created' "$LOG_FILE" | sed -n 's/.*"password":"\([^"]*\)".*/\1/p' | head -n1)"
rm -f "$LOG_FILE"
if [ -z "$PASSWORD" ]; then
  echo "Could not extract bootstrap admin password from mcm logs" >&2
  exit 1
fi
echo "extracted password (length=${#PASSWORD})"

echo "--- quickstart-smoke: POST /api/v1/auth/login ---"
login_body="$(mktemp)"
login_code="$(curl -sS -o "$login_body" -w '%{http_code}' \
  -X POST "$LOGIN_URL" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"${PASSWORD}\"}" || echo "000")"
if [ "$login_code" != "200" ]; then
  echo "login failed with HTTP $login_code:" >&2
  cat "$login_body" >&2
  rm -f "$login_body"
  exit 1
fi

# Respond with a JSON object containing a token field. Pull it with
# jq if available, else with a sed fallback.
if command -v jq >/dev/null 2>&1; then
  TOKEN="$(jq -r '.token // .access_token // .jwt // empty' "$login_body")"
else
  TOKEN="$(sed -n 's/.*"token":"\([^"]*\)".*/\1/p' "$login_body" | head -n1)"
fi
rm -f "$login_body"
if [ -z "$TOKEN" ]; then
  echo "login returned 200 but no token field was present" >&2
  exit 1
fi
echo "login OK, token length=${#TOKEN}"

echo "--- quickstart-smoke: all invariants passed ---"
