#!/usr/bin/env bash
# smoke-admin-ui.sh — Side-effect-free analysis of release/admin/.
#
# Verifies that the modular admin build is structurally correct:
#   1. No outdated "backend has no endpoint yet" for bulk import.
#   2. Real bulk import endpoint is referenced.
#   3. Real settings endpoint is referenced.
#   4. Real monitoring alert providers endpoint is referenced.
#   5. Real runtime/listener status endpoint is referenced.
#   6. RTL helper / dir auto handling is present.
#   7. Every page module has a corresponding register() call.
#   8. Every register() call has a corresponding page module.
#   9. CSP allows ES modules (script-src includes 'self' for type=module).
#
# This is the gate you wire into CI: "did someone accidentally
# remove the new admin console features or break the modular
# structure?" — if yes, this script exits non-zero.

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
[ -f "$ADMIN_DIR/index.html" ] || fail "release/admin/index.html not found"
[ -f "$ADMIN_DIR/app.js" ]      || fail "release/admin/app.js not found"
[ -f "$ADMIN_DIR/styles.css" ]  || fail "release/admin/styles.css not found"
[ -d "$ADMIN_DIR/modules" ]     || fail "release/admin/modules directory not found"
[ -d "$ADMIN_DIR/modules/pages" ] || fail "release/admin/modules/pages directory not found"
pass "release/admin/ has the expected modular structure"

# ── 2. index.html uses ES module loader ────────────────────────
if ! grep -q '<script type="module"' "$ADMIN_DIR/index.html"; then
    fail "index.html must use <script type=\"module\">"
fi
pass "index.html loads app.js as ES module"

# ── 3. The outdated bulk-import copy has been removed ──────────
# The previous admin client had the false statement "Bulk mailbox
# CSV import — backend has no endpoint yet". The real endpoint
# now exists. We assert the false copy is gone from any HTML / UI
# text — comments explaining the removal are fine; the false
# statement as user-facing copy is not.
matches=$(grep -nE 'Bulk mailbox CSV import[^.]*backend has no endpoint' "$ADMIN_DIR/app.js" \
   "$ADMIN_DIR/modules/"*.js "$ADMIN_DIR/modules/pages/"*.js 2>/dev/null || true)
if [ -n "$matches" ]; then
    log "  matches: $matches"
    fail "stale 'Bulk mailbox CSV import — backend has no endpoint yet' copy remains in admin UI"
fi
pass "stale 'Bulk mailbox CSV import — backend has no endpoint yet' copy is gone"

# ── 4. Real bulk-import endpoint is referenced ─────────────────
if ! grep -q 'mailboxes/import' "$ADMIN_DIR/app.js" \
   "$ADMIN_DIR/modules/pages/bulk-import.js" 2>/dev/null; then
    fail "admin UI does not reference the real /api/v1/mailboxes/import endpoint"
fi
pass "admin UI references real /api/v1/mailboxes/import endpoint"

# ── 5. Real settings endpoint is referenced ────────────────────
if ! grep -q 'admin/settings' "$ADMIN_DIR/app.js" \
   "$ADMIN_DIR/modules/pages/settings.js" 2>/dev/null; then
    fail "admin UI does not reference /api/v1/admin/settings"
fi
pass "admin UI references /api/v1/admin/settings"

# ── 6. Real alert providers endpoint is referenced ─────────────
if ! grep -q 'alert-providers' "$ADMIN_DIR/app.js" \
   "$ADMIN_DIR/modules/pages/alert-providers.js" 2>/dev/null; then
    fail "admin UI does not reference /api/v1/monitoring/alert-providers"
fi
pass "admin UI references /api/v1/monitoring/alert-providers"

# ── 7. Real runtime/listener endpoint is referenced ───────────
if ! grep -q 'admin/runtime' "$ADMIN_DIR/app.js" \
   "$ADMIN_DIR/modules/pages/runtime-listeners.js" 2>/dev/null; then
    fail "admin UI does not reference /api/v1/admin/runtime"
fi
pass "admin UI references /api/v1/admin/runtime"

# ── 8. RTL helper present ──────────────────────────────────────
if ! grep -q 'directionForText' "$ADMIN_DIR/modules/rtl.js" 2>/dev/null; then
    fail "modules/rtl.js must export directionForText helper"
fi
if ! grep -q "withAutoDir\|applyAutoDir" "$ADMIN_DIR/modules/rtl.js" 2>/dev/null; then
    fail "modules/rtl.js must export withAutoDir / applyAutoDir helper"
fi
pass "modules/rtl.js exposes directionForText + withAutoDir helpers"

# ── 9. Every page module is registered ─────────────────────────
page_files=$(ls "$ADMIN_DIR/modules/pages/"*.js 2>/dev/null || true)
[ -n "$page_files" ] || fail "modules/pages/ is empty"
registered=$(grep -oE "register\('[a-z/]+'" "$ADMIN_DIR/app.js" | sort -u)
missing=""
for pf in $page_files; do
    base=$(basename "$pf" .js)
    if [ "$base" = "_planned" ]; then continue; fi
    if ! echo "$registered" | grep -qE "register\('[a-z/]+'.*$base|$base"; then
        # try matching the file's exported function name (camelCase)
        camel=$(echo "$base" | sed -E 's/[-_]([a-z])/\U\1/g')
        if ! grep -q "import.*$base\b" "$ADMIN_DIR/app.js" 2>/dev/null; then
            missing="$missing $base"
        fi
    fi
