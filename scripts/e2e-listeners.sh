#!/usr/bin/env bash
# End-to-end listener management test for issue #299.
#
# MCM owns the Docker Compose restart through DockerComposeRestartRunner.
# The Compose-related environment values are retained for operator context only;
# this script does not run Docker Compose itself.
set -euo pipefail

readonly GREEN='\033[0;32m'
readonly RED='\033[0;31m'
readonly YELLOW='\033[0;33m'
readonly RESET='\033[0m'

MCM_BASE_URL="${MCM_BASE_URL:-http://127.0.0.1:8080}"
MCM_E2E_LISTENER_PORT="${MCM_E2E_LISTENER_PORT:-1884}"
MCM_MOSQUITTO_COMPOSE_PATH="${MCM_MOSQUITTO_COMPOSE_PATH:-}"
MCM_MOSQUITTO_SERVICE="${MCM_MOSQUITTO_SERVICE:-mosquitto}"

if [[ -z "${MCM_AUTH_TOKEN:-}" ]]; then
    printf '%b\n' "${RED}✗ MCM_AUTH_TOKEN is required${RESET}" >&2
    exit 1
fi

TMP_FILES=()
cleanup() {
    local exit_code=$?
    local file
    for file in "${TMP_FILES[@]}"; do
        rm -f "$file"
    done
    exit "$exit_code"
}
trap cleanup EXIT

pass() {
    printf '%b\n' "${GREEN}✓ step $1: $2${RESET}"
}

fail() {
    printf '%b\n' "${RED}✗ $1${RESET}" >&2
    exit 1
}

skip() {
    printf '%b\n' "${YELLOW}SKIP: $1${RESET}"
}

response_file() {
    RESPONSE_FILE="$(mktemp)"
    TMP_FILES+=("$RESPONSE_FILE")
}

wait_for_ready() {
    local deadline=$((SECONDS + 30))
    while (( SECONDS < deadline )); do
        if curl -fsS "${MCM_BASE_URL}/readyz" >/dev/null; then
            pass 1 "wait_for_ready"
            return
        fi
        sleep 1
    done
    fail "MCM did not become ready at ${MCM_BASE_URL}/readyz within 30 seconds"
}

post_json() {
    local endpoint="$1"
    local payload="$2"
    local output="$3"
    curl -sS -o "$output" -w '%{http_code}' \
        -X POST "${MCM_BASE_URL}${endpoint}" \
        -H "Authorization: Bearer ${MCM_AUTH_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$payload" || printf '000'
}

preview() {
    local desired="$1"
    local output code
    response_file
    output="$RESPONSE_FILE"
    code="$(post_json '/api/v1/listeners/preview' "$(jq -cn --argjson specs "$desired" '{specs: $specs, confirm: false}')" "$output")"
    if [[ "$code" != '200' ]]; then
        fail "listener preview failed with HTTP ${code}: $(<"$output")"
    fi
    if [[ "$(jq -r '.needs_restart // false' "$output")" != 'true' ]]; then
        fail "listener preview did not require a restart: $(<"$output")"
    fi
    REVISION_ID="$(jq -r '.revision_id // empty' "$output")"
    if [[ -z "$REVISION_ID" ]]; then
        fail "listener preview did not return revision_id: $(<"$output")"
    fi
}

apply() {
    local output code
    response_file
    output="$RESPONSE_FILE"
    code="$(post_json '/api/v1/listeners/apply' "$(jq -cn --arg revision_id "$REVISION_ID" '{revision_id: $revision_id, confirm: true}')" "$output")"
    if [[ "$code" != '200' ]]; then
        fail "listener apply failed with HTTP ${code}: $(<"$output")"
    fi
}

wait_for_ready
printf '%s\n' "Listener Compose context: path=${MCM_MOSQUITTO_COMPOSE_PATH:-not set}, service=${MCM_MOSQUITTO_SERVICE}"

LISTENERS="$(curl -fsS -H "Authorization: Bearer ${MCM_AUTH_TOKEN}" "${MCM_BASE_URL}/api/v1/listeners" | jq -c '.specs // []')" \
    || fail 'could not fetch existing listener specs'
pass 2 'list listeners'

NEW_LISTENER="$(jq -cn --argjson port "$MCM_E2E_LISTENER_PORT" '{port: $port, bind: "0.0.0.0", protocols: ["mqtt"]}')"
DESIRED="$(printf '%s\n%s\n' "$LISTENERS" "$NEW_LISTENER" | jq -cs 'add')"
preview "$DESIRED"
pass 3 'preview listener addition'
apply
pass 4 'apply listener addition'
sleep 5
pass 5 'wait for listener restart'

if command -v nc >/dev/null 2>&1; then
    nc -zv 127.0.0.1 "$MCM_E2E_LISTENER_PORT" || fail "listener port ${MCM_E2E_LISTENER_PORT} is not open"
    pass 6 'listener port is open'
else
    skip 'nc is not installed; listener port-open check skipped'
fi

if command -v mosquitto_pub >/dev/null 2>&1; then
    mosquitto_pub -h 127.0.0.1 -p "$MCM_E2E_LISTENER_PORT" -t 'mcm/listeners/e2e' -m 'ok' \
        || fail "MQTT publish to listener port ${MCM_E2E_LISTENER_PORT} failed"
    pass 7 'publish through new listener'
else
    skip 'mosquitto_pub is not installed; MQTT publish check skipped'
fi

preview "$LISTENERS"
pass 8 'preview listener removal'
apply
pass 9 'apply listener removal'
sleep 5
pass 10 'wait for listener removal restart'

if command -v nc >/dev/null 2>&1; then
    if nc -zv 127.0.0.1 "$MCM_E2E_LISTENER_PORT"; then
        fail "listener port ${MCM_E2E_LISTENER_PORT} remained open after removal"
    fi
    pass 11 'listener port is closed after removal'
else
    skip 'nc is not installed; listener port-closed check skipped'
fi

UNMAPPED_LISTENER="$(jq -cn '{port: 1885, bind: "0.0.0.0", protocols: ["mqtt"]}')"
UNMAPPED_DESIRED="$(printf '%s\n%s\n' "$LISTENERS" "$UNMAPPED_LISTENER" | jq -cs 'add')"
response_file
UNMAPPED_BODY="$RESPONSE_FILE"
UNMAPPED_CODE="$(post_json '/api/v1/listeners/preview' "$(jq -cn --argjson specs "$UNMAPPED_DESIRED" '{specs: $specs, confirm: false}')" "$UNMAPPED_BODY")"
if [[ "$UNMAPPED_CODE" != '409' ]] || ! grep -Eqi 'compose|unmapped' "$UNMAPPED_BODY"; then
    fail "unmapped listener preview expected HTTP 409 with compose/unmapped message; got HTTP ${UNMAPPED_CODE}: $(<"$UNMAPPED_BODY")"
fi
pass 12 'reject unmapped Compose port'
printf '%b\n' "${GREEN}✓ e2e-listeners: all steps passed${RESET}"
