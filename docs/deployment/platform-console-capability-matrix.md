# Platform Console Capability Matrix

Complete audit of every route gated with `platformMW[0], platformMW[1]`
in `internal/api/router.go` (231 route registrations, verified by
`internal/api/capability_matrix_test.go` against the branch head this
document was written against â€” updated for the Milestone 13-15 DR,
retention, platform billing, and signed-update-artifact routes, plus
three pre-existing queue routes found undocumented during that pass,
plus the DR operation-history and platform-billing reconciliation
routes added during the M13-15 re-audit gap-closure pass, plus the
canonical, audited, permanent platform domain delete route added
alongside deactivate, plus the six audited read-only mailbox
support-view routes, plus the seven routes added by the Enterprise
Product Completion Pass: PSA organization creation, platform groups
CRUD (4 routes), platform billing overview, and platform DKIM revoke).
Every disposition below reflects the actual handler and actual
frontend consumer, not the route's name.

## Disposition taxonomy

- **UI_SUPPORTED** â€” a real frontend page/action calls this route, tested.
- **READ_ONLY_STATUS** â€” a real frontend page displays this route's data; no mutation exists for it (by backend design, not omission).
- **MACHINE_ONLY** â€” the request shape makes an operator form nonsensical (e.g. takes raw email content).
- **INTERNAL_DEPENDENCY** â€” not applicable to any route in this matrix (reserved for vendor/infra dependencies; none found among these 100).
- **DEPRECATED** â€” the handler itself is a stub/gone-marker (`LegacyGone`) or the whole feature is retired.
- **DUPLICATE_SUPERSEDED_ROUTE** â€” a newer route/page covers the same capability; this one is intentionally not wired.
- **MISSING_UI** â€” a real, working backend route with no frontend consumer (a UI gap this PR did not close).
- **MISSING_BACKEND** â€” the frontend needs a capability that has no registered route.

## Overview

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /platform/dashboard` | platformMW | `PlatformDashboard` | `overview/contract.ts`'s `PlatformDashboard` | Platform | `overview/page.test.tsx` | UI_SUPPORTED |

## Organizations

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /platform/organizations` | platformMW | `ListPlatformOrganizations` | `organizations/contract.ts`'s `ListOrganizationsResponse` | Platform | `organizations/page.test.tsx` | UI_SUPPORTED |
| `POST /platform/organizations` | platformMW | `CreatePlatformOrganization` | PSA organization creation: `name`, optional `slug`/`domain`/`plan_id`/`max_domains`/`max_mailboxes`, REQUIRED `owner_email`; one-time `invite_token` in the live response only; Idempotency-Key required | Platform | `platform_org_creation_acceptance_test.go` (RBAC, CSRF, idempotency replay, owner-required, no-ownerless-org, hash-only token storage) | MISSING_UI (backend COMPLETE â€” the documented MISSING_BACKEND capability, closed by the Enterprise Completion Pass; no frontend consumer yet â€” the create-org form is the frontend agent's wiring task) | UI_SUPPORTED (CreateOrganizationDialog: owner_email required, one-time invite token shown once with copy + pending_activation framing; organizations/create.test.tsx) |
| `GET /platform/organizations/:id/detail` | platformMW | `GetOrganizationDetail` | `OrganizationDetail` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `POST /platform/organizations/:id/active` | platformMW | `SetOrganizationActive` | `SetOrganizationActiveRequest/Response` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `PATCH /platform/organizations/:id` | platformMW | `UpdateOrganization` | `UpdateOrganizationRequest/Response` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `GET /platform/organizations/:id` | platformMW | `GetPlatformOrganization` | untyped `map[string]interface{}` from a different service (`platformAdminSvc`) than the typed `/detail` route above | Platform | `page.test.tsx` (regression: proves the client has no function for this route at all) | DUPLICATE_SUPERSEDED_ROUTE |
| `POST /platform/organizations/:id/deletion` | platformMW | `PlatformScheduleOrganizationDeletion` | `organizations/contract.ts`'s `ScheduleOrganizationDeletionRequest/Response` | Platform | `platform_deletion_test.go`, `platform_deletion_auth_test.go` | UI_SUPPORTED |

MISSING_BACKEND (not a route, so not counted in the 100): the console
has no way for a Platform Super Admin to create a new organization â€”
`web/admin/src/features/platform/organizations/api.ts` has no create
function, and no `POST /platform/organizations` (or equivalent)
route is registered anywhere in `router.go`. Organizations are
currently created only via tenant self-signup, never PSA-initiated.
Not fixed in this pass â€” flagged, not silently omitted.

**CLOSED by the Enterprise Product Completion Pass**: `POST
/platform/organizations` is now a real, registered route (row above).
Product semantics decided and enforced: PSA-created organizations ARE
product-supported; an initial owner is REQUIRED (`owner_email`) and is
established via the real tenant_admin invitation/activation model
(one-time token, hashed at rest, 7-day expiry) — never an invented
owner user/password, and never an ownerless ACTIVE organization; plan/
subscription initialized consistently with self-signup (free plan
default); the org row + owner invitation + audit record commit in ONE
transaction; creation is Idempotency-Key-guarded with replay returning
the stored (token-free) result. **CLOSED (Phase 3 frontend)**: the
console's create-org dialog (owner email required, one-time invite
token revealed once with copy, pending_activation lifecycle rendered
distinctly) is wired and tested (`organizations/create.test.tsx`,
`organizations/page.test.tsx`); the org activates only when the owner
redeems the token at the public invitation-accept page
(`InvitationAcceptPage.tsx`).

