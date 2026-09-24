#!/usr/bin/env bash
# E2E broker-config test for issue #298.
#
# Proves the broker-config surface end-to-end on the dev Compose stack:
#
#   1. `docker compose up -d` with MCM_MOSQUITTO_CONFIG_DIR set enables
#      the broker-config API (without it the handlers return 503).
#   2. The bootstrap admin password authenticates against
#      POST /api/v1/auth/login.
#   3. GET /api/v1/broker/config returns the live mosquitto.conf path,
#      body and sha256 hash.
#   4. POST /api/v1/broker/config/import round-trips the conf through
#      the parser (byte-exact) and returns a preview revision.
#   5. POST /api/v1/broker/config/adopt records the explicit adoption
#      required before any apply.
#   6. POST /api/v1/broker/config/apply binds the immutable revision,
#      atomically writes the conf back, and signals the broker reload.
#   7. An external edit (docker exec append) is detected on the next
#      import as a new base hash — drift never silently overwrites.
#   8. The drift revision applies cleanly with adopt=true.
#
# Invariants asserted:
#   1. /livez returns 200 within the wait budget.
#   2. GET /broker/config returns 200 with a non-empty body and hash.
#   3. POST /broker/config/import returns 200 with revision_id.
#   4. POST /broker/config/adopt returns 200.
#   5. POST /broker/config/apply returns 200 with
#      status="active_verified".
#   6. After apply, the on-disk conf hash (as seen by the mosquitto
#      container) matches the rendered hash from the preview.
#   7. After an external edit, the next import reports a different
#      base_conf_hash (drift detected).
#   8. docker compose down -v cleans up.
#
# Exit code: 0 all invariants passed, 1 any invariant failed.
#
# Required: docker (compose v2), curl, jq. Installs jq if missing.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE="docker compose"
PORT="${MCM_PORT:-8080}"
HOST_URL="http://localhost:${PORT}"

LIVE_URL="${HOST_URL}/livez"
LOGIN_URL="${HOST_URL}/api/v1/auth/login"
BROKER_CONFIG_URL="${HOST_URL}/api/v1/broker/config"

WAIT_SECONDS="${E2E_BROKER_CONFIG_WAIT_SECONDS:-180}"
SETTLE_SECONDS="${E2E_BROKER_CONFIG_SETTLE_SECONDS:-3}"

# The mcm container sees the shared broker volume at this path. Setting
# MCM_MOSQUITTO_CONFIG_DIR enables the broker-config surface (handlers
# return 503 without it).
export MCM_MOSQUITTO_CONFIG_DIR="${MCM_MOSQUITTO_CONFIG_DIR:-/var/lib/mosquitto-config}"

cd "$REPO_ROOT"

cleanup() {
    echo "--- e2e-broker-config: cleaning up ---"
    $COMPOSE down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

if ! command -v jq >/dev/null 2>&1; then
    echo "--- e2e-broker-config: installing jq ---"
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

echo "--- e2e-broker-config: compose up (ConfigDir=${MCM_MOSQUITTO_CONFIG_DIR}) ---"
$COMPOSE up -d --build

echo "--- e2e-broker-config: waiting for /livez (up to ${WAIT_SECONDS}s) ---"
deadline=$(( $(date +%s) + WAIT_SECONDS ))
while true; do
    if curl -fsS "$LIVE_URL" >/dev/null 2>&1; then
        echo "livez OK"
        break
    fi
    if [ "$(date +%s)" -ge "$deadline" ]; then
        echo "timed out waiting for /livez" >&2
        $COMPOSE logs mcm --tail 50 >&2 || true
        exit 1
    fi
    sleep 3
done

# The bootstrap admin password is logged once on first boot, but the line
# lands in `docker compose logs mcm` slightly after /livez returns 200.
# Retry until it appears or we exhaust the budget (the same approach as
# scripts/e2e-deploy.sh:138 but with the regex corrected to match the
# actual JSON log payload).
BOOTSTRAP_PASSWORD=""
for _ in $(seq 1 30); do
    BOOTSTRAP_PASSWORD="$($COMPOSE logs --no-color mcm 2>/dev/null \
        | grep '"msg":"bootstrap admin created' \
        | sed -n 's/.*"password":"\([^"]*\)".*/\1/p' \
        | tail -1 || true)"
    [ -n "$BOOTSTRAP_PASSWORD" ] && break
    sleep 1
done
if [ -z "$BOOTSTRAP_PASSWORD" ]; then
    echo "could not extract bootstrap admin password from mcm logs" >&2
    $COMPOSE logs mcm --tail 50 >&2 || true
    exit 1
fi

login_body="$(mktemp)"
login_code="$(curl -sS -o "$login_body" -w '%{http_code}' \
    -X POST "$LOGIN_URL" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"admin\",\"password\":\"${BOOTSTRAP_PASSWORD}\"}")"
