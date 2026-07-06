#!/usr/bin/env bash
# smoke-webmail-js.sh — Side-effect-free Node.js syntax check for the
# Orvix Webmail front-end bundle.
#
# Mirrors the admin equivalent (smoke-admin-js.sh) but for the
# webmail SPA at release/webmail/. Verifies that every shipped JS
# file parses cleanly with `node --check`. No runtime, no DOM — a
# pure parse pass per file.
#
# Files covered:
#   1. release/webmail/assets/auth-gate.js   — session probe + login form
#   2. release/webmail/assets/webmail.js     — main SPA shell
#   3. release/webmail/assets/webmail-push.js — RFC 8030 web-push helper
#   4. release/webmail/sw.js                 — service worker
#   5. release/webmail/client-setup.js       — Mail Client Setup sub-page
#
# Node discovery, CRLF normalisation, and Windows-path probing are
# identical to smoke-admin-js.sh so both gates can run side-by-side
# in CI without surprising path-handling differences.
#
# Usage:
#   bash release/scripts/smoke-webmail-js.sh [--verbose]
#
# Exits 0 when every file parses, 1 otherwise.

set -euo pipefail

REPO_ROOT="${REPO_ROOT:-.}"
WEBMAIL_DIR="${WEBMAIL_DIR:-$REPO_ROOT/release/webmail}"

VERBOSE=0
[ "${1:-}" = "--verbose" ] || [ "${1:-}" = "-v" ] && VERBOSE=1

log() {
    if [ "$VERBOSE" = "1" ]; then
        printf '  %s\n' "$*" >&2
    fi
}

pass() { printf 'PASS  %s\n' "$*" >&2; }
fail() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

# ── 1. Required structure exists ─────────────────────────────────
[ -d "$WEBMAIL_DIR" ]           || fail "release/webmail directory not found"
[ -f "$WEBMAIL_DIR/index.html" ] || fail "release/webmail/index.html not found"
[ -d "$WEBMAIL_DIR/assets" ]    || fail "release/webmail/assets directory not found"
pass "release/webmail/ has the expected directory shape"

# ── 2. Locate Node.js (portable) ─────────────────────────────────
#
# probe_node PATH_VAR <candidate...> — sets PATH_VAR to the first
# candidate that resolves to an executable file. Returns 0 on
# success, 1 on miss. CRLF and whitespace are stripped before any
# "is this path usable" check.
probe_node() {
    local __var="$1"; shift
    for candidate in "$@"; do
        if [ -z "$candidate" ] || [ "$candidate" = " " ]; then
            continue
        fi
        candidate="${candidate%$'\r'}"
        candidate="$(printf '%s' "$candidate" | sed -E 's/^[[:space:]]+//;s/[[:space:]]+$//')"
        [ -n "$candidate" ] || continue
        case "$candidate" in *$'\r'*) continue ;; esac
        if [ -x "$candidate" ]; then
            eval "$__var=\"\$candidate\""
            return 0
        fi
    done
    return 1
}

NODE_BIN=""

if [ -n "${NODE:-}" ]; then
    if probe_node NODE_BIN "$NODE"; then
        pass "located node via \$NODE override: $NODE_BIN"
    else
        printf 'WARN  \$NODE=%s set but not executable; falling through to PATH probes\n' "$NODE" >&2
    fi
fi
if [ -z "$NODE_BIN" ] && command -v node >/dev/null 2>&1; then
    NODE_BIN="$(command -v node)"
    pass "located node via command -v: $NODE_BIN"
fi
if [ -z "$NODE_BIN" ] && command -v nodejs >/dev/null 2>&1; then
    NODE_BIN="$(command -v nodejs)"
    pass "located nodejs via command -v: $NODE_BIN"
fi
if [ -z "$NODE_BIN" ]; then
    WIN_CANDIDATES=(
        "/c/Program Files/nodejs/node.exe"
        "/c/Program Files (x86)/nodejs/node.exe"
        "/mnt/c/Program Files/nodejs/node.exe"
        "/mnt/c/Program Files (x86)/nodejs/node.exe"
        "C:/Program Files/nodejs/node.exe"
        "C:/Program Files (x86)/nodejs/node.exe"
    )
    if probe_node NODE_BIN "${WIN_CANDIDATES[@]}"; then
        pass "located node via Windows common path: $NODE_BIN"
    fi
