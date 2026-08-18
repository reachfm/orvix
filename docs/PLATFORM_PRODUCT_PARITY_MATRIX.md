# Platform Product Parity Matrix — Platform Super Admin Portal

> Author: Backend Architect (Enterprise Product Completion Pass, Phase 1)
> Branch: `work/enterprise-parity-ff78209` (worktree `D:\orvix-parity`)
> Starting SHA: `ff78209f54d08397e6995455920aca3779948711`
> Scope: Platform Super Admin portal (`portal="platform"`), every visible page and
> meaningful feature traced Frontend → API client → HTTP route → middleware/RBAC →
> handler → service/domain → repository/storage → real persisted/runtime behavior.
> This document is the **execution plan** for Phase 1 and the audit record handed to
> the API Platform Engineer and Frontend Developer.

## Disposition taxonomy (final, this pass)

| Disposition | Meaning |
|---|---|
| COMPLETE | Real backend + real frontend consumer + tests; no gap found this pass. |
| UI_GAP | Backend capability real and complete; frontend page/action missing or not wired (frontend agent's scope; backend evidence recorded here). |
| BACKEND_GAP | Backend capability missing or incomplete; fixed in this pass (see Remediation Log) or explicitly dispositioned. |
| CONTRACT_DRIFT | Backend response/request contract differs from the frontend consumer's expectation; fixed in this pass or recorded. |
| RBAC_BUG / TENANT_SCOPE_BUG | Authorization or tenant-isolation defect; fixed in this pass or recorded. |
| FAKE_UI | Frontend renders a state/action the backend does not perform. |
| STUB_BACKEND | Backend handler no-ops/fabricates success. |
| DEAD_UI | Frontend page/action no longer reachable (removed nav, removed component). |
| DIRECT_FETCH_BYPASS | Frontend calls `fetch()` directly instead of the shared CSRF/auth client. |
| MISSING_TEST | Behavior real but no automated test pins it. |
| INTENTIONAL_READ_ONLY | Read-only surface by design; no mutation exists or should exist. |
| INTENTIONAL_MACHINE_ONLY | API/CLI-only by design; no operator form makes sense. |
| DEPRECATED | Feature retired; route returns 410/removed marker or is retired backend. |
| DUPLICATE_SUPERSEDED | Superseded by a newer route/page; intentionally not wired. |

## How to read a row

`Portal | Page | Component | Visible feature/action | Expected user outcome | API client method | HTTP method/path | Request contract | Response contract | Handler | Service | Repository/runtime dependency | RBAC permission | Tenant/platform scope | CSRF | Idempotency | Automated test | Status → disposition`

---

## 1. Overview

### 1.1 Overview page (`platform-home`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 1.1.1 | Platform overview dashboard (KPIs: orgs, mailboxes, domains, queue, alerts, runtime) | `web/admin/src/features/platform/overview/page.tsx` → `features/platform/overview/api.ts` → `GET /platform/dashboard` (platformMW) → `PlatformDashboard` handler → `internal/admin/dashboard` service (real tenant-wide aggregates) → SQL over `tenants`/`coremail_mailboxes`/`coremail_domains`/`coremail_queue`/`orvix_audit`. RBAC: `RolePlatformSuperAdmin`/`RoleSuperAdmin` gate. CSRF: n/a (GET). Test: `overview/page.test.tsx`; `internal/admin/dashboard` service tests. | COMPLETE |
| 1.1.2 | Quick-action tiles (if rendered) | Overview page renders nav cards to Organizations / Mail Operations / Reliability / Security — all routes verified below. | COMPLETE |
| 1.1.3 | Platform-wide "Summary" totals (`enterprise` tab → `EnterpriseDashboard.tsx` → `GET /admin/summary`) | Real platform-wide aggregate handler `AdminSummary` (domains/mailboxes/queue/audit counts across ALL tenants). Pre-existing page, verified genuinely platform-owned; route is platformMW-gated. READ_ONLY_STATUS was the prior disposition; no mutation surface exists by design. | INTENTIONAL_READ_ONLY |

## 2. Commercial

### 2.1 Organizations (`organizations`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 2.1.1 | List organizations (search/paginate) | `features/platform/organizations/page.tsx` → `api.ts listOrganizations` → `GET /platform/organizations` → `ListPlatformOrganizations` → `platformSvc.ListOrganizationSummaries` → SQL over `tenants`. RBAC: platformMW + `PermPlatformOrganizationsRead` (route registered without explicit perm — platformMW role gate only; verified per-route RBAC in `enterprise/rbac` map grants platform roles all perms). CSRF n/a. Test: `organizations/page.test.tsx`. | COMPLETE |
| 2.1.2 | Organization detail drawer (counts, admins, status) | `features/platform/organizations/components/` → `GET /platform/organizations/:id/detail` → `GetOrganizationDetail` → `orgAdminSvc.GetOrganizationDetail` (real domain/mailbox/admin counts). | COMPLETE |
| 2.1.3 | Edit organization (name/domain/plan/limits/logo/color) | `mutations.ts updateOrganization` → `PATCH /platform/organizations/:id` → `UpdateOrganization` → `orgAdminSvc.UpdateOrganization` (audited). CSRF required. | COMPLETE |
| 2.1.4 | Enable/disable organization | `mutations.ts` → `POST /platform/organizations/:id/active` → `SetOrganizationActive` → `orgAdminSvc.SetOrganizationActive` (audited; reason required). | COMPLETE |
| 2.1.5 | Schedule organization deletion (30-day soft delete) | `POST /platform/organizations/:id/deletion` → `PlatformScheduleOrganizationDeletion` → `orgAdminSvc.PlatformScheduleDeletion` (typed domain confirmation, dependency blockers, idempotent). Tests: `platform_deletion_test.go`, `platform_deletion_auth_test.go`. | COMPLETE |
| 2.1.6 | **Create organization (PSA)** | **MISSING_BACKEND before this pass.** No `POST /platform/organizations`; frontend has no create function. **Fixed in this pass** — see Remediation R-1 (`POST /platform/organizations`, owner-invitation/activation model, transactional, audited, idempotency-keyed). | BACKEND_GAP → **FIXED (R-1)**; UI wiring is UI_GAP for frontend agent |
| 2.1.7 | `GET /platform/organizations/:id` (untyped map from platformSvc) | Duplicate of `/detail`; unused by client. | DUPLICATE_SUPERSEDED |
| 2.1.8 | Organization tenant context selector (across mail-control pages) | `features/platform/tenant-context/` — local UI state; each platform mail-control route requires explicit `tenant_id` in path; verified tenant-scoped in SQL (`tenant_id=` predicate). | COMPLETE |

### 2.2 Platform Billing (`platform-billing`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 2.2.1 | Tenant balance card | `features/platform/platform-billing/page.tsx` → `GET /platform/billing/tenants/:tenant_id/balance` → `GetPlatformBillingBalance` → `platformBillSvc.GetBalance` (integer minor units). | COMPLETE |
| 2.2.2 | Adjustments ledger (list) | `GET /platform/billing/tenants/:tenant_id/adjustments` → `GetPlatformBillingAdjustments`. | COMPLETE |
| 2.2.3 | Apply credit/debit adjustment | `POST /platform/billing/tenants/:tenant_id/adjustments` → `PostPlatformBillingAdjustment` → `platformBillSvc.ApplyAdjustment` (idempotency-key supported, audited, reasoned). CSRF required. | COMPLETE |
| 2.2.4 | Reconciliation report | `GET /platform/billing/tenants/:tenant_id/reconciliation` → `GetPlatformBillingReconciliation` (read-only; never auto-corrects). | COMPLETE |
| 2.2.5 | **Organization billing overview (subscription/plan/period/usage/invoices/provider state)** | **BACKEND_GAP before this pass**: no endpoint combined the real billing domain (subscription, plan, usage, invoices) with the platform ledger for a tenant. **Fixed in this pass** — see Remediation R-3 (`GET /platform/billing/tenants/:tenant_id/overview`). Honest "provider not configured" when `cfg.Payment` has no provider; never fabricates MRR/cards/paid invoices. | BACKEND_GAP → **FIXED (R-3)**; UI_GAP for frontend agent |

## 3. Operations

### 3.1 Imports (`platform-imports`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.1.1 | Import job list (paginated/filtered) | `features/platform/imports/` → `GET /platform/imports` → `ListImports` (shared with tenant scope; platform route = explicit platformMW; tenant-scoped in SQL) → `internal/platform/importer` service + repo (`platform_imports` tables). | COMPLETE (backend); UI wiring verified in frontend feature dir — page exists (`imports/page.tsx`) |
| 3.1.2 | Import detail | `GET /platform/imports/:id` (no staging/lease internals). | COMPLETE |
| 3.1.3 | Import validation report (dry-run, before/after diffs, redacted) | `GET /platform/imports/:id/report`. | COMPLETE |
| 3.1.4 | Stage new import (CSV/JSON, SHA-256 verified at every read) | `POST /platform/imports` → `CreateImport` (Idempotency-Key required). | COMPLETE |
| 3.1.5 | Validate (dry-run, zero mutations) | `POST /platform/imports/:id/validate`. | COMPLETE |
| 3.1.6 | Execute (durable queued activation; typed confirm `EXECUTE-IMPORT-<id>` + Idempotency-Key) | `POST /platform/imports/:id/execute` → durable `internal/platform/jobs` worker. | COMPLETE |
| 3.1.7 | Resume paused/failed | `POST /platform/imports/:id/resume` (Idempotency-Key). | COMPLETE |
| 3.1.8 | Cancel running/paused | `POST /platform/imports/:id/cancel`. | COMPLETE |
| 3.1.9 | Compensate (reverse own mutations; typed confirm `COMPENSATE-IMPORT-<id>`; refuses to overwrite human changes) | `POST /platform/imports/:id/compensate` (Idempotency-Key). | COMPLETE |
| 3.1.10 | Row failures / job logs surfaced | Covered by import report + job detail + `internal/platform/importer` worker tests; the durable jobs framework records per-job result/log evidence. | COMPLETE |

### 3.2 Automation Jobs (`automation-jobs`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.2.1 | Submit platform automation job (allowlisted, idempotency-key required) | `features/platform/automation-jobs/` → `POST /platform/automation/jobs` → `SubmitPlatformAutomationJob` → `internal/platform/jobs` service/repo (`platform_jobs` tables) + registry worker. RBAC: platformMW + `PermJobsWrite`. CSRF required. | COMPLETE |
| 3.2.2 | List jobs (paginated/filtered) | `GET /platform/automation/jobs` → `ListPlatformAutomationJobs` (`PermJobsRead`). | COMPLETE |
| 3.2.3 | Job detail (no payload/lease/idempotency internals) | `GET /platform/automation/jobs/:id`. | COMPLETE |
| 3.2.4 | Cancel queued/running (cooperative) | `POST /platform/automation/jobs/:id/cancel`. | COMPLETE |
| 3.2.5 | Retry failed (idempotent requeue) | `POST /platform/automation/jobs/:id/retry`. | COMPLETE |

### 3.3 Mail Queue (`mail-operations`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.3.1 | Queue summary | `features/platform/mail-operations/` → `GET /admin/queue/summary` → `AdminQueueSummary` → real queue tables via `internal/coremail/queue`. **All six UI_SUPPORTED queue routes share the `COREMAIL_DISABLED` sanitized-503 contract** when the coremail runtime is disabled (verified `admin_queue_coremail_state_test.go`) — honest fail-closed, not fabrication. | COMPLETE (with documented 503-when-disabled behavior) |
| 3.3.2 | Queue message list | `GET /admin/queue/messages` → `AdminQueueList`. | COMPLETE |
| 3.3.3 | Queue message detail | `GET /admin/queue/messages/:id` → `AdminQueueDetail`. | COMPLETE |
| 3.3.4 | Retry now | `POST /admin/queue/messages/:id/retry` → `AdminQueueRetryNow` (CSRF). | COMPLETE |
| 3.3.5 | Bounce | `POST /admin/queue/messages/:id/bounce` → `AdminQueueBounce` (CSRF). | COMPLETE |
| 3.3.6 | Cancel | `POST /admin/queue/messages/:id/cancel` → `AdminQueueCancel` (CSRF). | COMPLETE |
| 3.3.7 | **Queue history** | `GET /admin/queue/history` → `AdminQueueHistory` — real route/handler, previously MISSING_UI. Backend verified real (history from delivery-attempt evidence); no frontend consumer. | UI_GAP (backend COMPLETE) |
| 3.3.8 | **Queue export** | `GET /admin/queue/export` → `AdminQueueExport` — real route/handler; no frontend consumer. | UI_GAP (backend COMPLETE) |
| 3.3.9 | **Bulk action (retry/bounce/cancel batch)** | `POST /admin/queue/messages/bulk-action` → `AdminQueueBulkAction` — real bounded handler; no frontend consumer. Destructive actions require typed confirmation + audit in the handler. | UI_GAP (backend COMPLETE) |
| 3.3.10 | Legacy `/queue` list / `DELETE /queue/:id` / `POST /queue/:id/retry` | Superseded by `/admin/queue/*`; still load-bearing for webmail SPA. | DUPLICATE_SUPERSEDED |
| 3.3.11 | `GET /admin/queue/:id` diagnostic entry | Superseded by `AdminQueueDetail`. | DUPLICATE_SUPERSEDED |

### 3.4 Suppressions (`platform-suppressions`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.4.1 | Suppression list (domain/state/reason/source/ranges, default active-only) | `features/platform/suppressions/` → `GET /platform/suppressions/:tenant_id` → `internal/platform/deliverability` service/repo (`platform_suppressions` + lifecycle history) — the SAME service the outbound path consults; no separate fake table. RBAC: platformMW + `PermSuppressionsRead`. | COMPLETE |
| 3.4.2 | Add suppression (reasoned, atomic upsert, idempotent, audited) | `POST /platform/suppressions/:tenant_id` (`PermSuppressionsWrite`, CSRF). | COMPLETE |
| 3.4.3 | Detail + history (append-only lifecycle evidence) | `GET /platform/suppressions/:tenant_id/:id`, `GET .../history`. | COMPLETE |
| 3.4.4 | Release / reactivate / delete | `POST .../release`, `POST .../reactivate`, `DELETE .../:id` (typed confirm `RELEASE-SUPPRESSION-<id>`), `DELETE .../` by address. All guarded transitions, audited. | COMPLETE |
| 3.4.5 | Deliverability metrics/events (real rates, no fabricated reputation) | `GET /platform/deliverability/:tenant_id/metrics`, `.../events`, `.../events/:id` (`PermDeliverabilityRead`). Data from real delivery evidence tables. | COMPLETE |

### 3.5 Bulk Mailboxes (`platform-bulk-mailboxes`) & Bulk Import (`platform-bulk-mailbox-import`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.5.1 | Template download (CSV/XLSX, no-store) | `features/platform/bulk-mailboxes/` → `GET /platform/mailboxes/bulk/template` (`PermMailboxesRead`). | COMPLETE |
| 3.5.2 | Stage upload (8 MiB bound, magic-byte sniff, formula-injection/path-traversal checks, server-generated staging ID + SHA-256) | `POST /platform/mailboxes/bulk/:tenant_id/stage` (Idempotency-Key, `PermMailboxesWrite`). | COMPLETE |
| 3.5.3 | Validate (pure dry-run against staged bytes) | `POST /platform/mailboxes/bulk/:tenant_id/validate`. | COMPLETE |
| 3.5.4 | Create job (re-validates server-side; conflict policy fail/skip_existing only) | `POST /platform/mailboxes/bulk/:tenant_id/jobs`. | COMPLETE |
| 3.5.5 | Job list/detail/rows (paginated per-row results, no secrets) | `GET /platform/mailboxes/bulk/:tenant_id/jobs`, `.../jobs/:jobId`, `.../jobs/:jobId/rows`. | COMPLETE |
| 3.5.6 | Execute (202 + durable async job, exactly-once) | `POST /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/execute` (Idempotency-Key). | COMPLETE |
| 3.5.7 | Cancel / retry (cooperative; retry only `RowFailed` rows) | `POST .../jobs/:jobId/cancel`, `.../retry`. | COMPLETE |
| 3.5.8 | Bulk Import tab (`platform-bulk-mailbox-import`) | Separate feature dir routing to the same bulk-provisioning routes; page renders tenant/job workflow. | COMPLETE |

### 3.6 Incidents (`platform-incidents`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.6.1 | Incident list (status filter) | `features/platform/incidents/` → `GET /incidents` → `internal/incident` service/repo (`incidents` + `incident_timeline`). RBAC: platformMW. | COMPLETE |
| 3.6.2 | Create incident (severity/services/regions) | `POST /incidents` (CSRF). | COMPLETE |
| 3.6.3 | Detail | `GET /incidents/:id`. | COMPLETE |
| 3.6.4 | Update status (timeline event appended) | `PATCH /incidents/:id` (CSRF). | COMPLETE |
| 3.6.5 | Timeline | `GET /incidents/:id/timeline`. | COMPLETE |

### 3.7 Retention (`platform-retention`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.7.1 | Policy create / effective policy read | `features/platform/retention/` → `POST /retention/policies`, `GET /retention/policies/effective` → `internal/platform/retention` service/repo (`retention_policies`). Destructive purge requires typed confirm `PURGE-ELIGIBLE-DATA` + rechecks legal hold at execution; **never one-click**. | COMPLETE |
| 3.7.2 | Legal holds create/list/release | `POST /retention/legal-holds`, `GET /retention/legal-holds`, `POST /retention/legal-holds/:id/release`. | COMPLETE |
| 3.7.3 | Purge plan (non-destructive dry-run) | `POST /retention/purge/plan`. | COMPLETE |
| 3.7.4 | Purge execute (typed confirm, hold recheck) | `POST /retention/purge/execute` (CSRF). | COMPLETE |
| 3.7.5 | Mailbox recover (undelete via production lifecycle) + chain of custody | `POST /retention/mailboxes/:id/recover`, `GET /retention/custody` (IDs/hashes/metadata only, never bodies). | COMPLETE |

### 3.8 DR (`platform-dr`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.8.1 | Readiness | `features/platform/dr/` → `GET /dr/readiness` → `internal/platform/dr` service (real component checks). | COMPLETE |
| 3.8.2 | Drill list / record drill outcome (no fake restore) | `GET /dr/drills`, `POST /dr/drills` (CSRF). | COMPLETE |
| 3.8.3 | Coordinated backup (durable lease) | `POST /dr/backup` → same `backup.Service` as Reliability. | COMPLETE |
| 3.8.4 | Coordinated restore (typed confirm `RESTORE-THIS-BACKUP` → `restorecoord.Coordinator`, same as `/admin/backups/:id/restore`) | `POST /dr/backups/:id/restore`. | COMPLETE |
| 3.8.5 | Operation history / job status | `GET /dr/operations`, `GET /dr/operations/:job_id` (reads same restorecoord result). | COMPLETE |

### 3.9 Reliability (`reliability`) & Health (`health`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 3.9.1 | Backups list/create/now/validate/restore/retention/delete/download | `features/platform/reliability/api.ts` → `/admin/backups*` routes → `internal/backup` service. Restore/delete typed-confirm; restore is durable (`restore-jobs/:job_id` polled). Tests: `BackupsPanel.test.tsx`, `backups_test.go`, `restore_jobs_test.go`. | COMPLETE |
| 3.9.2 | **Backup schedule mutation** | `POST /admin/backups/schedule` → `SetBackupSchedule` (real persistence, `enabled/frequency/retentionCount`). Backend verified COMPLETE; **no component calls it** (prior MISSING_UI — panel read-only). | UI_GAP (backend COMPLETE) |
| 3.9.3 | Single backup fetch | `GET /admin/backups/:id` — real route; list view already has all fields. | INTENTIONAL_READ_ONLY (no UI needed) |
| 3.9.4 | Update status/check/preflight/run/history/changelog | `/update/status`, `/update/check` (GET+POST), `/update/preflight`, `/update/run` (typed confirm + real single-flight `svc.IsRunning`), `/update/history` (envelope `{history}` — frontend unwraps), `/updates/changelog`. Tests: `UpdatesPanel.test.tsx`, `update_test.go`. | COMPLETE |
| 3.9.5 | Monitoring alerts/capacity/snapshot/providers/alert-deliveries + resolve | `/monitoring/*` routes (resolve = CSRF + typed action). Tests: `MonitoringPanel.test.tsx`. | COMPLETE |
| 3.9.6 | Storage volumes (real statfs) | `GET /admin/storage/volumes` → `ListStorageVolumes`. | COMPLETE |
| 3.9.7 | Cluster status (honest single-node static) | `GET /admin/cluster/status` → `deployment_mode:"single_node"` + honest note. | INTENTIONAL_READ_ONLY |
| 3.9.8 | Protocol settings (10 protocol IDs, hot_applied/pending_restart from real bridge) | `GET/PATCH /admin/settings/protocol/:protocol` → `settingsBridge.Apply` post-commit split. | COMPLETE |
| 3.9.9 | Health page | `components/SystemHealth.tsx` → **`fetch("/api/v1/monitoring/health")` DIRECTLY** (bypasses shared client; real data, platform route). Known pre-existing defect (flagged in prior matrix as READ_ONLY_STATUS w/ direct-fetch defect). | DIRECT_FETCH_BYPASS (frontend agent scope) |
| 3.9.10 | Metrics (Prometheus text) | `GET /metrics` (platformMW, `cfg.Metrics.Enabled`) — external link in MonitoringPanel; appropriate for exposition format. | COMPLETE |
| 3.9.11 | Legacy `/backups*` routes | 410 `LegacyGone`. | DEPRECATED |
| 3.9.12 | `POST /updates/apply/:module` no-op stub | Audit entry + `{"status":"update initiated"}`; superseded by `POST /update/run`. **Never wire a UI action to this stub.** | DEPRECATED (stub retired from interactive use) |
| 3.9.13 | `GET /updates/check` legacy flat shape | Superseded by `GET /update/check`. | DUPLICATE_SUPERSEDED |
| 3.9.14 | Signed update artifacts (verify/stage/apply/rollback/history/status) | `/updates/artifacts*` + `/updates/operations/:job_id` — ed25519 verification, typed confirms, external-coordinator handoff, fails closed 503 when coordinator absent. Backend COMPLETE; no console UI (intentionally operator/API-driven — signed artifact upload is a release-engineering action, not an admin form). | INTENTIONAL_MACHINE_ONLY (apply/rollback remain operator-gated via typed confirm; upload is release tooling) |

## 4. Mail Control

### 4.1 Domains (`platform-domains`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 4.1.1 | List (tenant selector + search/status filters) | `features/platform/domains/` → `GET /platform/domains/:tenant_id` → `internal/platform/mailcontrol` service → production `internal/admin/domain` service + `coremail_domains`/DKIM repo. RBAC: platformMW + `PermDomainsRead`; explicit `tenant_id` in path; tenant predicate in SQL. | COMPLETE |
| 4.1.2 | Detail (counts, mail-access mode) | `GET /platform/domains/:tenant_id/:id`. | COMPLETE |
| 4.1.3 | Create (transactional provisioning, plan/limit enforcement, optional DKIM, DNS requirements via dnsops, outbox evidence; Idempotency-Key required) | `POST /platform/domains/:tenant_id` (`PermDomainsWrite`). Tests: `platform_provisioning_acceptance_test.go`. | COMPLETE |
| 4.1.4 | Status transition / mail-access mode | `POST .../status`, `POST .../mail-access-mode` (audited, tenant-scoped). | COMPLETE |
| 4.1.5 | Deactivate (canonical soft-delete lifecycle; typed confirm, optimistic concurrency, dependency checks; Idempotency-Key) | `POST /platform/domains/:tenant_id/:id/deactivate` (`PermPlatformDomainsDeactivate`). | COMPLETE |
| 4.1.6 | Delete (PERMANENT tombstone; requires prior deactivate; blocks on dependencies; purges active DKIM config preserving history; never mutates public DNS; Idempotency-Key) | `POST /platform/domains/:tenant_id/:id/delete` (`PermPlatformDomainsDelete`). Tests: `platform_domain_lifecycle_acceptance_test.go`. | COMPLETE |
| 4.1.7 | DNS snapshot + live DNS verify (read-only; per-record Matched/Mismatch/Missing; never generates keys; DKIM compared against CURRENT configured key) | `GET .../dns`, `POST .../dns/verify` (read perm; auto-trigger + "Re-check DNS" in `DomainDetailDrawer.tsx`). Tests: `platform_dns_verify_test.go`, `dnsops/verifier_test.go`. | COMPLETE |
| 4.1.8 | DKIM generate / rotate (version-guarded, Idempotency-Key, rotation typed confirm `rotate-dkim-key`; **private keys never exposed**) | `POST .../dkim/generate`, `POST .../dkim/rotate` (`PermDomainsWrite`). | COMPLETE |
| 4.1.9 | **DKIM revoke** | **BACKEND_GAP before this pass**: org portal has `POST /domains/:id/dkim/revoke` but the platform surface has no revoke route. **Fixed in this pass** — see Remediation R-4 (`POST /platform/domains/:tenant_id/:id/dkim/revoke`). | BACKEND_GAP → **FIXED (R-4)**; UI_GAP for frontend agent |
| 4.1.10 | Audit/history + version/conflict UX | Domain lifecycle mutations carry audit/outbox evidence + optimistic-concurrency (`expected_version`) where the contract defines it; history surfaced via audit log + DKIM history on org side. | COMPLETE |

### 4.2 Mailboxes (`platform-mailboxes`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 4.2.1 | List (tenant/domain filters, search, pagination) | `features/platform/mailboxes/` → `GET /platform/mailboxes/:tenant_id` (`PermMailboxesRead`). | COMPLETE |
| 4.2.2 | Detail (configured + effective mail-access mode) | `GET /platform/mailboxes/:tenant_id/:id`. | COMPLETE |
| 4.2.3 | Create (REQUIRED explicit `mail_access_mode`, Argon2id via canonical hasher, in-transaction folder provisioning, audit/outbox, **password never returned**, Cache-Control: no-store, Idempotency-Key) | `POST /platform/mailboxes/:tenant_id` (`PermMailboxesWrite`). Tests: `platform_provisioning_acceptance_test.go`. | COMPLETE |
| 4.2.4 | Access mode (expected_version optimistic concurrency) / status / quota (domain-bound ceiling) | `POST .../access-mode`, `POST .../status`, `POST .../quota` (all audited). | COMPLETE |
| 4.2.5 | Password reset (secure one-time via production service, audited; **no stored password ever displayed**) | `POST /platform/mailboxes/:tenant_id/:id/reset-password`. | COMPLETE |
| 4.2.6 | Safe delete (soft-delete, typed confirm `PURGE-MAILBOX-<id>`) | `DELETE /platform/mailboxes/:tenant_id/:id`. | COMPLETE |
| 4.2.7 | Bulk status (max 500 ids, tenant-scoped, audited) | `POST /platform/mailboxes/:tenant_id/bulk/status`. | COMPLETE |
| 4.2.8 | Support view (audited, read-only, time-boxed session: start/folders/messages/message/attachment/end; typed confirm `ACCESS-MAILBOX-<id>`, ticket+reason required, 30m default/60m max, **password never read**, no reusable token) | `POST .../support-view` + session routes (`PermPlatformMailboxSupportView`). Tests: `platform_mailbox_support_view_test.go`. Attachment download not yet exercised by test. | COMPLETE (attachment download: MISSING_TEST — backend handler + service real) |
| 4.2.9 | Audit/history for mailbox operations | Mutations audited via orvix_audit; mailbox-level audit history exists on org side (`GET /mailboxes/:id/audit`); platform detail shows state, history surfaced via Audit Log page. | COMPLETE |

### 4.3 Aliases (`platform-aliases`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 4.3.1 | List/detail (tenant-scoped) | `features/platform/aliases/` → `GET /platform/aliases/:tenant_id`, `GET .../:id` (`PermAliasesRead`). | COMPLETE |
| 4.3.2 | Create (domain ownership check, loop rejection, conflict detection, audited) | `POST /platform/aliases/:tenant_id` (`PermAliasesWrite`, CSRF). | COMPLETE |
| 4.3.3 | Delete (soft-delete, tenant-scoped, audited) | `DELETE /platform/aliases/:tenant_id/:id`. | COMPLETE |

### 4.4 Groups (`platform-groups`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 4.4.1 | List/detail/members (read) | `features/platform/groups/` → `GET /platform/groups/:tenant_id`, `GET .../:id`, `GET .../:id/members` — `coremail_groups`/`coremail_group_members` tables, tenant predicate in SQL (`PermGroupsRead`). | COMPLETE |
| 4.4.2 | **Create group** | **BACKEND_GAP before this pass** (read-only platform surface; org portal has CRUD via `customer_mail.go` handlers). **Fixed in this pass** — see Remediation R-2 (`POST /platform/groups/:tenant_id`). | BACKEND_GAP → **FIXED (R-2)**; UI_GAP for frontend agent |
| 4.4.3 | **Delete group (soft-delete)** | **Fixed in this pass** — R-2 (`DELETE /platform/groups/:tenant_id/:id`, typed confirm). | BACKEND_GAP → **FIXED (R-2)** |
| 4.4.4 | **Add/remove group member** | **Fixed in this pass** — R-2 (`POST .../members`, `DELETE .../members/:member_id`; member rows keyed to group in SQL). | BACKEND_GAP → **FIXED (R-2)** |

### 4.5 Relays (`platform-relays`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 4.5.1 | List/detail (redacted; last safe test outcome) | `features/platform/relay/` → `GET /platform/relays`, `GET /platform/relays/:id` (`PermRelaysRead`) → `internal/platform/relay` admin service; **credentials encrypted at rest, never returned**. | COMPLETE |
| 4.5.2 | Create (credential encrypted at rest; idempotent) / update (version-guarded, idempotent) | `POST /platform/relays`, `PATCH /platform/relays/:id` (`PermRelaysWrite`, CSRF, Idempotency-Key). | COMPLETE |
| 4.5.3 | Enable / disable (disable = typed confirm + version + idempotency, audited) / rotate-credentials (generated once if not supplied; typed confirm) | `POST .../enable`, `POST .../disable`, `POST .../rotate-credentials`. | COMPLETE |
| 4.5.4 | **Test connectivity** | `POST /platform/relays/:id/test` (`PermRelaysTest`) — SSRF/DNS-rebinding-safe, bounded timeouts, **real result only; no "test successful" simulation** (`relay/admin_test.go` covers fail-closed + timeouts). | COMPLETE |
| 4.5.5 | Delete (typed confirm, audited) | `DELETE /platform/relays/:id`. | COMPLETE |

## 5. Security

### 5.1 Audit Log (`platform-audit`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 5.1.1 | Audit list with filters (action/actor/tenant/result) + pagination | `features/platform/audit/api.ts listAuditLogs` expects **`{entries, total}`** from `GET /audit/logs`. **CONTRACT_DRIFT before this pass**: handler returned a bare array (hard-coded limit 100, no filters, no total). **Fixed in this pass** — see Remediation R-5. | CONTRACT_DRIFT → **FIXED (R-5)** |
| 5.1.2 | Audit entry detail | `GET /audit/logs/:id` → `GetAuditEntry` → extended store; returns full ExtendedEntry (actor/actor_id/actor_role/tenant_id/action/target/result/reason/before/after/ip/user_agent/timestamp). | COMPLETE |
| 5.1.3 | Audit export (JSON/CSV with filters) | `GET /audit/logs/export` → `ExportAuditLogs` streams real filtered entries. Frontend `useAuditExport` exists but no button wired (no file download plumbing). | UI_GAP (backend COMPLETE) |
| 5.1.4 | Audit log ingestion | Real extended audit store (`orvix_audit` table) written transactionally by every mutation service (org, mailbox, domain, alias, suppression, billing adjustment, support access, etc.). | COMPLETE |

### 5.2 Support Access (`support-access`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 5.2.1 | Grant list/detail (by tenant) | `features/platform/support-access/` → `GET /platform/support/grants`, `GET .../:id` → `internal/supportaccess` service/repo (`support_access_grants`). RBAC: platformMW. | COMPLETE |
| 5.2.2 | Request grant (ticket ref, reason, scope, target tenant, expiry) | `POST /platform/support/grants` (CSRF). | COMPLETE |
| 5.2.3 | Activate / revoke (hard expiry, revoke reason, audited; **no customer JWT impersonation — the operator's own session is never touched; no password backdoor**) | `POST .../activate`, `POST .../revoke`. Scope model: read_only/mailbox_view/domain_view/full_tenant_view; enforcement middleware binds request-local tenant context. | COMPLETE |
| 5.2.4 | Mailbox support viewer | `SupportMailboxViewer.tsx` (session-based read-only viewer; end-session route idempotent, audited). | COMPLETE |
| 5.2.5 | Immutable operator identity | Grant rows record `granted_by_id`; sessions bind operator to mailbox; audit records actor on every session action. | COMPLETE |

### 5.3 Security (`platform-security`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 5.3.1 | SSL certificates (list/reload/expiry-warnings/upload/delete; ACME honest static `enabled:false`) | `features/platform/security/` → `/admin/ssl/*` (mutations CSRF + typed confirm for delete; upload secret never retained/redisplayed). Tests: `SslPanel.test.tsx`, `ssl_certificate_test.go`. | COMPLETE |
| 5.3.2 | Antivirus status (real engine policy — no AI-classifier claims) | `GET /admin/security/antivirus` → engine `Policy()/RuntimeEnforced()/LastError()` from the runtime module. | COMPLETE |
| 5.3.3 | Firewall rules/logs | `GET /firewall/rules`, `GET /firewall/logs` (real stored rows). **`POST /firewall/rules` fails closed 410 `FIREWALL_RULE_ENGINE_NOT_OPERATIONAL`, inserts nothing** — no production mail path consults `firewall_rules`; UI correctly shows no create control (legacy-not-enforced label). | DEPRECATED (create retired; reads remain for forensics) |
| 5.3.4 | Guardian logs + analyze | `GET /guardian/logs` (UI_SUPPORTED); `POST /guardian/analyze` takes raw email content — machine-only, no operator form. | COMPLETE / INTENTIONAL_MACHINE_ONLY (analyze) |
| 5.3.5 | Self-heal (history + run check) | `GET /heal/history`, `POST /heal/check/:name` (CSRF). | COMPLETE |
| 5.3.6 | Log rules (list/create/delete; delete typed confirm) | `/admin/log-rules*` (platformMW). | COMPLETE |
| 5.3.7 | MFA (platform duplicate of self-scoped `/account/mfa/*`) | `/admin/mfa/*` registered but superseded by `/account/mfa/*` used by the shared Account/Security page. | DUPLICATE_SUPERSEDED |
| 5.3.8 | `GET /admin/fs/browse` + `GET /admin/fs/read` | Raw filesystem traversal — intentionally NOT surfaced as UI (blast-radius containment). | INTENTIONAL_MACHINE_ONLY |
| 5.3.9 | Monitoring alert resolve | `POST /monitoring/alerts/:id/resolve` (CSRF). | COMPLETE |

## 6. System

### 6.1 Modules (`modules`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 6.1.1 | Module list (id/version/status) | `components/Modules.tsx` → `GET /modules` → `ListModules` reads the **runtime module registry** (registered modules + health), not static config. Pre-existing page, no tests. | COMPLETE (MISSING_TEST — add backend test pinning runtime-truth read) |

### 6.2 Configuration (`platform-configuration`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 6.2.1 | Settings read/patch (sectioned typed response; nested diff-only patch; bridge decides hot_applied vs pending_restart) | `features/platform/configuration/` → `GET/PATCH /admin/settings` → settings store + bridge. Tests: `SettingsPanel.test.tsx`, `admin_settings_test.go`. | COMPLETE |
| 6.2.2 | Feature flags list/toggle | `GET /feature-flags`, `PUT /feature-flags/:id` (CSRF) — real persisted flag rows. | COMPLETE |

### 6.3 Config Truth (`platform-config-truth`)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 6.3.1 | Config truth list (source/effective/pending state) | `features/platform/config-truth/` → `GET /platform/config` → `internal/configtruth` service/repo — configured vs persisted vs effective runtime + drift, **no secrets in responses** (config truth service redacts secret-typed keys). | COMPLETE |
| 6.3.2 | Single setting detail | `GET /platform/config/:key`. | COMPLETE |
| 6.3.3 | Mutate setting (validate + apply with optimistic concurrency) | `PATCH /platform/config/:key` (CSRF) — `expected_version` guard; hot-applied vs restart-required recorded. | COMPLETE |
| 6.3.4 | Runtime telemetry | `GET /admin/runtime` (listener/runtime snapshot) — real route, no frontend consumer (overlaps Health). | UI_GAP (backend COMPLETE) |

## 7. Account (shared pages, self-scoped)

| # | Feature/action | Trace | Disposition |
|---|---|---|---|
| 7.1 | Account profile (get/update) | `components/AccountSettingsPage.tsx` → `GET/PATCH /account/profile` (self-scoped, tenant-agnostic). | COMPLETE |
| 7.2 | Security (MFA status/setup/verify/disable/recovery regenerate; sessions list/revoke; change password) | `components/SecurityPage.tsx` → `/account/mfa/*`, `/account/sessions*`, `/auth/change-password` (CSRF on mutations; MFA rate-limited 10/15min). | COMPLETE |
| 7.3 | Preferences (get/update + notification prefs) | `components/PreferencesPage.tsx` → `/account/preferences`, `/account/notification-preferences`. | COMPLETE |

## 8. Platform routes not in any visible page (all 231 registered routes dispositioned)

The full route-level disposition (231 routes, every one classified) is
the canonical `docs/deployment/platform-console-capability-matrix.md`,
enforced by `internal/api/capability_matrix_test.go` against the actual
`platformMW[0], platformMW[1]` registrations in `router.go`. This pass
**adds 7 new platformMW routes** (total becomes 231) and re-dispositions
them in that document. The parity-matrix taxonomy uses `UI_GAP
(backend COMPLETE)` for the capability matrix's `MISSING_UI` class —
they are the same thing, named differently per document. The summary
below uses the enforced capability-matrix counts:

| Disposition | Before | After (this pass) |
|---|---|---|
| UI_SUPPORTED | 66 | 66 |
| READ_ONLY_STATUS | 5 | 5 |
| MACHINE_ONLY | 3 | 3 |
| DEPRECATED | 12 | 12 |
| DUPLICATE_SUPERSEDED_ROUTE | 18 | 18 |
| MISSING_UI (== parity-taxonomy UI_GAP rows) | 120 | 127 (7 new backend-COMPLETE routes: `POST /platform/organizations`, groups CRUD ×4, `GET /platform/billing/tenants/:tenant_id/overview`, `POST /platform/domains/:tenant_id/:id/dkim/revoke`) |
| MISSING_BACKEND | 0 (1 non-route gap) | 0 (gap closed via R-1) |
| **Total** | **224** | **231** |

## 9. Remediation log (backend fixes in this pass)

| ID | Area | Fix | Files |
|---|---|---|---|
| R-1 | Organizations | `POST /platform/organizations` — PSA org creation with real invitation/activation model (owner email REQUIRED; tenant + subscription + tenant_admin invitation in ONE transaction; no invented owner; no ownerless active org; audit; idempotency-keyed) | `internal/admin/platform/service.go`, `internal/admin/organization/{service,repository}.go`, `internal/api/handlers/platform_admin.go`, `internal/api/router.go`, tests |
| R-2 | Groups | Platform groups CRUD: `POST /platform/groups/:tenant_id`, `DELETE .../:id`, `POST .../members`, `DELETE .../members/:member_id` — same `coremail_groups`/`coremail_group_members` tables, tenant predicate in SQL, audited | `internal/platform/mailcontrol/{repository,service}.go`, `internal/api/handlers/platform_mail_control.go`, `internal/api/router.go`, tests |
| R-3 | Platform Billing | `GET /platform/billing/tenants/:tenant_id/overview` — subscription/plan/period/usage/invoices/balance/ledger/reconciliation + honest provider state | `internal/api/handlers/platform_billing_admin.go`, `internal/api/router.go`, tests |
| R-4 | Domains | Platform DKIM revoke: `POST /platform/domains/:tenant_id/:id/dkim/revoke` (mirrors org-side revoke; audited; version-guarded) | `internal/platform/mailcontrol/{repository,service}.go` (if needed), `internal/api/handlers/platform_mail_control.go`, `internal/api/router.go`, tests |
| R-5 | Audit Log | `GET /audit/logs` contract fix: returns `{entries, total}` with action/actor/tenant_id/result filters + limit/offset pagination (extended entries) | `internal/api/handlers/handlers.go`, tests |

## 10. Phase 2 addendum (API Platform Engineer) — contract completion

Executed after Phase 1 (backend) on the same branch. Starting SHA:
`0c9ad30ff801bb9d762036445338aafebf310d84`. Scope: normalize the new/
changed contracts, OpenAPI spec, capability matrix, and contract tests.

### 10.1 Final CONTRACT_DRIFT disposition (Phase 2)

| ID | Drift found (proved from source) | Fix (Phase 2) | Status |
|---|---|---|---|
| R-5 (audit list) | `GET /audit/logs` returned a bare array (fixed in Phase 1) | Verified in source: `{entries, total, limit, offset}` envelope + filters + page/page_size aliases + legacy-store fallback with the same envelope; pinned by `platform_audit_contract_acceptance_test.go` and the new `TestAuditLogList_ResultFilterLimitClampAndDetailShape` | RESOLVED (backend Phase 1 + contract pins Phase 2) |
| D-1 (org create) | `POST /platform/organizations` one-time invite-token response had no `Cache-Control: no-store` — the platform convention for one-time-secret responses | Handler now sets no-store before the idempotency wrapper, so live AND replay responses carry it; pinned by `TestPlatformCreateOrganization_OneTimeTokenNeverCached` | FIXED (Phase 2) |
| D-2 (org create) | Second PSA org with an empty/duplicate `domain` failed on the tenants `UNIQUE(domain)` constraint but surfaced as the misleading CONFLICT "an organization with this slug already exists" | Added `ExistsByDomain` pre-check + `ErrOrganizationDomainExists` → truthful 409 CONFLICT "an organization with this domain already exists" (slug conflicts keep their message); pinned by the new duplicate-domain assertion | FIXED (Phase 2) |
| D-3 (invitation resend) | `POST /enterprise/invitations/:id/resend` non-pending rejection returned the raw service error with no stable code; one-time token response had no no-store | Stable codes: `VALIDATION_FAILED` (bad id) / `NOT_FOUND` (missing) / `INVALID_STATE_TRANSITION` (non-pending); no-store on the token response; pinned by `TestInvitationResend_ErrorShapeAndNoStore` | FIXED (Phase 2) |
| D-4 (audit detail) | `GET /audit/logs/:id` invalid-id rejection lacked a stable code | Added `code: VALIDATION_FAILED`; pinned by the new audit test | FIXED (Phase 2) |

Verified consistent (no drift found): groups CRUD confirmation/error
shapes (428 `PRECONDITION_FAILED`, 409 `CONFLICT`, 400
`VALIDATION_FAILED`, 404 `NOT_FOUND`), DKIM revoke response + 409
`CONFLICT`, billing overview/state envelopes (non-null arrays, honest
`payment_provider`), idempotency replay semantics
(`X-Idempotency-Replay`, token-free stored body, 409
`IDEMPOTENCY_KEY_REUSE_MISMATCH` on key reuse with a different body).

### 10.2 Deliverables completed (Phase 2)

- OpenAPI spec (`docs/api/openapi.yaml`) documents every new/changed
  route with request/response schemas, error codes, and header
  conventions; local gate (`npx @redocly/cli@1.25.0 lint` on both specs
  + `go test ./internal/api -run TestPublicRouterMatchesOpenAPI`) green.
- Capability matrix updated (231 routes; this section's counts table).
- Contract-shape tests added (`platform_contract_shape_acceptance_test.go`).

### 10.3 Blocker reviews (Phase 2 API Platform, second pass)

Starting SHA: `29d7877e0e5dd1986576d215862cd2c12ba8f4ec`. Three
mission-required deep audits were run against source; two required
backend fixes, one required an idempotency correction.

| ID | Blocker finding (proved from source) | Fix (this pass) | Status |
|---|---|---|---|
| B-A1 (org lifecycle) | `POST /platform/organizations` created the tenant row with `tenants.active=1` BEFORE any owner user existed — an ownerless ACTIVE organization, violating the owner-required lifecycle | PSA-created orgs now start `pending_activation` (`active=0`) with a REQUIRED pending tenant_admin owner invitation; the org becomes operational ONLY when the owner redeems the invitation via the new public `POST /auth/invitations/accept` (user created as tenant_admin + org activated in ONE transaction, honoring open suspensions). `GET /platform/organizations/:id/detail` reports `status_label: pending_activation` | FIXED |
| B-A2 (no accept path) | `AcceptInvitation` existed in the service but was DEAD CODE — no route could redeem the one-time token, so the owner could never activate | New public route `POST /auth/invitations/accept` (throttled per-IP like other credential endpoints): token + password → user created (email from invitation row only), invitation atomically claimed (`WHERE status='pending'`), org activated, audited. Stable codes: 404 `NOT_FOUND` (unknown token), 409 `INVALID_STATE_TRANSITION` (revoked/expired/already-used), 409 `CONFLICT` (existing account / platform identity) | FIXED |
| B-A3 (activation guard) | `POST /platform/organizations/:id/active` let the PSA force-activate an ownerless org, re-creating the violation through the back door | `SetOrganizationActive` now refuses `active=true` when the org has zero active administrators → 409 `CONFLICT` (`ErrOrganizationRequiresOwner`); disabling stays allowed | FIXED |
| B-A4 (duplicate invites) | `org_invitations` had no unique constraint and `POST /enterprise/invitations` had no pre-check — two pending invitations for one email meant two live tokens, one silently orphaned on redemption | `ExistsPendingInvitation` pre-check in the service + stable 409 `CONFLICT` "a pending invitation already exists for this email" (resend rotates the token for a fresh share instead) | FIXED |
| B-B1 (audit export) | `GET /audit/logs/export` read the LEGACY `coremail_audit` store while list/detail read the canonical `orvix_audit` — exports disagreed with the platform audit page | `ExportAuditLogs` now reads the extended store (`ExtendedStore.ExportTo`, JSON/CSV with the same rich fields); legacy exporter remains only as a fallback | FIXED |
| B-B2 (tenant audit page) | `GET /enterprise/audit/logs` read the legacy store and returned a bare array — the tenant page saw different actions than the platform page, under a second incompatible contract | Now reads the canonical `orvix_audit`, tenant-scoped, with the SAME `{entries, total, limit, offset}` envelope and rich entry fields as `GET /audit/logs`; filters action/actor/target/result/since/until + limit/offset/page. Frontend `api.listAuditLogs` unwraps `.entries` for compatibility | FIXED |
| B-B3 (dual-write agreement) | `writeAuditLog`'s legacy-side write omitted `tenant_id` and `role`, so the two stores disagreed on every handler-level action | Legacy mirror now carries role + tenant_id (fields agree with the canonical extended write); extended write additionally populates `request_id` from `X-Request-ID`/`X-Correlation-ID`. Canonical source: `orvix_audit`; `coremail_audit` = legacy compatibility mirror | FIXED |
| B-C1 (DKIM revoke idempotency) | Repeat `POST /platform/domains/:tenant_id/:id/dkim/revoke` re-wrote the selector history and audit rows although the documented contract promised a no-op success | `RevokeDKIM` now commits without mutation when the config is already disabled — a repeat revoke is a true no-op success, never a duplicate history/audit record | FIXED |

Verified SAFE (no change required): DKIM revoke never exposes key
material, never generates a new key, never rotates the selector, never
mutates public DNS, is tenant-scoped (cross-tenant → 404), and its
`{status, domain_id, revoked}` response states the real resulting state.
Billing overview/state expose only real data with honest
`payment_provider` state (no MRR/cards/paid-invoice fabrication, integer
minor units). Route ownership holds: `platformMW` (PSA + CSRF) on every
`/platform/*` route, `enterpriseRead` (tenant family + tenant context) on
every `/enterprise/*` route, RoleUser denied everywhere administrative,
and the new accept route is public by design (the token is the
credential).

## 11. Frontend handoff notes (for Frontend Developer)

1. **Organizations**: wire create-org form → `POST /platform/organizations` (R-1). Owner email required; response includes the one-time invitation token — show it once with copy button.
2. **Platform Billing**: render overview card from R-3; keep "provider not configured" honesty; never show cards/MRR.
3. **Groups**: wire create/delete/add-member/remove-member against R-2 routes; delete requires typed confirm.
4. **Domains**: wire DKIM revoke (R-4) next to generate/rotate.
5. **Audit Log**: current page already expects the fixed `{entries,total}` shape; wire export button (blob download) for `GET /audit/logs/export`.
6. **Mail Queue**: add History/Export/Bulk Action UI against existing routes (backend complete).
7. **Backups**: add schedule edit UI against existing `POST /admin/backups/schedule`.
8. **SystemHealth.tsx**: replace direct `fetch("/api/v1/monitoring/health")` with the shared client (DIRECT_FETCH_BYPASS).
9. Do NOT wire: `POST /firewall/rules` (410 by design), `POST /updates/apply/:module` (stub — use `/update/run`), `/admin/fs/*` (machine-only), `POST /guardian/analyze` (machine-only), signed artifact apply/rollback (operator/machine-only, external coordinator required).
