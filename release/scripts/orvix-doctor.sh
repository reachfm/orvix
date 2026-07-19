#!/usr/bin/env bash
set -euo pipefail

# Orvix Doctor — comprehensive health-check script.
#
# Usage:
#   bash orvix-doctor.sh               # human-readable colour output
#   bash orvix-doctor.sh --json        # machine-readable JSON
#   bash orvix-doctor.sh --quiet       # exit code only (0=healthy, 1=unhealthy)
#   bash orvix-doctor.sh --json --quiet  # JSON to stdout, exit code semantics

ORVIX_CONFIG="${ORVIX_CONFIG:-/etc/orvix/orvix.yaml}"
ORVIX_DB="${ORVIX_DB:-/var/lib/orvix/orvix.db}"
ORVIX_JWT_KEY="${ORVIX_JWT_KEY:-/etc/orvix/orvix.jwt.key}"
ORVIX_VAPID_KEY="${ORVIX_VAPID_KEY:-/etc/orvix/vapid_private.key}"
ORVIX_DKIM_DIR="${ORVIX_DKIM_DIR:-/var/lib/orvix/dkim}"
ORVIX_MAILSTORE="${ORVIX_MAILSTORE:-/var/lib/orvix/mailstore}"
ORVIX_BACKUP_DIR="${ORVIX_BACKUP_DIR:-/var/backups/orvix}"
ORVIX_CERT_DIR="${ORVIX_CERT_DIR:-/etc/orvix/certs}"
CADDY_CERT_DIR="${CADDY_CERT_DIR:-/etc/caddy/certificates}"

HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/api/v1/health}"
DNS_PROVIDERS_URL="${DNS_PROVIDERS_URL:-http://127.0.0.1:8080/api/v1/admin/dns/providers}"

OUTPUT_JSON=0
OUTPUT_QUIET=0

BOLD=$'\033[1m'
RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

CHECKS=()
STATUS="healthy"

add_check() {
    local name="$1"
    local status="$2"
    local message="$3"
    CHECKS+=("$(printf '{"name":"%s","status":"%s","message":"%s"}' \
        "$(json_escape "$name")" \
        "$(json_escape "$status")" \
        "$(json_escape "$message")")")
    if [ "$status" = "FAIL" ]; then
        STATUS="unhealthy"
    fi
}

json_escape() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//$'\n'/\\n}"
    value="${value//$'\r'/\\r}"
    value="${value//$'\t'/\\t}"
    printf '%s' "$value"
}

# yaml_section_val reads a key from a specific YAML top-level section.
# Usage: yaml_section_val <section> <key> [file]
# Returns the value (stripped of quotes/whitespace) or empty string.
# ONLY matches keys indented under the named top-level section.
# Example: yaml_section_val "coremail" "submission_enabled" -> "true"
yaml_section_val() {
    local section="$1"
    local key="$2"
    local file="${3:-$ORVIX_CONFIG}"
    [ -f "$file" ] || { printf ''; return; }
    awk -v section="$section" -v key="$key" '
    BEGIN { in_section=""; val="" }
    /^[a-zA-Z0-9_-]+:/ {
        if ($0 ~ "^" section ":") { in_section=section; next }
        if ($0 !~ /^[ ]/) { in_section="" }
    }
    in_section == section && $0 ~ "^[ ]+" key "[ ]*:" {
        # Extract value after the colon, strip quotes and trailing comment.
        sub(/^[ ]+/, "")
        sub(/^[a-zA-Z0-9_-]+[ ]*:[ ]*/, "")
        sub(/[ ]*#.*$/, "")
        gsub(/^["'"'"']|["'"'"']$/, "")
        val=$0
        exit
    }
    END { printf "%s", val }
    ' "$file"
}

# yaml_section_bool returns "true" if the key under the section is "true",
# "false" otherwise. Section-aware — only reads keys inside the named section.
yaml_section_bool() {
    local section="$1"
    local key="$2"
    local file="${3:-$ORVIX_CONFIG}"
    local val
    val="$(yaml_section_val "$section" "$key" "$file")"
    [ "$val" = "true" ] && { printf 'true'; return 0; }
    printf 'false'
}
color_status() {
    local status="$1"
    case "$status" in
        PASS) printf '%b%s%b' "$GREEN" "$status" "$NC" ;;
        FAIL) printf '%b%s%b' "$RED" "$status" "$NC" ;;
        WARN) printf '%b%s%b' "$YELLOW" "$status" "$NC" ;;
        *)  printf '%s' "$status" ;;
    esac
}

