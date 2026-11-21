#!/usr/bin/env bash
# Tests for scripts/backup.sh archive publication (issue #401).
#
# Run from the repo root:
#   bash scripts/tests/test-backup.sh
#
# No real docker is needed: a stub `docker` on PATH accepts every helper
# container and drops a fake mcm.db into the SQLite snapshot mount. A
# wrapper `tar` on PATH delegates to the real tar, or, when
# FAKE_TAR_FAIL=1, writes a partial archive to its destination and fails
# (packaging interrupted after the destination was opened).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REAL_TAR="$(command -v tar)"

PASS=0
FAIL=0
WORK="$(mktemp -d -t mcm-test-backup-XXXXXX)"
trap 'rm -rf "$WORK"' EXIT

check() {
    local msg="$1"
    shift
    if "$@"; then
        PASS=$((PASS + 1))
    else
        FAIL=$((FAIL + 1))
        echo "FAIL: $msg" >&2
        echo "  --- stderr ---" >&2; sed 's/^/  /' "$WORK/err" >&2
    fi
}

mkdir -p "$WORK/bin"
cat > "$WORK/bin/docker" <<'STUB'
#!/usr/bin/env bash
[ "$1" = "run" ] || exit 0
for arg in "$@"; do
    case "$arg" in
        *:/backup) echo "fake sqlite snapshot" > "${arg%:/backup}/mcm.db" ;;
    esac
done
STUB
cat > "$WORK/bin/tar" <<'STUB'
#!/usr/bin/env bash
if [ "${FAKE_TAR_FAIL:-}" = 1 ] && [ "$1" = czf ] && [ "$2" != - ]; then
    echo "partial" > "$2"
    echo "tar: write error" >&2
    exit 1
fi
exec "$REAL_TAR" "$@"
STUB
chmod +x "$WORK/bin/docker" "$WORK/bin/tar"

# run_backup <output> [FAKE_TAR_FAIL]; sets RC, output in $WORK/out|err.
run_backup() {
    set +e
    PATH="$WORK/bin:$PATH" REAL_TAR="$REAL_TAR" FAKE_TAR_FAIL="${2:-}" \
        MCM_BACKUP_GIT_COMMIT=test MCM_BACKUP_MCM_VERSION=test \
        bash "$REPO_ROOT/scripts/backup.sh" "$1" > "$WORK/out" 2> "$WORK/err"
    RC=$?
    set -e
}

only_archive_in() { [ "$(ls -A "$1")" = "$2" ]; }

# Previous behaviour: a successful run creates the missing directory and
# writes an owner-only archive with a manifest.
run_backup "$WORK/fresh/mcm-data.tgz"
check "fresh backup exits 0 (got $RC)" [ "$RC" -eq 0 ]
check "fresh backup reports the archive" \
    grep -qF "backup.sh: wrote $WORK/fresh/mcm-data.tgz (mode 0600; contains secrets, store securely)" "$WORK/out"
check "fresh backup archive is mode 0600" [ "$(stat -c %a "$WORK/fresh/mcm-data.tgz")" = 600 ]
check "fresh backup archive holds the manifest" \
    bash -c "'$REAL_TAR' tzf '$WORK/fresh/mcm-data.tgz' | grep -qx './manifest.json'"
check "fresh backup leaves no temporary file" only_archive_in "$WORK/fresh" mcm-data.tgz

# Issue #401: an interrupted packaging keeps the previous archive intact
# and leaves no temporary file behind.
mkdir -p "$WORK/keep"
echo "previous good archive" > "$WORK/keep/mcm-data.tgz"
run_backup "$WORK/keep/mcm-data.tgz" 1
check "failed packaging exits non-zero (got $RC)" [ "$RC" -ne 0 ]
check "failed packaging keeps the previous archive" \
    [ "$(cat "$WORK/keep/mcm-data.tgz")" = "previous good archive" ]
check "failed packaging leaves no temporary file" only_archive_in "$WORK/keep" mcm-data.tgz
check "failed packaging does not report a written archive" \
    bash -c "! grep -qF 'backup.sh: wrote' '$WORK/out'"

# A successful run replaces the previous archive.
run_backup "$WORK/keep/mcm-data.tgz"
check "replacing backup exits 0 (got $RC)" [ "$RC" -eq 0 ]
check "replacing backup publishes the new archive" \
    bash -c "'$REAL_TAR' tzf '$WORK/keep/mcm-data.tgz' | grep -qx './mcm/mcm.db'"
check "replacing backup archive is mode 0600" [ "$(stat -c %a "$WORK/keep/mcm-data.tgz")" = 600 ]
check "replacing backup leaves no temporary file" only_archive_in "$WORK/keep" mcm-data.tgz

echo "test-backup: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
