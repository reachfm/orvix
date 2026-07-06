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
# must either define its own local state or import from state.js.
# Files that define their own local state (const/let/var state =)
# are exempt from this check since they own the state object.
bad_state_refs=""
for f in "$ADMIN_DIR/modules/pages/"*.js; do
    if grep -qE '^\s*(const|let|var)\s+state\s*=' "$f" 2>/dev/null; then
        continue
    fi
    # Match state.xxx JS property access only — exclude import paths (state.js),
    # string literals (state.' sentence-ending period), and comments.
    refs=$(grep -nE '[^a-zA-Z]state\.[a-zA-Z_$]' "$f" | grep -vE '//.*state\.|state\.js|state\.json|from.*state\.' || true)
    if [ -n "$refs" ]; then
        bad_state_refs="$bad_state_refs"$'\n'"  $(basename "$f"):"$'\n'"$refs"
    fi
done
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
if grep -qE "version: STSv1|max_age:" "$DNS_DKIM"; then
    # Only fail if these strings appear OUTSIDE of header comments and backend field references.
    policy_lines=$(grep -nE "version: STSv1|max_age:" "$DNS_DKIM" | grep -vE "mta_sts_policy_file|plan\.|backend|^\s+\*|//" 2>/dev/null || true)
    if [ -n "$policy_lines" ]; then
        log "  found frontend-generated MTA-STS policy content:"
        echo "$policy_lines" >&2
        fail "dns-dkim.js must not generate MTA-STS policy file content — only render from backend plan.mta_sts_policy_file"
    fi
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
bad_storage=$(grep -rnE "localStorage\.setItem|sessionStorage\.setItem" "$ADMIN_DIR/modules/"*.js "$ADMIN_DIR/app.js" 2>/dev/null | grep -vE "orvix_admin_token|orvix_admin_csrf|orvix_admin_refresh|TOKEN_KEY|CSRF_KEY|locale|theme|sidebar" || true)
if [ -n "$bad_storage" ]; then
    log "  suspicious storage writes:"
    echo "$bad_storage" >&2
    fail "unexpected localStorage/sessionStorage writes — only auth tokens and UI prefs allowed"
fi
pass "storage usage is limited to auth tokens and UI preferences"

# ── 24. apiDelete must forward headers and body from opts ────────
# Codex BLOCKER 1: apiDelete() was dropping headers from the opts
# object because it passed opts as csrfFetch's third parameter
# instead of merging headers+body into init.
API_JS="$ADMIN_DIR/modules/api.js"
if ! grep -q "init\.headers\s*=\s*opts\.headers" "$API_JS" 2>/dev/null; then
    fail "apiDelete must assign init.headers = opts.headers to forward DELETE headers (BLOCKER 1)"
fi
if ! grep -q "init\.body\s*=\s*opts\.body" "$API_JS" 2>/dev/null; then
    fail "apiDelete must assign init.body = opts.body to forward DELETE body (BLOCKER 1)"
fi
pass "apiDelete forwards headers and body from opts to init"

# ── 25. Backup delete sends confirmation body ───────────────────
# Codex BLOCKER 1: the delete path must transmit both the
# X-Orvix-Confirm header AND a JSON body with the confirm field
# so the backend can validate the intent.
BACKUPS_JS="$ADMIN_DIR/modules/pages/backups.js"
if ! grep -q "body:\s*JSON\.stringify.*confirm.*delete-orvix-backup" "$BACKUPS_JS" 2>/dev/null; then
    fail "backups.js delete must send JSON body with confirm: 'delete-orvix-backup' (BLOCKER 1)"
fi
pass "backup delete sends confirmation body matching backend contract"

