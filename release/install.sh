#!/usr/bin/env bash
set -euo pipefail

# Orvix Installer
# Usage: curl -fsSL https://orvix.email/install.sh | bash
# Or:    bash install.sh

ORVIX_VERSION="${ORVIX_VERSION:-1.0.0}"
ORVIX_RELEASE_URL="${ORVIX_RELEASE_URL:-https://releases.orvix.email/v${ORVIX_VERSION}}"
STALWART_VERSION="${STALWART_VERSION:-0.10.0}"
STALWART_URL="https://github.com/stalwartlabs/mail-server/releases/download/v${STALWART_VERSION}/stalwart-mail-server-${STALWART_VERSION}-x86_64-unknown-linux-gnu.tar.gz"

BOLD='\033[1m'
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# ──────────────────────────────────────
# Pre-flight checks
# ──────────────────────────────────────
echo -e "${BOLD}Orvix v${ORVIX_VERSION} Installer${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if [ "$(id -u)" -ne 0 ]; then
    echo -e "${RED}Error: This installer must be run as root (or with sudo).${NC}"
    exit 1
fi

OS=""
if [ -f /etc/os-release ]; then
    . /etc/os-release
    OS="$ID"
fi

case "$OS" in
    ubuntu|debian)
        echo -e "${GREEN}✓${NC} Detected OS: $PRETTY_NAME"
        ;;
    *)
        echo -e "${YELLOW}Warning:${NC} Detected OS: ${OS:-unknown}. Ubuntu 22.04+/Debian 12+ recommended."
        echo "Proceeding anyway..."
        ;;
esac

# ──────────────────────────────────────
# Functions
# ──────────────────────────────────────
prompt() {
    local prompt_text="$1"
    local default_value="$2"
    local input
    if [ -n "$default_value" ]; then
        read -rp "$prompt_text [$default_value]: " input
        echo "${input:-$default_value}"
    else
        read -rp "$prompt_text: " input
        echo "$input"
    fi
}

generate_password() {
    tr -dc 'A-Za-z0-9!@#$%^&*()_+' < /dev/urandom 2>/dev/null | head -c 24 || echo "orvix$(date +%s)"
}

cleanup() {
    local exit_code=$?
    if [ $exit_code -ne 0 ]; then
        echo -e "\n${RED}Installation failed at step: ${CURRENT_STEP:-unknown}${NC}"
        echo "Check /var/log/orvix/install.log for details."
    fi
    exit $exit_code
}
trap cleanup EXIT

CURRENT_STEP="preflight"

# ──────────────────────────────────────
# Step 1: Install system dependencies
# ──────────────────────────────────────
CURRENT_STEP="dependencies"
echo ""
echo -e "${BOLD}[1/8] Installing system dependencies...${NC}"

apt-get update -qq
apt-get install -y -qq \
    curl wget tar gzip \
    sqlite3 ca-certificates \
    systemd

if command -v redis-server &>/dev/null; then
    echo -e "${GREEN}✓${NC} Redis detected"
else
    echo -e "${YELLOW}⚠${NC} Redis not detected (optional — rate limiting falls back to in-memory)"
fi

# ──────────────────────────────────────
# Step 2: Create system user and groups
# ──────────────────────────────────────
CURRENT_STEP="user"
echo ""
echo -e "${BOLD}[2/8] Creating system user...${NC}"

if ! id -u orvix &>/dev/null; then
    useradd --system --user-group --create-home --home-dir /var/lib/orvix --shell /usr/sbin/nologin orvix
    echo -e "${GREEN}✓${NC} Created system user 'orvix'"
else
    echo -e "${GREEN}✓${NC} System user 'orvix' already exists"
fi

# ──────────────────────────────────────
# Step 3: Create directories
# ──────────────────────────────────────
CURRENT_STEP="directories"
echo ""
echo -e "${BOLD}[3/8] Creating directories...${NC}"

mkdir -p /etc/orvix/stalwart
mkdir -p /var/lib/orvix/stalwart
mkdir -p /var/lib/orvix/backups
mkdir -p /var/log/orvix/stalwart

chown -R orvix:orvix /etc/orvix
chown -R orvix:orvix /var/lib/orvix
chown -R orvix:orvix /var/log/orvix

chmod 750 /etc/orvix
chmod 750 /var/lib/orvix
chmod 750 /var/log/orvix

echo -e "${GREEN}✓${NC} Directories created with secure permissions"

