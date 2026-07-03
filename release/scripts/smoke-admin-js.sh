#!/usr/bin/env bash
# smoke-admin-js.sh — Side-effect-free Node.js syntax check.
#
# Verifies that every JavaScript file under release/admin parses cleanly:
#   1. release/admin/app.js
#   2. every release/admin/modules/**/*.js
#
# Node is required. This script intentionally does NOT shell out to
# `xargs node --check` because on Windows / Git Bash that pipeline
# produces the failure mode "xargs: node: No such file or directory"
# when node is on PATH but not under the alias `xargs` reaches.
# Instead, this script:
#
#   - locates node via `command -v`, `which`, and on Windows also
#     `where.exe node` (the where.exe path is honoured so Git Bash on
#     Windows picks up the standard Node install at C:\Program Files\nodejs),
#   - falls back to `command -v nodejs`,
#   - fails closed with a clear message when Node is missing,
#   - invokes `node --check <file>` once per file.
#
# Usage:
#   bash release/scripts/smoke-admin-js.sh [--verbose]
#
# Exits 0 when every file parses, 1 otherwise.

set -euo pipefail

REPO_ROOT="${REPO_ROOT:-.}"
ADMIN_DIR="${ADMIN_DIR:-$REPO_ROOT/release/admin}"

VERBOSE=0
[ "${1:-}" = "--verbose" ] || [ "${1:-}" = "-v" ] && VERBOSE=1

log() {
    if [ "$VERBOSE" = "1" ]; then
        printf '  %s\n' "$*" >&2
    fi
}

pass() { printf 'PASS  %s\n' "$*" >&2; }
fail() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

# ── 1. Required files exist ─────────────────────────────────────
[ -d "$ADMIN_DIR" ] || fail "release/admin directory not found"
[ -f "$ADMIN_DIR/app.js" ] || fail "release/admin/app.js not found"
[ -d "$ADMIN_DIR/modules" ] || fail "release/admin/modules directory not found"
pass "release/admin structure exists"

# ── 2. Locate Node.js (portable) ─────────────────────────────────
#
# We try four probes in order:
#   (a) `command -v node`        — POSIX, works on Linux + macOS + Git Bash.
#   (b) `command -v nodejs`      — Debian/Ubuntu symlink alias.
#   (c) `which node`             — legacy fallback (some Git Bash releases).
#   (d) `where.exe node`         — Windows CMD / PowerShell — only available
#                                  when running under a shell that exposes
#                                  the Windows PATH (Git Bash + WSL).
# We prefer `command -v` because it prints a clean absolute path without
# any trailing CRLF that some Windows shells append.
#
# Whatever path we discover is captured into NODE_BIN. We never use
# `xargs node` because that pathway on Git Bash historically resolves
# `xargs` to the Cygwin build that does not see Windows PATH.

NODE_BIN=""
if command -v node >/dev/null 2>&1; then
    NODE_BIN="$(command -v node)"
elif command -v nodejs >/dev/null 2>&1; then
    NODE_BIN="$(command -v nodejs)"
elif which node >/dev/null 2>&1; then
    NODE_BIN="$(which node)"
elif which nodejs >/dev/null 2>&1; then
    NODE_BIN="$(which nodejs)"
elif command -v where.exe >/dev/null 2>&1; then
    # Windows-only probe. `where.exe` exits 0 when the binary is on
    # PATH and prints its absolute path. We strip CR + read the first
    # matching line.
    NODE_WIN="$(where.exe node 2>/dev/null | tr -d '\r' | head -n 1 || true)"
    if [ -n "$NODE_WIN" ] && [ -x "$NODE_WIN" ]; then
        NODE_BIN="$NODE_WIN"
    fi
    if [ -z "$NODE_BIN" ]; then
        NODE_WIN="$(where.exe nodejs 2>/dev/null | tr -d '\r' | head -n 1 || true)"
        if [ -n "$NODE_WIN" ] && [ -x "$NODE_WIN" ]; then
            NODE_BIN="$NODE_WIN"
        fi
    fi
