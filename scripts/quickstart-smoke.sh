#!/usr/bin/env bash
# Quickstart smoke test for first-admin setup and restart persistence.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The MCM container reaches the Docker daemon through the host's socket
# group. The image bakes the conventional GID at build time, but the
# socket of this host (and of a CI runner) can live under another GID.
# shellcheck source=lib-docker-host-gid.sh
source "${REPO_ROOT}/scripts/lib-docker-host-gid.sh"
export_docker_host_gid

COMPOSE="docker compose"
PORT="${MCM_PORT:-8080}"
BASE_URL="http://localhost:${PORT}"
HEALTH_URL="${BASE_URL}/healthz"
LIVE_URL="${BASE_URL}/livez"
SETUP_STATUS_URL="${BASE_URL}/api/v1/auth/setup"
SETUP_URL="${SETUP_STATUS_URL}"
LOGIN_URL="${BASE_URL}/api/v1/auth/login"
STATUS_URL="${BASE_URL}/api/v1/status"
WAIT_SECONDS="${QUICKSTART_WAIT_SECONDS:-120}"
SCRATCH="$(mktemp -d)"
SETUP_TOKEN="$(openssl rand -hex 32)"
ADMIN_PASSWORD="$(openssl rand -hex 24)"
ADMIN_USERNAME="quickstart-admin"

