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
)

for needle in "${required_safety[@]}"; do
    if grep -q -- "$needle" "$UPGRADE_SH"; then
        pass "upgrade.sh contains '$needle'"
    else
        fail "upgrade.sh is MISSING the safety property '$needle'"
    fi
done

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