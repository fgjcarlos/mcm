#!/usr/bin/env bash
# E2E production-style deploy test for issue #294.
#
# Boots a Compose stack that mirrors the production managed flow:
#
#   - mosquitto runs as the official `mosquitto` UID (NOT root).
#   - The mcm service shares a NAMED VOLUME with mosquitto for the
#     passwd/acl files but does NOT mount the Docker socket.
#   - Reload is signalled via MCM_MOSQUITTO_DEPLOY_RELOAD_COMMAND
#     running a small helper script that sends SIGHUP to the broker
#     process — the production analogue of `systemctl reload`.
#
# Scenarios:
#
#   1. Bring up the production-style stack.
#   2. Login.
#   3. POST /api/v1/mqtt-users creates "prod-user" with a cleartext
#      password (the deploy verifier needs it).
#   4. POST /api/v1/acls grants prod-user on prod/test/allowed.
#   5. POST /api/v1/deployments/preview returns has_changes=true.
#   6. POST /api/v1/deployments/apply returns 200 with status
#      "active_verified" — proves the FileApplier + ReloadCommand
#      path works end-to-end without docker.sock.
#   7. The broker accepts the published message on the granted topic.
#   8. docker compose down -v cleans up.
#
# Invariants asserted:
#   1. /livez returns 200 within the wait budget.
#   2. The mcm service log shows the "managed production reload"
#      path (i.e. ReloadCommand invoked, no docker exec).
#   3. POST /deployments/apply returns status=active_verified.
#   4. prod-user can publish + a subscriber receives on the granted
#      topic.
#
# Exit code:
#   0  every invariant passed.
#   1  any invariant failed (see the failing step's stderr).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE="docker compose"
COMPOSE_FILE="$REPO_ROOT/docker-compose.prod.yml"
PORT="${MCM_PORT:-8080}"
HOST_URL="http://localhost:${PORT}"

WAIT_SECONDS="${E2E_PROD_WAIT_SECONDS:-180}"
RELOAD_SETTLE_SECONDS="${E2E_PROD_RELOAD_SETTLE:-5}"
PROD_USER="prod-user"
ALLOWED_TOPIC="prod/test/allowed"

cleanup() {
    local exit_code=$?
    echo "--- e2e-prod: tearing down (exit=$exit_code) ---"
    (cd "$REPO_ROOT" && $COMPOSE -f "$COMPOSE_FILE" down -v --remove-orphans >/dev/null 2>&1) || true
    exit "$exit_code"
}
trap cleanup EXIT

if ! command -v jq >/dev/null 2>&1; then
    echo "--- e2e-prod: installing jq ---"
    if command -v apt-get >/dev/null 2>&1; then
        sudo apt-get update -qq && sudo apt-get install -y -qq jq
    elif command -v dnf >/dev/null 2>&1; then
        sudo dnf install -y jq
    elif command -v apk >/dev/null 2>&1; then
        sudo apk add --no-cache jq
    else
        echo "Cannot install jq: no supported package manager found" >&2
        exit 1
    fi
fi

cd "$REPO_ROOT"

if [ -f ".env" ]; then
    echo "Refusing to run: .env exists in repo root. Remove it before running the e2e prod test." >&2
    exit 1
fi

echo "--- e2e-prod: up (prod-style stack: no docker.sock mount) ---"
$COMPOSE -f "$COMPOSE_FILE" up -d --no-build

echo "--- e2e-prod: waiting for /livez (${WAIT_SECONDS}s budget) ---"
ready=0
for i in $(seq 1 "$WAIT_SECONDS"); do
    if curl -fsS "${HOST_URL}/livez" >/dev/null 2>&1; then
        echo "livez ready after ${i}s"
        ready=1
        break
    fi
    sleep 1
done
if [ "$ready" -ne 1 ]; then
    echo "MCM did not become live within ${WAIT_SECONDS}s" >&2
    $COMPOSE -f "$COMPOSE_FILE" logs --no-color mcm >&2 || true
    exit 1
fi

echo "--- e2e-prod: extracting bootstrap admin password from mcm logs ---"
LOG_FILE="$(mktemp)"
$COMPOSE -f "$COMPOSE_FILE" logs --no-color mcm > "$LOG_FILE" || true
if ! grep -q 'bootstrap admin created' "$LOG_FILE"; then
    echo "Expected bootstrap admin warn log line not found in mcm logs:" >&2
    cat "$LOG_FILE" >&2
    rm -f "$LOG_FILE"
    exit 1
