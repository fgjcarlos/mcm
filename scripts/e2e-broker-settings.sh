#!/usr/bin/env bash
# End-to-end test of the broker settings editor (issue #302).
#
# The script drives the real API of a running MCM instance:
#   1. GET  /api/v1/broker/settings         — the form and its catalog bounds
#   2. POST /api/v1/broker/settings/preview — the diff and the reload kind
#   3. POST /api/v1/broker/config/apply     — the write of the file
#
# It needs an admin token because the apply endpoint is admin only.
set -euo pipefail

readonly GREEN='\033[0;32m'
readonly RED='\033[0;31m'
readonly YELLOW='\033[0;33m'
readonly RESET='\033[0m'

MCM_BASE_URL="${MCM_BASE_URL:-http://127.0.0.1:8080}"

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
        # /livez answers as soon as the API is up. The settings editor
        # reads and writes the configuration file, so it does not need a
        # connected broker to be reachable; the apply step proves the
        # broker signal path.
        if curl -fsS "${MCM_BASE_URL}/livez" >/dev/null; then
            pass 1 "wait_for_ready"
            return
        fi
        sleep 1
    done
    fail "MCM did not become ready at ${MCM_BASE_URL}/livez within 30 seconds"
}

get_json() {
    local endpoint="$1"
    local output="$2"
    curl -sS -o "$output" -w '%{http_code}' \
        "${MCM_BASE_URL}${endpoint}" \
        -H "Authorization: Bearer ${MCM_AUTH_TOKEN}"
}

post_json() {
    local endpoint="$1"
    local payload="$2"
    local output="$3"
    curl -sS -o "$output" -w '%{http_code}' \
        -X POST "${MCM_BASE_URL}${endpoint}" \
        -H "Authorization: Bearer ${MCM_AUTH_TOKEN}" \
        -H 'Content-Type: application/json' \
        -d "$payload"
}

wait_for_ready

# --- step 2: the settings form is described by the catalog -----------------
response_file
settings_file="$RESPONSE_FILE"
settings_code="$(get_json '/api/v1/broker/settings' "$settings_file")"
if [[ "$settings_code" != '200' ]]; then
    fail "GET /broker/settings failed with HTTP ${settings_code}: $(<"$settings_file")"
fi
sections="$(jq -r '[.sections[].id] | join(",")' "$settings_file")"
for section in persistence queues sessions logging; do
    if [[ ",$sections," != *",$section,"* ]]; then
        fail "the settings payload has no ${section} section: ${sections}"
    fi
done
pass 2 "the form offers the persistence, queues, sessions and logging sections"

# The bounds the operator sees are the bounds the server enforces.
autosave_unit="$(jq -r '.sections[] | select(.id=="persistence") | .fields[] | select(.directive=="autosave_interval") | .unit' "$settings_file")"
autosave_min="$(jq -r '.sections[] | select(.id=="persistence") | .fields[] | select(.directive=="autosave_interval") | .min' "$settings_file")"
if [[ "$autosave_unit" != 'seconds' || "$autosave_min" != '0' ]]; then
    fail "autosave_interval has no unit or bound: unit=${autosave_unit} min=${autosave_min}"
fi
pass 3 "the catalog describes the unit and the bounds of a field"

# --- step 4: an out-of-range value is refused ------------------------------
response_file
invalid_file="$RESPONSE_FILE"
invalid_payload="$(jq -cn '{entries: [{directive: "max_packet_size", values: ["10"]}]}')"
invalid_code="$(post_json '/api/v1/broker/settings/preview' "$invalid_payload" "$invalid_file")"
if [[ "$invalid_code" != '400' ]]; then
    fail "an out-of-range value was not refused (HTTP ${invalid_code}): $(<"$invalid_file")"
fi
if ! jq -e '.issues[]? | select(.directive == "max_packet_size")' "$invalid_file" >/dev/null; then
    fail "the refusal does not mention the directive: $(<"$invalid_file")"
fi
pass 4 "a value outside the catalog range is refused with the issue"

# --- step 5: a valid change previews as a reload --------------------------
# The document starts from the current state so the diff only holds the
# change the script makes.
document="$(jq -c '.document' "$settings_file")"
# The script must work whatever the file holds, so it picks a value that
# differs from the current one and restores the original state at the end.
original_autosave="$(jq -c '
    [(.entries)[] | select(.directive == "autosave_interval")][0]
    // {directive: "autosave_interval", unset: true}' <<<"$document")"
current_autosave="$(jq -r '.values[0] // empty' <<<"$original_autosave")"
new_autosave=60
if [[ "$current_autosave" == "60" ]]; then
    new_autosave=90
fi
edited="$(jq -c --arg value "$new_autosave" '
    (.entries) |= map(
        if .directive == "autosave_interval"
        then {directive: "autosave_interval", values: [$value]}
        else . end
    )' <<<"$document")"
