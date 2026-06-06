# Orvix Security Audit — Updated

## Release Status
- **RC6 Release**: `release/orvix-v1.0.5-linux-amd64.tar.gz`
- Archive SHA256: `f93d159fb27631220ef03814da910d649e72edb6b60623d344fbf192d0d80d48`
- Binary SHA256: `8d5d728188ab724f5e878b43f812071b15fa969e0f0faa8b83ab12effe447556`
- Git Commit: (pending push)
- GitHub Release: https://github.com/reachfm/orvix/releases/tag/v1.0.5
- Installer: Shows "Orvix v1.0.5 RC6 Installer" banner
- Download URL: https://github.com/reachfm/orvix/releases/download/v1.0.5/
- VPS test plan: `VPS_TEST_PLAN.md`
- Release audit: `RELEASE_AUDIT.md`
- Installer: Production-grade for Ubuntu 22.04+/Debian 12+
- Stalwart: Managed External — downloaded by installer (v0.16.7)
- Redis: Installed and enabled by installer

### RC6 Critical Fixes
- **Systemd override syntax**: Fixed using printf instead of heredoc, proper escaping
- **Domain-first installer**: Added prompts for Primary Domain, Mail/Admin/Webmail Hostnames
- **Password policy**: Minimum 8 chars with weak password rejection
- **Stalwart verification**: Enhanced healthcheck shows exact command and config path

### RC5 Critical Fixes
- **Systemd hardening**: Added ReadWritePaths for /etc/orvix, /var/lib/orvix, /var/log/orvix
- **Stalwart v0.16.7**: Fixed config.json format, removed --data arg
- **Redis**: Added redis-server installation and enable
- **Healthcheck**: Comprehensive post-install validation added

### RC3 Security Fixes
- **CRITICAL**: Removed hardcoded default credentials (admin@orvix.local / admin123)
- Admin credentials MUST be provided via environment variables:
  - `ORVIX_ADMIN_EMAIL`
  - `ORVIX_ADMIN_PASSWORD`
- Installer prompts for admin email and password during installation

## Build Status
- `go build ./...` — PASS
- `go vet ./...` — PASS
- `go test ./...` — PASS
- `npm run build` (webmail) — PASS
- `npm run build` (admin) — PASS

## New Features Implemented
- **Multi-Tenancy**: Tenant model, tenant middleware, reseller_id on tenants
- **Reseller**: Reseller model with limits (tenants/domains/mailboxes/commission)
- **White Label**: Logo URL, primary color on Tenant; tenant branding middleware
- **Security Alert Delivery**: AlertSender with SMTP and webhook; AlertConfig model
- **LDAP Integration**: LDAPConfig model, LDAP syncer with connection test
- **ClamAV Anti-Virus**: Scanner with TCP INSTREAM protocol support
- **Backup & Restore**: Database backup, config backup, BackupHistory model

## New Models Added
- Tenant, Reseller, LDAPConfig, SSOConfig, AlertConfig (in models.go)
- BackupHistory (in internal/backup)
- ClamAV scan result types (in internal/clamav)

## New Packages
- `internal/auth/tenant.go` — Tenant middleware (resolve, scope, branding)
- `internal/auth/alerts.go` — AlertSender (SMTP + webhook delivery)
- `internal/clamav/scanner.go` — ClamAV virus scanner
- `internal/ldap/syncer.go` — LDAP sync engine
- `internal/backup/manager.go` — Backup/restore manager

## Security Measures Implemented
- All previous measures: JWT RS256, Argon2id, CSRF, CORS, rate limiting, encryption
- SecurityMonitor now uses AlertSender for real SMTP/webhook alert delivery
- Tenant isolation via tenant_id scoping middleware
- CSRF enforced on all state-changing admin endpoints
- API key auth alongside JWT

## Test Coverage
187 tests across 22 packages — all passing

## Known Limitations
1. Stalwart external dependency
2. Full SSO/OAuth redirect flow not implemented (config storage exists)
3. Reseller/White Label admin UI pages not built
4. ClamAV webhook integration not wired (scanner exists)
5. S3 cloud backup not implemented (local backup works)
6. Multi-instance clustering not supported
7. Installer untested on clean Linux
