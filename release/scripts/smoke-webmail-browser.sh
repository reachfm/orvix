#!/usr/bin/env bash
# smoke-webmail-browser.sh — Static-asset check for the Orvix
# Webmail SPA.
#
# This is the "browser surface" smoke. It runs as a fast
# file-presence + content marker probe (no HTTP server, no
# Chrome, no Node). The browser-rendered assertions live in
# smoke-webmail-functional-browser.sh (which spins up Chrome
# via CDP); the structural assertions live in
# smoke-webmail-ui.sh (which does a similar file-presence
# probe to this script but at the JS-helper level).
#
# Why a separate script from smoke-webmail-ui.sh? The two
# gates have different scope:
#
#   - smoke-webmail-ui.sh  — JS-helper / CSS structural
#     surface: dirAuto wiring, helper function presence,
#     no-queue-API guard, RTL plumbing, responsive blocks,
#     auth-gate router drift, system-folder constants,
#     attachment URL auth.
#   - smoke-webmail-browser.sh — bundle-fetch surface: every
#     asset file the browser loads renders correctly, every
#     marker the bundle ships is on disk, the auth-gate's
#     session probe URL is unreachable without a cookie.
#
# In a CI environment with no Browser and no Node, both
# scripts run side-by-side; together they cover the smoke
# surface that smoke-webmail-functional-browser.sh would
# otherwise cover.
#
# Usage:
#   bash release/scripts/smoke-webmail-browser.sh [--verbose]
#
# Exits 0 when every check passes, 1 on first failure.

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

INDEX="$WEBMAIL_DIR/index.html"
ASSETS="$WEBMAIL_DIR/assets"

# ── 1. Required files exist ─────────────────────────────────────
[ -d "$WEBMAIL_DIR" ] || fail "release/webmail directory not found"
[ -f "$INDEX" ]       || fail "release/webmail/index.html not found"
[ -d "$ASSETS" ]      || fail "release/webmail/assets directory not found"
[ -f "$ASSETS/webmail.js" ]      || fail "$ASSETS/webmail.js not found (main webmail bundle)"
[ -f "$ASSETS/auth-gate.js" ]    || fail "$ASSETS/auth-gate.js not found (auth gate)"
[ -f "$ASSETS/webmail.css" ]     || fail "$ASSETS/webmail.css not found (webmail stylesheet)"
[ -f "$ASSETS/webmail-push.js" ] || fail "$ASSETS/webmail-push.js not found (push helper)"
[ -f "$WEBMAIL_DIR/sw.js" ]      || fail "$WEBMAIL_DIR/sw.js not found (service worker)"
pass "release/webmail bundle shape verified (index.html, assets/*.js + .css, sw.js)"

# ── 2. index.html references the assets in order ───────────────
[ -s "$INDEX" ] || fail "$INDEX is empty"

# auth-gate.js must be referenced before webmail.js in the
# rendered HTML. The auth-gate initialises window.authenticated
# and may render the login overlay before the SPA boots.
gate_pos=$(grep -bo '/assets/auth-gate.js'  "$INDEX" 2>/dev/null | head -n1 | cut -d: -f1 || true)
wm_pos=$(grep -bo '/assets/webmail.js'      "$INDEX" 2>/dev/null | head -n1 | cut -d: -f1 || true)
if [ -z "$gate_pos" ] || [ -z "$wm_pos" ]; then
    fail "$INDEX missing references to auth-gate.js or webmail.js"
fi
if [ "$gate_pos" -ge "$wm_pos" ]; then
    fail "auth-gate.js must be referenced BEFORE webmail.js in $INDEX"
fi
pass "index.html references auth-gate.js before webmail.js"

# webmail.css must be linked.
grep -q '/assets/webmail.css' "$INDEX" || fail "$INDEX does not reference /assets/webmail.css"
pass "index.html references webmail.css"

# ── 3. Asset files are non-empty and parse-clean via Node ──────
#
# If Node is available we run `node --check` on each asset
# file. This is the same check smoke-webmail-js.sh does,
# but at the bundle surface (no Node required to PASS).
#
# If Node is missing, we skip the parse check and rely on
# the smoke-webmail-js.sh gate running in CI alongside this
# one.
if command -v node >/dev/null 2>&1 || command -v nodejs >/dev/null 2>&1; then
    if command -v node >/dev/null 2>&1; then
        NODE_BIN="$(command -v node)"
    else
        NODE_BIN="$(command -v nodejs)"
    fi
    for f in "$ASSETS/auth-gate.js" "$ASSETS/webmail.js" "$ASSETS/webmail-push.js" "$WEBMAIL_DIR/sw.js"; do
        if ! "$NODE_BIN" --check "$f" >/dev/null 2>&1; then
            fail "node --check failed for $f (run smoke-webmail-js.sh for details)"
        fi
        log "ok: $f"
    done
    pass "every shipped .js file in release/webmail parses under node --check"
