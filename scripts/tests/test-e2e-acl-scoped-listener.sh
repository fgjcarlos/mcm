#!/usr/bin/env bash
# E2E test for the listener-scoped ACL rules (issue #301).
#
# The unit tests prove that MCM renders a listener-scoped rule into the ACL
# file of that listener and that the broker reads one file per listener.
# This script proves the whole path against a live broker: MCM renders the
# files, the broker applies them to the listener they belong to, and the
# scoped rule does not leak into the broker-wide access.
#
#   1. The dev Compose stack comes up with MCM managing the broker
#      configuration (the listener writer needs the shared volume).
#   2. A listener with its own port is added and applied.
#   3. An MQTT user is created; a broker-wide rule and a listener-scoped
#      rule grant it two different topics.
#   4. The deployment is applied: MCM writes the broker-wide ACL file and
#      the ACL file of the listener.
#   5. The listener configuration is applied, so the broker restarts with
#      the acl_file of the listener.
#   6. On the scoped listener the user may publish to the scoped topic and
#      may not publish to the broker-wide one; on the broker-wide listener
#      the opposite holds.
#
# Invariants asserted:
#   1. /livez returns 200 within the wait budget.
#   2. POST /auth/login returns 200 + token.
#   3. POST /mqtt-users returns 201 + the cleartext password.
#   4. POST /acls returns 201 for both rules.
#   5. POST /deployments/preview shows the file of the listener.
#   6. POST /deployments/apply returns 200 and the per-listener file is
#      written into the shared volume.
#   7. On the scoped listener the scoped topic reaches a subscriber and
#      the broker-wide topic does not.
#   8. On the broker-wide listener the broker-wide topic reaches a
#      subscriber and the scoped topic does not.
#
# The verdict of a rule is proven by the delivery of a message, not by the
# exit code of a publish: MQTT 3.1.1 has no negative acknowledgement for a
# publish, so the broker drops a message it refuses and the client sees a
# successful publish. A subscriber is the only client that can tell the
# difference, which is why every assertion below waits for a message.
#
# Exit code:
#   0  every invariant passed.
#   1  any invariant failed (see the failing step's output).
#
# Required: docker (compose v2), curl, jq. The script tries to install
# jq on the fly if missing.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
PORT="${MCM_PORT:-8080}"
HOST_URL="http://localhost:${PORT}"

HEALTH_URL="${HOST_URL}/healthz"
LIVE_URL="${HOST_URL}/livez"
LOGIN_URL="${HOST_URL}/api/v1/auth/login"

# The wait budget for the stack: the compose healthcheck on mcm uses
# start_period=15s, retries=3, interval=30s — 60s+ cold.
WAIT_SECONDS="${E2E_ACL_SCOPE_WAIT_SECONDS:-180}"

# The port of the listener that owns the scoped rules. It is published by
# the Compose override below so the broker accepts the listener.
SCOPED_PORT="${E2E_ACL_SCOPE_PORT:-1886}"
SCOPED_LISTENER_ID="e2e-acl"
# The listener that keeps the broker-wide access. MCM manages the
# listeners of the broker: the listener blocks of the configuration are the
# ones of the specs, so the listener of the broker-wide access has to be
# declared too.
GLOBAL_PORT="${E2E_ACL_SCOPE_GLOBAL_PORT:-1883}"
GLOBAL_LISTENER_ID="e2e-global"

# The MQTT user the rules belong to.
E2E_USER="${E2E_ACL_SCOPE_USER:-e2e-scoped-user}"
# The topic the broker-wide rule grants and the topic the listener-scoped
# rule grants.
GLOBAL_TOPIC="${E2E_ACL_SCOPE_GLOBAL_TOPIC:-e2e/scope/global}"
SCOPED_TOPIC="${E2E_ACL_SCOPE_TOPIC:-e2e/scope/scoped}"

# The Compose override: the listener port is published and MCM reads the
# override to discover the ports of the broker.
OVERRIDE_FILE="${E2E_ACL_SCOPE_OVERRIDE:-/tmp/docker-compose.acl-scope.yml}"

# The Compose project name is "mcm": MCM restarts the broker through the
# same project, so the stack has to run under it.
COMPOSE=(docker compose -f docker-compose.yml -f "$OVERRIDE_FILE" -p mcm)

GREEN='\033[0;32m'
RED='\033[0;31m'
RESET='\033[0m'

pass() {
    printf '%b\n' "${GREEN}✓ step $1: $2${RESET}"
}