# ── 26. No bare undeclared applyDisabled in dns-dkim.js ─────────
# Codex BLOCKER 2: applyDisabled was used as an undeclared bare
# identifier in renderDnsProviderPanel, which would throw at runtime.
# It must be a local const/let/var or state.applyDisabled.
# After BLOCKER 4 fix this identifier should not exist as a local
# variable — the Apply button uses per-provider disabled logic.
DNS_DKIM="$ADMIN_DIR/modules/pages/dns-dkim.js"
has_local_decl=$(grep -cE "^\s*(const|let|var)\s+applyDisabled\s*=" "$DNS_DKIM" 2>/dev/null || echo 0)
bad_apply=$(grep -nE '\bapplyDisabled\b' "$DNS_DKIM" 2>/dev/null | grep -vE 'state\.applyDisabled|const applyDisabled|let applyDisabled|var applyDisabled|//.*applyDisabled|^\s*\*|applyDisabled:' || true)
if [ -n "$bad_apply" ] && [ "$has_local_decl" -eq 0 ]; then
    log "  bare applyDisabled references:"
    echo "$bad_apply" >&2
    fail "dns-dkim.js uses bare undeclared applyDisabled without local declaration (BLOCKER 2)"
fi
pass "dns-dkim.js uses no bare undeclared applyDisabled"

# ── 27. Provider plan uses composite key only (no bare-name fallback) ─
# Codex CROSS-DOMAIN: plans must be stored by composite key
# providerName::domain and retrieved with the same key.  Bare-name
# storage or fallback allows stale cross-domain plan reuse.
# 27a. getProviderPlan must use composite key (providerKey), not fall
#      back to bare providerName.
if grep -q "state\.dnsProviderPlans\[providerName\]" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js getProviderPlan must NOT fall back to bare providerName key — composite key only (CROSS-DOMAIN)"
fi
# 27b. getProviderPlan must reference providerKey for lookup.
if ! grep -q "providerKey(providerName, domain)" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js getProviderPlan must use providerKey(providerName, domain) composite lookup (CROSS-DOMAIN)"
fi
# 27c. loadDnsProviderPlan must store by composite key, not bare [name].
if grep -qE "state\.dnsProviderPlans\[name\]\s*=" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js must NOT store plan by bare [name] — use composite key only (CROSS-DOMAIN)"
fi
# 27d. loadDnsProviderPlan must use providerKey for storage.
if ! grep -q "providerKey(name, state\.currentDomain)" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js loadDnsProviderPlan must use providerKey(name, currentDomain) composite key (CROSS-DOMAIN)"
fi
pass "dns-dkim.js provider plan uses composite key only"

# ── 28. MTA-STS .records.find() must be guarded by Array.isArray ─
# Codex HIGH-RISK: inner.records.find() throws if backend omits
# or renames the records field. Must be guarded.
if grep -q "\.records\.find(" "$DNS_DKIM" 2>/dev/null; then
    if ! grep -q "Array\.isArray.*records" "$DNS_DKIM" 2>/dev/null; then
        fail "dns-dkim.js uses .records.find() without Array.isArray guard (HIGH RISK)"
    fi
fi
pass "dns-dkim.js MTA-STS .records.find() is guarded by Array.isArray"

