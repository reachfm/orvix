# Orvix WORK_CONTEXT.md

**Last Updated:** 2026-06-06
**Agent:** Autonomous 100% Completion Agent
**Repository:** https://github.com/reachfm/orvix

---

## Current Architecture

### System Overview
```
┌──────────────────────────────────────────────────────────────┐
│                      Orvix Binary                            │
│  ┌────────────────────────────────────────────────────────┐   │
│  │  API Layer (Fiber v3) — 60+ endpoints                │   │
│  │  JWT Auth · RBAC · CSRF · Rate Limiting               │   │
│  └────────────────────────────────────────────────────────┘   │
│  ┌────────────────────────────────────────────────────────┐   │
│  │  Modules (12 functional)                               │   │
│  │  Firewall · Guardian · Auto-Heal · Smart Compose      │   │
│  │  DNS · Migration · Provision · Calendar · Compliance  │   │
│  │  Intelligence · Collaboration                          │   │
│  └────────────────────────────────────────────────────────┘   │
│  ┌────────────────────────────────────────────────────────┐   │
│  │  Infrastructure                                        │   │
│  │  Multi-Tenancy · Reseller · White Label                │   │
│  │  Security Alerts · LDAP · ClamAV · Backup             │   │
│  └────────────────────────────────────────────────────────┘   │
│  ┌────────────────────────────────────────────────────────┐   │
│  │  Stalwart (External Binary — Managed)                 │   │
│  │  SMTP · IMAP · POP3 · JMAP · DKIM · SPF               │   │
│  └────────────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────────┘
```

### Tech Stack
| Component | Technology | Version |
|-----------|-----------|---------|
| Backend | Go | 1.25+ |
| Web Framework | Fiber v3 | v3.3.0 |
| ORM | GORM | v1.31.1 |
| Database | SQLite/PostgreSQL | modernc.org/sqlite |
| Cache | Redis | 7+ (optional) |
| Frontend | React 19 + Vite 6 | React 19 |

---

## Completed Features (from MVP.md)

### Phase 1 — Foundation ✅
- [x] Project structure setup
- [x] go.mod with module: github.com/orvix/orvix
- [x] Config system (Viper)
- [x] Database layer (GORM + PostgreSQL + SQLite)
- [x] Migration system (auto-run, additive only)
- [x] Logging (Zap — structured)
- [x] Metrics (Prometheus)
- [x] License engine (JWT RS256 + feature flags)
- [x] Code watermarking

### Phase 2 — Stalwart Integration ✅
- [x] Stalwart managed external (not embedded)
- [x] Stalwart process manager
- [x] Stalwart config generator
- [x] Stalwart REST API client
- [x] Stalwart webhook receiver
- [x] Domain/mailbox/queue management

### Phase 3 — Auth + Core API ✅
- [x] Auth system (JWT + refresh tokens + TOTP 2FA)
- [x] Session management (Redis)
- [x] RBAC (superadmin, admin, user)
- [x] User/Domain/Admin API
- [x] API key system
- [x] Rate limiting middleware
- [x] Security headers

### Phase 4 — Modules ✅
- [x] Module registry
- [x] Auto-Heal module
- [x] Mail Firewall module
- [x] Guardian Agent module
- [x] Smart Compose AI module
- [x] Instant Deployment API module
- [x] Smart Migration module
- [x] DNS automation module
- [x] Email Intelligence module
- [x] Versioning + Auto-Update system
- [x] Changelog system

### Phase 5 — Frontend ✅
- [x] Design system + Component library
- [x] Webmail UI (compose, read, search, folders, labels)
- [x] Smart Compose AI panel
- [x] Admin Console (all panels)
- [x] Firewall rules UI
- [x] Guardian/Auto-Heal dashboards
- [x] Migration/DNS wizard UI
- [x] PWA manifest + service worker

### Phase 6 — Advanced Features ⚠️
- [x] Calendar module (CalDAV + CardDAV)
- [x] Zero-Knowledge Encryption
- [x] Collaboration Layer
- [x] LDAP/Active Directory sync
- [x] Backup & Restore
- [x] Reseller Panel model
- [x] White Label system
- [x] Compliance Center
- [ ] ActiveSync (ROADMAP.md)
- [ ] Multi-Cloud Storage (ROADMAP.md)
- [ ] SSO full redirect (ROADMAP.md)

### Phase 7 — Hardening ⚠️
- [x] Security audit complete
- [x] One-line installer
- [x] Systemd service
- [x] API documentation
- [ ] Penetration testing (ROADMAP.md)
- [ ] Load testing (ROADMAP.md)
- [ ] Deliverability testing (ROADMAP.md)
- [ ] Marketing website (ROADMAP.md)
- [ ] License portal (ROADMAP.md)
- [ ] Update server (ROADMAP.md)

