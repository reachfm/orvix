#!/usr/bin/env bash
set -euo pipefail

# Orvix Uninstaller (operator-initiated).
#
# Usage:
#   sudo bash uninstall.sh
#   sudo bash uninstall.sh --purge-data
#   sudo bash uninstall.sh --purge-caddy
#
# Default behaviour is a SAFE uninstall that:
#   1. Stops and disables the orvix service and oneshot unit.
#   2. Removes the binary, the systemd units, the sudoers
#      drop-in, and the orvix system user.
#   3. PRESERVES /etc/orvix (config + VAPID keys), /var/lib/orvix
#      (SQLite DB, mailstore, backups, JWT key), and /var/log/orvix
#      (logs) — these are copied into a timestamped backup under
#      /var/backups/orvix-uninstall/ so an accidental uninstall
#      is always recoverable.
#
# --purge-data   also deletes /var/lib/orvix, /etc/orvix,
#                /var/log/orvix from disk. Data destruction is
#                permanent and the operator must type the literal
#                confirmation phrase on top of --purge-data.
# --purge-caddy  also disables and removes the caddy unit (only
#                run this if setup-https.sh was used on this
#                host and you no longer need the reverse proxy).

BOLD=$'\033[1m'
RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[1;33m'
NC=$'\033[0m'

PURGE_DATA=0
PURGE_CADDY=0
CONFIRM_PHRASE="purge all orvix data"

usage() {
    cat <<USAGE
Usage:
  sudo bash uninstall.sh                # safe uninstall; data preserved in /var/backups
  sudo bash uninstall.sh --purge-data   # permanently remove all data (requires confirmation phrase)
  sudo bash uninstall.sh --purge-caddy  # also disable and remove the caddy service

Safe uninstall preserves:
  - /etc/orvix          (config, VAPID keys, license)
  - /var/lib/orvix      (SQLite DB, mailstore, backups, JWT key)
  - /var/log/orvix      (logs)
All three are copied into a timestamped backup directory under
/var/backups/orvix-uninstall/ before any destructive action.
USAGE
}

require_root() {
    [ "$(id -u)" -eq 0 ] || fail "must be run as root (or with sudo)"
}

fail() {
    printf '%bERROR:%b %s\n' "$RED" "$NC" "$*" >&2
    exit 1
}

info() {
    printf '%b%s%b %s\n' "$GREEN" "$1" "$NC" "${2:-}"
}

warn() {
    printf '%bWARN:%b %s\n' "$YELLOW" "$1" "$NC" "${2:-}"
}

# ─── argument parsing ──────────────────────────────────────────────

while [ $# -gt 0 ]; do
    case "$1" in
        --purge-data)
            PURGE_DATA=1
            shift
            ;;
        --purge-caddy)
            PURGE_CADDY=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            fail "unknown flag: $1 (try --help)"
            ;;
    esac
done

# ─── preflight ─────────────────────────────────────────────────────

require_root

printf '%b%s%b\n' "$BOLD" "Orvix Uninstaller"
printf '%s\n' "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
printf '\n'

# ─── confirmation gate ─────────────────────────────────────────────

if [ "$PURGE_DATA" = "1" ]; then
    printf '%bDESTRUCTIVE:%b --purge-data will PERMANENTLY delete all data.\n' "$RED" "$NC"
    printf 'Type the literal phrase below to continue:\n'
    printf '  %s\n' "$CONFIRM_PHRASE"
    printf '> '
    read -r reply
    if [ "$reply" != "$CONFIRM_PHRASE" ]; then
        fail "confirmation phrase did not match; aborting"
    fi
else
    printf 'This will stop the Orvix service and remove the binary + systemd units.\n'
    printf 'Configuration, data, and logs are PRESERVED on disk and copied into\n'
    printf 'a timestamped backup directory.\n\n'
    printf 'Continue? [y/N]: '
    read -r reply
    case "$reply" in
        y|Y|yes|YES)
            : # proceed
            ;;
        *)
            info "Uninstall cancelled."
            exit 0
            ;;
    esac
fi

# ─── backup data (always, even on --purge-data) ────────────────────

BACKUP_DIR="/var/backups/orvix-uninstall/$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$BACKUP_DIR"
printf '\n%b%s%b\n' "$BOLD" "[1/5] Backing up data" "$NC"

