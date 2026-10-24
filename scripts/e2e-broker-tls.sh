#!/usr/bin/env bash
# End-to-end broker TLS certificate management test for issue #300.
#
# The script drives the real certificate API of MCM against the dev Compose
# stack and proves the acceptance criteria of the issue:
#
#   1. The upload validates the material: a certificate and a key that do not
#      belong together are rejected with the reported issues.
#   2. A listener serves plain TLS and another one serves mutual TLS, both
#      bound to a certificate profile.
#   3. A rotation activates a new revision under a new authority, the
#      previous revision is superseded, an activation the broker rejects
#      restores the previous material, and an expiring certificate is
#      reported with a warning.
#   4. The private key never appears in an API response, in a rendered
#      configuration or in the MCM logs.
#
# Every transport probe runs inside the broker network: a published host
# port is accepted by docker-proxy even when no listener is bound inside the
# broker, so a host-side check cannot prove that TLS is live.
set -euo pipefail

readonly GREEN='\033[0;32m'
readonly RED='\033[0;31m'
readonly YELLOW='\033[0;33m'
readonly RESET='\033[0m'

MCM_BASE_URL="${MCM_BASE_URL:-http://127.0.0.1:8080}"
MCM_E2E_TLS_PORT="${MCM_E2E_TLS_PORT:-8883}"
MCM_E2E_MTLS_PORT="${MCM_E2E_MTLS_PORT:-8884}"
MCM_E2E_CERT_PROFILE="${MCM_E2E_CERT_PROFILE:-e2e}"
MCM_E2E_BROKER_HOST="${MCM_E2E_BROKER_HOST:-mcm-mosquitto}"
MCM_E2E_NETWORK="${MCM_E2E_NETWORK:-mcm_default}"
MCM_MOSQUITTO_USERNAME="${MCM_MOSQUITTO_USERNAME:-admin}"
MCM_MOSQUITTO_PASSWORD="${MCM_MOSQUITTO_PASSWORD:-mcm-dev-broker-password}"
export MCM_MOSQUITTO_USERNAME MCM_MOSQUITTO_PASSWORD

if [[ -z "${MCM_AUTH_TOKEN:-}" ]]; then
    printf '%b\n' "${RED}✗ MCM_AUTH_TOKEN is required${RESET}" >&2
    exit 1
fi

if ! command -v openssl >/dev/null 2>&1; then
    printf '%b\n' "${RED}✗ openssl is required to build the certificate material${RESET}" >&2
    exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
    printf '%b\n' "${RED}✗ docker is required to probe the broker network${RESET}" >&2
    exit 1
fi

STEP=0
TMP_FILES=()
TMP_DIRS=()

cleanup() {
    local exit_code=$?
    local file directory
    for file in "${TMP_FILES[@]}"; do
        rm -f "$file"
    done
    for directory in "${TMP_DIRS[@]}"; do
        rm -rf "$directory"
    done
    exit "$exit_code"
}
trap cleanup EXIT

