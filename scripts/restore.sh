#!/usr/bin/env bash
# restore.sh — restore a backup archive into the current MCM deployment
# (issue #295).
#
# Validates the manifest's sha256 against each file, then writes the
# files back to their original Docker volume paths via a helper
# container. Refuses to proceed if any file's sha256 mismatches.
#
# The legacy Taskfile recipe had three defects, all addressed here:
#   1. archive contained data/... and extracted into /data, producing
#      /data/data/... (broken path). restore.sh puts each file at its
#      original volume path.
#   2. hardcoded mcm_mcm_data; this script resolves the volume via
#      `docker compose --project-name volume ls` so any project name
#      works.
#   3. SQLite copied without coordinating a consistent snapshot;
#      backup.sh now uses `sqlite3 .backup` for consistency.
#
# Usage: restore.sh <archive.tar.gz> [--confirm]

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=lib-backup.sh
source "${REPO_ROOT}/scripts/lib-backup.sh"

ARCHIVE=""
CONFIRMED=false
OVERWRITE=false
STAGING_DIR=""
ALPINE_IMAGE="${MCM_BACKUP_ALPINE_IMAGE:-alpine:3.21}"

usage() {
    cat <<EOF
Usage: $0 [--confirm] [--overwrite] <archive.tar.gz>

Restores a backup archive produced by backup.sh into the active MCM +
Mosquitto deployment. Refuses to proceed without --confirm.

--overwrite is additionally required when the destination volumes
already contain data (an existing mcm.db, passwd or acl): restoring
replaces them, and the script refuses to destroy existing data on a
mere --confirm. Bootstrapping a fresh stack and then restoring over
the just-created empty database is the intended --overwrite case.

Steps:
  1. Extract the archive to a private staging dir.
  2. Re-read manifest.json; verify each entry's sha256 matches the
     staged file (fails fast on tamper / corruption).
  3. Resolve volumes via 'docker compose --project-name volume ls'.
  4. Refuse to replace existing data unless --overwrite is given.
  5. Copy each file to its original volume path via a helper
     container, preserving ownership and mode bits.
  6. VERIFY the restored database content: SQLite integrity_check
     plus a non-empty schema read-back (issue #295, "verificar
     contenido").

After restore, run 'docker compose restart mcm' so the new database
and .bootstrap.json are picked up. The first deploy apply may need
to re-issue SIGHUP / ReloadCommand to refresh the broker.
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --confirm)        CONFIRMED=true; shift ;;
        --confirm=*)      CONFIRMED="${1#*=}"; shift ;;
        --overwrite)      OVERWRITE=true; shift ;;
        --overwrite=*)    OVERWRITE="${1#*=}"; shift ;;
        --help|-h)         usage; exit 0 ;;
        --*)               echo "restore.sh: unknown flag $1" >&2; usage >&2; exit 2 ;;
        *)
            if [ -z "$ARCHIVE" ]; then ARCHIVE="$1"; shift
            else echo "restore.sh: unexpected positional arg $1" >&2; usage >&2; exit 2
            fi
            ;;
    esac
done

if [ -z "$ARCHIVE" ] || [ "$CONFIRMED" != true ]; then
    if [ "$CONFIRMED" != true ]; then
        echo "restore.sh: refusing to restore without --confirm" >&2
    fi
    usage >&2
    exit 2
fi

if [ ! -f "$ARCHIVE" ]; then
    echo "restore.sh: archive not found: $ARCHIVE" >&2
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

# volume_has_files <volume>
# Reports (exit 0) when any file already exists in the volume. Used by
# the --overwrite guard so a restore never destroys data missed by a
# small hardcoded list of filenames (e.g. certificates only).
volume_has_files() {
    local volume="$1"
    docker run --rm \
        -v "${volume}:/data:ro" \
        "$ALPINE_IMAGE" \
        sh -c 'find /data -type f -print -quit | grep -q .'
}

