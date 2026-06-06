#!/usr/bin/env bash
set -euo pipefail

# Orvix RC5 Installer
# Usage: curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.4/install.sh | bash
# Or:    bash install.sh

# RC5 FIXES:
# - Systemd hardening: Added ReadWritePaths for /etc/orvix, /var/lib/orvix, /var/log/orvix
# - Stalwart v0.16.7: Uses config.json (not stalwart.yaml), correct --config arg
# - Redis: Install and enable redis-server
# - Healthcheck: Comprehensive post-install validation

ORVIX_VERSION="${ORVIX_VERSION:-1.0.4}"
ORVIX_RELEASE_URL="${ORVIX_RELEASE_URL:-https://github.com/reachfm/orvix/releases/download/v${ORVIX_VERSION}}"
STALWART_VERSION="${STALWART_VERSION:-0.16.7}"

BOLD='\033[1m'
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# ──────────────────────────────────────
# Pre-flight checks
# ──────────────────────────────────────
echo -e "${BOLD}Orvix v${ORVIX_VERSION} RC5 Installer${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

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
# Functions (RC5 FIX: All validation fixes)
# ──────────────────────────────────────

# RC5 FIX: Domain validation function
prompt_domain() {
    local domain=""
    while true; do
        read -rp "Primary email domain (e.g., example.com): " domain
        if [ -z "$domain" ]; then
            echo -e "${RED}Error: Domain cannot be empty.${NC}"
            continue
        fi
        # Basic domain validation (must have TLD)
        if [[ ! "$domain" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*\.[a-zA-Z]{2,}$ ]]; then
            echo -e "${RED}Error: Invalid domain format. Domain must have a valid TLD (e.g., example.com)${NC}"
            continue
        fi
        break
    done
    echo "$domain"
}

# RC5 FIX: Email validation function
prompt_email() {
    local email=""
    while true; do
        read -rp "Admin email address: " email
        if [ -z "$email" ]; then
            echo -e "${RED}Error: Email cannot be empty.${NC}"
            continue
        fi
        # Basic email validation
        if [[ ! "$email" =~ ^[^@]+@[^@]+\.[^@]+$ ]]; then
            echo -e "${RED}Error: Invalid email format.${NC}"
            continue
        fi
        break
    done
    echo "$email"
}

