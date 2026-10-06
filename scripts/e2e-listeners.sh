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
MCM_E2E_WS_PORT=9001
MCM_MOSQUITTO_COMPOSE_PATH="${MCM_MOSQUITTO_COMPOSE_PATH:-}"
MCM_MOSQUITTO_SERVICE="${MCM_MOSQUITTO_SERVICE:-mosquitto}"
MCM_MOSQUITTO_USERNAME="${MCM_MOSQUITTO_USERNAME:-admin}"
MCM_MOSQUITTO_PASSWORD="${MCM_MOSQUITTO_PASSWORD:-mcm-dev-broker-password}"

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

listener_accepts_ws() {
    python3 - "$MCM_MOSQUITTO_USERNAME" "$MCM_MOSQUITTO_PASSWORD" <<'PY' >/dev/null 2>&1
import sys
import paho.mqtt.client as mqtt
client = mqtt.Client(transport="websockets")
client.username_pw_set(sys.argv[1], sys.argv[2])
client.ws_set_options(path="/mqtt")
try:
    client.connect("mcm-mosquitto", 9001, 5)
    client.disconnect()
except Exception:
    raise SystemExit(1)
PY
}

wait_for_ready
printf '%s\n' "Listener Compose context: path=${MCM_MOSQUITTO_COMPOSE_PATH:-not set}, service=${MCM_MOSQUITTO_SERVICE}"

LISTENERS="$(curl -fsS -H "Authorization: Bearer ${MCM_AUTH_TOKEN}" "${MCM_BASE_URL}/api/v1/listeners" | jq -c '.specs // []')" \
    || fail 'could not fetch existing listener specs'
pass 2 'list listeners'

NEW_LISTENER="$(jq -cn --argjson port "$MCM_E2E_LISTENER_PORT" '{port: $port, bind: "0.0.0.0", protocols: ["mqtt"]}')"
DESIRED="$(jq -c --argjson new "$NEW_LISTENER" '. + [$new]' <<<"$LISTENERS")"
preview "$DESIRED"
pass 3 'preview listener addition'
apply
pass 4 'apply listener addition'
sleep 5
pass 5 'wait for listener restart'

if [[ -z "$MCM_MOSQUITTO_COMPOSE_PATH" || ! -r "$MCM_MOSQUITTO_COMPOSE_PATH" ]] || \
    ! grep -Eq '9001:9001' "$MCM_MOSQUITTO_COMPOSE_PATH"; then
    fail 'WS listener port 9001 is not mapped by the configured Compose override'
fi
WS_LISTENER="$(jq -cn '{port: 9001, bind: "0.0.0.0", protocols: ["ws"]}')"
WS_DESIRED="$(jq -c --argjson new "$WS_LISTENER" '. + [$new]' <<<"$DESIRED")"
preview "$WS_DESIRED"
pass 6 'preview ws listener addition'
apply
pass 7 'apply ws listener'
sleep 5
pass 8 'wait for ws listener restart'

if command -v nc >/dev/null 2>&1; then
    nc -zv 127.0.0.1 "$MCM_E2E_LISTENER_PORT" || fail "listener port ${MCM_E2E_LISTENER_PORT} is not open"
    pass 6 'listener port is open'
else
    skip 'nc is not installed; listener port-open check skipped'
fi

if ! python3 -c 'import paho.mqtt.client' >/dev/null 2>&1; then
    if ! command -v pip3 >/dev/null 2>&1; then
        sudo apt-get update -qq && sudo apt-get install -y python3-pip >/dev/null \
            || fail 'could not install python3-pip for the WS listener check'
    fi
    pip3 install --quiet paho-mqtt >/dev/null 2>&1 \
        || fail 'could not install paho-mqtt for the WS listener check'
    if [[ -n "${GITHUB_ENV:-}" ]]; then
        printf '%s\n' 'MCM_E2E_PAHO_MQTT_INSTALLED=1' >> "$GITHUB_ENV"
    fi
fi

if command -v docker >/dev/null 2>&1; then
    TCP_ROUNDTRIP="$(docker run --rm --network mcm_default eclipse-mosquitto:2.0 sh -ec '
        mosquitto_sub -h mcm-mosquitto -p "$1" -u "$2" -P "$3" -t "$4" -C 1 > /tmp/mcm-listener-message &
        subscriber=$!
        sleep 1
        mosquitto_pub -h mcm-mosquitto -p "$1" -u "$2" -P "$3" -t "$4" -m "$5"
        wait "$subscriber"
        cat /tmp/mcm-listener-message
    ' sh "$MCM_E2E_LISTENER_PORT" "$MCM_MOSQUITTO_USERNAME" "$MCM_MOSQUITTO_PASSWORD" 'mcm/listeners/tcp-e2e' 'tcp-ok' 2>/dev/null)" \
        || fail 'TCP publish/subscribe round-trip through listener failed'
    [[ "$TCP_ROUNDTRIP" == 'tcp-ok' ]] || fail 'TCP listener round-trip payload mismatch'
    pass 9 'TCP publish/subscribe round-trip'
else
    fail 'Docker is required for listener transport checks on mcm_default'
fi

if ! python3 -c 'import paho.mqtt.client' >/dev/null 2>&1; then
    fail 'paho-mqtt is not importable after installation'
