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
#   5. POST /broker/config/apply returns 200 with a truthful activation
#      state (active_verified only when MQTT access could be probed).
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

# The MCM container reaches the Docker daemon through the host's socket
# group. The image bakes the conventional GID at build time, but the
# socket of this host (and of a CI runner) can live under another GID.
# shellcheck source=lib-docker-host-gid.sh
source "${REPO_ROOT}/scripts/lib-docker-host-gid.sh"
export_docker_host_gid

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
    local rc=$?
    echo "--- e2e-broker-config: cleaning up ---"
    # On failure, capture bounded mcm container logs before teardown so the
    # underlying apply error survives in job output (down -v would remove
    # it). The dump is best-effort and never masks the original failure.
    if [ "$rc" -ne 0 ]; then
        $COMPOSE logs mcm --tail 100 >&2 || true
    fi
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
# CI loads mcm:dev built with the host Docker socket GID; rebuilding here
# would use Compose's default GID and break socket access during apply.
$COMPOSE up -d --no-build

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

# --- Recent MFA -----------------------------------------------------------
# Sensitive operations require a recent, session-bound MFA proof. The
# helper enrolls TOTP for the authenticated account (fresh stacks only),
# confirms it, and exchanges an unused time step for the proof.
read -r MFA_TOKEN MFA_PROOF <<<"$("${REPO_ROOT}/scripts/mfa-step-up.sh" "$HOST_URL" "$TOKEN")"
if [ -z "$MFA_PROOF" ]; then
    echo "could not obtain a recent MFA proof" >&2
    exit 1
fi
echo "recent MFA proof acquired"


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

# apply_curl performs an apply with the recent-MFA proof that sensitive
# operations require.
apply_curl() {
    local path="$1" body="$2"
    curl -sS -X POST "${HOST_URL}${path}" \
        -H "Authorization: Bearer ${MFA_TOKEN}" \
        -H "X-MCM-MFA: ${MFA_PROOF}" \
        -H 'Content-Type: application/json' \
        -d "$body"
}

# mfa_curl performs any method with the recent-MFA proof that the sensitive
# operations require. The proof is bound to the session that requested it,
# so the request runs as the operator the helper created.
mfa_curl() {
    local method="$1" path="$2" body="${3:-}"
    if [ -n "$body" ]; then
        curl -sS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${MFA_TOKEN}" \
            -H "X-MCM-MFA: ${MFA_PROOF}" \
            -H 'Content-Type: application/json' \
            -d "$body"
    else
        curl -sS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${MFA_TOKEN}" \
            -H "X-MCM-MFA: ${MFA_PROOF}"
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
# The CI compose uses MCM_MOSQUITTO_DEPLOY_RELOAD_STRATEGY=sighup: SIGHUP
# only reloads non-listener directives, so a normalised diff that includes
# persistence_location / user / listener triggers ErrRequiresRestart.
# `force=true` lets the apply proceed; on-disk hash is the real contract
# (the file IS rewritten regardless of whether Mosquitto picks up
# restart-required directives from SIGHUP alone). The apply carries the
# recent-MFA proof the sensitive operations require.
apply_resp="$(apply_curl /api/v1/broker/config/apply \
    "{\"revision_id\":\"${revision_id}\",\"force\":true,\"adopt\":true}")"
apply_status="$(jq -r '.status // empty' <<<"$apply_resp")"
apply_message="$(jq -r '.message // empty' <<<"$apply_resp")"
# The apply must report a truthful activation state. This stack manages no
# MQTT user with a known password, so MQTT access cannot be probed here and
# the record must say so instead of claiming a verified activation.
case "$apply_status" in
    active_verified) ;;
    pending_activation)
        if ! printf '%s' "$apply_message" | grep -qi 'verification unavailable'; then
            echo "unverified activation did not explain the missing verification: $apply_resp" >&2
            exit 1
        fi
        ;;
    *)
        echo "apply did not report a truthful activation state: $apply_resp" >&2
        exit 1
        ;;
esac
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
# Same restart-required rationale as invariant 5; see the comment there.
drift_apply="$(apply_curl /api/v1/broker/config/apply \
    "{\"revision_id\":\"${drift_revision}\",\"force\":true,\"adopt\":true}")"
drift_status="$(jq -r '.status // empty' <<<"$drift_apply")"
case "$drift_status" in
    active_verified|pending_activation) ;;
    *)
        echo "drift apply did not report a truthful activation state: $drift_apply" >&2
        exit 1
        ;;
esac
echo "drift apply OK"

echo "--- invariant 9: revision history ---"
revisions="$(auth_curl GET /api/v1/broker/config/revisions)"
revision_count="$(jq -r '.revisions | length // 0' <<<"$revisions")"
if [ "$revision_count" -lt 1 ]; then
    echo "revision history is empty: $revisions" >&2
    exit 1
fi
echo "revisions OK count=$revision_count"

echo "--- invariant 10: recovery and copies require a recent MFA proof ---"
# The proof is what distinguishes an authorized operator from anyone else
# holding the session, so the refusal must be explicit.
for target in "GET /api/v1/broker/config/copy" "POST /api/v1/broker/config/revisions/${drift_revision}/recover"; do
    method="${target%% *}"
    path="${target#* }"
    body_file="$(mktemp)"
    status="$(curl -sS -o "$body_file" -w '%{http_code}' \
        -X "$method" "${HOST_URL}${path}" \
        -H "Authorization: Bearer ${TOKEN}" \
        -H 'Content-Type: application/json' \
        -d '{}' || echo "000")"
    if [ "$status" != "403" ]; then
        echo "${method} ${path} without a proof returned ${status}: $(cat "$body_file")" >&2
        exit 1
    fi
    if ! grep -q 'mfa_required' "$body_file"; then
        echo "${method} ${path} without a proof did not report mfa_required: $(cat "$body_file")" >&2
        exit 1
    fi
    rm -f "$body_file"