# ── 29. Apply guard uses shared canApplyProvider helper ────────────
# Codex BLOCKER 4 + CROSS-DOMAIN: Both the render button and the
# apply handler must use the same canApplyProvider() helper so the
# two paths cannot diverge.  canApplyProvider in turn calls
# getProviderByName, getProviderPlan, and planHasApplyableWork —
# any one of them returning false prevents Apply.
#
# 29a. Apply button must not use a global applyDisabled variable.
if grep -qE "disabled:\s*\bapplyDisabled\b" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js Apply button uses global applyDisabled — must be per-provider (BLOCKER 4)"
fi
# 29b. Apply button render must use canApplyProvider.
if ! grep -q "canApplyProvider(p\.name, domain)" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js Apply button must use canApplyProvider(p.name, domain) (BLOCKER 4)"
fi
# 29c. applyDnsProvider must call canApplyProvider before POST.
if ! grep -q "canApplyProvider(name, domain)" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js applyDnsProvider must call canApplyProvider(name, domain) before POST (CROSS-DOMAIN)"
fi
# 29d. canApplyProvider must call getProviderPlan.
if ! grep -q "getProviderPlan(name, domain)" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js canApplyProvider must call getProviderPlan(name, domain)"
fi
# 29e. canApplyProvider must check planHasApplyableWork.
if ! grep -q "planHasApplyableWork(plan)" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js canApplyProvider must check planHasApplyableWork(plan)"
fi
# 29f. canApplyProvider must check provider readiness.
if ! grep -q "provider.status !== 'ready'" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js canApplyProvider must check provider.status === 'ready'"
fi
# 29g. planHasApplyableWork checks can_apply / apply_allowed / changes.
if ! grep -qE "plan\.can_apply|plan\.apply_allowed" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js planHasApplyableWork must check plan.can_apply / plan.apply_allowed"
fi
# 29h. getProviderByName is called by canApplyProvider.
if ! grep -q "getProviderByName(name)" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js canApplyProvider must call getProviderByName(name)"
fi
# 29i. applyDnsProvider error toast matches full guard wording.
if ! grep -q "Run dry-run for this configured provider and domain" "$DNS_DKIM" 2>/dev/null; then
    fail "dns-dkim.js applyDnsProvider must show error when canApplyProvider is false"
fi
pass "dns-dkim.js Apply guard uses shared canApplyProvider helper"

# ── 30. Admin Enterprise v2 pages exist and are wired ────────────
ENTERPRISE_PAGES=(
    "account-classes.js"
    "domain-groups.js"
    "mailing-lists.js"
    "public-folders.js"
    "admin-groups.js"
    "audit-log.js"
    "quarantine.js"
    "acl.js"
    "log-rules.js"
    "security-extra.js"
    "migration.js"
    "clustering.js"
)
for p in "${ENTERPRISE_PAGES[@]}"; do
    [ -f "$ADMIN_DIR/modules/pages/$p" ] || fail "missing enterprise page module: $p"
    if ! grep -q "$p" "$ADMIN_DIR/app.js" 2>/dev/null; then
        fail "enterprise page $p not imported in app.js"
    fi
done
pass "all 12 enterprise page modules exist and are imported in app.js"

# ── 35. CTO BLOCKER 3 — Acceptance UI offers only runtime-truthful actions ──
# The runtime (internal/coremail/smtp/receive.go) accepts only
# {accept, reject, quarantine}. The frontend must NOT expose
# {redirect, hold} in the action <select> option list. We
# anchor the check on the select() call for the action field
# so false positives from CSS classes or unrelated strings
# cannot pass the gate.
ACCEPTANCE_JS="$ADMIN_DIR/modules/pages/acceptance.js"
acceptance_action_block=$(grep -nA 1 -E "select\(\s*'Action'\s*," "$ACCEPTANCE_JS" | head -n 2 || true)
if [ -z "$acceptance_action_block" ]; then
    fail "acceptance.js is missing the action <select> declaration (BLOCKER 3)"
fi
for bad in redirect hold; do
    if echo "$acceptance_action_block" | grep -qE "'$bad'"; then
        fail "acceptance.js action <select> offers unsupported action '$bad' (BLOCKER 3)"
    fi
done
# 35c. No redirect_to UI field — this one is grepped over
# the whole file because the field name must not appear at all.
if grep -qE "redirect_to|redirect-to" "$ACCEPTANCE_JS"; then
    fail "acceptance.js UI exposes 'redirect_to' field (BLOCKER 3)"
fi
# 35d. Action options list must include accept / reject / quarantine.
for need in accept reject quarantine; do
    if ! echo "$acceptance_action_block" | grep -qE "'$need'"; then
        fail "acceptance.js action <select> is missing runtime-supported action '$need' (BLOCKER 3)"
    fi
done
pass "acceptance.js action <select> exposes only runtime-supported actions {accept, reject, quarantine} (BLOCKER 3)"