print_human() {
    local name="$1" status="$2" message="$3"
    printf '  %-6s  %-40s  %s\n' "$(color_status "$status")" "$name" "$message"
}

# ── Checks ────────────────────────────────────────────────────────

sudo_read() {
    if [ "$(id -u)" -eq 0 ]; then
        cat "$1" 2>/dev/null || true
    else
        sudo cat "$1" 2>/dev/null || true
    fi
}

sudo_stat_owner() {
    if [ "$(id -u)" -eq 0 ]; then
        stat -c '%U' "$1" 2>/dev/null || echo ''
    else
        sudo stat -c '%U' "$1" 2>/dev/null || echo ''
    fi
}

sudo_stat_mode() {
    if [ "$(id -u)" -eq 0 ]; then
        stat -c '%a' "$1" 2>/dev/null || echo ''
    else
        sudo stat -c '%a' "$1" 2>/dev/null || echo ''
    fi
}

check_orvix_service() {
    local name="orvix service active"
    if systemctl is-active --quiet orvix 2>/dev/null; then
        add_check "$name" "PASS" "orvix.service is running"
    else
        add_check "$name" "FAIL" "orvix.service is not active"
    fi
}

check_caddy_service() {
    local name="caddy service active"
    if [ -f /etc/caddy/Caddyfile ]; then
        if systemctl is-active --quiet caddy 2>/dev/null; then
            add_check "$name" "PASS" "caddy.service is running"
        else
            add_check "$name" "FAIL" "caddy.service is not active but Caddyfile exists"
        fi
    else
        add_check "$name" "PASS" "caddy not installed (Caddyfile absent)"
    fi
}

check_redis_service() {
    local name="redis service active"
    if command -v redis-cli >/dev/null 2>&1; then
        if redis-cli ping >/dev/null 2>&1; then
            add_check "$name" "PASS" "redis-cli ping succeeded"
            return
        fi
    fi
    if systemctl is-active --quiet redis-server 2>/dev/null; then
        add_check "$name" "PASS" "redis-server.service is running"
    else
        add_check "$name" "FAIL" "redis is not reachable"
    fi
}

check_config_parses() {
    local name="config parses"
    if [ ! -f "$ORVIX_CONFIG" ]; then
        add_check "$name" "FAIL" "$ORVIX_CONFIG does not exist"
        return
    fi
    if ! command -v python3 >/dev/null 2>&1 && ! command -v python >/dev/null 2>&1; then
        if command -v jq >/dev/null 2>&1; then
            # jq can't parse YAML directly; fall back to basic check.
            if grep -q '^server:' "$ORVIX_CONFIG" 2>/dev/null; then
                add_check "$name" "PASS" "$ORVIX_CONFIG exists and contains server: block"
            else
                add_check "$name" "WARN" "$ORVIX_CONFIG exists but unable to verify YAML structure"
            fi
        else
            add_check "$name" "WARN" "cannot verify YAML: python3 and jq not available"
        fi
        return
    fi
    local py_cmd
    py_cmd="python3"
    if ! command -v python3 >/dev/null 2>&1; then
        py_cmd="python"
    fi
    if "$py_cmd" -c "import yaml; yaml.safe_load(open('$ORVIX_CONFIG'))" 2>/dev/null; then
        add_check "$name" "PASS" "$ORVIX_CONFIG is valid YAML"
    else
        add_check "$name" "FAIL" "$ORVIX_CONFIG is not valid YAML"
    fi
}