fi
MCM_E2E_WS_HOST=mcm-mosquitto docker run --rm --network mcm_default eclipse-mosquitto:2.0 python3 - \
    "$MCM_MOSQUITTO_USERNAME" "$MCM_MOSQUITTO_PASSWORD" <<'PY' \
    || fail 'WS publish/subscribe round-trip failed'
import os, sys, threading, uuid
import paho.mqtt.client as mqtt

username, password = sys.argv[1:3]
topic = "mcm/listeners/ws-e2e/" + uuid.uuid4().hex
expected_payload = "ws-roundtrip-" + uuid.uuid4().hex
received = threading.Event()
connected = threading.Event()
subscribed = threading.Event()
actual = []
client = mqtt.Client(transport="websockets")
client.username_pw_set(username, password)
client.ws_set_options(path="/mqtt")

def on_connect(client, userdata, flags, reason_code, properties=None):
    if int(reason_code) == 0:
        connected.set()

def on_subscribe(client, userdata, mid, granted_qos, properties=None):
    subscribed.set()

def on_message(client, userdata, message):
    actual.append(message.payload.decode())
    received.set()

client.on_connect = on_connect
client.on_subscribe = on_subscribe
client.on_message = on_message
try:
    client.connect(os.environ.get("MCM_E2E_WS_HOST", "mcm-mosquitto"), 9001, 10)
    client.loop_start()
    if not connected.wait(8):
        raise RuntimeError("WS connection was not established")
    client.subscribe(topic, qos=1)
    if not subscribed.wait(8):
        raise RuntimeError("WS subscription was not established")
    info = client.publish(topic, expected_payload, qos=1)
    if info.rc != mqtt.MQTT_ERR_SUCCESS:
        raise RuntimeError("WS publish was rejected")
    info.wait_for_publish(timeout=8)
    if not received.wait(8) or actual[0] != expected_payload:
        raise RuntimeError("WS payload was not received exactly")
finally:
    client.loop_stop()
    client.disconnect()
PY
pass 10 'WS publish/subscribe round-trip'

preview "$DESIRED"
pass 11 'preview ws listener removal'
apply
pass 12 'apply ws listener removal'
sleep 5
pass 13 'wait for ws listener removal restart'
if listener_accepts_ws; then
    fail 'WS listener port 9001 remained open after removal'
fi
pass 14 'WS listener port is closed after removal'
preview "$LISTENERS"
pass 15 'preview TCP listener removal'
apply
pass 16 'apply listener removal'
sleep 5
pass 17 'wait for listener removal restart'

# BEGIN listener removal probe
listener_accepts_mqtt() {
    LISTENER_PROBE_MODE=none
    if command -v docker >/dev/null 2>&1; then
        LISTENER_PROBE_MODE=docker
        # Probe the broker over Compose's service network, not the published host
        # port. Suppress client output so no broker credentials can leak to logs.
        docker run --rm --network mcm_default eclipse-mosquitto:2.0 \
            mosquitto_pub -h mcm-mosquitto -p "$MCM_E2E_LISTENER_PORT" \
            -u "$MCM_MOSQUITTO_USERNAME" -P "$MCM_MOSQUITTO_PASSWORD" \
            -t 'mcm/listeners/e2e-probe' -m 'probe' >/dev/null 2>&1
        return $?
    fi
    if command -v nc >/dev/null 2>&1; then
        LISTENER_PROBE_MODE=nc
        nc -z 127.0.0.1 "$MCM_E2E_LISTENER_PORT" >/dev/null 2>&1
        return $?
    fi
    return 1
}

listener_removed_check() {
    if [[ "$LISTENER_PROBE_MODE" == none ]]; then
        skip 'neither docker MQTT probe nor nc is available; listener port-closed check skipped'
        return 0
    fi
    if listener_accepts_mqtt; then
        fail "listener port ${MCM_E2E_LISTENER_PORT} remained open after removal"
    fi
    pass 11 'listener port is closed after removal'
}
# END listener removal probe

if ! command -v docker >/dev/null 2>&1 && ! command -v nc >/dev/null 2>&1; then
    LISTENER_PROBE_MODE=none
else
    LISTENER_PROBE_MODE=''
fi
listener_removed_check

UNMAPPED_LISTENER="$(jq -cn '{port: 1885, bind: "0.0.0.0", protocols: ["mqtt"]}')"
UNMAPPED_DESIRED="$(jq -c --argjson new "$UNMAPPED_LISTENER" '. + [$new]' <<<"$LISTENERS")"
response_file
UNMAPPED_BODY="$RESPONSE_FILE"
UNMAPPED_CODE="$(post_json '/api/v1/listeners/preview' "$(jq -cn --argjson specs "$UNMAPPED_DESIRED" '{specs: $specs, confirm: false}')" "$UNMAPPED_BODY")"
if [[ "$UNMAPPED_CODE" != '409' ]] || ! grep -Eqi 'compose|unmapped' "$UNMAPPED_BODY"; then
    fail "unmapped listener preview expected HTTP 409 with compose/unmapped message; got HTTP ${UNMAPPED_CODE}: $(<"$UNMAPPED_BODY")"
fi
pass 12 'reject unmapped Compose port'
printf '%b\n' "${GREEN}✓ e2e-listeners: all steps passed${RESET}"
