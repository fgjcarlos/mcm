#!/usr/bin/env bash
# lib-backup.sh — shared helpers for backup.sh / restore.sh (issue #295).
#
# Conventions:
#   - Pure bash helpers; does NOT touch docker directly except for the
#     compose_volume_name resolver which wraps `docker compose … volume ls`.
#   - All functions are idempotent and re-entrant.
#   - All paths are taken from $REPO_ROOT or set explicitly via env vars
#     (never hard-coded).
#
# Sourced by backup.sh / restore.sh. Also sourced by the unit tests at
# scripts/tests/test-lib-backup.sh.

# Avoid double-sourcing.
if [ -n "${LIB_BACKUP_LOADED:-}" ]; then
    return 0
fi
LIB_BACKUP_LOADED=1

# Bail out on errors / unset vars / failed pipes in anything that sources us.
# Individual helper functions still avoid `set -e` internally so callers can
# inspect failure paths explicitly.

# ---
# compose_project_name <repo-root>
#   Resolve the Docker Compose project name for this checkout. Honours
#   $COMPOSE_PROJECT_NAME when set; otherwise falls back to the basename
#   of the repo root (which is what `docker compose` itself does).
compose_project_name() {
    local repo_root="${1:-$(pwd)}"
    if [ -n "${COMPOSE_PROJECT_NAME:-}" ]; then
        printf '%s\n' "$COMPOSE_PROJECT_NAME"
    else
        basename "$repo_root"
    fi
}

# ---
# compose_volume_name <repo-root> <volume-key>
#
# Resolves the *named* volume for a given volume key declared in the
# Compose file (e.g. "mcm_data"). Runs `docker compose --project-name
# NAME volume ls --format '{{.Name}}'` and filters by the volume key.
#
# The Compose `volume ls` output includes both service-scoped bind mounts
# (which we ignore) and the named volumes from the `volumes:` block. We
# match volumes that end with `_${key}` to be robust to project names
# with hyphens (Compose replaces non-alphanumerics with underscores).
#
# Falls back to `<project>_<key>` when docker is unavailable — matching
# the standard prefix Compose uses for non-overridden projects.
compose_volume_name() {
    local repo_root="${1:-$(pwd)}" volume_key="${2:?compose_volume_name requires volume key}"
    local project_name
    project_name="$(compose_project_name "$repo_root")"

    local resolved=""
    if command -v docker >/dev/null 2>&1; then
        # `docker compose --project-name NAME volume ls` does not honour
        # `--format` across all compose v2 versions; fall back to plain
        # text parsing if needed.
        resolved="$(cd "$repo_root" && docker compose --project-name "$project_name" volume ls 2>/dev/null \
            | awk '{print $2}' \
            | grep -E "_${volume_key}\$" \
            | head -n1 || true)"
    fi

    if [ -z "$resolved" ]; then
        resolved="${project_name}_${volume_key}"
        echo "lib-backup: WARN docker unavailable; assuming volume name $resolved" >&2
    fi
    printf '%s\n' "$resolved"
}

# ---
# sha256_file <path>
#
# Returns the hex SHA-256 digest of `path`. Empty path or missing file
# produces empty output (do not call with both unset).
sha256_file() {
    local f="${1:?sha256_file requires path}"
    if [ ! -f "$f" ]; then
        echo "lib-backup: sha256_file: missing $f" >&2
        return 1
    fi
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$f" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$f" | awk '{print $1}'
    else
        echo "lib-backup: sha256_file: no sha256sum or shasum available" >&2
        return 1
    fi
}

# ---
# normalize_archive_target_dir <path>
#
# Strip a single trailing slash so volume mount paths canonicalize
# uniformly (Docker occasionally reports them either way).
normalize_archive_target_dir() {
    local p="${1:-}"
    # Strip exactly one trailing slash, but never the root.
    if [ -z "$p" ]; then
        printf '%s\n' ""
        return 0
    fi
    p="${p%/}"
    if [ -z "$p" ]; then
        printf '%s\n' "/"
        return 0
    fi
    printf '%s\n' "$p"
}

# ---
# write_manifest <staging-dir> <key=value>... --file <path> --label <relpath>
#                       [--file <path> --label <relpath>]...
#
# Walks the staging directory, writes each labeled file's sha256 to
# `manifest.json`. Manifest shape:
#   {
#     "created_at": "...",
#     "operator": "...",
#     "mcm_version": "...",
#     "git_commit": "...",
#     "project_name": "...",
#     "files": { "<relpath>": { "size": N, "sha256": "..." }, ... }
#   }
#
# Writes the manifest to <staging>/manifest.json. Captures the JSON
# in the variable MANIFEST_JSON for callers that need to compute the
# sha256 of the final manifest (when including the manifest itself as
# one of the file entries). The function also prints the manifest path
# to stdout for backward-compat with the test that expects a path.
write_manifest() {
    local staging="$1"
    shift

    local created_at
    created_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

    local entries_json=""
    local key_value_pairs=""
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --file)
                local fpath="$2"
                shift 3
                local label="$1"
                shift
                local sha size rel
                rel="$(normalize_archive_target_dir "$label")"
                sha="$(sha256_file "$fpath")"
                size="$(stat -c %s "$fpath" 2>/dev/null || wc -c < "$fpath")"
                if [ -n "$entries_json" ]; then entries_json="$entries_json, "; fi
                entries_json="${entries_json}\"$rel\": {\"sha256\": \"$sha\", \"size\": $size}"
                ;;
            --*=*|*=*)
                if [ -n "$key_value_pairs" ]; then key_value_pairs="$key_value_pairs, "; fi
                # Strip leading "--" if present.
                local kv="${1#--}"
                key_value_pairs="${key_value_pairs}\"${kv%%=*}\": \"${kv#*=}\""
                shift
                ;;
            *)
                shift
                ;;
        esac
    done

    local manifest_path="${staging}/manifest.json"
    {
        printf '{\n'
        printf '  "created_at": "%s",\n' "$created_at"
        if [ -n "$key_value_pairs" ]; then
            printf '  %s,\n' "$key_value_pairs"
        fi
        printf '  "files": { %s }\n' "$entries_json"
        printf '}\n'
    } > "$manifest_path"
    MANIFEST_JSON="$(cat "$manifest_path")"
    printf '%s\n' "$manifest_path"
}

# ---
# parse_manifest <manifest.json>
#
# Returns the raw JSON. Empty file returns empty string (with a warning
# to stderr) so callers can detect a malformed archive.
parse_manifest() {
    local manifest_path="${1:?parse_manifest requires path}"
    if [ ! -f "$manifest_path" ]; then
        echo "lib-backup: parse_manifest: missing $manifest_path" >&2
        return 1
    fi
    cat "$manifest_path"
}
