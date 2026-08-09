# Platform Console Capability Matrix

Rebuilt from the current code in this PR (branch
`feature/complete-platform-operations-console`). Every row below was
verified against the real Go handler and the real frontend file/test
that consume it during this session's work — not inferred from route
names.

**Scope note:** this matrix covers the six domains that were migrated
to the SOLID feature-directory pattern in this PR (Overview,
Organizations, Mail Operations, Reliability, Security, Configuration)
plus the routes discovered to be genuinely unwired (UI gaps) or
machine-only while investigating those six. It does not re-enumerate
every `platformMW`-gated route in `internal/api/router.go` from
scratch — some platform routes outside these six domains (e.g.
`/modules`, `/monitoring/health`) predate this PR and were not
re-audited here.

## Overview

| Route | Frontend feature | Backend handler | Owner | Operations | Test file |
|---|---|---|---|---|---|
| `GET /platform/dashboard` | `features/platform/overview/page.tsx` | `PlatformDashboard` (dashboard_admin.go) | Platform | read | `features/platform/overview/page.test.tsx` |

## Organizations

| Route | Frontend feature | Backend handler | Owner | Operations | Test file |
|---|---|---|---|---|---|
| `GET /platform/organizations` | `features/platform/organizations/page.tsx` | `ListPlatformOrganizations` (platform_admin.go) | Platform | read, search, paginate | `features/platform/organizations/page.test.tsx` |
| `GET /platform/organizations/:id/detail` | `.../components/OrganizationDetailDrawer.tsx` | `GetOrganizationDetail` (organization_admin.go) | Platform | read | `page.test.tsx` |
| `POST /platform/organizations/:id/active` | `.../components/OrganizationDetailDrawer.tsx` | `SetOrganizationActive` (organization_admin.go) | Platform | mutate (typed confirm) | `page.test.tsx` |
| `PATCH /platform/organizations/:id` | `.../components/OrganizationEditForm.tsx` | `UpdateOrganization` (organization_admin.go) | Platform | mutate (diff-only) | `page.test.tsx` |
| `GET /platform/organizations/:id` | none | `GetPlatformOrganization` (platform_admin.go) | Platform | — | UI_GAP: returns an untyped `map[string]interface{}` from a different service than the typed detail endpoint above; unused by design, not removed (still a real route). |
| `POST /platform/organizations` | none | — | — | — | MISSING_BACKEND_CAPABILITY: not a registered route. Organizations are created via tenant signup, not a platform-admin action. |

## Mail Operations

| Route | Frontend feature | Backend handler | Owner | Operations | Test file |
|---|---|---|---|---|---|
| `GET /admin/queue/summary` | `features/platform/mail-operations/components/QueueSummaryCards.tsx` | `AdminQueueSummary` (handlers.go) | Platform | read | `page.test.tsx` |
| `GET /admin/queue/messages` | `.../components/QueueTable.tsx` | `AdminQueueList` (admin_queue.go) | Platform | read, filter (status/from/to), paginate | `page.test.tsx` |
| `GET /admin/queue/messages/:id` | `.../components/QueueDetailDrawer.tsx` | `AdminQueueDetail` (admin_queue.go) | Platform | read | `page.test.tsx` |
| `POST /admin/queue/messages/:id/retry` | `page.tsx` (table row action) | `AdminQueueRetryNow` (admin_queue.go) | Platform | mutate | `page.test.tsx` |
| `POST /admin/queue/messages/:id/bounce` | `page.tsx` (typed confirm) | `AdminQueueBounce` (admin_queue.go) | Platform | mutate (typed confirm) | `page.test.tsx` |
| `POST /admin/queue/messages/:id/cancel` | `page.tsx` (typed confirm) | `AdminQueueCancel` (admin_queue.go) | Platform | mutate (typed confirm) | `page.test.tsx` |

All six endpoints share the `COREMAIL_DISABLED` sanitized-503 contract
(`h.cfg.CoreMail.Enabled` gate) verified in
`internal/api/handlers/admin_queue_coremail_state_test.go` (backend)
and this feature's `queries.ts`'s `isCoreMailDisabled` (frontend).

## Reliability

