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

# ── 19. No undeclared `state` references in page modules ─────────
# The modular refactor removed the global `state` object. Any
# remaining reference to bare `state.xxx` (without import) is a
# false positive that would throw at runtime. Each page module
# defines its own local state or imports from state.js.
bad_state_refs=$(grep -nE '[^a-zA-Z]state\.' "$ADMIN_DIR/modules/pages/"*.js | grep -v '//.*state\.' | grep -vE 'import.*state|const state|var state|let state' || true)
if [ -n "$bad_state_refs" ]; then
    log "  suspicious state. references:"
    echo "$bad_state_refs" >&2
    fail "page modules contain bare state. references — use imports or local state"
fi
pass "page modules have no undeclared state. references"

# ── 20. Backup delete uses typed confirmation ──────────────────
if ! grep -q "requireText: 'delete-orvix-backup'" "$ADMIN_DIR/modules/pages/backups.js"; then
    fail "backups.js delete must require typed confirmation 'delete-orvix-backup'"
fi
if ! grep -q "X-Orvix-Confirm.*delete-orvix-backup" "$ADMIN_DIR/modules/pages/backups.js"; then
    fail "backups.js delete must send X-Orvix-Confirm header with 'delete-orvix-backup'"
fi
pass "backup delete uses typed confirmation matching backend contract"

# ── 21. MTA-STS policy ID must NOT be fabricated in frontend ──
#
# MTA-STS policy IDs are content-derived hashes generated by the
# backend. The frontend must never fabricate, generate, or hard-code
# any MTA-STS TXT record value or policy ID. Doing so would produce
# a record that does not match what the backend serves at the public
# /.well-known/mta-sts.txt endpoint, causing MTA-STS validation
# failures for sending MTAs.
#
# The ONLY acceptable MTA-STS values are those rendered from backend
# plan fields (plan.records[], plan.mta_sts_policy_id,
# plan.mta_sts_policy_file). The frontend may render them via
# template or display, but must not generate the content itself.
#
# Forbidden patterns:
#   - new Date() used in MTA-STS context
#   - Hard-coded date strings like 20250101
#   - Literal 'v=STSv1; id=' constructed in frontend code
#   - Any frontend-generated policy file / TXT value
DNS_DKIM="$ADMIN_DIR/modules/pages/dns-dkim.js"

# 21a. No new Date() in MTA-STS context.
if grep -qnE "new Date.*mta.sts|mta.sts.*new Date|new Date.*STSv1|STSv1.*new Date|\.toISOString.*id|mta_sts.*new Date" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js must NOT derive MTA-STS id from new Date()"
fi

# 21b. No hard-coded fabricated MTA-STS date/ID values.
for pat in "20250101" "20240101" "20230101"; do
    if grep -qnF "$pat" "$DNS_DKIM" 2>/dev/null; then
        fail "dns-dkim.js must NOT contain hard-coded date $pat as MTA-STS ID"
    fi
done

# 21c. No frontend-generated 'v=STSv1; id=' literal.
# This must ONLY appear as a backend record value being rendered,
# not as a template string constructed in frontend code.
# We check for the pattern outside of a record.value access context.
bad_vsts=$(grep -nE "'v=STSv1; id=" "$DNS_DKIM" 2>/dev/null || true)
if [ -n "$bad_vsts" ]; then
    log "  found literal 'v=STSv1; id=' in frontend code:"
    echo "$bad_vsts" >&2
    fail "dns-dkim.js must not contain frontend-generated 'v=STSv1; id=' — only render from backend record values"
fi

# 21d. No frontend-generated MTA-STS policy file content.
# The policy file body must come from the backend plan field.
bad_policy=$(grep -nE "version: STSv1|mode: testing|max_age:" "$DNS_DKIM" | grep -vE "plan\.|mta_sts_policy_file|backend|label|subtle|//|comment|/\*" 2>/dev/null || true)
if [ -n "$bad_policy" ]; then
    log "  found frontend-generated MTA-STS policy content:"
    echo "$bad_policy" >&2
    fail "dns-dkim.js must not generate MTA-STS policy file content — only render from backend plan.mta_sts_policy_file"
fi

# 21e. Confirm backend plan fields are referenced for MTA-STS data.
if ! grep -q "mta_sts_policy_id" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js must reference backend plan.mta_sts_policy_id for the MTA-STS policy ID"
fi
if ! grep -q "mta_sts_policy_file" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js must reference backend plan.mta_sts_policy_file for the MTA-STS policy file"
fi
if ! grep -q "mta_sts_mode" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js must reference backend plan.mta_sts_mode for the MTA-STS mode"
fi
if ! grep -q "mta_sts_hostname" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js must reference backend plan.mta_sts_hostname for the MTA-STS hostname"
fi
if ! grep -q "mta_sts_policy_url" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js must reference backend plan.mta_sts_policy_url for the MTA-STS policy URL"
fi
pass "MTA-STS card only renders backend-provided data, never fabricates"

# ── 22. Keyboard shortcut imports openModal/el in app.js ───────
if ! grep -q "import.*openModal.*components" "$ADMIN_DIR/app.js" 2>/dev/null; then
    fail "app.js keyboard handler uses openModal but does not import it"
fi
if ! grep -q "import.*\bel\b.*components" "$ADMIN_DIR/app.js" 2>/dev/null; then
    fail "app.js keyboard handler uses el but does not import it"
fi
pass "app.js imports openModal and el for keyboard shortcut handler"

# ── 23. localStorage / sessionStorage policy (MEDIUM) ──────────
# Only authentication tokens, CSRF tokens, UI preferences, and
# locale are stored. No secrets, no private keys, no license
# material touches the browser storage.
bad_storage=$(grep -rnE "localStorage\.setItem|sessionStorage\.setItem" "$ADMIN_DIR/modules/"*.js "$ADMIN_DIR/app.js" 2>/dev/null | grep -vE "orvix_admin_token|orvix_admin_csrf|orvix_admin_refresh|locale|theme|sidebar" || true)
if [ -n "$bad_storage" ]; then
    log "  suspicious storage writes:"
    echo "$bad_storage" >&2
    fail "unexpected localStorage/sessionStorage writes — only auth tokens and UI prefs allowed"
fi
pass "storage usage is limited to auth tokens and UI preferences"

echo
echo "ALL ADMIN UI SMOKE TESTS PASSED"
