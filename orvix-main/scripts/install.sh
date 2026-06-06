#!/usr/bin/env bash
set -euo pipefail

echo "==> Orvix Email Server Installer"
echo "==> Installing to /usr/local/bin/orvix"

BINARY_URL="${1:-https://releases.orvix.email/latest/orvix-linux-amd64}"
VERSION="${2:-latest}"

if [ "$(id -u)" -ne 0 ]; then
    echo "This installer must be run as root (or with sudo)."
    exit 1
fi

echo "==> Downloading Orvix ${VERSION}..."
if command -v curl &>/dev/null; then
    curl -sL -o /tmp/orvix "${BINARY_URL}"
elif command -v wget &>/dev/null; then
    wget -qO /tmp/orvix "${BINARY_URL}"
else
    echo "Need curl or wget to download."
    exit 1
fi

chmod +x /tmp/orvix
mv /tmp/orvix /usr/local/bin/orvix

echo "==> Creating directories..."
mkdir -p /etc/orvix /var/lib/orvix /var/log/orvix /etc/orvix/stalwart

echo "==> Setting up config..."
if [ ! -f /etc/orvix/orvix.yaml ]; then
    /usr/local/bin/orvix --dump-config > /etc/orvix/orvix.yaml
    echo "Config written to /etc/orvix/orvix.yaml"
fi

echo "==> Creating systemd service..."
cat > /etc/systemd/system/orvix.service << 'SERVICE'
[Unit]
Description=Orvix Email Server Platform
After=network.target postgresql.service redis.service
Wants=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/orvix
Restart=always
RestartSec=10
User=root
Group=root
Environment=ORVIX_CONFIG=/etc/orvix/orvix.yaml
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE

systemctl daemon-reload
systemctl enable orvix

echo "==> Installation complete!"
echo ""
echo "Next steps:"
echo "  1. Edit /etc/orvix/orvix.yaml with your settings"
echo "  2. Place your Stalwart binary at /usr/local/bin/stalwart"
echo "  3. Start Orvix: systemctl start orvix"
echo "  4. Check status: systemctl status orvix"
