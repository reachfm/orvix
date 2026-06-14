# Orvix RC4 Release Notes

**Version:** 1.0.3-rc4
**Date:** 2026-06-06
**Type:** Release Candidate 4 (RC4)

## RC3 → RC4 Critical Fixes

### A. Stalwart Download URL Fixed
- **ISSUE**: Stalwart download returned 404 error
- **ROOT CAUSE**: Wrong GitHub repo URL and old version (v0.10.5)
- **FIX**:
  - Updated to v0.16.7 (latest stable)
  - Correct URL: `https://github.com/stalwartlabs/stalwart/releases/download/v0.16.7/stalwart-x86_64-unknown-linux-gnu.tar.gz`
  - Installer now fails with clear message if download fails

### B. Systemd Override Directory Fixed
- **ISSUE**: `install.sh: line 458: /etc/systemd/system/orvix.service.d/override.conf: No such file or directory`
- **ROOT CAUSE**: Directory `/etc/systemd/system/orvix.service.d/` not created before writing
- **FIX**: Added `mkdir -p /etc/systemd/system/orvix.service.d` before writing override.conf

### C. Password Prompt Fixed
- **ISSUE**: Password prompt repeated strangely (Admin password: Admin password: Admin password:)
- **ROOT CAUSE**: Loop in prompt function with improper echo
- **FIX**:
  - Cleaned up password validation loop
  - Added password confirmation step
  - Rejects mismatched passwords
  - Never echoes password in logs

## Installation

### Fresh Install
```bash
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.3/install.sh | bash
```

Or download and run manually:
```bash
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.3/orvix-v1.0.3-linux-amd64.tar.gz -o orvix.tar.gz
tar -xzf orvix.tar.gz
sudo ./install.sh
```

The installer will prompt for:
1. Primary email domain (e.g., mail.example.com)
2. Admin email address
3. Admin password (minimum 8 characters)
4. Confirm admin password

### Upgrade from RC3
```bash
sudo systemctl stop orvix
sudo tar -xzf orvix-v1.0.3-linux-amd64.tar.gz -C /tmp
sudo cp /tmp/orvix-v1.0.3-linux-amd64 /usr/local/bin/orvix
sudo systemctl start orvix
```

## Known Limitations

### Stalwart Mail Server
- Stalwart binary downloads from GitHub releases
- Full mail flow (SMTP send/receive) requires additional Stalwart configuration
- Web UI for Stalwart available at port 8080 after bootstrap setup

## Checksums

```
orvix-v1.0.3-linux-amd64: 1cc564f2183ee9ad4d07e3fa4515eb2e22e8caecdfb8a6215fb817f78b7287f5
orvix-v1.0.3-linux-amd64.tar.gz: aed4f97924b3e9315afbe9185600e6d3b8a3cecdff8698314090e768499099bb
```

## Commit Information

- **Git Commit:** (to be determined on push)
- **Source Repository:** https://github.com/reachfm/orvix
- **Build Machine:** CGO_ENABLED=0, Pure Go binary

---

**Previous Version:** RC3 (1.0.2)
**Next Version:** Stable release planned after RC4 validation