check_db_reachable() {
    local name="database reachable"
    if [ ! -f "$ORVIX_DB" ]; then
        add_check "$name" "FAIL" "$ORVIX_DB does not exist"
        return
    fi
    if command -v sqlite3 >/dev/null 2>&1; then
        if sqlite3 "$ORVIX_DB" "PRAGMA integrity_check;" 2>/dev/null | grep -q '^ok$'; then
            add_check "$name" "PASS" "$ORVIX_DB integrity check ok"
        else
            add_check "$name" "FAIL" "$ORVIX_DB integrity check failed"
        fi
    else
        if [ -r "$ORVIX_DB" ]; then
            add_check "$name" "PASS" "$ORVIX_DB exists and is readable"
        else
            add_check "$name" "FAIL" "$ORVIX_DB is not readable"
        fi
    fi
}

check_jwt_key() {
    local name="jwt key"
    local jwt_key_path="${ORVIX_JWT_KEY:-/var/lib/orvix/jwt_key.pem}"
    if [ ! -f "$jwt_key_path" ]; then
        jwt_key_path="/var/lib/orvix/jwt_key.pem"
    fi
    if [ ! -f "$jwt_key_path" ]; then
        add_check "$name" "FAIL" "jwt key not found at $jwt_key_path"
        return
    fi
    local owner mode
    owner="$(sudo_stat_owner "$jwt_key_path")"
    mode="$(sudo_stat_mode "$jwt_key_path")"
    local issues=""
    if [ "$owner" != "orvix" ]; then
        issues="owner $owner (expected orvix)"
    fi
    if [ "$mode" != "600" ]; then
        issues="${issues:+$issues, }mode $mode (expected 600)"
    fi
    if [ -z "$issues" ]; then
        add_check "$name" "PASS" "$jwt_key_path owner=$owner mode=$mode"
    else
        add_check "$name" "FAIL" "$jwt_key_path: $issues"
    fi
}

check_vapid_key() {
    local name="vapid key"
    if [ ! -f "$ORVIX_VAPID_KEY" ]; then
        add_check "$name" "FAIL" "vapid private key not found at $ORVIX_VAPID_KEY"
        return
    fi
    local owner mode
    owner="$(sudo_stat_owner "$ORVIX_VAPID_KEY")"
    mode="$(sudo_stat_mode "$ORVIX_VAPID_KEY")"
    local issues=""
    if [ "$owner" != "root" ] && [ "$owner" != "orvix" ]; then
        issues="owner $owner (expected root or orvix)"
    fi
    if [ "$mode" != "600" ] && [ "$mode" != "640" ]; then
        issues="${issues:+$issues, }mode $mode (expected 600 or 640)"
    fi
    if [ -z "$issues" ]; then
        add_check "$name" "PASS" "$ORVIX_VAPID_KEY owner=$owner mode=$mode"
    else
        add_check "$name" "FAIL" "$ORVIX_VAPID_KEY: $issues"
    fi
}

check_dkim_keys() {
    local name="dkim keys"
    if [ ! -d "$ORVIX_DKIM_DIR" ]; then
        add_check "$name" "WARN" "dkim directory $ORVIX_DKIM_DIR does not exist"
        return
    fi
    local key_count
    key_count="$(find "$ORVIX_DKIM_DIR" -name '*.private' -o -name '*.key' -o -name 'private.key' 2>/dev/null | wc -l)"
    if [ "$key_count" -gt 0 ]; then
        add_check "$name" "PASS" "$key_count dkim key file(s) found in $ORVIX_DKIM_DIR"
    else
        add_check "$name" "WARN" "no dkim key files found in $ORVIX_DKIM_DIR"
    fi
}

check_mailstore_path() {
    local name="mailstore path"
    local mailstore
    mailstore="$ORVIX_MAILSTORE"
    if [ -f "$ORVIX_CONFIG" ]; then
        local cfg_path
        cfg_path="$(grep -E '^\s*mailstore_path:' "$ORVIX_CONFIG" 2>/dev/null | sed -E 's/^\s*mailstore_path:\s*//; s/["'']*//g' | head -n1 || true)"
        if [ -n "$cfg_path" ]; then
            mailstore="$cfg_path"
        fi
    fi
    if [ -d "$mailstore" ]; then
        add_check "$name" "PASS" "$mailstore exists"
    else
        add_check "$name" "WARN" "$mailstore does not exist"
    fi
}

