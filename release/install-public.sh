#!/usr/bin/env bash
set -euo pipefail

# Orvix Enterprise Mail — public one-line installer entrypoint.
#
# Usage:
#   curl -fsSL https://releases.orvix.email/install-public.sh | bash
#   curl -fsSL https://releases.orvix.email/install-public.sh | bash -s -- --help
#
# Environment variables:
#   ORVIX_DOMAIN            Primary mail domain
#   ORVIX_PUBLIC_IPV4        Public IPv4 of this host
#   ORVIX_ADMIN_EMAIL        Admin email address
#   ORVIX_ADMIN_PASSWORD     Admin password (8-72 bytes)
#   ORVIX_SETUP_HTTPS        If set, invoke setup-https.sh after install
#   ORVIX_HARDEN_FIREWALL    If set, run firewall hardening after install
#   ORVIX_NON_INTERACTIVE    If set, require ORVIX_DOMAIN + ORVIX_PUBLIC_IPV4
#                            and run without any prompts. Fails with exit 1
#                            if required env vars are missing.

ORVIX_DOCS_URL="${ORVIX_DOCS_URL:-https://docs.orvix.email}"
ORVIX_INSTALL_URL="${ORVIX_INSTALL_URL:-https://releases.orvix.email/install.sh}"
ORVIX_SOURCE_DIR="${ORVIX_SOURCE_DIR:-$(pwd)}"

RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

usage() {
    cat <<USAGE
Orvix Enterprise Mail — public installer entrypoint

Usage:
  curl -fsSL ${ORVIX_INSTALL_URL} | bash
  curl -fsSL ${ORVIX_INSTALL_URL} | bash -s -- --help

Environment variables:
  ORVIX_DOMAIN            Primary mail domain (required in non-interactive mode)
  ORVIX_PUBLIC_IPV4        Public IPv4 (required in non-interactive mode)
  ORVIX_ADMIN_EMAIL        Admin email (optional; prompted if not set)
  ORVIX_ADMIN_PASSWORD     Admin password (optional; prompted if not set)
  ORVIX_SETUP_HTTPS        Run HTTPS setup after install (any non-empty value)
  ORVIX_HARDEN_FIREWALL    Run firewall hardening after install
  ORVIX_NON_INTERACTIVE    Non-interactive mode; requires ORVIX_DOMAIN + ORVIX_PUBLIC_IPV4

Flags:
  --help, -h               Show this message
  --version                Show version info

Docs: $ORVIX_DOCS_URL
USAGE
}

# is_valid_public_ipv4 returns 0 (success) iff $1 is a syntactically
# valid, routable, public IPv4 address. Mirrors the identical helper in
# release/install.sh so validation happens BEFORE delegation.
#
# Rejects:
#   - empty string
#   - any string containing ':' (IPv6)
#   - 0.0.0.0 (unspecified)
#   - 127.0.0.0/8 (loopback)
#   - 10.0.0.0/8 (RFC1918 private)
#   - 100.64.0.0/10 (carrier-grade NAT / shared address space, RFC6598)
#   - 169.254.0.0/16 (link-local)
#   - 172.16.0.0/12 (RFC1918 private; covers 172.16.0.0–172.31.255.255)
#   - 192.0.0.0/24 (special-use, RFC6890)
#   - 192.0.2.0/24, 198.51.100.0/24, 203.0.113.0/24 (RFC5737 documentation)
#   - 192.168.0.0/16 (RFC1918 private)
#   - 198.18.0.0/15 (benchmarking, RFC2544)
#   - 224.0.0.0/4 (multicast) and 240.0.0.0/4 (reserved)
#   - any string not matching the strict dotted-quad regex
is_valid_public_ipv4() {
    local ip="${1:-}"
    [ -n "$ip" ] || return 1
    if ! [[ "$ip" =~ ^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])$ ]]; then
        return 1
    fi
    local o1 o2 o3 o4
    IFS=. read -r o1 o2 o3 o4 <<< "$ip"
    if [ "$o1" -eq 0 ]; then
        return 1
    fi
    if [ "$o1" -eq 10 ]; then
        return 1
    fi
    if [ "$o1" -eq 100 ] && [ "$o2" -ge 64 ] && [ "$o2" -le 127 ]; then
        return 1
    fi
    if [ "$o1" -eq 127 ]; then
        return 1
    fi
    if [ "$o1" -eq 169 ] && [ "$o2" -eq 254 ]; then
        return 1
    fi
    if [ "$o1" -eq 172 ] && [ "$o2" -ge 16 ] && [ "$o2" -le 31 ]; then
        return 1
    fi
    if [ "$o1" -eq 192 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 0 ]; then
        return 1
    fi
    if [ "$o1" -eq 192 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 2 ]; then
        return 1
    fi
    if [ "$o1" -eq 192 ] && [ "$o2" -eq 168 ]; then
        return 1
    fi
    if [ "$o1" -eq 198 ] && { [ "$o2" -eq 18 ] || [ "$o2" -eq 19 ]; }; then
        return 1
    fi
    if [ "$o1" -eq 198 ] && [ "$o2" -eq 51 ] && [ "$o3" -eq 100 ]; then
        return 1
    fi
    if [ "$o1" -eq 203 ] && [ "$o2" -eq 0 ] && [ "$o3" -eq 113 ]; then
        return 1
    fi
    if [ "$o1" -ge 224 ] && [ "$o1" -le 239 ]; then
        return 1
    fi
    if [ "$o1" -ge 240 ]; then
        return 1
    fi
    return 0
}

