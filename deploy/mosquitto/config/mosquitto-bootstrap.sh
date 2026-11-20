#!/bin/sh
# mosquitto-bootstrap.sh — initialises the shared Mosquitto config volume
# before launching the broker.
#
# The dev Compose stack uses a NAMED VOLUME for /mosquitto/config so MCM
# and Mosquitto share the passwd/acl files. On a fresh volume that path
# is empty: the broker would either start with anonymous access (because
# no password_file exists) or refuse to start (if allow_anonymous false
# and no password_file).
#
# This script:
#   1. Seeds /mosquitto/config/mosquitto.conf from
#      /mosquitto-init/mosquitto.conf on the first successful startup.
#      Docker may pre-populate a new named volume with the image's
#      anonymous-default config, so the initialization marker—not file
#      absence—distinguishes a managed config from image content. Later
#      restarts preserve MCM's managed config; if it is missing, it is
#      seeded again from the source template.
#   2. Seeds /mosquitto/config/passwd with a single "admin" user using
#      mosquitto_passwd when missing. The dev password is intentionally
#      fixed (MCM_DEV_BROKER_PASSWORD env var, default
#      "mcm-dev-broker-password") so the docker-compose service can pass
#      the matching value to MCM via MCM_MOSQUITTO_PASSWORD. The dev
#      Compose docs explicitly call out that this is NOT a production
#      pattern — production uses mosquitto_passwd -c with a secret from
#      a secret manager.
#   3. Hands passwd, acl, the persistence directory and the log file to
#      the MCM service user and runs the broker as that user (issue
#      #394). MCM writes passwd/acl as its own non-root user, and
#      Mosquitto warns (future versions refuse to load) a password or ACL
#      file whose owner or group is not the broker's user, or that is
#      world readable. Running the broker as the writer makes every file
#      MCM writes, 0600, already owned by the broker.
#   4. Execs /usr/sbin/mosquitto directly. We deliberately bypass the
#      official eclipse-mosquitto /docker-entrypoint.sh here because
#      that entrypoint ALSO seeds /mosquitto/config/mosquitto.conf with
#      its built-in anonymous-default conf BEFORE the CMD runs (Docker
#      ENTRYPOINT runs before CMD). If we let it seed first, our
#      auth-enabled conf would never land in the volume. Calling
#      mosquitto directly is safe because the official entrypoint's
#      other responsibilities (creating data/log dirs, envsubst) are
#      either already done by the image's filesystem layout or
#      unnecessary for our minimal dev config.
#
# Idempotency: the config marker is written only after successful
# initialization. The shared volume then owns mosquitto.conf, passwd, and
# ACL state as MCM updates it; the source template is only a seed, not a
# restart-time source of truth.

set -eu

CONFIG_DIR="/mosquitto/config"
INIT_DIR="${MOSQUITTO_INIT_DIR:-/mosquitto-init}"
DEV_PASSWORD="${MCM_DEV_BROKER_PASSWORD:-mcm-dev-broker-password}"
DEV_USER="${MCM_DEV_BROKER_USER:-admin}"
# uid/gid of the `mcm` user in the mcm:dev image (`docker exec mcm-server id`).
BROKER_UID="${MCM_SERVICE_UID:-100}"
BROKER_GID="${MCM_SERVICE_GID:-101}"
BROKER_BIN="/usr/sbin/mosquitto"

mkdir -p "$CONFIG_DIR"

# 0a. Make the shared config dir world-writable. The named volume is
# pre-populated from the eclipse-mosquitto image's /mosquitto/config
# contents which are owned by the mosquitto user (UID 1883). The mcm
# service runs as a different UID and must be able to atomically
# rewrite passwd/acl on every deploy apply. Without this chmod, the
# atomic-rename applier in the deploy service fails with EACCES on the
# cross-container write. Production deployments use the mosquitto user
# consistently across both containers (see deploy/mosquitto/README.md),
# so this 0777 shortcut is dev-only.
chmod 777 "$CONFIG_DIR" 2>/dev/null || true

# 1. Seed an absent config, or replace the image's anonymous-default config
#    on first initialization. A missing marker alone is not evidence that an
#    existing config is disposable: older managed configs may have no marker.
CONFIG_MARKER="$CONFIG_DIR/.mcm-bootstrap-initialized"
CONFIG_FILE="$CONFIG_DIR/mosquitto.conf"
needs_seed=false
if [ ! -f "$CONFIG_FILE" ]; then
    needs_seed=true
elif [ ! -f "$CONFIG_MARKER" ]; then
    # A markerless config is only disposable when it provably holds no active
    # directive. grep exits 1 for "no match" and >1 for a read or usage error,
    # so an unreadable config must be reported instead of silently replaced.
    if grep -Eq '^[[:space:]]*[^#[:space:]]' "$CONFIG_FILE"; then
        :
    else
        grep_status=$?
        if [ "$grep_status" -gt 1 ]; then
            echo "mosquitto-bootstrap: cannot inspect $CONFIG_FILE (grep exit $grep_status); refusing to replace it" >&2
            exit 1
        fi
        needs_seed=true
    fi