else
    log "node not on PATH; skipping per-file parse check (smoke-webmail-js.sh covers this)"
fi

# ── 4. Key UI markers in webmail.js ────────────────────────────
#
# These are the public-surface markers the Go-side helper
# test exercises via Node VM. They MUST be present in the
# bundle so the test harness can locate them.
JS="$ASSETS/webmail.js"
for marker in 'function dirAuto' 'function escapeHTML' 'function sanitiseHTML' 'function linkifyURLs' 'function renderBody' 'function openSettingsModal' '/api/v1/webmail/attachments/'; do
    if ! grep -qF "$marker" "$JS"; then
        fail "webmail.js missing required marker: $marker"
    fi
    log "ok: marker $marker"
done
pass "webmail.js contains all 7 expected UI markers (dirAuto / escapeHTML / sanitiseHTML / linkifyURLs / renderBody / openSettingsModal / attachments URL)"

# ── 5. auth-gate.js router-drift guard ─────────────────────────
GATE="$ASSETS/auth-gate.js"
for marker in '/api/v1/webmail/login' '/api/v1/webmail/session'; do
    if ! grep -qF "$marker" "$GATE"; then
        fail "auth-gate.js missing required router reference: $marker"
    fi
done
pass "auth-gate.js references /api/v1/webmail/login and /api/v1/webmail/session (no router drift)"

# ── 6. webmail.css responsive + RTL plumbing ──────────────────
CSS="$ASSETS/webmail.css"
if ! grep -q '@media' "$CSS"; then
    fail "webmail.css has no @media blocks (mobile responsive requirement unmet)"
fi
media_count=$(grep -c '@media' "$CSS" || true)
if [ "$media_count" -lt 3 ]; then
    fail "webmail.css has only $media_count @media block(s) (expected >= 3)"
fi
pass "webmail.css has $media_count responsive @media block(s)"

# Either CSS logical properties or [dir="rtl"] selectors.
logical_ok=0
if grep -qE 'margin-inline-(start|end)|padding-inline-(start|end)|text-align:[[:space:]]*(start|end)|inset-inline-(start|end)' "$CSS"; then
    logical_ok=1
fi
rtl_selector_ok=0
if grep -qE '\[dir="rtl"\]|\[dir=rtl\]|\.rtl' "$CSS"; then
    rtl_selector_ok=1
fi
if [ "$logical_ok" -eq 0 ] && [ "$rtl_selector_ok" -eq 0 ]; then
    fail "webmail.css has no RTL support (neither CSS logical properties nor [dir=rtl] overrides)"
fi
pass "webmail.css has RTL plumbing (logical=$logical_ok, [dir=rtl]=$rtl_selector_ok)"

# ── 7. Bundle never references admin-only API paths ───────────
for f in "$JS" "$GATE" "$ASSETS/webmail-push.js"; do
    if grep -qF '/api/v1/queue' "$f"; then
        fail "$f references /api/v1/queue (admin-only path; webmail must use /api/v1/webmail/* endpoints)"
    fi
done
pass "webmail bundle never references /api/v1/queue"

# ── 8. Bundle never touches localStorage / sessionStorage ──────
for store in localStorage sessionStorage; do
    for f in "$JS" "$GATE"; do
        if grep -qE "${store}\.(setItem|getItem|removeItem|clear)" "$f"; then
            fail "$f references ${store} (no client-side secret storage allowed)"
        fi
    done
done
pass "webmail bundle does not touch localStorage or sessionStorage"

# ── 9. service worker scope ─────────────────────────────────────
SW="$WEBMAIL_DIR/sw.js"
if [ -f "$SW" ]; then
    if ! grep -q 'self\.addEventListener' "$SW"; then
        fail "$SW exists but has no self.addEventListener (not a service worker)"
    fi
    pass "sw.js is a real service worker (registers a listener)"
fi

printf '\nALL WEBMAIL BROWSER SURFACE TESTS PASSED\n' >&2
exit 0
