#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
PRODUCTION_SCRIPT="$SCRIPT_DIR/e2e-listeners.sh"

fail() { printf 'FAIL: %s\n' "$1" >&2; exit 1; }

assert_assembly() {
    local assignment_name="$1"
    local appended_var="$2"
    local assignment output

    assignment="$(grep "^${assignment_name}=" "$PRODUCTION_SCRIPT")" \
        || { printf 'FAIL: could not find production assignment %s\n' "$assignment_name" >&2; return 1; }

    LISTENERS='[]'
    NEW_LISTENER='{"port":2883,"bind":"0.0.0.0","protocols":["mqtt"]}'
    UNMAPPED_LISTENER='{"port":1885,"bind":"0.0.0.0","protocols":["mqtt"]}'
    eval "$assignment"
    output="${!assignment_name}"
    jq -e --argjson expected "${!appended_var}" \
        'length == 1 and .[0] == $expected and (.[0] | type) == "object"' \
        <<<"$output" >/dev/null || {
        printf 'FAIL: %s did not append the expected object to an empty list: %s\n' "$assignment_name" "$output" >&2
        return 1
    }

    LISTENERS='[{"port":1883,"bind":"127.0.0.1","protocols":["mqtt"]}]'
    eval "$assignment"
    output="${!assignment_name}"
    jq -e --argjson expected "${!appended_var}" \
        'length == 2 and .[0] == {"port":1883,"bind":"127.0.0.1","protocols":["mqtt"]} and .[1] == $expected and (.[1] | type) == "object"' \
        <<<"$output" >/dev/null || {
        printf 'FAIL: %s did not preserve the existing list and append the expected object: %s\n' "$assignment_name" "$output" >&2
        return 1
    }

    printf 'PASS: %s handles empty and nonempty listener arrays\n' "$assignment_name"
}

assert_assembly DESIRED NEW_LISTENER
assert_assembly UNMAPPED_DESIRED UNMAPPED_LISTENER

# The production E2E must configure WS, exercise both transports, and clean up
# both listeners without exposing credentials in its output.
grep -Fq 'WS_LISTENER="$(jq -cn' "$PRODUCTION_SCRIPT" \
    && grep -Fq 'port: 9001, bind: "0.0.0.0", protocols: ["websockets"]' "$PRODUCTION_SCRIPT" \
    || fail 'WS listener tuple is missing or uses the wrong backend protocol'
grep -Fq 'ws_set_options(path="/mqtt")' "$PRODUCTION_SCRIPT" || fail 'WS client does not use the /mqtt endpoint'
grep -Fq 'mqtt.Client(transport="websockets")' "$PRODUCTION_SCRIPT" || fail 'WS round-trip does not use paho WebSockets transport'
grep -Fq 'actual[0] != expected_payload' "$PRODUCTION_SCRIPT" || fail 'WS round-trip does not verify payload equality'
grep -Fq 'listener_accepts_ws' "$PRODUCTION_SCRIPT" || fail 'WS listener removal probe is missing'
grep -Fq 'MCM_MOSQUITTO_USERNAME' "$PRODUCTION_SCRIPT" || fail 'TCP/WS publishes do not use broker credentials'
grep -Fq 'apply ws listener removal' "$PRODUCTION_SCRIPT" || fail 'WS cleanup apply step is missing'
grep -Fq 'apply listener removal' "$PRODUCTION_SCRIPT" || fail 'TCP cleanup apply step is missing'
printf 'PASS: listener E2E configures, verifies, and removes TCP and WS listeners\n'

# Exercise the production listener-data hooks through Vitest without the live API or Compose.
VITEST_OUTPUT="$(cd "$SCRIPT_DIR/../frontend" && npx vitest run --reporter=verbose \
    "src/features/broker-config/__fixtures__/listenerValidation.fixture.test.ts" \
    "src/features/broker-config/listenerValidation.test.ts" \
    "src/features/broker-config/ListenersPanel.test.tsx" 2>&1)" \
    || fail 'listener validation Vitest regression tests failed'
for expected in \
    'accepts a well-formed listener' \
    'reports duplicate listener tuples' \
    'maps compose host-port conflicts to listener IDs' \
    'listenerValidation.fixture.test.ts' \
    'listenerValidation.test.ts' \
    'ListenersPanel.test.tsx'; do
    grep -Fq "$expected" <<<"$VITEST_OUTPUT" || fail "Vitest output omitted expected result: $expected"
