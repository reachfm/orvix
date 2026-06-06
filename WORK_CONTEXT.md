# Orvix Work Context

## Architecture Summary
- **Backend**: Go 1.25+ with Fiber v3 HTTP framework
- **Database**: GORM with SQLite (default) or PostgreSQL
- **Caching/Rate Limiting**: Redis (optional), falls back to in-memory
- **Mail Engine**: Stalwart (external binary, REST API integration)
- **Frontend**: React 19 + Vite 6 + Tailwind CSS v4 (webmail + admin)
- **Auth**: JWT RS256 (15min access) + refresh tokens (30d, rotated), Argon2id passwords
- **License**: JWT RS256-signed tokens with 3 tiers (SMB/ISP/Enterprise)
- **Modules**: 12 modules registered and functional

## Module Status (All 12 Functional)

| Module | Status | Description |
|--------|--------|-------------|
| Firewall | ✅ | Pipeline, rules CRUD, IP reputation, webhook-triggered |
| Auto-Heal | ✅ | DB/disk health checks, action history |
| Guardian | ✅ | DeepSeek AI + offline threat analysis, threat logs |
| Smart Compose AI | ✅ | Compose/summarize/rewrite via DeepSeek, SSE streaming |
| DNS Automation | ✅ | MX/SPF/DKIM/DMARC generation, Cloudflare provider |
| Smart Migration | ✅ | IMAP engine, connection test, job tracking |
| Auto-Update | ✅ | Version check, changelog, update application |
| Provision API | ✅ | Instant domain + mailbox provision, rollback |
| Calendar | ✅ | Events CRUD, user-scoped |
| Contacts | ✅ | Contacts CRUD, user-scoped |
| Tasks | ✅ | Tasks CRUD with completion |
| Collaboration | ✅ | Shared mailboxes via Stalwart |
| Compliance | ✅ | Legal holds, retention policies, ZKE encryption |
| Intelligence | ✅ | Email analytics, delivery reports |

## Build Status
- `go build ./...` — PASS
- `go vet ./...` — PASS
- `go test ./...` — PASS (187 tests across 18 packages)
- `npm run build` (webmail) — PASS
- `npm run build` (admin) — PASS

## Test Coverage
187 tests across 18 packages:
- auth: 15, autoheal: 5, calendar: 6, collaboration: 4, compliance: 10, compose: 3,
  config: 8, dns: 6, firewall: 8, guardian: 7, intelligence: 5, license: 5,
  migration: 5, modules: 7, provision: 5, stalwart: 8, updater: 6

## Security Status
- All critical issues resolved
- JWT key persisted to disk (no ephemeral keys)
- Argon2id + constant-time password comparison
- CSRF double-submit cookie on all state-changing endpoints
- CORS configurable (no wildcard)
- Redis or in-memory rate limiting
- AES-256-GCM encryption on sensitive DB fields
- Security monitor with 5-failure threshold

## API Inventory
60+ endpoints under `/api/v1/` covering all modules

## Features Not Yet Implemented
- Reseller panel / White label / SSO / LDAP / ActiveSync / ClamAV
- SMTP alert delivery (alerts are log-only)
- Multi-tenancy scoping
- Full frontend SPA integration
