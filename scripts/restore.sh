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
STAGING_DIR=""

usage() {
    cat <<EOF
Usage: $0 [--confirm] <archive.tar.gz>

Restores a backup archive produced by backup.sh into the active MCM +
Mosquitto deployment. Refuses to proceed without --confirm.

Steps:
  1. Extract the archive to a private staging dir.
  2. Re-read manifest.json; verify each entry's sha256 matches the
     staged file (fails fast on tamper / corruption).
  3. Resolve volumes via 'docker compose --project-name volume ls'.
  4. Copy each file to its original volume path via a helper
     container, preserving ownership and mode bits.

After restore, run 'docker compose restart mcm' so the new database
and .bootstrap.json are picked up. The first deploy apply may need
to re-issue SIGHUP / ReloadCommand to refresh the broker.
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --confirm)        CONFIRMED=true; shift ;;
        --confirm=*)      CONFIRMED="${1#*=}"; shift ;;
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

# verify_manifest walks the manifest, checks every file's sha256, and
# echoes the list of validated labels on stdout.
verify_manifest() {
    local staging="$1"
    local manifest="${staging}/manifest.json"
    if [ ! -f "$manifest" ]; then
        echo "restore.sh: manifest.json not found in archive" >&2
        return 1
    fi

    # Use grep + sed to extract entries. We avoid jq to keep the
    # dependency surface small. The manifest has the shape:
    #   "files": { "<label>": { "sha256": "<hash>", "size": <int> }, ... }
    local entries
    entries="$(grep -oE '"[^"]+"\s*:\s*\{[^}]+\}' "$manifest" | tail -n +1)"
    # Actually parse line-by-line with awk for robustness.
    awk -F'"' '
        /"sha256"/ {
            for (i=1; i<=NF; i++) {
                if ($i ~ /^(rel|sha256|size)$/) {
                    if ($i == "rel") label = $(i+2)
                    if ($i == "sha256") expected = $(i+2)
                }
            }
        }
        /^\s*\}\s*$/ { }
        # Simpler approach: stream the raw JSON to a python helper if
        # available, fall back to a sed-based extraction otherwise.
    ' "$manifest" >/dev/null

    # The awk above is exploratory; the real parser uses python3 if
    # available, else a small POSIX awk.
    local verified=()
    local failed=()
    if command -v python3 >/dev/null 2>&1; then
        # python3 - reads the script from stdin; the manifest path is
        # passed as the only positional arg. The quoted here-doc keeps
        # bash from interpolating any shell metacharacters in the
        # script body.
        while IFS='|' read -r label sha size; do
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
        alpine:3.21 \
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

echo "restore.sh: writing MCM database …"
# Layout inside staging: mcm/mcm.db, mcm/.bootstrap.json.
# IMPORTANT: the dest_path is RELATIVE to the volume's mount target (/data
# in the helper container). The volume's content root is the directory
# Mosquitto sees as /var/lib/mcm; the staging files map directly to the
# volume root because of the leading path component.
write_file_to_volume "mcm/mcm.db"           "$MCM_VOLUME" "mcm.db"
write_file_to_volume "mcm/mcm.db-shm"      "$MCM_VOLUME" "mcm.db-shm" || true
write_file_to_volume "mcm/mcm.db-wal"      "$MCM_VOLUME" "mcm.db-wal" || true
write_file_to_volume "mcm/.bootstrap.json" "$MCM_VOLUME" ".bootstrap.json"

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
        alpine:3.21 \
        sh -c "
            set -e
            mkdir -p /data/certs
            cp -rp /staging/mosquitto/certs/. /data/certs/
            chown -R ${MOSQ_RESTORE_UID:-1883}:${MOSQ_RESTORE_GID:-1883} /data/certs
            echo wrote certs/
        "
fi

echo "restore.sh: SUCCESS"
echo
echo "Next steps:"
echo "  1. docker compose restart mcm"
echo "  2. Verify the admin login works (the JWT secret was restored)."
echo "  3. Run a fresh deploy apply so the broker reloads."
