#!/usr/bin/env bash
# backup.sh — online backup of the MCM + Mosquitto configuration (issue #295).
#
# Captures:
#   - MCM database + .bootstrap.json (the JWT secret). SQLite WAL
#     files are snapshotted via `sqlite3 .backup` for consistency —
#     no server downtime required.
#   - Mosquitto passwd + acl + mosquitto.conf (operator-edited source).
#   - Mosquitto certificates when present (config/certs/*).
#
# Skipped by default (override with --include-broker-data):
#   - Mosquitto persistence volume (can be rebuilt by replaying
#     retained messages).
#   - Mosquitto logs (regenerated on every restart).
#
# Output: a single .tgz archive containing:
#   mcm/
#     mcm.db
#     mcm.db-shm   (when present)
#     mcm.db-wal   (when present)
#     .bootstrap.json
#   mosquitto/
#     passwd
#     acl
#     mosquitto.conf
#     certs/          (when present)
#     broker-data/   (when --include-broker-data)
#   manifest.json
#
# Required: docker (compose v2), and an internet-pulling alpine:3.21
# image for the temporary SQLite + tar helper container. The container
# is `docker run --rm` so nothing persists on the host.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib-backup.sh
source "${REPO_ROOT}/scripts/lib-backup.sh"

ALPINE_IMAGE="${MCM_BACKUP_ALPINE_IMAGE:-alpine:3.21}"
STAGING_DIR=""
BACKUP_FILE=""
OPERATOR="${MCM_BACKUP_OPERATOR:-$(id -un 2>/dev/null || echo unknown)}"
GIT_COMMIT="${MCM_BACKUP_GIT_COMMIT:-$(cd "$REPO_ROOT" && git rev-parse --short HEAD 2>/dev/null || echo unknown)}"
MCM_VERSION="${MCM_BACKUP_MCM_VERSION:-$(cd "$REPO_ROOT" && git describe --tags --always 2>/dev/null || echo unknown)}"
INCLUDE_BROKER_DATA=false
SKIP_MOSQUITTO=false

usage() {
    cat <<EOF
Usage: $0 [flags] <output.tar.gz>

Produces a recovery archive that restores.sh can load into an empty
deployment. Capture is online (no MCM downtime); the MCM server keeps
serving traffic while the backup runs.

Flags:
  --include-broker-data   Also backup mosquitto_data volume
                         (persistence; can be large).
  --skip-mosquitto        Skip the mosquitto_config backup (MCM only).
  --operator NAME         Operator tag for the manifest. Defaults to
                         the current unix username.
  --alpine-image IMAGE    Override the alpine image used by the
                         helper container. Default: $ALPINE_IMAGE
  --help                 Print this message and exit.

Output:
  <output.tar.gz>        Path to the archive to write. Use '-' for
                         stdout (used by tests).

The script writes to <output>.tgz-or-<output>.tar.gz depending on the
suffix you pass; the path is created if it does not exist.
EOF
}

# --- arg parsing -------------------------------------------------------------

while [ "$#" -gt 0 ]; do
    case "$1" in
        --include-broker-data)   INCLUDE_BROKER_DATA=true; shift ;;
        --skip-mosquitto)        SKIP_MOSQUITTO=true;  shift ;;
        --operator)              OPERATOR="$2"; shift 2 ;;
        --operator=*)            OPERATOR="${1#*=}"; shift ;;
        --alpine-image)          ALPINE_IMAGE="$2"; shift 2 ;;
        --alpine-image=*)        ALPINE_IMAGE="${1#*=}"; shift ;;
        --help|-h)               usage; exit 0 ;;
        --*)                     echo "backup.sh: unknown flag $1" >&2; usage >&2; exit 2 ;;
        *)
            if [ -z "$BACKUP_FILE" ]; then BACKUP_FILE="$1"; shift
            else echo "backup.sh: unexpected positional arg $1" >&2; usage >&2; exit 2
            fi
            ;;
    esac
done

if [ -z "$BACKUP_FILE" ]; then
    usage >&2
    exit 2
fi

# --- helpers ------------------------------------------------------------------

mcm_data_volume() {
    compose_volume_name "$REPO_ROOT" "mcm_data"
}

mosquitto_config_volume() {
    compose_volume_name "$REPO_ROOT" "mosquitto_config"
}

mosquitto_data_volume() {
    compose_volume_name "$REPO_ROOT" "mosquitto_data"
}

# create_tmp_staging creates a private staging dir. Cleanup is wired to
# trap so a failure mid-backup does not leak /tmp content.
create_tmp_staging() {
    STAGING_DIR="$(mktemp -d -t mcm-backup-XXXXXX)"
    trap 'rm -rf "$STAGING_DIR"' EXIT
}

# run_alpine_helper mounts the given volume (read-only by default) and
# runs the supplied command inside a fresh alpine container. The
# container is removed automatically (`--rm`). stdout is the helper's
# stdout; non-zero exit propagates.
run_alpine_helper() {
    local volume="$1" mount_target="$2"; shift 2
    docker run --rm \
        -v "${volume}:${mount_target}:ro" \
        "$ALPINE_IMAGE" \
        "$@"
}

