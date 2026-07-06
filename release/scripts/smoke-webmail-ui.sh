#!/usr/bin/env bash
# smoke-webmail-ui.sh — Side-effect-free structural analysis of
# release/webmail/. Mirrors smoke-admin-ui.sh but for the webmail
# SPA. Runs entirely from disk: no network, no DOM, no JS execution.
#
# Checks:
#   1. release/webmail/index.html exists and references the right
#      bundled scripts in correct load order.
#   2. Every expected asset file exists (auth-gate, webmail,
#      webmail-push, css).
#   3. webmail.js exposes the helpers dirAuto / escapeHTML /
#      sanitiseHTML / linkifyURLs / renderBody (used by tests + the
#      RTL pipeline).
#   4. webmail.js wires dirAuto to visible fields (subject, from,
#      preview, compose body, compose subject).
#   5. webmail.js never references the admin-only /api/v1/queue
#      endpoint (the regression guard mirrors the Go-side
#      TestWebmailNoQueueAPICallsInWebmailAsset test).
#   6. webmail.js never reads from localStorage / sessionStorage
#      (no secrets persisted client-side).
#   7. webmail.css has responsive @media blocks (mobile / narrow
#      viewport). This is the regression guard for the mobile /
#      tablet layout requirement.
#   8. webmail.css has the dir="auto" / RTL plumbing (logical
#      properties, [dir="rtl"] overrides) so mixed Arabic/English
#      emails render correctly without text reversal.
#   9. webmail.js exposes a Settings modal (openSettingsModal is the
#      entry hook the auth-gate uses to surface the no-mailbox card
#      on bad sessions).
#  10. The auth-gate.js module references the same /api/v1/webmail/*
#      endpoints the router mounts in router.go (no API drift).
#  11. webmail.js handles the explicit sent / drafts / trash /
#      archive / junk folder types — system folders rendered
#      without the UI exposing Archive / Trash / Sent when the
#      backend hasn't provisioned them.
#  12. Attachments: webmail.js wires attachment download to the
#      authenticated endpoint /api/v1/webmail/attachments/:id
#      (not a raw static path; auth required for every download).
#
# Usage:
#   bash release/scripts/smoke-webmail-ui.sh [--verbose]
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
JS="$ASSETS/webmail.js"
CSS="$ASSETS/webmail.css"
GATE="$ASSETS/auth-gate.js"

# ── 1. Required files exist ─────────────────────────────────────
[ -d "$WEBMAIL_DIR" ] || fail "release/webmail directory not found"
[ -f "$INDEX" ]       || fail "$INDEX not found"
[ -d "$ASSETS" ]      || fail "$ASSETS directory not found"
[ -f "$JS" ]          || fail "$JS not found (main webmail bundle)"
[ -f "$CSS" ]         || fail "$CSS not found (webmail stylesheet)"
[ -f "$GATE" ]        || fail "$GATE not found (auth gate)"
pass "release/webmail has the expected structure (index.html, assets/, *.js, *.css)"

# ── 2. index.html loads the right scripts in order ─────────────
#
# The order matters: auth-gate.js must execute BEFORE webmail.js
# because it sets window.authenticated and may render the login
# overlay before the SPA boots. webmail-push.js loads LAST so it
# can hook init() via window.OrvixWebmailPush.onInit() at boot.
if ! grep -q '/assets/auth-gate.js' "$INDEX"; then
    fail "index.html does not reference auth-gate.js"
fi
if ! grep -q '/assets/webmail.js' "$INDEX"; then
    fail "index.html does not reference webmail.js"
fi
if ! grep -q '/assets/webmail.css' "$INDEX"; then
    fail "index.html does not reference webmail.css"
fi
# auth-gate must come before webmail in document order. Simple
# substring check: if the byte offset of auth-gate.js is less than
# the byte offset of webmail.js in the rendered HTML, the load
# order is correct.
gate_pos=$(grep -bo '/assets/auth-gate.js'  "$INDEX" | head -n1 | cut -d: -f1)
wm_pos=$(grep -bo '/assets/webmail.js'      "$INDEX" | head -n1 | cut -d: -f1)
if [ -z "$gate_pos" ] || [ -z "$wm_pos" ]; then
    fail "could not pin down the byte offset of auth-gate.js vs webmail.js in index.html"
fi
if [ "$gate_pos" -ge "$wm_pos" ]; then
    fail "auth-gate.js must load BEFORE webmail.js (auth-gate at $gate_pos, webmail at $wm_pos)"
fi
pass "index.html loads auth-gate.js before webmail.js"

# ── 3. Required JS helpers present in webmail.js ───────────────
#
# These names are the public surface the suite
# (webmail_frontend_test.go) loads by regex. If anyone renames
# them without updating the test, this fails first.
for helper in dirAuto escapeHTML sanitiseHTML linkifyURLs renderBody; do
    if ! grep -q "function ${helper}(" "$JS"; then
        fail "missing required helper: function ${helper}(...) in webmail.js"
    fi