---

## RC1 Failure Analysis

### Critical Failures on Clean VPS

| Issue | Error Message | Root Cause |
|-------|---------------|-----------|
| **SQLite CGO** | "Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo" | `mattn/go-sqlite3` requires CGO compilation |
| **Stalwart 404** | "404 Not Found" on download | Incorrect GitHub release URL |
| **systemd Warning** | "Unknown key name 'StartLimitIntervalSec'" | Invalid systemd directive |
| **Installer UX** | No validation on prompts | Missing input validation loops |

---

## RC2 Fixes Applied

### 1. SQLite CGO Fix ✅
**Files Changed:**
- `go.mod` — Replaced `mattn/go-sqlite3` with `modernc.org/sqlite`
- `internal/config/database.go` — Complete rewrite for pure Go SQLite

**Code Evidence:**
```go
// Before (RC1):
_ "github.com/mattn/go-sqlite3"  // requires CGO

// After (RC2):
_ "modernc.org/sqlite"  // pure Go, no CGO required
```

### 2. Stalwart Download Fix ✅
**Files Changed:**
- `release/install.sh` — Multiple download URL fallbacks

**Code Evidence:**
```bash
# Try stalw.art API first (most reliable)
STALWART_URL="https://stalw.art/api/download/v${STALWART_VERSION}/linux/x86_64"

# Fallback to GitHub if needed
GITHUB_URL="https://github.com/stalwartlabs/mail-server/releases/download/v${STALWART_VERSION}/..."
```

### 3. systemd Fix ✅
**Files Changed:**
- `release/systemd/orvix.service` — Removed invalid directives

**Code Evidence:**
```ini
# Before (RC1):
StartLimitBurst=3
StartLimitIntervalSec=60  # ← INVALID

# After (RC2):
Restart=always
RestartSec=10
# No StartLimit directives
```

### 4. Installer Validation Fix ✅
**Files Changed:**
- `release/install.sh` — Added validation functions

**Code Evidence:**
```bash
prompt_domain() {
    while true; do
        read -rp "Primary email domain (e.g., example.com): " domain
        if [[ ! "$domain" =~ ^[a-zA-Z0-9][a-zA-Z0-9.-]*\.[a-zA-Z]{2,}$ ]]; then
            echo -e "${RED}Error: Invalid domain format.${NC}"
            continue
        fi
        break
    done
    echo "$domain"
}
```

---

## Current Blockers

| Blocker | Severity | Status |
|---------|----------|--------|
| SQLite CGO | CRITICAL | ✅ Fixed in RC2 |
| Stalwart 404 | CRITICAL | ✅ Fixed in RC2 |
| systemd warning | MEDIUM | ✅ Fixed in RC2 |
| Installer validation | MEDIUM | ✅ Fixed in RC2 |
| VPS retest not done | HIGH | ⏳ Pending |
| Real checksums needed | HIGH | ⏳ Pending |

---

## Production Risks

| Risk | Impact | Mitigation |
|------|--------|------------|
| RC1 installer fails on VPS | CRITICAL | RC2 fixes applied, retest needed |
| Stalwart download URL broken | HIGH | Multiple fallback URLs added |
| No real checksums in release | MEDIUM | Build script generates checksums |
| ActiveSync not implemented | LOW | Moved to ROADMAP.md |
| SSO redirect not implemented | LOW | SSOConfig model exists |

---

## Next Execution Plan

### Priority 1: Verify RC2 Fixes
- [ ] Read actual code from GitHub
- [ ] Compare with RC2 fixes
- [ ] Verify build/test pass with CGO_ENABLED=0
- [ ] Generate real checksums

### Priority 2: Update Documentation
- [ ] Update AUDIT.md with RC2 status
- [ ] Update HANDOFF.md with current state
- [ ] Update PROGRESS.md
- [ ] Create WORK_CONTEXT.md (this file)

### Priority 3: Build Release Artifacts
- [ ] Build Go binary (CGO_ENABLED=0)
- [ ] Build frontend (webmail/admin)
- [ ] Generate checksums.txt
- [ ] Create release package

### Priority 4: Create VPS Validation Docs
- [ ] Create VPS_RETEST_COMMANDS.md
- [ ] Document expected outputs
- [ ] Document failure collection

### Priority 5: Final Report
- [ ] Honest status report
- [ ] Distinguish verified vs pending
- [ ] Clear next actions

---

## Verification Status

| Component | Local Build | Unit Tests | Vet | Frontend | VPS |
|-----------|-------------|------------|-----|----------|-----|
| RC1 | ✅ | ✅ | ✅ | ✅ | ❌ |
| RC2 | ⏳ | ⏳ | ⏳ | ⏳ | ⏳ |

---

*This file is the source of truth for current project state.*