# run_alpine_helper_rw is like run_alpine_helper but mounts the volume
# read-write so the helper can write into it (used to stream the
# sqlite3 .backup output directly into the staging dir).
run_alpine_helper_rw() {
    local volume="$1" mount_target="$2"; shift 2
    docker run --rm \
        -v "${volume}:${mount_target}" \
        "$ALPINE_IMAGE" \
        "$@"
}

# backup_sqlite_snapshot uses sqlite3 .backup to produce a consistent
# copy of the live database. The driver in MCM holds a single
# connection (SetMaxOpenConns(1)) so the .backup grabs a brief
# exclusive lock and yields as soon as the copy is on disk. No server
# downtime required.
backup_sqlite_snapshot() {
    local src_volume="$1" out_path="$2"
    local tmpdir
    tmpdir="$(mktemp -d)"
    docker run --rm \
        -v "${src_volume}:/data" \
        -v "${tmpdir}:/backup" \
        "$ALPINE_IMAGE" \
        sh -c "
            if ! command -v sqlite3 >/dev/null 2>&1; then
                apk add --no-cache sqlite >/dev/null 2>&1 || {
                    echo 'sqlite3 CLI not found and apk install failed' >&2
                    exit 1
                }
            fi
            sqlite3 /data/mcm.db \".timeout 5000\" '.backup /backup/mcm.db'
            cp -p /data/mcm.db-shm /backup/mcm.db-shm 2>/dev/null || true
            cp -p /data/mcm.db-wal /backup/mcm.db-wal 2>/dev/null || true
            if [ -f /data/.bootstrap.json ]; then
                # Copy and then chmod 0644 so the host user running
                # backup.sh can sha256 the file for the manifest. The
                # restore step re-chmods 0600 on the destination
                # volume; the staged 0644 is only metadata.
                cp -p /data/.bootstrap.json /backup/.bootstrap.json
                chmod 0644 /backup/.bootstrap.json
            fi
            echo 'sqlite3 .backup completed'
        "
    cp -p "${tmpdir}/mcm.db" "$out_path"
    [ -f "${tmpdir}/mcm.db-shm" ] && cp -p "${tmpdir}/mcm.db-shm" "${out_path%.*}.db-shm" || true
    [ -f "${tmpdir}/mcm.db-wal" ] && cp -p "${tmpdir}/mcm.db-wal" "${out_path%.*}.db-wal" || true
    if [ -f "${tmpdir}/.bootstrap.json" ]; then
        cp -p "${tmpdir}/.bootstrap.json" "${out_path%/*}/.bootstrap.json"
        chmod 0644 "${out_path%/*}/.bootstrap.json"
    fi
    rm -rf "$tmpdir"
}

# backup_volume_dir walks the given volume at mount_target and copies
# its content (preserving relative paths) into the staging dir.
backup_volume_dir() {
    local volume="$1" mount_target="$2" dest_relpath="$3"
    docker run --rm \
        -v "${volume}:${mount_target}:ro" \
        -v "${STAGING_DIR}:/staging" \
        "$ALPINE_IMAGE" \
        sh -c "
            mkdir -p /staging/${dest_relpath}
            # tar preserves perms, ownership, mtime. -C strips the mount
            # prefix so the staging tree mirrors the volume layout.
            tar cf - -C '${mount_target}' . | tar xf - -C /staging/${dest_relpath}
        "
}

# backup_mosquitto_files copies the documented broker side of the
# captured config (passwd / acl / mosquitto.conf / optional certs/)
# into the staging dir under mosquitto/. Other operator-added files
# in the volume are intentionally NOT copied — they are not part of
# the documented recovery set and including them opens the door to
# silently backing up stale or unknown state.
backup_mosquitto_files() {
    local volume="$1"
    docker run --rm \
        -v "${volume}:/data:ro" \
        -v "${STAGING_DIR}:/staging" \
        "$ALPINE_IMAGE" \
        sh -c "
            mkdir -p /staging/mosquitto
            for f in passwd acl mosquitto.conf; do
                if [ -f /data/\$f ]; then
                    cp -p /data/\$f /staging/mosquitto/\$f
                    # Staging dir is host-mounted; the copy was made as
                    # root inside the container. Loosen the mode so the
                    # host user (and restore.sh inside another container)
                    # can read it.
                    chmod 0644 /staging/mosquitto/\$f
                fi
            done
            if [ -d /data/certs ]; then
                cp -rp /data/certs /staging/mosquitto/certs
                find /staging/mosquitto/certs -type f -exec chmod 0644 {} +
                find /staging/mosquitto/certs -type d -exec chmod 0755 {} +
            fi
        "
}

# backup_mosquitto_data copies the broker persistence volume wholesale.
backup_mosquitto_data() {
    local volume="$1"
    backup_volume_dir "$volume" "/data" "mosquitto-data"
}

# --- main ---------------------------------------------------------------------

create_tmp_staging
mkdir -p "${STAGING_DIR}/mcm" "${STAGING_DIR}/mosquitto"