# Detect if running inside a git worktree of the Orvix repo.
# Returns the repo root directory if found, empty string otherwise.
detect_worktree() {
    local d
    d="$(pwd)"
    while [ -n "$d" ] && [ "$d" != "/" ]; do
        if [ -f "$d/release/install.sh" ] && [ -f "$d/go.mod" ]; then
            if grep -q 'module github.com/orvix/orvix' "$d/go.mod" 2>/dev/null; then
                printf '%s' "$d"
                return 0
            fi
        fi
        d="$(dirname "$d")"
    done
    return 1
}

fail() {
    printf '%bERROR:%b %s\n' "$RED" "$NC" "$*" >&2
    exit 1
}

warn() {
    printf '%bWARN:%b %s\n' "$YELLOW" "$NC" "$*" >&2
}

# ── Main ──────────────────────────────────────────────────────────

main() {
    local non_interactive="${ORVIX_NON_INTERACTIVE:-}"
    local domain="${ORVIX_DOMAIN:-}"
    local public_ipv4="${ORVIX_PUBLIC_IPV4:-}"
    local admin_email="${ORVIX_ADMIN_EMAIL:-}"
    local admin_password="${ORVIX_ADMIN_PASSWORD:-}"
    local setup_https="${ORVIX_SETUP_HTTPS:-}"
    local harden_firewall="${ORVIX_HARDEN_FIREWALL:-}"

    while [ $# -gt 0 ]; do
        case "$1" in
            --help|-h)
                usage
                exit 0
                ;;
            --version|-V)
                printf 'Orvix Enterprise Mail — public installer v2.0.0\n'
                exit 0
                ;;
            *)
                warn "unrecognised argument: $1 (use --help for usage)"
                shift
                ;;
        esac
    done

    # Non-interactive mode: require ORVIX_DOMAIN and ORVIX_PUBLIC_IPV4.
    if [ -n "$non_interactive" ]; then
        if [ -z "$domain" ]; then
            fail "ORVIX_DOMAIN is required in non-interactive mode"
        fi
        if [ -z "$public_ipv4" ]; then
            fail "ORVIX_PUBLIC_IPV4 is required in non-interactive mode"
        fi
    fi

    # Validate ORVIX_PUBLIC_IPV4 if set.
    if [ -n "$public_ipv4" ]; then
        if ! is_valid_public_ipv4 "$public_ipv4"; then
            cat >&2 <<ERR
${RED}ERROR: invalid ORVIX_PUBLIC_IPV4: $public_ipv4${NC}

The value must be a routable public IPv4 address. The following are rejected:
  - 127.0.0.1 and other loopback addresses
  - 0.0.0.0 (unspecified)
  - 192.168.x.x, 10.x.x.x, 172.16-31.x.x (private RFC1918)
  - 100.64-127.x.x (CGNAT)
  - 169.254.x.x (link-local)
  - 192.0.2.x, 198.51.100.x, 203.0.113.x (documentation ranges)
  - 224-255.x.x.x (multicast/reserved)
  - IPv6 addresses

Set ORVIX_PUBLIC_IPV4 to your VPS public IPv4 address.
ERR
            exit 1
        fi
    fi

    # Determine the installer script source.
    local installer_script

    # Check for git worktree first.
    local worktree_root
    if worktree_root="$(detect_worktree 2>/dev/null || true)" && [ -n "$worktree_root" ]; then
        installer_script="$worktree_root/release/install.sh"
        if [ -f "$installer_script" ]; then
            printf '%bINFO:%b using local install.sh from git worktree: %s\n' "$GREEN" "$NC" "$installer_script" >&2
        else
            fail "git worktree detected at $worktree_root but release/install.sh not found"
        fi
    elif [ -f "$ORVIX_SOURCE_DIR/release/install.sh" ]; then
        installer_script="$ORVIX_SOURCE_DIR/release/install.sh"
        printf '%bINFO:%b using local install.sh: %s\n' "$GREEN" "$NC" "$installer_script" >&2
    else
        # Download from the release URL.
        local tmp_installer
        tmp_installer="$(mktemp /tmp/orvix-install.XXXXXX)"
        printf '%bINFO:%b downloading install.sh from %s\n' "$GREEN" "$NC" "$ORVIX_INSTALL_URL" >&2
        if ! curl -fsSL --retry 3 --max-time 120 -o "$tmp_installer" "$ORVIX_INSTALL_URL"; then
            rm -f "$tmp_installer"
            fail "could not download install.sh from $ORVIX_INSTALL_URL; check network connectivity"
        fi
        installer_script="$tmp_installer"
        trap 'rm -f "$tmp_installer"' EXIT
    fi

    if [ ! -f "$installer_script" ]; then
        fail "installer script not found at $installer_script"
    fi

    # Export all ORVIX_* env vars so install.sh picks them up.
    export ORVIX_DOMAIN="$domain"
    export ORVIX_PUBLIC_IPV4="$public_ipv4"
    export ORVIX_ADMIN_EMAIL="$admin_email"
    export ORVIX_ADMIN_PASSWORD="$admin_password"
    export ORVIX_SETUP_HTTPS="$setup_https"
    export ORVIX_HARDEN_FIREWALL="$harden_firewall"
    export ORVIX_NON_INTERACTIVE="$non_interactive"

    # Delegate to install.sh. Pass through all remaining args
    # (after stripping our own flags) and inherit env vars.
    printf '%bINFO:%b delegating to %s\n' "$GREEN" "$NC" "$installer_script" >&2
    exec bash "$installer_script"
}

main "$@"