pass() {
    STEP=$((STEP + 1))
    printf '%b\n' "${GREEN}✓ step ${STEP}: $1${RESET}"
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

work_dir() {
    local directory
    directory="$(mktemp -d)"
    TMP_DIRS+=("$directory")
    printf '%s' "$directory"
}

wait_for_ready() {
    local deadline=$((SECONDS + 60))
    while (( SECONDS < deadline )); do
        if curl -fsS "${MCM_BASE_URL}/readyz" >/dev/null 2>&1; then
            pass 'wait for MCM to become ready'
            return
        fi
        sleep 1
    done
    fail "MCM did not become ready at ${MCM_BASE_URL}/readyz within 60 seconds"
}

post_json_file() {
    local endpoint="$1"
    local payload_file="$2"
    local output="$3"
    curl -sS -o "$output" -w '%{http_code}' \
        -X POST "${MCM_BASE_URL}${endpoint}" \
        -H "Authorization: Bearer ${MCM_AUTH_TOKEN}" \
        -H 'Content-Type: application/json' \
        --data-binary "@${payload_file}" || printf '000'
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

get_json() {
    local endpoint="$1"
    local output="$2"
    curl -sS -o "$output" -w '%{http_code}' \
        -H "Authorization: Bearer ${MCM_AUTH_TOKEN}" \
        "${MCM_BASE_URL}${endpoint}" || printf '000'
}

# generate_material builds a certificate authority, a server certificate and a
# client certificate in the given directory.
#
# The server certificate covers the broker host name so the TLS clients can
# verify it, and the client certificate carries the clientAuth usage so the
# broker can accept it on a mutual TLS listener.
generate_material() {
    local directory="$1"
    local name="$2"
    local days="$3"
    mkdir -p "$directory"
    openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 3650 \
        -keyout "$directory/ca.key" -out "$directory/ca.crt" \
        -subj "/CN=${name}-ca" \
        -addext 'basicConstraints=critical,CA:TRUE' \
        -addext 'keyUsage=critical,keyCertSign,cRLSign' >/dev/null 2>&1
    openssl req -newkey rsa:2048 -nodes -sha256 \
        -keyout "$directory/server.key" -out "$directory/server.csr" \
        -subj "/CN=${MCM_E2E_BROKER_HOST}" >/dev/null 2>&1
    printf 'subjectAltName=DNS:%s,DNS:mosquitto,DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' \
        "$MCM_E2E_BROKER_HOST" >"$directory/server.ext"
    openssl x509 -req -in "$directory/server.csr" -CA "$directory/ca.crt" -CAkey "$directory/ca.key" \
        -CAserial "$directory/server.srl" -CAcreateserial -out "$directory/server.crt" \
        -days "$days" -sha256 -extfile "$directory/server.ext" >/dev/null 2>&1
    openssl req -newkey rsa:2048 -nodes -sha256 \
        -keyout "$directory/client.key" -out "$directory/client.csr" \
        -subj "/CN=${name}-client" >/dev/null 2>&1
    printf 'extendedKeyUsage=clientAuth\n' >"$directory/client.ext"
    openssl x509 -req -in "$directory/client.csr" -CA "$directory/ca.crt" -CAkey "$directory/ca.key" \
        -CAserial "$directory/client.srl" -CAcreateserial -out "$directory/client.crt" \
        -days "$days" -sha256 -extfile "$directory/client.ext" >/dev/null 2>&1
}

# upload_revision uploads the material of a directory as a new revision.
#
# The private key travels in the request body only: it is written to a file
# that is passed to curl with --data-binary so it never reaches the process
# arguments.
upload_revision() {
    local directory="$1"
    local description="$2"
    local payload="$3"
    local output="$4"
    jq -cn \
        --rawfile ca "$directory/ca.crt" \
        --rawfile cert "$directory/server.crt" \
        --rawfile key "$directory/server.key" \
        --arg description "$description" \
        --arg host "$MCM_E2E_BROKER_HOST" \
        '{ca_cert_pem: $ca, server_cert_pem: $cert, server_key_pem: $key,
          chain_pem: "", expected_names: [$host, "mosquitto"],
          description: $description, require_ca: true}' >"$payload"
    post_json_file "/api/v1/broker/certificates/profiles/${MCM_E2E_CERT_PROFILE}/revisions" \
        "$payload" "$output"
}

# mqtt_publish publishes one message over TLS.
#
# The probe runs inside the broker network and reaches the broker by service
# name, so the TLS server name matches the certificate subject alternative
# name.
mqtt_publish() {
    local port="$1"
    local ca="$2"
    local cert="$3"
    local key="$4"
    local topic="$5"
    local payload="$6"
    # The certificate and the key are optional: an empty value means the
    # client does not present one.
    docker run --rm --network "$MCM_E2E_NETWORK" \
        -e MCM_MOSQUITTO_USERNAME -e MCM_MOSQUITTO_PASSWORD \
        -v "${WORK_ROOT}:/certs:ro" \
        eclipse-mosquitto:2.0 sh -ec '
            host="$1"; port="$2"; ca="$3"; cert="$4"; key="$5"; topic="$6"; payload="$7"
            set -- mosquitto_pub -h "$host" -p "$port" --cafile "$ca" \
                -u "$MCM_MOSQUITTO_USERNAME" -P "$MCM_MOSQUITTO_PASSWORD" \
                -t "$topic" -q 1 -m "$payload"
            if [ -n "$cert" ]; then
                set -- "$@" --cert "$cert" --key "$key"
            fi
            exec "$@"
        ' sh "$MCM_E2E_BROKER_HOST" "$port" "$ca" "$cert" "$key" \
        "$topic" "$payload"
}

# mqtt_roundtrip publishes and receives one message over TLS.
mqtt_roundtrip() {
    local port="$1"
    local ca="$2"
    local cert="$3"
    local key="$4"
    local topic="$5"
    local payload="$6"
    # The certificate and the key are optional: an empty value means the
    # client does not present one.
    docker run --rm --network "$MCM_E2E_NETWORK" \
        -e MCM_MOSQUITTO_USERNAME -e MCM_MOSQUITTO_PASSWORD \
        -v "${WORK_ROOT}:/certs:ro" \
        eclipse-mosquitto:2.0 sh -ec '
            host="$1"; port="$2"; ca="$3"; cert="$4"; key="$5"; topic="$6"; payload="$7"
            output=/tmp/mcm-tls-message
            set -- mosquitto_sub -h "$host" -p "$port" --cafile "$ca" \
                -u "$MCM_MOSQUITTO_USERNAME" -P "$MCM_MOSQUITTO_PASSWORD" \
                -t "$topic" -q 1 -W 15
            if [ -n "$cert" ]; then
                set -- "$@" --cert "$cert" --key "$key"
            fi
            "$@" >"$output" 2>&1 &
            subscriber=$!
            sleep 1
            set -- mosquitto_pub -h "$host" -p "$port" --cafile "$ca" \
                -u "$MCM_MOSQUITTO_USERNAME" -P "$MCM_MOSQUITTO_PASSWORD" \
                -t "$topic" -q 1 -m "$payload"
            if [ -n "$cert" ]; then
                set -- "$@" --cert "$cert" --key "$key"
            fi
            "$@"
            wait "$subscriber" || true
            grep -Fxq "$payload" "$output" || {
                printf "expected payload was not received; subscriber output follows:\\n"
                cat "$output"
                exit 1
            }
        ' sh "$MCM_E2E_BROKER_HOST" "$port" "$ca" "$cert" "$key" \
        "$topic" "$payload"
}

# served_fingerprint prints the SHA-256 fingerprint of the certificate the
# broker serves on the given port.
#
# The Mosquitto image ships no openssl client, so the probe uses the Python
# standard library: it reads the peer certificate in DER form and hashes it
# exactly like MCM does.
served_fingerprint() {
    local port="$1"
    local ca="$2"
    # The certificate authority is the only file the probe needs.
    docker run --rm --network "$MCM_E2E_NETWORK" \
        -v "${WORK_ROOT}:/certs:ro" \
        python:3.12-slim python3 -c '
import hashlib
import socket
import ssl
import sys

host, port, cafile = sys.argv[1], int(sys.argv[2]), sys.argv[3]
context = ssl.create_default_context(cafile=cafile)
with socket.create_connection((host, port), timeout=20) as sock:
    with context.wrap_socket(sock, server_hostname=host) as tls_sock:
        print(hashlib.sha256(tls_sock.getpeercert(binary_form=True)).hexdigest())
' "$MCM_E2E_BROKER_HOST" "$port" "$ca"
}

# retry runs a probe until it succeeds or the deadline expires.
retry() {
    local description="$1"
    local attempts="$2"
    shift 2
    local attempt=1
    while (( attempt <= attempts )); do
        if "$@"; then
            return 0
        fi
        attempt=$((attempt + 1))
        sleep 2
    done
    fail "$description did not succeed after ${attempts} attempts"
}

listener_preview() {
    local desired="$1"
    local output="$2"
    local code
    code="$(post_json '/api/v1/listeners/preview' \
        "$(jq -cn --argjson specs "$desired" '{specs: $specs, confirm: false}')" "$output")"
    if [[ "$code" != '200' ]]; then
        fail "listener preview failed with HTTP ${code}: $(<"$output")"
    fi
    LISTENER_REVISION_ID="$(jq -r '.revision_id // empty' "$output")"
    if [[ -z "$LISTENER_REVISION_ID" ]]; then
        fail "listener preview did not return a revision identifier: $(<"$output")"
    fi
}

listener_apply() {
    local output="$1"
    local code
    code="$(post_json '/api/v1/listeners/apply' \
        "$(jq -cn --arg revision_id "$LISTENER_REVISION_ID" '{revision_id: $revision_id, confirm: true}')" \
        "$output")"
    if [[ "$code" != '200' ]]; then
        fail "listener apply failed with HTTP ${code}: $(<"$output")"
    fi
}

assert_no_key_material() {
    local description="$1"
    local file="$2"
    if grep -q 'PRIVATE KEY' "$file"; then
        fail "$description exposed private key material"
    fi
}

WORK_ROOT="$(work_dir)"
MATERIAL_ONE="$WORK_ROOT/material-one"
MATERIAL_TWO="$WORK_ROOT/material-two"
MATERIAL_EXPIRING="$WORK_ROOT/material-expiring"

# The certificate material is mounted read-only inside the probe
# containers. The paths are the ones the probes use.
CA_ONE="/certs/material-one/ca.crt"
CLIENT_ONE_CERT="/certs/material-one/client.crt"
CLIENT_ONE_KEY="/certs/material-one/client.key"
CA_TWO="/certs/material-two/ca.crt"

# The listener renderer owns the whole listener section of the broker
# configuration: a listener that is not part of the desired set is
# removed. The default listeners of the dev stack are therefore part of
# the desired set, otherwise the broker would stop serving them.
BASE_LISTENERS="$(jq -cn '[
    {id: "e2e-plain", port: 1883, bind: "0.0.0.0", protocols: ["mqtt"]},
    {id: "e2e-websockets", port: 9001, bind: "0.0.0.0", protocols: ["websockets"]}
]')"

