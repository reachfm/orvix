#!/usr/bin/env bash
# smoke-caddy-autodiscover.sh — Regression smoke for the Caddy
# generated-mail.vhost block that routes the mail-client
# autodiscover / autoconfig paths to the CoreMail API at
# 127.0.0.1:8080 BEFORE the catch-all to 8081 (JMAP / SMTP /
# IMAP / POP3 listener).
#
# Background: the previous install had a subtle bug where
# /autodiscover/* on mail.<domain> was being routed to the
# catch-all (8081, JMAP) because the literal handler
# registration for it lived AFTER the catch-all in the Caddy
# generation script, or lived only in /webmail (a different
# vhost) and never reached mail.<domain>. The fix is in
# release/scripts/setup-https.sh — it injects FOUR explicit
# handlers ahead of the catch-all:
#
#   handle /autodiscover/*           { reverse_proxy 127.0.0.1:8080 }
#   handle /Autodiscover/*           { reverse_proxy 127.0.0.1:8080 }
#   handle /.well-known/autoconfig/* { reverse_proxy 127.0.0.1:8080 }
#   handle /mail/config-v1.1.xml     { reverse_proxy 127.0.0.1:8080 }
#
# This smoke is a static-analysis check on setup-https.sh —
# it has no runtime side effects. It does not parse Caddyfile
# binaries; it grep-confirms that:
#
#   1. setup-https.sh defines the four required handlers,
#   2. they appear INSIDE the mail.<domain> block (after the
#      mail.<domain> opener and before the catch-all `handle {`),
#   3. the catch-all still routes to 8081,
#   4. the webmail.<domain> block is preserved (the
#      substring-match bug from earlier must not return).
#
# The script is side-effect-free and runs in any CI image
# that has bash. Exits 0 on pass, 1 on failure.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SETUP_HTTPS="${SETUP_HTTPS:-$SCRIPT_DIR/setup-https.sh}"

pass() { printf 'PASS  %s\n' "$*" >&2; }
fail() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

[ -f "$SETUP_HTTPS" ] || fail "setup-https.sh not found at $SETUP_HTTPS"
pass "setup-https.sh exists"

# Strip CRLF once, up front, so byte-offset arithmetic works on
# Windows-checked-out files. We never want to compare a CR-stripped
# offset against a raw offset in the same script.
TXT="$SETUP_HTTPS"
TXT_CR="$SCRIPT_DIR/.caddy-smoke.tmp.$$"
trap 'rm -f "$TXT_CR"; rm -f "$subset" 2>/dev/null; rm -f "$webmail_subset" 2>/dev/null' EXIT
tr -d '\r' < "$TXT" > "$TXT_CR"
TXT="$TXT_CR"

# Find the mail.<domain> opener — the Caddyfile snippet opens
# with `$MAIL_DOMAIN {` inside a heredoc. We anchor at the
# literal string because the heredoc terminator is `CADDY` and
# we want to stay between them.
mail_block_start=$(grep -nE '^\$MAIL_DOMAIN\s*\{|^\$?MAIL_DOMAIN\s*\{' "$TXT" | head -n1 | cut -d: -f1)
if [ -z "$mail_block_start" ]; then
    # Fallback: any line referencing MAIL_DOMAIN as a Caddy
    # host opener. We deliberately exclude the assignment
    # lines like `MAIL_DOMAIN="${ORVIX_MAIL_DOMAIN:-}"`.
    mail_block_start=$(grep -nE 'MAIL_DOMAIN \{|MAIL_DOMAIN\{' "$TXT" | head -n1 | cut -d: -f1)
fi
if [ -z "$mail_block_start" ]; then
    fail "could not locate mail.<domain> Caddy block in setup-https.sh"
fi
pass "mail.<domain> block opens at line $mail_block_start"

# Find the catch-all `handle { reverse_proxy 127.0.0.1:8081 }`.
# Several Caddy blocks have catch-alls; we want the LAST one
# INSIDE the mail.<domain> block (which comes after the API
# and webmail handlers). Easier: find every line with 8081
# and pick the largest line number ≥ mail_block_start.
catchall_lines=$(grep -nE 'reverse_proxy 127\.0\.0\.1:8081' "$TXT" | cut -d: -f1 || true)
if [ -z "$catchall_lines" ]; then
    fail "no catch-all reverse_proxy to 127.0.0.1:8081 in setup-https.sh"
fi
catchall_line=$(echo "$catchall_lines" | awk -v start="$mail_block_start" '$1 >= start { print $1 }' | tail -n1)
if [ -z "$catchall_line" ]; then
    fail "could not find 8081 catch-all line inside mail.<domain> block"
