#!/usr/bin/env bash
set -euo pipefail

# Orvix Health Check
# Exits with 0 if all checks pass, non-zero otherwise.
# Usage: bash healthcheck.sh [--verbose]

ORVIX_URL="${ORVIX_URL:-http://localhost:8080}"
JMAP_URL="${JMAP_URL:-http://localhost:8081}"
VERBOSE=false

if [ "${1:-}" = "--verbose" ] || [ "${1:-}" = "-v" ]; then
    VERBOSE=true
fi

log() {
    if $VERBOSE; then
        echo "$@"
    fi
}

failures=0

# 1. Check Orvix API
log "Checking Orvix API at $ORVIX_URL..."
HEALTH=$(curl -fsSL -o /dev/null -w "%{http_code}" "${ORVIX_URL}/api/v1/health" 2>/dev/null || echo "failed")
if [ "$HEALTH" = "200" ]; then
    log "  ✓ API is healthy"
else
    echo "  ✗ API health check failed (HTTP $HEALTH)"
    failures=$((failures + 1))
fi

# 2. Check database
log "Checking database..."
DB_CHECK=$(curl -fsSL "${ORVIX_URL}/api/v1/health" 2>/dev/null | grep -o '"status":"ok"' || echo "")
if [ -n "$DB_CHECK" ]; then
    log "  ✓ Database is responding"
else
    echo "  ✗ Database check failed"
    failures=$((failures + 1))
fi

# 3. Check Redis (if available)
if command -v redis-cli &>/dev/null; then
    log "Checking Redis..."
    if redis-cli ping 2>/dev/null | grep -q PONG; then
        log "  ✓ Redis is responding"
    else
        log "  ⚠ Redis is not available (optional)"
    fi
fi

# 4. Check JMAP endpoint reachability (advisory; may be on
#    a separate port, may be proxied through Caddy).
if command -v curl &>/dev/null; then
    log "Checking JMAP endpoint at $JMAP_URL..."
    JMAP_HEALTH=$(curl -fsSL -o /dev/null -w "%{http_code}" "${JMAP_URL}/.well-known/jmap" 2>/dev/null || echo "failed")
    if [ "$JMAP_HEALTH" = "200" ]; then
        log "  ✓ JMAP is reachable"
    else
        # Non-fatal — JMAP may not be enabled in this build, or
        # the port may be firewalled.
        log "  ⚠ JMAP probe returned HTTP $JMAP_HEALTH (advisory)"
    fi
fi

# 5. Check SMTP/IMAP/POP3 listener reachability (advisory).
for spec in "25|smtp" "110|pop3" "143|imap"; do
    port="${spec%%|*}"
    name="${spec##*|}"
    log "Checking $name on tcp/$port..."
    if command -v bash &>/dev/null && [ -e /dev/tcp/localhost/"$port" ]; then
        log "  ✓ $name is listening on tcp/$port"
    elif command -v ss &>/dev/null && ss -ltn "( sport = :$port )" 2>/dev/null | grep -q ":$port"; then
        log "  ✓ $name is listening on tcp/$port"
    else
        # Non-fatal — listener may be intentionally down.
        log "  ⚠ $name is not listening on tcp/$port (advisory)"
    fi
done

# 6. Check disk space
log "Checking disk space..."
DISK_USAGE=$(df /var/lib/orvix 2>/dev/null | awk 'NR==2 {print $5}' | sed 's/%//' || echo "0")
if [ "${DISK_USAGE:-0}" -gt 90 ]; then
    echo "  ✗ Disk usage at ${DISK_USAGE}% (threshold: 90%)"
    failures=$((failures + 1))
else
    log "  ✓ Disk usage at ${DISK_USAGE}%"
fi

if [ $failures -gt 0 ]; then
    echo ""
    echo "Health check: $failures failure(s) detected"
    exit 1
fi

echo ""
log "All health checks passed"
exit 0
