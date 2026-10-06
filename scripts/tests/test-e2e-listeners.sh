#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
PRODUCTION_SCRIPT="$SCRIPT_DIR/e2e-listeners.sh"

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