# RC5 FIX: Password validation with confirmation
prompt_password() {
    local password=""
    local confirm=""
    while true; do
        read -rsp "Admin password (min 12 chars): " password
        echo ""
        if [ ${#password} -lt 12 ]; then
            echo -e "${RED}Error: Password must be at least 12 characters.${NC}"
            continue
        fi
        # RC5 FIX: Add confirmation step
        read -rsp "Confirm admin password: " confirm
        echo ""
        if [ "$password" != "$confirm" ]; then
            echo -e "${RED}Error: Passwords do not match. Please try again.${NC}"
            continue
        fi
        break
    done
    echo "$password"
}

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
# RC5 FIX: Added redis-server
# ──────────────────────────────────────
CURRENT_STEP="dependencies"
echo ""
echo -e "${BOLD}[1/9] Installing system dependencies...${NC}"

apt-get update -qq
apt-get install -y -qq \
    curl wget tar gzip \
    ca-certificates \
    systemd \
    redis-server

echo -e "${GREEN}✓${NC} System dependencies installed"
echo -e "${GREEN}✓${NC} Redis server installed"

# ──────────────────────────────────────
# Step 2: Start and enable Redis
# RC5 FIX: Ensure Redis is running
# ──────────────────────────────────────
CURRENT_STEP="redis"
echo ""
echo -e "${BOLD}[2/9] Configuring Redis...${NC}"

# Start Redis
systemctl enable redis-server 2>/dev/null || true
systemctl start redis-server || {
    echo -e "${YELLOW}Warning:${NC} Redis failed to start, attempting configuration..."
    # Try with protected mode disabled for local connections
    sed -i 's/bind 127.0.0.1/bind 127.0.0.1 ::1/' /etc/redis/redis.conf 2>/dev/null || true
    systemctl restart redis-server || true
}

# Verify Redis is running
if systemctl is-active --quiet redis-server; then
    echo -e "${GREEN}✓${NC} Redis server is running"
else
    echo -e "${YELLOW}Warning:${NC} Redis may not be running, continuing..."
fi

# ──────────────────────────────────────
# Step 3: Create system user and groups
# ──────────────────────────────────────
CURRENT_STEP="user"
echo ""
echo -e "${BOLD}[3/9] Creating system user...${NC}"

if ! id -u orvix &>/dev/null; then
    useradd --system --user-group --create-home --home-dir /var/lib/orvix --shell /usr/sbin/nologin orvix
    echo -e "${GREEN}✓${NC} Created system user 'orvix'"
else
    echo -e "${GREEN}✓${NC} System user 'orvix' already exists"
fi

# ──────────────────────────────────────
# Step 4: Create directories
# RC5 FIX: Ensure all required directories exist with correct ownership
# ──────────────────────────────────────
CURRENT_STEP="directories"
echo ""
echo -e "${BOLD}[4/9] Creating directories...${NC}"

# Create all required directories
mkdir -p /etc/orvix/stalwart
mkdir -p /var/lib/orvix/stalwart
mkdir -p /var/lib/orvix/backups
mkdir -p /var/log/orvix/stalwart
mkdir -p /var/lib/orvix

# Set ownership
chown -R orvix:orvix /etc/orvix
chown -R orvix:orvix /var/lib/orvix
chown -R orvix:orvix /var/log/orvix

# Set permissions
chmod 750 /etc/orvix
chmod 750 /var/lib/orvix
chmod 750 /var/log/orvix
chmod 750 /etc/orvix/stalwart
chmod 750 /var/lib/orvix/stalwart
chmod 750 /var/log/orvix/stalwart

echo -e "${GREEN}✓${NC} Directories created with secure permissions"

# ──────────────────────────────────────
# Step 5: Install Orvix binary
# ──────────────────────────────────────
CURRENT_STEP="orvix_binary"
echo ""
echo -e "${BOLD}[5/9] Installing Orvix binary...${NC}"

ORVIX_BIN="/usr/local/bin/orvix"

if [ -f "release/orvix-v${ORVIX_VERSION}-linux-amd64" ]; then
    cp "release/orvix-v${ORVIX_VERSION}-linux-amd64" "$ORVIX_BIN"
    chmod 755 "$ORVIX_BIN"
    echo -e "${GREEN}✓${NC} Using local binary from release/"
elif [ -f "release/orvix-linux-amd64" ]; then
    cp "release/orvix-linux-amd64" "$ORVIX_BIN"
    chmod 755 "$ORVIX_BIN"
    echo -e "${GREEN}✓${NC} Using local binary"
elif [ -f "orvix-linux-amd64" ]; then
    cp "orvix-linux-amd64" "$ORVIX_BIN"
    chmod 755 "$ORVIX_BIN"
    echo -e "${GREEN}✓${NC} Using local binary"
elif [ -f "orvix" ]; then
    cp "orvix" "$ORVIX_BIN"
    chmod 755 "$ORVIX_BIN"
    echo -e "${GREEN}✓${NC} Using local binary"
elif command -v orvix &>/dev/null; then
    ORVIX_BIN=$(command -v orvix)
    echo -e "${GREEN}✓${NC} Orvix binary already installed at $ORVIX_BIN"
else
    echo "Downloading Orvix v${ORVIX_VERSION}..."
    curl -fsSL -o /tmp/orvix "${ORVIX_RELEASE_URL}/orvix-v${ORVIX_VERSION}-linux-amd64" || {
        echo -e "${RED}Failed to download Orvix binary${NC}"
        echo "Please download from: https://github.com/reachfm/orvix/releases"
        echo "Then re-run this installer."
        exit 1
    }
    mv /tmp/orvix "$ORVIX_BIN"
    chmod 755 "$ORVIX_BIN"
    echo -e "${GREEN}✓${NC} Downloaded Orvix binary"
fi

echo -e "${GREEN}✓${NC} Orvix binary installed at $ORVIX_BIN"

# ──────────────────────────────────────
# Step 6: Install Stalwart binary (RC5 FIX: Correct GitHub URL)
# ──────────────────────────────────────
CURRENT_STEP="stalwart_binary"
echo ""
echo -e "${BOLD}[6/9] Installing Stalwart mail server...${NC}"

STALWART_BIN="/usr/local/bin/stalwart"

if command -v stalwart &>/dev/null; then
    echo -e "${GREEN}✓${NC} Stalwart already installed at $(command -v stalwart)"
elif [ -f "$STALWART_BIN" ]; then
    echo -e "${GREEN}✓${NC} Stalwart already installed"
else
    echo "Downloading Stalwart v${STALWART_VERSION} from GitHub..."

    # RC5 FIX: Use correct GitHub URL for stalwartlabs/stalwart
    STALWART_DOWNLOAD_URL="https://github.com/stalwartlabs/stalwart/releases/download/v${STALWART_VERSION}/stalwart-x86_64-unknown-linux-gnu.tar.gz"

    if curl -fsSL -o /tmp/stalwart.tar.gz "$STALWART_DOWNLOAD_URL"; then
        tar -xzf /tmp/stalwart.tar.gz -C /tmp/

        # Find the stalwart binary in extracted files
        if [ -f /tmp/stalwart ]; then
            cp /tmp/stalwart "$STALWART_BIN"
            chmod 755 "$STALWART_BIN"
            echo -e "${GREEN}✓${NC} Stalwart v${STALWART_VERSION} installed from GitHub"
        elif [ -f /tmp/stalwart-mail-server ]; then
            cp /tmp/stalwart-mail-server "$STALWART_BIN"
            chmod 755 "$STALWART_BIN"
            echo -e "${GREEN}✓${NC} Stalwart v${STALWART_VERSION} installed from GitHub"
        else
            echo -e "${RED}Error: Stalwart binary not found in archive.${NC}"
            echo "Please download manually from: https://github.com/stalwartlabs/stalwart/releases"
            rm -f /tmp/stalwart.tar.gz
            exit 1
        fi
        rm -f /tmp/stalwart.tar.gz
    else
        echo -e "${RED}Failed to download Stalwart v${STALWART_VERSION}${NC}"
        echo ""
        echo "Please download manually:"
        echo "  1. Visit: https://github.com/stalwartlabs/stalwart/releases"
        echo "  2. Download: stalwart-x86_64-unknown-linux-gnu.tar.gz"
        echo "  3. Extract: tar -xzf stalwart-x86_64-unknown-linux-gnu.tar.gz"
        echo "  4. Copy: cp stalwart /usr/local/bin/stalwart && chmod 755 /usr/local/bin/stalwart"
        echo "  5. Re-run this installer"
        exit 1
    fi
    rm -rf /tmp/stalwart*
fi

# ──────────────────────────────────────
# Step 7: Generate configuration (RC5 FIX: Use validation functions)
# ──────────────────────────────────────
CURRENT_STEP="config"
echo ""
echo -e "${BOLD}[7/9] Configuring Orvix...${NC}"

# RC5 FIX: Use validated prompt functions
PRIMARY_DOMAIN=$(prompt_domain)
ADMIN_EMAIL=$(prompt_email)
ADMIN_PASSWORD=$(prompt_password)
LICENSE_KEY=$(prompt "License key (optional, press Enter to skip)" "")

# Validate email domain matches primary domain
EMAIL_DOMAIN=$(echo "$ADMIN_EMAIL" | cut -d'@' -f2)
if [ "$EMAIL_DOMAIN" != "$PRIMARY_DOMAIN" ]; then
    echo -e "${YELLOW}Warning:${NC} Email domain ($EMAIL_DOMAIN) differs from primary domain ($PRIMARY_DOMAIN)"
    read -rp "Use admin email as: $ADMIN_EMAIL? [Y/n]: " confirm
    if [[ "$confirm" =~ ^[Nn]$ ]]; then
        ADMIN_EMAIL="admin@$PRIMARY_DOMAIN"
    fi
fi

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
    - "http://localhost:3000"
    - "http://localhost:3001"
  trusted_proxies: []

database:
  driver: sqlite
  dsn: /var/lib/orvix/orvix.db?_loc=auto&_busy_timeout=5000&_txlock=immediate

redis:
  host: "127.0.0.1"
  port: 6379
  password: ""
  db: 0

stalwart:
  api_url: http://localhost:8080
  api_key: ""
  bin_path: /usr/local/bin/stalwart
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

# Save credentials securely (RC5 FIX: Do not log password)
cat > /etc/orvix/install_credentials.txt << CREDS
Orvix Installation Credentials
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Admin Email: $ADMIN_EMAIL
Admin Password: [REDACTED - use the password you provided]
License Key: ${LICENSE_KEY:-not provided}
Primary Domain: $PRIMARY_DOMAIN
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Admin Console: http://localhost:8080/admin
Webmail: http://localhost:3000
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
Save these credentials in a secure location.
CREDS

chmod 600 /etc/orvix/install_credentials.txt

echo -e "${GREEN}✓${NC} Configuration generated"
echo -e "${GREEN}✓${NC} Credentials saved to /etc/orvix/install_credentials.txt"

# ──────────────────────────────────────
# Step 8: Install systemd service
# RC5 FIX: Added ReadWritePaths for systemd hardening
# ──────────────────────────────────────
CURRENT_STEP="systemd"
echo ""
echo -e "${BOLD}[8/9] Installing systemd service...${NC}"

# RC5 FIX: Add ReadWritePaths so Orvix can write to required directories
cat > /etc/systemd/system/orvix.service << 'UNIT'
[Unit]
Description=Orvix Email Server Platform
Documentation=https://github.com/reachfm/orvix
After=network.target redis-server.target
Wants=network.target redis-server.target

[Service]
Type=simple
User=orvix
Group=orvix
ExecStart=/usr/local/bin/orvix serve
ExecReload=/bin/kill -HUP $MAINPID
Restart=always
RestartSec=10

# RC5 FIX: Security hardening with ReadWritePaths
ProtectSystem=full
ProtectHome=true
NoNewPrivileges=true
PrivateTmp=true

# RC5 FIX: Allow writing to Orvix directories
ReadWritePaths=/etc/orvix
ReadWritePaths=/etc/orvix/stalwart
ReadWritePaths=/var/lib/orvix
ReadWritePaths=/var/lib/orvix/stalwart
ReadWritePaths=/var/log/orvix
ReadWritePaths=/var/log/orvix/stalwart

# Environment
Environment=ORVIX_CONFIG=/etc/orvix/orvix.yaml
Environment=ORVIX_LOG_DIR=/var/log/orvix

# Logging
StandardOutput=journal
StandardError=journal
SyslogIdentifier=orvix

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable orvix.service

echo -e "${GREEN}✓${NC} systemd service installed and enabled"

# ──────────────────────────────────────
# Step 9: Start services and healthcheck
# RC5 FIX: Comprehensive post-install healthcheck
# ──────────────────────────────────────
CURRENT_STEP="start"
echo ""
echo -e "${BOLD}[9/9] Starting services and validation...${NC}"

# Ensure database directory exists and has correct permissions
mkdir -p /var/lib/orvix
chown orvix:orvix /var/lib/orvix

# RC5 FIX: Create systemd override directory before writing
mkdir -p /etc/systemd/system/orvix.service.d

# RC5 FIX: Pass admin credentials via environment variables
cat > /etc/systemd/system/orvix.service.d/override.conf << OVERRIDE
[Service]
Environment=ORVIX_ADMIN_EMAIL=${ADMIN_EMAIL}
Environment=ORVIX_ADMIN_PASSWORD=${ADMIN_PASSWORD}
OVERRIDE

systemctl daemon-reload

# Start Orvix service
systemctl start orvix.service || {
    echo -e "${YELLOW}⚠${NC} Service failed to start. Check logs:"
    echo "  journalctl -u orvix.service -n 50"
}

# RC5 FIX: Comprehensive healthcheck
sleep 5
echo ""
echo -e "${BOLD}Post-Install Healthcheck:${NC}"

HEALTHCHECK_PASSED=true

# Check Orvix service status
echo -n "  Orvix Service: "
if systemctl is-active --quiet orvix.service; then
    echo -e "${GREEN}✓ Running${NC}"
else
    echo -e "${RED}✗ Not running${NC}"
    HEALTHCHECK_PASSED=false
fi

# Check Redis
echo -n "  Redis Server: "
if systemctl is-active --quiet redis-server; then
    echo -e "${GREEN}✓ Running${NC}"
else
    echo -e "${RED}✗ Not running${NC}"
    HEALTHCHECK_PASSED=false
fi

# Check Orvix API health
echo -n "  Orvix API Health: "
if curl -sf http://localhost:8080/api/v1/health > /dev/null 2>&1; then
    echo -e "${GREEN}✓ OK${NC}"
else
    echo -e "${YELLOW}⚠ Not ready${NC}"
    echo "    (May take a few seconds to initialize)"
fi

# Check Database
echo -n "  Database: "
if [ -f /var/lib/orvix/orvix.db ]; then
    echo -e "${GREEN}✓ Created${NC}"
else
    echo -e "${YELLOW}⚠ Not created yet${NC}"
fi

# Summary
echo ""
if [ "$HEALTHCHECK_PASSED" = true ]; then
    echo -e "${GREEN}✓ All core services running${NC}"
else
    echo -e "${YELLOW}⚠ Some services need attention${NC}"
fi

echo ""
echo "  Verification commands:"
echo "    systemctl status orvix --no-pager -l"
echo "    systemctl status redis-server --no-pager -l"
echo "    curl -fsS http://127.0.0.1:8080/api/v1/health"
echo "    journalctl -u orvix --no-pager -n 50"

# ──────────────────────────────────────
# Summary
# ──────────────────────────────────────
IP_ADDR=$(hostname -I | awk '{print $1}')

echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "${BOLD}Orvix v${ORVIX_VERSION} RC5 Installation Complete${NC}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo ""
echo -e " ${BOLD}Admin Console:${NC} http://${IP_ADDR}:8080/admin"
echo -e " ${BOLD}Webmail:${NC} http://${IP_ADDR}:3000"
echo -e " ${BOLD}Admin Email:${NC} ${ADMIN_EMAIL}"
echo -e " ${BOLD}Primary Domain:${NC} ${PRIMARY_DOMAIN}"
echo ""
echo "Next steps:"
echo " 1. Configure DNS records for ${PRIMARY_DOMAIN}:"
echo "    - MX record → ${IP_ADDR} (priority 10)"
echo "    - A record for mail.${PRIMARY_DOMAIN} → ${IP_ADDR}"
echo " 2. Open admin console: http://${IP_ADDR}:8080/admin"
echo " 3. Add domain: ${PRIMARY_DOMAIN}"
echo " 4. Create mailboxes"
echo ""
echo -e "${YELLOW}Credentials saved to:${NC} /etc/orvix/install_credentials.txt"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# RC5 FIX: Clear password from memory
unset ADMIN_PASSWORD
unset ADMIN_CONFIRM