done
echo "recovery and copies are refused without a proof"

echo "--- invariant 11: export and re-import a configuration copy ---"
copy_body="$(mktemp)"
copy_status="$(curl -sS -o "$copy_body" -w '%{http_code}' \
    -X GET "${HOST_URL}/api/v1/broker/config/copy" \
    -H "Authorization: Bearer ${MFA_TOKEN}" \
    -H "X-MCM-MFA: ${MFA_PROOF}" || echo "000")"
if [ "$copy_status" != "200" ]; then
    echo "copy export returned ${copy_status}: $(cat "$copy_body")" >&2
    exit 1
fi
copy_format="$(jq -r '.format // empty' <"$copy_body")"
copy_hash="$(jq -r '.hash // empty' <"$copy_body")"
copy_text="$(jq -r '.body // empty' <"$copy_body")"
if [ "$copy_format" != "mcm-broker-config/v1" ] || [ -z "$copy_hash" ] || [ -z "$copy_text" ]; then
    echo "copy export is malformed: $(cat "$copy_body")" >&2
    exit 1
fi
# The copy carries the root configuration file and nothing else. A
# complete backup is tracked separately (#295), so the copy must not
# embed the contents of the files the root file references: the
# password file, the ACL file and the include files. The root file
# itself names them, so the assertion is that the copy equals the file
# on disk and that none of their contents travelled with it.
root_text="$($COMPOSE exec -T mosquitto cat /mosquitto/config/mosquitto.conf)"
if [ "$copy_text" != "$root_text" ]; then
    echo "the copy is not the root configuration file" >&2
    exit 1
fi
for leak in 'mcm/healthcheck' '$7$' '$2b$'; do
    if printf '%s' "$copy_text" | grep -qF "$leak"; then
        echo "the configuration copy embedded a dependency: ${leak}" >&2
        exit 1
    fi
done
# A copy whose hash does not match its body is refused.
invalid_copy="$(jq -c '.hash = "0000000000000000000000000000000000000000000000000000000000000000"' <"$copy_body")"
invalid_status="$(curl -sS -o /dev/null -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/broker/config/copy" \
    -H "Authorization: Bearer ${MFA_TOKEN}" \
    -H "X-MCM-MFA: ${MFA_PROOF}" \
    -H 'Content-Type: application/json' \
    -d "$invalid_copy" || echo "000")"
if [ "$invalid_status" != "422" ]; then
    echo "a corrupted copy returned ${invalid_status}, want 422" >&2
    exit 1
fi
reimport="$(mfa_curl POST /api/v1/broker/config/copy "$(cat "$copy_body")")"
reimport_revision="$(jq -r '.revision_id // empty' <<<"$reimport")"
if [ -z "$reimport_revision" ]; then
    echo "re-importing the copy did not return a revision: $reimport" >&2
    exit 1
fi
if [ "$reimport_revision" = "$drift_revision" ]; then
    echo "re-importing the copy reused the revision ${drift_revision}" >&2
    exit 1
fi
rm -f "$copy_body"
echo "copy export and re-import OK"

echo "--- invariant 12: recover a revision into a new preview ---"
current_hash="$(jq -r '.hash // empty' <<<"$(auth_curl GET /api/v1/broker/config)")"
if [ -z "$current_hash" ]; then
    echo "could not read the current on-disk hash" >&2
    exit 1
fi
# The hash of the file as it is right now: the drift probe and the drift
# apply changed it, so the comparison below must start from this state
# and not from the hash of the first apply.
disk_hash_before_recovery="$($COMPOSE exec -T mosquitto sha256sum /mosquitto/config/mosquitto.conf | awk '{print $1}')"
if [ "$disk_hash_before_recovery" != "$current_hash" ]; then
    echo "the on-disk hash and the reported hash disagree: ${disk_hash_before_recovery} != ${current_hash}" >&2
    exit 1
fi
recovered="$(mfa_curl POST "/api/v1/broker/config/revisions/${drift_revision}/recover" "{\"base_hash\":\"${current_hash}\"}")"
recovered_revision="$(jq -r '.revision_id // empty' <<<"$recovered")"
if [ -z "$recovered_revision" ]; then
    echo "recovery did not return a revision: $recovered" >&2
    exit 1
fi
if [ "$recovered_revision" = "$drift_revision" ]; then
    echo "recovery reused the consumed revision ${drift_revision}" >&2
    exit 1
fi
# Recovery prepares a candidate; it does not touch the broker by itself.
disk_hash_after="$($COMPOSE exec -T mosquitto sha256sum /mosquitto/config/mosquitto.conf | awk '{print $1}')"
if [ "$disk_hash_after" != "$disk_hash_before_recovery" ]; then
    echo "recovery changed the broker file without an apply: ${disk_hash_before_recovery} -> ${disk_hash_after}" >&2
    exit 1
fi
echo "recovery OK revision=${recovered_revision:0:8} (broker file untouched)"

echo "--- e2e-broker-config: all invariants passed ---"
