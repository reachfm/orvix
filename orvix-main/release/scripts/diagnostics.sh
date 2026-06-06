#!/usr/bin/env bash
set -euo pipefail

# Orvix Diagnostics
# Collects system information for troubleshooting.
# Usage: bash diagnostics.sh [output_file]

OUTPUT_FILE="${1:-orvix-diagnostics-$(date +%Y%m%d_%H%M%S).txt}"

{
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Orvix Diagnostics Report"
    echo "Generated: $(date -u)"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""

    # System info
    echo "## System Information"
    echo "Hostname: $(hostname 2>/dev/null || echo 'unknown')"
    echo "Kernel: $(uname -a 2>/dev/null || echo 'unknown')"
    echo "OS: $(cat /etc/os-release 2>/dev/null | grep PRETTY_NAME | cut -d= -f2 | tr -d '"' || echo 'unknown')"
    echo "Uptime: $(uptime -p 2>/dev/null || echo 'unknown')"
    echo ""

    # Orvix version
    echo "## Orvix Version"
    orvix version 2>/dev/null || echo "orvix binary not found in PATH"
    echo ""

    # Service status
    echo "## Service Status"
    for svc in orvix.service; do
        STATUS=$(systemctl is-active "$svc" 2>/dev/null || echo "not found")
        echo "$svc: $STATUS"
        if [ "$STATUS" = "active" ]; then
            systemctl status "$svc" 2>/dev/null | head -10
        fi
    done
    echo ""

    # Config status
    echo "## Configuration"
    if [ -f /etc/orvix/orvix.yaml ]; then
        echo "Config file: /etc/orvix/orvix.yaml (exists)"
        echo "Config size: $(wc -c < /etc/orvix/orvix.yaml) bytes"
        echo "Config permissions: $(stat -c '%a %U:%G' /etc/orvix/orvix.yaml 2>/dev/null || echo 'unknown')"
    else
        echo "Config file: /etc/orvix/orvix.yaml (MISSING)"
    fi
    echo ""

    # Directory permissions
    echo "## Directory Status"
    for dir in /etc/orvix /var/lib/orvix /var/log/orvix; do
        if [ -d "$dir" ]; then
            echo "$dir: exists ($(stat -c '%a %U:%G' "$dir" 2>/dev/null))"
            echo "  Contents: $(ls -la "$dir" 2>/dev/null | head -10)"
        else
            echo "$dir: MISSING"
        fi
    done
    echo ""

    # Binary status
    echo "## Binary Status"
    for bin in /usr/local/bin/orvix /usr/local/bin/stalwart; do
        if [ -f "$bin" ]; then
            echo "$bin: exists ($(stat -c '%a %U:%G' "$bin" 2>/dev/null), $(wc -c < "$bin") bytes)"
        else
            echo "$bin: MISSING"
        fi
    done
    echo ""

    # Port status
    echo "## Port Status"
    for port in 8080 18080 80 443 25 143 993; do
        STATUS=$(ss -tlnp "sport = :$port" 2>/dev/null | head -1 || echo "")
        if [ -n "$STATUS" ]; then
            echo "Port $port: LISTENING"
        else
            echo "Port $port: not listening"
        fi
    done
    echo ""

    # Recent logs
    echo "## Recent Logs (last 20 lines)"
    journalctl -u orvix.service --no-pager -n 20 2>/dev/null || echo "No logs available"
    echo ""

    # Database status
    echo "## Database Status"
    if [ -f /var/lib/orvix/orvix.db ]; then
        echo "Database: /var/lib/orvix/orvix.db"
        echo "Size: $(du -h /var/lib/orvix/orvix.db 2>/dev/null | cut -f1)"
        echo "Permissions: $(stat -c '%a %U:%G' /var/lib/orvix/orvix.db 2>/dev/null)"
    else
        echo "Database: not found (will be created on first start)"
    fi
    echo ""

    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "End of Diagnostics Report"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

} | tee "$OUTPUT_FILE"

echo ""
echo "Report saved to: $OUTPUT_FILE"
