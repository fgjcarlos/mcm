#!/bin/sh
# reload-mosquitto.sh — production-style reload signal.
#
# The CI analogue of `systemctl reload mosquitto.service`. Finds the
# running mosquitto process (by PID file when present, by process-name
# match otherwise) and sends SIGHUP — Mosquitto's documented reload
# signal. The broker re-reads password_file / acl_file and applies the
# new config without dropping in-flight connections.
#
# Production deployments replace this script with a systemd unit, an
# SSH hop, or any other sidecar that signals the broker out-of-band
# from MCM — MCM never needs the Docker socket.
#
# Note: this script does NOT pre-verify with `kill -0` because the
# shared PID namespace does not grant signal permission across UID
# boundaries in the test setup. The actual `kill -HUP` will fail if
# the process is gone.

set -eu

PID=""

# 1. Try the explicit PID file (env override or default location).
for candidate in \
    "${MOSQUITTO_PID_FILE:-}" \
    "/mosquitto/config/mosquitto.pid" \
    "/mosquitto/mosquitto.pid" \
    "/var/run/mosquitto.pid" \
    "/tmp/mosquitto.pid"; do
    if [ -n "$candidate" ] && [ -f "$candidate" ]; then
        CANDIDATE_PID="$(cat "$candidate" 2>/dev/null || true)"
        if [ -n "$CANDIDATE_PID" ]; then
            PID="$CANDIDATE_PID"
            break
        fi
    fi
done

# 2. Fall back to a process-name match. With Compose
#    `pid: "service:mosquitto"` the broker runs as
#    `/usr/sbin/mosquitto -c ...` and is visible from MCM's PID
#    namespace. We match the literal command line so pgrep does not
#    pick up the docker exec wrapper itself.
if [ -z "$PID" ]; then
    PID="$(pgrep -fx '/usr/sbin/mosquitto -c /mosquitto/config/mosquitto.conf' | head -n1 || true)"
    if [ -z "$PID" ]; then
        # Looser match — accept any mosquitto command line in case the
        # production config uses a different -c path.
        PID="$(pgrep -f '/usr/sbin/mosquitto -c ' | head -n1 || true)"
    fi
fi

if [ -z "$PID" ]; then
    echo "reload-mosquitto: could not find mosquitto process (no pid file, no matching process)" >&2
    exit 1
fi

# Send SIGHUP — Mosquitto's documented reload signal. We intentionally
# do not pre-check the PID because the shared PID namespace does not
# grant signal permission across UID boundaries in the test setup;
# the actual kill surfaces a meaningful error if the process is gone.
kill -HUP "$PID"
