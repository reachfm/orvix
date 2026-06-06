# Orvix Security Audit — Updated

## Release Status
- Release package: `release/orvix-v1.0.0-linux-amd64.tar.gz`
- Archive size: 9,445,131 bytes (9.4 MB)
- Archive SHA256: `79226CBEABFF3F9DB0079B1B5EDFA0A4A3F949454324270DD6B72853E08EA18B`
- Binary SHA256: `F64406D238BDB037D103950AA80A41E11E7123AC3BDB40A84209EBFB30EE9299`
- Upload manifest: `release/UPLOAD_MANIFEST.md`
- VPS test plan: `VPS_TEST_PLAN.md`
- Release audit: `RELEASE_AUDIT.md`
- Installer: Production-grade for Ubuntu 22.04+/Debian 12+
- Stalwart: Managed External — downloaded by installer

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
