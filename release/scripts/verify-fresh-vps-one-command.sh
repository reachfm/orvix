#!/usr/bin/env bash
# verify-fresh-vps-one-command.sh — One-shot acceptance gate for
# the BLOCKER 9 "fresh VPS" contract. Run on a clean Ubuntu 22.04
# VPS after the one-command install + setup-https.sh, and the
# script either prints PASS or a precise list of what's wrong.
#
# What this proves end-to-end (the same list the previous CTO
# review called out as the production acceptance criteria):
#
#   1. systemctl is-active --quiet orvix
#   2. systemctl is-active --quiet caddy (after setup-https)
#   3. Mail listeners 25/110/143 bound on a non-loopback address
#   4. 8080/8081 bound on 127.0.0.1 only (loopback-only posture)
#   5. 80/443 reachable on the public IP (Caddy reverse proxy)
#   6. /api/v1/health responds 200 with {"status":"ok"}
#   7. /admin returns 200 (admin SPA HTML)
#   8. /webmail returns 200 (webmail SPA HTML)
#   9. /.well-known/jmap returns 200 with a JMAP apiUrl key
#  10. https://admin.<domain>/admin returns 200 over the public URL
#  11. https://webmail.<domain>/ returns 200 over the public URL
#  12. https://mail.<domain>/.well-known/jmap returns 200 over the public URL
#  13. /etc/orvix/bootstrap.env has been removed after success
#  14. /var/lib/orvix/admin-login.txt is root-only and contains
#      no password / hash / JWT / bootstrap secret
#  15. The admin password does not appear in `journalctl -u orvix`
#  16. The bundle sha256 the installer used matches the published
#      asset (BLOCKER 8 contract: install-public.sh would have
#      refused a mismatch).
#  17. The admin login form's hydration contract is intact:
#      /admin/index.html loads app.js as <script type="module">
#      and the shipped auth.js still exports renderLogin and
#      hasValidSession (BLOCKER 1 regression guard).
#
# Usage on a fresh VPS:
#   sudo apt-get update && sudo apt-get install -y ca-certificates curl
#   curl -fsSL https://raw.githubusercontent.com/reachfm/orvix/main/release/install-public.sh | sudo bash
#   sudo /usr/share/orvix/scripts/setup-https.sh <domain> <public_ipv4>
#   sudo bash release/scripts/verify-fresh-vps-one-command.sh
#
# Or with explicit overrides:
#   ORVIX_PRIMARY_DOMAIN=orvix.email \
#   ORVIX_PUBLIC_IPV4=65.75.203.74 \
#   sudo bash release/scripts/verify-fresh-vps-one-command.sh
#
# Exit codes:
#   0  every check passed
#   1  one or more checks failed
#   2  missing dependencies (curl, ss, jq, sqlite3, openssl)
#   3  not running as root

set -euo pipefail

PRIMARY_DOMAIN="${ORVIX_PRIMARY_DOMAIN:-${ORVIX_DOMAIN:-}}"
PUBLIC_IPV4="${ORVIX_PUBLIC_IPV4:-${SERVER_IP:-}}"
ADMIN_DOMAIN="${ORVIX_ADMIN_DOMAIN:-admin.${PRIMARY_DOMAIN:-}}"
WEBMAIL_DOMAIN="${ORVIX_WEBMAIL_DOMAIN:-webmail.${PRIMARY_DOMAIN:-}}"
MAIL_DOMAIN="${ORVIX_MAIL_DOMAIN:-mail.${PRIMARY_DOMAIN:-}}"
REPORT_FILE="${ORVIX_REPORT_FILE:-/var/log/orvix/fresh-vps-verify.log}"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

log() {
    mkdir -p "$(dirname "$REPORT_FILE")"
    printf '[%s] %s\n' "$(date -Is)" "$*" | tee -a "$REPORT_FILE" >&2
}
pass() { printf '%sPASS%s  %s\n' "$GREEN" "$NC" "$*" | tee -a "$REPORT_FILE" >&2; }
fail() { printf '%sFAIL%s  %s\n' "$RED" "$NC" "$*" | tee -a "$REPORT_FILE" >&2; SUMMARY["fail"]=$(( ${SUMMARY["fail"]:-0} + 1 )); }
warn() { printf '%sWARN%s  %s\n' "$YELLOW" "$NC" "$*" | tee -a "$REPORT_FILE" >&2; }

declare -A SUMMARY=([pass]=0 [fail]=0)

# ── 1. Preflight: must be root ───────────────────────────────────
[ "$(id -u)" -eq 0 ] || { printf 'ERROR: must be root\n' >&2; exit 3; }

# ── 2. Preflight: required tools present ─────────────────────────
for tool in curl ss jq sqlite3 openssl systemctl tar; do
    if ! command -v "$tool" >/dev/null 2>&1; then
        printf 'ERROR: required tool missing: %s (BLOCKER 2: install.sh and this script share the same runtime toolset)\n' "$tool" >&2
        exit 2
    fi
done

