#!/usr/bin/env bash
# smoke-upgrade.sh — Side-effect-free analysis of upgrade.sh.
#
# Usage:
#   sudo bash smoke-upgrade.sh [--verbose]
#
# This script does NOT actually perform an upgrade. It exercises
# the upgrade.sh safety properties by:
#   1. Syntax-checking upgrade.sh with bash -n.
#   2. Verifying upgrade.sh declares the safety features the
#      production-readiness gate requires:
#        - set -euo pipefail
#        - SHA256 verification
#        - backup of /var/lib/orvix/orvix.db
#        - backup of /var/lib/orvix/jwt_key.pem
#        - backup of /etc/orvix/vapid_private.key (if present)
#        - health-endpoint polling after restart
#        - rollback path on health failure
#        - --dry-run support
#   3. Verifying checksums.txt exists and parses.
#   4. Running upgrade.sh --help and checking the usage banner
#      actually renders (i.e. the ANSI escape sequences work —
#      catches the old `BOLD='\033[1m'` bug).
#
# This is the gate you wire into CI: "did someone accidentally
# remove the safety guarantees from upgrade.sh?" — if yes, this
# script exits non-zero.

set -euo pipefail

REPO_ROOT="${REPO_ROOT:-.}"
UPGRADE_SH="${UPGRADE_SH:-$REPO_ROOT/release/upgrade.sh}"
CHECKSUMS="${CHECKSUMS:-$REPO_ROOT/release/checksums.txt}"

VERBOSE=0
[ "${1:-}" = "--verbose" ] || [ "${1:-}" = "-v" ] && VERBOSE=1

log() {
    if [ "$VERBOSE" = "1" ]; then
        printf '  %s\n' "$*" >&2
    fi
}

pass() { printf 'PASS  %s\n' "$*" >&2; }
fail() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

# ─── 1. bash -n upgrade.sh ──────────────────────────────────────
if ! command -v bash >/dev/null 2>&1; then
    fail "bash is not installed"
fi
if [ ! -f "$UPGRADE_SH" ]; then
    fail "upgrade.sh not found at $UPGRADE_SH"
fi
if bash -n "$UPGRADE_SH"; then
    pass "upgrade.sh parses cleanly"
else
    fail "upgrade.sh has a bash syntax error"
fi

# ─── 2. upgrade.sh contains required safety properties ──────────
required_safety=(
    'set -euo pipefail'
    'sha256sum'
    'orvix.db'
    'jwt_key.pem'
    'vapid_private.key'
    'verify_health'
    'rolling back'
    '--dry-run'
    # Production-readiness gate BLOCKER 5: fail-closed checksum.
    'verify_checksum_fail_closed'
    '--allow-unsigned-local-artifact'
    'refusing to install an unverified downloaded binary'
    # Asset propagation: a backend upgrade MUST ship the matching
    # admin + webmail static assets; otherwise the operator hits a
    # stale admin SPA against a new backend. The contract:
    #   - upgrade.sh sources lib-asset-propagate.sh
    #   - upgrade.sh calls a propagate_assets function
    #   - that function calls asset_propagate for admin + webmail
    'lib-asset-propagate.sh'
    'propagate_assets'
    'asset_propagate'
)

for needle in "${required_safety[@]}"; do
    if grep -q -- "$needle" "$UPGRADE_SH"; then
        pass "upgrade.sh contains '$needle'"
    else
        fail "upgrade.sh is MISSING the safety property '$needle'"
    fi
done

# BLOCKER 5: the OLD warning-only verify_checksum function
# must NOT exist anymore. If it does, the fail-closed
# enforcement was reverted.
if grep -qE 'verify_checksum\(\)' "$UPGRADE_SH" && ! grep -qE 'verify_checksum_fail_closed\(\)' "$UPGRADE_SH"; then
    fail "upgrade.sh still has the OLD warning-only verify_checksum and is missing fail-closed enforcement"
fi

# ─── 2b. lib-asset-propagate.sh exists and parses ───────────────
# The asset-propagation library is the contract that backs the
# upgrade.sh propagate_assets call. If the lib is missing or has a
# syntax error, an upgrade leaves stale admin/webmail assets and
# the smoke gate must fail.
LIB_PATH="${LIB_PATH:-$REPO_ROOT/release/scripts/lib-asset-propagate.sh}"
if [ ! -f "$LIB_PATH" ]; then
    fail "release/scripts/lib-asset-propagate.sh is missing (asset propagation contract broken)"
fi
if bash -n "$LIB_PATH"; then
    pass "lib-asset-propagate.sh parses cleanly"
else
    fail "lib-asset-propagate.sh has a bash syntax error"