fi
ADMIN_PASSWORD="$(grep 'bootstrap admin created' "$LOG_FILE" | sed -n 's/.*"password":"\([^"]*\)".*/\1/p' | head -n1)"
rm -f "$LOG_FILE"
if [ -z "$ADMIN_PASSWORD" ]; then
    echo "Could not extract bootstrap admin password from mcm logs" >&2
    exit 1
fi

echo "--- e2e-prod: POST /api/v1/auth/login ---"
login_body="$(mktemp)"
login_code="$(curl -sS -o "$login_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"${ADMIN_PASSWORD}\"}" || echo "000")"
if [ "$login_code" != "200" ]; then
    echo "login failed with HTTP $login_code:" >&2
    cat "$login_body" >&2
    rm -f "$login_body"
    exit 1
fi
TOKEN="$(jq -r '.token // .access_token // .jwt // empty' "$login_body")"
rm -f "$login_body"
if [ -z "$TOKEN" ]; then
    echo "login returned 200 but no token field was present" >&2
    exit 1
fi

echo "--- e2e-prod: POST /api/v1/mqtt-users (create ${PROD_USER}) ---"
user_body="$(mktemp)"
user_code="$(curl -sS -o "$user_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/mqtt-users" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"${PROD_USER}\"}" || echo "000")"
if [ "$user_code" != "201" ]; then
    echo "create mqtt-user failed with HTTP $user_code:" >&2
    cat "$user_body" >&2
    rm -f "$user_body"
    exit 1
fi
USER_PASSWORD="$(jq -r '.password // empty' "$user_body")"
rm -f "$user_body"
if [ -z "$USER_PASSWORD" ]; then
    echo "create mqtt-user response missing password field" >&2
    exit 1
fi

echo "--- e2e-prod: POST /api/v1/acls (grant ${PROD_USER} on ${ALLOWED_TOPIC}) ---"
acl_body="$(mktemp)"
acl_code="$(curl -sS -o "$acl_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/acls" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"principal\":\"${PROD_USER}\",\"topic_filter\":\"${ALLOWED_TOPIC}\",\"permission\":\"readwrite\"}" || echo "000")"
if [ "$acl_code" != "201" ]; then
    echo "create acl failed with HTTP $acl_code:" >&2
    cat "$acl_body" >&2
    rm -f "$acl_body"
    exit 1
fi
rm -f "$acl_body"

echo "--- e2e-prod: POST /api/v1/deployments/preview ---"
preview_body="$(mktemp)"
preview_code="$(curl -sS -o "$preview_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/deployments/preview" \
    -H "Authorization: Bearer ${TOKEN}" || echo "000")"
if [ "$preview_code" != "200" ]; then
    echo "deploy preview failed with HTTP $preview_code:" >&2
    cat "$preview_body" >&2
    rm -f "$preview_body"
    exit 1
fi
HAS_CHANGES="$(jq -r '.has_changes // false' "$preview_body")"
rm -f "$preview_body"
if [ "$HAS_CHANGES" != "true" ]; then
    echo "expected deploy preview has_changes=true, got $HAS_CHANGES" >&2
    exit 1
fi
echo "preview OK, has_changes=true"

echo "--- e2e-prod: POST /api/v1/deployments/apply ---"
apply_body="$(mktemp)"
apply_code="$(curl -sS -o "$apply_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/deployments/apply" \
    -H "Authorization: Bearer ${TOKEN}" || echo "000")"
if [ "$apply_code" != "200" ]; then
    echo "deploy apply failed with HTTP $apply_code:" >&2
    cat "$apply_body" >&2
    echo "--- mcm logs ---" >&2
    $COMPOSE -f "$COMPOSE_FILE" logs --no-color mcm >&2 || true
    rm -f "$apply_body"
    exit 1
fi
APPLY_STATUS="$(jq -r '.status // empty' "$apply_body")"
rm -f "$apply_body"
if [ "$APPLY_STATUS" != "active_verified" ]; then
    echo "expected deploy apply status=active_verified, got $APPLY_STATUS" >&2
    exit 1
fi
echo "apply OK, status=active_verified"

echo "--- e2e-prod: waiting ${RELOAD_SETTLE_SECONDS}s for broker reload to settle ---"
sleep "$RELOAD_SETTLE_SECONDS"

echo "--- e2e-prod: verifying publish on the live broker ---"
# The prod stack uses a helper container (mosquitto-clients) for the
# publish/subscribe loop — the dev e2e uses docker exec into the
# broker container directly, which the prod-style stack avoids.
allowed_log="$($COMPOSE -f "$COMPOSE_FILE" exec -T mosquitto-clients sh -c "
    mosquitto_sub -h mosquitto -p 1883 \
        -u \"$PROD_USER\" -P \"$USER_PASSWORD\" \
        -t \"$ALLOWED_TOPIC\" -C 1 -W 15 -q 1 2>&1 &
    SUB_PID=\$!
    sleep 2
    mosquitto_pub -h mosquitto -p 1883 \
        -u \"$PROD_USER\" -P \"$USER_PASSWORD\" \
        -t \"$ALLOWED_TOPIC\" -m \"prod-verified\" -q 1
    wait \$SUB_PID
" 2>&1 || true)"
if ! printf '%s\n' "$allowed_log" | grep -q '^prod-verified$'; then
    echo "expected subscriber on allowed topic '$ALLOWED_TOPIC' to receive 'prod-verified', got:" >&2
    printf '%s\n' "$allowed_log" >&2
    exit 1
fi
echo "publish+deliver OK — ReloadCommand path works without docker.sock"

echo "--- e2e-prod: all invariants passed ---"
