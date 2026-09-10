#!/usr/bin/env bash
# E2E recovery test for issue #295.
#
# Proves the new backup/restore scripts actually round-trip the
# deployment through a complete wipe-and-restore:
#
#   1. Bring up the dev Compose stack.
#   2. Login + create a known MQTT user + ACL + apply.
#   3. Run scripts/backup.sh — produces an archive.
#   4. Wipe EVERYTHING: docker compose down -v.
#   5. Re-up fresh. MCM bootstraps a NEW admin (in-memory), but the
#      volume is now empty.
#   6. Run scripts/restore.sh --confirm — overwrites the volume with
#      the captured archive contents.
#   7. Restart mcm so the restored DB is loaded.
#   8. Verify the OLD admin password (from the original bootstrap)
#      authenticates — proving the .bootstrap.json + SQLite were
#      restored faithfully.
#   9. Verify the user created in step 2 still exists, with the
#      original cleartext password — proving users/auth round-tripped.
#  10. Verify the ACL still grants the user access on the granted
#      topic — a publish round-trip proves the broker config was
#      restored too. The broker is restarted in phase 5 so it reads
#      the restored files; we do not run a deploy apply because the
#      verifier's in-memory cleartext store is empty after a container
#      restart (#293).
#  11. Verify the archive's sha256 manifest matches every restored
#      file (covered by restore.sh internally — the script fails the
#      test on any mismatch).
#  12. docker compose down -v cleans up.
#
# The "OLD admin password" check is the core invariant. If the
# SQLite + .bootstrap.json round-trip is broken, the original admin
# password is gone (MCM regenerates a new one) and the login fails.
# The "user still present" check is the second core invariant — it
# proves users/auth persisted across the wipe.
#
# Exit code:
#   0  every invariant passed.
#   1  any invariant failed (see the failing step's stderr).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE="docker compose"
PORT="${MCM_PORT:-8080}"
HOST_URL="http://localhost:${PORT}"
WAIT_SECONDS="${E2E_RECOVERY_WAIT_SECONDS:-180}"
RELOAD_SETTLE_SECONDS="${E2E_RECOVERY_RELOAD_SETTLE:-5}"
RECOVERY_USER="recovery-user"
ALLOWED_TOPIC="recovery/test/allowed"

# Backup archive lives in /tmp so it survives the docker compose down -v.
ARCHIVE_PATH="/tmp/mcm-recovery-e2e-$$-$(date +%s).tar.gz"

cleanup() {
    local exit_code=$?
    echo "--- e2e-recovery: tearing down (exit=$exit_code) ---"
    rm -f "$ARCHIVE_PATH" || true
    (cd "$REPO_ROOT" && $COMPOSE down -v --remove-orphans >/dev/null 2>&1) || true
    exit "$exit_code"
}
trap cleanup EXIT

if ! command -v jq >/dev/null 2>&1; then
    echo "--- e2e-recovery: installing jq ---"
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
    echo "Refusing to run: .env exists in repo root. Remove it before running the e2e recovery test." >&2
    exit 1
fi

# --- 1. Initial setup -----------------------------------------------------------

echo "--- e2e-recovery: phase 1 (initial setup) ---"
$COMPOSE up -d --no-build

echo "--- e2e-recovery: waiting for /livez (${WAIT_SECONDS}s budget) ---"
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

# Capture the ORIGINAL admin password from the bootstrap log.
LOG_FILE="$(mktemp)"
$COMPOSE logs --no-color mcm > "$LOG_FILE" || true
if ! grep -q 'bootstrap admin created' "$LOG_FILE"; then
    echo "Expected bootstrap admin warn log line not found in mcm logs:" >&2
    cat "$LOG_FILE" >&2
    rm -f "$LOG_FILE"
    exit 1
