# Platform Console Capability Matrix

Complete audit of every route gated with `platformMW[0], platformMW[1]`
in `internal/api/router.go` (128 route registrations, verified by
`internal/api/capability_matrix_test.go` against the branch head this
document was written against — updated for the Milestone 13-15 DR,
retention, platform billing, and signed-update-artifact routes, plus
three pre-existing queue routes found undocumented during that pass,
plus the DR operation-history and platform-billing reconciliation
routes added during the M13-15 re-audit gap-closure pass).
Every disposition below reflects the actual handler and actual
frontend consumer, not the route's name.

## Disposition taxonomy

- **UI_SUPPORTED** — a real frontend page/action calls this route, tested.
- **READ_ONLY_STATUS** — a real frontend page displays this route's data; no mutation exists for it (by backend design, not omission).
- **MACHINE_ONLY** — the request shape makes an operator form nonsensical (e.g. takes raw email content).
- **INTERNAL_DEPENDENCY** — not applicable to any route in this matrix (reserved for vendor/infra dependencies; none found among these 100).
- **DEPRECATED** — the handler itself is a stub/gone-marker (`LegacyGone`) or the whole feature is retired.
- **DUPLICATE_SUPERSEDED_ROUTE** — a newer route/page covers the same capability; this one is intentionally not wired.
- **MISSING_UI** — a real, working backend route with no frontend consumer (a UI gap this PR did not close).
- **MISSING_BACKEND** — the frontend needs a capability that has no registered route.

## Overview

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /platform/dashboard` | platformMW | `PlatformDashboard` | `overview/contract.ts`'s `PlatformDashboard` | Platform | `overview/page.test.tsx` | UI_SUPPORTED |

## Organizations

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /platform/organizations` | platformMW | `ListPlatformOrganizations` | `organizations/contract.ts`'s `ListOrganizationsResponse` | Platform | `organizations/page.test.tsx` | UI_SUPPORTED |
| `GET /platform/organizations/:id/detail` | platformMW | `GetOrganizationDetail` | `OrganizationDetail` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `POST /platform/organizations/:id/active` | platformMW | `SetOrganizationActive` | `SetOrganizationActiveRequest/Response` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `PATCH /platform/organizations/:id` | platformMW | `UpdateOrganization` | `UpdateOrganizationRequest/Response` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `GET /platform/organizations/:id` | platformMW | `GetPlatformOrganization` | untyped `map[string]interface{}` from a different service (`platformAdminSvc`) than the typed `/detail` route above | Platform | `page.test.tsx` (regression: proves the client has no function for this route at all) | DUPLICATE_SUPERSEDED_ROUTE |

MISSING_BACKEND (not a route, so not counted in the 100): the console
has no way for a Platform Super Admin to create a new organization —
`web/admin/src/features/platform/organizations/api.ts` has no create
function, and no `POST /platform/organizations` (or equivalent)
route is registered anywhere in `router.go`. Organizations are
currently created only via tenant self-signup, never PSA-initiated.
Not fixed in this pass — flagged, not silently omitted.

## Mail Operations

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /admin/queue/summary` | platformMW | `AdminQueueSummary` | `QueueSummaryResponse` | Platform | `mail-operations/page.test.tsx` | UI_SUPPORTED |
| `GET /admin/queue/messages` | platformMW | `AdminQueueList` | `ListQueueMessagesResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `GET /admin/queue/messages/:id` | platformMW | `AdminQueueDetail` | `QueueDetailResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `POST /admin/queue/messages/:id/retry` | platformMW | `AdminQueueRetryNow` | `QueueActionResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `POST /admin/queue/messages/:id/bounce` | platformMW | `AdminQueueBounce` | `QueueActionResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `POST /admin/queue/messages/:id/cancel` | platformMW | `AdminQueueCancel` | `QueueActionResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `GET /admin/queue/:id` | platformMW | `GetAdminQueueEntry` | diagnostic single-entry view, superseded by `AdminQueueDetail` | Platform | — | DUPLICATE_SUPERSEDED_ROUTE |
| `GET /queue` | platformMW | `ListQueue` | legacy webmail-facing queue list (different schema than `AdminQueueList` — see the earlier `COREMAIL_DISABLED` fix commit) | Platform (webmail SPA is the real consumer, not this admin console) | `cmd/orvix/fullstack_repro_test.go` (backend) | DUPLICATE_SUPERSEDED_ROUTE (superseded, for this admin console, by `AdminQueueList`; still load-bearing for the separate webmail frontend, so not removable here) |
| `DELETE /queue/:id` | platformMW | `DeleteQueue` | legacy queue delete | Platform | — | DUPLICATE_SUPERSEDED_ROUTE (superseded by `AdminQueueCancel`) |
| `POST /queue/:id/retry` | platformMW | `RetryQueue` | legacy queue retry | Platform | — | DUPLICATE_SUPERSEDED_ROUTE (superseded by `AdminQueueRetryNow`) |
| `GET /admin/queue/history` | platformMW | `AdminQueueHistory` | queue history | Platform | — | MISSING_UI (real route, no frontend consumer found) |
| `GET /admin/queue/export` | platformMW | `AdminQueueExport` | queue export | Platform | — | MISSING_UI (real route, no frontend consumer found) |
| `POST /admin/queue/messages/bulk-action` | platformMW | `AdminQueueBulkAction` | bulk queue action | Platform | — | MISSING_UI (real route, no frontend consumer found) |

