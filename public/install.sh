#!/bin/bash
# Orvix Enterprise Mail — Installer
# curl -sSL https://get.orvix.email | bash
# Domain: orvix.email | Product: OrvixEM

set -euo pipefail

ORVIX_VERSION="${ORVIX_VERSION:-latest}"
ORVIX_CHANNEL="${ORVIX_CHANNEL:-stable}"
ORVIX_INSTALL_DIR="${ORVIX_INSTALL_DIR:-/usr/local/bin}"
ORVIX_CONFIG_DIR="${ORVIX_CONFIG_DIR:-/etc/orvix}"
ORVIX_DATA_DIR="${ORVIX_DATA_DIR:-/var/lib/orvix}"
ORVIX_UPDATE_SERVER="${ORVIX_UPDATE_SERVER:-https://updates.orvix.email}"

echo "==> Installing Orvix Enterprise Mail (OrvixEM)"
echo "    Version: $ORVIX_VERSION"
echo "    Channel: $ORVIX_CHANNEL"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
    linux)   ;;
    darwin)  echo "macOS not yet supported"; exit 1 ;;
    *)       echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Check for existing installation
if command -v orvix &>/dev/null; then
    echo "    Existing installation detected: $(orvix version 2>/dev/null || echo 'unknown')"
fi

# Install dependencies check
echo "    Checking system dependencies..."
for cmd in curl tar; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "    Missing dependency: $cmd"
        exit 1
    fi
done

# Create directories
sudo mkdir -p "$ORVIX_CONFIG_DIR" "$ORVIX_DATA_DIR"/{rollback,snapshots,data} /var/log/orvix

# Download OrvixEM binary
DOWNLOAD_URL="${ORVIX_UPDATE_SERVER}/download/${ORVIX_VERSION}/orvix-${OS}-${ARCH}"
echo "    Downloading from: $DOWNLOAD_URL"

if ! curl -sSLf "$DOWNLOAD_URL" -o /tmp/orvix; then
    echo "    Download failed. Building from source instead..."
    if command -v go &>/dev/null; then
        go build -o /tmp/orvix ./cmd/orvix
    else
        echo "    Go not installed. Please install Go 1.23+ or download the binary manually."
        exit 1
    fi
fi

# Install binary
sudo mv /tmp/orvix "$ORVIX_INSTALL_DIR/orvix"
sudo chmod +x "$ORVIX_INSTALL_DIR/orvix"

# Install default config if not present
if [ ! -f "$ORVIX_CONFIG_DIR/orvix.yaml" ]; then
    if [ -f "./configs/orvix.yaml" ]; then
        sudo cp ./configs/orvix.yaml "$ORVIX_CONFIG_DIR/orvix.yaml"
    else
        echo "    No default config found. Generate one at $ORVIX_CONFIG_DIR/orvix.yaml"
    fi
fi

# Install systemd service
if command -v systemctl &>/dev/null; then
    echo "    Installing systemd service..."
    cat <<'SERVICE' | sudo tee /etc/systemd/system/orvix.service >/dev/null
[Unit]
Description=Orvix Enterprise Mail (OrvixEM)
Documentation=https://docs.orvix.email
After=network.target postgresql.service redis.service stalwart.service
Wants=network.target

[Service]
Type=simple
User=orvix
Group=orvix
ExecStart=/usr/local/bin/orvix start
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=10
WorkingDirectory=/var/lib/orvix
Environment=ORVIX_CONFIG_DIR=/etc/orvix
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE

    sudo systemctl daemon-reload
    echo "    Service installed. Run: sudo systemctl enable --now orvix"
fi

echo ""
echo "==> Installation complete!"
echo "    Binary: $(command -v orvix)"
echo "    Config: $ORVIX_CONFIG_DIR/orvix.yaml"
echo "    Data:   $ORVIX_DATA_DIR"
echo ""
echo "    To start: sudo systemctl start orvix"
echo "    Or:       orvix start"
echo "    Status:   orvix status"
echo ""
echo "    Documentation: https://docs.orvix.email"
echo "    License:       https://license.orvix.email"
