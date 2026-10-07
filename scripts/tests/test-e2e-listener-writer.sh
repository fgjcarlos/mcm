#!/usr/bin/env bash
# Regression for the listener conf writer splice without Docker.
#
# This test exercises the listener conf writer against a representative
# mosquitto.conf file written to a temp directory. It guards against
# the writer losing directives (passwd / acl / persistence / log_dest /
# log_type / connection_messages / user) when listener blocks are
# spliced in or removed.
#
# The driver lives in internal/listener/writer as a build-tagged test
# so the project's normal `go test ./...` excludes it; the shell
# regression enables the tag to run the full splice flow.
#
# It is intentionally Docker-free: it must pass on every CI runner,
# not only the runners with a Mosquitto container.
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

go_test() {
    GOFLAGS='-mod=mod' go test -count=1 ./internal/listener/writer/... "$@"
}

# 1) The always-on unit suite must still be green.
go_test                 || fail 'listener writer unit tests failed'
pass 'listener writer unit tests green'

# 2) The shell-tagged driver runs the splice on a temp config that
#    mimics the dev compose mosquitto.conf shape. The driver is
#    controlled through LISTENER_WRITER_CONF.
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

CONF_FILE="$TMP_DIR/mosquitto.conf"
cat > "$CONF_FILE" <<'CONF'
# MCM listener writer regression fixture
persistence true
persistence_location /mosquitto/data/

log_dest stdout
log_dest file /mosquitto/log/mosquitto.log
log_type error
log_type warning
connection_messages true

allow_anonymous false
password_file /mosquitto/config/passwd
acl_file /mosquitto/config/acl

user root

listener 1883 0.0.0.0

listener 9001 0.0.0.0
protocol websockets
CONF

LISTENER_WRITER_CONF="$CONF_FILE" go_test -tags shellregression -run TestShellRegressionApplyAndClear \
    || fail 'shell regression driver failed'
pass 'apply, clear, idempotence, snapshot and restore all behave correctly'

printf 'PASS: listener writer shell regression\n'