## Mail Operations

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /admin/queue/summary` | platformMW | `AdminQueueSummary` | `QueueSummaryResponse` | Platform | `mail-operations/page.test.tsx` | UI_SUPPORTED |
| `GET /admin/queue/messages` | platformMW | `AdminQueueList` | `ListQueueMessagesResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `GET /admin/queue/messages/:id` | platformMW | `AdminQueueDetail` | `QueueDetailResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `POST /admin/queue/messages/:id/retry` | platformMW | `AdminQueueRetryNow` | `QueueActionResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `POST /admin/queue/messages/:id/bounce` | platformMW | `AdminQueueBounce` | `QueueActionResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `POST /admin/queue/messages/:id/cancel` | platformMW | `AdminQueueCancel` | `QueueActionResponse` | Platform | `page.test.tsx` | UI_SUPPORTED |
| `GET /admin/queue/:id` | platformMW | `GetAdminQueueEntry` | diagnostic single-entry view, superseded by `AdminQueueDetail` | Platform | â€” | DUPLICATE_SUPERSEDED_ROUTE |
| `GET /queue` | platformMW | `ListQueue` | legacy webmail-facing queue list (different schema than `AdminQueueList` â€” see the earlier `COREMAIL_DISABLED` fix commit) | Platform (webmail SPA is the real consumer, not this admin console) | `cmd/orvix/fullstack_repro_test.go` (backend) | DUPLICATE_SUPERSEDED_ROUTE (superseded, for this admin console, by `AdminQueueList`; still load-bearing for the separate webmail frontend, so not removable here) |
| `DELETE /queue/:id` | platformMW | `DeleteQueue` | legacy queue delete | Platform | â€” | DUPLICATE_SUPERSEDED_ROUTE (superseded by `AdminQueueCancel`) |
| `POST /queue/:id/retry` | platformMW | `RetryQueue` | legacy queue retry | Platform | â€” | DUPLICATE_SUPERSEDED_ROUTE (superseded by `AdminQueueRetryNow`) |
| `GET /admin/queue/history` | platformMW | `AdminQueueHistory` | queue history | Platform | â€” | MISSING_UI (real route, no frontend consumer found) | UI_SUPPORTED (QueueHistoryPanel delivery-attempt history with cursor pagination + status/remote-host filters; mail-operations/history.test.tsx) |
| `GET /admin/queue/export` | platformMW | `AdminQueueExport` | queue export | Platform | â€” | MISSING_UI (real route, no frontend consumer found) | UI_SUPPORTED (QueueHistoryPanel 'Export queue CSV' blob download through the shared client's responseType=blob; mail-operations/history.test.tsx) |
| `POST /admin/queue/messages/bulk-action` | platformMW | `AdminQueueBulkAction` | bulk queue action | Platform | â€” | MISSING_UI (real route, no frontend consumer found) | UI_SUPPORTED (BulkQueueActionPanel: retry/cancel/bounce batch with per-row results + typed confirmation; mail-operations/page.test.tsx) |

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
| `POST /admin/backups/schedule` | platformMW | `SetBackupSchedule` | `BackupSchedule` (enabled/frequency/retentionCount) | Platform | â€” | MISSING_UI (a `setBackupSchedule` client function exists in `reliability/api.ts` but no component calls it â€” the panel shows the schedule read-only; found during this audit, not fixed in this PR) | UI_SUPPORTED (BackupsPanel schedule editor: enabled/frequency/retentionCount via POST /admin/backups/schedule; BackupsPanelSchedule.test.tsx) |
| `GET /admin/backups/metrics` | platformMW | `GetBackupMetrics` | `BackupMetrics` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/backups/health` | platformMW | `GetBackupHealth` | `BackupHealth` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups/retention` | platformMW | `RunBackupRetention` | `{deleted?}` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups/:id/validate` | platformMW | `PostValidateBackup` | `{status}` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/backups/:id/restore` | platformMW | `PostRestoreBackup` | `RestoreJobSubmitResponse`, typed confirm `restore-orvix-backup` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `DELETE /admin/backups/:id` | platformMW | `DeleteBackup` | destructive, typed confirm `delete-orvix-backup` | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/backups/restore-jobs/:job_id` | platformMW | `GetRestoreJobStatus` | `RestoreJobResult`, polled | Platform | `BackupsPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/backups/:id/download` | platformMW | `DownloadBackup` | binary archive stream | Platform | `BackupsPanel` (`downloadBackupUrl` used as a plain `<a href>`, not a fetch â€” not independently unit-testable) | UI_SUPPORTED |
| `GET /admin/backups/:id` | platformMW | `GetBackup` | single backup metadata | Platform | â€” | MISSING_UI (real route; the panel lists all backups but never fetches one individually â€” the table row already has everything `Backup` provides) |
| `GET /backups`, `GET /backups/schedule`, `GET /backups/metrics`, `GET /backups/health`, `GET /backups/:id/download`, `POST /backups`, `POST /backups/schedule`, `POST /backups/retention`, `DELETE /backups/:id` (9 routes) | platformMW | `LegacyGone` | 410/removed marker | Platform | â€” | DEPRECATED |
| `GET /update/status` | platformMW | `GetUpdateStatus` | `UpdateStatus` | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `GET /update/check` | platformMW | `GetUpdateCheck` | `UpdateCheckResult` | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `POST /update/check` | platformMW | `PostUpdateCheck` | `UpdateCheckResult` | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `GET /update/history` | platformMW | `GetUpdateHistory` | `{"history": UpdateHistoryRow[]}` â€” an envelope, unlike its status/check/preflight siblings; a real bug where the frontend cast it straight through as a bare array (crashing every real load) was found and fixed via `test/playwright/portal.spec.ts`'s live-server gap-coverage sweep, `api.ts` unwraps it now | Platform | `UpdatesPanel.test.tsx`, `api.test.ts` | UI_SUPPORTED |
| `GET /update/preflight` | platformMW | `GetUpdatePreflight` | `PreflightResult` | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `POST /update/run` | platformMW | `PostUpdateRun` | typed confirm, real single-flight (`svc.IsRunning`) | Platform | `UpdatesPanel.test.tsx` | UI_SUPPORTED |
| `GET /updates/changelog` | platformMW | `GetChangelog` | `ChangelogEntry[]` (capitalized fields â€” no json tags) | Platform | `ChangelogPanel.test.tsx` | UI_SUPPORTED |
| `GET /updates/check` | platformMW | `CheckUpdates` | legacy flat `{status,module,version}` shape | Platform | â€” | DUPLICATE_SUPERSEDED_ROUTE (superseded by `GET /update/check`) |
| `POST /updates/apply/:module` | platformMW | `ApplyUpdate` | **no-op stub**: writes an audit entry and returns `{"status":"update initiated"}` without checking module existence, without single-flight protection, without performing any real update | Platform | â€” | DUPLICATE_SUPERSEDED_ROUTE (superseded by `POST /update/run`, which has real single-flight + preflight; wiring this stub to a UI action would be a fabricated success state) |
| `GET /monitoring/alerts` | platformMW | `GetMonitoringAlerts` | `{alerts}` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `POST /monitoring/alerts/:id/resolve` | platformMW | `PostMonitoringAlertResolve` | `{status,id}` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/capacity` | platformMW | `GetMonitoringCapacity` | `Capacity` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/snapshot` | platformMW | `GetMonitoringSnapshot` | `MonitoringSnapshot` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/alert-providers` | platformMW | `GetMonitoringProviders` | `MonitoringProvidersResponse` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/alert-deliveries` | platformMW | `ListAlertDeliveries` | `ListAlertDeliveriesResponse` | Platform | `MonitoringPanel.test.tsx` | UI_SUPPORTED |
| `GET /monitoring/health` | platformMW | `GetMonitoringHealth` | `monitoring.Health` | Platform (pre-existing `SystemHealth.tsx`, the "Health" nav item) | none â€” `SystemHealth.tsx` calls `fetch("/api/v1/monitoring/health")` directly, bypassing the shared client | READ_ONLY_STATUS (real page, real data â€” but the direct-fetch defect in `SystemHealth.tsx` was NOT fixed in this PR; it predates the six migrated domains and is out of this PR's explicit scope. Flagged, not silently left undocumented.) | UI_SUPPORTED (SystemHealth.tsx now reads through the canonical typed client getMonitoringHealth — DIRECT_FETCH_BYPASS fixed; SystemHealth.test.tsx) |
| `GET /metrics` | platformMW | `metrics.Handler()` (Prometheus exposition format) | raw text | Platform | `MonitoringPanel`'s "Open raw metrics" external link | UI_SUPPORTED (external link, not embedded â€” appropriate for a Prometheus-format payload) |
| `GET /admin/storage/volumes` | platformMW | `ListStorageVolumes` | `ListStorageVolumesResponse` | Platform | `StoragePanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/cluster/status` | platformMW | `AdminClusteringStatus` | `ClusterStatus` â€” **honestly static**: `deployment_mode:"single_node"`, `honest_note:"Clustering + proxy replication is not implemented in this build."` | Platform | `ClusterPanel.test.tsx` | READ_ONLY_STATUS (this is the current, correct state â€” not a preview of the future `platform-cluster-control-plane` bounded context, which is unimplemented and out of scope for this PR) |
| `GET /admin/settings/protocol/:protocol`, `PATCH /admin/settings/protocol/:protocol` | platformMW | `ListProtocolSettings` / `PatchProtocolSettings` | `ProtocolSettingsResponse` / flat diff-only PATCH, 10 protocol IDs, per-key semantic validation, single-transaction write, post-commit `settingsBridge.Apply` determines the real `hot_applied`/`pending_restart` split (never a static guess); `coremail.imap_idle_enabled` is `ReadOnly` (no live-config field backs it) | Platform | `ProtocolSettingsPanel.test.tsx`, `protocol_settings_test.go` (10 backend cases against the real bridge/DB) | UI_SUPPORTED |

## Security

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /audit/logs` | platformMW | `ListAuditLogs` | `AuditEntry[]` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `GET /audit/logs/export` | platformMW | `ExportAuditLogs` | CSV/JSON export of filtered audit entries | Platform | `internal/audit/` | MISSING_UI | UI_SUPPORTED (audit page Export CSV/JSON blob download; features/platform/audit/export.test.tsx) |
| `GET /audit/logs/:id` | platformMW | `GetAuditEntry` | single audit entry by ID | Platform | `internal/audit/` | MISSING_UI | MISSING_UI (real route; the audit pages render list rows but no detail action consumes GET /audit/logs/:id yet) |
| `GET /admin/ssl/certificates` | platformMW | `AdminSslListCertificates` | `ListCertificatesResponse` | Platform | `SslPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/ssl/certificates/reload` | platformMW | `AdminSslReloadCertificates` | `{status?}` | Platform | `SslPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/ssl/certificates/reload` | platformMW | `AdminSslReloadCertificates` (same handler, also registered under GET) | `{status?}` | Platform | â€” | DUPLICATE_SUPERSEDED_ROUTE (the frontend uses the POST registration for this mutating action; the GET registration on the identical path is not called and is unusual for a mutating action to expose under GET â€” not removed, just unused) |
| `GET /admin/ssl/expiry-warnings` | platformMW | `AdminSslExpiryWarnings` | `{warnings}` | Platform | `SslPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/ssl/acme/status` | platformMW | `AdminSslAcmeStatus` | `AcmeStatus` â€” honestly static (`acme_enabled:false`, ACME issuance not implemented) | Platform | `SslPanel.test.tsx` | READ_ONLY_STATUS |
| `POST /admin/ssl/certificates` | platformMW | `AdminSslUploadCertificate` | `UploadCertificateRequest/Response`, secret input never retained/redisplayed | Platform | `SslUploadForm.test.tsx` | UI_SUPPORTED |
| `DELETE /admin/ssl/certificates/:id` | platformMW | `AdminSslDeleteCertificate` | destructive, typed confirm | Platform | `SslPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/security/antivirus` | platformMW | `AdminAntivirusStatus` | `AntivirusStatus` | Platform | `AntivirusPanel.test.tsx` | UI_SUPPORTED |
| `GET /firewall/rules` | platformMW | `ListFirewallRules` | `FirewallRule[]` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `GET /firewall/logs` | platformMW | `ListFirewallLogs` | `FirewallLog[]` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `POST /firewall/rules` | platformMW | `CreateFirewallRule` | fails closed: `410 Gone`, stable code `FIREWALL_RULE_ENGINE_NOT_OPERATIONAL`, inserts nothing. No production mail path consults `firewall_rules` â€” `internal/firewall.Module.Start` never calls `LoadRules`, and CoreMail enforces policy via `internal/ruler` instead. Previously bound raw JSON directly into `models.FirewallRule` with no validation and silently persisted rules nothing enforced; retired in this pass rather than wired into the mail path (a separate bounded backend change). | Platform | `firewall_test.go` (backend: 410, stable code, zero row/audit mutation), `AuditFirewallSelfHeal.test.tsx` (frontend: no Create Rule control, no create mutation, legacy-not-enforced label) | DEPRECATED / NOT_OPERATIONAL |
| `GET /guardian/logs` | platformMW | `ListGuardianLogs` | `GuardianLog[]` | Platform | `GuardianPanel.test.tsx` | UI_SUPPORTED |
| `POST /guardian/analyze` | platformMW | `AnalyzeEmail` | takes raw email content (subject/body/headers/SPF/DKIM/DMARC results) as input | Platform | â€” | MACHINE_ONLY (no operator form makes sense for submitting raw email content for analysis â€” this is an internal analysis call, not an admin action) |
| `GET /heal/history` | platformMW | `ListHealHistory` | `HealHistoryEntry[]` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `POST /heal/check/:name` | platformMW | `RunHealCheck` | `RunHealCheckResponse` | Platform | `AuditFirewallSelfHeal.test.tsx` | UI_SUPPORTED |
| `GET /admin/log-rules` | platformMW | `ListLogRules` | `ListLogRulesResponse` | Platform | `LogRulesPanel.test.tsx` | UI_SUPPORTED |
| `POST /admin/log-rules` | platformMW | `CreateLogRule` | `CreateLogRuleRequest` | Platform | `LogRulesPanel.test.tsx` | UI_SUPPORTED |
| `DELETE /admin/log-rules/:id` | platformMW | `DeleteLogRule` | destructive, typed confirm | Platform | `LogRulesPanel.test.tsx` | UI_SUPPORTED |
| `GET /admin/fs/browse`, `GET /admin/fs/read` | platformMW | `AdminFsBrowse` / `AdminFsRead` | raw server filesystem directory listing/file read | Platform | â€” | MACHINE_ONLY (exposing arbitrary filesystem traversal in a remotely-reachable web console meaningfully widens the blast radius of any session/token compromise; this capability is intentionally not surfaced as a browsable UI) |
| `GET /admin/mfa/status`, `POST /admin/mfa/setup/begin`, `POST /admin/mfa/setup/verify`, `POST /admin/mfa/disable` | platformMW | `MFAStatusGet` / `MFASetupBegin` / `MFASetupVerify` / `MFADisable` | duplicate of the self-scoped `/account/mfa/*` routes already used by the shared Account/Security page (safe for either portal) | Platform | (covered indirectly via the self-scoped `/account/mfa/*` path's own tests, outside this matrix's platformMW scope) | DUPLICATE_SUPERSEDED_ROUTE |

## Configuration

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /admin/settings` | platformMW | `AdminSettingsGet` | sectioned, typed `AdminSettingsResponse` | Platform | `SettingsPanel.test.tsx` | UI_SUPPORTED |
| `PATCH /admin/settings` | platformMW | `AdminSettingsPatch` | nested diff-only `SettingsPatchRequest` | Platform | `SettingsPanel.test.tsx` | UI_SUPPORTED |
| `GET /feature-flags` | platformMW | `ListFeatureFlags` | `FeatureFlag[]` | Platform | `FeatureFlagsPanel.test.tsx` | UI_SUPPORTED |
| `PUT /feature-flags/:id` | platformMW | `UpdateFeatureFlag` | `{enabled}` | Platform | `FeatureFlagsPanel.test.tsx` | UI_SUPPORTED |
| `GET /modules` | platformMW | `ListModules` | `{id,version,status}[]` | Platform (pre-existing `Modules.tsx`, its own top-level nav item) | none (pre-existing, not migrated into a feature directory or given new tests in this PR) | READ_ONLY_STATUS (real page, real data, correctly platform-owned â€” not re-migrated in this PR; no defect found on inspection) |
| `GET /admin/summary` | platformMW | `AdminSummary` | platform-wide totals across every tenant | Platform (pre-existing `EnterpriseDashboard.tsx`, the "Summary" nav item) | none (pre-existing) | READ_ONLY_STATUS (verified genuinely platform-owned despite the component's historical "Customer Admin" framing, corrected in an earlier commit's on-page copy) |
| `GET /admin/runtime` | platformMW | `GetAdminRuntime` | listener/runtime snapshot (ports, watermark, listener status) | Platform | â€” | MISSING_UI (real route, no frontend consumer; overlaps partially with `SystemHealth.tsx`'s `/monitoring/health` but is not the same data) | MISSING_UI (real route, no frontend consumer; overlaps SystemHealth's /monitoring/health — not the same data) |
| `GET /license` | platformMW | `GetLicense` | unconditionally `{"status":"not_required"}` | Platform | â€” | DEPRECATED (backend already retired for this hosted-SaaS product; frontend page removed in this PR) |
| `POST /license/validate` | platformMW | `ValidateLicense` | unconditionally `410 Gone` | Platform | â€” | DEPRECATED |
| `GET /console/reports` | platformMW | `AdminReports` | superseded reporting view | Platform | â€” | DUPLICATE_SUPERSEDED_ROUTE (superseded by `/platform/dashboard` + the six migrated feature pages) |
| `GET /console/internal/overview`, `GET /console/internal/tenants`, `GET /console/internal/domain-intelligence`, `GET /console/internal/security-ops`, `GET /console/internal/mail-flow-ops` (5 routes) | platformMW | `InternalOverview` / `InternalTenants` / `InternalDomainIntelligence` / `InternalSecurityOps` / `InternalMailFlowOps` | superseded internal-console views | Platform | â€” | DUPLICATE_SUPERSEDED_ROUTE (superseded by `/platform/dashboard`, Organizations, Mail Operations, and Security â€” the newer, tested pages) |

## Disaster Recovery (Milestone 13)

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /dr/readiness` | platformMW | `GetDRReadiness` | `dr.Readiness` | Platform | `internal/platform/dr` service tests | MISSING_UI (real route, no frontend consumer yet â€” backend-only in this pass) | UI_SUPPORTED (features/platform/dr/page.tsx readiness card; parity matrix 3.8.1 COMPLETE) |
| `GET /dr/drills` | platformMW | `GetDRDrills` | `{drills: dr.Drill[]}` | Platform | `internal/platform/dr` service tests | MISSING_UI | UI_SUPPORTED (features/platform/dr/page.tsx drills list) |
| `POST /dr/drills` | platformMW | `PostDRDrill` | records drill outcome, no restore performed | Platform | `internal/platform/dr` service tests | MISSING_UI | UI_SUPPORTED (features/platform/dr/page.tsx record-drill form) |
| `POST /dr/backup` | platformMW | `PostDRCoordinatedBackup` | durable-lease-coordinated backup over `backup.Service.CreateBackup` | Platform | `internal/platform/dr` service tests | MISSING_UI | UI_SUPPORTED (features/platform/dr/page.tsx coordinated backup action) |
| `POST /dr/backups/:id/restore` | platformMW | `PostDRCoordinatedRestore` | typed confirm `RESTORE-THIS-BACKUP`; submits to the same `restorecoord.Coordinator` as `POST /admin/backups/:id/restore` â€” no competing restart/rollback implementation | Platform | `internal/restorecoord` tests (shared coordinator) | MISSING_UI | UI_SUPPORTED (features/platform/dr/page.tsx restore with typed confirm RESTORE-THIS-BACKUP) |
| `GET /dr/operations` | platformMW | `GetDROperationHistory` | paginated `{operations: dr.Operation[], total, limit, offset}` â€” past coordinated backup/restore operations, newest first (distinct from the live single-job status below) | Platform | `internal/platform/dr` service tests, `internal/api/handlers` idempotency-replay test | MISSING_UI | UI_SUPPORTED (features/platform/dr/page.tsx operation history) |
| `GET /dr/operations/:job_id` | platformMW | `GetDROperationStatus` | reads the same `restorecoord` job result as `GET /admin/backups/restore-jobs/:job_id` | Platform | `internal/restorecoord` tests | MISSING_UI | UI_SUPPORTED (features/platform/dr/page.tsx operation status) |

## Retention / Legal Hold / Purge (Milestone 14)

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `POST /retention/policies` | platformMW | `PostRetentionPolicy` | `retention.Policy` | Platform | `internal/platform/retention` service tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx policy create; parity matrix 3.7 COMPLETE) |
| `GET /retention/policies/effective` | platformMW | `GetRetentionEffectivePolicy` | resolved `retention.Policy` for a scope | Platform | `internal/platform/retention` service tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx effective policy) |
| `POST /retention/legal-holds` | platformMW | `PostRetentionLegalHold` | `retention.LegalHold` | Platform | `internal/platform/retention` service tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx legal hold create) |
| `GET /retention/legal-holds` | platformMW | `GetRetentionLegalHolds` | `{holds: retention.LegalHold[]}` for a scope | Platform | `internal/platform/retention` service tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx legal holds list) |
| `POST /retention/legal-holds/:id/release` | platformMW | `PostRetentionLegalHoldRelease` | releases a hold | Platform | `internal/platform/retention` service tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx legal hold release) |
| `POST /retention/purge/plan` | platformMW | `PostRetentionPurgePlan` | non-destructive dry-run `retention.PurgePlan` | Platform | `internal/platform/retention` service tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx purge plan dry-run) |
| `POST /retention/purge/execute` | platformMW | `PostRetentionPurgeExecute` | destructive, typed confirm `PURGE-ELIGIBLE-DATA`, rechecks legal hold at execution time | Platform | `internal/platform/retention` service tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx purge execute, typed confirm PURGE-ELIGIBLE-DATA) |
| `POST /retention/mailboxes/:id/recover` | platformMW | `PostRetentionRecoverMailbox` | delegates to the existing `mailboxAdminSvc.RestoreMailbox` undelete capability (same one behind `POST /enterprise/mailboxes/:id/restore`), records chain-of-custody | Platform | `internal/admin/mailbox` restore tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx mailbox recover) |
| `GET /retention/custody` | platformMW | `GetRetentionCustody` | paginated `retention.ChainOfCustodyEvent[]` â€” IDs/hashes/metadata only, never message bodies | Platform | `internal/platform/retention` service tests | MISSING_UI | UI_SUPPORTED (features/platform/retention/page.tsx custody chain) |

## Platform Billing Balances/Adjustments (Milestone 15)

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /platform/billing/tenants/:tenant_id/overview` | platformMW | `GetPlatformBillingOverview` | coherent Platform Billing control-plane view: subscription + plan + billing period, live usage, invoice state, account balance, ledger adjustments, reconciliation, and honest payment/provider configuration (`configured:false` + note when no provider is wired â€” never MRR/cards/paid-invoice fabrication). Enterprise Completion Pass addition | Platform | `internal/api/handlers/platform_billing_overview_acceptance_test.go` | MISSING_UI (backend COMPLETE) | UI_SUPPORTED (platform-billing overview card: subscription/plan/period/usage/invoices + honest provider state; platform-billing/overview.test.tsx) |
| `GET /platform/billing/tenants/:tenant_id/balance` | platformMW | `GetPlatformBillingBalance` | `platformbilling.Balance` | Platform | `internal/platform/billing` service tests | MISSING_UI | UI_SUPPORTED (platform-billing page balance card; platform-billing/page.tsx + overview.test.tsx) |
| `POST /platform/billing/tenants/:tenant_id/adjustments` | platformMW | `PostPlatformBillingAdjustment` | `platformbilling.Adjustment`, integer minor units + currency only, idempotency-key supported | Platform | `internal/platform/billing` service tests (incl. concurrent-idempotency-key test) | MISSING_UI | UI_SUPPORTED (platform-billing page apply-adjustment form, idempotency-keyed; platform-billing/page.tsx) |
| `GET /platform/billing/tenants/:tenant_id/adjustments` | platformMW | `GetPlatformBillingAdjustments` | `{adjustments: platformbilling.Adjustment[]}` | Platform | `internal/platform/billing` service tests | MISSING_UI | UI_SUPPORTED (platform-billing page adjustment history; platform-billing/page.tsx) |
| `GET /platform/billing/tenants/:tenant_id/reconciliation` | platformMW | `GetPlatformBillingReconciliation` | read-only `billing.ReconciliationReport` â€” recomputes the ledger balance from full adjustment history and reports any discrepancy against the maintained balance row; never auto-corrects (Milestone 13-15 re-audit gap fix) | Platform | `internal/api/handlers` idempotency-replay test | MISSING_UI | UI_SUPPORTED (platform-billing page reconciliation banner; platform-billing/page.tsx) |

## Signed Update Artifacts (Milestone 13)

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `POST /updates/artifacts` | platformMW | `PostUpdateArtifact` | ed25519-signed manifest verification + hash check + staged lifecycle; rejects unsigned/tampered/wrong-version/wrong-platform artifacts | Platform | `internal/platform/updates/service_test.go` (unsigned, tampered, invalid signature, wrong version, wrong platform, valid end-to-end) | MISSING_UI |
| `GET /updates/artifacts/history` | platformMW | `GetUpdateArtifactHistory` | `{history: updates.Record[]}` | Platform | `internal/platform/updates/service_test.go` | MISSING_UI |
| `GET /updates/artifacts/:id` | platformMW | `GetUpdateArtifactStatus` | `updates.Record` | Platform | `internal/platform/updates/service_test.go` | MISSING_UI |
| `POST /updates/artifacts/:id/apply` | platformMW | `PostUpdateArtifactApply` | requires typed confirmation `APPLY-STAGED-UPDATE`; fails closed (503) unless the external `orvix-update.path`/`.service` coordinator is installed; hands the staged, verified artifact off to `internal/updatecoord` (durable job, mutually exclusive with rollback, idempotent retries) â€” never applies in-process | Platform | `internal/platform/updates/service_test.go` (`TestTriggerApply_NoCoordinator_LeavesStaged`, `TestTriggerApply_WithCoordinator_TransitionsToApplied`, `TestTriggerApply_RetryIsIdempotent`), `internal/updatecoord/coordinator_test.go` | MISSING_UI |
| `POST /updates/artifacts/:id/rollback` | platformMW | `PostUpdateArtifactRollback` | requires typed confirmation `ROLLBACK-APPLIED-UPDATE` + reason; hands off to the same external coordinator using pre-captured previous-version/hash metadata; fails closed if not installed | Platform | `internal/platform/updates/service_test.go` (`TestRollback_RetryIsIdempotent`, `TestRollback_WithoutApply_Rejected`) | MISSING_UI |
| `GET /updates/operations/:job_id` | platformMW | `GetUpdateOperationStatus` | durable apply/rollback job status, re-read from disk every call so it survives the Orvix restart the coordinator performs | Platform | `internal/updatecoord/coordinator_test.go` | MISSING_UI |
| `POST /incidents` | platformMW | `CreateIncident` | create a new incident with severity, services, and regions | Platform | `internal/incident/` | MISSING_UI | UI_SUPPORTED (features/platform/incidents/page.tsx create form; parity matrix 3.6 COMPLETE) |
| `GET /incidents` | platformMW | `ListIncidents` | list incidents with optional status filter | Platform | `internal/incident/` | MISSING_UI | UI_SUPPORTED (features/platform/incidents/page.tsx list with status filter) |
| `GET /incidents/:id` | platformMW | `GetIncident` | get an incident by ID | Platform | `internal/incident/` | MISSING_UI | UI_SUPPORTED (features/platform/incidents/page.tsx detail) |
| `PATCH /incidents/:id` | platformMW | `UpdateIncident` | update incident status with timeline event | Platform | `internal/incident/` | MISSING_UI | UI_SUPPORTED (features/platform/incidents/page.tsx status update) |
| `GET /incidents/:id/timeline` | platformMW | `GetIncidentTimeline` | get incident timeline events | Platform | `internal/incident/` | MISSING_UI | UI_SUPPORTED (features/platform/incidents/page.tsx timeline) |
| `POST /platform/support/grants` | platformMW | `CreateSupportAccessGrant` | request a temporary support-access grant for a tenant | Platform | `internal/supportaccess/` | MISSING_UI | UI_SUPPORTED (features/platform/support-access/page.tsx request grant form; parity matrix 5.2 COMPLETE) |
| `GET /platform/support/grants` | platformMW | `ListSupportAccessGrants` | list support-access grants by tenant | Platform | `internal/supportaccess/` | MISSING_UI | UI_SUPPORTED (features/platform/support-access/page.tsx grants list) |
| `GET /platform/support/grants/:id` | platformMW | `GetSupportAccessGrant` | get a support-access grant by ID | Platform | `internal/supportaccess/` | MISSING_UI | UI_SUPPORTED (features/platform/support-access/page.tsx grant detail) |
| `POST /platform/support/grants/:id/activate` | platformMW | `ActivateSupportAccessGrant` | activate an approved support-access grant | Platform | `internal/supportaccess/` | MISSING_UI | UI_SUPPORTED (features/platform/support-access/page.tsx activate action) |
| `POST /platform/support/grants/:id/revoke` | platformMW | `RevokeSupportAccessGrant` | revoke an active support-access grant | Platform | `internal/supportaccess/` | MISSING_UI | UI_SUPPORTED (features/platform/support-access/page.tsx revoke action, reasoned) |
| `POST /platform/automation/jobs` | platformMW | `SubmitPlatformAutomationJob` | submit an explicitly platform-scoped allowlisted durable job; requires idempotency key | Platform | `internal/platform/jobs/` | MISSING_UI | UI_SUPPORTED (features/platform/automation-jobs/page.tsx submit form, idempotency-keyed; parity matrix 3.2 COMPLETE) |
| `GET /platform/automation/jobs` | platformMW | `ListPlatformAutomationJobs` | paginated and filterable platform-job history | Platform | `internal/platform/jobs/` | MISSING_UI | UI_SUPPORTED (features/platform/automation-jobs/page.tsx paginated list) |
| `GET /platform/automation/jobs/:id` | platformMW | `GetPlatformAutomationJob` | safe durable job detail without payload, lease, or idempotency internals | Platform | `internal/platform/jobs/` | MISSING_UI | UI_SUPPORTED (features/platform/automation-jobs/page.tsx job detail) |
| `POST /platform/automation/jobs/:id/cancel` | platformMW | `CancelPlatformAutomationJob` | atomically cancel queued work or request cooperative cancellation of running work | Platform | `internal/platform/jobs/` | MISSING_UI | UI_SUPPORTED (features/platform/automation-jobs/page.tsx cancel action) |
| `POST /platform/automation/jobs/:id/retry` | platformMW | `RetryPlatformAutomationJob` | idempotently requeue an eligible failed platform job | Platform | `internal/platform/jobs/` | MISSING_UI | UI_SUPPORTED (features/platform/automation-jobs/page.tsx retry action) |
| `GET /platform/capabilities` | platformMW | `GetCapabilities` | runtime capability registry derived from registered modules and health | Platform | `internal/capability/` | MISSING_UI |
| `GET /platform/config` | platformMW | `ListConfigurationSettings` | list authoritative configuration settings with source/effective/pending state | Platform | `internal/configtruth/` | MISSING_UI | UI_SUPPORTED (features/platform/config-truth/page.tsx settings list; parity matrix 6.3 COMPLETE) |
| `GET /platform/config/:key` | platformMW | `GetConfigurationSetting` | get authoritative view of one setting | Platform | `internal/configtruth/` | MISSING_UI | UI_SUPPORTED (features/platform/config-truth/page.tsx single setting detail) |
| `PATCH /platform/config/:key` | platformMW | `MutateConfigurationSetting` | validate and apply a configuration mutation with optimistic concurrency | Platform | `internal/configtruth/` | MISSING_UI | UI_SUPPORTED (features/platform/config-truth/page.tsx mutate with optimistic concurrency) |

## Imports (Milestone 16, Phase 4B)

Bulk tenant provisioning via staged CSV/JSON sources. All nine routes
are backend-only in this pass (no console UI consumer yet): the
importer is driven through the API and the `orvix` CLI. The full
import lifecycle is covered by `internal/platform/importer`
service/worker tests and `internal/api/handlers/import_route_acceptance_test.go`
(tenant isolation, RBAC, CSRF, confirmation strings, idempotency,
hash verification, safe-field updates, compensation).

| `GET /platform/imports` | platformMW | `ListImports` | paginated/filtered import history, tenant-scoped | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx job list; parity matrix 3.1 COMPLETE) |
| `GET /platform/imports/:id` | platformMW | `GetImport` | import job detail without staging/lease/idempotency internals | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx import detail) |
| `GET /platform/imports/:id/report` | platformMW | `GetImportReport` | dry-run validation report with before/after diffs and redacted secrets | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx validation report view) |
| `POST /platform/imports` | platformMW | `CreateImport` | stage a new import source (SHA-256 verified at every later read) | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx stage-new-import form, Idempotency-Key required) |
| `POST /platform/imports/:id/validate` | platformMW | `ValidateImport` | dry-run validation with zero mutations | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx validate dry-run action) |
| `POST /platform/imports/:id/execute` | platformMW | `ExecuteImport` | durable queued-activation handoff; requires `Idempotency-Key` and exact confirmation `EXECUTE-IMPORT-<id>` | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx execute with typed confirm EXECUTE-IMPORT-<id>) |
| `POST /platform/imports/:id/resume` | platformMW | `ResumeImport` | idempotent resume of a paused/failed import; requires `Idempotency-Key` | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx resume action) |
| `POST /platform/imports/:id/cancel` | platformMW | `CancelImport` | cancel a running/paused import | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx cancel action) |
| `POST /platform/imports/:id/compensate` | platformMW | `CompensateImport` | reverse the import's own mutations only; requires `Idempotency-Key` and exact confirmation `COMPENSATE-IMPORT-<id>`; refuses to overwrite human changes | Platform | `internal/api/handlers/import_route_acceptance_test.go`, `internal/platform/importer/` | MISSING_UI | UI_SUPPORTED (features/platform/imports/page.tsx compensate with typed confirm COMPENSATE-IMPORT-<id>) |

## Platform Mail Control (Mail-Control enablement)

Platform Super Admin mail-control surface over the production admin
services. Every route requires an explicit target `tenant_id` in the
path and is platformMW-gated (tenant roles are denied); all mutations
are RBAC-permissioned, audited, and tenant-scoped in SQL.

| `GET /platform/domains/:tenant_id` | platformMW | `ListPlatformDomains` | paginated platform domain list for an explicit tenant; search/status filters | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/domains/page.tsx + DomainDetailDrawer; domains/page.test.tsx) |
| `GET /platform/domains/:tenant_id/:id` | platformMW | `GetPlatformDomain` | domain detail with counts and mail-access mode, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/domains/page.tsx + DomainDetailDrawer) |
| `POST /platform/domains/:tenant_id` | platformMW | `CreatePlatformDomain` | transactional domain provisioning for an explicit tenant (canonical admin service; plan/limit enforcement, optional DKIM, DNS requirements via dnsops, outbox evidence; Idempotency-Key required) | Platform | `internal/platform/mailcontrol/service_test.go`, `internal/api/handlers/platform_provisioning.go` | MISSING_UI | UI_SUPPORTED (features/platform/domains CreateDomainDialog; domains/page.test.tsx) |
| `POST /platform/domains/:tenant_id/:id/status` | platformMW | `SetPlatformDomainStatus` | allowed lifecycle transition, tenant-scoped, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (DomainDetailDrawer lifecycle status control; domains/page.test.tsx) |
| `POST /platform/domains/:tenant_id/:id/mail-access-mode` | platformMW | `SetPlatformDomainMailAccessMode` | set canonical SMTP mail-access mode (`internal_only`/`internal_external`), audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (DomainDetailDrawer mail-access-mode control) |
| `POST /platform/domains/:tenant_id/:id/deactivate` | platformMW | `DeactivatePlatformDomain` | canonical domain deactivation/soft-delete lifecycle (`PermPlatformDomainsDeactivate`; typed confirmation, optimistic concurrency, dependency checks against active mailboxes/aliases/queued mail, never touches `deleted_at` or DKIM evidence; Idempotency-Key required) | Platform | `internal/api/handlers/platform_domain_lifecycle_acceptance_test.go` | MISSING_UI | UI_SUPPORTED (DomainDetailDrawer deactivate, typed confirm; domains/page.test.tsx) |
| `POST /platform/domains/:tenant_id/:id/delete` | platformMW | `DeletePlatformDomain` | canonical, audited, PERMANENT `deleted_at`-tombstone delete distinct from deactivate above (`PermPlatformDomainsDelete`; typed confirmation, optimistic concurrency, requires prior canonical deactivation via `deactivated_at`, blocks with structured dependency counts on active mailboxes/aliases/queued mail, purges the active DKIM config while preserving DKIM/audit history, never mutates public DNS, Idempotency-Key required) | Platform | `internal/api/handlers/platform_domain_lifecycle_acceptance_test.go`, `web/admin/src/features/platform/domains/page.test.tsx` | UI_SUPPORTED |
| `GET /platform/domains/:tenant_id/:id/dns` | platformMW | `GetPlatformDomainDNS` | read-only public DNS/DKIM snapshot for an existing domain (never generates a key; DNS requirements reuse the same dnsops generator `CreatePlatformDomain` uses) | Platform | `internal/api/handlers/platform_domain_lifecycle_acceptance_test.go` | MISSING_UI | UI_SUPPORTED (DomainDetailDrawer DNS Setup tab live snapshot; domains/page.test.tsx) |
| `POST /platform/domains/:tenant_id/:id/dns/verify` | platformMW | `VerifyPlatformDomainDNS` | read-only live public-DNS verification of every record `GetPlatformDomainDNS` presents, via the shared `dnsops.Service.Generate`/`Verify` (the same `Verifier` the existing admin DNS verify route uses); external DNS lookups only â€” never mutates public DNS, never generates/rotates DKIM, never modifies the domain; DKIM is compared against the CURRENT configured key read fresh on every call; `DomainDetailDrawer.tsx`'s DNS Setup tab auto-triggers it once per domain (gated to `activeTab === "dns"`) and offers an explicit "Re-check DNS" action, rendering per-record Matched/Mismatch/Missing/Check failed status with Expected/Actual on mismatch | Platform | `internal/api/handlers/platform_dns_verify_test.go`, `internal/dnsops/verifier_test.go`, `web/admin/src/features/platform/domains/page.test.tsx`, `web/admin/tests/e2e/platform-domains-contract.spec.ts` | UI_SUPPORTED |
| `POST /platform/domains/:tenant_id/:id/dkim/generate` | platformMW | `GeneratePlatformDomainDKIM` | canonical, version-guarded DKIM generation for a domain with none configured (`PlatformDKIMForTenant`; tenant-scoped resolution, optimistic concurrency, Idempotency-Key required) | Platform | `internal/api/handlers/platform_domain_lifecycle_acceptance_test.go` | MISSING_UI | UI_SUPPORTED (DomainDetailDrawer DKIM tab generate; domains/page.test.tsx) |
| `POST /platform/domains/:tenant_id/:id/dkim/rotate` | platformMW | `RotatePlatformDomainDKIM` | canonical, version-guarded DKIM rotation requiring `confirm_rotation: "rotate-dkim-key"` (`PlatformDKIMForTenant`; tenant-scoped resolution, optimistic concurrency, Idempotency-Key required) | Platform | `internal/api/handlers/platform_domain_lifecycle_acceptance_test.go` | MISSING_UI | UI_SUPPORTED (DomainDetailDrawer DKIM tab rotate, typed confirm; domains/page.test.tsx) |
| `POST /platform/domains/:tenant_id/:id/dkim/revoke` | platformMW | `RevokePlatformDomainDKIM` | canonical DKIM revoke through the SAME transactional path the tenant console uses (config disabled, domain DKIM state cleared, DKIM selector-history entry `revoked` + audit; never exposes key material, never mutates public DNS); tenant-scoped; no frontend consumer yet (Enterprise Completion Pass addition) | Platform | `internal/api/handlers/platform_dkim_revoke_acceptance_test.go` | MISSING_UI (backend COMPLETE) | UI_SUPPORTED (DomainDetailDrawer DKIM tab revoke beside generate/rotate, repeat revoke no-op; DkimRevoke.test.tsx) |
| `POST /platform/users/:id/deactivate` | platformMW | `DeactivatePlatformUser` | canonical, audited deactivation of another platform-scoped user account (`PermPlatformUsersWrite`; blocks self-targeting, revokes sessions/API keys/MFA recovery codes/MFA challenges, bumps `token_version`; Idempotency-Key required) | Platform | `internal/api/handlers/platform_user_lifecycle_acceptance_test.go` | MISSING_UI |
| `GET /platform/mailboxes/:tenant_id` | platformMW | `ListPlatformMailboxes` | paginated platform mailbox list for an explicit tenant/domain | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/mailboxes/page.tsx list; mailboxes/page.test.tsx) |
| `GET /platform/mailboxes/:tenant_id/:id` | platformMW | `GetPlatformMailbox` | mailbox detail with configured + effective mail-access mode, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/mailboxes/page.tsx + MailboxDetailDrawer) |
| `POST /platform/mailboxes/:tenant_id` | platformMW | `CreatePlatformMailbox` | secure mailbox provisioning (REQUIRED explicit `mail_access_mode`, Argon2id via canonical hasher, in-transaction folder provisioning, audit/outbox; password never returned; Cache-Control: no-store; Idempotency-Key required) | Platform | `internal/platform/mailcontrol/service_test.go`, `internal/api/handlers/platform_provisioning.go` | MISSING_UI | UI_SUPPORTED (features/platform/mailboxes CreateMailboxDialog, explicit mail_access_mode; mailboxes/page.test.tsx) |
| `POST /platform/mailboxes/:tenant_id/:id/access-mode` | platformMW | `SetPlatformMailboxAccessMode` | guarded per-mailbox access-mode mutation (`expected_version` optimistic concurrency, tenant predicate in SQL, audit/outbox evidence; Idempotency-Key required) | Platform | `internal/platform/mailcontrol/service_test.go`, `internal/api/handlers/platform_provisioning.go` | MISSING_UI | UI_SUPPORTED (MailboxDetailDrawer access-mode control) |
| `POST /platform/mailboxes/:tenant_id/:id/status` | platformMW | `SetPlatformMailboxStatus` | mailbox lifecycle transition, tenant-scoped, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (MailboxDetailDrawer status control; mailboxes/page.test.tsx) |
| `POST /platform/mailboxes/:tenant_id/:id/quota` | platformMW | `SetPlatformMailboxQuota` | quota update with domain-bound ceiling, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (MailboxDetailDrawer quota control) |
| `POST /platform/mailboxes/:tenant_id/:id/reset-password` | platformMW | `ResetPlatformMailboxPassword` | secure one-time password reset via the production service, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (MailboxDetailDrawer reset-password one-time reveal; mailboxes/page.test.tsx) |
| `DELETE /platform/mailboxes/:tenant_id/:id` | platformMW | `DeletePlatformMailbox` | soft-delete with typed confirmation `PURGE-MAILBOX-<id>`, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (MailboxDetailDrawer safe delete, typed confirm PURGE-MAILBOX-<id>; mailboxes/page.test.tsx) |
| `POST /platform/mailboxes/:tenant_id/bulk/status` | platformMW | `BulkPlatformMailboxStatus` | bounded bulk status transition (max 500 ids), tenant-scoped, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (mailboxes page bulk status action; mailboxes/page.test.tsx) |
| `POST /platform/mailboxes/:tenant_id/:id/support-view` | platformMW | `StartMailboxSupportView` | starts an audited, read-only, time-boxed support session (`PermPlatformMailboxSupportView`; typed confirmation `ACCESS-MAILBOX-<id>`, ticket/reason required, default 30m/max 60m expiry, mailbox password never read; response never contains a password/hash/reusable token); `AccessMailboxDialog.tsx`'s "Access mailbox" action on the mailbox detail drawer | Platform | `internal/api/handlers/platform_mailbox_support_view_test.go`, `web/admin/src/features/platform/mailboxes/page.test.tsx` | UI_SUPPORTED |
| `GET /platform/mailboxes/:tenant_id/:id/support-view/:session_id/folders` | platformMW | `ListMailboxSupportFolders` | read-only folder list for the session's bound mailbox; fails closed on any operator/tenant/mailbox mismatch or expired/ended/revoked session; `SupportMailboxViewer.tsx`'s folder sidebar | Platform | `internal/api/handlers/platform_mailbox_support_view_test.go`, `web/admin/src/features/platform/mailboxes/page.test.tsx` | UI_SUPPORTED |
| `GET /platform/mailboxes/:tenant_id/:id/support-view/:session_id/messages` | platformMW | `ListMailboxSupportMessages` | read-only message list/search for the session's bound mailbox; `SupportMailboxViewer.tsx`'s message list | Platform | `internal/api/handlers/platform_mailbox_support_view_test.go`, `web/admin/src/features/platform/mailboxes/page.test.tsx` | UI_SUPPORTED |
| `GET /platform/mailboxes/:tenant_id/:id/support-view/:session_id/messages/:message_id` | platformMW | `GetMailboxSupportMessage` | read-only message body/headers/attachment list; never calls `UpdateFlags` (never marks a customer message seen as a side effect); `SupportMailboxViewer.tsx`'s message detail pane | Platform | `internal/api/handlers/platform_mailbox_support_view_test.go` | MISSING_UI | UI_SUPPORTED (SupportMailboxViewer message detail pane; parity matrix 4.2.8 COMPLETE) |
| `GET /platform/mailboxes/:tenant_id/:id/support-view/:session_id/messages/:message_id/attachments/:attachment_id` | platformMW | `GetMailboxSupportAttachment` | read-only attachment download, scoped to the session's message; `SupportMailboxViewer.tsx` renders a download link but it is not yet exercised by an automated test | Platform | `internal/api/handlers/platform_mailbox_support_view_test.go` | MISSING_UI | MISSING_UI (attachment download link rendered but not exercised by an automated test — parity matrix MISSING_TEST) |
| `POST /platform/mailboxes/:tenant_id/:id/support-view/:session_id/end` | platformMW | `EndMailboxSupportView` | operator-initiated session end, audited, idempotent; `SupportMailboxViewer.tsx`'s "End access" action | Platform | `internal/api/handlers/platform_mailbox_support_view_test.go`, `web/admin/src/features/platform/mailboxes/page.test.tsx` | UI_SUPPORTED |
| `GET /platform/aliases/:tenant_id` | platformMW | `ListPlatformAliases` | paginated alias list for an explicit tenant/domain | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/aliases/page.tsx list; aliases/page.test.tsx) |
| `GET /platform/aliases/:tenant_id/:id` | platformMW | `GetPlatformAlias` | alias detail, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/aliases/page.tsx detail) |
| `POST /platform/aliases/:tenant_id` | platformMW | `CreatePlatformAlias` | create alias with domain ownership, loop rejection, conflict detection, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/aliases/page.tsx create; aliases/page.test.tsx) |
| `DELETE /platform/aliases/:tenant_id/:id` | platformMW | `DeletePlatformAlias` | soft-delete alias, tenant-scoped, audited | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/aliases/page.tsx delete; aliases/page.test.tsx) |
| `GET /platform/groups/:tenant_id` | platformMW | `ListPlatformGroups` | paginated group list for an explicit tenant | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/groups/page.tsx inventory; groups/page.test.tsx) |
| `GET /platform/groups/:tenant_id/:id` | platformMW | `GetPlatformGroup` | group detail with member count, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/groups/page.tsx detail) |
| `GET /platform/groups/:tenant_id/:id/members` | platformMW | `ListPlatformGroupMembers` | group member emails, tenant-scoped | Platform | `internal/platform/mailcontrol/service_test.go` | MISSING_UI | UI_SUPPORTED (GroupMembersDrawer members list; groups/page.test.tsx) |
| `POST /platform/groups/:tenant_id` | platformMW | `CreatePlatformGroup` | group creation in the SAME `coremail_groups` table the tenant self-service Groups page uses (name required â‰¤64 chars, duplicate (tenant_id,name) = 409, audited) â€” Enterprise Completion Pass addition | Platform | `internal/api/handlers/platform_groups_crud_acceptance_test.go` | MISSING_UI (backend COMPLETE) | UI_SUPPORTED (CreateGroupDialog; groups/page.test.tsx) |
| `DELETE /platform/groups/:tenant_id/:id` | platformMW | `DeletePlatformGroup` | soft-delete (deleted_at tombstone) requiring typed X-Confirm `DELETE-GROUP-<id>`, audited â€” Enterprise Completion Pass addition | Platform | `internal/api/handlers/platform_groups_crud_acceptance_test.go` | MISSING_UI (backend COMPLETE) | UI_SUPPORTED (groups page delete with typed X-Confirm DELETE-GROUP-<id>; groups/page.test.tsx) |
| `POST /platform/groups/:tenant_id/:id/members` | platformMW | `AddPlatformGroupMember` | member add validated + duplicate (group_id,email) = 409; group ownership predicate in SQL â€” Enterprise Completion Pass addition | Platform | `internal/api/handlers/platform_groups_crud_acceptance_test.go` | MISSING_UI (backend COMPLETE) | UI_SUPPORTED (GroupMembersDrawer add member; groups/page.test.tsx) |
| `DELETE /platform/groups/:tenant_id/:id/members/:member_id` | platformMW | `RemovePlatformGroupMember` | member removal scoped through the group's tenant ownership â€” Enterprise Completion Pass addition | Platform | `internal/api/handlers/platform_groups_crud_acceptance_test.go` | MISSING_UI (backend COMPLETE) | UI_SUPPORTED (GroupMembersDrawer remove member; groups/page.test.tsx) |
| `GET /platform/suppressions/:tenant_id` | platformMW | `ListPlatformSuppressions` | paginated/filterable suppression list (domain/state/reason/source/ranges), default active-only | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/suppressions/page.tsx list; suppressions/page.test.tsx) |
| `POST /platform/suppressions/:tenant_id` | platformMW | `AddPlatformSuppression` | create a reasoned, tenant-scoped suppression (atomic upsert, idempotent); audited | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/suppressions/page.tsx add, reasoned) |
| `GET /platform/suppressions/:tenant_id/:id` | platformMW | `GetPlatformSuppression` | suppression detail with state/version/release fields, tenant-scoped | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/suppressions/page.tsx detail) |
| `GET /platform/suppressions/:tenant_id/:id/history` | platformMW | `GetPlatformSuppressionHistory` | append-only lifecycle evidence (created/released/reactivated/expired) | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/suppressions/page.tsx history) |
| `POST /platform/suppressions/:tenant_id/:id/release` | platformMW | `ReleasePlatformSuppression` | guarded active->released transition, audited, history-recorded | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/suppressions/page.tsx release, typed confirm) |
| `POST /platform/suppressions/:tenant_id/:id/reactivate` | platformMW | `ReactivatePlatformSuppression` | guarded terminal->active transition (policy permits), audited | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/suppressions/page.tsx reactivate) |
| `DELETE /platform/suppressions/:tenant_id/:id` | platformMW | `DeletePlatformSuppression` | release semantics with typed confirmation (RELEASE-SUPPRESSION-<id>) | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/suppressions/page.tsx delete) |
| `DELETE /platform/suppressions/:tenant_id` | platformMW | `RemovePlatformSuppression` | release an active suppression by address; audited | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/suppressions/page.tsx delete by address) |
| `GET /platform/deliverability/:tenant_id/metrics` | platformMW | `GetPlatformDeliverabilityMetrics` | totals + real rates + failure/domain/provider breakdowns + UTC time buckets | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/deliverability/page.tsx metrics; deliverability/page.test.tsx) |
| `GET /platform/deliverability/:tenant_id/events` | platformMW | `ListPlatformDeliverabilityEvents` | real delivery evidence, filters (domain/type/provider/time), bounded pagination, safe projection | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/deliverability/page.tsx events) |
| `GET /platform/deliverability/:tenant_id/events/:id` | platformMW | `GetPlatformDeliverabilityEvent` | one event detail, tenant-scoped, safe projection | Platform | `internal/platform/deliverability/lifecycle_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/deliverability/page.tsx event detail) |

## Platform Relay Administration (Mail-Control Phase B)

Production outbound relay endpoints (the same providers the delivery
worker routes through). Credentials are encrypted at rest and never
returned; mutations are idempotency-keyed, version-guarded, and
typed-confirmation gated; connectivity tests are SSRF/DNS-rebinding
safe.

| `GET /platform/relays` | platformMW | `ListPlatformRelays` | paginated relay list with scope/tenant/domain/active/search filters, redacted | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx list; relay/page.test.tsx) |
| `GET /platform/relays/:id` | platformMW | `GetPlatformRelay` | relay detail, redacted, with last safe test outcome | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx detail, redacted) |
| `POST /platform/relays` | platformMW | `CreatePlatformRelay` | create relay endpoint (credential encrypted at rest), idempotent | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx create) |
| `PATCH /platform/relays/:id` | platformMW | `UpdatePlatformRelay` | guarded versioned update, idempotent | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx update, version-guarded) |
| `POST /platform/relays/:id/enable` | platformMW | `EnablePlatformRelay` | enable relay for routing, version-guarded, idempotent, audited | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx enable) |
| `POST /platform/relays/:id/disable` | platformMW | `DisablePlatformRelay` | disable relay, typed confirmation + version + idempotency, audited | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx disable, typed confirm) |
| `POST /platform/relays/:id/rotate-credentials` | platformMW | `RotatePlatformRelayCredentials` | rotate credential (generated once if not supplied), typed confirmation, idempotent | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx rotate-credentials, typed confirm) |
| `POST /platform/relays/:id/test` | platformMW | `TestPlatformRelay` | SSRF/DNS-rebinding-safe connection test, bounded timeouts, idempotent, redacted | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx test connectivity — real result only) |
| `DELETE /platform/relays/:id` | platformMW | `DeletePlatformRelay` | delete relay, typed confirmation, audited | Platform | `internal/platform/relay/admin_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/relay/page.tsx delete, typed confirm) |

## Bulk Mailbox Provisioning (Milestone 17)

Platform Super Admin bulk CSV/XLSX mailbox import, backend-only in
this pass (no console UI consumer yet â€” driven through the API only).
Every route requires an explicit target `tenant_id` in the path
(never inferred as tenant 0) and is platformMW-gated (tenant roles are
denied); mutations are RBAC-permissioned (`PermMailboxesWrite`), the
staging/validate/create-job/execute mutations require an
`Idempotency-Key`, execution is durable/checkpointed/resumable via the
generic `internal/platform/jobs` framework, and lifecycle state
transitions carry transactional audit/outbox evidence
(`internal/platform/bulkprovision.Service.finalizeLifecycleTx`). The
template route (`GET`, `PermMailboxesRead`) is the only read-only,
non-tenant-scoped route in this group â€” it returns a generated
CSV/XLSX template, not tenant data. Covered by
`internal/platform/bulkprovision/*_test.go` (parsing/security,
durability, audit/outbox failure-injection) and
`internal/api/handlers/platform_bulk_mailboxes_acceptance_test.go`
(real router, real middleware: RBAC, CSRF, tenant isolation,
idempotency replay, redaction, dry-run-then-execute).

| Route | Middleware | Handler | Contract | Owner | Test file | Disposition |
|---|---|---|---|---|---|---|
| `GET /platform/mailboxes/bulk/template` | platformMW | `GetPlatformBulkMailboxTemplate` | generated CSV/XLSX import template, `?format=csv\|xlsx`, `Cache-Control: no-store`, not tenant-scoped (static content) | Platform | `platform_bulk_mailboxes_acceptance_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx template download; bulk-mailboxes/page.test.tsx) |
| `POST /platform/mailboxes/bulk/:tenant_id/stage` | platformMW | `PostPlatformBulkMailboxStage` | bounded (8 MiB) multipart upload, content-sniffed (magic bytes vs. extension), parsed and security-checked (formula injection, macro/path-traversal, unknown/duplicate headers) before staging; confined server-generated staging ID + SHA-256 hash, never a filesystem path; Idempotency-Key required | Platform | `platform_bulk_mailboxes_acceptance_test.go`, `internal/platform/bulkprovision/parse_security_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx stage upload, Idempotency-Key) |
| `POST /platform/mailboxes/bulk/:tenant_id/validate` | platformMW | `PostPlatformBulkMailboxValidate` | pure dry-run against the hash-verified staged bytes (never a client-supplied row list); zero mailbox/folder/quota mutation | Platform | `platform_bulk_mailboxes_acceptance_test.go`, `internal/platform/bulkprovision/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx validate dry-run) |
| `POST /platform/mailboxes/bulk/:tenant_id/jobs` | platformMW | `PostPlatformBulkMailboxCreateJob` | re-validates server-side against the hash-verified staged bytes and persists the durable job in "ready" state; conflict policy `fail`\|`skip_existing` only (never silently converts create into update); Idempotency-Key required; still zero mailbox mutation | Platform | `platform_bulk_mailboxes_acceptance_test.go`, `internal/platform/bulkprovision/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx create job) |
| `GET /platform/mailboxes/bulk/:tenant_id/jobs` | platformMW | `GetPlatformBulkMailboxJobs` | paginated (bounded limit/offset) job list, tenant-scoped | Platform | `internal/platform/bulkprovision/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx job list) |
| `GET /platform/mailboxes/bulk/:tenant_id/jobs/:jobId` | platformMW | `GetPlatformBulkMailboxJob` | job detail (counts/status/checkpoint), tenant-scoped, no staging path/lease/secret in response | Platform | `platform_bulk_mailboxes_acceptance_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx job detail) |
| `GET /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/rows` | platformMW | `GetPlatformBulkMailboxJobRows` | paginated per-row result report, tenant-scoped, no secret material | Platform | `platform_bulk_mailboxes_acceptance_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx per-row results) |
| `POST /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/execute` | platformMW | `PostPlatformBulkMailboxExecute` | submits for DURABLE ASYNC execution on `internal/platform/jobs` (never runs inline); returns `202 Accepted` + durable job reference; Idempotency-Key required, exactly-once submission | Platform | `platform_bulk_mailboxes_acceptance_test.go`, `internal/platform/bulkprovision/execute_durability_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx execute, Idempotency-Key, 202 durable) |
| `POST /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/cancel` | platformMW | `PostPlatformBulkMailboxCancel` | cooperative cancellation, version-guarded transition, transactional lifecycle audit/outbox evidence | Platform | `internal/platform/bulkprovision/execute_audit_outbox_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx cancel) |
| `POST /platform/mailboxes/bulk/:tenant_id/jobs/:jobId/retry` | platformMW | `PostPlatformBulkMailboxRetry` | idempotent re-attempt of only the rows left `RowFailed`; never re-touches already-succeeded rows | Platform | `internal/platform/bulkprovision/service_test.go` | MISSING_UI | UI_SUPPORTED (features/platform/bulk-mailboxes/page.tsx retry failed rows) |

## Theme system (cross-cutting, not a route)

Verified by `web/admin/src/shared/theme/{theme,useTheme}.test.ts`,
`noHardcodedColors.test.ts` (static scan, zero hardcoded colors in
application code), and `test/playwright/portal.spec.ts`'s merged
Light/Dark coverage for both PSA and Tenant Admin.

## Summary counts

This is the single canonical summary for this document â€” a prior
draft of this file briefly carried two contradictory "Summary counts"
sections (59/5/3/11/19/3=100 vs. 62/6/3/11/17/2=101); that was a
real defect in the document itself, not just a typo, and
`internal/api/capability_matrix_test.go` now fails the build if a
second "## Summary counts" heading, or a route missing from either
side, is ever reintroduced.

Route-level, not row-level â€” several rows above group multiple route
registrations under one disposition (the 9 legacy `/backups*`
`LegacyGone` routes, the 5 `/console/internal/*` routes, the 4
`/admin/mfa/*` routes, the `GET /admin/fs/browse`+`GET /admin/fs/read`
pair, and the combined `GET`+`PATCH /admin/settings/protocol/:protocol`
pair). Every number below was recomputed directly from the table rows
above (not carried over from an earlier draft) and is enforced equal
to the router's actual route set by
`internal/api/capability_matrix_test.go`, which parses
`platformMW[0], platformMW[1]` registrations straight out of
`router.go` â€” currently 231 â€” and parses every `` `METHOD /path` ``
occurrence and its row's disposition straight out of this document.

| Disposition | Routes |
|---|---|
| UI_SUPPORTED | 182 |
| READ_ONLY_STATUS | 4 |
| MACHINE_ONLY | 3 |
| DEPRECATED | 12 |
| DUPLICATE_SUPERSEDED_ROUTE | 18 |
| MISSING_UI | 12 |
| MISSING_BACKEND | 0 (the one MISSING_BACKEND case — platform-initiated organization creation — is documented under Organizations; it is now a real route, closed by the Enterprise Completion Pass) |
| **Total** | **231** |

Phase 3 (Frontend) re-disposition: every route whose page/action the
Enterprise Product Completion Pass frontend agent completed (or that
was verified wired by the Phase 1 parity-matrix audit but still marked
MISSING_UI here) moved to UI_SUPPORTED — PSA organization creation,
platform groups CRUD ×4, platform billing overview, platform DKIM
revoke, queue history/export/bulk-action, backup schedule editing,
audit export, monitoring health via the canonical client (the
DIRECT_FETCH_BYPASS in SystemHealth.tsx is fixed), and the Mail
Control / DR / Retention / Incidents / Support Access / Automation
Jobs / Config Truth / Imports / Platform Billing surfaces that already
had real pages (see the platform + organization parity matrices).
The 12 remaining MISSING_UI rows are intentional: `GET /admin/backups/:id`
(list view already shows every field), `GET /audit/logs/:id` (list
pages render rows; no detail action consumes it yet),
`GET /admin/runtime` (overlaps Health; not the same data), the six
signed update-artifact routes (operator/machine-only by design —
release engineering, not an admin form), `GET /platform/capabilities`
(no consumer), `POST /platform/users/:id/deactivate` (no consumer),
and the support-view attachment download (rendered but not yet
exercised by an automated test — MISSING_TEST per the parity matrix).

DEPRECATED is 12, not 11, because `POST /firewall/rules` moved from
UI_SUPPORTED to DEPRECATED / NOT_OPERATIONAL in this pass (see
Security, above) â€” the legacy rule-creation UI was retired rather
than left pointing at a non-enforcing write.
