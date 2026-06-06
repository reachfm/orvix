#!/usr/bin/env bash
set -euo pipefail

# Orvix Upgrade Script
# Usage: bash upgrade.sh [version]
# If version is omitted, the latest stable version will be used.

BOLD='\033[1m'
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}Error: This script must be run as root (or with sudo).${NC}"
    exit 1
fi

NEW_VERSION="${1:-latest}"
BACKUP_DIR="/var/backups/orvix-upgrade-$(date +%Y%m%d_%H%M%S)"
RELEASE_URL="${ORVIX_RELEASE_URL:-https://releases.orvix.email}"

echo -e "${BOLD}Orvix Upgrade${NC}"
echo "Target version: ${NEW_VERSION}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Check current version
if command -v orvix &>/dev/null; then
    echo -e "Current: ${GREEN}$(orvix version 2>/dev/null || echo "unknown")${NC}"
fi

echo ""
echo -e "${BOLD}[1/4] Backing up current installation...${NC}"
mkdir -p "$BACKUP_DIR"
cp /usr/local/bin/orvix "$BACKUP_DIR/orvix.backup" 2>/dev/null || true
[ -f /etc/orvix/orvix.yaml ] && cp /etc/orvix/orvix.yaml "$BACKUP_DIR/orvix.yaml.backup"
echo -e "${GREEN}✓${NC} Backup saved to $BACKUP_DIR"

echo ""
echo -e "${BOLD}[2/4] Downloading new version...${NC}"
if [ "$NEW_VERSION" = "latest" ]; then
    DOWNLOAD_URL="${RELEASE_URL}/latest/orvix-linux-amd64"
else
    DOWNLOAD_URL="${RELEASE_URL}/v${NEW_VERSION}/orvix-linux-amd64"
fi

if [ -f "orvix-linux-amd64" ]; then
    cp orvix-linux-amd64 /usr/local/bin/orvix
    echo -e "${GREEN}✓${NC} Using local binary"
else
    curl -fsSL -o /tmp/orvix-new "$DOWNLOAD_URL" || {
        echo -e "${RED}Failed to download update.${NC}"
        echo "Rolling back..."
        [ -f "$BACKUP_DIR/orvix.backup" ] && cp "$BACKUP_DIR/orvix.backup" /usr/local/bin/orvix
        exit 1
    }
    mv /tmp/orvix-new /usr/local/bin/orvix
    echo -e "${GREEN}✓${NC} Downloaded new version"
fi

chmod 755 /usr/local/bin/orvix

echo ""
echo -e "${BOLD}[3/4] Restarting service...${NC}"
systemctl restart orvix.service || {
    echo -e "${RED}Service failed to restart.${NC}"
    echo "Rolling back..."
    [ -f "$BACKUP_DIR/orvix.backup" ] && cp "$BACKUP_DIR/orvix.backup" /usr/local/bin/orvix
    systemctl restart orvix.service || true
    exit 1
}

echo ""
echo -e "${BOLD}[4/4] Verifying health...${NC}"
sleep 3
if systemctl is-active --quiet orvix.service; then
    echo -e "${GREEN}✓${NC} Orvix is running after upgrade"
else
    echo -e "${YELLOW}⚠${NC} Service not running. Check: journalctl -u orvix.service"
    echo "Rolling back..."
    [ -f "$BACKUP_DIR/orvix.backup" ] && cp "$BACKUP_DIR/orvix.backup" /usr/local/bin/orvix
    systemctl restart orvix.service || true
fi

echo ""
echo -e "${GREEN}Upgrade complete.${NC}"
echo -e "Backup saved to: ${YELLOW}$BACKUP_DIR${NC}"
echo -e "To rollback manually: cp $BACKUP_DIR/orvix.backup /usr/local/bin/orvix && systemctl restart orvix"
