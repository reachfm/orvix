# Stalwart Integration Guide

## Overview

OrvixEM integrates with **Stalwart Mail Server** as the underlying mail protocol engine. Stalwart handles SMTP, IMAP, POP3, JMAP, mail storage, and queue operations while OrvixEM provides the enterprise management layer.

**Architecture:** Stalwart Core Engine + Orvix Enterprise Layer

OrvixEM does **not** implement SMTP, IMAP, POP3, or JMAP. These are provided by Stalwart.

---

## Installation

### 1. Install Stalwart

```bash
# Option 1: Official installer
curl -sSL https://stalw.art/install | bash

# Option 2: Docker
docker run -d --name stalwart \
  -p 25:25 -p 587:587 -p 465:465 \
  -p 143:143 -p 993:993 \
  -v stalwart_data:/var/lib/stalwart \
  ghcr.io/stalwartlabs/stalwart:v0.16
```

### 2. Configure OrvixEM to find Stalwart

Edit `orvix.yaml`:

```yaml
stalwart:
  binary_path: "/usr/local/bin/stalwart"    # Path to Stalwart binary
  config_path: "/etc/stalwart/config.yaml"   # Stalwart 0.16 datastore bootstrap JSON
  smtp_ports: [25, 587, 465]
  imap_ports: [143, 993]
  pop3_ports: [110, 995]
  jmap_ports: [80, 443]
```

Or set the environment variable:

```bash
export ORVIX_STALWART_BINARY=/usr/local/bin/stalwart
```

### 3. Verify Integration

```bash
# Check if Stalwart is detected
orvix stalwart status

# Get binary path
orvix stalwart path

# Apply and verify the complete Stalwart 0.16.7 integration
sudo orvix stalwart apply

# Validate Stalwart 0.16 bootstrap JSON
orvix stalwart validate

# Start/stop/restart
orvix stalwart start
orvix stalwart stop
orvix stalwart restart
```

---

## Binary Detection Order

OrvixEM searches for the Stalwart binary in this order:

1. `stalwart.binary_path` from `orvix.yaml`
2. `ORVIX_STALWART_BINARY` environment variable
3. Common install paths:
   - `/usr/local/bin/stalwart`
   - `/usr/bin/stalwart`
   - `/opt/stalwart/bin/stalwart`
   - `/opt/homebrew/bin/stalwart`
   - `/home/stalwart/bin/stalwart`
4. `which stalwart` (PATH lookup on Linux/macOS)

---

## Docker Compose

The `docker-compose.yml` includes a Stalwart service profile:

```bash
# Start with Stalwart
docker-compose --profile stalwart up -d
```

This runs Stalwart in a container alongside OrvixEM.

---

## Provisioning

When Stalwart is detected, OrvixEM automatically provisions:

- **Domains**: Created in Stalwart via CLI when added through OrvixEM admin
- **Mailboxes**: Created in Stalwart when users are added
- **Aliases**: Created in Stalwart when email aliases are configured

If Stalwart is not available, provisioning jobs are queued and will execute when Stalwart becomes available.

---

## Health Checks

The OrvixEM health endpoint (`/healthz`) checks:

- `stalwart`: Whether Stalwart binary is detected
- Individual service ports: SMTP, IMAP, POP3, JMAP
- Config file presence

---

## Troubleshooting

### "Stalwart binary not found"

```bash
# Verify binary exists
ls -la /usr/local/bin/stalwart

# Check path configuration
orvix stalwart path

# Set binary path explicitly
export ORVIX_STALWART_BINARY=/path/to/stalwart
```

### "Config validation failed"

```bash
# Stalwart 0.16 --config must point at datastore JSON only.
cat /etc/stalwart/config.yaml

# Generate and apply fresh bootstrap/listener settings
sudo orvix stalwart apply
```

### Stalwart 0.16 fixed management listener

Stalwart 0.16 reads only a small datastore bootstrap file from `--config`.
Listener configuration lives in the datastore. Orvix applies the required
listener update through Stalwart's JMAP management API.

Systemd must start Stalwart with the same file Orvix writes:

```bash
/usr/local/bin/stalwart --config /etc/stalwart/config.yaml
```

The bootstrap JSON must look like:

```json
{"@type":"RocksDb","path":"/var/lib/stalwart/"}
```

To provision or repair a server non-interactively:

```bash
sudo orvix stalwart apply
curl http://127.0.0.1:8081/status
```

`orvix stalwart apply` writes the bootstrap JSON, installs the systemd override,
restarts Stalwart, enters recovery mode with a generated temporary credential,
patches the `http` `NetworkListener` through the JMAP management API, clears
recovery mode, restarts again, and verifies that 8081 survives a restart.

### "Failed to start/stop/restart"

OrvixEM tries systemd first (`systemctl start stalwart-server`), then falls back to direct binary calls (`stalwart --daemon`).

```bash
# Check systemd service
systemctl status stalwart-server

# Start manually
sudo stalwart --daemon
```

---

## Stalwart Version Compatibility

| OrvixEM Version | Stalwart Version |
|---|---|
| 0.1.4-preview+ | 0.16.7 |

---

## External Mode

OrvixEM can operate without a local Stalwart installation by using an external Stalwart server:

```yaml
stalwart:
  external: true
  host: "remote-stalwart.example.com"
  admin_port: 8081
  smtp_ports: [25, 587]
  imap_ports: [143, 993]
```

In external mode, provisioning commands are sent via SSH or the Stalwart management API.