# verify_restored_db reads the restored database back: SQLite
# integrity_check must report ok and the schema must contain tables.
# This is the "verificar contenido" half of issue #295's first
# criterion — the manifest sha256 proves transport integrity, this
# proves the file is a usable database.
verify_restored_db() {
    local volume="$1"
    docker run --rm \
        -v "${volume}:/data:ro" \
        "$ALPINE_IMAGE" \
        sh -c '
            if ! command -v sqlite3 >/dev/null 2>&1; then
                apk add --no-cache sqlite >/dev/null 2>&1 || {
                    echo "sqlite3 CLI not found and apk install failed" >&2
                    exit 1
                }
            fi
            # The .backup artifact preserves WAL mode; immutable=1
            # prevents SQLite from trying to create a -shm file on the
            # read-only mounted volume. The snapshot has no concurrent
            # writer, so immutable reads are correct here.
            db="file:/data/mcm.db?mode=ro&immutable=1"
            result="$(sqlite3 "$db" "PRAGMA integrity_check;")"
            if [ "$result" != "ok" ]; then
                echo "restore.sh: integrity_check failed: $result" >&2
                exit 1
            fi
            tables="$(sqlite3 "$db" "SELECT count(*) FROM sqlite_master;")"
            if [ "$tables" -eq 0 ] 2>/dev/null; then
                echo "restore.sh: restored database has an empty schema" >&2
                exit 1
            fi
            echo "restore.sh: restored mcm.db verified (integrity ok, $tables schema objects)"
        '
}

