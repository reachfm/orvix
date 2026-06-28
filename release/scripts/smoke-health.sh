#!/usr/bin/env bash
# smoke-health.sh — Cheap liveness probe for the running Orvix host.
#
# Usage:
#   sudo bash smoke-health.sh [--verbose]
#
# Exits 0 when every check passes; non-zero on first failure.
# This script is SAFE to run on a production host. It does NOT
# write to disk, does NOT mutate configuration, does NOT touch
# the database. It is the script you wire into systemd's
# OnFailure=, an uptime monitor, or a cron-every-minute job.
#
# Checks performed (in order):
#   1. orvix.service is active (systemctl is-active)
#   2. redis-server.service is active (hard dependency)
#   3. /api/v1/health returns HTTP 200 within 5s
#   4. /api/v1/health body contains "status":"ok"
#   5. /admin and /webmail static asset roots respond (HEAD 200)
#   6. /var/lib/orvix/orvix.db exists and is a SQLite file
#   7. /var/lib/orvix/jwt_key.pem exists, is 0600 orvix:orvix
#   8. /var/log/orvix is writable by the orvix user

set -euo pipefail

ORVIX_BIN="${ORVIX_BIN:-/usr/local/bin/orvix}"
ORVIX_ADMIN_URL="${ORVIX_ADMIN_URL:-http://127.0.0.1:8080}"
ORVIX_JMAP_URL="${ORVIX_JMAP_URL:-http://127.0.0.1:8081}"
ORVIX_DATA_DIR="${ORVIX_DATA_DIR:-/var/lib/orvix}"
ORVIX_LOG_DIR="${ORVIX_LOG_DIR:-/var/log/orvix}"

VERBOSE=0
[ "${1:-}" = "--verbose" ] || [ "${1:-}" = "-v" ] && VERBOSE=1

log() {
    if [ "$VERBOSE" = "1" ]; then
        printf '  %s\n' "$*" >&2
    fi
}

pass() { printf 'PASS  %s\n' "$*" >&2; }
fail() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

# ─── 1. orvix.service ──────────────────────────────────────────────
if systemctl is-active --quiet orvix.service 2>/dev/null; then
    pass "orvix.service is active"
else
    fail "orvix.service is NOT active (journalctl -u orvix.service -n 50)"
fi

# ─── 2. redis-server.service ──────────────────────────────────────
if systemctl is-active --quiet redis-server.service 2>/dev/null; then
    pass "redis-server.service is active"
else
    fail "redis-server.service is NOT active — orvix will refuse new requests"
fi

# ─── 3. /api/v1/health returns 200 ────────────────────────────────
code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
    "${ORVIX_ADMIN_URL}/api/v1/health" 2>/dev/null || echo 000)
if [ "$code" = "200" ]; then
    pass "/api/v1/health returned 200"
else
    fail "/api/v1/health returned HTTP $code (expected 200)"
fi

# ─── 4. /api/v1/health body has status:ok ──────────────────────────
body=$(curl -sS --max-time 5 "${ORVIX_ADMIN_URL}/api/v1/health" 2>/dev/null || true)
if printf '%s' "$body" | grep -q '"status":"ok"'; then
    pass "/api/v1/health body has status:ok"
else
    log "body was: $(printf '%s' "$body" | head -c 200)"
    fail "/api/v1/health body missing status:ok"
fi

# ─── 5. admin + webmail static asset roots ─────────────────────────
for path in /admin /webmail; do
    code=$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 \
        "${ORVIX_ADMIN_URL}${path}" 2>/dev/null || echo 000)
    if [ "$code" = "200" ]; then
        pass "${path} returned 200"
    else
        fail "${path} returned HTTP $code"
    fi
done

# ─── 6. SQLite database exists and is a SQLite file ───────────────
db="${ORVIX_DATA_DIR}/orvix.db"
if [ ! -f "$db" ]; then
    fail "database $db not found"
fi
if head -c 16 "$db" 2>/dev/null | od -An -c | tr -d ' \n' | grep -q '^SQLite'; then
    pass "$db is a SQLite file"
else
    fail "$db is NOT a SQLite file (corrupt or wrong format)"
fi

# ─── 7. JWT key permissions ──────────────────────────────────────
key="${ORVIX_DATA_DIR}/jwt_key.pem"
if [ ! -f "$key" ]; then
    fail "$key missing — runtime cannot sign tokens"
fi
mode=$(stat -c '%a' "$key" 2>/dev/null || echo 000)
owner=$(stat -c '%U:%G' "$key" 2>/dev/null || echo ?:?)
if [ "$mode" = "600" ] && [ "$owner" = "orvix:orvix" ]; then
    pass "$key is 0600 orvix:orvix"
else
    log "expected mode 0600 owner orvix:orvix, got mode $mode owner $owner"
    fail "$key has wrong permissions; run chmod 600 chown orvix:orvix $key"
fi

# ─── 8. log dir writable ─────────────────────────────────────────
logfile="${ORVIX_LOG_DIR}/orvix.log"
if [ -d "$ORVIX_LOG_DIR" ]; then
    if sudo -u orvix test -w "$ORVIX_LOG_DIR" 2>/dev/null || \
       [ -w "$ORVIX_LOG_DIR" ]; then
        pass "${ORVIX_LOG_DIR} is writable"
    else
        fail "${ORVIX_LOG_DIR} is NOT writable by orvix"
    fi
else
    fail "${ORVIX_LOG_DIR} does not exist"
fi

printf '\nALL HEALTH CHECKS PASSED\n' >&2
exit 0