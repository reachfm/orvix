# Orvix Full Feature Plan

## Priority Legend
- P0: Must have for core email product
- P1: Important for ISP/Enterprise  
- P2: Value-add features

## Implementation Status

### 1. Core Mail/Domain/Mailbox/Queue (P0) ✅ Complete
- Stalwart REST API wired
- Backend CRUD complete
- Admin UI complete

### 2. Auth/Security (P0) ✅ Complete
- JWT, CSRF, CORS, API keys, rate limiting, RBAC
- Session management, security monitor
- 51 tests across auth/config/security packages

### 3. Firewall (P0) ✅ Complete
- Pipeline engine with configurable layers
- Rules CRUD with regex safety limits
- IP reputation via AbuseIPDB (cache with 15min TTL)
- Webhook trigger wired via EventProcessor
- Allow/block/quarantine actions
- Audit logging on every evaluation
- 8 pipeline tests

### 4. Auto-Heal (P1) ✅ Complete
- Health check registry with configurable intervals
- Database health check (ping)
- Disk usage health check (statfs)
- Queue depth health check
- Action history persisted to HealHistory model
- API endpoints: GET /heal/history, POST /heal/check/:name

### 5. DNS Automation (P1) ✅ Complete
- DNS record generation (MX/SPF/DKIM/DMARC)
- Cloudflare provider with CreateMX/TXT/DKIM/DMARC/SPF
- Route53 provider interface defined
- DNS verification via POST /dns/check/:domain
- DNS wizard via POST /dns/wizard/:domain

### 6. Smart Migration (P1) ✅ Complete
- Generic IMAP sync engine with connection test
- Source connection test (TCP dial with timeout)
- Background sync job with progress tracking
- Migration job model with status/progress/timestamps
- Migration log model for event tracking
- API endpoints: POST /migration/test, POST /migration/start, GET /migration/jobs
- Supported providers: axigen, zimbra, exchange, cpanel, google-workspace, generic-imap

### 7. Guardian (P1) ✅ Complete
- Webhook-triggered email analysis via EventProcessor
- DeepSeek AI integration (configurable via ORVIX_DEEPSEEK_API_KEY)
- Offline SPF/DKIM/DMARC scoring fallback
- Threat logs persisted to GuardianLog model
- Quarantine decision support (pass/quarantine/block)
- API endpoints: POST /guardian/analyze, GET /guardian/logs

### 8. Smart Compose AI (P1) ✅ Complete
- Compose completion endpoint (POST /compose/complete)
- Summarize endpoint (via action parameter)
- Tone rewrite endpoint (via action + tone parameters)
- DeepSeek streaming via SSE (POST /compose/stream)
- Graceful error when API key missing
- Webmail compose integration API ready

### 9. Auto-Update (P2) ✅ Complete
- Version check endpoint (GET /updates/check)
- Changelog endpoint with DB persistence (GET /updates/changelog)
- Update download with checksum (via UpdateManager)
- Backup before update (via RollbackManager)
- Rollback metadata (via UpdateHistory model)
- API endpoint: POST /updates/apply/:module

### 10. Provision API (P1) ✅ Complete
- Create domain + mailbox via Stalwart API
- Rollback on failure (deletes domain if mailbox creation fails)
- Audit logging on every provision
- API key auth support (protected group)
- API endpoint: POST /provision/domain

### 11. Calendar/Contacts/Tasks (P2) ✅ Complete
- Event model with title, description, start/end time, all-day, location, color, recurrence
- Contact model with name, email, phone, company, notes, photo
- Task model with title, description, due date, completed, priority
- Full CRUD APIs for all three
- User-scoped queries

### 12. Compliance/Legal Hold (P2) ✅ Complete
- Legal hold model with target email, reason, active, expiration
- Retention policy model with name, retention days, action, enabled
- Full CRUD APIs for both
- Audit logging on legal hold creation
- Existing ZKE integration preserved

### 13. Webhook Event Handlers (P1) ✅ Complete
- Email received → firewall pipeline + guardian analysis + audit log
- Email sent → audit log + analytics
- Bounce received → audit log
- Auth failure → security monitor + audit log
- EventProcessor in stalwart/events.go with full implementation

### 14. Security Alert Delivery (P1) 🔶 Partial
- Failed login detection ✅ (DB-backed, 5-failure threshold)
- Suspicious key usage detection ✅ (via SecurityMonitor)
- Email/webhook notification delivery ❌ (log-only)
- SMTP alert config needed

### 15. Multi-Tenancy (P2) 🔶 Partial
- Tenant boundaries in shared mailbox/collaboration models
- Admin/reseller boundaries defined
- Full scoping across all models needed

### 16. Reseller/White Label (P2) 🔴 Not Started
- Reseller model with customer limits
- Tenant branding (logo, colors, domain)
- Reseller dashboard

### 17. SSO/LDAP (P2) 🔴 Not Started
- LDAP sync config + connection test
- OAuth/SAML config storage
- Routes disabled if not configured

### 18. Installer (P1) 🔶 Partial
- Linux install script exists
- Stalwart download + verify needed
- System user/group creation needed
- Config prompts needed
- Uninstall script needed

### 19. Tests (P1) 🔶 Partial
- 51 existing tests across auth/config/firewall/license/modules/stalwart
- New modules need tests: autoheal, guardian, compose, dns, migration, provision, calendar, collaboration, compliance, intelligence, updater
- Mock-based tests needed
- Integration test mode needed

## Build Verification
- `go build ./...` — PASS
- `go vet ./...` — PASS
- `go test ./...` — PASS (51 tests)
- `npm run build` (webmail) — PASS
- `npm run build` (admin) — PASS