check_port() {
    local port="$1" desc="$2" expected_scope="$3"
    local name="port $port ($desc)"
    local addrs addr ip
    addrs="$(ss -ltnH "( sport = :$port )" 2>/dev/null | awk '{print $4}' || true)"
    if [ -z "$addrs" ]; then
        add_check "$name" "WARN" "port $port is not listening"
        return
    fi
    local all_ok=true
    for addr in $addrs; do
        ip="${addr%:*}"
        case "$expected_scope" in
            loopback)
                case "$ip" in
                    127.*|127.0.0.1|\[::1\]|::1) ;;
                    *) all_ok=false ;;
                esac
                ;;
            public)
                case "$ip" in
                    127.*|127.0.0.1|\[::1\]|::1) all_ok=false ;;
                    *) ;;
                esac
                ;;
        esac
    done
    if [ "$all_ok" = true ]; then
        add_check "$name" "PASS" "port $port listening on correct scope ($expected_scope)"
    else
        local found
        found="$(echo "$addrs" | tr '\n' ' ')"
        add_check "$name" "FAIL" "port $port has wrong scope: $found (expected $expected_scope)"
    fi
}

check_ports() {
    # Check if coremail is enabled — section-aware.
    local coremail_enabled=1
    if [ -f "$ORVIX_CONFIG" ]; then
        if [ "$(yaml_section_bool "coremail" "enabled")" != "true" ]; then
            coremail_enabled=0
        fi
    fi

    if [ "$coremail_enabled" = "1" ]; then
        check_port 25 "SMTP" "public"
        check_port 110 "POP3" "public"
        check_port 143 "IMAP" "public"
    else
        add_check "port 25/110/143 (coremail)" "PASS" "coremail not enabled; skipping mail port checks"
    fi

    check_port 8080 "admin/webmail HTTP" "loopback"
    check_port 8081 "JMAP" "loopback"

    # Caddy ports.
    if [ -f /etc/caddy/Caddyfile ]; then
        check_port 80 "HTTP (caddy)" "public"
        check_port 443 "HTTPS (caddy)" "public"
    fi

    # Optional TLS ports — section-aware checks.
    if [ -f "$ORVIX_CONFIG" ]; then
        if [ "$(yaml_section_bool "coremail" "submission_enabled")" = "true" ]; then
            check_port 587 "Submission" "public"
        else
            add_check "port 587 (Submission)" "PASS" "submission not enabled"
        fi
        if [ "$(yaml_section_bool "coremail" "smtps_enabled")" = "true" ]; then
            check_port 465 "SMTPS" "public"
        else
            add_check "port 465 (SMTPS)" "PASS" "smtps not enabled (SMTPS not yet implemented)"
        fi
        if [ "$(yaml_section_bool "coremail" "imaps_enabled")" = "true" ]; then
            check_port 993 "IMAPS" "public"
        else
            add_check "port 993 (IMAPS)" "PASS" "imaps not enabled"
        fi
        if [ "$(yaml_section_bool "coremail" "pop3s_enabled")" = "true" ]; then
            check_port 995 "POP3S" "public"
        else
            add_check "port 995 (POP3S)" "PASS" "pop3s not enabled"
        fi
    fi
}

check_dns_public_ipv4() {
    local name="dns public_ipv4 configured"
    if [ ! -f "$ORVIX_CONFIG" ]; then
        add_check "$name" "FAIL" "$ORVIX_CONFIG does not exist"
        return
    fi
    local ip
    ip="$(yaml_section_val "dns" "public_ipv4")"
    if [ -z "$ip" ] || [ "$ip" = '""' ] || [ "$ip" = "''" ]; then
        add_check "$name" "FAIL" "dns.public_ipv4 is not set in $ORVIX_CONFIG"
    else
        add_check "$name" "PASS" "dns.public_ipv4 = $ip"
    fi
}

check_dns_readiness() {
    local name="dns providers readiness"
    local code
    code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "$DNS_PROVIDERS_URL" 2>/dev/null || echo '000')"
    case "$code" in
        200)
            add_check "$name" "PASS" "dns providers endpoint returned 200" ;;
        401|403)
            add_check "$name" "PASS" "protected dns providers endpoint returned $code (correctly requires authentication)" ;;
        000)
            add_check "$name" "WARN" "dns providers endpoint unreachable (orvix not running?)" ;;
        404)
            add_check "$name" "FAIL" "dns providers endpoint returned 404 (expected endpoint missing)" ;;
        5??)
            add_check "$name" "FAIL" "dns providers endpoint returned $code (server-side failure)" ;;
        *)
            add_check "$name" "FAIL" "dns providers endpoint returned unexpected HTTP $code" ;;
    esac
}