# ──────────────────────────────────────
# Step 4: Install Orvix binary
# ──────────────────────────────────────
CURRENT_STEP="orvix_binary"
echo ""
echo -e "${BOLD}[4/8] Installing Orvix binary...${NC}"

ORVIX_BIN="/usr/local/bin/orvix"

if [ -f "release/orvix-linux-amd64" ]; then
    cp "release/orvix-linux-amd64" "$ORVIX_BIN"
    echo -e "${GREEN}✓${NC} Using local binary"
elif [ -f "orvix-linux-amd64" ]; then
    cp "orvix-linux-amd64" "$ORVIX_BIN"
    echo -e "${GREEN}✓${NC} Using local binary"
elif command -v orvix &>/dev/null; then
    ORVIX_BIN=$(command -v orvix)
    echo -e "${GREEN}✓${NC} Orvix binary already installed at $ORVIX_BIN"
else
    echo "Downloading Orvix v${ORVIX_VERSION}..."
    curl -fsSL -o /tmp/orvix "${ORVIX_RELEASE_URL}/orvix-linux-amd64" || {
        echo -e "${RED}Failed to download Orvix binary${NC}"
        echo "Please build the binary manually: cd orvix && make build"
        echo "Then re-run this installer."
        exit 1
    }
    mv /tmp/orvix "$ORVIX_BIN"
    echo -e "${GREEN}✓${NC} Downloaded Orvix binary"
fi

chmod 755 "$ORVIX_BIN"
echo -e "${GREEN}✓${NC} Orvix binary installed at $ORVIX_BIN"

# ──────────────────────────────────────
# Step 5: Install Stalwart binary
# ──────────────────────────────────────
CURRENT_STEP="stalwart_binary"
echo ""
echo -e "${BOLD}[5/8] Installing Stalwart mail server...${NC}"

STALWART_BIN="/usr/local/bin/stalwart"

if command -v stalwart &>/dev/null; then
    echo -e "${GREEN}✓${NC} Stalwart already installed at $(command -v stalwart)"
elif [ -f "/usr/local/bin/stalwart" ]; then
    echo -e "${GREEN}✓${NC} Stalwart already installed"
else
    echo "Downloading Stalwart v${STALWART_VERSION}..."
    STALWART_TMP="/tmp/stalwart-${STALWART_VERSION}"
    mkdir -p "$STALWART_TMP"

    if curl -fsSL -o /tmp/stalwart.tar.gz "$STALWART_URL"; then
        tar -xzf /tmp/stalwart.tar.gz -C "$STALWART_TMP" --strip-components=1 2>/dev/null || true
        FOUND_STALWART=$(find "$STALWART_TMP" -name "stalwart" -type f 2>/dev/null | head -1)
        if [ -n "$FOUND_STALWART" ]; then
            cp "$FOUND_STALWART" "$STALWART_BIN"
            chmod 755 "$STALWART_BIN"
            echo -e "${GREEN}✓${NC} Stalwart installed at $STALWART_BIN"
        else
            echo -e "${YELLOW}⚠${NC} Stalwart binary not found in release archive."
            echo "You can download it manually from: https://stalw.art/download"
        fi
        rm -f /tmp/stalwart.tar.gz
    else
        echo -e "${YELLOW}⚠${NC} Could not download Stalwart automatically."
        echo "You can download it manually from: https://stalw.art/download"
    fi
    rm -rf "$STALWART_TMP"
fi

# ──────────────────────────────────────
# Step 6: Generate configuration
# ──────────────────────────────────────
CURRENT_STEP="config"
echo ""
echo -e "${BOLD}[6/8] Configuring Orvix...${NC}"

HOSTNAME=$(hostname -f 2>/dev/null || hostname)
PRIMARY_DOMAIN=$(prompt "Primary email domain" "$HOSTNAME")
ADMIN_EMAIL=$(prompt "Admin email address" "admin@$PRIMARY_DOMAIN")
ADMIN_PASSWORD=$(prompt "Admin password" "$(generate_password)")
LICENSE_KEY=$(prompt "License key (optional)" "")

cat > /etc/orvix/orvix.yaml << ORVIX_YAML
server:
  host: "0.0.0.0"
  admin_port: 8080
  read_timeout: 60s
  write_timeout: 60s
  idle_timeout: 120s
  body_limit: 52428800
  allowed_origins:
    - "https://mail.$PRIMARY_DOMAIN"
  trusted_proxies: []

