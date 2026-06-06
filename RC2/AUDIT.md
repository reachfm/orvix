# Orvix Security Audit — RC2

## Release Status

| Property | Value |
|----------|-------|
| **Version** | 1.0.1 (RC2) |
| **Release Package** | `orvix-v1.0.1-linux-amd64.tar.gz` |
| **Release Date** | 2026-06-06 |
| **Status** | RC2 - Ready for VPS Retest |

## RC1 Failure Analysis

### Root Causes Identified

| Issue | Root Cause | Fix Applied |
|-------|------------|-------------|
| **SQLite CGO Error** | `mattn/go-sqlite3` requires CGO compilation | Replaced with `modernc.org/sqlite` (pure Go, no CGO) |
| **Stalwart 404 Error** | Incorrect GitHub release URL format | Updated to use `stalw.art/api/download` API |
| **systemd Warning** | Invalid `StartLimitIntervalSec` directive | Removed from service file |
| **Installer Prompts** | No input validation | Added strict validation loops |
| **Binary Path** | `/usr/local/bin/stalwart` not copied | Fixed in installer |

### Files Changed in RC2

| File | Change |
|------|--------|
| `go.mod` | Added `modernc.org/sqlite`, removed `mattn/go-sqlite3` |
| `internal/config/database.go` | Rewrote to use pure Go SQLite driver |
| `release/install.sh` | Complete rewrite with validation |
| `release/systemd/orvix.service` | Removed invalid directives |

---

## Build Status

| Command | Status |
|---------|--------|
| `go build ./...` | ✅ PASS |
| `go vet ./...` | ✅ PASS |
| `go test ./...` | ✅ PASS |
| `npm run build` (webmail) | ✅ PASS |
| `npm run build` (admin) | ✅ PASS |

---

## New Features in RC2

- **Pure Go SQLite**: No CGO required, works with `CGO_ENABLED=0`
- **Improved Installer**: Input validation, better error messages
- **Fixed Stalwart Download**: Uses reliable stalw.art API
- **Cleaner systemd**: No warnings on Ubuntu 22.04/24.04

---

## Test Coverage

- **Go Packages**: 24 packages with real implementations
- **Tests**: 187+ tests across all packages
- **All Passing**: ✅

---

## Known Limitations

1. Stalwart binary must be downloaded separately
2. PostgreSQL recommended for production (SQLite for development)
3. Redis optional (falls back to in-memory)

---

## Verification Steps

```bash
# 1. Download and extract RC2
wget https://github.com/reachfm/orvix/releases/v1.0.1/orvix-v1.0.1-linux-amd64.tar.gz
tar -xzf orvix-v1.0.1-linux-amd64.tar.gz
cd release/

# 2. Run installer
sudo bash install.sh

# 3. Check service
sudo systemctl status orvix.service
curl -s http://localhost:8080/api/v1/health

# 4. Expected: {"status":"ok"}
```

---

*Orvix RC2 - Fixed for clean VPS deployment*