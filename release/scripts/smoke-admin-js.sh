#!/usr/bin/env bash
# smoke-admin-js.sh — Side-effect-free Node.js syntax check.
#
# Verifies that every JavaScript file under release/admin parses cleanly:
#   1. release/admin/app.js
#   2. every release/admin/modules/**/*.js
#
# This script is intentionally defensive about finding Node.js. The
# original gate ran
#
#     find release/admin/modules -name "*.js" -print0 | xargs -0 -n1 node --check
#
# which failed on Git Bash for Windows with
# "xargs: node: No such file or directory" because the Cygwin build of
# xargs does not see the Windows PATH (node.exe lives at
# C:\Program Files\nodejs and is invisible to xargs). We do NOT pipe
# through xargs here: we call `node --check <file>` once per file from
# bash itself, with the discovered Node binary, so the spawn happens
# in the same process tree that already sees the Windows PATH.
#
# Node discovery, in order:
#   1. $NODE env var if set (CI override)
#   2. command -v node
#   3. command -v nodejs
#   4. /c/Program Files/nodejs/node.exe       (Git Bash Windows path)
#   5. /c/Program Files (x86)/nodejs/node.exe
#   6. /mnt/c/Program Files/nodejs/node.exe   (WSL path)
#   7. /mnt/c/Program Files (x86)/nodejs/node.exe
#   8. where.exe node                         (Windows CMD probe)
#   9. where.exe nodejs
#  10. powershell.exe -NoProfile -Command "..."  (last-resort Windows probe)
#
# Every Windows probe is normalised: trailing \r and \n are stripped,
# surrounding whitespace is removed, only the first matching line is
# kept, and the path must exist and be executable. If every probe
# fails the script fails closed with a clear message; a fake PASS
# is never produced.
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
# probe_node PATH_VAR <candidate...> — sets PATH_VAR to the first
# candidate that resolves to an executable file. Returns 0 on
# success, 1 on miss. The function deliberately does not echo
# anything; the caller decides how to format the success path.
#
# CRLF normalisation: Windows tools (where.exe, powershell.exe,
# cmd.exe) tend to emit trailing \r on each line. We strip CR
# globally before any "is this path usable" check. A path that
# still contains a CR after the strip is rejected.
probe_node() {
    local __var="$1"; shift
    for candidate in "$@"; do
        # Skip empty / whitespace-only entries.
        if [ -z "$candidate" ] || [ "$candidate" = " " ]; then
            continue
        fi
        # Strip CR (Windows) and surrounding whitespace.
        candidate="${candidate%$'\r'}"
        candidate="$(printf '%s' "$candidate" | sed -E 's/^[[:space:]]+//;s/[[:space:]]+$//')"
        [ -n "$candidate" ] || continue
        # Reject any path that still has CR after the strip —
        # that means the upstream tool fed us something exotic.
        case "$candidate" in *$'\r'*) continue ;; esac
        if [ -x "$candidate" ]; then
            printf '%s' "$candidate" | tee /dev/null > /dev/null
            # shellcheck disable=SC2086
            set -- $candidate
            # Above dance is so we propagate the value cleanly.
            eval "$__var=\"\$candidate\""
            return 0
        fi
    done
    return 1
}

NODE_BIN=""

# Probe 1 — explicit $NODE override.
if [ -n "${NODE:-}" ]; then
    if probe_node NODE_BIN "$NODE"; then
        pass "located node via \$NODE override: $NODE_BIN"
    else
        printf 'WARN  \$NODE=%s set but not executable; falling through to PATH probes\n' "$NODE" >&2
    fi
fi

# Probe 2 — command -v node.
if [ -z "$NODE_BIN" ] && command -v node >/dev/null 2>&1; then
    NODE_BIN="$(command -v node)"
    pass "located node via command -v: $NODE_BIN"
fi

# Probe 3 — command -v nodejs (Debian / Ubuntu symlink alias).
if [ -z "$NODE_BIN" ] && command -v nodejs >/dev/null 2>&1; then
    NODE_BIN="$(command -v nodejs)"
    pass "located nodejs via command -v: $NODE_BIN"
fi

# Probe 4..7 — hard-coded common Windows install locations. We try
# both the Git Bash (/c/...) and WSL (/mnt/c/...) spellings because
# either shell may be the one running this script.
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

# Probe 8..9 — where.exe node / nodejs. where.exe is only present
# when running under a shell that exposes the Windows PATH
# (Git Bash + WSL). CRLF is stripped via probe_node.
if [ -z "$NODE_BIN" ] && command -v where.exe >/dev/null 2>&1; then
    WIN_CANDIDATES=()
    while IFS= read -r line; do
        WIN_CANDIDATES+=("$line")
    done < <(where.exe node 2>/dev/null | tr -d '\r')
    if probe_node NODE_BIN "${WIN_CANDIDATES[@]}"; then
        pass "located node via where.exe: $NODE_BIN"
    fi