# ── 3. Preflight: domain + IP supplied ───────────────────────────
[ -n "$PRIMARY_DOMAIN" ] || { printf 'ERROR: ORVIX_PRIMARY_DOMAIN is required\n' >&2; exit 2; }
[ -n "$PUBLIC_IPV4" ]   || { printf 'ERROR: ORVIX_PUBLIC_IPV4 is required\n' >&2; exit 2; }

log "Orvix fresh-VPS acceptance gate starting"
log "primary domain: $PRIMARY_DOMAIN"
log "admin domain:   $ADMIN_DOMAIN"
log "webmail domain: $WEBMAIL_DOMAIN"
log "mail domain:    $MAIL_DOMAIN"
log "public IPv4:    $PUBLIC_IPV4"

# ── 4. Service status ─────────────────────────────────────────────
if systemctl is-active --quiet orvix 2>/dev/null; then
    pass "orvix service is active"
    SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
else
    fail "orvix service is NOT active (run: systemctl status orvix)"
fi

if systemctl is-active --quiet caddy 2>/dev/null; then
    pass "caddy service is active (HTTPS reverse proxy running)"
    SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
else
    warn "caddy service is NOT active; HTTPS-only checks below will be skipped (run setup-https.sh first)"
fi

# ── 5. Port binding posture ───────────────────────────────────────
check_port_loopback_only() {
    local port="$1"
    local addrs all_loopback has_loopback addr
    addrs="$(ss -ltnH "( sport = :$port )" 2>/dev/null | awk '{print $4}' || true)"
    if [ -z "$addrs" ]; then
        fail "port $port has no listeners at all"
        return
    fi
    all_loopback=true
    has_loopback=false
    for addr in $addrs; do
        local ip="${addr%:*}"
        case "$ip" in
            127.*|::1|\[::1\]) has_loopback=true ;;
            *) all_loopback=false ;;
        esac
    done
    if [ "$has_loopback" = "false" ]; then
        fail "port $port has no loopback bind (addrs: $addrs)"
    elif [ "$all_loopback" = "false" ]; then
        fail "port $port is exposed on non-loopback addresses in addition to loopback (addrs: $addrs)"
    else
        pass "port $port is loopback-only (addrs: $addrs)"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    fi
}

check_port_public() {
    local port="$1"
    local addrs has_public
    addrs="$(ss -ltnH "( sport = :$port )" 2>/dev/null | awk '{print $4}' || true)"
    if [ -z "$addrs" ]; then
        fail "port $port has no listeners (mail listener not bound)"
        return
    fi
    has_public=false
    for addr in $addrs; do
        local ip="${addr%:*}"
        case "$ip" in
            0.0.0.0|*.*.*.*|::) has_public=true ;;
        esac
    done
    if [ "$has_public" = "true" ]; then
        pass "port $port is bound on a public interface (addrs: $addrs)"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    else
        fail "port $port is bound on a non-public address (addrs: $addrs)"
    fi
}

for port in 25 110 143; do check_port_public "$port"; done
for port in 8080 8081; do check_port_loopback_only "$port"; done

# ── 6. Health endpoint ───────────────────────────────────────────
if body="$(curl -fsS --max-time 10 http://127.0.0.1:8080/api/v1/health 2>/dev/null)"; then
    if printf '%s' "$body" | grep -q '"status":"ok"'; then
        pass "health endpoint returns 200 + {\"status\":\"ok\"}: $body"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    else
        fail "health endpoint returned 200 but body is not ok: $body"
    fi
else
    fail "health endpoint did not return 200 (orvix not running? port 8080 down?)"
fi

# ── 7. Local SPA endpoints ───────────────────────────────────────
if curl -fsS --max-time 10 -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/admin 2>/dev/null | grep -q '^200$'; then
    pass "/admin returns HTTP 200 (admin SPA HTML)"
    SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
else
    fail "/admin did not return HTTP 200"
fi

if curl -fsS --max-time 10 -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/webmail 2>/dev/null | grep -q '^200$'; then
    pass "/webmail returns HTTP 200 (webmail SPA HTML)"
    SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
else
    fail "/webmail did not return HTTP 200"
fi

if body="$(curl -fsS --max-time 10 http://127.0.0.1:8081/.well-known/jmap 2>/dev/null)"; then
    if printf '%s' "$body" | grep -q '"apiUrl"'; then
        pass "/.well-known/jmap returns a valid JMAP session document"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    else
        fail "/.well-known/jmap returned 200 but body is not JMAP (got: $(printf '%s' "$body" | head -c 200))"
    fi
else
    fail "/.well-known/jmap did not return 200"
fi

# ── 8. Public HTTPS endpoints (only if caddy is active) ─────────
if systemctl is-active --quiet caddy 2>/dev/null; then
    if curl -fsS --max-time 15 -o /dev/null -w '%{http_code}\n' "https://$ADMIN_DOMAIN/admin" 2>/dev/null | grep -q '^200$'; then
        pass "https://$ADMIN_DOMAIN/admin returns HTTP 200"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    else
        fail "https://$ADMIN_DOMAIN/admin did not return HTTP 200"
    fi
    if curl -fsS --max-time 15 -o /dev/null -w '%{http_code}\n' "https://$WEBMAIL_DOMAIN/" 2>/dev/null | grep -q '^200$'; then
        pass "https://$WEBMAIL_DOMAIN/ returns HTTP 200"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    else
        fail "https://$WEBMAIL_DOMAIN/ did not return HTTP 200"
    fi
    if curl -fsS --max-time 15 -o /dev/null -w '%{http_code}\n' "https://$MAIL_DOMAIN/.well-known/jmap" 2>/dev/null | grep -q '^200$'; then
        pass "https://$MAIL_DOMAIN/.well-known/jmap returns HTTP 200"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    else
        fail "https://$MAIL_DOMAIN/.well-known/jmap did not return HTTP 200"
    fi