fail() {
    printf '%b\n' "${RED}✗ $1${RESET}" >&2
    exit 1
}

# The body of the last response. It is kept in a file because the helper
# runs in a command substitution, where a variable assignment would not
# reach the caller.
BODY_FILE="$(mktemp)"

cleanup() {
    local exit_code=$?
    printf '%s\n' "--- e2e-acl-scoped-listener: tearing down (exit=$exit_code) ---"
    (cd "$REPO_ROOT" && "${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1) || true
    rm -f "$OVERRIDE_FILE" "$BODY_FILE"
    exit "$exit_code"
}
trap cleanup EXIT

if ! command -v jq >/dev/null 2>&1; then
    echo "--- e2e-acl-scoped-listener: installing jq ---"
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

if ! command -v docker >/dev/null 2>&1; then
    echo "Docker is required for the scoped listener e2e" >&2
    exit 1
fi

cd "$REPO_ROOT"

cat > "$OVERRIDE_FILE" <<EOF
services:
  mosquitto:
    image: eclipse-mosquitto:2.0
    ports:
      - "1883:1883"
      - "9001:9001"
      - "${SCOPED_PORT}:${SCOPED_PORT}"
  mcm:
    image: mcm:dev
    environment:
      MCM_MOSQUITTO_COMPOSE_PATH: ${OVERRIDE_FILE}
    volumes:
      - ${OVERRIDE_FILE}:${OVERRIDE_FILE}:ro
EOF

# MCM manages the broker configuration in this scenario: the listener
# writer splices the listener blocks into the shared mosquitto.conf, which
# is how the acl_file of the listener reaches the broker.
export MCM_MOSQUITTO_CONFIG_DIR=/var/lib/mosquitto-config

# The stack starts from a clean slate: the bootstrap admin password is
# logged once, on the first boot of an empty database.
"${COMPOSE[@]}" down -v --remove-orphans >/dev/null 2>&1 || true

echo "--- e2e-acl-scoped-listener: up (image already loaded) ---"
"${COMPOSE[@]}" up -d --no-build

echo "--- e2e-acl-scoped-listener: waiting for /livez (${WAIT_SECONDS}s budget) ---"
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
    "${COMPOSE[@]}" logs --no-color mcm >&2 || true
    "${COMPOSE[@]}" logs --no-color mosquitto >&2 || true
    exit 1
fi
if ! curl -fsS "$HEALTH_URL" >/dev/null; then
    echo "healthz did not return 200" >&2
    exit 1
fi
pass 1 'the stack is up'

echo "--- e2e-acl-scoped-listener: extracting bootstrap admin password ---"
LOG_FILE="$(mktemp)"
"${COMPOSE[@]}" logs --no-color mcm > "$LOG_FILE" || true
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

echo "--- e2e-acl-scoped-listener: POST /api/v1/auth/login ---"
login_code="$(curl -sS -o "$BODY_FILE" -w '%{http_code}' \
    -X POST "$LOGIN_URL" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"${ADMIN_PASSWORD}\"}" || echo "000")"
if [ "$login_code" != "200" ]; then
    echo "login failed with HTTP $login_code:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
TOKEN="$(jq -r '.token // .access_token // .jwt // empty' "$BODY_FILE")"
if [ -z "$TOKEN" ]; then
    echo "login returned 200 but no token field was present" >&2
    exit 1
fi
pass 2 'the operator is authenticated'

# post_json sends a request and writes the response body into $BODY_FILE.
# It returns the HTTP status code.
post_json() {
    local path="$1"
    local body="$2"
    curl -sS -o "$BODY_FILE" -w '%{http_code}' \
        -X POST "${HOST_URL}${path}" \
        -H "Authorization: Bearer ${TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$body" || echo "000"
}

# apply_listener_configuration previews and applies the given listener
# specs. It fails when the broker refuses the change.
apply_listener_configuration() {
    local specs="$1"
    local code revision
    code="$(post_json '/api/v1/listeners/preview' \
        "$(jq -cn --argjson specs "$specs" '{specs: $specs, confirm: false}')")"
    if [ "$code" != "200" ]; then
        echo "the listener preview failed with HTTP ${code}:" >&2
        cat "$BODY_FILE" >&2
        return 1
    fi
    revision="$(jq -r '.revision_id // empty' "$BODY_FILE")"
    if [ -z "$revision" ]; then
        echo "the listener preview carries no revision id:" >&2
        cat "$BODY_FILE" >&2
        return 1
    fi
    code="$(post_json '/api/v1/listeners/apply' \
        "$(jq -cn --arg id "$revision" '{revision_id: $id, confirm: true}')")"
    if [ "$code" != "200" ]; then
        echo "the listener apply failed with HTTP ${code}:" >&2
        cat "$BODY_FILE" >&2
        "${COMPOSE[@]}" logs --no-color mosquitto >&2 || true
        return 1
    fi
    return 0
}