done
printf 'PASS: production listener validation and panel tests pass through Vitest\n'

# Load the production probe functions without running the live API flow.
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
awk '/# BEGIN listener removal probe/{capture=1; next} /# END listener removal probe/{capture=0} capture' "$PRODUCTION_SCRIPT" > "$TMP_DIR/probe-functions.sh"
[[ -s "$TMP_DIR/probe-functions.sh" ]] || fail 'listener removal probe functions are missing'
# shellcheck source=/dev/null
pass() { printf 'PASS: step %s: %s\n' "$1" "$2"; }
skip() { printf 'SKIP: %s\n' "$1"; }
export MCM_MOSQUITTO_USERNAME=admin MCM_MOSQUITTO_PASSWORD=test-password
source "$TMP_DIR/probe-functions.sh"

command -v nc >/dev/null 2>&1 || { printf 'FAIL: nc is required for the offline TCP fixture\n' >&2; exit 1; }
TEST_PORT=$((20000 + ($$ % 20000)))
cat > "$TMP_DIR/tcp-server.py" <<'PY'
import socket, sys, time
s = socket.socket()
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1])))
s.listen(1)
s.settimeout(0.2)
while True:
    try:
        conn, _ = s.accept()
        conn.close()
    except socket.timeout:
        pass
PY
cat > "$TMP_DIR/docker" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$*" == *'--network mcm_default eclipse-mosquitto:2.0 mosquitto_pub -h mcm-mosquitto'* ]] || exit 125
[[ "$*" == *"-p $MCM_E2E_LISTENER_PORT"* ]] || exit 125
nc -z 127.0.0.1 "$FAKE_LISTENER_PORT"
SH
chmod +x "$TMP_DIR/docker"

python3 "$TMP_DIR/tcp-server.py" "$TEST_PORT" &
SERVER_PID=$!
trap 'kill "$SERVER_PID" 2>/dev/null || true; /bin/rm -rf "$TMP_DIR"' EXIT
sleep 0.2
export PATH="$TMP_DIR:/usr/bin:/bin"
export FAKE_LISTENER_PORT="$TEST_PORT" MCM_E2E_LISTENER_PORT=1884
LISTENER_PROBE_MODE=''
listener_accepts_mqtt || fail 'probe did not recognize a real MQTT-listener fixture'
[[ "$LISTENER_PROBE_MODE" == docker ]] || fail 'probe did not prefer the containerized MQTT check'
printf 'PASS: probe recognizes a live listener through the broker network\n'
if ( listener_removed_check ) > "$TMP_DIR/listener-present.out" 2>&1; then
    fail 'listener removal check did not fail E2E when a real listener remained'
fi
grep -Fq 'listener port 1884 remained open after removal' "$TMP_DIR/listener-present.out" \
    || fail 'listener removal check did not preserve the step-19 failure'
printf 'PASS: step 19 fails when a real listener remains\n'

kill "$SERVER_PID" 2>/dev/null || true
wait "$SERVER_PID" 2>/dev/null || true
SERVER_PID=''
if listener_accepts_mqtt; then
    fail 'probe treated a published-but-empty port as a real MQTT listener'
fi
[[ "$LISTENER_PROBE_MODE" == docker ]] || fail 'empty-port probe did not use the MQTT mechanism'
printf 'PASS: probe rejects a published-but-empty port\n'
if ! ( listener_removed_check ) > "$TMP_DIR/no-listener.out" 2>&1; then
    fail 'listener removal check failed E2E although no listener was listening'
fi

mkdir "$TMP_DIR/no-tools"
PATH="$TMP_DIR/no-tools"; export PATH
LISTENER_PROBE_MODE=''
if listener_accepts_mqtt; then fail 'probe unexpectedly succeeded without any probe mechanism'; fi
[[ "$LISTENER_PROBE_MODE" == none ]] || fail 'probe did not report unavailable mechanisms'
output="$(listener_removed_check)"
[[ "$output" == *'SKIP:'* ]] || fail 'step 19 did not skip cleanly without nc or Docker'
printf 'PASS: step 19 skips when neither probe mechanism is available\n'