fi
if grep -qE '^asset_propagate\(\)' "$LIB_PATH"; then
    pass "lib-asset-propagate.sh declares asset_propagate()"
else
    fail "lib-asset-propagate.sh is missing the asset_propagate() entry point"
fi

# BLOCKER 5: --allow-unsigned-local-artifact must be REFUSED
# for --from-url upgrades. The check is implemented as a
# string match in main() — pin it here.
if ! grep -qE 'allow-unsigned-local-artifact.*refused for --from-url|FAIL.*--allow-unsigned-local-artifact is refused' "$UPGRADE_SH"; then
    fail "upgrade.sh does not refuse --allow-unsigned-local-artifact for --from-url (BLOCKER 5)"
fi

# ─── 3. upgrade.sh does NOT contain the unsafe patterns ─────────
# Specifically, the old version used BOLD='\033[1m' (single
# quotes) which would print literal "\033[1m" to the terminal
# instead of an ANSI bold escape. Catch any regression.
if grep -qE "^[[:space:]]*BOLD='\\\\033" "$UPGRADE_SH"; then
    fail "upgrade.sh uses single-quoted BOLD='\\033...'; must be BOLD=\$'\\033...'"
fi

if grep -E '^[[:space:]]*[^#]' "$UPGRADE_SH" 2>/dev/null | grep -qE "releases\.orvix\.email"; then
    fail "upgrade.sh hardcodes https://releases.orvix.email in executable code; URL does not exist"
fi

# ─── 4. checksums.txt exists and parses ─────────────────────────
if [ ! -f "$CHECKSUMS" ]; then
    fail "checksums.txt not found at $CHECKSUMS"
fi
# Every non-comment, non-blank line must have at least two
# whitespace-separated fields: <sha256> <filename>
bad_lines=$(grep -vE '^\s*(#|$)' "$CHECKSUMS" | awk 'NF<2 {print NR": "$0}')
if [ -n "$bad_lines" ]; then
    fail "checksums.txt has malformed lines:"$'\n'"$bad_lines"
fi
# Every sha256 must be exactly 64 lowercase hex chars.
bad_sha=$(grep -vE '^\s*(#|$)' "$CHECKSUMS" | awk '{print $1}' | grep -vE '^[0-9a-f]{64}$' || true)
if [ -n "$bad_sha" ]; then
    fail "checksums.txt has malformed sha256 lines: $bad_sha"
fi
pass "checksums.txt is well-formed"

# If a binary is checked in, verify the listed sha256 matches
# the actual file.
while read -r sha filename; do
    case "$filename" in ''|\#*) continue ;; esac
    if [ -f "$REPO_ROOT/release/$filename" ]; then
        actual=$(sha256sum "$REPO_ROOT/release/$filename" | awk '{print $1}')
        if [ "$actual" = "$sha" ]; then
            pass "checksum matches release/$filename"
        else
            fail "checksum MISMATCH for release/$filename (expected $sha, got $actual)"
        fi
    fi
done <"$CHECKSUMS"

# ─── 4b. lib-asset-propagate.sh actually copies + verifies hashes ─
# The smoke gate is not just a static check: we exercise the lib
# against a real temp source tree and assert the destination is
# populated, the hashes match the source, and a rollback to a
# pre-existing dest works.
LIB_TEST_TMP="$(mktemp -d -t orvix-asset-smoke-XXXXXX)"
trap 'rm -rf "$LIB_TEST_TMP" 2>/dev/null || true' EXIT
LIB_SRC="$LIB_TEST_TMP/src"
LIB_DEST="$LIB_TEST_TMP/dest"
LIB_BACKUP="$LIB_TEST_TMP/backup"
mkdir -p "$LIB_SRC/sub" "$LIB_DEST"
printf 'alpha\n' > "$LIB_SRC/index.html"
printf 'beta\n' > "$LIB_SRC/sub/page.html"
# Pre-existing file in dest that the propagation must overwrite.
printf 'OLD\n' > "$LIB_DEST/index.html"
# shellcheck disable=SC1090
. "$LIB_PATH"
ASSET_BACKUP_PARENT="$LIB_BACKUP" \
    ASSET_SKIP_BACKUP=0 \
    ASSET_VERBOSE=0 \
    asset_propagate "$LIB_SRC" "$LIB_DEST" "smoke-admin" || \
    fail "asset_propagate failed on a clean temp source"
# Destination must have the new content.
if [ "$(cat "$LIB_DEST/index.html")" = "alpha" ]; then
    pass "asset_propagate replaced existing dest with new content"
else
    fail "asset_propagate did not replace existing dest (got $(cat "$LIB_DEST/index.html"))"
