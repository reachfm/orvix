# Orvix RC5 Release Notes

**Version:** 1.0.4-rc5
**Date:** 2026-06-07
**Type:** Release Candidate 5 (RC5)

## RC4 → RC5 Critical Fixes

### A. Systemd Hardening Fixed
- **ISSUE**: `open /etc/orvix/stalwart/stalwart.yaml: read-only file system`
- **ROOT CAUSE**: `ProtectSystem=full` blocks writes to `/etc/orvix`, `/var/lib/orvix`, `/var/log/orvix`
- **FIX**:
  - Added `ReadWritePaths` directives to orvix.service:
    ```
    ReadWritePaths=/etc/orvix
    ReadWritePaths=/etc/orvix/stalwart
    ReadWritePaths=/var/lib/orvix
    ReadWritePaths=/var/lib/orvix/stalwart
    ReadWritePaths=/var/log/orvix
    ReadWritePaths=/var/log/orvix/stalwart
    ```

### B. Stalwart v0.16.7 Startup Fixed
- **ISSUE**: `Missing value for argument 'data', try '--help'`
- **ROOT CAUSE**: Stalwart v0.16+ uses JSON config.json (not YAML), no `--data` argument
- **FIX**:
  - Config generator now writes `/etc/orvix/stalwart/config.json` (JSON format)
  - Process manager uses `--config /etc/orvix/stalwart/config.json` only
  - Data directory specified INSIDE config.json, not as command line arg
  - Sample config.json:
    ```json
    {
      "storage": {"@type": "RocksDb", "path": "/var/lib/orvix/stalwart"},
      "server": {"hostname": "localhost"},
      "tracing": {"level": "info"}
    }
    ```

### C. Redis Installation Added
- **ISSUE**: `dial tcp 127.0.0.1:6379: connect: connection refused`
- **ROOT CAUSE**: Redis not installed or running
- **FIX**:
  - Added `redis-server` to apt-get install
  - Installer enables and starts redis-server
  - Orvix service now has `After=redis-server.target` dependency

### D. Post-Install Healthcheck Added
- **ADDITION**: Comprehensive post-install validation
  - Orvix Service status check
  - Redis Server status check
  - Orvix API health endpoint check
  - Database file existence check
  - Clear status indicators (✓/✗/⚠)

## Installation

### Fresh Install
```bash
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.4/install.sh | bash
```

Or download and run manually:
```bash
curl -fsSL https://github.com/reachfm/orvix/releases/download/v1.0.4/orvix-v1.0.4-linux-amd64.tar.gz -o orvix.tar.gz
tar -xzf orvix.tar.gz
sudo ./install.sh
```

### Upgrade from RC4
```bash
sudo systemctl stop orvix
sudo tar -xzf orvix-v1.0.4-linux-amd64.tar.gz -C /tmp
sudo cp /tmp/orvix-v1.0.4-linux-amd64 /usr/local/bin/orvix
sudo systemctl daemon-reload
sudo systemctl start orvix
```

## Checksums

```
orvix-v1.0.4-linux-amd64: e7ad824523dea77858b11dfcc06793bb868a1141bf1f95dd9f511b4317b1138b
orvix-v1.0.4-linux-amd64.tar.gz: 48be25d12c7d9eb257680088f2d74bb6aa24250b7ac6aee5b9b305f11bd3f955
```

## Commit Information

- **Source Repository:** https://github.com/reachfm/orvix
- **Build Machine:** CGO_ENABLED=0, Pure Go binary

---

**Previous Version:** RC4 (1.0.3)
**Next Version:** Stable release planned after RC5 validation