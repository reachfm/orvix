#!/usr/bin/env bash
set -euo pipefail

# Orvix Health Check
# Exits with 0 if all checks pass, non-zero otherwise.
# Usage: bash healthcheck.sh [--verbose]

ORVIX_URL="${ORVIX_URL:-http://localhost:8080}"
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

# 4. Check Stalwart (if installed)
if command -v stalwart &>/dev/null; then
    log "Checking Stalwart..."
    STALWART_CHECK=$(curl -fsSL -o /dev/null -w "%{http_code}" "http://localhost:18080/health" 2>/dev/null || echo "failed")
    if [ "$STALWART_CHECK" = "200" ]; then
        log "  ✓ Stalwart is responding"
    else
        echo "  ⚠ Stalwart health check failed (HTTP $STALWART_CHECK)"
        # Non-fatal — Stalwart may not be running yet
    fi
fi

# 5. Check disk space
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
