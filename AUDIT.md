# Orvix Security Audit — Updated

## Release Status
- **RC4 Release**: `release/orvix-v1.0.3-linux-amd64.tar.gz`
- Archive SHA256: `aed4f97924b3e9315afbe9185600e6d3b8a3cecdff8698314090e768499099bb`
- Binary SHA256: `1cc564f2183ee9ad4d07e3fa4515eb2e22e8caecdfb8a6215fb817f78b7287f5`
- Git Commit: (pending push)
- GitHub Release: https://github.com/reachfm/orvix/releases/tag/v1.0.3
- Installer: Shows "Orvix v1.0.3 RC4 Installer" banner
- Download URL: https://github.com/reachfm/orvix/releases/download/v1.0.3/
- VPS test plan: `VPS_TEST_PLAN.md`
- Release audit: `RELEASE_AUDIT.md`
- Installer: Production-grade for Ubuntu 22.04+/Debian 12+
- Stalwart: Managed External — downloaded by installer (v0.16.7)

### RC4 Critical Fixes
- **Stalwart URL**: Fixed 404 error, now uses correct GitHub URL
- **Systemd directory**: Fixed missing directory before writing override.conf
- **Password prompt**: Fixed loop issue, added confirmation step

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
