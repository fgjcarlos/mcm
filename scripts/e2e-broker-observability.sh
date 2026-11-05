#!/usr/bin/env bash
# End-to-end verification of broker diagnostics against the real Compose stack.
#
# Proves the route reports observable behaviour from a live Mosquitto broker,
# including explicit $SYS metrics, application traffic and broker-owned logs.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOST_URL="${MCM_BASE_URL:-http://127.0.0.1:8080}"
WAIT_SECONDS="${E2E_BROKER_OBSERVABILITY_WAIT_SECONDS:-60}"
DIAGNOSTICS_PATH="/api/v1/broker/diagnostics"

cd "$REPO_ROOT"

compose() {
    docker compose -f docker-compose.yml -p mcm "$@"
}

cleanup() {
    local rc=$?
    echo "--- e2e-broker-observability: cleaning up ---"
    if [ -n "${response_file:-}" ]; then
        rm -f "$response_file"
    fi
    if [ -n "${login_file:-}" ]; then
        rm -f "$login_file"
    fi
    compose down -v >/dev/null 2>&1 || true
    exit "$rc"
}
trap cleanup EXIT

for tool in curl jq docker; do
    command -v "$tool" >/dev/null 2>&1 || { echo "Required tool is unavailable: $tool" >&2; exit 2; }
done

echo "--- e2e-broker-observability: compose up ---"
# The MCM container reaches the Docker daemon through the host's socket
# group. The image bakes the conventional GID 999 at build time, but the
# socket of this host can live under another GID.
export DOCKER_HOST_GID="${DOCKER_HOST_GID:-$(stat -c '%g' /var/run/docker.sock 2>/dev/null || echo 999)}"
compose up -d --no-build

echo "--- waiting for MCM (up to ${WAIT_SECONDS}s) ---"
deadline=$((SECONDS + WAIT_SECONDS))
while (( SECONDS < deadline )); do
    if curl -fsS "${HOST_URL}/livez" >/dev/null 2>&1; then
        break
    fi
    sleep 2
done
if ! curl -fsS "${HOST_URL}/livez" >/dev/null 2>&1; then
    echo "MCM did not become live at ${HOST_URL}/livez within ${WAIT_SECONDS}s" >&2
    exit 1
fi

# The bootstrap administrator's generated password is available only in the
# fresh stack's logs; keep it in shell memory and never print it.
BOOTSTRAP_PASSWORD=""
for _ in $(seq 1 30); do
    BOOTSTRAP_PASSWORD="$(compose logs --no-color mcm 2>/dev/null \
        | grep '"msg":"bootstrap admin created' \
        | sed -n 's/.*"password":"\([^"]*\)".*/\1/p' \
        | tail -1 || true)"
    [ -n "$BOOTSTRAP_PASSWORD" ] && break
    sleep 1
done
if [ -z "$BOOTSTRAP_PASSWORD" ]; then
    echo "could not extract bootstrap admin password from MCM logs" >&2
    exit 1
fi
login_file="$(mktemp)"
trap cleanup EXIT
login_code="$(curl -sS -o "$login_file" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/auth/login" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"${BOOTSTRAP_PASSWORD}\"}")"
TOKEN="$(jq -r '.token // .access_token // .jwt // empty' "$login_file")"
rm -f "$login_file"
if [ "$login_code" != '200' ] || [ -z "$TOKEN" ]; then
    echo "admin login failed (HTTP ${login_code})" >&2
    exit 1
fi
echo "authenticated as bootstrap operator"

request() {
    local endpoint="$1" output="$2"
    curl -sS -o "$output" -w '%{http_code}' "${HOST_URL}${endpoint}" \
        -H "Authorization: Bearer ${TOKEN}"
}

# A request WITHOUT an Authorization header. It must not reuse the token
# helper above: an empty token argument would fall back to the operator
# token and would silently test the authenticated path.
request_unauthenticated() {
    local endpoint="$1" output="$2"
    curl -sS -o "$output" -w '%{http_code}' "${HOST_URL}${endpoint}"
}

response_file="$(mktemp)"
unauth_code="$(request_unauthenticated "$DIAGNOSTICS_PATH" "$response_file")"
if [ "$unauth_code" != '401' ]; then
    echo "unauthenticated diagnostics returned HTTP ${unauth_code}, want 401: $(<"$response_file")" >&2
    exit 1
fi
echo "unauthenticated diagnostics refused (401)"

code="$(request "$DIAGNOSTICS_PATH" "$response_file")"
if [ "$code" != '200' ]; then
    echo "authenticated diagnostics returned HTTP ${code}: $(<"$response_file")" >&2
    exit 1
fi
if ! jq -e '.diagnostics.tcp.status == "reachable" and .diagnostics.mqtt.status == "connected"' "$response_file" >/dev/null; then
    echo "TCP/MQTT diagnostics do not report a live connection: $(<"$response_file")" >&2
    exit 1