echo "--- e2e-acl-scoped-listener: POST /api/v1/mqtt-users (create ${E2E_USER}) ---"
user_code="$(post_json '/api/v1/mqtt-users' \
    "$(jq -cn --arg u "$E2E_USER" '{username: $u}')")"
if [ "$user_code" != "201" ]; then
    echo "creating the MQTT user failed with HTTP ${user_code}:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
USER_PASSWORD="$(jq -r '.password // empty' "$BODY_FILE")"
if [ -z "$USER_PASSWORD" ]; then
    echo "the MQTT user response carries no cleartext password:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
pass 3 "the MQTT user ${E2E_USER} exists"

echo "--- e2e-acl-scoped-listener: adding the listener ${SCOPED_LISTENER_ID} ---"
LISTENERS="$(curl -fsS -H "Authorization: Bearer ${TOKEN}" \
    "${HOST_URL}/api/v1/listeners" | jq -c '.specs // []')" \
    || fail 'could not fetch the listener specs'
NEW_LISTENER="$(jq -cn --arg id "$SCOPED_LISTENER_ID" --argjson port "$SCOPED_PORT" \
    '{id: $id, port: $port, bind: "0.0.0.0", protocols: ["mqtt"]}')"
GLOBAL_LISTENER="$(jq -cn --arg id "$GLOBAL_LISTENER_ID" --argjson port "$GLOBAL_PORT" \
    '{id: $id, port: $port, bind: "0.0.0.0", protocols: ["mqtt"]}')"
DESIRED="$(jq -c --argjson new "$NEW_LISTENER" --argjson global "$GLOBAL_LISTENER" \
    '. + [$global] + [$new]' <<<"$LISTENERS")"
apply_listener_configuration "$DESIRED" || exit 1
sleep 5
pass 4 "the listeners ${GLOBAL_LISTENER_ID} and ${SCOPED_LISTENER_ID} are applied"

echo "--- e2e-acl-scoped-listener: POST /api/v1/acls (the broker-wide rule) ---"
global_code="$(post_json '/api/v1/acls' "$(jq -cn \
    --arg u "$E2E_USER" --arg topic "$GLOBAL_TOPIC" \
    '{selector: "user", scope: "global", principal: $u, topic_filter: $topic, permission: "readwrite"}')")"
if [ "$global_code" != "201" ]; then
    echo "creating the broker-wide rule failed with HTTP ${global_code}:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi

echo "--- e2e-acl-scoped-listener: POST /api/v1/acls (the listener-scoped rule) ---"
scoped_code="$(post_json '/api/v1/acls' "$(jq -cn \
    --arg u "$E2E_USER" --arg topic "$SCOPED_TOPIC" --arg listener "$SCOPED_LISTENER_ID" \
    '{selector: "user", scope: "listener", listener_id: $listener, principal: $u, topic_filter: $topic, permission: "readwrite"}')")"
if [ "$scoped_code" != "201" ]; then
    echo "creating the listener-scoped rule failed with HTTP ${scoped_code}:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
pass 5 'both rules exist'

echo "--- e2e-acl-scoped-listener: POST /api/v1/deployments/preview ---"
preview_code="$(post_json '/api/v1/deployments/preview' '{}')"
if [ "$preview_code" != "200" ]; then
    echo "the deployment preview failed with HTTP ${preview_code}:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
BODY="$(cat "$BODY_FILE")"
DEPLOY_REVISION_ID="$(jq -r '.revision_id // empty' <<<"$BODY")"
if [ -z "$DEPLOY_REVISION_ID" ]; then
    echo "the deployment preview carries no revision id:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
if [ "$(jq -r '.has_changes // false' <<<"$BODY")" != "true" ]; then
    echo "the deployment preview reports no changes:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
if [ "$(jq -r '[.listener_acl_files[]? | select(.listener_id == "'"$SCOPED_LISTENER_ID"'")] | length' <<<"$BODY")" != "1" ]; then
    echo "the deployment preview does not show the file of the listener:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
pass 6 'the preview shows the per-listener file'

