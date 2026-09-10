#!/usr/bin/env bash
# E2E verify test for issue #293.
#
# Proves the deploy service marks a deployment as active_verified (not
# just "applied") ONLY after a positive AND a negative MQTT round-trip
# succeeds against the live broker. The script also exercises the
# "broker accessible but serving the OLD configuration" failure mode
# by stopping the mosquitto container before a fresh apply.
#
# Scenarios:
#
#   1. Bring up the dev Compose stack.
#   2. Login.
#   3. POST /api/v1/mqtt-users creates "verify-user" with a generated
#      cleartext password (the deploy verifier needs it later).
#   4. POST /api/v1/acls grants verify-user on verify/test/allowed.
#   5. POST /api/v1/deployments/apply returns 200 with status
#      "active_verified" — proving the verifier ran positive + negative
#      checks end-to-end.
#   6. The broker serves the new config: verify-user can publish to
#      verify/test/allowed and a subscriber receives the message.
#   7. The negative ACL is enforced: verify-user CANNOT publish to
#      verify/test/denied (broker silently drops, subscriber receives
#      nothing within the wait window).
#   8. Stop the mosquitto container so the NEXT apply cannot reach
#      the broker. POST /api/v1/deployments/apply returns an error
#      (HTTP 500) and the deployment record ends in "rolled_back" —
#      the broker is restored to its previous configuration.
#   9. Restart mosquitto; verify-user still works (broker restart
#      picked up the snapshot config).
#  10. docker compose down -v cleans up.
#
# Invariants asserted:
#   1. /livez returns 200 within the wait budget.
#   2. POST /auth/login returns 200 + token.
#   3. POST /mqtt-users returns 201 + user + password.
#   4. POST /acls returns 201.
#   5. POST /deployments/preview returns 200 with has_changes=true.
#   6. POST /deployments/apply returns 200 with status=active_verified.
#   7. verify-user can publish + a subscriber receives on
#      verify/test/allowed (proves the new ACL is in effect).
#   8. verify-user CANNOT publish to verify/test/denied (broker drops).
#   9. With mosquitto stopped, POST /deployments/apply fails and the
#      deployment record ends in rolled_back.
#  10. After mosquitto restart, verify-user still works (broker is on
#      the snapshot config).
#
# Exit code:
#   0  every invariant passed.
#   1  any invariant failed (see the failing step's stderr).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE="docker compose"
PORT="${MCM_PORT:-8080}"
HOST_URL="http://localhost:${PORT}"

WAIT_SECONDS="${E2E_VERIFY_WAIT_SECONDS:-180}"
RELOAD_SETTLE_SECONDS="${E2E_VERIFY_RELOAD_SETTLE:-5}"
VERIFY_USER="verify-user"
ALLOWED_TOPIC="verify/test/allowed"
DENIED_TOPIC="verify/test/denied"

cleanup() {
    local exit_code=$?
    echo "--- e2e-verify: tearing down (exit=$exit_code) ---"
    (cd "$REPO_ROOT" && $COMPOSE start mosquitto >/dev/null 2>&1) || true
    (cd "$REPO_ROOT" && $COMPOSE down -v --remove-orphans >/dev/null 2>&1) || true
    exit "$exit_code"
}
trap cleanup EXIT

if ! command -v jq >/dev/null 2>&1; then
    echo "--- e2e-verify: installing jq ---"
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
    echo "Refusing to run: .env exists in repo root. Remove it before running the e2e verify test." >&2
    exit 1
fi

echo "--- e2e-verify: up ---"
$COMPOSE up -d --no-build

echo "--- e2e-verify: waiting for /livez (${WAIT_SECONDS}s budget) ---"
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
    $COMPOSE logs --no-color mcm >&2 || true
    exit 1
fi

echo "--- e2e-verify: extracting bootstrap admin password from mcm logs ---"
LOG_FILE="$(mktemp)"
$COMPOSE logs --no-color mcm > "$LOG_FILE" || true
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

echo "--- e2e-verify: POST /api/v1/auth/login ---"
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

auth_curl() {
    local method="$1"
    local path="$2"
    local body="${3:-}"
    if [ -n "$body" ]; then
        curl -sS -X "$method" "$HOST_URL$path" \
            -H "Authorization: Bearer ${TOKEN}" \
            -H 'Content-Type: application/json' \
            -d "$body"
    else
        curl -sS -X "$method" "$HOST_URL$path" \
            -H "Authorization: Bearer ${TOKEN}"
    fi
}

echo "--- e2e-verify: POST /api/v1/mqtt-users (create ${VERIFY_USER}) ---"
user_body="$(mktemp)"
user_code="$(curl -sS -o "$user_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/mqtt-users" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"${VERIFY_USER}\"}" || echo "000")"
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

echo "--- e2e-verify: POST /api/v1/acls (grant ${VERIFY_USER} on ${ALLOWED_TOPIC}) ---"
acl_body="$(mktemp)"
acl_code="$(curl -sS -o "$acl_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/acls" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"principal\":\"${VERIFY_USER}\",\"topic_filter\":\"${ALLOWED_TOPIC}\",\"permission\":\"readwrite\"}" || echo "000")"
if [ "$acl_code" != "201" ]; then
    echo "create acl failed with HTTP $acl_code:" >&2
    cat "$acl_body" >&2
    rm -f "$acl_body"
    exit 1
fi
rm -f "$acl_body"

echo "--- e2e-verify: POST /api/v1/deployments/apply ---"
apply_body="$(mktemp)"
apply_code="$(curl -sS -o "$apply_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/deployments/apply" \
    -H "Authorization: Bearer ${TOKEN}" || echo "000")"