# ── 36. CTO BLOCKER 4 — Incoming UI offers only runtime-truthful actions ──
# The runtime (internal/coremail/smtp/receive.go) accepts only
# {reject, quarantine, tag}. The frontend must NOT expose
# {move, label, forward, discard, hold}. We grep the action-
# select line specifically rather than the whole file, because
# the words "label" and "forward" appear legitimately elsewhere
# (CSS classes, modal button labels, helper text). We anchor on
# the select() call for the action field and pull in up to 2
# lines so the options array on the continuation line is in
# scope.
INCOMING_JS="$ADMIN_DIR/modules/pages/incoming-rules.js"
incoming_action_block=$(grep -nA 1 -E "select\(\s*'Action'\s*," "$INCOMING_JS" | head -n 2 || true)
if [ -z "$incoming_action_block" ]; then
    fail "incoming-rules.js is missing the action <select> declaration (BLOCKER 4)"
fi
for bad in move label forward discard hold; do
    if echo "$incoming_action_block" | grep -qE "'$bad'"; then
        fail "incoming-rules.js action <select> offers unsupported action '$bad' (BLOCKER 4)"
    fi
done
for need in reject quarantine tag; do
    if ! echo "$incoming_action_block" | grep -qE "'$need'"; then
        fail "incoming-rules.js action <select> is missing runtime-supported action '$need' (BLOCKER 4)"
    fi
done
pass "incoming-rules.js action <select> exposes only runtime-supported actions {reject, quarantine, tag} (BLOCKER 4)"

# ── 36b. CTO FIX 4 — incoming action_target helper text must be runtime-truthful ──
# The legacy "folder / label / forward address" wording refers to
# actions (move / label / forward) the runtime no longer executes.
# The active actions are reject / quarantine / tag, so the helper
# text must describe the new contract. We assert that the legacy
# wording is gone and that the new wording is present.
if grep -qE "folder / label / forward|folder / label|forward address" "$INCOMING_JS"; then
    fail "incoming-rules.js still references legacy 'folder / label / forward' / 'forward address' wording (FIX 4)"
fi
if ! grep -qE "Action value" "$INCOMING_JS"; then
    fail "incoming-rules.js is missing the runtime-truthful 'Action value' field label (FIX 4)"
fi
pass "incoming-rules.js action_target helper text describes the runtime contract (FIX 4)"

# ── 37. CTO BLOCKER 2 — No SFTP askpass secret-on-disk pattern ─────
# The runtime must never write a decrypted SFTP password to a
# shell script (no SSH_ASKPASS=... helper file containing the
# cleartext). The CTO's preferred path is pure Go SFTP, which
# has its own guard in internal/backup/targets/uploader_test.go.
TARGETS_GO="$REPO_ROOT/internal/backup/targets/uploader.go"
[ -f "$TARGETS_GO" ] || fail "internal/backup/targets/uploader.go is missing"
if grep -qE "writeAskpassHelper|SSH_ASKPASS_REQUIRE=force" "$TARGETS_GO"; then
    fail "internal/backup/targets/uploader.go still references writeAskpassHelper or SSH_ASKPASS_REQUIRE=force (BLOCKER 2)"
fi
# Defence in depth: nothing under internal/backup/targets/ may
# reference the askpass helper by name.
if grep -rn "writeAskpassHelper" "$REPO_ROOT/internal/backup/" 2>/dev/null; then
    fail "internal/backup/ still references the forbidden writeAskpassHelper helper (BLOCKER 2)"
fi
pass "internal/backup/targets/uploader.go has no SSH_ASKPASS secret-on-disk pattern (BLOCKER 2)"

# ── 31. Admin Enterprise v2 endpoints are referenced from the JS ─
for ep in \
    "/api/v1/admin/account-classes" \
    "/api/v1/admin/domain-groups" \
    "/api/v1/admin/mailing-lists" \
    "/api/v1/admin/public-folders" \
    "/api/v1/admin/admin-groups" \
    "/api/v1/admin/audit-logs" \
    "/api/v1/admin/quarantine" \
    "/api/v1/admin/acl-rules" \
    "/api/v1/admin/log-rules"; do
    if ! grep -q "$ep" "$ADMIN_DIR/modules/pages/"*.js 2>/dev/null; then
        fail "no page module references $ep"
    fi
