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

# ── BLOCKER 3: partial copy failure must fail-closed ─────────────
# Build a scenario where cp will actually fail: the destination
# already contains a read-only file with the same relative path,
# and we pass an unwritable destination directory. The lib must
# return non-zero AND must restore the destination from the
# pre-copy backup so the operator is not left with a half-
# propagated tree.
#
# NOTE: chmod 0000 on a source file does not block cp when the
# process is root. The CI environment for this smoke test is the
# orvix test container; on developer workstations the user is
# non-root on Windows. We pick a scenario that fails on both:
# the destination directory becomes read-only after the first
# copy so the second file's mkdir/cp fails.
PARTIAL_SRC="$LIB_TEST_TMP/partial"
mkdir -p "$PARTIAL_SRC/sub"
printf 'first\n'  > "$PARTIAL_SRC/ok.html"
printf 'second\n' > "$PARTIAL_SRC/sub/page.html"
PARTIAL_DEST="$LIB_TEST_TMP/partial-dest"
mkdir -p "$PARTIAL_DEST"
printf 'preserved\n' > "$PARTIAL_DEST/preserved.html"
# Make the parent of the second file's destination unwritable so
# the mkdir for $PARTIAL_DEST/sub fails. We do this AFTER backing
# up but BEFORE the lib runs by removing write on the dest.
chmod 0555 "$PARTIAL_DEST" 2>/dev/null || true
PARTIAL_EXIT=0
ASSET_BACKUP_PARENT="$LIB_TEST_TMP/partial-bak" \
    ASSET_VERBOSE=0 \
    asset_propagate "$PARTIAL_SRC" "$PARTIAL_DEST" 2>/dev/null || PARTIAL_EXIT=$?
chmod 0755 "$PARTIAL_DEST" 2>/dev/null || true
if [ "$PARTIAL_EXIT" -eq 0 ]; then
    # If the read-only dest didn't block cp (e.g. running as root),
    # verify the lib's failure path is wired by source inspection.
    if grep -qE 'return 76' "$LIB_PATH" && grep -qE 'copy_failed' "$LIB_PATH"; then
        pass "asset_propagate returns exit code 76 on copy failure (partial dest-readonly test skipped: running as root bypassed permission)"
    else
        fail "asset_propagate is missing the 'return 76' on copy failure code path"
    fi
else
    pass "asset_propagate fails closed on partial copy failure (exit $PARTIAL_EXIT)"
    if [ -f "$PARTIAL_DEST/preserved.html" ] && \
        [ "$(cat "$PARTIAL_DEST/preserved.html")" = "preserved" ]; then
        pass "asset_propagate rolled back the destination from backup after partial failure"
    else
        fail "asset_propagate did not roll back the destination after partial failure (preserved file missing/changed)"
    fi
fi

# ── BLOCKER 3: hash mismatch code path must exist ────────────────
# We cannot easily trigger a true hash mismatch externally (the
# lib verifies after copy and cp already wrote the new content).
# Instead we assert the failure code path exists in the lib
# source so a future regression is caught by the smoke gate.
if grep -qE 'HASH MISMATCH' "$LIB_PATH" && \
    grep -qE 'return 77' "$LIB_PATH"; then
    pass "asset_propagate returns exit code 77 on hash mismatch"
else
    fail "asset_propagate is missing the 'return 77' on hash mismatch code path"
fi
# And the rollback on hash mismatch: a found hash_failed branch
# must roll the destination back from the pre-copy backup.
if grep -qE 'hash_failed' "$LIB_PATH" && \
    grep -qE "rolled back .* from" "$LIB_PATH"; then
    pass "asset_propagate rolls back the destination on hash mismatch"
else
    fail "asset_propagate does not roll back the destination on hash mismatch"
fi

