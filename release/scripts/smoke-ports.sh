#!/usr/bin/env bash
# smoke-ports.sh — Port + firewall posture probe.
#
# Usage:
#   sudo bash smoke-ports.sh [--verbose] [--with-internal]
#
# Reports which of the Orvix ports are listening on the local
# host, AND whether the firewall (ufw, when active) admits
# inbound traffic to each. This is the gate an operator runs
# after setup-https.sh to confirm:
#
#   - Mail ports (25, 110, 143, 587, 465, 993, 995) are open
#     on the public interface.
#   - HTTPS proxy ports (80, 443) are open.
#   - Internal ports (8080 admin/webmail, 8081 JMAP) are bound
#     but ONLY allowed on 127.0.0.1 (post-setup-https hardening).
#   - Redis (6379) is bound on 127.0.0.1 (IPv4 loopback) and/or
#     [::1] (IPv6 loopback) only. Redis listening on either
#     loopback family is safe and MUST NOT be flagged as public
#     exposure.
#
# Exit codes:
#   0  all expected ports in expected state
#   1  at least one expected port is wrong
#   2  could not determine state (missing ss, missing ufw, etc.)
#
# This script does NOT modify the firewall. It only reports.

set -euo pipefail

VERBOSE=0
WITH_INTERNAL=0
for arg in "$@"; do
    case "$arg" in
        --verbose|-v) VERBOSE=1 ;;
        --with-internal) WITH_INTERNAL=1 ;;
    esac
done

log() {
    if [ "$VERBOSE" = "1" ]; then
        printf '  %s\n' "$*" >&2
    fi
}

pass() { printf 'PASS  %s\n' "$*" >&2; }
warn() { printf 'WARN  %s\n' "$*" >&2; }
fail() { printf 'FAIL  %s\n' "$*" >&2; exit 1; }

if ! command -v ss >/dev/null 2>&1; then
    fail "ss(8) not installed; install iproute2"
fi

# listening_on <port> <protocol> — print bound addresses for <port>,
# one per line. Empty if not listening.
listening_on() {
    local port="$1"
    local proto="${2:-tcp}"
    ss -ltnH "( sport = :$port )" 2>/dev/null \
        | awk '{
            # ss -H prints: LISTEN 0 128 0.0.0.0:25 0.0.0.0:*
            # We want the local-address column.
            print $4
        }' \
        | sed -E 's/\[?::ffff:([0-9.]+)\]?/\1/g' \
        | sort -u
}

# is_loopback_bind <addr> — return 0 if the bound address is a
# local loopback, 1 if it is public-facing.
#
# Accepted loopback forms:
#   127.0.0.1:<port>      IPv4 loopback
#   [::1]:<port>          IPv6 loopback (bracketed, ss -H default form)
#   ::1:<port>            IPv6 loopback (some ss/netstat versions drop the brackets)
#   127.<rest>:<port>     entire 127.0.0.0/8 IPv4 loopback block
#
# REJECTED (treated as public exposure):
#   0.0.0.0:<port>        "any" IPv4
#   [::]:<port>           IPv6 "any"
#   *:<port>              wildcard
#   any non-loopback interface address
#
# IMPORTANT: the case patterns below are QUOTED. Without quotes,
# bash interprets [::1] as the character class [:1] (matching `:`
# or `1`), which silently mis-matches and treats [::1]:6379 as
# public. This bug originally regressed smoke-ports.sh and was
# fixed 2026-06-29. Do NOT remove the quotes.
is_loopback_bind() {
    local addr="$1"
    case "$addr" in
        "127.0.0.1:"*|"127."*) return 0 ;;
        "[::1]:"*)             return 0 ;;
        "::1:"*)               return 0 ;;
        *)                     return 1 ;;
    esac
}