fi
# The broker publishes the $SYS tree every sys_interval seconds, so the
# client count of a publication made before this monitor connected still
# reads zero. Wait for a publication that reports the connected client.
deadline=$((SECONDS + 45))
sys_seen=false
while (( SECONDS < deadline )); do
    code="$(request "$DIAGNOSTICS_PATH" "$response_file")"
    if [ "$code" = '200' ] && jq -e '
        .broker_sys.available == true
        and (.broker_sys.metrics["broker/clients/connected"].value | tonumber >= 1)
    ' "$response_file" >/dev/null; then
        sys_seen=true
        break
    fi
    sleep 2
done
if [ "$sys_seen" != true ]; then
    echo "explicit \$SYS subscription did not expose the connected-client metric: $(<"$response_file")" >&2
    exit 1
fi
echo "TCP/MQTT and broker-published client metric are observable"

# Publish an application message the monitor's own "#" subscription must see.
# The service user may only WRITE to mcm/healthcheck (the ACL grants read on
# "#" and readwrite on that topic), so the publish uses the healthcheck topic
# with a unique payload and a unique client id; the uniqueness the assertions
# need comes from the client id the broker writes into its own log.
TEST_SUFFIX="$(date +%s)-$$"
TEST_TOPIC="mcm/healthcheck"
TEST_CLIENT="mcm-e2e-observability-${TEST_SUFFIX}"
before_count="$(jq -r '.sources.observed_traffic.message_count' "$response_file")"
compose exec -T mosquitto mosquitto_pub -h 127.0.0.1 -p 1883 \
    -u admin -P mcm-dev-broker-password -i "$TEST_CLIENT" \
    -t "$TEST_TOPIC" -q 1 -m "observability-${TEST_SUFFIX}" >/dev/null

echo "waiting for application publish to reach observed traffic"
deadline=$((SECONDS + 30))
observed=false
while (( SECONDS < deadline )); do
    code="$(request "$DIAGNOSTICS_PATH" "$response_file")"
    if [ "$code" = '200' ] && jq -e --arg topic "$TEST_TOPIC" --argjson before "$before_count" '
        .sources.observed_traffic.message_count > $before
        and any(.sources.observed_traffic.top_topics[]?; .name == $topic)
        and all(.sources.observed_traffic.top_topics[]?; (.name | startswith("$SYS/")) | not)
    ' "$response_file" >/dev/null; then
        observed=true
        break
    fi
    sleep 1
done
if [ "$observed" != true ]; then
    echo "application traffic was not counted and no \$SYS topic reached the observed traffic: $(<"$response_file")" >&2
    exit 1
fi
echo "application publish counted; observed topics exclude \$SYS"

# Polling the broker file is asynchronous. The test publisher's CONNECT line
# is written by Mosquitto itself and carries the unique client ID.
deadline=$((SECONDS + WAIT_SECONDS))
log_seen=false
while (( SECONDS < deadline )); do
    code="$(request "$DIAGNOSTICS_PATH" "$response_file")"
    if [ "$code" = '200' ] && jq -e --arg client "$TEST_CLIENT" '
        .sources.broker_logs.available == true
        and any(.sources.broker_logs.entries[]?; .kind == "client_connected" and .client_id == $client and (.message | contains("New client connected")))
    ' "$response_file" >/dev/null; then
        log_seen=true
        break
    fi
    sleep 2
done
if [ "$log_seen" != true ]; then
    echo "broker log did not expose Mosquitto's connection transition for ${TEST_CLIENT}: $(<"$response_file")" >&2
    exit 1
fi
if ! jq -e '
    (.sources.mcm_events.note | length > 0)
    and (.sources.internal_metrics.note | length > 0)
' "$response_file" >/dev/null; then
    echo "MCM events/internal metrics are missing their source notes: $(<"$response_file")" >&2
    exit 1
fi
echo "Mosquitto log line ingested; MCM and internal metric source notes are present"

for query in 'severity=verbose' 'limit=201' 'limit=0'; do
    code="$(request "${DIAGNOSTICS_PATH}?${query}" "$response_file")"
    if [ "$code" != '400' ]; then
        echo "invalid diagnostics query '${query}' returned HTTP ${code}, want 400: $(<"$response_file")" >&2
        exit 1
    fi
done
echo "unknown severity and out-of-range limits are refused (400)"

ready_code="$(curl -sS -o "$response_file" -w '%{http_code}' "${HOST_URL}/readyz")"
if [ "$ready_code" != '200' ] || ! jq -e 'keys == ["status"] and .status == "ready"' "$response_file" >/dev/null; then
    echo "/readyz did not retain its readiness-only response: HTTP ${ready_code} $(<"$response_file")" >&2
    exit 1
fi
echo "/readyz is ready and reports only readiness status"
rm -f "$response_file"
echo "--- e2e-broker-observability: all invariants passed ---"