# merge_listeners adds the given listeners to the desired set read from
# the standard input, keeping the first listener that claims a port.
merge_listeners() {
    jq -c --argjson extra "$1" '
        reduce $extra[] as $candidate (.;
            if any(.[]; .port == $candidate.port) then . else . + [$candidate] end)'
}

wait_for_ready

# The published listener contract uses the capitalized field names, so the
# specs are normalized to the lowercase form the script works with.
LISTENERS="$(curl -fsS -H "Authorization: Bearer ${MCM_AUTH_TOKEN}" \
    "${MCM_BASE_URL}/api/v1/listeners" | jq -c '[
        (.specs // [])[]
        | {id: (.ID // .id), port: (.Port // .port), bind: (.Bind // .bind),
           protocols: (.Protocols // .protocols),
           options: (.Options // .options // {}),
           tls_profile: (.tls_profile // "")}
    ]')" \
    || fail 'could not fetch the existing listener specs'

# The listeners the broker served before the script added the TLS ones.
# The TLS ports are excluded so a previous run cannot leak them into the
# restored configuration.
ORIGINAL_LISTENERS="$(merge_listeners "$BASE_LISTENERS" <<<"$(jq -c \
    --argjson tls "$MCM_E2E_TLS_PORT" --argjson mtls "$MCM_E2E_MTLS_PORT" \
    '[.[] | select(.port != $tls and .port != $mtls)]' <<<"$LISTENERS")")"
pass 'read the existing listener specs'

response_file
LIST_RESPONSE="$RESPONSE_FILE"
LIST_CODE="$(get_json '/api/v1/broker/certificates' "$LIST_RESPONSE")"
if [[ "$LIST_CODE" != '200' ]]; then
    fail "the certificate list endpoint answered HTTP ${LIST_CODE}: $(<"$LIST_RESPONSE")"
fi
assert_no_key_material 'the certificate list response' "$LIST_RESPONSE"
pass 'the certificate surface is available'

generate_material "$MATERIAL_ONE" 'e2e-one' 365
generate_material "$MATERIAL_TWO" 'e2e-two' 365
generate_material "$MATERIAL_EXPIRING" 'e2e-expiring' 3
pass 'generated the certificate material'

# A certificate and a key that do not belong together must be rejected.
response_file
INVALID_PAYLOAD="$RESPONSE_FILE"
jq -cn \
    --rawfile ca "$MATERIAL_ONE/ca.crt" \
    --rawfile cert "$MATERIAL_ONE/server.crt" \
    --rawfile key "$MATERIAL_TWO/server.key" \
    '{ca_cert_pem: $ca, server_cert_pem: $cert, server_key_pem: $key,
      expected_names: [], description: "mismatched material", require_ca: true}' \
    >"$INVALID_PAYLOAD"
response_file
INVALID_RESPONSE="$RESPONSE_FILE"
INVALID_CODE="$(post_json_file \
    "/api/v1/broker/certificates/profiles/${MCM_E2E_CERT_PROFILE}/revisions" \
    "$INVALID_PAYLOAD" "$INVALID_RESPONSE")"
if [[ "$INVALID_CODE" != '400' ]]; then
    fail "a mismatched certificate and key were accepted with HTTP ${INVALID_CODE}: $(<"$INVALID_RESPONSE")"
fi
if [[ "$(jq -r '.issues | length // 0' "$INVALID_RESPONSE")" == '0' ]]; then
    fail "the mismatched upload did not report any issue: $(<"$INVALID_RESPONSE")"
fi
assert_no_key_material 'the rejected upload response' "$INVALID_RESPONSE"
pass 'reject a certificate and a key that do not match'

response_file
UPLOAD_PAYLOAD="$RESPONSE_FILE"
response_file
UPLOAD_RESPONSE="$RESPONSE_FILE"
UPLOAD_CODE="$(upload_revision "$MATERIAL_ONE" 'issue #300 end-to-end material' \
    "$UPLOAD_PAYLOAD" "$UPLOAD_RESPONSE")"
if [[ "$UPLOAD_CODE" != '201' ]]; then
    fail "the certificate upload failed with HTTP ${UPLOAD_CODE}: $(<"$UPLOAD_RESPONSE")"
fi
REVISION_ONE="$(jq -r '.revision_id // empty' "$UPLOAD_RESPONSE")"
FINGERPRINT_ONE="$(jq -r '.fingerprint_sha256 // empty' "$UPLOAD_RESPONSE")"
if [[ -z "$REVISION_ONE" || -z "$FINGERPRINT_ONE" ]]; then
    fail "the certificate upload did not return the revision and its fingerprint: $(<"$UPLOAD_RESPONSE")"
fi
assert_no_key_material 'the certificate upload response' "$UPLOAD_RESPONSE"
pass "upload the first revision ${REVISION_ONE}"

response_file
CERT_PREVIEW="$RESPONSE_FILE"
CERT_PREVIEW_CODE="$(post_json '/api/v1/broker/certificates/preview' \
    "$(jq -cn --arg profile "$MCM_E2E_CERT_PROFILE" --arg revision "$REVISION_ONE" \
        '{profile: $profile, revision_id: $revision}')" "$CERT_PREVIEW")"
if [[ "$CERT_PREVIEW_CODE" != '200' ]]; then
    fail "the certificate preview failed with HTTP ${CERT_PREVIEW_CODE}: $(<"$CERT_PREVIEW")"
fi
CERT_PATH="$(jq -r '.files.cert_path // empty' "$CERT_PREVIEW")"
KEY_PATH="$(jq -r '.files.key_path // empty' "$CERT_PREVIEW")"
if [[ "$CERT_PATH" != */"${MCM_E2E_CERT_PROFILE}/active/server.crt" ]]; then
    fail "the certificate preview did not report the active symlink path: $(<"$CERT_PREVIEW")"
fi
if [[ "$KEY_PATH" != */"${MCM_E2E_CERT_PROFILE}/active/server.key" ]]; then
    fail "the certificate preview did not report the active key path: $(<"$CERT_PREVIEW")"
fi
assert_no_key_material 'the certificate preview response' "$CERT_PREVIEW"
pass 'preview the activation and the stable broker paths'

response_file
CERT_APPLY="$RESPONSE_FILE"
CERT_APPLY_CODE="$(post_json '/api/v1/broker/certificates/apply' \
    "$(jq -cn --arg profile "$MCM_E2E_CERT_PROFILE" --arg revision "$REVISION_ONE" \
        '{profile: $profile, revision_id: $revision}')" "$CERT_APPLY")"
if [[ "$CERT_APPLY_CODE" != '200' ]]; then
    fail "the certificate activation failed with HTTP ${CERT_APPLY_CODE}: $(<"$CERT_APPLY")"
fi
if [[ "$(jq -r '.restarted // false' "$CERT_APPLY")" != 'true' ]]; then
    fail "the certificate activation did not restart the broker: $(<"$CERT_APPLY")"
fi
assert_no_key_material 'the certificate activation response' "$CERT_APPLY"
pass 'activate the first revision'

TLS_LISTENER="$(jq -cn --argjson port "$MCM_E2E_TLS_PORT" --arg profile "$MCM_E2E_CERT_PROFILE" \
    '[{id: "e2e-tls", port: $port, bind: "0.0.0.0", protocols: ["mqtt"], tls_profile: $profile}]')"
TLS_DESIRED="$(merge_listeners "$TLS_LISTENER" <<<"$ORIGINAL_LISTENERS")"
response_file
TLS_PREVIEW="$RESPONSE_FILE"
listener_preview "$TLS_DESIRED" "$TLS_PREVIEW"
if ! grep -q 'certfile' "$TLS_PREVIEW"; then
    fail "the listener preview did not inject the certificate path: $(<"$TLS_PREVIEW")"
fi
if ! grep -q 'keyfile' "$TLS_PREVIEW"; then
    fail "the listener preview did not inject the key path: $(<"$TLS_PREVIEW")"
fi
assert_no_key_material 'the listener preview' "$TLS_PREVIEW"
response_file
TLS_APPLY="$RESPONSE_FILE"
listener_apply "$TLS_APPLY"
assert_no_key_material 'the listener apply' "$TLS_APPLY"
pass 'bind a listener to the certificate profile'

TLS_PAYLOAD="tls-roundtrip-${RANDOM}-$(date +%s)"
retry 'the plain TLS round-trip' 10 \
    mqtt_roundtrip "$MCM_E2E_TLS_PORT" "$CA_ONE" '' '' 'mcm/healthcheck' "$TLS_PAYLOAD"
pass 'publish and receive over plain TLS'

SERVED_FINGERPRINT="$(served_fingerprint "$MCM_E2E_TLS_PORT" "$CA_ONE")"
if [[ "$SERVED_FINGERPRINT" != "$FINGERPRINT_ONE" ]]; then
    fail "the broker served the certificate ${SERVED_FINGERPRINT} instead of ${FINGERPRINT_ONE}"
fi
pass 'the broker serves the uploaded certificate'

MTLS_LISTENER="$(jq -cn --argjson port "$MCM_E2E_MTLS_PORT" --arg profile "$MCM_E2E_CERT_PROFILE" \
    '[{id: "e2e-mtls", port: $port, bind: "0.0.0.0", protocols: ["mqtt"],
      options: {require_certificate: "true"}, tls_profile: $profile}]')"
MTLS_DESIRED="$(merge_listeners "$MTLS_LISTENER" <<<"$TLS_DESIRED")"
response_file
MTLS_PREVIEW="$RESPONSE_FILE"
listener_preview "$MTLS_DESIRED" "$MTLS_PREVIEW"
if ! grep -q 'cafile' "$MTLS_PREVIEW"; then
    fail "the mutual TLS listener did not receive the certificate authority: $(<"$MTLS_PREVIEW")"
fi
if ! grep -q 'require_certificate' "$MTLS_PREVIEW"; then
    fail "the mutual TLS listener did not require a client certificate: $(<"$MTLS_PREVIEW")"
fi
response_file
MTLS_APPLY="$RESPONSE_FILE"
listener_apply "$MTLS_APPLY"
pass 'bind a second listener that requires a client certificate'

MTLS_PAYLOAD="mtls-roundtrip-${RANDOM}-$(date +%s)"
retry 'the mutual TLS round-trip' 10 \
    mqtt_roundtrip "$MCM_E2E_MTLS_PORT" "$CA_ONE" "$CLIENT_ONE_CERT" \
    "$CLIENT_ONE_KEY" 'mcm/healthcheck' "$MTLS_PAYLOAD"
pass 'publish and receive over mutual TLS'

if mqtt_publish "$MCM_E2E_MTLS_PORT" "$CA_ONE" '' '' 'mcm/healthcheck' 'no-client-certificate' \
    >/dev/null 2>&1; then
    fail 'the mutual TLS listener accepted a client without a certificate'
fi
pass 'the mutual TLS listener rejects a client without a certificate'

if mqtt_publish "$MCM_E2E_TLS_PORT" "$CA_TWO" '' '' 'mcm/healthcheck' 'untrusted-authority' \
    >/dev/null 2>&1; then
    fail 'the plain TLS listener accepted a client that trusts another authority'
fi
pass 'the plain TLS listener rejects an untrusted authority'

# An activation the broker rejects must restore the previous material.
response_file
BROKEN_PAYLOAD="$RESPONSE_FILE"
jq -cn \
    --rawfile ca "$MATERIAL_ONE/ca.crt" \
    --rawfile cert "$MATERIAL_TWO/server.crt" \
    --rawfile key "$MATERIAL_TWO/server.key" \
    --arg description 'signed by another authority' \
    '{ca_cert_pem: $ca, server_cert_pem: $cert, server_key_pem: $key,
      expected_names: [], description: $description, require_ca: true}' \
    >"$BROKEN_PAYLOAD"
response_file
BROKEN_UPLOAD="$RESPONSE_FILE"
BROKEN_UPLOAD_CODE="$(post_json_file \
    "/api/v1/broker/certificates/profiles/${MCM_E2E_CERT_PROFILE}/revisions" \
    "$BROKEN_PAYLOAD" "$BROKEN_UPLOAD")"
if [[ "$BROKEN_UPLOAD_CODE" != '201' ]]; then
    fail "the upload of the mismatched authority failed with HTTP ${BROKEN_UPLOAD_CODE}: $(<"$BROKEN_UPLOAD")"
fi
BROKEN_REVISION="$(jq -r '.revision_id // empty' "$BROKEN_UPLOAD")"
response_file
BROKEN_APPLY="$RESPONSE_FILE"
BROKEN_APPLY_CODE="$(post_json '/api/v1/broker/certificates/apply' \
    "$(jq -cn --arg profile "$MCM_E2E_CERT_PROFILE" --arg revision "$BROKEN_REVISION" \
        '{profile: $profile, revision_id: $revision}')" "$BROKEN_APPLY")"
if [[ "$BROKEN_APPLY_CODE" != '502' ]]; then
    fail "the activation of a certificate the broker rejects answered HTTP ${BROKEN_APPLY_CODE}: $(<"$BROKEN_APPLY")"
fi
if [[ "$(jq -r '.result.recovered // false' "$BROKEN_APPLY")" != 'true' ]]; then
    fail "the rejected activation did not report a recovery: $(<"$BROKEN_APPLY")"
fi
assert_no_key_material 'the rejected activation response' "$BROKEN_APPLY"
pass 'reject an activation the broker does not serve and recover the previous material'

retry 'the plain TLS round-trip after the recovery' 10 \
    mqtt_roundtrip "$MCM_E2E_TLS_PORT" "$CA_ONE" '' '' 'mcm/healthcheck' \
    "recovered-${RANDOM}"
RECOVERED_FINGERPRINT="$(served_fingerprint "$MCM_E2E_TLS_PORT" "$CA_ONE")"
if [[ "$RECOVERED_FINGERPRINT" != "$FINGERPRINT_ONE" ]]; then
    fail "the recovery did not restore the previous certificate: ${RECOVERED_FINGERPRINT}"
fi
pass 'the previous material is served again after the recovery'

# The rotation activates a new revision under a new authority.
response_file
ROTATION_PAYLOAD="$RESPONSE_FILE"
response_file
ROTATION_RESPONSE="$RESPONSE_FILE"
ROTATION_CODE="$(upload_revision "$MATERIAL_TWO" 'issue #300 rotation material' \
    "$ROTATION_PAYLOAD" "$ROTATION_RESPONSE")"
if [[ "$ROTATION_CODE" != '201' ]]; then
    fail "the rotation upload failed with HTTP ${ROTATION_CODE}: $(<"$ROTATION_RESPONSE")"
fi
REVISION_TWO="$(jq -r '.revision_id // empty' "$ROTATION_RESPONSE")"
FINGERPRINT_TWO="$(jq -r '.fingerprint_sha256 // empty' "$ROTATION_RESPONSE")"
if [[ "$FINGERPRINT_TWO" == "$FINGERPRINT_ONE" ]]; then
    fail 'the rotation did not change the served certificate'
fi
response_file
ROTATION_APPLY="$RESPONSE_FILE"
ROTATION_APPLY_CODE="$(post_json '/api/v1/broker/certificates/apply' \
    "$(jq -cn --arg profile "$MCM_E2E_CERT_PROFILE" --arg revision "$REVISION_TWO" \
        '{profile: $profile, revision_id: $revision}')" "$ROTATION_APPLY")"
if [[ "$ROTATION_APPLY_CODE" != '200' ]]; then
    fail "the rotation activation failed with HTTP ${ROTATION_APPLY_CODE}: $(<"$ROTATION_APPLY")"
fi
assert_no_key_material 'the rotation activation response' "$ROTATION_APPLY"
pass "activate the rotated revision ${REVISION_TWO}"

retry 'the plain TLS round-trip after the rotation' 10 \
    mqtt_roundtrip "$MCM_E2E_TLS_PORT" "$CA_TWO" '' '' 'mcm/healthcheck' \
    "rotated-${RANDOM}"
ROTATED_FINGERPRINT="$(served_fingerprint "$MCM_E2E_TLS_PORT" "$CA_TWO")"
if [[ "$ROTATED_FINGERPRINT" != "$FINGERPRINT_TWO" ]]; then
    fail "the broker served ${ROTATED_FINGERPRINT} instead of the rotated ${FINGERPRINT_TWO}"
fi
if mqtt_publish "$MCM_E2E_TLS_PORT" "$CA_ONE" '' '' 'mcm/healthcheck' 'previous-authority' \
    >/dev/null 2>&1; then
    fail 'the broker still trusts the previous authority after the rotation'
fi
pass 'the rotated certificate is served and the previous authority is rejected'

response_file
REVISIONS="$RESPONSE_FILE"
REVISIONS_CODE="$(get_json '/api/v1/broker/certificates' "$REVISIONS")"
if [[ "$REVISIONS_CODE" != '200' ]]; then
    fail "the certificate list answered HTTP ${REVISIONS_CODE}: $(<"$REVISIONS")"
fi
if [[ "$(jq -r --arg profile "$MCM_E2E_CERT_PROFILE" \
    '.profiles[] | select(.name == $profile) | .active_revision' "$REVISIONS")" != "$REVISION_TWO" ]]; then
    fail "the profile does not report the rotated revision as active: $(<"$REVISIONS")"
fi
if [[ "$(jq -r --arg revision "$REVISION_ONE" \
    '.profiles[] | .revisions[] | select(.revision_id == $revision) | .state' "$REVISIONS")" != 'superseded' ]]; then
    fail "the previous revision was not superseded: $(<"$REVISIONS")"
fi
if [[ "$(jq -r --arg revision "$BROKEN_REVISION" \
    '.profiles[] | .revisions[] | select(.revision_id == $revision) | .state' "$REVISIONS")" != 'failed' ]]; then
    fail "the rejected revision was not marked as failed: $(<"$REVISIONS")"
fi
assert_no_key_material 'the certificate list response' "$REVISIONS"
pass 'the revisions report their lifecycle state'

# A certificate that expires soon must be reported with a warning.
response_file
EXPIRING_PAYLOAD="$RESPONSE_FILE"
response_file
EXPIRING_RESPONSE="$RESPONSE_FILE"
EXPIRING_CODE="$(upload_revision "$MATERIAL_EXPIRING" 'issue #300 expiring material' \
    "$EXPIRING_PAYLOAD" "$EXPIRING_RESPONSE")"
if [[ "$EXPIRING_CODE" != '201' ]]; then
    fail "the upload of the expiring certificate failed with HTTP ${EXPIRING_CODE}: $(<"$EXPIRING_RESPONSE")"
fi
if [[ "$(jq -r '[.warnings[]? | select(.kind == "expiring_soon")] | length' "$EXPIRING_RESPONSE")" == '0' ]]; then
    fail "the expiring certificate was accepted without a warning: $(<"$EXPIRING_RESPONSE")"
fi
assert_no_key_material 'the expiring upload response' "$EXPIRING_RESPONSE"
pass 'report the expiry of a short-lived certificate'

MCM_CONTAINER="$(docker ps --filter 'label=com.docker.compose.service=mcm' --format '{{.Names}}' | head -1)"
if [[ -z "$MCM_CONTAINER" ]]; then
    skip 'the MCM container is not running; the log check was skipped'
else
    if docker logs "$MCM_CONTAINER" 2>&1 | grep -q 'PRIVATE KEY'; then
        fail 'the MCM logs contain private key material'
    fi
    pass 'the MCM logs carry no private key material'
fi

RESTORE_LISTENERS="$ORIGINAL_LISTENERS"
response_file
RESTORE_PREVIEW="$RESPONSE_FILE"
listener_preview "$RESTORE_LISTENERS" "$RESTORE_PREVIEW"
response_file
RESTORE_APPLY="$RESPONSE_FILE"
listener_apply "$RESTORE_APPLY"
pass 'restore the original listener set'

printf '%b\n' "${GREEN}✓ e2e-broker-tls: all steps passed${RESET}"
