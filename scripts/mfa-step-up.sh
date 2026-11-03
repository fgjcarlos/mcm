#!/usr/bin/env bash
# Obtain a recent-MFA proof for a sensitive operation.
#
# Usage: scripts/mfa-step-up.sh <host_url> <admin_token>
#
# Prints: "<operator_token> <proof>"
#
# The proof is bound to the session that requested it, so the apply that
# carries it must use the same bearer token. This helper therefore creates
# its own operator, signs in as that operator, enrolls TOTP for it and
# exchanges an unused time step for the proof.
#
# The operator is deliberately NOT the bootstrap administrator: enrolling
# MFA on that account would change how every later login on the same
# instance behaves, and the other end-to-end scripts sign in with it. Each
# call also uses a fresh operator, so no two scripts share the consumed
# TOTP time steps of one account.
#
# Requires: curl, jq, base32, xxd and openssl.
set -euo pipefail

HOST_URL="${1:?usage: mfa-step-up.sh <host_url> <admin_token>}"
ADMIN_TOKEN="${2:?usage: mfa-step-up.sh <host_url> <admin_token>}"

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

api_public() {
    local method="$1" path="$2" body="${3:-}"
    if [ -n "$body" ]; then
        curl -sS -X "$method" "${HOST_URL}${path}" \
            -H 'Content-Type: application/json' \
            -d "$body"
    else
        curl -sS -X "$method" "${HOST_URL}${path}"
    fi
}

api_auth() {
    local token="$1" method="$2" path="$3" body="${4:-}"
    if [ -n "$body" ]; then
        curl -sS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${token}" \
            -H 'Content-Type: application/json' \
            -d "$body"
    else
        curl -sS -X "$method" "${HOST_URL}${path}" \
            -H "Authorization: Bearer ${token}"
    fi
}

# --- a dedicated operator --------------------------------------------------
suffix="$(openssl rand -hex 6)"
operator="mcm-e2e-mfa-${suffix}"
operator_password="$(openssl rand -hex 12)"

created="$(api_auth "$ADMIN_TOKEN" POST /api/v1/admin-users "{\"username\":\"${operator}\",\"password\":\"${operator_password}\",\"role\":\"admin\",\"disabled\":false}")"
if ! printf '%s' "$created" | jq -e '.username // empty' >/dev/null 2>&1; then
    printf 'Could not create the MFA operator: %s\n' "$created" >&2
    exit 1
fi

login="$(api_public POST /api/v1/auth/login "{\"username\":\"${operator}\",\"password\":\"${operator_password}\"}")"
operator_token="$(printf '%s' "$login" | jq -r '.token // empty')"
if [ -z "$operator_token" ]; then
    printf 'The MFA operator could not sign in: %s\n' "$login" >&2
    exit 1
fi

# --- enroll and prove ------------------------------------------------------
setup_body="$(api_auth "$operator_token" POST /api/v1/auth/mfa/setup '{}')"
secret="$(printf '%s' "$setup_body" | jq -r '.secret // empty')"
if [ -z "$secret" ]; then
    printf 'MFA enrollment failed: %s\n' "$setup_body" >&2
    exit 1
fi

step="$(current_step)"
verify_body="$(api_auth "$operator_token" POST /api/v1/auth/mfa/verify "{\"code\":\"$(totp_code "$secret" "$step")\"}")"
if printf '%s' "$verify_body" | jq -e '.error // empty' >/dev/null 2>&1; then
    printf 'MFA enrollment confirmation failed: %s\n' "$verify_body" >&2
    exit 1
fi

# The confirmation consumed the current time step, so the step-up uses the
# next one. The API accepts that skew window but rejects a reused step.
step=$(( step + 1 ))
proof_body="$(api_auth "$operator_token" POST /api/v1/auth/mfa/step-up "{\"code\":\"$(totp_code "$secret" "$step")\"}")"
proof="$(printf '%s' "$proof_body" | jq -r '.proof // empty')"
if [ -z "$proof" ]; then
    printf 'Recent MFA step-up failed: %s\n' "$proof_body" >&2
    exit 1
fi

printf '%s %s' "$operator_token" "$proof"
