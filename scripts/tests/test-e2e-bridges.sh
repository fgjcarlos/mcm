#!/usr/bin/env bash
# Integration test of the bridge surface against a real Mosquitto broker.
#
# The test runs against two real Mosquitto 2.0 brokers: a bridge is a
# connection between two brokers, so only the broker can prove that the
# traffic flows, that the configuration MCM renders is accepted, and that
# the connection recovers when the remote broker comes back. It covers
#
#   - the configuration MCM renders loads in the broker,
#   - the traffic flows from the local broker to the remote one,
#   - the traffic flows from the remote broker back to the local one,
#   - the bridge reconnects after the remote broker was restarted.
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
    GOFLAGS='-mod=mod' go test -count=1 -tags integration ./internal/mosquitto/bridges/... "$@"
}

if ! command -v docker >/dev/null 2>&1 && [ -z "${MOSQUITTO_BIN:-}" ]; then
    fail 'neither MOSQUITTO_BIN nor docker is available: the integration test needs a real broker'
fi

go_test -run TestRenderedConfigurationLoadsInTheBroker -v \
    || fail 'the configuration MCM renders is not accepted by the broker'
pass 'the rendered configuration loads in a real broker'

go_test -run TestBridgeForwardsTheTrafficAndRecovers -v \
    || fail 'the bridge does not carry the traffic and does not recover'
pass 'a bridge forwards the traffic between two brokers and recovers'

# The always-on unit suite must stay green.
GOFLAGS='-mod=mod' go test -count=1 ./internal/mosquitto/bridges/... \
    || fail 'the bridge unit tests failed'
pass 'the bridge unit tests are green'
