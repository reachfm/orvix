# Orvix RC6 Release Notes

**Version:** 1.0.5-rc6
**Date:** 2026-06-07
**Type:** Release Candidate 6 (RC6)

## RC5 → RC6 Critical Fixes

### A. Systemd Override Syntax Fixed
- **ISSUE**: `/etc/systemd/system/orvix.service.d/override.conf:5: Missing '='`
- **ROOT CAUSE**: Unquoted heredoc caused variable expansion during file creation, corrupting systemd syntax
- **FIX**:
  - Changed from unquoted heredoc `<< OVERRIDE` to printf-based file creation
  - Escapes special characters in environment variables
  - Generates valid systemd override.conf with proper syntax:
    ```
    [Service]
    Environment="ORVIX_ADMIN_EMAIL=admin@example.com"
    Environment="ORVIX_ADMIN_PASSWORD=..."
    ```

### B. Domain-First Installer Implemented
- **ADDITION**: New prompts for full domain configuration:
  - Primary Domain (e.g., orvix.email)
  - Mail Hostname (e.g., mail.orvix.email)
  - Admin Hostname (e.g., admin.orvix.email)
  - Webmail Hostname (e.g., webmail.orvix.email)
- **FIX**: Success output now shows domains instead of IPs:
  - Admin Console: https://admin.orvix.email
  - Webmail: https://webmail.orvix.email
- **FIX**: Generated config now uses domains for all URLs, CORS origins, and cookie domains

### C. Password Policy Enhanced
- **CHANGE**: Minimum password length reduced from 12 to 8 characters
- **ADDITION**: Weak password rejection:
  - Rejects common patterns: 12345678, password, password123, admin123, admin1234, qwerty123, etc.
  - Shows error: "Password is too weak. Please choose a stronger password."

### D. Stalwart Health Verification Enhanced
- **ADDITION**: Post-install validation now verifies:
  - Stalwart process exists with correct command line
  - Config file exists at /etc/orvix/stalwart/config.json
  - Shows exact command used (e.g., `stalwart --config /etc/orvix/stalwart/config.json`)
  - Verifies PID and process details

## Preserved RC5 Fixes

1. **Systemd Hardening**: ReadWritePaths for /etc/orvix, /var/lib/orvix, /var/log/orvix
2. **Stalwart v0.16.7**: JSON config.json format, correct --config argument
3. **Redis**: Installation, enable, and startup verification
4. **Healthcheck**: Comprehensive post-install validation with status indicators

## Installation

### Fresh Install
```bash
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.5/install.sh | bash
```

Or download and run manually:
```bash
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.5/orvix-v1.0.5-linux-amd64.tar.gz -o orvix.tar.gz
tar -xzf orvix.tar.gz
sudo ./install.sh
```

### Upgrade from RC5
```bash
sudo systemctl stop orvix
sudo tar -xzf orvix-v1.0.5-linux-amd64.tar.gz -C /tmp
sudo cp /tmp/orvix-v1.0.5-linux-amd64 /usr/local/bin/orvix
sudo systemctl daemon-reload
sudo systemctl start orvix
```

## Checksums

```
orvix-v1.0.5-linux-amd64: 8d5d728188ab724f5e878b43f812071b15fa969e0f0faa8b83ab12effe447556
orvix-v1.0.5-linux-amd64.tar.gz: f93d159fb27631220ef03814da910d649e72edb6b60623d344fbf192d0d80d48
```

## Installer Prompts

1. **Primary Domain**: orvix.email (default)
2. **Mail Hostname**: mail.orvix.email (default)
3. **Admin Hostname**: admin.orvix.email (default)
4. **Webmail Hostname**: webmail.orvix.email (default)
5. **Admin Email**: admin@orvix.email (default)
6. **Admin Password**: minimum 8 characters, rejects weak passwords

## Commit Information

- **Source Repository:** https://github.com/reachfm/orvix
- **Build Machine:** CGO_ENABLED=0, Pure Go binary

---

**Previous Version:** RC5 (1.0.4)
**Next Version:** Stable release planned after RC6 validation