# verify_manifest walks the manifest, checks every file's sha256, and
# echoes the list of validated labels on stdout.
verify_manifest() {
    local staging="$1"
    local manifest="${staging}/manifest.json"
    if [ ! -f "$manifest" ]; then
        echo "restore.sh: manifest.json not found in archive" >&2
        return 1
    fi

    # Parse with python3 when available (robust against key order and
    # whitespace); fall back to a sed-based extraction that matches
    # write_manifest's one-entry-per-line layout otherwise.
    local verified=()
    local failed=()
    if command -v python3 >/dev/null 2>&1; then
        # python3 - reads the script from stdin; the manifest path is
        # passed as the only positional arg. The quoted here-doc keeps
        # bash from interpolating any shell metacharacters in the
        # script body.
        while IFS='|' read -r label sha _size; do
            [ -z "$label" ] && continue
            local actual
            actual="$(sha256_file "$staging/$label" 2>/dev/null || echo MISSING)"
            if [ "$actual" != "$sha" ]; then
                failed+=("$label (expected $sha, got $actual)")
            else
                verified+=("$label")
            fi
        done < <(python3 - "$manifest" <<'PYTHON_EOF'
import json, sys
m = json.load(open(sys.argv[1]))
for k, v in m.get("files", {}).items():
    print(f"{k}|{v['sha256']}|{v['size']}")
PYTHON_EOF
        )
    else
        # POSIX fallback: assume manifest ordered as written by
        # write_manifest; entries are key, sha256, size in that order.
        while IFS= read -r line; do
            [ -z "$line" ] && continue
            label="$(echo "$line" | sed -n 's/.*"\([^"]*\)": { "sha256": "\([^"]*\)".*/\1/p')"
            sha="$(echo "$line" | sed -n 's/.*"sha256": "\([^"]*\)".*/\1/p')"
            [ -z "$label" ] || [ -z "$sha" ] && continue
            local actual
            actual="$(sha256_file "$staging/$label" 2>/dev/null || echo MISSING)"
            if [ "$actual" != "$sha" ]; then
                failed+=("$label (expected $sha, got $actual)")
            else
                verified+=("$label")
            fi
        done < <(grep -oE '"[^"]+":\s*\{\s*"sha256":\s*"[^"]+"' "$manifest")
    fi

    if [ "${#failed[@]}" -gt 0 ]; then
        echo "restore.sh: sha256 verification failed for:" >&2
        for f in "${failed[@]}"; do
            echo "  - $f" >&2
        done
        return 1
    fi
    echo "restore.sh: manifest verified (${#verified[@]} files)"
}

# write_file_to_volume extracts a single file from the staging into the
# destination path inside the named volume. Uses a small alpine helper
# with the staging mounted read-only and the destination volume mounted
# read-write; ownership/mode bits are set explicitly so the MCM process
# (which runs as UID 100 in the alpine-based image — see Dockerfile
# `RUN adduser -S mcm`) can read the restored files. Without this the
# host docker user's UID (typically 1000) would own the files and MCM
# would fail with "permission denied" on startup.
write_file_to_volume() {
    local staging_relpath="$1" volume="$2" dest_path="$3"
    local abs_source="${STAGING_DIR}/${staging_relpath}"
    if [ ! -f "$abs_source" ]; then
        echo "restore.sh: missing staged file $abs_source" >&2
        return 1
    fi

    local mode="${4:-0600}"
    # MCM's UID:GID inside the alpine-based image (see Dockerfile).
    local owner="${MCM_RESTORE_UID:-100}:${MCM_RESTORE_GID:-101}"
    docker run --rm \
        -v "${STAGING_DIR}:/staging:ro" \
        -v "${volume}:/data" \
        "$ALPINE_IMAGE" \
        sh -c "
            set -e
            mkdir -p \"\$(dirname '/data/${dest_path}')\"
            cp -p '/staging/${staging_relpath}' '/data/${dest_path}'
            chmod ${mode} '/data/${dest_path}'
            chown ${owner} '/data/${dest_path}'
            echo wrote '/data/${dest_path}' mode=${mode} owner=${owner}
        "
}

# --- main ---------------------------------------------------------------------

STAGING_DIR="$(mktemp -d -t mcm-restore-XXXXXX)"
trap 'rm -rf "$STAGING_DIR"' EXIT

echo "restore.sh: extracting $ARCHIVE …"
tar xzf "$ARCHIVE" -C "$STAGING_DIR"
echo "restore.sh: extracted $(du -sh "$STAGING_DIR" | awk '{print $1}')"

echo "restore.sh: verifying manifest …"
verify_manifest "$STAGING_DIR"

echo "restore.sh: resolving volumes …"
MCM_VOLUME="$(mcm_data_volume)"
MOSQ_VOLUME="$(mosquitto_config_volume)"
echo "  MCM data volume      → $MCM_VOLUME"
echo "  Mosquitto config vol → $MOSQ_VOLUME"

# A consistent database restore must not race the running MCM process.
# Require an explicit stop instead of silently replacing a live SQLite
# file (the user already confirmed destructive overwrite separately).
if ! RUNNING_SERVICES="$(cd "$REPO_ROOT" && docker compose ps --status running --services 2>/dev/null)"; then
    echo "restore.sh: cannot determine whether MCM is running; refusing to restore SQLite" >&2
    exit 2
fi
if printf '%s\n' "$RUNNING_SERVICES" | grep -qx 'mcm'; then
    echo "restore.sh: MCM is running; stop it with 'docker compose stop mcm' before restoring SQLite" >&2
    exit 2
fi

# Guard (issue #295, "sin pérdida accidental de datos"): restoring
# replaces the destination files; refuse to destroy existing data
# unless --overwrite was given in addition to --confirm. Restoring
# over a freshly bootstrapped empty stack is the intended --overwrite
# case.
if volume_has_files "$MCM_VOLUME" || volume_has_files "$MOSQ_VOLUME"; then
    if [ "$OVERWRITE" != true ]; then
        echo "restore.sh: destination volumes already contain data." >&2
        echo "restore.sh: re-run with --overwrite to replace it (this replaces the current state)." >&2
        exit 2
    fi
    echo "restore.sh: --overwrite given; replacing existing destination data"
fi

# If the archive contains broker persistence, extend the overwrite guard
# to that separately named volume too.
if [ -d "${STAGING_DIR}/mosquitto-data" ]; then
    MOSQ_DATA_VOLUME="$(mosquitto_data_volume)"
    if volume_has_files "$MOSQ_DATA_VOLUME" && [ "$OVERWRITE" != true ]; then
        echo "restore.sh: broker persistence volume already contains data." >&2
        echo "restore.sh: re-run with --overwrite to replace it (this replaces the current state)." >&2
        exit 2
    fi
fi

# Stale WAL/SHM sidecar files from older archives are ignored on
# purpose: the sqlite3 .backup snapshot is self-contained, and a
# WAL/SHM from a different point in time can corrupt the restored
# database (see backup.sh).
for stale in mcm/mcm.db-shm mcm/mcm.db-wal; do
    if [ -f "${STAGING_DIR}/${stale}" ]; then
        echo "restore.sh: ignoring stale sidecar ${stale} from the archive"
    fi
done

echo "restore.sh: writing MCM database …"
# The snapshot is self-contained (sqlite3 .backup); remove any WAL/SHM
# left by the empty bootstrap database before installing it. MCM must be
# stopped for this step (see docs/production.md and e2e-recovery.sh), so
# no process can recreate or append to these sidecars mid-restore.
docker run --rm -v "${MCM_VOLUME}:/data" "$ALPINE_IMAGE" sh -c 'rm -f /data/mcm.db-wal /data/mcm.db-shm'
# Layout inside staging: mcm/mcm.db, mcm/.bootstrap.json.
# IMPORTANT: the dest_path is RELATIVE to the volume's mount target (/data
# in the helper container). The volume's content root is the directory
# Mosquitto sees as /var/lib/mcm; the staging files map directly to the
# volume root because of the leading path component.
write_file_to_volume "mcm/mcm.db"           "$MCM_VOLUME" "mcm.db"
# .bootstrap.json may be absent from the archive (older volumes); MCM
# regenerates it on first boot, so its absence is not fatal.
if ! write_file_to_volume "mcm/.bootstrap.json" "$MCM_VOLUME" ".bootstrap.json"; then
    echo "restore.sh: no .bootstrap.json in archive; MCM will regenerate it on first boot"
fi

echo "restore.sh: verifying restored database content …"
verify_restored_db "$MCM_VOLUME"

echo "restore.sh: writing Mosquitto configuration …"
# Same rule as the MCM database: dest_path is RELATIVE to the
# volume's mount target. The volume is mounted at /mosquitto/config in
# the broker container, so the file paths inside the volume are
# exactly the broker-side paths (no leading "config/" prefix).
write_file_to_volume "mosquitto/passwd"          "$MOSQ_VOLUME" "passwd" || true
write_file_to_volume "mosquitto/acl"             "$MOSQ_VOLUME" "acl" || true
write_file_to_volume "mosquitto/mosquitto.conf"  "$MOSQ_VOLUME" "mosquitto.conf" || true

# Certificates directory (whole tree, if present). Owned by the
# mosquitto UID (1883 by default for the eclipse-mosquitto image) so
# the broker can read them. The volume's content root is the broker's
# /mosquitto/config, so certs go directly under that root.
if [ -d "${STAGING_DIR}/mosquitto/certs" ]; then
    echo "restore.sh: writing broker certificates …"
    docker run --rm \
        -v "${STAGING_DIR}:/staging:ro" \
        -v "${MOSQ_VOLUME}:/data" \
        "$ALPINE_IMAGE" \
        sh -c "
            set -e
            mkdir -p /data/certs
            find /data/certs -mindepth 1 -delete
            cp -rp /staging/mosquitto/certs/. /data/certs/
            chown -R ${MOSQ_RESTORE_UID:-1883}:${MOSQ_RESTORE_GID:-1883} /data/certs
            echo wrote certs/
        "
fi

# Broker persistence (only present when the archive was taken with
# --include-broker-data). Issue #295: the manifest covers it, so the
# restore must too.
if [ -d "${STAGING_DIR}/mosquitto-data" ]; then
    echo "restore.sh: writing broker persistence …"
    MOSQ_DATA_VOLUME="$(mosquitto_data_volume)"
    docker run --rm \
        -v "${STAGING_DIR}:/staging:ro" \
        -v "${MOSQ_DATA_VOLUME}:/data" \
        "$ALPINE_IMAGE" \
        sh -c '
            set -e
            mkdir -p /data
            find /data -mindepth 1 -delete
            cp -rp /staging/mosquitto-data/. /data/
            echo wrote broker persistence
        '
fi

echo "restore.sh: SUCCESS"
echo
echo "Next steps:"
echo "  1. docker compose restart mcm"
echo "  2. Verify the admin login works (the JWT secret was restored)."
echo "  3. Run a fresh deploy apply so the broker reloads."