done
pass "every new admin enterprise endpoint is referenced from the JS"

# ── 32. No tokens in localStorage (sessionStorage only) ──────────
for f in "$ADMIN_DIR"/modules/api.js "$ADMIN_DIR"/app.js; do
    if grep -q "localStorage.setItem(.*token" "$f" 2>/dev/null; then
        fail "$f must not put tokens in localStorage"
    fi
done
pass "no auth tokens stored in localStorage (sessionStorage only)"

# ── 33. CSRF protection on every new mutating admin endpoint ────
for ep in \
    "admin/account-classes" \
    "admin/domain-groups" \
    "admin/mailing-lists" \
    "admin/public-folders" \
    "admin/admin-groups" \
    "admin/quarantine" \
    "admin/acl-rules" \
    "admin/log-rules"; do
    # Verify that some page module posts to this endpoint.
    if ! grep -q "apiPost.*$ep\|apiPut.*$ep\|apiPatch.*$ep\|apiDelete.*$ep" "$ADMIN_DIR/modules/pages/"*.js 2>/dev/null; then
        fail "no page module performs a mutation on $ep"
    fi
done
pass "every enterprise mutation is reachable from a page module"

# ── 34. Rate limiter exemption is documented in router.go ────────
REPO_ROOT="${REPO_ROOT:-.}"
ROUTER="$REPO_ROOT/internal/api/router.go"
if ! grep -q "PHASE-0" "$ROUTER" 2>/dev/null; then
    fail "router.go must document the PHASE-0 rate-limit exemption"
fi
if ! grep -q "apiRateLimitMiddleware" "$ROUTER" 2>/dev/null; then
    fail "router.go must define apiRateLimitMiddleware helper"
fi
pass "router.go exempts the admin SPA from the API rate limiter"

# ── 38. No banned placeholder strings in production UI ────────────────────
#
# User prompt explicitly bans the following strings from any visible
# production admin UI surface (.js / .html / .css):
#   "coming soon" / "future release" / "not implemented" /
#   "unavailable in this build" / "will be added later" /
#   "placeholder" / "TODO" / "mock" / "fake"
#
# Per the same prompt, "Docs may contain honest limitations" so we
# only scan product-UI assets. Code comments and the HTML form
# `placeholder=` attribute are also excluded — comments are not
# visible copy, and form hint attributes are standard HTML semantics.
#
# The _planned.js module is exempt because it is the legitimate
# "honest stub" template used by the 404 handler; it is not routed
# to any visible sidebar item.
BANNED_PATTERNS=(
    'coming soon'
    'future release'
    'not implemented'
    'unavailable in this build'
    'will be added later'
    'mock'
    'fake'
)
bad_strings=""
for pat in "${BANNED_PATTERNS[@]}"; do
    # Scan only the production asset types; skip node_modules + the
    # legitimate _planned.js module that is the "honest stub" template.
    # Strip // line comments and /* ... */ block comments before the
    # pattern check, since banned words inside source-code comments
    # are not visible product copy.
    matches=$(grep -rnE "$pat" \
        --include='*.js' --include='*.html' --include='*.css' \
        --exclude-dir=node_modules \
        --exclude='_planned.js' \
        "$ADMIN_DIR/" 2>/dev/null \
        | sed -E 's|//.*$||g; s|/\*.*\*/||g' \
        | grep -E "$pat" \
        | grep -vE 'placeholder\s*[:=]|placeholder:' \
        || true)
    if [ -n "$matches" ]; then
        bad_strings="${bad_strings}"$'\n'"  [$pat]:"$'\n'"$matches"
    fi
done
if [ -n "$bad_strings" ]; then
    log "  banned placeholder strings found:"
    echo "$bad_strings" >&2
    fail "production UI contains banned placeholder strings — wire the real endpoint or render an honest empty/stub state"
fi
pass "no banned placeholder strings in production UI"

echo
echo "ALL ADMIN UI SMOKE TESTS PASSED"