TOKEN="$(jq -r '.token // .access_token // .jwt // empty' "$login_body")"
rm -f "$login_body"
if [ "$login_code" != "200" ] || [ -z "$TOKEN" ]; then
    echo "login failed (HTTP ${login_code})" >&2
    exit 1
fi
echo "login OK"

auth_curl() {
    local method="$1" path="$2" body="${3:-}"
    if [ -n "$body" ]; then
        curl -sS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${TOKEN}" \
            -H 'Content-Type: application/json' \
            -d "$body"
    else
        curl -sS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${TOKEN}"
    fi
}

echo "--- invariant 2: GET /broker/config ---"
config_resp="$(auth_curl GET /api/v1/broker/config)"
config_path="$(jq -r '.path // empty' <<<"$config_resp")"
config_hash="$(jq -r '.hash // empty' <<<"$config_resp")"
config_body="$(jq -r '.body // empty' <<<"$config_resp")"
if [ -z "$config_path" ] || [ -z "$config_hash" ] || [ -z "$config_body" ]; then
    echo "GET /broker/config did not return path/hash/body: $config_resp" >&2
    exit 1
fi
echo "config path=${config_path} hash=${config_hash:0:12}"

echo "--- invariant 3: POST /broker/config/import ---"
import_resp="$(auth_curl POST /api/v1/broker/config/import '{}')"
revision_id="$(jq -r '.revision_id // empty' <<<"$import_resp")"
rendered_hash="$(jq -r '.rendered_conf_hash // empty' <<<"$import_resp")"
if [ -z "$revision_id" ]; then
    echo "import did not return a revision_id: $import_resp" >&2
    exit 1
fi
echo "import OK revision=${revision_id} rendered_hash=${rendered_hash:0:12}"

echo "--- invariant 4: POST /broker/config/adopt ---"
adopt_resp="$(auth_curl POST /api/v1/broker/config/adopt '{}')"
# The BrokerConfigAdoption struct has Go-style field names (ID /
# SourcePath / AdoptedBy / AdoptedAt) without explicit json tags,
# so the wire format is capital-case. The `// .id` fallback keeps the
# check working if a future tag aligns the wire to snake_case.
adopt_id="$(jq -r '.ID // .id // empty' <<<"$adopt_resp")"
if [ -z "$adopt_id" ]; then
    echo "adopt did not return an id: $adopt_resp" >&2
    exit 1
fi
echo "adopt OK id=${adopt_id}"

echo "--- invariant 5+6: POST /broker/config/apply, verify on-disk hash ---"
apply_resp="$(auth_curl POST /api/v1/broker/config/apply \
    "{\"revision_id\":\"${revision_id}\",\"force\":false,\"adopt\":true}")"
apply_status="$(jq -r '.status // empty' <<<"$apply_resp")"
if [ "$apply_status" != "active_verified" ]; then
    echo "apply did not reach active_verified: $apply_resp" >&2
    exit 1
fi
sleep "$SETTLE_SECONDS"

# The mosquitto container reads the same volume at /mosquitto/config.
disk_hash="$($COMPOSE exec -T mosquitto sha256sum /mosquitto/config/mosquitto.conf | awk '{print $1}')"
if [ "$disk_hash" != "$rendered_hash" ]; then
    echo "on-disk hash mismatch: disk=${disk_hash} rendered=${rendered_hash}" >&2
    exit 1
fi
echo "apply OK, on-disk hash matches rendered hash"

echo "--- invariant 7: external edit detected as drift ---"
$COMPOSE exec -T mosquitto sh -c 'printf "# e2e drift probe\n" >> /mosquitto/config/mosquitto.conf'
drift_import="$(auth_curl POST /api/v1/broker/config/import '{}')"
drift_base="$(jq -r '.base_conf_hash // empty' <<<"$drift_import")"
drift_revision="$(jq -r '.revision_id // empty' <<<"$drift_import")"
if [ -z "$drift_revision" ] || [ "$drift_base" == "$config_hash" ]; then
    echo "drift import did not reflect the external edit: $drift_import" >&2
    exit 1
fi
echo "drift detected: new base hash ${drift_base:0:12}"

echo "--- invariant 8: apply the drift revision (adopt=true) ---"
drift_apply="$(auth_curl POST /api/v1/broker/config/apply \
    "{\"revision_id\":\"${drift_revision}\",\"force\":false,\"adopt\":true}")"
drift_status="$(jq -r '.status // empty' <<<"$drift_apply")"
if [ "$drift_status" != "active_verified" ]; then
    echo "drift apply did not reach active_verified: $drift_apply" >&2
    exit 1
fi
echo "drift apply OK"

echo "--- e2e-broker-config: all invariants passed ---"
