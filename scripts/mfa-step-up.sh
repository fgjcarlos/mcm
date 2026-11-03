#!/usr/bin/env bash
# Obtain a recent-MFA proof for the given API token.
#
# Usage: scripts/mfa-step-up.sh <host_url> <token>
#
# Enrolls TOTP for the authenticated account (when it is not enrolled yet),
# confirms the enrollment, and exchanges a fresh, unused TOTP step for the
# five-minute proof that sensitive operations require in X-MCM-MFA.
#
# The proof is bound to the exact bearer token and to the current enrollment.
# Requires: curl, jq, base32, xxd and openssl.
set -euo pipefail

HOST_URL="${1:?usage: mfa-step-up.sh <host_url> <token>}"
TOKEN="${2:?usage: mfa-step-up.sh <host_url> <token>}"

for tool in curl jq base32 xxd openssl; do
    command -v "$tool" >/dev/null || { printf 'Required tool is unavailable: %s\n' "$tool" >&2; exit 2; }
done

# totp_code SECRET STEP prints the RFC 6238 code for a base32 secret.
totp_code() {
    local secret="$1" step="$2" padded key_hex counter_hex hmac offset slice value
    secret="$(printf '%s' "$secret" | tr -d '= ' | tr '[:lower:]' '[:upper:]')"
    while [ $(( ${#secret} % 8 )) -ne 0 ]; do secret="${secret}="; done
    key_hex="$(printf '%s' "$secret" | base32 -d 2>/dev/null | xxd -p | tr -d '\n')"
    counter_hex="$(printf '%016x' "$step")"
    hmac="$(printf '%s' "$counter_hex" | xxd -r -p | openssl dgst -sha1 -mac HMAC -macopt "hexkey:${key_hex}" -binary | xxd -p | tr -d '\n')"
    offset=$(( 16#${hmac: -1} ))
    slice="${hmac:$(( offset * 2 )):8}"
    value=$(( 16#$slice & 0x7fffffff ))
    printf '%06d' $(( value % 1000000 ))
}

current_step() { printf '%s' $(( $(date +%s) / 30 )); }

api() {
    local method="$1" path="$2" body="${3:-}"
    if [ -n "$body" ]; then
        curl -sS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${TOKEN}" \
            -H 'Content-Type: application/json' \
            -d "$body"
    else
        curl -sS -X "$method" "${HOST_URL}${path}" -H "Authorization: Bearer ${TOKEN}"
    fi
}

setup_body="$(api POST /api/v1/auth/mfa/setup '{}')"
secret="$(printf '%s' "$setup_body" | jq -r '.secret // empty')"
if [ -z "$secret" ]; then
    printf 'MFA enrollment failed: %s\n' "$setup_body" >&2
    exit 1
fi

step="$(current_step)"
verify_body="$(api POST /api/v1/auth/mfa/verify "{\"code\":\"$(totp_code "$secret" "$step")\"}")"
if ! printf '%s' "$verify_body" | jq -e '.error // empty' >/dev/null 2>&1; then
    : # 204 No Content has an empty body.
else
    printf 'MFA enrollment confirmation failed: %s\n' "$verify_body" >&2
    exit 1
fi

# The confirmation consumed the current time step, so the step-up uses the
# next one. The API accepts that skew window but rejects a reused step.
step=$(( step + 1 ))
proof_body="$(api POST /api/v1/auth/mfa/step-up "{\"code\":\"$(totp_code "$secret" "$step")\"}")"
proof="$(printf '%s' "$proof_body" | jq -r '.proof // empty')"
if [ -z "$proof" ]; then
    printf 'Recent MFA step-up failed: %s\n' "$proof_body" >&2
    exit 1
fi
printf '%s' "$proof"
