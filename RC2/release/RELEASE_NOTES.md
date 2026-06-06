# Orvix v1.0.1 (RC2) Release Notes

## Release Date: 2026-06-06

---

## RC2 - Critical Fixes

This release fixes critical RC1 deployment failures discovered on clean Ubuntu VPS.

### 🔴 Critical Fixes

#### 1. SQLite CGO Error (FIXED)
**Problem:**
```
failed to connect to database:
Binary was compiled with 'CGO_ENABLED=0',
go-sqlite3 requires cgo to work.
```

**Solution:** Replaced `mattn/go-sqlite3` with `modernc.org/sqlite` (pure Go, no CGO required)

**Files Changed:**
- `go.mod` - Added `modernc.org/sqlite v1.35.0`
- `internal/config/database.go` - Rewrote database initialization

---

#### 2. Stalwart Download 404 (FIXED)
**Problem:**
```
Downloading Stalwart v0.10.0…
404 Not Found
```

**Solution:** Updated download URLs to use `stalw.art/api/download` with GitHub fallback

**Files Changed:**
- `release/install.sh` - Fixed Stalwart download logic

---

#### 3. systemd Warning (FIXED)
**Problem:**
```
Unknown key name 'StartLimitIntervalSec'
```

**Solution:** Removed invalid directive from service file

**Files Changed:**
- `release/systemd/orvix.service` - Cleaned up directives

---

#### 4. Installer Prompts (FIXED)
**Problem:**
- No input validation
- Domain/email confusion
- Invalid passwords accepted

**Solution:** Added strict validation loops with clear error messages

**Files Changed:**
- `release/install.sh` - Complete rewrite of prompt handling

---

## Installation

### Clean VPS Install

```bash
# Download RC2
wget https://github.com/reachfm/orvix/releases/v1.0.1/orvix-v1.0.1-linux-amd64.tar.gz
tar -xzf orvix-v1.0.1-linux-amd64.tar.gz

# Run installer (as root)
cd release
bash install.sh

# Follow prompts with validation
# - Domain: example.com
# - Email: admin@example.com
# - Password: (min 12 characters)
```

### Upgrade from RC1

```bash
cd /path/to/new/release
bash upgrade.sh
```

---

## Known Issues

- Stalwart binary must be downloaded during installation
- PostgreSQL recommended for production with high traffic
- Redis optional (falls back to in-memory)

---

## Next Release (v1.0.2 - Planned)

- ActiveSync protocol support
- S3 cloud backup integration
- SSO redirect flow
- Load testing validation

---

## Changelog

### v1.0.1 (2026-06-06)
- **FIXED**: SQLite CGO dependency
- **FIXED**: Stalwart download URL
- **FIXED**: systemd service warnings
- **IMPROVED**: Installer prompts with validation

### v1.0.0 (2026-06-XX)
- Initial MVP release
- Full mail server platform
- 12 functional modules
- React webmail and admin UIs

---

*Orvix RC2 - Deploy with confidence*