| Route | Frontend feature | Backend handler | Owner | Operations | Test file |
|---|---|---|---|---|---|
| `GET /admin/backups` | `components/BackupsPanel.tsx` | `ListBackups` (backups.go) | Platform | read | `BackupsPanel.test.tsx` |
| `POST /admin/backups` | `components/BackupsPanel.tsx` | `CreateBackup` (backups.go) | Platform | mutate | `BackupsPanel.test.tsx` |
| `POST /admin/backups/now` | `components/BackupsPanel.tsx` | `PostBackupNow` (backups.go) | Platform | mutate | `BackupsPanel.test.tsx` |
| `GET /admin/backups/schedule` | `components/BackupsPanel.tsx` | `GetBackupSchedule` (backups.go) | Platform | read | `BackupsPanel.test.tsx` |
| `GET /admin/backups/metrics` | `components/BackupsPanel.tsx` | `GetBackupMetrics` (backups.go) | Platform | read | `BackupsPanel.test.tsx` |
| `GET /admin/backups/health` | `components/BackupsPanel.tsx` | `GetBackupHealth` (backups.go) | Platform | read | `BackupsPanel.test.tsx` |
| `POST /admin/backups/retention` | `components/BackupsPanel.tsx` | `RunBackupRetention` (backups.go) | Platform | mutate (typed confirm) | `BackupsPanel.test.tsx` |
| `POST /admin/backups/:id/validate` | `components/BackupsPanel.tsx` | `PostValidateBackup` (backups.go) | Platform | mutate | `BackupsPanel.test.tsx` |
| `POST /admin/backups/:id/restore` | `components/BackupsPanel.tsx` | `PostRestoreBackup` (restore_jobs.go) | Platform | mutate (typed confirm `restore-orvix-backup`) | `BackupsPanel.test.tsx` |
| `DELETE /admin/backups/:id` | `components/BackupsPanel.tsx` | `DeleteBackup` (backups.go) | Platform | mutate, destructive (typed confirm `delete-orvix-backup`) | `BackupsPanel.test.tsx` |
| `GET /admin/backups/restore-jobs/:job_id` | `components/BackupsPanel.tsx` | `GetRestoreJobStatus` (restore_jobs.go) | Platform | read, polled | `BackupsPanel.test.tsx` |
| `GET /update/status` | `components/UpdatesPanel.tsx` | `GetUpdateStatus` (update.go) | Platform | read | `UpdatesPanel.test.tsx` |
| `GET /update/check` | `components/UpdatesPanel.tsx` | `GetUpdateCheck` (update.go) | Platform | read | `UpdatesPanel.test.tsx` |
| `POST /update/check` | `components/UpdatesPanel.tsx` | `PostUpdateCheck` (update.go) | Platform | mutate | `UpdatesPanel.test.tsx` |
| `GET /update/history` | `components/UpdatesPanel.tsx` | `GetUpdateHistory` (update.go) | Platform | read | `UpdatesPanel.test.tsx` |
| `GET /update/preflight` | `components/UpdatesPanel.tsx` | `GetUpdatePreflight` (update.go) | Platform | read | `UpdatesPanel.test.tsx` |
| `POST /update/run` | `components/UpdatesPanel.tsx` | `PostUpdateRun` (update.go) | Platform | mutate (typed confirm), single-flight | `UpdatesPanel.test.tsx` |
| `GET /monitoring/alerts` | `components/MonitoringPanel.tsx` | `GetMonitoringAlerts` (monitoring.go) | Platform | read | `MonitoringPanel.test.tsx` |
| `POST /monitoring/alerts/:id/resolve` | `components/MonitoringPanel.tsx` | `PostMonitoringAlertResolve` (monitoring.go) | Platform | mutate | `MonitoringPanel.test.tsx` |
| `GET /monitoring/capacity` | `components/MonitoringPanel.tsx` | `GetMonitoringCapacity` (monitoring.go) | Platform | read | `MonitoringPanel.test.tsx` |
| `GET /monitoring/snapshot` | `components/MonitoringPanel.tsx` | `GetMonitoringSnapshot` (monitoring.go) | Platform | read | `MonitoringPanel.test.tsx` |
| `GET /monitoring/alert-providers` | `components/MonitoringPanel.tsx` | `GetMonitoringProviders` (monitoring.go) | Platform | read | `MonitoringPanel.test.tsx` |
| `GET /monitoring/alert-deliveries` | `components/MonitoringPanel.tsx` | `ListAlertDeliveries` (enterprise_parity.go) | Platform | read | `MonitoringPanel.test.tsx` |
| `GET /admin/storage/volumes` | `components/StoragePanel.tsx` | `ListStorageVolumes` (enterprise_parity.go) | Platform | read | `StoragePanel.test.tsx` |
| `GET /admin/cluster/status` | `components/ClusterPanel.tsx` | `AdminClusteringStatus` (enterprise_admin_v3.go) | Platform | read (honestly static: single-node deployment, no clustering implemented) | `ClusterPanel.test.tsx` |
| `GET /updates/changelog` | none | `GetChangelog` (handlers_advanced.go) | Platform | — | UI_GAP: real route, no UI consumer. |
| `POST /updates/apply/:module` | none | `ApplyUpdate` (handlers_advanced.go) | Platform | — | UI_GAP: real route, no UI consumer. |
| `GET/PATCH /admin/settings/protocol/:protocol` | none | `ListProtocolSettings` / `PatchProtocolSettings` (router.go refs) | Platform | — | UI_GAP: real routes, no UI consumer. |

## Security