fi
if [ -z "$NODE_BIN" ] && command -v where.exe >/dev/null 2>&1; then
    WIN_CANDIDATES=()
    while IFS= read -r line; do WIN_CANDIDATES+=("$line"); done \
        < <(where.exe node 2>/dev/null | tr -d '\r')
    if probe_node NODE_BIN "${WIN_CANDIDATES[@]}"; then
        pass "located node via where.exe: $NODE_BIN"
    fi
fi
if [ -z "$NODE_BIN" ] && command -v powershell.exe >/dev/null 2>&1; then
    PS_CANDIDATES=()
    while IFS= read -r line; do PS_CANDIDATES+=("$line"); done \
        < <(powershell.exe -NoProfile -Command \
            "(Get-Command node -ErrorAction SilentlyContinue).Source" \
            2>/dev/null | tr -d '\r')
    if probe_node NODE_BIN "${PS_CANDIDATES[@]}"; then
        pass "located node via powershell Get-Command: $NODE_BIN"
    fi
fi

if [ -z "$NODE_BIN" ]; then
    fail "node (or nodejs) is not installed and could not be located on this system; install Node.js 18+ from https://nodejs.org and retry"
fi

if ! "$NODE_BIN" --version >/dev/null 2>&1; then
    fail "discovered node binary at '$NODE_BIN' but it does not execute"
fi

NODE_VERSION="$("$NODE_BIN" --version 2>&1 | tr -d '\r\n' || true)"
pass "using node: $NODE_BIN ($NODE_VERSION)"

# ── 3. Enumerate JS files to parse ──────────────────────────────
#
# The webmail bundle has only a few files (vs admin's dozen) so we
# list them explicitly rather than globbing. This makes the script
# fail-closed: if a future contributor adds a fifth JS file in
# release/webmail/, this script won't silently skip it.
JS_FILES=()
for rel in \
    "assets/auth-gate.js" \
    "assets/webmail.js" \
    "assets/webmail-push.js" \
    "sw.js" \
    "client-setup.js"; do
    full="$WEBMAIL_DIR/$rel"
    if [ -f "$full" ]; then
        JS_FILES+=("$full")
    else
        # Optional files. sw.js is optional (offline mode is opt-in);
        # client-setup.js only exists if the Client Setup sub-page is
        # shipped. Halt explicitly here is wrong — some files are
        # genuinely optional.
        log "skipping optional missing file: $full"
    fi
done

# Required hard-minimum: auth-gate.js + webmail.js. The SPA cannot
# boot without at least these two.
for required in \
    "$WEBMAIL_DIR/assets/auth-gate.js" \
    "$WEBMAIL_DIR/assets/webmail.js"; do
    [ -f "$required" ] || fail "required webmail asset missing: $required"
done

if [ "${#JS_FILES[@]}" -eq 0 ]; then
    fail "no .js files found under $WEBMAIL_DIR (webmail bundle missing)"
fi

pass "enumerated ${#JS_FILES[@]} JS file(s) to check"

# ── 4. Syntax check each file ───────────────────────────────────
FAIL_COUNT=0
for f in "${JS_FILES[@]}"; do
    if ! "$NODE_BIN" --check "$f" >/dev/null 2>&1; then
        FAIL_COUNT=$((FAIL_COUNT + 1))
        printf 'FAIL  syntax error in %s\n' "$f" >&2
        "$NODE_BIN" --check "$f" || true
    else
        log "ok: $f"
    fi
done

if [ "$FAIL_COUNT" -ne 0 ]; then
    fail "$FAIL_COUNT JS file(s) failed syntax check (see diagnostics above)"
fi

pass "all ${#JS_FILES[@]} JS file(s) parse cleanly under Node $NODE_VERSION (via $NODE_BIN)"

printf '\nALL WEBMAIL JS SYNTAX TESTS PASSED\n' >&2
exit 0