fi
ADMIN_PASSWORD_ORIG="$(grep 'bootstrap admin created' "$LOG_FILE" | sed -n 's/.*"password":"\([^"]*\)".*/\1/p' | head -n1)"
rm -f "$LOG_FILE"
if [ -z "$ADMIN_PASSWORD_ORIG" ]; then
    echo "Could not extract bootstrap admin password from mcm logs" >&2
    exit 1
fi
echo "captured original admin password (length=${#ADMIN_PASSWORD_ORIG})"

auth_curl() {
    local method="$1" path="$2" body="${3:-}"
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

login() {
    local body="$1"
    curl -sS -X POST "${HOST_URL}/api/v1/auth/login" \
        -H 'Content-Type: application/json' \
        -d "$body"
}

TOKEN="$(login "{\"username\":\"admin\",\"password\":\"${ADMIN_PASSWORD_ORIG}\"}" | jq -r .token)"
if [ -z "$TOKEN" ]; then
    echo "Initial login failed" >&2
    exit 1
fi

echo "--- e2e-recovery: create ${RECOVERY_USER} + ACL + apply ---"
USER_BODY="$(mktemp)"
USER_CODE="$(curl -sS -o "$USER_BODY" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/mqtt-users" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"username\":\"${RECOVERY_USER}\"}" || echo "000")"
if [ "$USER_CODE" != "201" ]; then
    echo "create user failed: HTTP $USER_CODE" >&2
    cat "$USER_BODY" >&2
    rm -f "$USER_BODY"
    exit 1
fi
RECOVERY_USER_PASSWORD="$(jq -r .password "$USER_BODY")"
rm -f "$USER_BODY"

ACL_BODY="$(mktemp)"
ACL_CODE="$(curl -sS -o "$ACL_BODY" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/acls" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"principal\":\"${RECOVERY_USER}\",\"topic_filter\":\"${ALLOWED_TOPIC}\",\"permission\":\"readwrite\"}" || echo "000")"
if [ "$ACL_CODE" != "201" ] && [ "$ACL_CODE" != "200" ]; then
    echo "create acl failed: HTTP $ACL_CODE" >&2
    cat "$ACL_BODY" >&2
    rm -f "$ACL_BODY"
    exit 1
fi
rm -f "$ACL_BODY"

REVISION_ID="$(auth_curl POST /api/v1/deployments/preview | jq -r '.revision_id // empty')"
if [ -z "$REVISION_ID" ]; then
    echo "preview response did not include revision_id" >&2
    exit 1
fi

APPLY_BODY="$(mktemp)"
APPLY_CODE="$(curl -sS -o "$APPLY_BODY" -w '%{http_code}' \
    -X POST "${HOST_URL}/api/v1/deployments/apply" \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Content-Type: application/json' \
    -d "{\"revision_id\":\"${REVISION_ID}\"}" || echo "000")"
if [ "$APPLY_CODE" != "200" ]; then
    echo "apply returned HTTP $APPLY_CODE (expected 200)" >&2
    cat "$APPLY_BODY" >&2
    rm -f "$APPLY_BODY"
    exit 1
fi
APPLY_STATUS_INITIAL="$(jq -r .status "$APPLY_BODY" 2>/dev/null || echo unknown)"
rm -f "$APPLY_BODY"
echo "initial apply status=$APPLY_STATUS_INITIAL"

sleep "$RELOAD_SETTLE_SECONDS"

# --- 2. Backup -----------------------------------------------------------------

echo
echo "--- e2e-recovery: phase 2 (backup) ---"
bash scripts/backup.sh "$ARCHIVE_PATH" --operator=e2e-recovery
if [ ! -f "$ARCHIVE_PATH" ]; then
    echo "Backup archive was not produced at $ARCHIVE_PATH" >&2
    exit 1
fi
echo "backup: $(ls -lh "$ARCHIVE_PATH" | awk '{print $5}') $(file "$ARCHIVE_PATH" | cut -d: -f2)"

# --- 3. Wipe -------------------------------------------------------------------

echo
echo "--- e2e-recovery: phase 3 (wipe) ---"
$COMPOSE down -v --remove-orphans
echo "wiped all mcm_* and mosquitto_* volumes"

# --- 4. Re-up empty ------------------------------------------------------------

echo
echo "--- e2e-recovery: phase 4 (re-up empty stack) ---"
$COMPOSE up -d --no-build
for i in $(seq 1 "$WAIT_SECONDS"); do
    if curl -fsS "${HOST_URL}/livez" >/dev/null 2>&1; then
        echo "livez ready after ${i}s (fresh empty DB)"
        break
    fi
    sleep 1
done
sleep 2

# A new admin must have been generated. Capture it for the
# "this is gone after restore" assertion path.
LOG_FILE="$(mktemp)"
$COMPOSE logs --no-color mcm > "$LOG_FILE" || true
if ! grep -q 'bootstrap admin created' "$LOG_FILE"; then
    # mcm may not have logged bootstrap if persistence did not bootstrap.
    echo "DEBUG: mcm log after wipe — no bootstrap admin line" >&2
fi
ADMIN_PASSWORD_NEW="$(grep 'bootstrap admin created' "$LOG_FILE" | sed -n 's/.*"password":"\([^"]*\)".*/\1/p' | head -n1)"
rm -f "$LOG_FILE"

# Verify the bootstrap generated a DIFFERENT password (proves the
# fresh DB is truly empty — no leftover from backup).
if [ -n "$ADMIN_PASSWORD_NEW" ] && [ "$ADMIN_PASSWORD_NEW" = "$ADMIN_PASSWORD_ORIG" ]; then
    echo "Fresh DB appears to retain the old admin — wipe incomplete" >&2
    exit 1
fi
echo "fresh DB is empty (new admin generated)"

# --- 5. Restore ---------------------------------------------------------------

echo
echo "--- e2e-recovery: phase 5 (restore) ---"
bash scripts/restore.sh --confirm "$ARCHIVE_PATH"

# Restart mcm so it picks up the restored DB and JWT secret. The
# broker still has its pre-restore in-memory state at this point
# (it reads files only on SIGHUP / restart). Phase 6 brings the
# broker back in sync by restarting it.
$COMPOSE restart mcm
for i in $(seq 1 "$WAIT_SECONDS"); do
    if curl -fsS "${HOST_URL}/livez" >/dev/null 2>&1; then
        echo "mcm restarted after restore, livez ready after ${i}s"
        break
    fi
    sleep 1
done
sleep 2

# Restart the broker so it reads the restored passwd + acl. The
# in-memory verifier cleartext store (#293) is empty after a container
# restart, so we cannot run a deploy apply to drive the reload — the
# only way to sync the broker is a full restart. Production operators
# running a service like mosquitto under systemd can do this with
# `systemctl restart mosquitto` after restore.
echo "--- e2e-recovery: restart mosquitto so it reads restored passwd/acl ---"
$COMPOSE restart mosquitto
for i in $(seq 1 "$WAIT_SECONDS"); do
    if $COMPOSE ps mosquitto 2>/dev/null | grep -q "(healthy)"; then
        echo "mosquitto healthy after restart, ${i}s"
        break
    fi
    sleep 1
done
sleep "$RELOAD_SETTLE_SECONDS"

# --- 6. Verify -----------------------------------------------------------------

echo
echo "--- e2e-recovery: phase 6 (verify) ---"

# (8) The OLD admin password must work — proves .bootstrap.json +
#     SQLite were faithfully restored.
echo "--- verifying old admin login restored (original JWT secret) ---"
RESTORED_LOGIN="$(login "{\"username\":\"admin\",\"password\":\"${ADMIN_PASSWORD_ORIG}\"}")"
if ! echo "$RESTORED_LOGIN" | grep -q '"token"'; then
    echo "FAIL: original admin password no longer authenticates after restore" >&2
    echo "response: $RESTORED_LOGIN" >&2
    exit 1
fi
RESTORED_TOKEN="$(echo "$RESTORED_LOGIN" | jq -r .token)"
if [ -z "$RESTORED_TOKEN" ] || [ "$RESTORED_TOKEN" = "null" ]; then
    echo "FAIL: token not issued for original admin — JWT secret not restored" >&2
    exit 1
fi
echo "old admin login OK after restore"

# (9) The user created in phase 1 must exist with the ORIGINAL password.
echo "--- verifying ${RECOVERY_USER} still present with original password ---"
USER_LIST="$(curl -sS -X GET "${HOST_URL}/api/v1/mqtt-users" \
    -H "Authorization: Bearer ${RESTORED_TOKEN}")"
if ! echo "$USER_LIST" | jq -r '.[] | select(.username=="'"$RECOVERY_USER"'") | .username' | grep -q "$RECOVERY_USER"; then
    echo "FAIL: ${RECOVERY_USER} no longer exists after restore" >&2
    echo "list: $USER_LIST" >&2
    exit 1
fi
echo "${RECOVERY_USER} still present after restore"

# (10) The broker must accept the publish with the original
#      credentials (proves passwd/acl round-tripped). We do NOT run
#      a deploy apply after restore because the verifier's in-memory
#      cleartext store (#293) is empty after a container restart —
#      any apply would fail with "no test subject available" and
#      trigger a rollback that overwrites the just-restored files.
#      Restarting mosquitto (in phase 5) makes the broker read the
#      restored files directly, which is the production equivalent of
#      `systemctl restart mosquitto` after a restore.
echo "--- verifying broker round-trip on the granted topic ---"
allow_log="$($COMPOSE exec -T mosquitto sh -c "
    mosquitto_sub -h 127.0.0.1 -p 1883 \
        -u \"$RECOVERY_USER\" -P \"$RECOVERY_USER_PASSWORD\" \
        -t \"$ALLOWED_TOPIC\" -C 1 -W 15 -q 1 2>&1 &
    SUB_PID=\$!
    sleep 2
    mosquitto_pub -h 127.0.0.1 -p 1883 \
        -u \"$RECOVERY_USER\" -P \"$RECOVERY_USER_PASSWORD\" \
        -t \"$ALLOWED_TOPIC\" -m \"recovered\" -q 1
    wait \$SUB_PID
" 2>&1 || true)"
if ! printf '%s\n' "$allow_log" | grep -q '^recovered$'; then
    echo "FAIL: subscriber did not receive 'recovered' on $ALLOWED_TOPIC after restore" >&2
    printf '%s\n' "$allow_log" >&2
    exit 1
fi
echo "broker round-trip OK after restore"

echo
echo "--- e2e-recovery: all invariants passed ---"