| Route | Frontend feature | Backend handler | Owner | Operations | Test file |
|---|---|---|---|---|---|
| `GET /audit/logs` | `components/AuditPanel.tsx` | `ListAuditLogs` (handlers.go) | Platform | read | `AuditFirewallSelfHeal.test.tsx` |
| `GET /admin/ssl/certificates` | `components/SslPanel.tsx` | `AdminSslListCertificates` (enterprise_admin_ssl.go) | Platform | read | `SslPanel.test.tsx` |
| `POST /admin/ssl/certificates/reload` | `components/SslPanel.tsx` | `AdminSslReloadCertificates` (enterprise_admin_ssl.go) | Platform | mutate | `SslPanel.test.tsx` |
| `GET /admin/ssl/expiry-warnings` | `components/SslPanel.tsx` | `AdminSslExpiryWarnings` (enterprise_admin_ssl.go) | Platform | read | `SslPanel.test.tsx` |
| `GET /admin/ssl/acme/status` | `components/SslPanel.tsx` | `AdminSslAcmeStatus` (enterprise_admin_ssl.go) | Platform | read (honestly static: ACME issuance not implemented) | `SslPanel.test.tsx` |
| `DELETE /admin/ssl/certificates/:id` | `components/SslPanel.tsx` | `AdminSslDeleteCertificate` (enterprise_admin_ssl.go) | Platform | mutate, destructive (typed confirm) | `SslPanel.test.tsx` |
| `POST /admin/ssl/certificates` | none | `AdminSslUploadCertificate` (enterprise_admin_ssl.go) | Platform | — | UI_GAP: real route, no upload form wired. |
| `GET /admin/security/antivirus` | `components/AntivirusPanel.tsx` | `AdminAntivirusStatus` (enterprise_admin_v3.go) | Platform | read | `AntivirusPanel.test.tsx` |
| `GET /firewall/rules` | `components/FirewallPanel.tsx` | `ListFirewallRules` (handlers.go) | Platform | read | `AuditFirewallSelfHeal.test.tsx` |
| `GET /firewall/logs` | `components/FirewallPanel.tsx` | `ListFirewallLogs` (handlers.go) | Platform | read | `AuditFirewallSelfHeal.test.tsx` |
| `POST /firewall/rules` | none | `CreateFirewallRule` (handlers.go) | Platform | — | UI_GAP: real route, no create form wired. |
| `GET /guardian/logs` | `components/GuardianPanel.tsx` | `ListGuardianLogs` (handlers_guardian.go) | Platform | read | `GuardianPanel.test.tsx` |
| `POST /guardian/analyze` | none | `AnalyzeEmail` (handlers_guardian.go) | Platform | — | MACHINE_ONLY: takes raw email content (subject/body/headers) as input — no operator form makes sense for this; it's an internal analysis call, not an admin action. |
| `GET /heal/history` | `components/SelfHealPanel.tsx` | `ListHealHistory` (handlers_autoheal.go) | Platform | read | `AuditFirewallSelfHeal.test.tsx` |
| `POST /heal/check/:name` | `components/SelfHealPanel.tsx` | `RunHealCheck` (handlers_autoheal.go) | Platform | mutate | `AuditFirewallSelfHeal.test.tsx` |
| `GET /admin/log-rules` | `components/LogRulesPanel.tsx` | `ListLogRules` (enterprise_admin.go) | Platform | read | `LogRulesPanel.test.tsx` |
| `POST /admin/log-rules` | `components/LogRulesPanel.tsx` | `CreateLogRule` (enterprise_admin.go) | Platform | mutate | `LogRulesPanel.test.tsx` |
| `DELETE /admin/log-rules/:id` | `components/LogRulesPanel.tsx` | `DeleteLogRule` (enterprise_admin.go) | Platform | mutate, destructive (typed confirm) | `LogRulesPanel.test.tsx` |

## Configuration

| Route | Frontend feature | Backend handler | Owner | Operations | Test file |
|---|---|---|---|---|---|
| `GET /admin/settings` | `components/SettingsPanel.tsx` | `AdminSettingsGet` (admin_settings.go) | Platform | read (sectioned, typed) | `SettingsPanel.test.tsx` |
| `PATCH /admin/settings` | `components/SettingsPanel.tsx` | `AdminSettingsPatch` (admin_settings.go) | Platform | mutate (nested, diff-only) | `SettingsPanel.test.tsx` |
| `GET /feature-flags` | `components/FeatureFlagsPanel.tsx` | `ListFeatureFlags` (handlers.go) | Platform | read | `FeatureFlagsPanel.test.tsx` |
| `PUT /feature-flags/:id` | `components/FeatureFlagsPanel.tsx` | `UpdateFeatureFlag` (handlers.go) | Platform | mutate | `FeatureFlagsPanel.test.tsx` |

## Removed from the commercial platform navigation

| Route | Disposition |
|---|---|
| `GET /license`, `POST /license/validate` | Backend already retired for this hosted-SaaS product (`GetLicense` unconditionally returns `{"status":"not_required"}`; `ValidateLicense` returns 410 Gone). Frontend page and nav entry removed outright — not repurposed into a fake subscription system. Commercial plans/subscriptions/billing are the separate future `platform-commercial-control-plane` bounded context. |

## Theme system (cross-cutting, not a route)

Verified by `web/admin/src/shared/theme/{theme,useTheme}.test.ts`,
`noHardcodedColors.test.ts` (static scan, zero hardcoded colors in
application code), and `test/playwright/portal.spec.ts`'s merged
Light/Dark coverage for both PSA and Tenant Admin (see that file's
first two tests).