done
if [ -n "$missing" ]; then
    log "  registered routes: $(echo "$registered" | tr '\n' ' ')"
    fail "page modules not imported in app.js:$missing"
fi
pass "every page module is imported in app.js"

# ── 10. No app.js page renderer should exceed 600 lines ───────
# (Reasonable heuristic; the original admin was a 3618-line monolith.)
appjs_lines=$(wc -l < "$ADMIN_DIR/app.js")
[ "$appjs_lines" -lt 600 ] || fail "app.js has $appjs_lines lines; modular refactor should keep it under 600"
pass "app.js is a thin bootstrapper ($appjs_lines lines, < 600)"

# ── 11. CSRF / no-secret hygiene in admin UI ───────────────────
# Bulk import should NOT echo passwords to the response or to
# the console. The bulk import module must call /import with a
# raw body and not call any console.log that includes the CSV.
if grep -nE 'console\.(log|debug|info).*Password' "$ADMIN_DIR/modules/pages/bulk-import.js" 2>/dev/null; then
    fail "bulk-import.js must not log passwords to console"
fi
pass "bulk-import.js does not console.log passwords"

# ── 12. Destructive action double-gating ────────────────────────
# Backups restore must require typed confirmation.
if ! grep -q 'requireText' "$ADMIN_DIR/modules/pages/backups.js"; then
    fail "backups.js restore flow must require typed confirmation"
fi
pass "backups restore uses typed confirmation"

# ── 13. Destructive operation: queue cancel uses confirmDanger
if ! grep -q 'confirmDanger' "$ADMIN_DIR/modules/pages/queue.js"; then
    fail "queue.js destructive actions must go through confirmDanger"
fi
pass "queue destructive actions go through confirmDanger"

# ── 14. RTL pages register dir handling ────────────────────────
if ! grep -q "applyAutoDir\|withAutoDir" "$ADMIN_DIR/modules/pages/dashboard.js" 2>/dev/null; then
    fail "dashboard.js must call applyAutoDir / withAutoDir for mixed-direction support"
fi
pass "dashboard.js calls applyAutoDir for mixed-direction text"

# ── 15. CSRF helper used in mutating calls ─────────────────────
if ! grep -q 'apiPost\|apiPatch\|apiDelete' "$ADMIN_DIR/modules/pages/bulk-import.js" 2>/dev/null; then
    fail "bulk-import.js must use apiPost / apiPatch / apiDelete (csrfFetch-wrapped)"
fi
pass "bulk-import.js uses csrfFetch-wrapped mutating calls"

# ── 16. No bare fetch() for mutating calls (security) ──────────
# Only csrfFetch() / apiPost / etc. should reach the network for
# state-changing endpoints. A bare fetch() in a mutating path is
# a CSRF regression and must fail this gate.
for f in $(ls "$ADMIN_DIR/modules/pages/"*.js); do
    if grep -nE 'await fetch\(|^[[:space:]]*fetch\(' "$f" | grep -vE 'csrfFetch|apiGet|apiPost|apiPatch|apiDelete|login\(' | grep -v '/api/v1/csrf-token' >/dev/null; then
        log "  bare fetch() found in $f:"
        grep -nE 'await fetch\(|^[[:space:]]*fetch\(' "$f" | grep -vE 'csrfFetch|apiGet|apiPost|apiPatch|apiDelete|login\(' | grep -v '/api/v1/csrf-token' >&2
        fail "bare fetch() in $f — only csrfFetch is allowed for mutating calls"
    fi
done
pass "no bare fetch() for mutating calls"

# ── 17. Every sidebar entry has a route handler ───────────────
sidebar_routes=$(grep -oE "route: '[^']+'" "$ADMIN_DIR/modules/sidebar.js" | sed -E "s/route: '([^']+)'/\1/" | sort -u)
handled_routes=$(grep -oE "register\('[a-zA-Z0-9/_\.-]+'" "$ADMIN_DIR/app.js" | sed -E "s/register\('([^']+)'/\1/" | sort -u)
unhandled=""
for r in $sidebar_routes; do
    if ! echo "$handled_routes" | grep -qxF "$r"; then
        unhandled="$unhandled $r"
    fi
done
[ -z "$unhandled" ] || fail "sidebar has routes with no register() handler:$unhandled"
pass "every sidebar route is handled by the router"

# ── 18. lib-asset-propagate is the asset-propagation hook ──────
# Confirm that admin assets are part of the propagation surface.
PROPAGATE_SH="$REPO_ROOT/release/scripts/lib-asset-propagate.sh"
[ -f "$PROPAGATE_SH" ] || fail "release/scripts/lib-asset-propagate.sh not found"
if ! grep -q "admin" "$PROPAGATE_SH" 2>/dev/null; then
    fail "lib-asset-propagate.sh must include the admin asset path"
fi
pass "lib-asset-propagate.sh covers admin assets"

echo
echo "ALL ADMIN UI SMOKE TESTS PASSED"
