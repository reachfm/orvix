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

### Production one-command install (recommended)

The supported path on a fresh Ubuntu VPS is to fetch the
**release bundle**, which ships a verified `bin/orvix` plus
the complete runtime tree (admin SPA, webmail SPA, systemd
units, sudoers drop-in, scripts, configs). The public
installer `release/install-public.sh` fetches the bundle,
verifies its layout, then delegates to the bundled
`install.sh`. Everything below maps to one curl invocation.

```bash
# Default: latest stable
curl -fsSL https://releases.orvix.email/install-public.sh \
    | sudo ORVIX_NON_INTERACTIVE=1 \
         ORVIX_DOMAIN=mail.example.com \
         ORVIX_PUBLIC_IPV4=<your.public.ipv4> \
         bash -s --

# Pinned to a specific version
curl -fsSL https://releases.orvix.email/install-public.sh \
    | sudo ORVIX_VERSION=1.0.3-rc5 \
         ORVIX_COMMIT=53ecf240000... \
         ORVIX_BUNDLE_SHA256=<expected sha256> \
         ORVIX_NON_INTERACTIVE=1 \
         ORVIX_DOMAIN=mail.example.com \
         ORVIX_PUBLIC_IPV4=<your.public.ipv4> \
         bash -s --

# Air-gapped: point the installer at an internally-hosted bundle
curl -fsSL https://releases.orvix.email/install-public.sh \
    | sudo ORVIX_BUNDLE_URL=https://internal.example.com/orvix-1.0.3-rc5-linux-amd64.tar.gz \
         ORVIX_BUNDLE_SHA256=<expected sha256> \
         ORVIX_NON_INTERACTIVE=1 \
         ORVIX_DOMAIN=mail.example.com \
         ORVIX_PUBLIC_IPV4=<your.public.ipv4> \
         bash -s --
```

The bundle pathway is fail-closed: if `install-public.sh`
cannot reach the bundle, the bundle is missing required files,
or the bundled binary's embedded commit does not match
`ORVIX_COMMIT`, the installer aborts before mutating any
state on the host.

### Manual install (download then run)

If you prefer to download and run by hand:

```bash
# 1) Pull the bundle the public installer would otherwise fetch.
curl -fsSL https://releases.orvix.email/orvix-enterprise-mail-1.0.3-rc5-linux-amd64.tar.gz -o orvix.tar.gz
curl -fsSL https://releases.orvix.email/orvix-enterprise-mail-1.0.3-rc5-linux-amd64.tar.gz.sha256 -o orvix.tar.gz.sha256
sha256sum -c orvix.tar.gz.sha256

# 2) Extract and run the bundled installer (NOT a developer worktree).
tar -xzf orvix.tar.gz
cd orvix
sudo bash release/install.sh
```

The installer will prompt for:
1. Primary email domain (e.g., mail.example.com)
2. Admin email address
3. Admin password (minimum 8 characters)
4. Confirm admin password

### What ships in the bundle

| Path | Purpose |
|---|---|
| `bin/orvix`              | The verified binary, built from the bundle's pinned commit and embedded Version/Commit/Channel |
| `release/install.sh`     | The bundled installer — handles dependencies, systemd, sudoers, config, smoke tests |
| `release/upgrade.sh`     | Operator-driven upgrade path |
| `release/uninstall.sh`   | Operator-driven uninstall |
| `release/install-public.sh` | The public installer entrypoint (re-run after future releases) |
| `release/systemd/`       | `orvix.service` + `orvix-update.service` |
| `release/sudoers.d/`     | `orvix-update` (systemctl start only, never auto-enabled) |
| `release/scripts/`       | All operator scripts (diagnostics, doctor, vapid, https setup, smoke tests, asset lib) |
| `release/admin/**`       | The admin SPA (must match the binary version) |
| `release/webmail/**`     | The webmail SPA (must match the binary version) |
| `VERSION`                | Single source of truth for the version string |
| `BUILDINFO`              | version / commit / build_time / channel — read by install.sh for the stale-binary guard |
| `checksums.txt`          | sha256 of every file in the bundle |

### Bundling a release yourself

```bash
bash release/scripts/build-release-bundle.sh
# → dist/orvix-enterprise-mail-<version>-linux-amd64.tar.gz
# → dist/orvix-enterprise-mail-<version>-linux-amd64.tar.gz.sha256
```

The bundle script builds the current `HEAD`'s `bin/orvix`
with `-ldflags` injecting `internal/buildinfo.Version`,
`Commit`, `BuildTime`, and `Channel`, then re-runs
`orvix version --full` on the just-built binary to prove
the embedded metadata matches the bundle metadata before
sealing the tarball.

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