cleanup() {
  local exit_code=$?
  echo "--- quickstart-smoke: tearing down (exit=$exit_code) ---"
  (cd "$REPO_ROOT" && $COMPOSE down -v --remove-orphans >/dev/null 2>&1) || true
  rm -rf "$SCRATCH"
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

export MCM_SETUP_TOKEN="$SETUP_TOKEN"
echo "--- quickstart-smoke: build + up with a random setup token ---"
docker buildx build --load -t mcm:dev . >/dev/null
$COMPOSE up -d

wait_live() {
  local label="$1" ready=0
  for i in $(seq 1 "$WAIT_SECONDS"); do
    if curl -fsS "$LIVE_URL" >/dev/null 2>&1; then
      echo "livez ready ${label} after ${i}s"
      ready=1
      break
    fi
    sleep 1
  done
  if [ "$ready" -ne 1 ]; then
    echo "MCM did not become live ${label} within ${WAIT_SECONDS}s" >&2
    exit 1
  fi
}

json_field() {
  local file="$1" field="$2"
  if command -v jq >/dev/null 2>&1; then
    jq -r ".${field} // empty" "$file"
  else
    sed -n "s/.*\\\"${field}\\\":\\\"\\([^\\\"]*\\)\\\".*/\\1/p" "$file" | head -n1
  fi
}

wait_live "(initially)"
if ! curl -fsS "$HEALTH_URL" >/dev/null; then
  echo "healthz did not return 200" >&2
  exit 1
fi
echo "healthz OK"

setup_status="$(curl -fsS "$SETUP_STATUS_URL")"
if [ "$(printf '%s' "$setup_status" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')" != "required" ]; then
  echo "first-admin setup status was not required" >&2
  exit 1
fi

printf '{"username":"%s","password":"%s","password_confirmation":"%s","setup_token":"%s"}' \
  "$ADMIN_USERNAME" "$ADMIN_PASSWORD" "$ADMIN_PASSWORD" "$SETUP_TOKEN" > "$SCRATCH/setup-request.json"
setup_code="$(curl -sS -o "$SCRATCH/setup-response.json" -w '%{http_code}' \
  -X POST "$SETUP_URL" -H 'Content-Type: application/json' \
  --data-binary "@$SCRATCH/setup-request.json" || echo "000")"
if [ "$setup_code" != "201" ]; then
  echo "first-admin setup failed with HTTP $setup_code" >&2
  exit 1
fi
# Ensure the one-time setup endpoint returns a session without exposing credentials.
if [ -z "$(json_field "$SCRATCH/setup-response.json" token)" ]; then
  echo "first-admin setup returned no session token" >&2
  exit 1
fi
echo "first-admin setup OK"

# The public login API must authenticate the account created by setup.
printf '{"username":"%s","password":"%s"}' "$ADMIN_USERNAME" "$ADMIN_PASSWORD" > "$SCRATCH/login-request.json"
login_code="$(curl -sS -o "$SCRATCH/login-response.json" -w '%{http_code}' \
  -X POST "$LOGIN_URL" -H 'Content-Type: application/json' \
  --data-binary "@$SCRATCH/login-request.json" || echo "000")"
if [ "$login_code" != "200" ] || [ -z "$(json_field "$SCRATCH/login-response.json" token)" ]; then
  echo "login with the created administrator failed (HTTP $login_code)" >&2
  exit 1
fi
TOKEN="$(json_field "$SCRATCH/login-response.json" token)"
echo "login OK"

call_status() {
  curl -sS -o "$SCRATCH/status.json" -w '%{http_code}' \
    -H "Authorization: Bearer ${TOKEN}" "$STATUS_URL"
}

pre_code="$(call_status)"
if [ "$pre_code" != "200" ]; then
  echo "pre-restart GET /api/v1/status = $pre_code, want 200" >&2
  exit 1
fi
echo "pre-restart authenticated status OK"

assert_no_fixture_secrets_in_logs() {
  local logfile="$1"
  if grep -Fq -- "$SETUP_TOKEN" "$logfile" || grep -Fq -- "$ADMIN_PASSWORD" "$logfile"; then
    echo "setup token or administrator password appeared in MCM logs" >&2
    exit 1
  fi
}

if ! $COMPOSE logs --no-color mcm > "$SCRATCH/pre-restart-logs"; then
  echo "could not inspect MCM logs before restart" >&2
  exit 1
fi
assert_no_fixture_secrets_in_logs "$SCRATCH/pre-restart-logs"
if grep -Fq 'bootstrap admin created' "$SCRATCH/pre-restart-logs"; then
  echo "unexpected generated bootstrap admin log line after interactive setup" >&2
  exit 1
fi

# --- Persistence across restart --------------------------------------------
echo "--- quickstart-smoke: restart mcm ---"
$COMPOSE restart mcm >/dev/null
wait_live "after restart"

post_code="$(call_status)"
if [ "$post_code" != "200" ]; then
  echo "post-restart GET /api/v1/status = $post_code, want 200 (JWT secret must have persisted)" >&2
  exit 1
fi
echo "post-restart authenticated status OK with the SAME token"

setup_status="$(curl -fsS "$SETUP_STATUS_URL")"
if [ "$(printf '%s' "$setup_status" | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')" != "complete" ]; then
  echo "first-admin setup was not closed after restart" >&2
  exit 1
fi

login_code="$(curl -sS -o "$SCRATCH/post-restart-login.json" -w '%{http_code}' \
  -X POST "$LOGIN_URL" -H 'Content-Type: application/json' \
  --data-binary "@$SCRATCH/login-request.json" || echo "000")"
if [ "$login_code" != "200" ] || [ -z "$(json_field "$SCRATCH/post-restart-login.json" token)" ]; then
  echo "post-restart login failed (HTTP $login_code)" >&2
  exit 1
fi
echo "post-restart setup remains closed and administrator login succeeds"

if ! $COMPOSE logs --no-color mcm > "$SCRATCH/post-restart-logs"; then
  echo "could not inspect MCM logs after restart" >&2
  exit 1
fi
assert_no_fixture_secrets_in_logs "$SCRATCH/post-restart-logs"
if grep -Fq 'bootstrap admin created' "$SCRATCH/post-restart-logs"; then
  echo "unexpected generated bootstrap admin log line after interactive setup" >&2
  exit 1
fi

# Exit trap runs `docker compose down -v`, returning the stack to a true
# clean-clone state for the next run.
echo "--- quickstart-smoke: all invariants passed ---"