for src in /etc/orvix /var/lib/orvix /var/log/orvix; do
    if [ -d "$src" ]; then
        # rsync is not always present; fall back to cp -a which is
        # POSIX and preserves permissions / ownership.
        cp -a "$src" "$BACKUP_DIR/" 2>/dev/null \
            && info "✓ backed up $src" \
            || warn "could not fully back up $src"
    fi
done
info "Backup directory: $BACKUP_DIR"

# ─── stop services ─────────────────────────────────────────────────

printf '\n%b%s%b\n' "$BOLD" "[2/5] Stopping services" "$NC"
systemctl stop orvix.service 2>/dev/null || true
systemctl disable orvix.service 2>/dev/null || true
# orvix-update.service is operator-initiated and never enabled
# by install.sh, but disable it in case a previous operator
# enabled it manually.
systemctl disable orvix-update.service 2>/dev/null || true
info "✓ orvix services stopped and disabled"

# ─── remove binary, units, sudoers, user ────────────────────────────

printf '\n%b%s%b\n' "$BOLD" "[3/5] Removing binary, units, sudoers" "$NC"
rm -f /usr/local/bin/orvix
rm -f /etc/systemd/system/orvix.service
rm -f /etc/systemd/system/orvix-update.service
rm -f /etc/sudoers.d/orvix-update
systemctl daemon-reload || true
info "✓ binary + units + sudoers removed"

# Drop the orvix system user, but keep the home directory if
# --purge-data was NOT passed. userdel without -r leaves /var/lib/orvix
# in place (the user no longer owns it, so /var/lib/orvix will appear
# as numeric-uid under `ls -n` — this is intentional and harmless,
# and the data is preserved for a future reinstall).
if id -u orvix &>/dev/null; then
    userdel orvix 2>/dev/null \
        && info "✓ system user 'orvix' removed (home preserved when --purge-data not set)" \
        || warn "could not remove user 'orvix' (continuing)"
fi

# ─── remove scripts + UI assets ────────────────────────────────────

printf '\n%b%s%b\n' "$BOLD" "[4/5] Removing scripts + UI assets" "$NC"
rm -rf /usr/share/orvix/admin
rm -rf /usr/share/orvix/webmail
rm -rf /usr/share/orvix/marketing
rm -rf /usr/share/orvix/scripts
rm -rf /opt/orvix
info "✓ UI assets + helper scripts removed"

# ─── optional: purge data + caddy ──────────────────────────────────

printf '\n%b%s%b\n' "$BOLD" "[5/5] Final cleanup" "$NC"

if [ "$PURGE_DATA" = "1" ]; then
    rm -rf /var/lib/orvix
    rm -rf /etc/orvix
    rm -rf /var/log/orvix
    rm -rf "$BACKUP_DIR"  # explicit purge also wipes the backup
    info "✓ /var/lib/orvix, /etc/orvix, /var/log/orvix, and uninstall backup removed (DESTRUCTIVE)"
else
    info "✓ data preserved at /var/lib/orvix, /etc/orvix, /var/log/orvix"
    info "  (rerun with --purge-data to remove; type confirmation phrase)"
fi

if [ "$PURGE_CADDY" = "1" ]; then
    if command -v caddy >/dev/null 2>&1; then
        systemctl stop caddy 2>/dev/null || true
        systemctl disable caddy 2>/dev/null || true
        apt-get purge -y caddy 2>/dev/null || true
        rm -rf /etc/caddy
        info "✓ caddy removed"
    else
        info "✓ caddy not installed; skipping --purge-caddy"
    fi
fi

printf '\n%s\n' "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
printf '%bOrvix Uninstall Complete%b\n' "$BOLD" "$NC"
printf '\n'
if [ "$PURGE_DATA" = "1" ]; then
    info "All data purged."
else
    info "Backup preserved at: $BACKUP_DIR"
    info "To restore data, copy the backup back:"
    info "    sudo cp -a $BACKUP_DIR/etc/orvix  /etc/"
    info "    sudo cp -a $BACKUP_DIR/var/lib/orvix /var/lib/"
    info "    sudo cp -a $BACKUP_DIR/var/log/orvix /var/log/"
    info "Then sudo systemctl start orvix."
fi
printf '\n'