fi

if [ -z "$NODE_BIN" ]; then
    fail "node (or nodejs) is not installed; install Node.js 18+ from https://nodejs.org and retry"
fi

# Verify the discovered binary actually runs. A path on disk that fails
# to execute is worse than a clear "missing" error.
if ! "$NODE_BIN" --version >/dev/null 2>&1; then
    fail "discovered node binary at '$NODE_BIN' but it does not execute; check the Node install"
fi

NODE_VERSION="$("$NODE_BIN" --version 2>&1 | tr -d '\r\n' || true)"
pass "located node: $NODE_BIN ($NODE_VERSION)"

# ── 3. Enumerate JS files ───────────────────────────────────────
#
# We collect both `release/admin/app.js` and every .js under
# `release/admin/modules/`. Globbing is portable to Git Bash and Linux
# without relying on `find -print0 | xargs -0`, which is what the
# previous gate tripped over on Windows.

JS_FILES=()
if [ -f "$ADMIN_DIR/app.js" ]; then
    JS_FILES+=("$ADMIN_DIR/app.js")
fi

# Modules top-level.
if [ -d "$ADMIN_DIR/modules" ]; then
    for f in "$ADMIN_DIR/modules"/*.js; do
        [ -f "$f" ] || continue
        JS_FILES+=("$f")
    done
fi

# Modules/pages (the bulk of the admin console).
if [ -d "$ADMIN_DIR/modules/pages" ]; then
    for f in "$ADMIN_DIR/modules/pages"/*.js; do
        [ -f "$f" ] || continue
        JS_FILES+=("$f")
    done
fi

# Any nested subdirectory under modules (defensive — current admin
# uses modules/ and modules/pages/ but a future refactor could nest).
if command -v find >/dev/null 2>&1; then
    while IFS= read -r f; do
        JS_FILES+=("$f")
    done < <(find "$ADMIN_DIR/modules" -type f -name '*.js' 2>/dev/null)
fi

if [ "${#JS_FILES[@]}" -eq 0 ]; then
    fail "no .js files found under $ADMIN_DIR (admin structure missing)"
fi

# Deduplicate in case the find+glob combo produced overlaps.
if command -v awk >/dev/null 2>&1; then
    UNIQUE="$(printf '%s\n' "${JS_FILES[@]}" | awk '!seen[$0]++')"
    JS_FILES=()
    while IFS= read -r f; do
        JS_FILES+=("$f")
    done <<< "$UNIQUE"
fi

pass "enumerated ${#JS_FILES[@]} JS file(s) to check"

# ── 4. Syntax check each file ────────────────────────────────────
#
# We invoke `node --check <file>` per file and capture the exit code.
# `node --check` is a built-in parse-only mode that exits 0 on
# successful parse and non-zero with a diagnostic on failure. We
# suppress stderr to keep the gate quiet on success, but we re-run
# with stderr visible on failure so the operator sees the line +
# column of the bad token.

FAIL_COUNT=0
for f in "${JS_FILES[@]}"; do
    if ! "$NODE_BIN" --check "$f" >/dev/null 2>&1; then
        FAIL_COUNT=$((FAIL_COUNT + 1))
        printf 'FAIL  syntax error in %s\n' "$f" >&2
        # Re-run with stderr visible so the operator can fix the
        # regression without re-invoking the script.
        "$NODE_BIN" --check "$f" || true
    else
        log "ok: $f"
    fi
done

if [ "$FAIL_COUNT" -ne 0 ]; then
    fail "$FAIL_COUNT JS file(s) failed syntax check (see diagnostics above)"
fi

pass "all ${#JS_FILES[@]} JS file(s) parse cleanly under Node $NODE_VERSION"

printf '\nALL ADMIN JS SYNTAX TESTS PASSED\n' >&2
exit 0