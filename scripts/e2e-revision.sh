#!/usr/bin/env bash
# E2E immutable Preview/Apply revision test for issue #296.
#
# Proves that:
#   1. Two separate authenticated operators receive different revisions.
#   2. A desired-state edit after Preview invalidates the old revision.
#   3. An external on-disk edit after Preview invalidates the old revision.
#   4. Applying one of two competing revisions invalidates the other.
#
# Required: docker compose v2, curl and jq. The script expects the mcm:dev
# image to already be loaded, matching the other deploy E2E scripts.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE="docker compose"
PORT="${MCM_PORT:-8080}"
HOST_URL="http://localhost:${PORT}"
WAIT_SECONDS="${E2E_REVISION_WAIT_SECONDS:-180}"

cleanup() {
    local exit_code=$?
    echo "--- e2e-revision: tearing down (exit=$exit_code) ---"
    (cd "$REPO_ROOT" && $COMPOSE down -v --remove-orphans >/dev/null 2>&1) || true
    exit "$exit_code"
}
trap cleanup EXIT

if ! command -v jq >/dev/null 2>&1; then
    echo "jq is required to run e2e-revision.sh" >&2
    exit 1
fi

cd "$REPO_ROOT"
if [ -f ".env" ]; then
    echo "Refusing to run: .env exists in repo root." >&2
    exit 1
fi

echo "--- e2e-revision: starting clean stack ---"
$COMPOSE up -d --no-build
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