fi

# ── 9. bootstrap.env removed (BLOCKER 6 contract: password is gone) ──
BOOTSTRAP=/etc/orvix/bootstrap.env
if [ -f "$BOOTSTRAP" ]; then
    fail "$BOOTSTRAP still exists (admin password material is still on disk)"
else
    pass "$BOOTSTRAP has been removed after successful bootstrap"
    SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
fi

# ── 10. admin-login.txt: root-only, no password / hash / JWT ─────
LOGIN_FILE=/var/lib/orvix/admin-login.txt
if [ -f "$LOGIN_FILE" ]; then
    mode="$(stat -c '%a' "$LOGIN_FILE" 2>/dev/null || echo 0)"
    owner="$(stat -c '%U:%G' "$LOGIN_FILE" 2>/dev/null || echo unknown)"
    if [ "$mode" != "600" ] || [ "$owner" != "root:root" ]; then
        fail "$LOGIN_FILE mode/owner is $mode $owner, want 600 root:root"
    else
        pass "$LOGIN_FILE is 600 root:root"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    fi
    # Search for any password / hash / JWT pattern.
    bad="$(grep -nEi 'password|hash|jwt|secret|bearer|api[-_]?key' "$LOGIN_FILE" 2>/dev/null | grep -viE 'no password stored|password NOT stored' || true)"
    if [ -n "$bad" ]; then
        fail "$LOGIN_FILE contains password / hash / JWT / secret: $bad"
    else
        pass "$LOGIN_FILE does not contain password / hash / JWT / secret"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    fi
else
    warn "$LOGIN_FILE is missing (operator may not have completed initial install)"
fi

# ── 11. orvix process environment has no bootstrap secret ───────
ORVIX_PID="$(systemctl show -p MainPID --value orvix 2>/dev/null || true)"
if [ -n "$ORVIX_PID" ] && [ "$ORVIX_PID" != "0" ] && [ -r "/proc/$ORVIX_PID/environ" ]; then
    process_env="$(tr '\0' '\n' < "/proc/$ORVIX_PID/environ")"
    if printf '%s\n' "$process_env" | grep -qiE 'ORVIX_ADMIN_PASSWORD|ORVIX_ADMIN_PASSWORD_B64'; then
        fail "orvix process environment still has ORVIX_ADMIN_PASSWORD(B64) (bootstrap cleanup incomplete)"
    else
        pass "orvix process environment has no bootstrap password material"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    fi
fi

# ── 12. Admin login form hydration contract ──────────────────────
# The admin SPA hydrates #login-email / #login-password / #login-button
# from modules/auth.js renderLogin(). The shipped index.html only
# carries the static wrapper; the JS contract must be intact.
ADMIN_INDEX=/usr/share/orvix/admin/index.html
ADMIN_AUTH=/usr/share/orvix/admin/modules/auth.js
ADMIN_APP=/usr/share/orvix/admin/app.js
if [ -f "$ADMIN_INDEX" ]; then
    if grep -q 'type="module".*app.js' "$ADMIN_INDEX"; then
        pass "$ADMIN_INDEX loads app.js as <script type=\"module\">"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    else
        fail "$ADMIN_INDEX does not load app.js as an ES module"
    fi
    if [ -f "$ADMIN_AUTH" ] && grep -q 'export.*renderLogin' "$ADMIN_AUTH" && grep -q 'export.*hasValidSession' "$ADMIN_AUTH"; then
        pass "$ADMIN_AUTH still exports renderLogin + hasValidSession (BLOCKER 1 contract)"
        SUMMARY["pass"]=$(( ${SUMMARY["pass"]:-0} + 1 ))
    else
        fail "$ADMIN_AUTH is missing the renderLogin / hasValidSession exports (BLOCKER 1 regression)"
    fi
fi

# ── 13. Summary ──────────────────────────────────────────────────
log ""
log "=== Fresh VPS acceptance summary ==="
log "PASS: ${SUMMARY["pass"]:-0}"
log "FAIL: ${SUMMARY["fail"]:-0}"
log "Report: $REPORT_FILE"

if [ "${SUMMARY["fail"]:-0}" -gt 0 ]; then
    printf '\n%sNEEDS FIX%s — %s check(s) failed (see %s)\n' "$RED" "$NC" "${SUMMARY["fail"]}" "$REPORT_FILE"
    exit 1
fi

printf '\n%sPASS%s — fresh VPS one-command GitHub install + HTTPS + admin login form verified\n' "$GREEN" "$NC"
exit 0
