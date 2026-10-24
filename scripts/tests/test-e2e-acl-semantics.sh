#!/usr/bin/env bash
# Integration test of the native Mosquitto ACL semantics.
#
# The test runs against a real Mosquitto 2.0 broker: the semantics MCM
# models are the semantics of the broker, and a static audit cannot prove
# them. It covers
#
#   - a deny rule is handled before a grant,
#   - the anonymous section applies to unauthenticated clients only,
#   - the pattern rules apply to every client, with the username and the
#     client id substituted,
#   - a listener that declares its own ACL file uses only that file.
#
# The broker is the eclipse-mosquitto container image. A local mosquitto
# binary is used instead when MOSQUITTO_BIN points at one.
set -euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

pass() {
    printf 'PASS: %s\n' "$1"
}

cd "$ROOT"

# The integration test is tagged, so the normal `go test ./...` excludes it.
go_test() {
    GOFLAGS='-mod=mod' go test -count=1 -tags integration ./internal/acl/... "$@"
}

if ! command -v docker >/dev/null 2>&1 && [ -z "${MOSQUITTO_BIN:-}" ]; then
    fail 'neither MOSQUITTO_BIN nor docker is available: the integration test needs a real broker'
fi

go_test -run TestBrokerAppliesTheNativeACLSemantics -v \
    || fail 'the native ACL semantics of the broker do not match the model of MCM'
pass 'the native ACL semantics of a real broker are verified'

# The always-on unit suite must stay green.
GOFLAGS='-mod=mod' go test -count=1 ./internal/acl/... \
    || fail 'the acl unit tests failed'
pass 'the acl unit tests are green'
