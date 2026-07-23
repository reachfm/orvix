# ORVIX Feature Matrix

Status values: **COMPLETE** / **PARTIAL** / **IN PROGRESS** / **BLOCKED** / **NOT STARTED**

| Feature | Backend | Frontend | API | Database | Tests | Prod-ready | Priority | Status |
|---|---|---|---|---|---|---|---|---|
| SMTP inbound | `internal/coremail/smtp` | n/a | n/a | coremail_* mail-store tables | 5 test files | Yes | Critical | COMPLETE |
| Submission (587) | `internal/coremail/smtp` | n/a | n/a | same | shared with SMTP | Yes | Critical | COMPLETE |
| SMTPS (465) | `internal/coremail/runtime/module.go:372-378` | n/a | n/a | — | none | No — logs warning, never listens | High | NOT STARTED |
| IMAP / IMAPS | `internal/coremail/imap` (1091-line `commands.go`) | n/a | n/a | coremail_mailboxes/folders/messages | 7 test files | Yes | Critical | COMPLETE |
| POP3 / POP3S | `internal/coremail/pop3` | n/a | n/a | same | 3 test files | Yes (has `context.TODO()` tech debt) | High | PARTIAL |
| JMAP | `internal/coremail/jmap` | webmail SPA (indirect) | — | same | 13 test files (best-covered subpackage) | Yes | High | COMPLETE |
| DKIM / SPF / DMARC | `internal/coremail/dkim,spf,dmarc` | admin DNS pages | `/admin/dns/*` | coremail_dkim_config | 1 test file each | Yes, thinly tested | Critical | PARTIAL |
| Queue / delivery | `internal/coremail/queue,delivery` | admin queue.js | `/admin/queue/*` | coremail_queue, coremail_delivery_attempts | 2 + 7 test files | Yes | Critical | COMPLETE |
| Queue attempt detail | `internal/api/handlers/admin_queue.go` | admin queue.js | `/admin/queue/:id` | Repointed at `coremail_delivery_attempts` this session (real, actively-written table; `queue_attempts` was confirmed dead — zero writers anywhere) | none added this pass (out of the confirmed-issue list scope) | Yes | High | COMPLETE |
| Antispam / Antivirus | `internal/coremail/antispam`, `internal/antivirus`, `internal/clamav` | admin antivirus.js | `/admin/antivirus` | — | 1 test file (antispam) | Yes | Critical | PARTIAL |
| Webmail — mail list/read/send | `internal/api/handlers/webmail_user.go` | `web/webmail` | `/api/v1/webmail/*` | coremail_messages/attachments | present | Yes, but see mail-list endpoint mismatch below | Critical | PARTIAL |
| Webmail — mail list endpoint | same | `EmailList.tsx` calls `/api/v1/queue` | mismatch vs `/api/v1/webmail/messages` | — | — | Needs verification | High | BLOCKED (unverified) |
| Webmail — attachments | webmail_user.go | web/webmail | `/api/v1/webmail/*` | coremail_attachments | present | Yes | High | COMPLETE |
| Webmail — drafts | webmail_user.go | web/webmail (Tiptap) | `/api/v1/webmail/drafts` | present | present | Yes | High | COMPLETE |
| Webmail — calendar | `internal/calendar` module | web/webmail (FullCalendar) | admin calendar routes | present | 2 files | Partial | Medium | PARTIAL |
| Webmail — contacts | — | — | admin `/contacts` (admin-only, not customer-facing) | — | — | No customer-facing contacts API found | Medium | NOT STARTED |
| Vacation responder | `internal/coremail/storage/rules.go` (has a deprecated legacy path alongside `ClaimVacationReply`) | web/webmail settings | `/webmail/vacation` | present | present | Yes, with tech-debt flag | Medium | PARTIAL |
| Forwarding | webmail_user.go | web/webmail settings | `/webmail/forwarding` | present | present | Yes | Medium | COMPLETE |
| Push notifications (VAPID) | `internal/coremail/push` | web/webmail `push.ts` | `/webmail/push/*` | present | 1 test file | Yes | Medium | COMPLETE |
| Admin console (Vite SPA) | N/A | `web/admin` (38 files) | `/api/v1/admin/*` (~180 endpoints) | — | — | Yes — the confirmed `/mailboxes` routing bug (was `ListUsers`) is fixed this session | Critical | COMPLETE |
| Admin console (legacy fallback) | N/A | `release/admin/modules` (61 modules) | same | — | — | Intentional minimal-toolchain fallback, not runtime-loaded | Low | COMPLETE (by design) |
| Customer/enterprise self-service | `internal/api/handlers/customer_*.go`, `enterprise_admin*.go` | web/admin | `/api/v1/enterprise/*` (~45 endpoints) | tenants, coremail_domains/mailboxes | present, and freshly added for aliases/groups | Yes (aliases/groups IDOR fixed this session) | Critical | PARTIAL |
| Customer — aliases | customer_mail.go | web/admin | `/enterprise/aliases` | coremail_aliases | 12 new tenant-isolation tests | Yes | High | COMPLETE |
| Customer — groups | customer_mail.go | web/admin | `/enterprise/groups*` | `coremail_groups`/`coremail_group_members` — migrations added this session (both SQLite and PostgreSQL) | 12 handler-level tests + 2 full-router tests (`enterprise_mutation_smoke_test.go`) | Yes — fixed and verified end-to-end this session (also fixed a latent `ListGroups` scan-count bug surfaced by the schema now existing) | High | COMPLETE |
| RBAC (roles + permissions) | `internal/auth`, `internal/enterprise/rbac` | — | gates most `/enterprise` and `/admin` routes | coremail_admin_groups/_members, ACL rules | present | Yes | Critical | COMPLETE |
| Authentication (JWT, sessions) | `internal/auth` | login pages | `/auth/*` | sessions, revoked_tokens | present | Yes | Critical | COMPLETE |
| MFA | `internal/auth` (`admin_mfa.go`) | admin/login | `/auth/mfa/*`, `/admin/mfa/*` | mfa_recovery_codes | present | Yes (TOTP/SHA1 only — see Master TODO) | Critical | PARTIAL |
| WebAuthn / FIDO2 | — | — | — | — | — | No | Medium | NOT STARTED |
| CSRF protection | `internal/auth.CSRFManager` | all admin/enterprise mutation forms | group-level middleware | csrf_token cookie | present | Yes | Critical | COMPLETE |
| IDOR protections (tenant isolation) | `internal/api/handlers/*` | — | scattered across `/enterprise` and `/admin` | — | `tenant_isolation_test.go` (mailboxes/domains) + `customer_mail_tenant_isolation_test.go` (aliases/groups) + `enterprise_mutation_smoke_test.go` (`requireTenantActive`) + `mailbox_export_isolation_test.go` (CSV exports) | Mostly — `requireTenantActive` fixed; mailbox/domain **CSV export** IDOR fixed and regression-tested this session. **One known-open item:** `ListDomains` (`GET /api/v1/domains`) has the same missing-tenant-scope defect (see `docs/MASTER_TODO.md`) | Critical | PARTIAL |
| Billing / subscriptions | `internal/billing` (20 files) | admin billing pages | `/billing/*`, `/enterprise/billing` | invoices, billing tables | present | Yes | Critical | COMPLETE |
| Monitoring / alerting | `internal/monitoring`, `internal/observability` | admin observability.js/alerts.js | `/monitoring/*` | monitoring_alerts(+deliveries) | present | Yes | High | COMPLETE |
| Metrics | `internal/metrics` | admin dashboard | `/admin/summary` | in-memory counters | present | Yes | Medium | COMPLETE |
| Logging / audit trail | `internal/audit` | admin audit-log.js | `/audit/logs` | orvix_audit | present | Yes | Critical | COMPLETE |
| Backup | `internal/backup` (16 files, unix-only) | admin backups.js | `/admin/backups*` | backup_registry, backup_schedule_config | present | Yes | Critical | COMPLETE |
| Postgres-specific encrypted backup | `internal/pgbackup` | — | — | — | — | **Orphaned — zero references, unclear if wired anywhere** | Medium | BLOCKED (needs verification) |
| Restore | `internal/restorecoord` + `cmd/orvix restore-run` | — | privileged CLI path, not HTTP | — | present | Yes | Critical | COMPLETE |
| Upgrade / self-update | `internal/updater` (12 files) | admin updates.js | `/admin/update*` | upgrade_history, coremail_versions | present | Yes | Critical | COMPLETE |
| DNS ops (verification, providers) | `internal/dnsops`, `internal/dnsverify`, `internal/domainregistry` | admin DNS pages | `/admin/dns*`, `/customer/domains/:id/dns` | customer_domain_verifications | present | Yes | High | COMPLETE |
| Firewall | `internal/firewall` module | admin firewall.js | `/admin/firewall*` | FirewallRule/Log | present | Yes | High | COMPLETE |
| Guardian (AI threat analysis) | `internal/guardian` module | — | — | GuardianLog | present | Yes | Medium | COMPLETE |
| Autoheal | `internal/autoheal` module | admin health.js | `/admin/heal/*` | HealHistory | present | Yes | Medium | COMPLETE |
| Doctor / diagnostics | scripts (`healthcheck.sh`, `diagnostics.sh`) | — | — | — | — | Yes | Medium | COMPLETE |
| CLI (`orvix` binary) | `cmd/orvix/main.go` | n/a | subcommands: migrate, restore-run, serve | — | present | Yes | Critical | COMPLETE |
| Background workers/jobs | queue workers (coremail), autoheal, guardian, updater checks | — | — | — | present | Yes | High | COMPLETE |
| Legacy `internal/adminapi` server | orphaned, build-tag gated | — | — | — | — | No — dead code candidate | Low | NOT STARTED (deprecated) |
| LDAP sync | `internal/ldap` (1 file) | — | — | LDAPConfig model exists | — | **Orphaned — zero references** | Low | BLOCKED (needs verification) |
| SSO (OIDC/SAML) | `s_s_o_configs` table exists (per `docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md`) | — | no admin API | SSOConfig model | — | No | Medium | NOT STARTED |
| Multi-tenant branding | `enterprise_parity.go` | branding.js pages | `/admin/tenants/:id/branding` | tenants.logo_url/primary_color | present | Yes | Medium | COMPLETE |
| Storage topology | `enterprise_parity.go` | storage-topology.js | `/admin/storage/volumes` | filesystem-derived, no fake shard/replica data | present | Yes (honest single-node only) | Low | COMPLETE |

---

*Companion documents: `docs/PROJECT_MAP.md`, `docs/CODEBASE_INDEX.md`, `docs/MASTER_TODO.md`, `docs/PROJECT_AUDIT_REPORT.md`.*
