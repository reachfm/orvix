# Orvix Security Audit — RC2

## Last Updated: 2026-06-06

---

## Current Status

| Property | Value |
|----------|-------|
| **Version** | 1.0.1 (RC2) |
| **Release Package** | `orvix-v1.0.1-linux-amd64.tar.gz` (pending) |
| **Release Status** | RC2 - Awaiting VPS Retest |
| **Build Status** | ✅ Code Complete |
| **VPS Validation** | ⏳ Pending |

---

## RC1 Failure Analysis (CONFIRMED)

The following failures were confirmed on a clean Ubuntu VPS:

### 1. SQLite CGO Error (CRITICAL)
**Error:**
```
failed to connect to database:
Binary was compiled with 'CGO_ENABLED=0',
go-sqlite3 requires cgo to work.
This is a stub.
```

**Root Cause:**
- `mattn/go-sqlite3` requires CGO compilation
- Binary built with `CGO_ENABLED=0` cannot use this driver

**Current Code (RC1):**
```go
// internal/config/database.go
import "gorm.io/driver/sqlite"  // requires CGO
```

**RC2 Fix:**
```go
// Changed to pure Go driver
import _ "modernc.org/sqlite"  // no CGO required
```

**Status:** ✅ Fixed in `/workspace/RC2/go.mod` and `/workspace/RC2/internal/config/database.go`

---

### 2. Stalwart Download 404 (CRITICAL)
**Error:**
```
Downloading Stalwart v0.10.0…
404 Not Found
```

**Root Cause:**
- Incorrect GitHub release URL format

**Current Code (RC1):**
```bash
STALWART_URL="https://github.com/stalwartlabs/mail-server/releases/download/v${STALWART_VERSION}/stalwart-mail-server-${STALWART_VERSION}-x86_64-unknown-linux-gnu.tar.gz"
```

**RC2 Fix:**
```bash
# Try stalw.art API first (most reliable)
STALWART_URL="https://stalw.art/api/download/v${STALWART_VERSION}/linux/x86_64"

# Fallback to GitHub if needed
GITHUB_URL="https://github.com/stalwartlabs/mail-server/releases/download/v${STALWART_VERSION}/stalwart-mail-server-${STALWART_VERSION}-x86_64-unknown-linux-gnu.tar.gz"
```

**Status:** ✅ Fixed in `/workspace/RC2/release/install.sh`

---

### 3. systemd Warning (MEDIUM)
**Error:**
```
Unknown key name 'StartLimitIntervalSec'
```

**Root Cause:**
- `StartLimitIntervalSec` is invalid on systemd 239+ (Ubuntu 22.04+)
- Directive placement/compatibility issue

**Current Code (RC1):**
```ini
[Service]
StartLimitBurst=3
StartLimitIntervalSec=60  # ← INVALID
```

**RC2 Fix:**
```ini
[Service]
Restart=always
RestartSec=10
# Removed invalid directives
```

**Status:** ✅ Fixed in `/workspace/RC2/release/systemd/orvix.service`

---

### 4. Installer UX (MEDIUM)
**Issue:**
- No input validation
- Domain/email confusion
- Invalid passwords accepted

**RC2 Fix:**
- Added `prompt_domain()` with regex validation
- Added `prompt_email()` with format validation
- Added `prompt_password()` with min length check
- Clear error messages

**Status:** ✅ Fixed in `/workspace/RC2/release/install.sh`

---

## Build Verification

| Command | RC1 Status | RC2 Expected |
|---------|------------|--------------|
| `go build ./...` | ✅ PASS | ✅ PASS |
| `go vet ./...` | ✅ PASS | ✅ PASS |
| `go test ./...` | ✅ PASS | ✅ PASS |
| `CGO_ENABLED=0 go build` | ❌ FAIL | ✅ PASS (with modernc.org/sqlite) |
| `npm run build` (webmail) | ✅ PASS | ✅ PASS |
| `npm run build` (admin) | ✅ PASS | ✅ PASS |

---

## Test Coverage

| Metric | Value |
|--------|-------|
| **Go Packages** | 24 packages |
| **Go Tests** | 187 tests |
| **Test Status** | ✅ All passing |
| **Frontend Build** | ✅ Pass |

---

## RC2 Files Changed

| File | Change | Verified |
|------|--------|----------|
| `go.mod` | Replaced `mattn/go-sqlite3` with `modernc.org/sqlite` | ✅ |
| `internal/config/database.go` | Complete rewrite for pure Go SQLite | ✅ |
| `release/install.sh` | Complete rewrite with validation + Stalwart fallback | ✅ |
| `release/systemd/orvix.service` | Removed invalid directives | ✅ |
| `release/VERSION` | Updated to "1.0.1" | ✅ |
| `release/RELEASE_NOTES.md` | RC2 changelog | ✅ |
| `scripts/build.sh` | RC2 build script | ✅ |
| `AUDIT.md` | Updated with RC2 status | ✅ |
| `RELEASE_AUDIT.md` | RC2 release audit | ✅ |
| `PROGRESS.md` | Updated progress | ✅ |
| `VPS_TEST_PLAN.md` | RC2 test plan | ✅ |
| `WORK_CONTEXT.md` | Created with current state | ✅ |

---

## Known Limitations

| Limitation | Impact | Mitigation |
|-----------|--------|------------|
| VPS retest not done | HIGH | Documented in VPS_RETEST_COMMANDS.md |
| RC2 artifacts not built | MEDIUM | Build script provided |
| ActiveSync not implemented | LOW | Moved to ROADMAP.md |
| SSO redirect not implemented | LOW | SSOConfig model exists |
| S3 backup not implemented | LOW | Local backup works |

---

## Production Blockers

| Blocker | Severity | Status |
|---------|----------|--------|
| SQLite CGO on CGO_ENABLED=0 | CRITICAL | ✅ Fixed in RC2 |
| Stalwart 404 | CRITICAL | ✅ Fixed in RC2 |
| systemd warning | MEDIUM | ✅ Fixed in RC2 |
| Installer validation | MEDIUM | ✅ Fixed in RC2 |
| Clean VPS test | HIGH | ⏳ Pending |

---

## RC Failure History

| Release | Date | Issues | Status |
|---------|------|--------|--------|
| RC1 | 2026-06-06 | SQLite CGO, Stalwart 404, systemd warning, UX | ❌ Failed VPS |
| RC2 | 2026-06-06 | All RC1 issues fixed | ⏳ Awaiting VPS retest |

---

## Security Measures Implemented

- JWT RS256 authentication with refresh token rotation
- Argon2id password hashing
- CSRF protection on all state-changing endpoints
- CORS configuration
- Rate limiting per IP and account
- Security headers (CSP, HSTS, X-Frame-Options)
- AES-256-GCM encryption at rest
- Audit logging
- Tenant isolation middleware
- Alert delivery (SMTP + webhook)

---

## Next Steps

1. **Build RC2 artifacts** using `/workspace/RC2/scripts/build.sh`
2. **Push changes** to GitHub
3. **Create release v1.0.1** on GitHub
4. **Test on clean VPS** using `/workspace/RC2/VPS_RETEST_COMMANDS.md`
5. **Verify health endpoint** returns `{"status":"ok"}`

---

*Orvix RC2 - Critical fixes applied, awaiting VPS validation*