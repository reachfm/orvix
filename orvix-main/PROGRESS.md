# Build Progress

## Last Updated: 2026-06-07
## Current Phase: All Phases Complete
## Current Task: RC5 Release Validation
## Done: All MVP Build Order tasks resolved + RC5 runtime fixes

## Completed
- [x] Phase 1: Foundation — Project structure, config, database, migrations, logging, metrics, license, watermarking
- [x] Phase 2: Stalwart Integration — Process manager, config gen, REST client, webhook receiver, domain/mailbox/queue API. Embedding moved to Managed External Stalwart.
- [x] Phase 3: Auth + Core API — JWT, RBAC, user/domain/admin API, rate limiting, security headers, API key system
- [x] Phase 4: Modules — Registry, Auto-Heal, Firewall, Guardian, Smart Compose AI, Provision API, Migration, DNS, Intelligence, Versioning/Update, Changelog
- [x] Phase 5: Frontend — Design system, components, Webmail UI, Admin Console, Versions, Feature flags, Firewall UI, Guardian, Heal, Migration, DNS wizard
- [x] Phase 6: Advanced Features — Calendar (backend), ZKE, Collaboration, LDAP, Backup/Restore, Reseller, White Label, Compliance Center
- [x] Phase 7: Hardening — Security audit complete, installer, systemd, API docs in HANDOFF.md

## RC6 Critical Fixes (v1.0.5)
- [x] **Systemd override syntax**: Fixed using printf instead of heredoc, proper escaping
- [x] **Domain-first installer**: Added prompts for Primary Domain, Mail/Admin/Webmail Hostnames
- [x] **Password policy**: Minimum 8 chars with weak password rejection
- [x] **Stalwart verification**: Enhanced healthcheck shows exact command and config path

## RC5 Critical Fixes (v1.0.4)
- [x] **Systemd hardening**: Added ReadWritePaths for /etc/orvix, /var/lib/orvix, /var/log/orvix
- [x] **Stalwart v0.16.7**: Fixed config.json format (JSON not YAML), removed --data argument
- [x] **Redis**: Added redis-server installation and enable
- [x] **Healthcheck**: Comprehensive post-install validation added

## RC3 Security Release (v1.0.2)
- [x] **CRITICAL**: Removed hardcoded default credentials (admin@orvix.local / admin123)
- [x] Admin credentials via environment variables: `ORVIX_ADMIN_EMAIL`, `ORVIX_ADMIN_PASSWORD`
- [x] Pure Go SQLite (modernc.org/sqlite, no CGO)
- [x] Fixed /me endpoint returning empty data
- [x] bcrypt password hashing with golang.org/x/crypto
- [x] Redis rate limiting with safe fallback
- [x] VPS-validated on 65.75.203.74
- [x] GitHub release created: https://github.com/reachfm/orvix/releases/tag/v1.0.2
- [x] Installer banner shows "Orvix v1.0.2 RC3 Installer"
- [x] Download URL points to GitHub releases

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

## Build Verification
- [x] `go build ./...` — PASSED
- [x] `go test ./...` — PASSED (187 tests)
- [x] `go vet ./...` — PASSED
- [x] `npm run build` (web/webmail) — PASSED
- [x] `npm run build` (web/admin) — PASSED

## Notes
- All MVP Build Order items resolved. Some items moved to ROADMAP.md with documented reasons.
- Security audit complete. All critical issues fixed.
- 24 Go packages with real implementations. 187 passing tests.
- Stalwart is Managed External — binary downloaded separately.
- Full documentation in HANDOFF.md, AUDIT.md, ORVIX_STATUS_MATRIX.md.