check_backup_dir() {
    local name="backup directory writable"
    if [ -d "$ORVIX_BACKUP_DIR" ]; then
        if [ -w "$ORVIX_BACKUP_DIR" ] || sudo test -w "$ORVIX_BACKUP_DIR" 2>/dev/null; then
            add_check "$name" "PASS" "$ORVIX_BACKUP_DIR is writable"
        else
            add_check "$name" "FAIL" "$ORVIX_BACKUP_DIR is not writable"
        fi
    else
        add_check "$name" "WARN" "$ORVIX_BACKUP_DIR does not exist"
    fi
}

check_queue_health() {
    local name="queue health"
    if [ ! -f "$ORVIX_DB" ]; then
        add_check "$name" "WARN" "$ORVIX_DB not found; cannot check queue"
        return
    fi
    if ! command -v sqlite3 >/dev/null 2>&1; then
        add_check "$name" "WARN" "sqlite3 not available; cannot check queue"
        return
    fi
    local pending
    pending="$(sqlite3 "$ORVIX_DB" "SELECT COUNT(*) FROM queue_items WHERE status IN ('pending','retry');" 2>/dev/null || echo '')"
    if [ -z "$pending" ]; then
        # Try the outbound_queue table.
        pending="$(sqlite3 "$ORVIX_DB" "SELECT COUNT(*) FROM outbound_queue WHERE status IN ('pending','retry');" 2>/dev/null || echo '0')"
    fi
    if [ "$pending" = "0" ]; then
        add_check "$name" "PASS" "queue: 0 pending items"
    elif [ "$pending" -gt 0 ] 2>/dev/null; then
        if [ "$pending" -lt 50 ]; then
            add_check "$name" "PASS" "queue: $pending pending items"
        elif [ "$pending" -lt 500 ]; then
            add_check "$name" "WARN" "queue: $pending pending items (elevated)"
        else
            add_check "$name" "FAIL" "queue: $pending pending items (critical backlog)"
        fi
    else
        add_check "$name" "WARN" "could not determine queue count"
    fi
}

check_disk_usage() {
    local name="disk usage"
    local usage
    usage="$(df -h / 2>/dev/null | awk 'NR==2 {print $5}' | tr -d '%' || echo '')"
    if [ -z "$usage" ]; then
        add_check "$name" "WARN" "could not determine disk usage"
        return
    fi
    if [ "$usage" -lt 85 ]; then
        add_check "$name" "PASS" "disk $usage%% used"
    elif [ "$usage" -lt 95 ]; then
        add_check "$name" "WARN" "disk $usage%% used (>=85%%)"
    else
        add_check "$name" "FAIL" "disk $usage%% used (>=95%%, critical)"
    fi
}

check_tls_certs() {
    local name="tls certificate status"
    local certs_found=0
    local expired=0
    local expiring=0
    local now
    now="$(date +%s)"

    for cert_dir in "$ORVIX_CERT_DIR" "$CADDY_CERT_DIR"; do
        if [ ! -d "$cert_dir" ]; then
            continue
        fi
        local cert_file
        while IFS= read -r cert_file; do
            [ -f "$cert_file" ] || continue
            certs_found=$((certs_found + 1))
            local not_after
            not_after="$(openssl x509 -enddate -noout -in "$cert_file" 2>/dev/null | cut -d= -f2 || true)"
            if [ -z "$not_after" ]; then
                continue
            fi
            local expiry_epoch
            expiry_epoch="$(date -d "$not_after" +%s 2>/dev/null || echo '0')"
            if [ "$expiry_epoch" = "0" ]; then
                continue
            fi
            local days_left
            days_left=$(( (expiry_epoch - now) / 86400 ))
            if [ "$days_left" -le 0 ]; then
                expired=$((expired + 1))
            elif [ "$days_left" -le 30 ]; then
                expiring=$((expiring + 1))
            fi
        done < <(find "$cert_dir" -name '*.pem' -o -name '*.crt' 2>/dev/null || true)
    done

    if [ "$certs_found" -eq 0 ]; then
        add_check "$name" "WARN" "no TLS certificates found"
    elif [ "$expired" -gt 0 ]; then
        add_check "$name" "FAIL" "$expired expired, $expiring expiring within 30 days (of $certs_found total)"
    elif [ "$expiring" -gt 0 ]; then
        add_check "$name" "WARN" "$expiring certificate(s) expire within 30 days (of $certs_found total)"
    else
        add_check "$name" "PASS" "$certs_found certificate(s), none expiring within 30 days"
    fi
}

