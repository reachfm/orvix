# Orvix RC3 Release Notes

**Version:** 1.0.2-rc3
**Date:** 2026-06-06
**Type:** Release Candidate 3 (RC3)

## What's New in RC3

### Security Fixes
- **CRITICAL**: Removed hardcoded default credentials (admin@orvix.local / admin123)
- Admin credentials now MUST be provided via environment variables:
  - `ORVIX_ADMIN_EMAIL`
  - `ORVIX_ADMIN_PASSWORD`
- Installer now prompts for admin email and password during installation

### Core Fixes (VPS-Verified)

1. **Pure Go SQLite Driver**
   - Migrated from CGO-based sqlite3 to `modernc.org/sqlite`
   - No CGO compilation required
   - Builds successfully with `CGO_ENABLED=0`

2. **SQLite-Safe Query Pattern**
   - Fixed Login handler to use `database/sql` directly instead of GORM's First()
   - Fixed /me endpoint to use direct SQL queries
   - All database queries now work correctly with SQLite

3. **Password Hashing**
   - Uses bcrypt with golang.org/x/crypto
   - VerifyPassword properly compares bcrypt hashes

4. **Redis Rate Limiting**
   - Installs and configures redis-server
   - Safe fallback when Redis unavailable

5. **Systemd Service**
   - Removed invalid StartLimitIntervalSec directive
   - Runs as orvix user with security hardening

### Installer Improvements
- Prompts for primary domain (with validation)
- Prompts for admin email (with validation)
- Prompts for admin password (minimum 12 characters)
- Passes credentials via systemd environment override
- Health check verification after startup

## RC2 → RC3 Changes

### Files Modified
- `cmd/orvix/main.go` - Removed hardcoded credentials, uses env vars
- `internal/api/handlers/handlers.go` - Fixed SQLite-safe queries
- `internal/config/database.go` - Pure Go SQLite driver
- `internal/config/sqlite_dialect.go` - Custom GORM dialector
- `release/install.sh` - Added credential prompts and env var passing

### VPS Validation
This release was validated on VPS 65.75.203.74 where:
- Login endpoint works with bcrypt verification
- JWT RS256 token generation works
- /me endpoint returns user profile
- Health endpoint returns OK
- Service restart preserves data

## Installation

### Fresh Install
```bash
curl -fsSL https://releases.orvix.email/install.sh | bash
```

Or download and run manually:
```bash
tar -xzf orvix-v1.0.2-linux-amd64.tar.gz
sudo ./install.sh
```

The installer will prompt for:
1. Primary email domain (e.g., mail.example.com)
2. Admin email address
3. Admin password (minimum 12 characters)

### Upgrade from RC2
```bash
sudo systemctl stop orvix
sudo tar -xzf orvix-v1.0.2-linux-amd64.tar.gz -C /tmp
sudo cp /tmp/orvix-v1.0.2-linux-amd64 /usr/local/bin/orvix
sudo systemctl start orvix
```

## Known Limitations

### Stalwart Mail Server
- Stalwart binary downloads and runs in bootstrap mode
- Full mail flow (SMTP send/receive) requires additional Stalwart configuration
- Web UI for Stalwart available at port 8080 after bootstrap setup

## Checksums

```
orvix-v1.0.2-linux-amd64: d348b1050322da89a61544f3023cf29ee2b462a24e4f8ab6a278182cca3814ee
orvix-v1.0.2-linux-amd64.tar.gz: 7a00f2fb67b86e741887fe836d0f20523618536df4473f1af0e1509b3261b4c1
```

## Commit Information

- **Git Commit:** 1849e4e
- **Source Repository:** https://github.com/reachfm/orvix
- **Build Machine:** CGO_ENABLED=0, Pure Go binary

---

**Previous Version:** RC2 (1.0.1)
**Next Version:** Stable release planned after RC3 validation