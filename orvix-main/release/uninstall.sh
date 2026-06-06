#!/usr/bin/env bash
set -euo pipefail

# Orvix Uninstaller
# Removes Orvix from the system.
# Data backup is recommended before running this script.

BOLD='\033[1m'
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${BOLD}Orvix Uninstaller${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}Error: This script must be run as root (or with sudo).${NC}"
    exit 1
fi

BACKUP_DIR="/var/backups/orvix-uninstall-$(date +%Y%m%d_%H%M%S)"

echo ""
echo -e "${YELLOW}WARNING: This will remove Orvix from this system.${NC}"
echo "A backup of configuration and data will be saved to: $BACKUP_DIR"
echo ""

read -rp "Are you sure you want to uninstall Orvix? [y/N]: " CONFIRM
if [ "$CONFIRM" != "y" ] && [ "$CONFIRM" != "Y" ]; then
    echo "Uninstall cancelled."
    exit 0
fi

echo ""
echo -e "${BOLD}[1/4] Backing up data...${NC}"
mkdir -p "$BACKUP_DIR"
[ -d /etc/orvix ] && cp -r /etc/orvix "$BACKUP_DIR/etc-orvix" 2>/dev/null && echo -e "${GREEN}✓${NC} Config backed up"
[ -d /var/lib/orvix ] && cp -r /var/lib/orvix "$BACKUP_DIR/var-lib-orvix" 2>/dev/null && echo -e "${GREEN}✓${NC} Data backed up"

echo ""
echo -e "${BOLD}[2/4] Stopping services...${NC}"
systemctl stop orvix.service 2>/dev/null || true
systemctl disable orvix.service 2>/dev/null || true
echo -e "${GREEN}✓${NC} Services stopped and disabled"

echo ""
echo -e "${BOLD}[3/4] Removing files...${NC}"
rm -f /usr/local/bin/orvix
rm -f /usr/local/bin/stalwart
rm -f /etc/systemd/system/orvix.service
rm -f /etc/systemd/system/orvix-webmail.service
systemctl daemon-reload
echo -e "${GREEN}✓${NC} Binaries and service files removed"

echo ""
echo -e "${BOLD}[4/4] Cleaning up...${NC}"
id -u orvix &>/dev/null && userdel -r orvix 2>/dev/null && echo -e "${GREEN}✓${NC} User 'orvix' removed" || true
rm -rf /var/log/orvix 2>/dev/null || true
echo -e "${GREEN}✓${NC} Log files removed"

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "${BOLD}Orvix Uninstall Complete${NC}"
echo ""
echo -e "Backup saved to: ${YELLOW}$BACKUP_DIR${NC}"
echo "To restore data, copy the backup back to /etc/orvix and /var/lib/orvix"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