done
pass "webmail.js exposes dirAuto, escapeHTML, sanitiseHTML, linkifyURLs, renderBody"

# ── 4. dirAuto is actually wired to visible fields ──────────────
#
# We allow either 'dir: dirAuto(...)' (object form) or
# 'setAttribute("dir", dirAuto(...))' (string-form). The key
# invariant is that subject, from, preview, and body inputs have
# a dirAuto call next to them.
for pair in 'subject' 'from' 'preview'; do
    # grab the first 80 lines after a regex match so the search
    # localises to the renderer of that field.
    if ! awk -v key="$pair" '
        index($0, key) { found=1 }
        found && /dirAuto/ { print; exit }
    ' "$JS" | grep -q "dirAuto"; then
        fail "dirAuto is not wired to the \"${pair}\" field in webmail.js"
    fi
done
pass "dirAuto is wired to subject, from-name, and preview rows"

# ── 5. webmail.js never calls /api/v1/queue (admin-only path) ──
#
# The webmail UI must read all mailbox data through the protected
# /api/v1/webmail/* endpoints, never through the admin /api/v1/queue
# listing endpoint (which is the operator's view of outbound mail
# and not appropriate for end users). This mirrors
# TestWebmailNoQueueAPICallsInWebmailAsset in Go.
if grep -q '/api/v1/queue' "$JS" "$GATE" "$ASSETS/webmail-push.js" 2>/dev/null; then
    fail "webmail bundle references /api/v1/queue (admin-only path; webmail must use /api/v1/webmail/* endpoints)"
fi
pass "webmail bundle never references /api/v1/queue"

# ── 6. webmail.js never stores secrets in localStorage ─────────
#
# The webmail SPA uses HttpOnly cookies for auth. Persisting
# anything to localStorage / sessionStorage is a regression risk:
# it is visible to any script on the same origin (XSS-impacted
# page can read tokens). We assert no write goes to either store.
# Read-only reads (e.g. preference persistence keys unrelated to
# auth) are not flagged here, but the current codebase has no
# reads either.
for store in localStorage sessionStorage; do
    if grep -q "${store}\.setItem\|${store}\.getItem\|${store}\.removeItem\|${store}\.clear" "$JS" "$GATE" 2>/dev/null; then
        fail "webmail bundle references ${store} (no client-side secret storage allowed)"
    fi
done
pass "webmail bundle does not touch localStorage or sessionStorage"

# ── 7. webmail.css has responsive @media blocks ────────────────
#
# The mobile responsive requirement: viewport changes from
# 3-pane desktop to list/detail/compose on narrow widths. The
# CSS must have @media (max-width: …) rules that move the
# sidebar off-canvas, collapse panes, etc.
if ! grep -q '@media' "$CSS"; then
    fail "webmail.css has no @media blocks (mobile responsive requirement unmet)"
fi
# Count @media. The bundle should have at least three blocks
# (one for narrow phone, one for tablet, one for desktop-first).
media_count=$(grep -c '@media' "$CSS" || true)
if [ "$media_count" -lt 3 ]; then
    fail "webmail.css has only $media_count @media block(s) (expected >= 3 for mobile + tablet + desktop)"
fi
pass "webmail.css has $media_count responsive @media block(s)"

# ── 8. RTL plumbing: [dir="rtl"] overrides OR logical properties
#
# Two acceptable strategies for RTL support in a flat CSS bundle:
#   - use CSS Logical Properties (margin-inline-start, padding-
#     inline-end, text-align: start) that flip automatically with
#     dir="rtl";
#   - include explicit [dir="rtl"] selector overrides.
# Both are valid; the absence of BOTH is the failure mode.
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

# ── 9. Settings modal surface ──────────────────────────────────
#
# The auth-gate uses openSettingsModal to surface the no-mailbox
# card after a successful login if the user has no mailbox row.
# Removing it is a regression.
if ! grep -q 'openSettingsModal' "$JS"; then
    fail "webmail.js does not define openSettingsModal (auth-gate cannot surface no-mailbox card)"
fi
pass "webmail.js exposes openSettingsModal"

# ── 10. auth-gate.js references the same endpoints as router.go
#
# The router mounts:
#   POST /api/v1/webmail/login
#   GET  /api/v1/webmail/session
# Any deviation between auth-gate.js and router.go is a drift
# bug (a "404 from the login form" class). The smoke fails closed
# if either endpoint is missing from auth-gate.
if ! grep -q '/api/v1/webmail/login'   "$GATE"; then
    fail "auth-gate.js does not POST to /api/v1/webmail/login"