fi
if [ -z "$NODE_BIN" ] && command -v where.exe >/dev/null 2>&1; then
    WIN_CANDIDATES=()
    while IFS= read -r line; do
        WIN_CANDIDATES+=("$line")
    done < <(where.exe nodejs 2>/dev/null | tr -d '\r')
    if probe_node NODE_BIN "${WIN_CANDIDATES[@]}"; then
        pass "located nodejs via where.exe: $NODE_BIN"
    fi
fi

# Probe 10..11 — powershell.exe. We invoke Get-Command which
# respects the user's PATH the same way PowerShell does, which is
# strictly more permissive than Git Bash's Cygwin-derived PATH.
# CRLF is stripped. We swallow stderr because PS noise about
# non-zero exit codes pollutes the log; the probe succeeds or
# fails on stdout alone.
if [ -z "$NODE_BIN" ] && command -v powershell.exe >/dev/null 2>&1; then
    PS_CANDIDATES=()
    while IFS= read -r line; do
        PS_CANDIDATES+=("$line")
    done < <(powershell.exe -NoProfile -Command \
        "(Get-Command node -ErrorAction SilentlyContinue).Source" \
        2>/dev/null | tr -d '\r')
    if probe_node NODE_BIN "${PS_CANDIDATES[@]}"; then
        pass "located node via powershell Get-Command: $NODE_BIN"
    fi
fi
if [ -z "$NODE_BIN" ] && command -v powershell.exe >/dev/null 2>&1; then
    PS_CANDIDATES=()
    while IFS= read -r line; do
        PS_CANDIDATES+=("$line")
    done < <(powershell.exe -NoProfile -Command \
        "(Get-Command nodejs -ErrorAction SilentlyContinue).Source" \
        2>/dev/null | tr -d '\r')
    if probe_node NODE_BIN "${PS_CANDIDATES[@]}"; then
        pass "located nodejs via powershell Get-Command: $NODE_BIN"
    fi
fi

if [ -z "$NODE_BIN" ]; then
    fail "node (or nodejs) is not installed and could not be located on this system; install Node.js 18+ from https://nodejs.org and retry"
fi

# Verify the discovered binary actually runs. A path on disk that
# fails to execute is worse than a clear "missing" error.
if ! "$NODE_BIN" --version >/dev/null 2>&1; then
    fail "discovered node binary at '$NODE_BIN' but it does not execute; check the Node install"
fi

NODE_VERSION="$("$NODE_BIN" --version 2>&1 | tr -d '\r\n' || true)"
pass "using node: $NODE_BIN ($NODE_VERSION)"

# ── 3. Enumerate JS files ───────────────────────────────────────
#
# We collect both `release/admin/app.js` and every .js under
# `release/admin/modules/`. Globbing is portable to Git Bash and
# Linux without relying on `find -print0 | xargs -0`, which is what
# the previous gate tripped over on Windows.

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

pass "all ${#JS_FILES[@]} JS file(s) parse cleanly under Node $NODE_VERSION (via $NODE_BIN)"

# ── 5. Banned placeholder strings in production admin JS ────────────
#
# Same contract as smoke-admin-ui.sh §38: the user prompt bans the
# following visible strings from any production admin UI surface.
# Code comments and HTML form `placeholder=` attributes are
# excluded — comments are not visible copy, and form hint
# attributes are standard HTML semantics.
BANNED_PATTERNS_JS=(
    'coming soon'
    'future release'
    'not implemented'
    'unavailable in this build'
    'will be added later'
    'mock'
    'fake'
)
js_bad=""
for pat in "${BANNED_PATTERNS_JS[@]}"; do
    # grep -n then strip // line comments and the body of /* */ block
    # comments; re-grep for the pattern; skip form placeholder= attrs.
    matches=$(grep -rnE "$pat" --include='*.js' --exclude-dir=node_modules \
        --exclude='_planned.js' \
        "$ADMIN_DIR/" 2>/dev/null \
        | sed -E 's|//.*$||g; s|/\*.*\*/||g' \
        | grep -E "$pat" \
        | grep -vE 'placeholder\s*[:=]|placeholder:' \
        || true)
    if [ -n "$matches" ]; then
        js_bad="${js_bad}"$'\n'"  [$pat]:"$'\n'"$matches"
    fi
done
if [ -n "$js_bad" ]; then
    log "  banned strings in admin JS:"
    echo "$js_bad" >&2
    fail "admin JS contains banned placeholder strings — wire the real endpoint or render an honest empty/stub state"
fi
pass "no banned placeholder strings in admin JS"

printf '\nALL ADMIN JS SYNTAX TESTS PASSED\n' >&2
exit 0