database:
  driver: sqlite
  sqlite_path: /var/lib/orvix/orvix.db

stalwart:
  api_url: http://localhost:18080
  data_dir: /var/lib/orvix/stalwart
  config_dir: /etc/orvix/stalwart
  log_dir: /var/log/orvix/stalwart

auth:
  jwt_access_ttl: 15m
  jwt_refresh_ttl: 720h
  password_min_len: 12
  argon2_time: 3
  argon2_memory: 65536
  argon2_threads: 4

logging:
  level: info
  format: json
  output: stdout
  log_dir: /var/log/orvix

metrics:
  enabled: true
  path: /metrics

update:
  channel: stable
  auto_apply: false
  backup_before: true

backup:
  dir: /var/lib/orvix/backups
ORVIX_YAML

chmod 640 /etc/orvix/orvix.yaml
chown orvix:orvix /etc/orvix/orvix.yaml

cat > /etc/orvix/install_credentials.txt << CREDS
Orvix Installation Credentials
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Admin Email: $ADMIN_EMAIL
Admin Password: $ADMIN_PASSWORD
License Key: ${LICENSE_KEY:-not provided}

Admin URL: http://$(hostname -I | awk '{print $1}'):8080/admin
Webmail URL: http://$(hostname -I | awk '{print $1}'):3000
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Save these credentials in a secure location.
CREDS

chmod 600 /etc/orvix/install_credentials.txt

echo -e "${GREEN}✓${NC} Configuration generated"
echo -e "${GREEN}✓${NC} Credentials saved to /etc/orvix/install_credentials.txt"

# ──────────────────────────────────────
# Step 7: Install systemd service
# ──────────────────────────────────────
CURRENT_STEP="systemd"
echo ""
echo -e "${BOLD}[7/8] Installing systemd service...${NC}"

cat > /etc/systemd/system/orvix.service << 'UNIT'
[Unit]
Description=Orvix Email Server Platform
Documentation=https://orvix.email/docs
After=network.target postgresql.service redis.service
Wants=network.target

[Service]
Type=simple
User=orvix
Group=orvix
ExecStart=/usr/local/bin/orvix
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=10
StartLimitBurst=3
StartLimitIntervalSec=60

# Security hardening
ProtectSystem=full
ProtectHome=true
NoNewPrivileges=true
PrivateTmp=true

# Environment
Environment=ORVIX_CONFIG=/etc/orvix/orvix.yaml

# File limits
LimitNOFILE=65536
LimitNPROC=4096

# Logging
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable orvix.service

echo -e "${GREEN}✓${NC} systemd service installed and enabled"

# ──────────────────────────────────────
# Step 8: Start services
# ──────────────────────────────────────
CURRENT_STEP="start"
echo ""
echo -e "${BOLD}[8/8] Starting services...${NC}"

systemctl start orvix.service || {
    echo -e "${YELLOW}⚠${NC} Service failed to start. Check logs: journalctl -u orvix.service -n 50"
}

# Health check
sleep 3
if systemctl is-active --quiet orvix.service; then
    echo -e "${GREEN}✓${NC} Orvix service is running"
else
    echo -e "${YELLOW}⚠${NC} Orvix service not active. Check: journalctl -u orvix.service"
fi

# ──────────────────────────────────────
# Summary
# ──────────────────────────────────────
IP_ADDR=$(hostname -I | awk '{print $1}')
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "${BOLD}Orvix v${ORVIX_VERSION} Installation Complete${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo -e "  ${BOLD}Admin Console:${NC}  http://${IP_ADDR}:8080/admin"
echo -e "  ${BOLD}Webmail:${NC}       http://${IP_ADDR}:3000"
echo -e "  ${BOLD}Admin Email:${NC}   ${ADMIN_EMAIL}"
echo -e "  ${BOLD}Logs:${NC}          journalctl -u orvix.service -f"
echo ""
echo "Next steps:"
echo "  1. Configure your DNS:"
echo "     - MX record → your server IP (priority 10)"
echo "     - A record  → your server IP"
echo "     - txt record 'v=spf1 mx ~all'"
echo "  2. Login to admin console at http://${IP_ADDR}:8080/admin"
echo "  3. Add your domain"
echo "  4. Create mailboxes"
echo "  5. Configure Stalwart API key for full functionality"
echo ""
echo -e "${YELLOW}Credentials saved to:${NC} /etc/orvix/install_credentials.txt"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