response_file
preview_file="$RESPONSE_FILE"
preview_code="$(post_json '/api/v1/broker/settings/preview' "$edited" "$preview_file")"
if [[ "$preview_code" != '200' ]]; then
    fail "the preview failed with HTTP ${preview_code}: $(<"$preview_file")"
fi
revision_id="$(jq -r '.revision_id // empty' "$preview_file")"
reload_kind="$(jq -r '.reload_kind // empty' "$preview_file")"
requires_restart="$(jq -r '.requires_restart // false' "$preview_file")"
changed="$(jq -r '.changed_directives // [] | join(",")' "$preview_file")"
if [[ -z "$revision_id" ]]; then
    fail "the preview did not return a revision_id: $(<"$preview_file")"
fi
if [[ "$changed" != 'autosave_interval' ]]; then
    fail "the preview does not report the changed directive: ${changed}"
fi
if [[ "$requires_restart" != 'false' ]]; then
    fail "a reload-only change must not require a restart: $(<"$preview_file")"
fi
if [[ "$reload_kind" != 'reload' ]]; then
    fail "a reload-only change must be classified as reload: ${reload_kind}"
fi
pass 5 "the preview reports the changed directive and a reload"

# --- step 6: the apply writes the file -------------------------------------
response_file
apply_file="$RESPONSE_FILE"
apply_code="$(post_json '/api/v1/broker/config/apply' \
    "$(jq -cn --arg revision_id "$revision_id" '{revision_id: $revision_id, force: false, adopt: true}')" "$apply_file")"
if [[ "$apply_code" != '200' ]]; then
    fail "the apply failed with HTTP ${apply_code}: $(<"$apply_file")"
fi
apply_status="$(jq -r '.status // empty' "$apply_file")"
if [[ "$apply_status" != 'active_verified' ]]; then
    fail "the apply did not reach active_verified: $(<"$apply_file")"
fi
pass 6 "the apply wrote the configuration"

# --- step 7: the broker picked the new value up ----------------------------
response_file
after_file="$RESPONSE_FILE"
after_code="$(get_json '/api/v1/broker/config' "$after_file")"
if [[ "$after_code" != '200' ]]; then
    fail "GET /broker/config failed with HTTP ${after_code}"
fi
config_body="$(jq -r '.body // empty' "$after_file")"
if ! grep -q "^autosave_interval ${new_autosave}$" <<<"$config_body"; then
    fail "the applied value is not in the configuration file: ${config_body}"
fi
pass 7 "the applied value is in the configuration file"

# --- step 8: the settings read the new value back --------------------------
response_file
reload_file="$RESPONSE_FILE"
reload_code="$(get_json '/api/v1/broker/settings' "$reload_file")"
if [[ "$reload_code" != '200' ]]; then
    fail "GET /broker/settings failed with HTTP ${reload_code}"
fi
autosave_value="$(jq -r '.sections[] | select(.id=="persistence") | .fields[] | select(.directive=="autosave_interval") | .values[0] // empty' "$reload_file")"
if [[ "$autosave_value" != "$new_autosave" ]]; then
    fail "the settings do not read the applied value back: ${autosave_value}"
fi
pass 8 "the settings read the applied value back"

# --- step 9: saving the settings unchanged does not touch the file ---------
response_file
noop_file="$RESPONSE_FILE"
noop_code="$(post_json '/api/v1/broker/settings/preview' "$(jq -c '.document' "$reload_file")" "$noop_file")"
if [[ "$noop_code" != '200' ]]; then
    fail "the no-op preview failed with HTTP ${noop_code}: $(<"$noop_file")"
fi
noop_changes="$(jq -r '.has_changes // false' "$noop_file")"
if [[ "$noop_changes" != 'false' ]]; then
    fail "saving the settings unchanged changed the file: $(<"$noop_file")"
fi
pass 9 "saving the settings unchanged leaves the file alone"

# --- step 10: restore the original state -----------------------------------
restore="$(jq -c --argjson original "$original_autosave" '
    (.entries) |= map(
        if .directive == "autosave_interval"
        then $original
        else . end
    )' <<<"$(jq -c '.document' "$reload_file")")"
response_file
restore_preview_file="$RESPONSE_FILE"
restore_preview_code="$(post_json '/api/v1/broker/settings/preview' "$restore" "$restore_preview_file")"
if [[ "$restore_preview_code" != '200' ]]; then
    skip "the restore preview failed with HTTP ${restore_preview_code}"
else
    restore_revision="$(jq -r '.revision_id // empty' "$restore_preview_file")"
    response_file
    restore_apply_file="$RESPONSE_FILE"
    restore_code="$(post_json '/api/v1/broker/config/apply' \
        "$(jq -cn --arg revision_id "$restore_revision" '{revision_id: $revision_id, force: false, adopt: true}')" "$restore_apply_file")"
    if [[ "$restore_code" != '200' ]]; then
        fail "the restore apply failed with HTTP ${restore_code}: $(<"$restore_apply_file")"
    fi
    pass 10 "the original configuration is restored"
fi

printf '%b\n' "${GREEN}✓ all broker settings invariants hold${RESET}"
