#!/usr/bin/env bash
# Docs coherence lint (issue #280).
#
# Fails if active documentation references removed CLI commands,
# stale versions, or image tags that disagree with the actual release
# workflow. The contract is documented in ROADMAP.md under
# "Historical scope (removed by the Docker-first pivot, #226)".
#
# Conservative policy:
#   - Forbidden tokens are NOT allowed in active docs.
#   - They ARE allowed inside ADRs that carry a `Status: Superseded`
#     header (those are explicit supersession markers).
#   - They are NOT allowed in any other doc, including READMEs,
#     deployment guides, integration guides, the top-level ROADMAP,
#     and ADRs that are still "Accepted".
#
# Cheap on purpose: pure grep, no Go toolchain, runs in well under a
# second on the current doc tree.

set -euo pipefail

# Resolve repo root regardless of where the script is invoked from.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

# Files in scope: everything under docs/, deploy/, plus the two
# repo-root docs.
FILES=$(find docs deploy README.md ROADMAP.md -type f \
    \( -name '*.md' -o -name '*.html' -o -name '*.mdx' -o -name '*.txt' \) \
    2>/dev/null | sort -u)

if [ -z "$FILES" ]; then
    echo "lint-docs: no doc files found under docs/, deploy/, README.md, ROADMAP.md" >&2
    exit 1
fi

# Patterns the active docs must not contain. The grep -F (fixed-string)
# flavour keeps this safe and avoids regex pitfalls; we are matching
# literal tokens that used to be commands or stale version markers.
FORBIDDEN=(
    'mcm doctor'
    'mcm config validate'
    'mcm server'
    'mcm status'
    'mcm agent'
    'mcm backup'
    'mcm restore'
    'Go 1.24'
    'GoReleaser'
)

violations=0
checked=0

for f in $FILES; do
    # Allow forbidden tokens inside ADRs that explicitly mark the
    # section Superseded. The header is the first non-blank H2 section
    # immediately after the title; we check it directly instead of
    # trying to be clever about file layout.
    is_superseded_adr=0
    case "$f" in
        docs/adr/*.md)
            # The first non-empty line of the file after the H1 title
            # should be `## Status` followed by `Superseded` (or
            # `Accepted (the … dropped by the Docker-first pivot …)`).
            # We grep for the literal phrase `Superseded` on its own
            # line right after `## Status` — generous enough to admit
            # the explanatory variants we use elsewhere.
            if awk '
                /^# / { in_title = 1; next }
                in_title && /^$/ { next }
                in_title && /^## Status/ {
                    in_status = 1; next
                }
                in_status && /^$/ { next }
                in_status && /Superseded/ { found = 1; exit }
                in_status && /^## / { exit }
                END { exit !found }
            ' "$f"; then
                is_superseded_adr=1
            fi
            ;;
    esac

    checked=$((checked + 1))

    for token in "${FORBIDDEN[@]}"; do
        # fixed-string, case-sensitive.
        hits=$(grep -nF -- "$token" "$f" || true)
        if [ -n "$hits" ]; then
            if [ "$is_superseded_adr" -eq 1 ]; then
                # Allowed — file is explicitly Superseded. Continue.
                continue
            fi
            echo "lint-docs: forbidden token \"$token\" found in active doc $f:" >&2
            echo "$hits" | sed 's/^/  /' >&2
            violations=$((violations + 1))
        fi
    done
done

if [ "$violations" -gt 0 ]; then
    echo "lint-docs: $violations forbidden token(s) in active docs (checked $checked files)" >&2
    echo "lint-docs: see ROADMAP.md 'Historical scope' and issue #280 for context." >&2
    exit 1
fi

# Image-tag shape check: production.md and similar must not show
# `:v<version>` tags. The release workflow strips the leading `v`
# from the GHCR tag (see .github/workflows/release.yml), so any
# example with `:v0.1.0`-style tags is a contract mismatch.
tag_hits=$(grep -nE ':v[0-9]+\.[0-9]+\.[0-9]+([-+][A-Za-z0-9.-]+)?' \
    $FILES 2>/dev/null || true)
if [ -n "$tag_hits" ]; then
    echo "lint-docs: ':v<semver>' image tag found (workflow strips the v prefix):" >&2
    echo "$tag_hits" | sed 's/^/  /' >&2
    exit 1
fi

echo "lint-docs: ok ($checked files checked, 0 violations)"