fi
pass "catch-all reverse_proxy to 127.0.0.1:8081 at line $catchall_line"

# Confirm the four critical handlers each appear between
# mail_block_start and catchall_line.
required_handlers=(
    'handle /autodiscover/*'
    'handle /Autodiscover/*'
    'handle /.well-known/autoconfig/*'
    'handle /mail/config-v1.1.xml'
)
for h in "${required_handlers[@]}"; do
    # Escape slashes for awk's regex match if we go that route;
    # a simple grep with line range works here.
    line=$(awk -v start="$mail_block_start" -v end="$catchall_line" 'NR >= start && NR <= end' "$TXT" | grep -nF "$h" | head -n1 | cut -d: -f1)
    if [ -z "$line" ]; then
        fail "required handler missing in mail.<domain> block: $h (between lines $mail_block_start and $catchall_line)"
    fi
    pass "mail.<domain> block contains: $h"
done

# Confirm each handler routes to 127.0.0.1:8080 (NOT 8081).
awk -v start="$mail_block_start" -v end="$catchall_line" 'NR >= start && NR <= end' "$TXT" > "$SCRIPT_DIR/.caddy-smoke.subset.$$"
trap 'rm -f "$SCRIPT_DIR/.caddy-smoke.subset.$$"' EXIT
subset="$SCRIPT_DIR/.caddy-smoke.subset.$$"

for h in "${required_handlers[@]}"; do
    if ! grep -qF "$h" "$subset"; then
        fail "$h not in mail.<domain> subset"
    fi
    # Walk forward from the handler line and confirm the
    # next reverse_proxy line points to 8080.
    handler_line=$(grep -nF "$h" "$subset" | head -n1 | cut -d: -f1)
    if [ -z "$handler_line" ]; then
        fail "$h has no line offset in subset"
    fi
    # Find the nearest reverse_proxy after this handler line.
    rp_line=$(awk -v hl="$handler_line" 'NR > hl && /reverse_proxy/ { print NR ":" $0; exit }' "$subset")
    if [ -z "$rp_line" ]; then
        fail "$h has no following reverse_proxy line"
    fi
    if echo "$rp_line" | grep -q 'reverse_proxy 127\.0\.0\.1:8080'; then
        : # correct upstream — 8080 (CoreMail HTTP API)
    elif echo "$rp_line" | grep -q 'reverse_proxy 127\.0\.0\.1:8081'; then
        fail "$h routes to 8081 (JMAP), not 8080 (CoreMail API)"
    else
        fail "$h does not route to 127.0.0.1:8080 (got: $rp_line)"
    fi
done
pass "every required handler routes to 127.0.0.1:8080 (CoreMail API)"

# Confirm the webmail.<domain> block is preserved verbatim.
# Substring-match regression guard: handlers must NOT be
# accidentally patched into the webmail block. The
# webmail.<domain> block opens with `$WEBMAIL_DOMAIN {` and
# closes at its own `}` — we use the line BEFORE the
# $MAIL_DOMAIN block as the upper bound so the webmail block
# is selected exactly.
webmail_block_start=$(grep -nE '^\$WEBMAIL_DOMAIN\s*\{|WEBMAIL_DOMAIN\s*\{' "$TXT" | head -n1 | cut -d: -f1)
if [ -z "$webmail_block_start" ]; then
    fail "could not locate webmail.<domain> Caddy block in setup-https.sh"
fi
# Upper bound = line just before $MAIL_DOMAIN block opens.
webmail_block_end=$((mail_block_start - 1))
if [ "$webmail_block_end" -le "$webmail_block_start" ]; then
    webmail_block_end="$catchall_line"
fi
# The webmail block must NOT contain any of the
# autodiscover literal handlers we just verified live inside
# the mail block. A substring bug from earlier accidentally
# placed /autodiscover under $WEBMAIL_DOMAIN.
awk -v start="$webmail_block_start" -v end="$webmail_block_end" 'NR >= start && NR <= end' "$TXT" > "$SCRIPT_DIR/.caddy-smoke.webmail.$$"
trap 'rm -f "$TXT_CR"; rm -f "$subset" 2>/dev/null; rm -f "$webmail_subset" 2>/dev/null' EXIT
webmail_subset="$SCRIPT_DIR/.caddy-smoke.webmail.$$"
for h in "${required_handlers[@]}"; do
    if grep -qF "$h" "$webmail_subset"; then
        fail "$h accidentally appears in webmail.<domain> block — substring-match bug regression"
    fi
done
pass "webmail.<domain> block does NOT contain autodiscover handlers (no substring-match regression)"

printf '\nALL CADDY AUTODISCOVER SMOKE TESTS PASSED\n' >&2
exit 0