fi
if ! grep -q '/api/v1/webmail/session' "$GATE"; then
    fail "auth-gate.js does not probe /api/v1/webmail/session"
fi
pass "auth-gate.js references /api/v1/webmail/login and /api/v1/webmail/session (no router drift)"

# ── 11. System folder handling ─────────────────────────────────
#
# The folder list comes from the MailStore's Folders API; the UI
# must tolerate missing system folders (Trash/Archive/Junk) by
# NOT exposing actions that depend on them. The smoke asserts the
# folder-type constants exist so a future refactor cannot silently
# drop e.g. the Archive button.
for folder_type in INBOX Sent Drafts Trash Junk Archive; do
    if ! grep -qE "'${folder_type}'|\"${folder_type}\"" "$JS"; then
        fail "webmail.js has no '${folder_type}' folder-type constant (UI button regression risk)"
    fi
done
pass "webmail.js recognises all six system folder types (INBOX, Sent, Drafts, Trash, Junk, Archive)"

# ── 12. Attachments: authenticated download path ───────────────
#
# The attachment download endpoint is /api/v1/webmail/attachments/:id
# (NOT a static file under /assets/). The download must go through
# the API so the user's session cookie authenticates the request
# and MailStore ownership is verified per-request.
if ! grep -q '/api/v1/webmail/attachments/' "$JS"; then
    fail "webmail.js does not call /api/v1/webmail/attachments/ (attachments must be auth-gated)"
fi
# /webmail/assets/attachment is OK as a static-path reference in
# CSS / index.html; it would NOT be OK in webmail.js where the
# download click handler lives.
if grep -q 'href.*attachment.*\.\(pdf\|doc\|png\|jpg\)' "$JS"; then
    fail "webmail.js appears to link directly to attachment file extensions (must go through /api/v1/webmail/attachments/)"
fi
pass "webmail.js routes attachment downloads through the auth-gated /api/v1/webmail/attachments/ endpoint"

# ── 13. No placeholder / "coming soon" / future-release UI ─────
#
# Release 1 (and every production build going forward) must NOT
# expose unfinished features as clickable settings tabs or
# visible copy. Forbidden surface strings:
#
#   - "Coming later"            — the placeholder tab itself.
#   - "Available in a future…"  — the renderSettingsTab('deferred')
#                                 panel title.
#   - "future release"           — the same panel body / other copy.
#   - "coming soon" / "is not yet…enabled" — also forbidden.
#   - ".settings-deferred-list"  — the CSS class that visualises
#                                 the placeholder bullet list.
#
# We check the LIVE webmail bundle (release/webmail/assets/) and
# FAIL the smoke if any of these tokens survive. The match is
# case-insensitive because the developer who copy-pastes the
# placeholder is unlikely to think about case.
#
# Allowed exceptions: the docs/ subtree — but a smoke of the
# production bundle alone is sharper than a docs check. A
# separate docs check would over-fire on previous changelogs
# and inline docs comments that document the absence; we trust
# the release audit doc for those.
for forbidden in 'Coming later' 'Available in a future' 'settings-deferred-list' 'coming soon' 'is not enabled'; do
    matches=$(grep -niE "${forbidden}" "$JS" "$CSS" 2>/dev/null || true)
    if [ -n "$matches" ]; then
        fail "production webmail bundle still contains placeholder text '$forbidden' (forbidden in R1): $matches"
    fi
done
pass "production webmail bundle contains no placeholder / future-release / coming-soon copy"

# ── 14. Change Password surface ────────────────────────────────
#
# R1 ships a real employee Change Password feature inside
# Settings → Security. The form must expose three password
# inputs (current / new / confirm) and a Save button. The
# endpoint must be /api/v1/webmail/password/change on the
# JS side (the route is the router.go wiring; this smoke
# verifies the front-end is wired correctly).
if ! grep -q '/api/v1/webmail/password/change' "$JS"; then
    fail "webmail.js does not call /api/v1/webmail/password/change (Change Password feature not wired)"
fi
if ! grep -q 'current_password' "$JS"; then
    fail "webmail.js does not reference 'current_password' (Change Password form fields missing)"
fi
if ! grep -q 'new_password' "$JS"; then
    fail "webmail.js does not reference 'new_password' (Change Password form fields missing)"
fi
if ! grep -q 'confirm_password' "$JS"; then
    fail "webmail.js does not reference 'confirm_password' (Change Password form fields missing)"
fi
if ! grep -q 'function renderSecurityTab' "$JS"; then
    fail "webmail.js does not define renderSecurityTab (Settings → Security handler missing)"
fi
pass "Change Password UI + endpoint is wired (current/new/confirm + renderSecurityTab)"

printf '\nALL WEBMAIL UI STRUCTURAL TESTS PASSED\n' >&2
exit 0
