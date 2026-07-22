# OrvixEM Production Blockers

**Date:** 2026-06-05
**Source:** Runtime gap analysis + VPS deployment evidence

---

## P0 — Production Critical (Will break for every customer)

| # | Blocker | System | Details | Fix Required |
|---|---|---|---|---|
| PB-1 | **Mail never delivered** | Mail Queue | `isStalwartAvailable()` always returns `false` (hardcoded). All queued mail is marked "deferred" with error "mail server not available". Customer sends email → it disappears into queue → never delivered. No admin alert. | Wire `isStalwartAvailable()` to actual Stalwart binary detection OR implement SMTP relay fallback using configured SMTP server |
| PB-2 | **Webmail shows users, not messages** | Webmail | Inbox.tsx queries `/users` endpoint for email list. Returns user objects, not email messages. Customer sees list of users where emails should be. | Add proper message list endpoint or filter users to show only email-like data |
| PB-3 | **Backup directory wrong on VPS** | Backup | `getBackupDir()` defaults to `./backups` relative to process cwd. On systemd service, cwd is `/var/lib/orvix`. Backup creates files there silently. `scabBackups()` and `getBackupDir()` both use `./backups` which may be different directories if called at different times. | Use absolute path from config or env var. Default to `/var/backups/orvix`. |
| PB-4 | **Auto-heal not actually healing** | Auto-Heal | Monitor health checks run but no fixers are configured. `healMon.AddCheck()` adds checks with `Fix: nil`. When a check fails, nothing happens. Customer thinks auto-heal is working — it's not. | Either wire fixer functions or remove "auto-heal" branding from non-functional feature |
| PB-5 | **No welcome email or first-use guidance** | Onboarding | Fresh VPS install has zero data. Admin panel shows all zeros. No setup wizard, no welcome message, no guided tour. Customer is lost after install. | Add bootstrap detection and first-run setup flow |

## P1 — Major Customer-Facing Bug

| # | Blocker | System | Details | Fix Required |
|---|---|---|---|---|
| PB-6 | **Prometheus metrics empty** | Monitoring | `metrics.Register()` is never called. Default registry is empty. `/metrics` serves nothing useful. Customer cannot monitor system. | Call `metricsSvc.Register()` in main.go after creating the service |
| PB-7 | **License never activates** | Licensing | No API endpoint to submit license key. No license server integration. Customer purchases license but cannot activate it. Must manually insert DB record. | Add `POST /api/v1/license/activate` endpoint |
| PB-8 | **DNS records generated but never published** | DNS Wizard | Domain creation generates DKIM/SPF/DMARC text records. But they stay in the DB as text fields. No DNS provider publishes them. Customer thinks DNS is configured — it's not. | Add manual DNS instruction display in the domain detail view |
| PB-9 | **Backup never runs automatically** | Backup | `backupTicker` only starts when `updates.auto_apply = true`. Default is `false`. Customer thinks daily backups happen — they don't. | Start backup ticker independently of auto-apply setting |
| PB-10 | **`ProtectSystem=full` blocks DB writes** | systemd | Orvix.service has `ProtectSystem=full`. With SQLite, DB file is in working directory. `ProtectSystem=full` makes `/var/lib/orvix` read-only. Service starts but DB writes fail silently. | Set `ProtectSystem=strict` with `ReadWritePaths=/var/lib/orvix` or remove `ProtectSystem` |
| PB-11 | **Upgrade path never tested** | Upgrades | `orvix update-apply`, `orvix update-rollback` CLI commands exist but never tested on VPS. May corrupt installation. | Test upgrade cycle on real VPS |
| PB-12 | **JWT token in localStorage** | Security | API client stores JWT in localStorage (from `useAuthStore` with `persist` middleware). This is vulnerable to XSS. The MVP spec requires "memory only" storage for access tokens. | Move access token to memory-only, use httpOnly cookie for refresh token |

## P2 — Functionality Gaps

| # | Blocker | System | Details |
|---|---|---|---|
| PB-13 | No database migration safety checks | Migrations | AutoMigrate creates/alters tables on every startup. No version tracking for schema changes. |
| PB-14 | No session cleanup | Sessions | Expired sessions accumulate in database. No cleanup job. |
| PB-15 | No disk usage monitoring | Monitoring | System health check doesn't include disk, memory, or CPU metrics. |
| PB-16 | Installer doesn't set ORVIX_ENCRYPTION_KEY | Installer | Backup encryption key not configured during install. |
| PB-17 | Firewall rules never evaluated | Firewall | Rules engine exists but is never wired to any mail processing pipeline. |
| PB-18 | Guardian AI has no training data | Guardian | Pattern learning and anomaly detection are skeletons. No historical data to learn from. |
| PB-19 | Smart Compose has no user context | Compose | Suggestions are not personalized per user. No email history used. |
| PB-20 | No rate limit on login endpoint | Auth | Login form has no per-account rate limiting. Only per-IP limiting exists. |

## P3 — Polish

| # | Blocker | System | Details |
|---|---|---|---|
| PB-21 | Missing `favicon.ico` | Frontend | 404 on `/favicon.ico` |
| PB-22 | No browser notifications | Webmail | New email notifications not implemented |
| PB-23 | Calendar has no month/week/day views | Webmail | Calendar is basic CRUD list, no visual calendar |
| PB-24 | Admin pages have no pagination | Admin | Tables show all records, no limit/offset |
| PB-25 | No CSV export for users/domains | Admin | Cannot export data from admin panels |
| PB-26 | Port conflicts in docker-compose | Docker | docker-compose maps ports 25/143/993 for both orvix and stalwart — conflict |
| PB-27 | No maintenance page | Frontend | When server is down, no friendly error page |
| PB-28 | No email templates | System | Welcome emails, password resets, notifications all missing |