echo "--- e2e-acl-scoped-listener: POST /api/v1/deployments/apply ---"
apply_code="$(post_json '/api/v1/deployments/apply' \
    "$(jq -cn --arg id "$DEPLOY_REVISION_ID" '{revision_id: $id}')")"
if [ "$apply_code" != "200" ]; then
    echo "the deployment apply failed with HTTP ${apply_code}:" >&2
    cat "$BODY_FILE" >&2
    exit 1
fi
pass 7 'the deployment is applied'

# The file of the listener has to be in the shared volume: the broker
# refuses to start without it.
if ! "${COMPOSE[@]}" exec -T mosquitto \
    sh -ec "test -f /mosquitto/config/acl.${SCOPED_LISTENER_ID}"; then
    fail "the broker does not see the file of the listener (acl.${SCOPED_LISTENER_ID})"
fi
pass 8 'the broker sees the file of the listener'

echo "--- e2e-acl-scoped-listener: applying the listener configuration ---"
# The broker reads the file of a listener only once the configuration
# declares it. The deploy wrote the file; the listener apply declares it
# and the broker restarts with it.
LISTENERS="$(curl -fsS -H "Authorization: Bearer ${TOKEN}" \
    "${HOST_URL}/api/v1/listeners" | jq -c '.specs // []')" \
    || fail 'could not fetch the listener specs'
apply_listener_configuration "$LISTENERS" || exit 1
sleep 5
pass 9 'the listener configuration is applied'

# The verdict of a rule is read from the delivery of a message: a
# subscriber that receives the message proves the rule applies, a
# subscriber that times out proves the rule does not.
#
# The probes run inside the broker container: its client tools talk to the
# listeners over the loopback interface, so no extra container and no
# published port is involved.
#
# delivers <port> <topic> returns 0 when a message published on that port
# reaches a subscriber of that topic on the same port.
delivers() {
    local port="$1" topic="$2" pid rc=0
    local sub_log
    sub_log="$(mktemp)"
    (
        timeout 30 "${COMPOSE[@]}" exec -T mosquitto \
            mosquitto_sub -h 127.0.0.1 -p "$port" \
            -u "$E2E_USER" -P "$USER_PASSWORD" \
            -t "$topic" -q 1 -C 1 -W 8 \
            >"$sub_log" 2>&1
    ) &
    pid=$!
    # The subscription has to be in place before the message is published.
    sleep 2
    # The message is published repeatedly: the first one can arrive before
    # the subscription is registered.
    for _ in 1 2 3 4 5 6; do
        "${COMPOSE[@]}" exec -T mosquitto \
            mosquitto_pub -h 127.0.0.1 -p "$port" \
            -u "$E2E_USER" -P "$USER_PASSWORD" \
            -t "$topic" -q 1 -m 'e2e-acl-scope' >/dev/null 2>&1 || true
        if ! kill -0 "$pid" 2>/dev/null; then
            break
        fi
        sleep 1
    done
    wait "$pid" || rc=$?
    if [ "$rc" -ne 0 ]; then
        echo "    the subscriber of ${topic} on ${port} received nothing:" >&2
        cat "$sub_log" >&2 || true
    fi
    rm -f "$sub_log"
    return "$rc"
}

echo "--- e2e-acl-scoped-listener: the scoped listener ---"
if ! delivers "$SCOPED_PORT" "$SCOPED_TOPIC"; then
    fail "the rule of the scoped listener does not apply on ${SCOPED_PORT}"
fi
pass 10 "the user may use ${SCOPED_TOPIC} on the scoped listener"

if delivers "$SCOPED_PORT" "$GLOBAL_TOPIC"; then
    fail "the user used ${GLOBAL_TOPIC} on the scoped listener; the broker-wide rule leaked into the listener file"
fi
pass 11 "the broker-wide topic is refused on the scoped listener"

echo "--- e2e-acl-scoped-listener: the broker-wide listener ---"
if ! delivers "$GLOBAL_PORT" "$GLOBAL_TOPIC"; then
    fail "the broker-wide rule does not apply on ${GLOBAL_PORT}"
fi
pass 12 "the user may use ${GLOBAL_TOPIC} on the broker-wide listener"

if delivers "$GLOBAL_PORT" "$SCOPED_TOPIC"; then
    fail "the user used ${SCOPED_TOPIC} on the broker-wide listener; the scoped rule leaked into the broker-wide file"
fi
pass 13 "the scoped topic is refused on the broker-wide listener"

printf '%b\n' "${GREEN}✓ e2e-acl-scoped-listener: all invariants passed${RESET}"