All six `UI_SUPPORTED` mail-operations endpoints share the
`COREMAIL_DISABLED` sanitized-503 contract, verified in
`internal/api/handlers/admin_queue_coremail_state_test.go` (backend)
and `isCoreMailDisabled` (frontend).

## Reliability

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /admin/backups` | platformMW | `ListBackups` | `Backup[]` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups` | platformMW | `CreateBackup` | `Backup` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups/now` | platformMW | `PostBackupNow` | `Backup` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/backups/schedule` | platformMW | `GetBackupSchedule` | `BackupSchedule` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups/schedule` | platformMW | `SetBackupSchedule` | `BackupSchedule` (enabled/frequency/retentionCount) | Platform | — | MISSING_UI (a `setBackupSchedule` client function exists in `reliability/api.ts` but no component calls it — the panel shows the schedule read-only; found during this audit, not fixed in this PR) |
| `GET /admin/backups/metrics` | platformMW | `GetBackupMetrics` | `BackupMetrics` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/backups/health` | platformMW | `GetBackupHealth` | `BackupHealth` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups/retention` | platformMW | `RunBackupRetention` | `{deleted?}` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups/:id/validate` | platformMW | `PostValidateBackup` | `{status}` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups/:id/restore` | platformMW | `PostRestoreBackup` | `RestoreJobSubmitResponse`, typed confirm `restore-orvix-backup` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `DELETE /admin/backups/:id` | platformMW | `DeleteBackup` | destructive, typed confirm `delete-orvix-backup` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/backups/restore-jobs/:job_id` | platformMW | `GetRestoreJobStatus` | `RestoreJobResult`, polled | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/backups/:id/download` | platformMW | `DownloadBackup` | binary archive stream | Platform | `BackupsPanel` (`downloadBackupUrl` used as a plain `<a href>`, not a fetch — not independently unit-testable) | UI_SUPPORTED |
| `GET /admin/backups/:id` | platformMW | `GetBackup` | single backup metadata | Platform | — | MISSING_UI (real route; the panel lists all backups but never fetches one individually — the table row already has everything `Backup` provides) |
| `GET /backups`, `GET /backups/schedule`, `GET /backups/metrics`, `GET /backups/health`, `GET /backups/:id/download`, `POST /backups`, `POST /backups/schedule`, `POST /backups/retention`, `DELETE /backups/:id` (9 routes) | platformMW | `LegacyGone` | 410/removed marker | Platform | — | DEPRECATED |
| `GET /update/status` | platformMW | `GetUpdateStatus` | `UpdateStatus` | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `GET /update/check` | platformMW | `GetUpdateCheck` | `UpdateCheckResult` | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `POST /update/check` | platformMW | `PostUpdateCheck` | `UpdateCheckResult` | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `GET /update/history` | platformMW | `GetUpdateHistory` | `{"history": UpdateHistoryRow[]}` — an envelope, unlike its status/check/preflight siblings; a real bug where the frontend cast it straight through as a bare array (crashing every real load) was found and fixed via `test/playwright/portal.spec.ts`'s live-server gap-coverage sweep, `api.ts` unwraps it now | Platform | `UpdatesPanel.test.tsx`, `api.test.ts` | UI_SUPPORTED |
| `GET /update/preflight` | platformMW | `GetUpdatePreflight` | `PreflightResult` | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `POST /update/run` | platformMW | `PostUpdateRun` | typed confirm, real single-flight (`svc.IsRunning`) | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `GET /updates/changelog` | platformMW | `GetChangelog` | `ChangelogEntry[]` (capitalized fields — no json tags) | Platform | `ChangelogPanel.test.tsx` | UI_SUPPORTED |
| `GET /updates/check` | platformMW | `CheckUpdates` | legacy flat `{status,module,version}` shape | Platform | — | DUPLICATE_SUPERSEDED_ROUTE (superseded by `GET /update/check`) |
| `POST /updates/apply/:module` | platformMW | `ApplyUpdate` | **no-op stub**: writes an audit entry and returns `{"status":"update initiated"}` without checking module existence, without single-flight protection, without performing any real update | Platform | — | DUPLICATE_SUPERSEDED_ROUTE (superseded by `POST /update/run`, which has real single-flight + preflight; wiring this stub to a UI action would be a fabricated success state) |
| `GET /monitoring/alerts` | platformMW | `GetMonitoringAlerts` | `{alerts}` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `POST /monitoring/alerts/:id/resolve` | platformMW | `PostMonitoringAlertResolve` | `{status,id}` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/capacity` | platformMW | `GetMonitoringCapacity` | `Capacity` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/snapshot` | platformMW | `GetMonitoringSnapshot` | `MonitoringSnapshot` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/alert-providers` | platformMW | `GetMonitoringProviders` | `MonitoringProvidersResponse` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/alert-deliveries` | platformMW | `ListAlertDeliveries` | `ListAlertDeliveriesResponse` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/health` | platformMW | `GetMonitoringHealth` | `monitoring.Health` | Platform (pre-existing `SystemHealth.tsx`, the "Health" nav item) | none — `SystemHealth.tsx` calls `fetch("/api/v1/monitoring/health")` directly, bypassing the shared client | READ_ONLY_STATUS (real page, real data — but the direct-fetch defect in `SystemHealth.tsx` was NOT fixed in this PR; it predates the six migrated domains and is out of this PR's explicit scope. Flagged, not silently left undocumented.) |
| `GET /metrics` | platformMW | `metrics.Handler()` (Prometheus exposition format) | raw text | Platform | `MonitoringPanel`'s "Open raw metrics" external link | UI_SUPPORTED (external link, not embedded — appropriate for a Prometheus-format payload) |
| `GET /admin/storage/volumes` | platformMW | `ListStorageVolumes` | `ListStorageVolumesResponse` | Platform | `StoragePanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/cluster/status` | platformMW | `AdminClusteringStatus` | `ClusterStatus` — **honestly static**: `deployment_mode:"single_node"`, `honest_note:"Clustering + proxy replication is not implemented in this build."` | Platform | `ClusterPanel.test.tsx` | READ_ONLY_STATUS (this is the current, correct state — not a preview of the future `platform-cluster-control-plane` bounded context, which is unimplemented and out of scope for this PR) |
| `GET /admin/settings/protocol/:protocol`, `PATCH /admin/settings/protocol/:protocol` | platformMW | `ListProtocolSettings` / `PatchProtocolSettings` | `ProtocolSettingsResponse` / flat diff-only PATCH, 10 protocol IDs, per-key semantic validation, single-transaction write, post-commit `settingsBridge.Apply` determines the real `hot_applied`/`pending_restart` split (never a static guess); `coremail.imap_idle_enabled` is `ReadOnly` (no live-config field backs it) | Platform | `ProtocolSettingsPanel.test.tsx`, `protocol_settings_test.go` (10 backend cases against the real bridge/DB) | UI_SUPPORTED |

## Security

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /audit/logs` | platformMW | `ListAuditLogs` | `AuditEntry[]` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `GET /audit/logs/export` | platformMW | `ExportAuditLogs` | CSV/JSON export of filtered audit entries | Platform | `internal/audit/` | MISSING_UI |
| `GET /audit/logs/:id` | platformMW | `GetAuditEntry` | single audit entry by ID | Platform | `internal/audit/` | MISSING_UI |
| `GET /admin/ssl/certificates` | platformMW | `AdminSslListCertificates` | `ListCertificatesResponse` | Platform | `SslPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/ssl/certificates/reload` | platformMW | `AdminSslReloadCertificates` | `{status?}` | Platform | `SslPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/ssl/certificates/reload` | platformMW | `AdminSslReloadCertificates` (same handler, also registered under GET) | `{status?}` | Platform | — | DUPLICATE_SUPERSEDED_ROUTE (the frontend uses the POST registration for this mutating action; the GET registration on the identical path is not called and is unusual for a mutating action to expose under GET — not removed, just unused) |
| `GET /admin/ssl/expiry-warnings` | platformMW | `AdminSslExpiryWarnings` | `{warnings}` | Platform | `SslPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/ssl/acme/status` | platformMW | `AdminSslAcmeStatus` | `AcmeStatus` — honestly static (`acme_enabled:false`, ACME issuance not implemented) | Platform | `SslPanel.test.tsx` | READ_ONLY_STATUS |
| `POST /admin/ssl/certificates` | platformMW | `AdminSslUploadCertificate` | `UploadCertificateRequest/Response`, secret input never retained/redisplayed | Platform | `SslUploadForm.test.tsx` | UI_SUPPORTED |
| `DELETE /admin/ssl/certificates/:id` | platformMW | `AdminSslDeleteCertificate` | destructive, typed confirm | Platform | `SslPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/security/antivirus` | platformMW | `AdminAntivirusStatus` | `AntivirusStatus` | Platform | `AntivirusPanel.test.tsx` | UI_SUPPORTED |
| `GET /firewall/rules` | platformMW | `ListFirewallRules` | `FirewallRule[]` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `GET /firewall/logs` | platformMW | `ListFirewallLogs` | `FirewallLog[]` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `POST /firewall/rules` | platformMW | `CreateFirewallRule` | fails closed: `410 Gone`, stable code `FIREWALL_RULE_ENGINE_NOT_OPERATIONAL`, inserts nothing. No production mail path consults `firewall_rules` — `internal/firewall.Module.Start` never calls `LoadRules`, and CoreMail enforces policy via `internal/ruler` instead. Previously bound raw JSON directly into `models.FirewallRule` with no validation and silently persisted rules nothing enforced; retired in this pass rather than wired into the mail path (a separate bounded backend change). | Platform | `firewall_test.go` (backend: 410, stable code, zero row/audit mutation), `AuditFirewallSelfHeal.test.tsx` (frontend: no Create Rule control, no create mutation, legacy-not-enforced label) | DEPRECATED / NOT_OPERATIONAL |
| `GET /guardian/logs` | platformMW | `ListGuardianLogs` | `GuardianLog[]` | Platform | `GuardianPanel.test.tsx` | UI_SUPPORTED |
| `POST /guardian/analyze` | platformMW | `AnalyzeEmail` | takes raw email content (subject/body/headers/SPF/DKIM/DMARC results) as input | Platform | — | MACHINE_ONLY (no operator form makes sense for submitting raw email content for analysis — this is an internal analysis call, not an admin action) |
| `GET /heal/history` | platformMW | `ListHealHistory` | `HealHistoryEntry[]` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `POST /heal/check/:name` | platformMW | `RunHealCheck` | `RunHealCheckResponse` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `GET /admin/log-rules` | platformMW | `ListLogRules` | `ListLogRulesResponse` | Platform | `LogRulesPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/log-rules` | platformMW | `CreateLogRule` | `CreateLogRuleRequest` | Platform | `LogRulesPanel.test.tsx` | UI_SUPPORTED |
| `DELETE /admin/log-rules/:id` | platformMW | `DeleteLogRule` | destructive, typed confirm | Platform | `LogRulesPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/fs/browse`, `GET /admin/fs/read` | platformMW | `AdminFsBrowse` / `AdminFsRead` | raw server filesystem directory listing/file read | Platform | — | MACHINE_ONLY (exposing arbitrary filesystem traversal in a remotely-reachable web console meaningfully widens the blast radius of any session/token compromise; this capability is intentionally not surfaced as a browsable UI) |
| `GET /admin/mfa/status`, `POST /admin/mfa/setup/begin`, `POST /admin/mfa/setup/verify`, `POST /admin/mfa/disable` | platformMW | `MFAStatusGet` / `MFASetupBegin` / `MFASetupVerify` / `MFADisable` | duplicate of the self-scoped `/account/mfa/*` routes already used by the shared Account/Security page (safe for either portal) | Platform | (covered indirectly via the self-scoped `/account/mfa/*` path's own tests, outside this matrix's platformMW scope) | DUPLICATE_SUPERSEDED_ROUTE |

## Configuration

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /admin/settings` | platformMW | `AdminSettingsGet` | sectioned, typed `AdminSettingsResponse` | Platform | `SettingsPanel.test.tsx` | UI_SUPPORTED |
| `PATCH /admin/settings` | platformMW | `AdminSettingsPatch` | nested diff-only `SettingsPatchRequest` | Platform | `SettingsPanel.test.tsx` | UI_SUPPORTED |
| `GET /feature-flags` | platformMW | `ListFeatureFlags` | `FeatureFlag[]` | Platform | `FeatureFlagsPanel.test.tsx` | UI_SUPPORTED |
| `PUT /feature-flags/:id` | platformMW | `UpdateFeatureFlag` | `{enabled}` | Platform | `FeatureFlagsPanel.test.tsx` | UI_SUPPORTED |
| `GET /modules` | platformMW | `ListModules` | `{id,version,status}[]` | Platform (pre-existing `Modules.tsx`, its own top-level nav item) | none (pre-existing, not migrated into a feature directory or given new tests in this PR) | READ_ONLY_STATUS (real page, real data, correctly platform-owned — not re-migrated in this PR; no defect found on inspection) |
| `GET /admin/summary` | platformMW | `AdminSummary` | platform-wide totals across every tenant | Platform (pre-existing `EnterpriseDashboard.tsx`, the "Summary" nav item) | none (pre-existing) | READ_ONLY_STATUS (verified genuinely platform-owned despite the component's historical "Customer Admin" framing, corrected in an earlier commit's on-page copy) |
| `GET /admin/runtime` | platformMW | `GetAdminRuntime` | listener/runtime snapshot (ports, watermark, listener status) | Platform | — | MISSING_UI (real route, no frontend consumer; overlaps partially with `SystemHealth.tsx`'s `/monitoring/health` but is not the same data) |
| `GET /license` | platformMW | `GetLicense` | unconditionally `{"status":"not_required"}` | Platform | — | DEPRECATED (backend already retired for this hosted-SaaS product; frontend page removed in this PR) |
| `POST /license/validate` | platformMW | `ValidateLicense` | unconditionally `410 Gone` | Platform | — | DEPRECATED |
| `GET /console/reports` | platformMW | `AdminReports` | superseded reporting view | Platform | — | DUPLICATE_SUPERSEDED_ROUTE (superseded by `/platform/dashboard` + the six migrated feature pages) |
| `GET /console/internal/overview`, `GET /console/internal/tenants`, `GET /console/internal/domain-intelligence`, `GET /console/internal/security-ops`, `GET /console/internal/mail-flow-ops` (5 routes) | platformMW | `InternalOverview` / `InternalTenants` / `InternalDomainIntelligence` / `InternalSecurityOps` / `InternalMailFlowOps` | superseded internal-console views | Platform | — | DUPLICATE_SUPERSEDED_ROUTE (superseded by `/platform/dashboard`, Organizations, Mail Operations, and Security — the newer, tested pages) |

## Disaster Recovery (Milestone 13)

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /dr/readiness` | platformMW | `GetDRReadiness` | `dr.Readiness` | Platform | `internal/platform/dr` service tests | MISSING_UI (real route, no frontend consumer yet — backend-only in this pass) |
| `GET /dr/drills` | platformMW | `GetDRDrills` | `{drills: dr.Drill[]}` | Platform | `internal/platform/dr` service tests | MISSING_UI |
| `POST /dr/drills` | platformMW | `PostDRDrill` | records drill outcome, no restore performed | Platform | `internal/platform/dr` service tests | MISSING_UI |
| `POST /dr/backup` | platformMW | `PostDRCoordinatedBackup` | durable-lease-coordinated backup over `backup.Service.CreateBackup` | Platform | `internal/platform/dr` service tests | MISSING_UI |
| `POST /dr/backups/:id/restore` | platformMW | `PostDRCoordinatedRestore` | typed confirm `RESTORE-THIS-BACKUP`; submits to the same `restorecoord.Coordinator` as `POST /admin/backups/:id/restore` — no competing restart/rollback implementation | Platform | `internal/restorecoord` tests (shared coordinator) | MISSING_UI |
| `GET /dr/operations` | platformMW | `GetDROperationHistory` | paginated `{operations: dr.Operation[], total, limit, offset}` — past coordinated backup/restore operations, newest first (distinct from the live single-job status below) | Platform | `internal/platform/dr` service tests, `internal/api/handlers` idempotency-replay test | MISSING_UI |
| `GET /dr/operations/:job_id` | platformMW | `GetDROperationStatus` | reads the same `restorecoord` job result as `GET /admin/backups/restore-jobs/:job_id` | Platform | `internal/restorecoord` tests | MISSING_UI |

## Retention / Legal Hold / Purge (Milestone 14)

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `POST /retention/policies` | platformMW | `PostRetentionPolicy` | `retention.Policy` | Platform | `internal/platform/retention` service tests | MISSING_UI |
| `GET /retention/policies/effective` | platformMW | `GetRetentionEffectivePolicy` | resolved `retention.Policy` for a scope | Platform | `internal/platform/retention` service tests | MISSING_UI |
| `POST /retention/legal-holds` | platformMW | `PostRetentionLegalHold` | `retention.LegalHold` | Platform | `internal/platform/retention` service tests | MISSING_UI |
| `GET /retention/legal-holds` | platformMW | `GetRetentionLegalHolds` | `{holds: retention.LegalHold[]}` for a scope | Platform | `internal/platform/retention` service tests | MISSING_UI |
| `POST /retention/legal-holds/:id/release` | platformMW | `PostRetentionLegalHoldRelease` | releases a hold | Platform | `internal/platform/retention` service tests | MISSING_UI |
| `POST /retention/purge/plan` | platformMW | `PostRetentionPurgePlan` | non-destructive dry-run `retention.PurgePlan` | Platform | `internal/platform/retention` service tests | MISSING_UI |
| `POST /retention/purge/execute` | platformMW | `PostRetentionPurgeExecute` | destructive, typed confirm `PURGE-ELIGIBLE-DATA`, rechecks legal hold at execution time | Platform | `internal/platform/retention` service tests | MISSING_UI |
| `POST /retention/mailboxes/:id/recover` | platformMW | `PostRetentionRecoverMailbox` | delegates to the existing `mailboxAdminSvc.RestoreMailbox` undelete capability (same one behind `POST /enterprise/mailboxes/:id/restore`), records chain-of-custody | Platform | `internal/admin/mailbox` restore tests | MISSING_UI |
| `GET /retention/custody` | platformMW | `GetRetentionCustody` | paginated `retention.ChainOfCustodyEvent[]` — IDs/hashes/metadata only, never message bodies | Platform | `internal/platform/retention` service tests | MISSING_UI |

## Platform Billing Balances/Adjustments (Milestone 15)

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /platform/billing/tenants/:tenant_id/balance` | platformMW | `GetPlatformBillingBalance` | `platformbilling.Balance` | Platform | `internal/platform/billing` service tests | MISSING_UI |
| `POST /platform/billing/tenants/:tenant_id/adjustments` | platformMW | `PostPlatformBillingAdjustment` | `platformbilling.Adjustment`, integer minor units + currency only, idempotency-key supported | Platform | `internal/platform/billing` service tests (incl. concurrent-idempotency-key test) | MISSING_UI |
| `GET /platform/billing/tenants/:tenant_id/adjustments` | platformMW | `GetPlatformBillingAdjustments` | `{adjustments: platformbilling.Adjustment[]}` | Platform | `internal/platform/billing` service tests | MISSING_UI |
| `GET /platform/billing/tenants/:tenant_id/reconciliation` | platformMW | `GetPlatformBillingReconciliation` | read-only `billing.ReconciliationReport` — recomputes the ledger balance from full adjustment history and reports any discrepancy against the maintained balance row; never auto-corrects (Milestone 13-15 re-audit gap fix) | Platform | `internal/api/handlers` idempotency-replay test | MISSING_UI |

## Signed Update Artifacts (Milestone 13)

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `POST /updates/artifacts` | platformMW | `PostUpdateArtifact` | ed25519-signed manifest verification + hash check + staged lifecycle; rejects unsigned/tampered/wrong-version/wrong-platform artifacts | Platform | `internal/platform/updates/service_test.go` (unsigned, tampered, invalid signature, wrong version, wrong platform, valid end-to-end) | MISSING_UI |
| `GET /updates/artifacts/history` | platformMW | `GetUpdateArtifactHistory` | `{history: updates.Record[]}` | Platform | `internal/platform/updates/service_test.go` | MISSING_UI |
| `GET /updates/artifacts/:id` | platformMW | `GetUpdateArtifactStatus` | `updates.Record` | Platform | `internal/platform/updates/service_test.go` | MISSING_UI |
| `POST /updates/artifacts/:id/apply` | platformMW | `PostUpdateArtifactApply` | requires typed confirmation `APPLY-STAGED-UPDATE`; fails closed (503) unless the external `orvix-update.path`/`.service` coordinator is installed; hands the staged, verified artifact off to `internal/updatecoord` (durable job, mutually exclusive with rollback, idempotent retries) — never applies in-process | Platform | `internal/platform/updates/service_test.go` (`TestTriggerApply_NoCoordinator_LeavesStaged`, `TestTriggerApply_WithCoordinator_TransitionsToApplied`, `TestTriggerApply_RetryIsIdempotent`), `internal/updatecoord/coordinator_test.go` | MISSING_UI |
| `POST /updates/artifacts/:id/rollback` | platformMW | `PostUpdateArtifactRollback` | requires typed confirmation `ROLLBACK-APPLIED-UPDATE` + reason; hands off to the same external coordinator using pre-captured previous-version/hash metadata; fails closed if not installed | Platform | `internal/platform/updates/service_test.go` (`TestRollback_RetryIsIdempotent`, `TestRollback_WithoutApply_Rejected`) | MISSING_UI |
| `GET /updates/operations/:job_id` | platformMW | `GetUpdateOperationStatus` | durable apply/rollback job status, re-read from disk every call so it survives the Orvix restart the coordinator performs | Platform | `internal/updatecoord/coordinator_test.go` | MISSING_UI |
| `POST /incidents` | platformMW | `CreateIncident` | create a new incident with severity, services, and regions | Platform | `internal/incident/` | MISSING_UI |
| `GET /incidents` | platformMW | `ListIncidents` | list incidents with optional status filter | Platform | `internal/incident/` | MISSING_UI |
| `GET /incidents/:id` | platformMW | `GetIncident` | get an incident by ID | Platform | `internal/incident/` | MISSING_UI |
| `PATCH /incidents/:id` | platformMW | `UpdateIncident` | update incident status with timeline event | Platform | `internal/incident/` | MISSING_UI |
| `GET /incidents/:id/timeline` | platformMW | `GetIncidentTimeline` | get incident timeline events | Platform | `internal/incident/` | MISSING_UI |
| `POST /platform/support/grants` | platformMW | `CreateSupportAccessGrant` | request a temporary support-access grant for a tenant | Platform | `internal/supportaccess/` | MISSING_UI |
| `GET /platform/support/grants` | platformMW | `ListSupportAccessGrants` | list support-access grants by tenant | Platform | `internal/supportaccess/` | MISSING_UI |
| `GET /platform/support/grants/:id` | platformMW | `GetSupportAccessGrant` | get a support-access grant by ID | Platform | `internal/supportaccess/` | MISSING_UI |
| `POST /platform/support/grants/:id/activate` | platformMW | `ActivateSupportAccessGrant` | activate an approved support-access grant | Platform | `internal/supportaccess/` | MISSING_UI |
| `POST /platform/support/grants/:id/revoke` | platformMW | `RevokeSupportAccessGrant` | revoke an active support-access grant | Platform | `internal/supportaccess/` | MISSING_UI |
| `POST /platform/automation/jobs` | platformMW | `SubmitPlatformAutomationJob` | submit an explicitly platform-scoped allowlisted durable job; requires idempotency key | Platform | `internal/platform/jobs/` | MISSING_UI |
| `GET /platform/automation/jobs` | platformMW | `ListPlatformAutomationJobs` | paginated and filterable platform-job history | Platform | `internal/platform/jobs/` | MISSING_UI |
| `GET /platform/automation/jobs/:id` | platformMW | `GetPlatformAutomationJob` | safe durable job detail without payload, lease, or idempotency internals | Platform | `internal/platform/jobs/` | MISSING_UI |
| `POST /platform/automation/jobs/:id/cancel` | platformMW | `CancelPlatformAutomationJob` | atomically cancel queued work or request cooperative cancellation of running work | Platform | `internal/platform/jobs/` | MISSING_UI |
| `POST /platform/automation/jobs/:id/retry` | platformMW | `RetryPlatformAutomationJob` | idempotently requeue an eligible failed platform job | Platform | `internal/platform/jobs/` | MISSING_UI |
| `GET /platform/capabilities` | platformMW | `GetCapabilities` | runtime capability registry derived from registered modules and health | Platform | `internal/capability/` | MISSING_UI |
| `GET /platform/config` | platformMW | `ListConfigurationSettings` | list authoritative configuration settings with source/effective/pending state | Platform | `internal/configtruth/` | MISSING_UI |
| `GET /platform/config/:key` | platformMW | `GetConfigurationSetting` | get authoritative view of one setting | Platform | `internal/configtruth/` | MISSING_UI |
| `PATCH /platform/config/:key` | platformMW | `MutateConfigurationSetting` | validate and apply a configuration mutation with optimistic concurrency | Platform | `internal/configtruth/` | MISSING_UI |

## Imports (Milestone 16, Phase 4B)

Bulk tenant provisioning via staged CSV/JSON sources. All nine routes
are backend-only in this pass (no console UI consumer yet): the
importer is driven through the API and the `orvix` CLI. The full
import lifecycle is covered by `internal/platform/importer`
service/worker tests and `internal/api/handlers/import_route_acceptance_test.go`
(tenant isolation, RBAC, CSRF, confirmation strings, idempotency,
hash verification, safe-field updates, compensation).

| `GET /platform/imports` | platformMW | `ListImports` | paginated/filtered import history, tenant-scoped | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |
| `GET /platform/imports/:id` | platformMW | `GetImport` | import job detail without staging/lease/idempotency internals | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |
| `GET /platform/imports/:id/report` | platformMW | `GetImportReport` | dry-run validation report with before/after diffs and redacted secrets | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |
| `POST /platform/imports` | platformMW | `CreateImport` | stage a new import source (SHA-256 verified at every later read) | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |
| `POST /platform/imports/:id/validate` | platformMW | `ValidateImport` | dry-run validation with zero mutations | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |
| `POST /platform/imports/:id/execute` | platformMW | `ExecuteImport` | durable queued-activation handoff; requires `Idempotency-Key` and exact confirmation `EXECUTE-IMPORT-<id>` | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |
| `POST /platform/imports/:id/resume` | platformMW | `ResumeImport` | idempotent resume of a paused/failed import; requires `Idempotency-Key` | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |
| `POST /platform/imports/:id/cancel` | platformMW | `CancelImport` | cancel a running/paused import | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |
| `POST /platform/imports/:id/compensate` | platformMW | `CompensateImport` | reverse the import's own mutations only; requires `Idempotency-Key` and exact confirmation `COMPENSATE-IMPORT-<id>`; refuses to overwrite human changes | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI |

## Platform Mail Control (Mail-Control enablement)

Platform Super Admin mail-control surface over the production admin
services. Every route requires an explicit target `tenant_id` in the
path and is platformMW-gated (tenant roles are denied); all mutations
are RBAC-permissioned, audited, and tenant-scoped in SQL.

| `GET /platform/domains/:tenant_id` | platformMW | `ListPlatformDomains` | paginated platform domain list for an explicit tenant; search/status filters | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `GET /platform/domains/:tenant_id/:id` | platformMW | `GetPlatformDomain` | domain detail with counts and mail-access mode, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `POST /platform/domains/:tenant_id/:id/status` | platformMW | `SetPlatformDomainStatus` | allowed lifecycle transition, tenant-scoped, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `POST /platform/domains/:tenant_id/:id/mail-access-mode` | platformMW | `SetPlatformDomainMailAccessMode` | set canonical SMTP mail-access mode (`internal_only`/`internal_external`), audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `GET /platform/mailboxes/:tenant_id` | platformMW | `ListPlatformMailboxes` | paginated platform mailbox list for an explicit tenant/domain | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `GET /platform/mailboxes/:tenant_id/:id` | platformMW | `GetPlatformMailbox` | mailbox detail, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `POST /platform/mailboxes/:tenant_id/:id/status` | platformMW | `SetPlatformMailboxStatus` | mailbox lifecycle transition, tenant-scoped, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `POST /platform/mailboxes/:tenant_id/:id/quota` | platformMW | `SetPlatformMailboxQuota` | quota update with domain-bound ceiling, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `POST /platform/mailboxes/:tenant_id/:id/reset-password` | platformMW | `ResetPlatformMailboxPassword` | secure one-time password reset via the production service, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `DELETE /platform/mailboxes/:tenant_id/:id` | platformMW | `DeletePlatformMailbox` | soft-delete with typed confirmation `PURGE-MAILBOX-<id>`, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `POST /platform/mailboxes/:tenant_id/bulk/status` | platformMW | `BulkPlatformMailboxStatus` | bounded bulk status transition (max 500 ids), tenant-scoped, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `GET /platform/aliases/:tenant_id` | platformMW | `ListPlatformAliases` | paginated alias list for an explicit tenant/domain | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `GET /platform/aliases/:tenant_id/:id` | platformMW | `GetPlatformAlias` | alias detail, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `POST /platform/aliases/:tenant_id` | platformMW | `CreatePlatformAlias` | create alias with domain ownership, loop rejection, conflict detection, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `DELETE /platform/aliases/:tenant_id/:id` | platformMW | `DeletePlatformAlias` | soft-delete alias, tenant-scoped, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `GET /platform/groups/:tenant_id` | platformMW | `ListPlatformGroups` | paginated group list for an explicit tenant | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `GET /platform/groups/:tenant_id/:id` | platformMW | `GetPlatformGroup` | group detail with member count, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |
| `GET /platform/groups/:tenant_id/:id/members` | platformMW | `ListPlatformGroupMembers` | group member emails, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI |

## Theme system (cross-cutting, not a route)

Verified by `web/admin/src/shared/theme/{theme,useTheme}.test.ts`,
`noHardcodedColors.test.ts` (static scan, zero hardcoded colors in
application code), and `test/playwright/portal.spec.ts`'s merged
Light/Dark coverage for both PSA and Tenant Admin.

## Summary counts

This is the single canonical summary for this document — a prior
draft of this file briefly carried two contradictory "Summary counts"
sections (59/5/3/11/19/3=100 vs. 62/6/3/11/17/2=101); that was a
real defect in the document itself, not just a typo, and
`internal/api/capability_matrix_test.go` now fails the build if a
second "## Summary counts" heading, or a route missing from either
side, is ever reintroduced.

Route-level, not row-level — several rows above group multiple route
registrations under one disposition (the 9 legacy `/backups*`
`LegacyGone` routes, the 5 `/console/internal/*` routes, the 4
`/admin/mfa/*` routes, the `GET /admin/fs/browse`+`GET /admin/fs/read`
pair, and the combined `GET`+`PATCH /admin/settings/protocol/:protocol`
pair). Every number below was recomputed directly from the table rows
above (not carried over from an earlier draft) and is enforced equal
to the router's actual route set by
`internal/api/capability_matrix_test.go`, which parses
`platformMW[0], platformMW[1]` registrations straight out of
`router.go` — currently 177 — and parses every `` `METHOD /path` ``
occurrence and its row's disposition straight out of this document.

| Disposition | Routes |
|---|---|
| UI_SUPPORTED | 59 |
| READ_ONLY_STATUS | 5 |
| MACHINE_ONLY | 3 |
| DEPRECATED | 12 |
| DUPLICATE_SUPERSEDED_ROUTE | 18 |
| MISSING_UI | 80 |
| MISSING_BACKEND | 0 (the one MISSING_BACKEND case — platform-initiated organization creation — is a non-route documented under Organizations, not counted here) |
| **Total** | **177** |

Three pre-existing MISSING_UI gaps were documented rather than
silently omitted: `GET /admin/backups/:id` (single-backup fetch; the
list view already shows everything the struct provides), `POST
/admin/backups/schedule` (a `setBackupSchedule` client function exists
but no component calls it — the schedule is currently read-only in the
UI), and `GET /admin/runtime` (real route, no frontend consumer). The
remaining 26 MISSING_UI rows were added in the Milestone 13-15 pass:
three pre-existing queue routes (`/admin/queue/history`,
`/admin/queue/export`, `/admin/queue/messages/bulk-action`) found
undocumented, and 23 new backend-only routes for DR coordination,
retention/legal-hold/purge, platform billing balances/adjustments, and
signed update-artifact staging — all thin handlers over already-tested
service layers, with no console UI built for them in this pass. The
Milestone 16 pass adds 9 more MISSING_UI rows for the platform import
routes (bulk tenant provisioning via the durable importer), also
backend-only and driven through the API and CLI.

DEPRECATED is 12, not 11, because `POST /firewall/rules` moved from
UI_SUPPORTED to DEPRECATED / NOT_OPERATIONAL in this pass (see
Security, above) — the legacy rule-creation UI was retired rather
than left pointing at a non-enforcing write.
