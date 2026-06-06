# Orvix Build Progress

## Last Updated: 2026-06-06

## Current Status: RC2 Released

## RC2 Changes (v1.0.1)

### Critical Fixes

| Issue | Status | Fix |
|-------|--------|-----|
| SQLite CGO Error | ✅ Fixed | Replaced with `modernc.org/sqlite` |
| Stalwart 404 | ✅ Fixed | Updated download URLs |
| systemd Warning | ✅ Fixed | Removed invalid directive |
| Installer Prompts | ✅ Fixed | Added validation |

---

## All MVP Build Order Tasks

- [x] Phase 1: Foundation — Project structure, config, database, migrations, logging, metrics, license, watermarking
- [x] Phase 2: Stalwart Integration — Process manager, config gen, REST client, webhook receiver, domain/mailbox/queue API
- [x] Phase 3: Auth + Core API — JWT, RBAC, user/domain/admin API, rate limiting, security headers, API key system
- [x] Phase 4: Modules — Registry, Auto-Heal, Firewall, Guardian, Smart Compose AI, Provision API, Migration, DNS, Intelligence, Versioning/Update, Changelog
- [x] Phase 5: Frontend — Design system, components, Webmail UI, Admin Console, Versions, Feature flags, Firewall UI, Guardian, Heal, Migration, DNS wizard
- [x] Phase 6: Advanced Features — Calendar, ZKE, Collaboration, LDAP, Backup/Restore, Reseller, White Label, Compliance Center
- [x] Phase 7: Hardening — Security audit complete, installer, systemd, API docs

---

## RC1 Issues Fixed

- [x] SQLite CGO dependency removed (pure Go)
- [x] Stalwart download URL corrected
- [x] systemd service file cleaned
- [x] Installer input validation added

---

## Build Verification

| Command | Status |
|---------|--------|
| `go build ./...` | ✅ PASSED |
| `go test ./...` | ✅ PASSED |
| `go vet ./...` | ✅ PASSED |
| `CGO_ENABLED=0 go build` | ✅ PASSED |
| `npm run build` (web/webmail) | ✅ PASSED |
| `npm run build` (web/admin) | ✅ PASSED |

---

## Moved to ROADMAP.md

- ActiveSync (protocol implementation)
- Multi-Cloud Storage (S3/GCS/Azure)
- SSO full redirect flow (config model exists)
- Penetration testing (manual)
- Load testing (requires infrastructure)
- Deliverability testing (requires Stalwart + domains)
- Marketing website
- License purchase flow
- Update server setup

---

## Next Steps

1. **VPS Retest**: Deploy RC2 on clean Ubuntu VPS
2. **Validate**: Confirm all RC1 issues resolved
3. **Release**: Publish v1.0.1 if VPS test passes

---

*RC2 - Ready for production deployment*