fi
if [ "$needs_seed" = true ]; then
    if [ -e "$CONFIG_FILE" ] && [ ! -f "$CONFIG_FILE" ]; then
        echo "mosquitto-bootstrap: $CONFIG_FILE exists and is not a regular file; refusing to replace it" >&2
        exit 1
    fi
    if [ ! -f "$INIT_DIR/mosquitto.conf" ]; then
        echo "mosquitto-bootstrap: $INIT_DIR/mosquitto.conf not found; cannot seed broker config" >&2
        exit 1
    fi
    # Seed through a temporary file in the same directory and install it with
    # a rename. Copying straight onto the live config would let an
    # interrupted copy leave a truncated mosquitto.conf behind, which the
    # next startup reads as a managed config and preserves.
    SEED_TMP="$CONFIG_FILE.seed.$$"
    if ! cp "$INIT_DIR/mosquitto.conf" "$SEED_TMP"; then
        rm -f "$SEED_TMP" 2>/dev/null || true
        echo "mosquitto-bootstrap: failed to copy $INIT_DIR/mosquitto.conf into $CONFIG_DIR; $CONFIG_FILE left unchanged" >&2
        exit 1
    fi
    # MCM needs to read this file from inside its own container (different
    # UID); 0644 is sufficient since the file holds no secrets.
    chmod 644 "$SEED_TMP" 2>/dev/null || true
    if ! mv -f "$SEED_TMP" "$CONFIG_FILE"; then
        rm -f "$SEED_TMP" 2>/dev/null || true
        echo "mosquitto-bootstrap: failed to install $CONFIG_FILE from $INIT_DIR/mosquitto.conf" >&2
        exit 1
    fi
    echo "mosquitto-bootstrap: initialized $CONFIG_FILE from $INIT_DIR/mosquitto.conf"
fi
# Existing configs are retained when marker state is absent or ambiguous.
# Recreate the sidecar only after a config exists (seeded or preserved).
if [ ! -f "$CONFIG_MARKER" ]; then
    : > "$CONFIG_MARKER"
fi

# 2. Seed passwd if missing.
if [ ! -f "$CONFIG_DIR/passwd" ]; then
    if ! command -v mosquitto_passwd >/dev/null 2>&1; then
        echo "mosquitto-bootstrap: mosquitto_passwd not on PATH" >&2
        exit 1
    fi
    mosquitto_passwd -b -c "$CONFIG_DIR/passwd" "$DEV_USER" "$DEV_PASSWORD"
    echo "mosquitto-bootstrap: seeded $CONFIG_DIR/passwd with dev user '$DEV_USER'"
fi

# 2b. Touch the ACL file if missing. mosquitto refuses to start when
# acl_file is configured but absent; an empty file would mean "no
# per-user restrictions" in the broker's ACL implementation, but
# combined with allow_anonymous false the practical effect is that
# EVERY authenticated user (including the broker service user) is
# denied every access — including the deploy healthcheck publish and
# the broker events feed subscription that MCM and the UI rely on.
# Seed the ACL with the baseline service-user grants so the stack is
# usable on first boot. The deploy service re-emits the same block on
# every apply (see internal/deploy/service.go:serviceUserACL), so the
# bootstrap is just the initial state — not a special case.
if [ ! -f "$CONFIG_DIR/acl" ]; then
    cat > "$CONFIG_DIR/acl" <<EOF
# Seeded by deploy/mosquitto/config/mosquitto-bootstrap.sh on first
# boot. The deploy service preserves and re-emits this block on every
# apply; do not edit it manually if you want MCM to manage the broker.
user $DEV_USER
topic read #
topic read \$SYS/#
topic readwrite mcm/healthcheck
EOF
    echo "mosquitto-bootstrap: seeded $CONFIG_DIR/acl with service-user grants"
fi

LOG_DIR="/mosquitto/log"
LOG_FILE="$LOG_DIR/mosquitto.log"
mkdir -p "$LOG_DIR" 2>/dev/null || true
touch "$LOG_FILE" 2>/dev/null || true

# 3. Run the broker as the MCM service user. MCM writes passwd/acl 0600 as
#    that user on every deploy apply; the files seeded above, and the data
#    and log a broker that ran as root left on an existing volume, are
#    handed to it on every start. The log stays 0600: MCM reads it as the
#    same user, and Mosquitto warns about a world-readable log too.
if [ "$(id -u)" -eq 0 ]; then
    if ! awk -F: -v gid="$BROKER_GID" '$3 == gid { found = 1 } END { exit !found }' /etc/group; then
        addgroup -g "$BROKER_GID" mcm
    fi
    if ! awk -F: -v uid="$BROKER_UID" '$3 == uid { found = 1 } END { exit !found }' /etc/passwd; then
        adduser -D -H -S -u "$BROKER_UID" -G "$(awk -F: -v gid="$BROKER_GID" '$3 == gid { print $1; exit }' /etc/group)" mcm
    fi
    BROKER_USER="$(awk -F: -v uid="$BROKER_UID" '$3 == uid { print $1; exit }' /etc/passwd)"
    if [ "$(id -g "$BROKER_USER")" != "$BROKER_GID" ]; then
        echo "mosquitto-bootstrap: user $BROKER_USER (uid $BROKER_UID) has primary group $(id -g "$BROKER_USER"), not $BROKER_GID" >&2
        exit 1
    fi
    chown "$BROKER_UID:$BROKER_GID" "$CONFIG_DIR/passwd" "$CONFIG_DIR/acl" "$LOG_FILE"
    chmod 600 "$CONFIG_DIR/passwd" "$CONFIG_DIR/acl" "$LOG_FILE"
    chmod 755 "$LOG_DIR"
    chown -R "$BROKER_UID:$BROKER_GID" /mosquitto/data
    # Bypass /docker-entrypoint.sh — see the file header. busybox su execs
    # the shell, which execs the broker, so the broker stays PID 1: the
    # process the reload path signals with `docker exec … kill -HUP 1`.
    # The single-quoted $0 and $1 expand in the shell su starts.
    # shellcheck disable=SC2016
    exec su -s /bin/sh "$BROKER_USER" -c 'exec "$0" -c "$1"' "$BROKER_BIN" "$CONFIG_DIR/mosquitto.conf"
fi
# Already started as the broker user (or a test fixture): nothing to hand over.
exec "$BROKER_BIN" -c "$CONFIG_DIR/mosquitto.conf"