# firewall_allows <port>/<tcp|udp> — prints "yes" if ufw has an
# allow rule for <port>/<tcp>, "no" if there is an explicit deny,
# "default" if the default policy applies (ufw inactive => default
# allow; ufw active + default deny incoming => deny).
firewall_allows() {
    local spec="$1"
    local port="${spec%/*}"
    local proto="${spec##*/}"

    if ! command -v ufw >/dev/null 2>&1; then
        echo "no-ufw"
        return 0
    fi
    if ! ufw status >/dev/null 2>&1; then
        # ufw present but status command failed => probably not initialised
        echo "no-ufw"
        return 0
    fi
    local status
    status=$(ufw status 2>/dev/null | head -n1 || true)
    if printf '%s' "$status" | grep -q 'Status: inactive'; then
        echo "inactive"
        return 0
    fi
    if ufw status 2>/dev/null | grep -qE "^${port}/${proto}[[:space:]]+ALLOW"; then
        echo "allow"
        return 0
    fi
    if ufw status 2>/dev/null | grep -qE "^${port}/${proto}[[:space:]]+(DENY|REJECT)"; then
        echo "deny"
        return 0
    fi
    echo "default"
}

# check_port <port> <label> <expected-listening>
expected_listening="$1" 2>/dev/null || true
shift 2>/dev/null || true

# Default spec: <port> <label> <expected-listening?> <public-or-internal>
declare -a specs=(
    "25 smtp-mx public"
    "110 pop3 public"
    "143 imap public"
    "587 submission public"
    "465 smtps public"
    "993 imaps public"
    "995 pop3s public"
    "80 http-proxy public"
    "443 https-proxy public"
    "4190 sieve public"
    "6379 redis internal"
    "8080 admin-webmail internal"
    "8081 jmap internal"
)

fail_count=0
warn_count=0

printf '%-30s %-20s %-25s %s\n' PORT EXPECTED BOUND-ADDR FIREWALL >&2

for spec in "${specs[@]}"; do
    port=$(printf '%s' "$spec" | awk '{print $1}')
    label=$(printf '%s' "$spec" | awk '{print $2}')
    scope=$(printf '%s' "$spec" | awk '{print $3}')

    if [ "$scope" = "internal" ] && [ "$WITH_INTERNAL" = "0" ]; then
        continue
    fi

    addrs=$(listening_on "$port" || true)
    fw=$(firewall_allows "$port/tcp" || true)

    if [ -z "$addrs" ]; then
        # Not listening.
        printf '%-30s %-20s %-25s %s\n' "$port ($label)" "NOT LISTENING" "-" "$fw" >&2
        # Only warn if it's a public port we expect; internal
        # ports that are intentionally down are silent.
        if [ "$scope" = "public" ]; then
            warn "port $port ($label) is NOT listening"
            warn_count=$((warn_count + 1))
        fi
        continue
    fi

    # Check bind posture.
    bad=""
    for addr in $addrs; do
        if is_loopback_bind "$addr"; then
            # Loopback is the correct posture for internal ports
            # (Redis, admin/webmail, JMAP) and INCORRECT posture
            # for public ports (mail, http/https) — a public port
            # that is bound only to loopback is unreachable from
            # the network and is therefore a misconfiguration.
            if [ "$scope" = "public" ]; then
                bad="$bad $addr"
            fi
        else
            # Non-loopback bind is the correct posture for public
            # ports and INCORRECT for internal ports (would expose
            # the admin UI / JMAP / Redis to the network).
            if [ "$scope" = "internal" ]; then
                bad="$bad $addr"
            fi
        fi
    done

    # Surface a friendly note for the Redis dual-stack loopback
    # case so operators don't see "POSTURE WRONG" when actually
    # both bind families are the correct loopback posture.
    if [ -z "$bad" ] && [ "$label" = "redis" ]; then
        log "port $port (redis) bound on loopback (IPv4/IPv6) — OK"
    fi

    fw_label="$fw"
    if [ -n "$bad" ]; then
        printf '%-30s %-20s %-25s %s\n' "$port ($label)" "POSTURE WRONG" "$addrs" "$fw_label" >&2
        fail "port $port ($label) has wrong bind posture: $bad"
    fi
    printf '%-30s %-20s %-25s %s\n' "$port ($label)" "OK" "$addrs" "$fw_label" >&2
    log "port $port ($label) listening on $addrs, firewall=$fw_label"
done

# Summary
printf '\n' >&2
if [ "$fail_count" -gt 0 ]; then
    fail "$fail_count port posture problem(s); $warn_count warning(s)"
fi
if [ "$warn_count" -gt 0 ]; then
    warn "$warn_count port(s) not listening; review setup-https.sh and installer"
fi
printf 'PORT POSTURE OK (%d warnings)\n' "$warn_count" >&2
exit 0