# ── Run all checks ─────────────────────────────────────────────────

run_all_checks() {
    check_orvix_service
    check_caddy_service
    check_redis_service
    check_config_parses
    check_db_reachable
    check_jwt_key
    check_vapid_key
    check_dkim_keys
    check_mailstore_path
    check_ports
    check_dns_public_ipv4
    check_dns_readiness
    check_backup_dir
    check_queue_health
    check_disk_usage
    check_tls_certs
}

# ── Output ─────────────────────────────────────────────────────────

print_json_output() {
    local i
    printf '{\n  "status": "%s",\n  "checks": [\n' "$STATUS"
    for i in "${!CHECKS[@]}"; do
        if [ "$i" -gt 0 ]; then
            printf ',\n'
        fi
        printf '    %s' "${CHECKS[$i]}"
    done
    printf '\n  ]\n}\n'
}

print_human_output() {
    printf '\n%s========================================%s\n' "$BOLD" "$NC"
    printf '%s          ORVIX DOCTOR REPORT%s\n' "$BOLD" "$NC"
    printf '%s========================================%s\n\n' "$BOLD" "$NC"
    printf '  %-6s  %-40s  %s\n' "STATUS" "CHECK" "DETAILS"
    printf '  %-6s  %-40s  %s\n' "------" "----------------------------------------" "-------"
    local i
    for i in "${!CHECKS[@]}"; do
        local entry="${CHECKS[$i]}"
        local name status message
        name="$(printf '%s' "$entry" | sed 's/.*"name":"\([^"]*\)".*/\1/')"
        status="$(printf '%s' "$entry" | sed 's/.*"status":"\([^"]*\)".*/\1/')"
        message="$(printf '%s' "$entry" | sed 's/.*"message":"\([^"]*\)".*/\1/')"
        print_human "$name" "$status" "$message"
    done
    printf '\n%s========================================%s\n' "$BOLD" "$NC"
    printf 'Overall status: '
    if [ "$STATUS" = "healthy" ]; then
        printf '%b%s%b\n' "$GREEN" "HEALTHY" "$NC"
    else
        printf '%b%s%b\n' "$RED" "UNHEALTHY" "$NC"
    fi
    printf '%s========================================%s\n' "$BOLD" "$NC"
}

# ── Entrypoint ─────────────────────────────────────────────────────

main() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --json)
                OUTPUT_JSON=1
                shift
                ;;
            --quiet)
                OUTPUT_QUIET=1
                shift
                ;;
            --help|-h)
                cat <<USAGE
Usage: orvix-doctor.sh [--json] [--quiet]

Flags:
  --json     Output machine-readable JSON
  --quiet    Silent mode; exit 0 if healthy, exit 1 if unhealthy
  --help     Show this message
USAGE
                exit 0
                ;;
            *)
                shift
                ;;
        esac
    done

    run_all_checks

    if [ "$OUTPUT_QUIET" = "1" ]; then
        if [ "$OUTPUT_JSON" = "1" ]; then
            print_json_output
        fi
        if [ "$STATUS" = "healthy" ]; then
            exit 0
        else
            exit 1
        fi
    fi

    if [ "$OUTPUT_JSON" = "1" ]; then
        print_json_output
    else
        print_human_output
    fi

    if [ "$STATUS" = "healthy" ]; then
        exit 0
    else
        exit 1
    fi
}

[ "${BASH_SOURCE[0]}" != "${0}" ] && return 0
main "$@"