echo "backup.sh: resolving volumes …"
MCM_VOLUME="$(mcm_data_volume)"
MOSQ_VOLUME="$(mosquitto_config_volume)"
echo "  MCM data volume      → $MCM_VOLUME"
echo "  Mosquitto config vol → $MOSQ_VOLUME"

echo "backup.sh: snapshotting SQLite (online, no downtime) …"
backup_sqlite_snapshot "$MCM_VOLUME" "${STAGING_DIR}/mcm/mcm.db"

echo "backup.sh: collecting MCM bootstrap state …"
if [ ! -f "${STAGING_DIR}/mcm/.bootstrap.json" ]; then
    # sqlite3 helper may not have copied it (older volumes without
    # .bootstrap.json, or the file permission was wrong). Try a plain
    # copy via a fresh helper.
    docker run --rm \
        -v "${MCM_VOLUME}:/data:ro" \
        -v "${STAGING_DIR}:/staging" \
        "$ALPINE_IMAGE" \
        sh -c "
            if [ -f /data/.bootstrap.json ]; then
                cp -p /data/.bootstrap.json /staging/mcm/.bootstrap.json
                echo 'bootstrap copied'
            else
                echo 'no .bootstrap.json in volume (will be regenerated on first boot)'
            fi
        "
fi

if [ "$SKIP_MOSQUITTO" != true ]; then
    echo "backup.sh: collecting Mosquitto configuration …"
    backup_mosquitto_files "$MOSQ_VOLUME"
fi

if [ "$INCLUDE_BROKER_DATA" = true ]; then
    echo "backup.sh: collecting broker persistence (--include-broker-data) …"
    MOSQ_DATA_VOLUME="$(mosquitto_data_volume)"
    backup_mosquitto_data "$MOSQ_DATA_VOLUME"
fi

echo "backup.sh: writing manifest …"
# Build a single manifest that includes every file we created PLUS
# the manifest itself (so restore can verify integrity including the
# manifest). The trick: stage a temporary manifest that already
# contains the sha256 of the FINAL manifest content. The actual
# final manifest is written to a temp path first, then sha256'd,
# then re-rendered with that hash, then moved into place.
TMP_MANIFEST="${STAGING_DIR}/.manifest.tmp"
FINAL_MANIFEST="${STAGING_DIR}/manifest.json"

# Pass 1: write everything except the manifest itself. Capture the
# JSON content directly from write_manifest's stdout (the path it
# prints) by sending the call's stdout to /dev/null and reading the
# file we just wrote.
write_manifest "$STAGING_DIR" \
    "operator=$OPERATOR" \
    "git_commit=$GIT_COMMIT" \
    "mcm_version=$MCM_VERSION" \
    "include_broker_data=$INCLUDE_BROKER_DATA" \
    --file "${STAGING_DIR}/mcm/mcm.db"            --label "mcm/mcm.db" \
    --file "${STAGING_DIR}/mcm/mcm.db-shm"       --label "mcm/mcm.db-shm" \
    --file "${STAGING_DIR}/mcm/mcm.db-wal"       --label "mcm/mcm.db-wal" \
    --file "${STAGING_DIR}/mcm/.bootstrap.json"  --label "mcm/.bootstrap.json" \
    --file "${STAGING_DIR}/mosquitto/passwd"      --label "mosquitto/passwd" \
    --file "${STAGING_DIR}/mosquitto/acl"         --label "mosquitto/acl" \
    --file "${STAGING_DIR}/mosquitto/mosquitto.conf"  --label "mosquitto/mosquitto.conf" \
    > /dev/null
mv "${STAGING_DIR}/manifest.json" "$TMP_MANIFEST"

# Pass 2: include the manifest.json entry too. Compute the sha256 of
# the Pass-1 manifest content (which is now at $TMP_MANIFEST) and
# rewrite the manifest in place to include its own entry. The Pass-1
# manifest ends with a single "}" on its own line; we replace it with
# a trailing comma + the manifest entry + the closing brace.
MANIFEST_SHA="$(sha256_file "$TMP_MANIFEST")"
MANIFEST_SIZE="$(stat -c %s "$TMP_MANIFEST")"
# Use awk for the splice so the multi-line replacement is explicit
# (sed's `\n` in the replacement only works on GNU sed with the right
# flags, and the pattern is fragile).
awk -v sha="$MANIFEST_SHA" -v size="$MANIFEST_SIZE" '
    /^}$/ && !inserted {
        print "  , \"manifest.json\": { \"sha256\": \"" sha "\", \"size\": " size " }"
        inserted = 1
    }
    { print }
    END { if (!inserted) { print "  , \"manifest.json\": { \"sha256\": \"" sha "\", \"size\": " size " }" } }
' "$TMP_MANIFEST" > "$FINAL_MANIFEST"
rm -f "$TMP_MANIFEST"

echo "backup.sh: packaging …"
if [ "$BACKUP_FILE" = "-" ]; then
    tar czf - -C "$STAGING_DIR" .
else
    mkdir -p "$(dirname "$BACKUP_FILE")"
    tar czf "$BACKUP_FILE" -C "$STAGING_DIR" .
    echo "backup.sh: wrote $BACKUP_FILE"
fi