fi
if [ "$(cat "$LIB_DEST/sub/page.html")" = "beta" ]; then
    pass "asset_propagate propagated sub-directories"
else
    fail "asset_propagate did not propagate sub-directories"
fi
# Pre-existing dest must be backed up before replacement.
LATEST_BACKUP="$(ls -1 "$LIB_BACKUP" 2>/dev/null | sort | tail -n 1)"
if [ -n "$LATEST_BACKUP" ] && [ -f "$LIB_BACKUP/$LATEST_BACKUP/index.html" ]; then
    if [ "$(cat "$LIB_BACKUP/$LATEST_BACKUP/index.html")" = "OLD" ]; then
        pass "asset_propagate backed up the pre-existing dest before overwriting"
    else
        fail "asset_propagate backup does not contain the old content (got $(cat "$LIB_BACKUP/$LATEST_BACKUP/index.html"))"
    fi
else
    fail "asset_propagate did not produce a backup under $LIB_BACKUP"
fi
# Permission contract: 0755 on dirs, 0644 on files.
DEST_MODE="$(stat -c '%a' "$LIB_DEST" 2>/dev/null || stat -f '%Lp' "$LIB_DEST" 2>/dev/null || true)"
if [ "$DEST_MODE" = "755" ]; then
    pass "asset_propagate sets destination dir mode to 0755"
else
    fail "asset_propagate destination dir mode = $DEST_MODE, want 755"
fi
FILE_MODE="$(stat -c '%a' "$LIB_DEST/index.html" 2>/dev/null || stat -f '%Lp' "$LIB_DEST/index.html" 2>/dev/null || true)"
if [ "$FILE_MODE" = "644" ]; then
    pass "asset_propagate sets file mode to 0644"
else
    fail "asset_propagate file mode = $FILE_MODE, want 644"
fi

# Source missing must be a non-zero exit (fail-closed).
if asset_propagate "$LIB_TEST_TMP/does-not-exist" "$LIB_DEST" 2>/dev/null; then
    fail "asset_propagate must fail closed when source is missing"
else
    pass "asset_propagate fails closed when source is missing"
fi

# Source-empty must be reported (no files copied → exit non-zero).
EMPTY_SRC="$LIB_TEST_TMP/empty"
mkdir -p "$EMPTY_SRC"
if asset_propagate "$EMPTY_SRC" "$LIB_DEST" 2>/dev/null; then
    fail "asset_propagate must fail closed when source has no files"
else
    pass "asset_propagate fails closed when source is empty"
fi

# Symlink source must be refused (security contract). On Windows
# Git Bash, `ln -s` is a no-op for non-admin users, so we also
# accept a plain file as a "looks like a symlink" stand-in (the lib
# check is on `[ -L "$src" ]` which is only true for real symlinks).
# A real symlink works on Linux/macOS and on Windows Git Bash with
# developer mode enabled.
ln -s "$LIB_SRC" "$LIB_TEST_TMP/src-symlink" 2>/dev/null || true
if [ -L "$LIB_TEST_TMP/src-symlink" ]; then
    if asset_propagate "$LIB_TEST_TMP/src-symlink" "$LIB_DEST" 2>/dev/null; then
        fail "asset_propagate must refuse a symlink source"
    else
        pass "asset_propagate refuses a symlink source"
    fi
else
    # Git Bash on Windows without dev mode cannot create symlinks.
    # The path-traversal guard above is the equivalent safety
    # check; assert that is wired.
    if grep -qE "refusing path-traversal" "$LIB_PATH"; then
        pass "asset_propagate has a path-traversal guard (symlink test skipped: Git Bash on this host cannot create symlinks)"
    else
        fail "asset_propagate is missing a path-traversal guard"
    fi
fi
rm -rf "$LIB_TEST_TMP/src-symlink" 2>/dev/null || true

# ─── 5. upgrade.sh --help renders without ANSI escape artifacts ──
# Run --help and grep for the literal escape sequence; if BOLD
# was declared with single quotes, the help text will contain
# literal "\033[1m" instead of escape codes. Catches the
# cosmetic regression that broke the previous release's banner.
out=$(bash "$UPGRADE_SH" --help 2>&1 || true)
if printf '%s' "$out" | grep -qE '\\\\033'; then
    fail "upgrade.sh --help prints literal \\033 sequences (ANSI not interpolated)"
fi
if printf '%s' "$out" | grep -q 'Usage:'; then
    pass "upgrade.sh --help renders cleanly"
else
    fail "upgrade.sh --help does not print a Usage block"
fi

printf '\nALL UPGRADE SMOKE TESTS PASSED\n' >&2
exit 0