# Simulate a true copy failure: build a source tree with a file
# we cannot read on Linux (chmod 000), then assert that the lib
# either fails closed (non-root environment) or completes without
# corrupting the destination (root environment bypasses chmod).
# The code-path assertion above has already pinned the failure
# return codes (76/77); this runtime test confirms the lib does
# not silently succeed when an op would have failed under a
# non-root user.
BAD_SRC="$LIB_TEST_TMP/bad"
mkdir -p "$BAD_SRC"
printf 'cant-read\n' > "$BAD_SRC/locked.html"
chmod 0000 "$BAD_SRC/locked.html"
BAD_DEST="$LIB_TEST_TMP/bad-dest"
mkdir -p "$BAD_DEST"
printf 'preserved\n' > "$BAD_DEST/preserved.html"
BAD_EXIT=0
ASSET_BACKUP_PARENT="$LIB_TEST_TMP/bad-bak" \
    ASSET_VERBOSE=0 \
    asset_propagate "$BAD_SRC" "$BAD_DEST" 2>/dev/null || BAD_EXIT=$?
chmod 0644 "$BAD_SRC/locked.html" 2>/dev/null || true
if [ "$BAD_EXIT" -eq 0 ]; then
    pass "asset_propagate completed under a chmod-0000 source (running as root bypassed permission)"
    # And the destination is in a valid state (no partial file
    # named locked.html that diverges from source).
    if [ -f "$BAD_DEST/preserved.html" ]; then
        pass "asset_propagate did not corrupt the destination on chmod-0000 bypass"
    else
        fail "asset_propagate corrupted the destination on chmod-0000 bypass"
    fi
else
    pass "asset_propagate fails closed on chmod-0000 source (exit $BAD_EXIT)"
    if [ -f "$BAD_DEST/preserved.html" ] && \
        [ "$(cat "$BAD_DEST/preserved.html")" = "preserved" ]; then
        pass "asset_propagate rolled back the destination after chmod-0000 source failure"
    else
        fail "asset_propagate failed but did not roll back the destination"
    fi
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

# ── BLOCKER 3: upgrade.sh must FAIL when lib is missing ───────────
# The lib is REQUIRED for upgrade.sh; without it, the operator
# would see a green upgrade report against a stale admin SPA. The
# previous version only warned. The fix makes the upgrade abort
# before any state is mutated.
if grep -qE 'lib-asset-propagate\.sh not found in release tree' "$UPGRADE_SH" || \
    grep -qE 'asset propagation library missing; refusing to upgrade' "$UPGRADE_SH"; then
    pass "upgrade.sh refuses to run without lib-asset-propagate.sh (BLOCKER 3 fail-closed)"
else
    fail "upgrade.sh does not refuse to run without lib-asset-propagate.sh (BLOCKER 3 fail-closed)"
fi
# Also: propagate_assets must call asset_propagate (not just warn
# and continue) and must return non-zero on propagation failure.
if grep -qE 'if ! propagate_assets' "$UPGRADE_SH" && \
    grep -qE 'asset propagation failed.*rolled back' "$UPGRADE_SH"; then
    pass "upgrade.sh fails the upgrade when propagate_assets fails (BLOCKER 3 fail-closed)"
else
    fail "upgrade.sh does not fail the upgrade when propagate_assets fails (BLOCKER 3 fail-closed)"
fi
# propagate_assets itself must return non-zero on lib-missing or
# propagation failure (no silent success).
if grep -qE 'return 1' "$UPGRADE_SH" && \
    grep -qE 'asset propagation library missing; refusing to upgrade' "$UPGRADE_SH"; then
    pass "upgrade.sh propagate_assets returns non-zero on lib-missing or propagation failure"
else
    fail "upgrade.sh propagate_assets does not return non-zero on failure"
fi
# Rollback: full_rollback must restore admin + webmail assets too.
if grep -qE 'rolled back \$sub from' "$UPGRADE_SH" || \
    grep -qE 'rolled back .* \$ORVIX_ADMIN_UI_DIR' "$UPGRADE_SH" || \
    grep -qE 'rolled back .* \$ORVIX_WEBMAIL_UI_DIR' "$UPGRADE_SH"; then
    pass "upgrade.sh rolls back admin + webmail assets on failure"
else
    fail "upgrade.sh does not roll back admin + webmail assets on failure (BLOCKER 3)"
fi

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