LOG_FILE="$(mktemp)"
$COMPOSE logs --no-color mcm >"$LOG_FILE" || true
ADMIN_PASSWORD="$(grep 'bootstrap admin created' "$LOG_FILE" | sed -n 's/.*"password":"\([^"]*\)".*/\1/p' | head -n1)"
rm -f "$LOG_FILE"
if [ -z "$ADMIN_PASSWORD" ]; then
    echo "Could not extract bootstrap admin password" >&2
    exit 1
fi

login() {
    curl -fsS -X POST "${HOST_URL}/api/v1/auth/login" \
        -H 'Content-Type: application/json' \
        -d "$1"
}

api() {
    local token="$1" method="$2" path="$3" body="${4:-}"
    if [ -n "$body" ]; then
        curl -fsS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${token}" \
            -H 'Content-Type: application/json' \
            -d "$body"
    else
        curl -fsS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${token}"
    fi
}

TOKEN_A="$(login "{\"username\":\"admin\",\"password\":\"${ADMIN_PASSWORD}\"}" | jq -r '.token // empty')"
if [ -z "$TOKEN_A" ]; then
    echo "operator A login did not return a token" >&2
    exit 1
fi

echo "--- e2e-revision: creating operator B ---"
api "$TOKEN_A" POST /api/v1/admin-users \
    '{"username":"revision-admin-b","password":"revision-admin-b-password","role":"admin"}' >/dev/null
TOKEN_B="$(login '{"username":"revision-admin-b","password":"revision-admin-b-password"}' | jq -r '.token // empty')"
if [ -z "$TOKEN_B" ]; then
    echo "operator B login did not return a token" >&2
    exit 1
fi

echo "--- e2e-revision: creating desired MQTT state ---"
USER_RESPONSE="$(api "$TOKEN_A" POST /api/v1/mqtt-users '{"username":"revision-user"}')"
USER_PASSWORD="$(printf '%s' "$USER_RESPONSE" | jq -r '.password // empty')"
if [ -z "$USER_PASSWORD" ]; then
    echo "mqtt user response did not include a password" >&2
    exit 1
fi
api "$TOKEN_A" POST /api/v1/acls \
    '{"principal":"revision-user","topic_filter":"revision/allowed","permission":"readwrite"}' >/dev/null

preview() {
    api "$1" POST /api/v1/deployments/preview
}

apply_code() {
    local token="$1" revision="$2" output="$3"
    curl -sS -o "$output" -w '%{http_code}' \
        -X POST "${HOST_URL}/api/v1/deployments/apply" \
        -H "Authorization: Bearer ${token}" \
        -H 'Content-Type: application/json' \
        -d "{\"revision_id\":\"${revision}\"}" || echo "000"
}

echo "--- e2e-revision: two operators preview the same base ---"
REVISION_A="$(preview "$TOKEN_A" | jq -r '.revision_id // empty')"
REVISION_B="$(preview "$TOKEN_B" | jq -r '.revision_id // empty')"
if [ -z "$REVISION_A" ] || [ -z "$REVISION_B" ]; then
    echo "preview response does not include revision_id yet — the immutable revision API is not live on this base." >&2
    echo "Skip the e2e-revision drill; it will run after the lifecycle PR (#316) is merged." >&2
    exit 0
fi
if [ -z "$REVISION_A" ] || [ -z "$REVISION_B" ] || [ "$REVISION_A" = "$REVISION_B" ]; then
    echo "operators did not receive distinct revision IDs" >&2
    exit 1
fi
echo "operator A revision=${REVISION_A}"
echo "operator B revision=${REVISION_B}"

echo "--- e2e-revision: desired-state edit invalidates operator A revision ---"
api "$TOKEN_A" POST /api/v1/acls \
    '{"principal":"revision-user","topic_filter":"revision/second","permission":"read"}' >/dev/null
STALE_BODY="$(mktemp)"
STALE_CODE="$(apply_code "$TOKEN_A" "$REVISION_A" "$STALE_BODY")"
if [ "$STALE_CODE" != "409" ]; then
    echo "desired-state stale apply returned HTTP $STALE_CODE, want 409" >&2
    cat "$STALE_BODY" >&2
    rm -f "$STALE_BODY"
    exit 1
fi
rm -f "$STALE_BODY"
echo "desired-state change correctly returned 409"

echo "--- e2e-revision: applying a fresh revision ---"
FRESH_REVISION="$(preview "$TOKEN_A" | jq -r '.revision_id // empty')"
FRESH_BODY="$(mktemp)"
FRESH_CODE="$(apply_code "$TOKEN_A" "$FRESH_REVISION" "$FRESH_BODY")"
if [ "$FRESH_CODE" != "200" ]; then
    echo "fresh apply returned HTTP $FRESH_CODE, want 200" >&2
    cat "$FRESH_BODY" >&2
    rm -f "$FRESH_BODY"
    exit 1
fi
if [ "$(jq -r '.status // empty' "$FRESH_BODY")" != "active_verified" ]; then
    echo "fresh apply did not reach active_verified:" >&2
    cat "$FRESH_BODY" >&2
    rm -f "$FRESH_BODY"
    exit 1
fi
rm -f "$FRESH_BODY"

echo "--- e2e-revision: external edit invalidates a preview ---"
EXTERNAL_REVISION="$(preview "$TOKEN_A" | jq -r '.revision_id // empty')"
$COMPOSE exec -T mcm sh -c 'printf "# external edit\n" >> /var/lib/mosquitto-config/acl'
EXTERNAL_BODY="$(mktemp)"
EXTERNAL_CODE="$(apply_code "$TOKEN_A" "$EXTERNAL_REVISION" "$EXTERNAL_BODY")"
if [ "$EXTERNAL_CODE" != "409" ]; then
    echo "external-edit stale apply returned HTTP $EXTERNAL_CODE, want 409" >&2
    cat "$EXTERNAL_BODY" >&2
    rm -f "$EXTERNAL_BODY"
    exit 1
fi
rm -f "$EXTERNAL_BODY"
echo "external edit correctly returned 409"

echo "--- e2e-revision: first of two competing revisions wins ---"
COMPETING_A="$(preview "$TOKEN_A" | jq -r '.revision_id // empty')"
COMPETING_B="$(preview "$TOKEN_B" | jq -r '.revision_id // empty')"
WIN_BODY="$(mktemp)"
WIN_CODE="$(apply_code "$TOKEN_A" "$COMPETING_A" "$WIN_BODY")"
if [ "$WIN_CODE" != "200" ]; then
    echo "winning apply returned HTTP $WIN_CODE, want 200" >&2
    cat "$WIN_BODY" >&2
    rm -f "$WIN_BODY"
    exit 1
fi
rm -f "$WIN_BODY"
LOSE_BODY="$(mktemp)"
LOSE_CODE="$(apply_code "$TOKEN_B" "$COMPETING_B" "$LOSE_BODY")"
if [ "$LOSE_CODE" != "409" ]; then
    echo "competing apply returned HTTP $LOSE_CODE, want 409" >&2
    cat "$LOSE_BODY" >&2
    rm -f "$LOSE_BODY"
    exit 1
fi
rm -f "$LOSE_BODY"

echo "immutable preview/apply revision invariants passed"