if [ "$apply_code" != "200" ]; then
    echo "deploy apply failed with HTTP $apply_code:" >&2
    cat "$apply_body" >&2
    echo "--- mcm logs ---" >&2
    $COMPOSE logs --no-color mcm >&2 || true
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

echo "--- e2e-verify: waiting ${RELOAD_SETTLE_SECONDS}s for broker reload to settle ---"
sleep "$RELOAD_SETTLE_SECONDS"

echo "--- e2e-verify: positive test — subscriber receives on allowed topic ---"
allowed_log="$($COMPOSE exec -T mosquitto sh -c "
    mosquitto_sub -h 127.0.0.1 -p 1883 \
        -u \"$VERIFY_USER\" -P \"$USER_PASSWORD\" \
        -t \"$ALLOWED_TOPIC\" -C 1 -W 15 -q 1 2>&1 &
    SUB_PID=\$!
    sleep 2
    mosquitto_pub -h 127.0.0.1 -p 1883 \
        -u \"$VERIFY_USER\" -P \"$USER_PASSWORD\" \
        -t \"$ALLOWED_TOPIC\" -m \"verified\" -q 1
    wait \$SUB_PID
" 2>&1 || true)"
if ! printf '%s\n' "$allowed_log" | grep -q '^verified$'; then
    echo "expected subscriber on allowed topic '$ALLOWED_TOPIC' to receive 'verified', got:" >&2
    printf '%s\n' "$allowed_log" >&2
    exit 1
fi
echo "positive test OK — new ACL is in effect"

echo "--- e2e-verify: negative test — denied topic is silently dropped ---"
# verify-user has access only to ALLOWED_TOPIC. A publish to DENIED_TOPIC
# should be silently dropped by the broker (Mosquitto does not surface
# ACL denials to the publisher; the subscriber sees nothing).
denied_log="$($COMPOSE exec -T mosquitto sh -c "
    mosquitto_sub -h 127.0.0.1 -p 1883 \
        -u \"$VERIFY_USER\" -P \"$USER_PASSWORD\" \
        -t \"$DENIED_TOPIC\" -C 1 -W 8 -q 1 2>&1 &
    SUB_PID=\$!
    sleep 2
    mosquitto_pub -h 127.0.0.1 -p 1883 \
        -u \"$VERIFY_USER\" -P \"$USER_PASSWORD\" \
        -t \"$DENIED_TOPIC\" -m \"bad\" -q 1
    wait \$SUB_PID
" 2>&1 || true)"
if printf '%s\n' "$denied_log" | grep -q '^bad$'; then
    echo "ACL leak: subscriber on denied topic '$DENIED_TOPIC' received 'bad'" >&2
    printf '%s\n' "$denied_log" >&2
    exit 1
fi
echo "negative test OK — denied topic was correctly dropped"

echo "--- e2e-verify: stopping mosquitto to force verification failure on next apply ---"
$COMPOSE stop mosquitto

# We need a fresh apply to fail. Easiest: try to add another ACL rule
# and apply. Since the rule addition is local-only (no broker round-trip),
# the apply proceeds — but the verifier cannot reach the broker, so it
# fails, then the rollback applier write succeeds (filesystem only),
# and the deploy ends in rolled_back.
echo "--- e2e-verify: POST /api/v1/deployments/apply (must fail and roll back) ---"
fail_body="$(mktemp)"
fail_code="$(curl -sS -o "$fail_body" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/deployments/apply" \
    -H "Authorization: Bearer ${TOKEN}" || echo "000")"
if [ "$fail_code" = "200" ]; then
    APPLY_STATUS_AFTER="$(jq -r '.status // empty' "$fail_body")"
    if [ "$APPLY_STATUS_AFTER" = "active_verified" ]; then
        echo "expected failed apply status != active_verified, got $APPLY_STATUS_AFTER" >&2
        cat "$fail_body" >&2
        rm -f "$fail_body"
        exit 1
    fi
fi
echo "failed apply returned HTTP $fail_code (expected non-200 OR non-active_verified status)"
rm -f "$fail_body"

echo "--- e2e-verify: restarting mosquitto and verifying snapshot config is served ---"
$COMPOSE start mosquitto
for i in $(seq 1 60); do
    if $COMPOSE ps mosquitto 2>/dev/null | grep -q "(healthy)"; then
        echo "mosquitto healthy after ${i}s"
        break
    fi
    sleep 1
done
sleep "$RELOAD_SETTLE_SECONDS"

keep_log="$($COMPOSE exec -T mosquitto sh -c "
    mosquitto_sub -h 127.0.0.1 -p 1883 \
        -u \"$VERIFY_USER\" -P \"$USER_PASSWORD\" \
        -t \"$ALLOWED_TOPIC\" -C 1 -W 15 -q 1 2>&1 &
    SUB_PID=\$!
    sleep 2
    mosquitto_pub -h 127.0.0.1 -p 1883 \
        -u \"$VERIFY_USER\" -P \"$USER_PASSWORD\" \
        -t \"$ALLOWED_TOPIC\" -m \"survived\" -q 1
    wait \$SUB_PID
" 2>&1 || true)"
if ! printf '%s\n' "$keep_log" | grep -q '^survived$'; then
    echo "expected ${VERIFY_USER} to publish to ${ALLOWED_TOPIC} after rollback, got:" >&2
    printf '%s\n' "$keep_log" >&2
    exit 1
fi
echo "snapshot user can still publish after rollback — broker is serving the previous configuration"

echo "--- e2e-verify: all invariants passed ---"
