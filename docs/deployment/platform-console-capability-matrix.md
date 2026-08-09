# Platform Operations Console — Capability Matrix

Every route in `internal/api/router.go` gated with `platformMW[0], platformMW[1]`
(i.e. `RequireAnyRole(platform_super_admin, superadmin)` + CSRF), as of commit
`899ad6a84a33cdd12b0b69fb7ef35a6b76dcb63e`. Legacy `Legacy*Gone`-mapped aliases
(`/backups*` pre-`/admin/backups*` rename) are omitted — they 410 unconditionally
and have no independent behavior to classify.

Classification legend:
- **UI_PAGE** — has a dedicated page/section in the Platform console.
- **EMBEDDED_ACTION** — a mutation reachable from within a UI_PAGE (button/dialog), not a standalone nav item.
- **MACHINE_ONLY** — intentionally has no UI; justified below.
- **LOCAL_ROOT_CLI_ONLY** — intentionally has no UI and no remote path; justified below.

| Method | Path | Handler | Read/Mutate | Destructive | Frontend consumer | UI page/action | Test | Class |
|---|---|---|---|---|---|---|---|---|
| GET | `/platform/dashboard` | `PlatformDashboard` | read | no | `api.getPlatformDashboard` | Overview | `App.test.tsx` platform-nav table | UI_PAGE |
| GET | `/admin/summary` | `AdminSummary` | read | no | `api.getAdminSummary` | Summary | `App.test.tsx` platform-nav table | UI_PAGE |
| GET | `/platform/organizations` | `ListPlatformOrganizations` | read | no | `api.listPlatformOrganizations` | Organizations | `App.test.tsx` platform-nav table | UI_PAGE |
| GET | `/platform/organizations/:id` | `GetPlatformOrganization` | read | no | `api.getPlatformOrganization` | Organizations → detail drawer | `OrganizationDetail.test.tsx` | UI_PAGE |
| GET | `/platform/organizations/:id/detail` | `GetOrganizationDetail` | read | no | `api.getPlatformOrganizationDetail` | Organizations → detail drawer | `OrganizationDetail.test.tsx` | UI_PAGE |
| PATCH | `/platform/organizations/:id` | `UpdateOrganization` | mutate | no | `api.updatePlatformOrganization` | Organizations → detail drawer edit | `OrganizationDetail.test.tsx` | EMBEDDED_ACTION |
| POST | `/platform/organizations/:id/active` | `SetOrganizationActive` | mutate | yes (suspends tenant) | `api.setPlatformOrganizationActive` | Organizations → suspend/activate | `OrganizationDetail.test.tsx` | EMBEDDED_ACTION |
| GET | `/queue` | `ListQueue` | read | no | `api.listPlatformQueue` | Mail Ops → Queue | `MailOperations.test.tsx` | UI_PAGE |
| GET | `/admin/queue/summary` | `AdminQueueSummary` | read | no | `api.getQueueSummary` | Mail Ops → Queue (summary strip) | `MailOperations.test.tsx` | UI_PAGE |
| GET | `/admin/queue/messages` | `AdminQueueList` | read | no | `api.listQueueMessages` | Mail Ops → Queue table | `MailOperations.test.tsx` | UI_PAGE |
| GET | `/admin/queue/messages/:id` | `AdminQueueDetail` | read | no | `api.getQueueMessage` | Mail Ops → Queue detail drawer | `MailOperations.test.tsx` | UI_PAGE |
| GET | `/admin/queue/:id` | `GetAdminQueueEntry` | read | no | (superseded by `AdminQueueDetail`) | — | — | MACHINE_ONLY (superseded read alias; the console uses the richer `/admin/queue/messages/:id`) |
| POST | `/admin/queue/messages/:id/retry` | `AdminQueueRetryNow` | mutate | no | `api.retryQueueMessage` | Mail Ops → Queue row action | `MailOperations.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/queue/messages/:id/bounce` | `AdminQueueBounce` | mutate | yes | `api.bounceQueueMessage` | Mail Ops → Queue row action (typed confirm) | `MailOperations.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/queue/messages/:id/cancel` | `AdminQueueCancel` | mutate | yes | `api.cancelQueueMessage` | Mail Ops → Queue row action (typed confirm) | `MailOperations.test.tsx` | EMBEDDED_ACTION |
| DELETE | `/queue/:id` | `DeleteQueue` | mutate | yes | (superseded) | — | — | MACHINE_ONLY — legacy delete-by-id predates the `/admin/queue/messages/:id/*` action set; cancel achieves the same outcome through the audited path |
| POST | `/queue/:id/retry` | `RetryQueue` | mutate | no | (superseded) | — | — | MACHINE_ONLY — superseded by `AdminQueueRetryNow` |
| GET | `/admin/backups` | `ListBackups` | read | no | `api.listBackups` | Reliability → Backups | `Reliability.test.tsx` | UI_PAGE |
| GET | `/admin/backups/schedule` | `GetBackupSchedule` | read | no | `api.getBackupSchedule` | Reliability → Backups (schedule panel) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/admin/backups/metrics` | `GetBackupMetrics` | read | no | `api.getBackupMetrics` | Reliability → Backups (metrics strip) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/admin/backups/health` | `GetBackupHealth` | read | no | `api.getBackupHealth` | Reliability → Backups (health badge) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/admin/backups/restore-jobs/:job_id` | `GetRestoreJobStatus` | read | no | `api.getRestoreJobStatus` | Reliability → Backups → restore job poller | `Reliability.test.tsx` | UI_PAGE |
| GET | `/admin/backups/:id` | `GetBackup` | read | no | `api.getBackup` | Reliability → Backups → detail | `Reliability.test.tsx` | UI_PAGE |
| GET | `/admin/backups/:id/download` | `DownloadBackup` | read | no | `api.downloadBackupUrl` | Reliability → Backups row action (link) | `Reliability.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/backups` | `CreateBackup` | mutate | no | `api.createBackup` | Reliability → Backups "Create backup" | `Reliability.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/backups/now` | `PostBackupNow` | mutate | no | `api.runBackupNow` | Reliability → Backups "Run now" | `Reliability.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/backups/schedule` | `SetBackupSchedule` | mutate | no | `api.setBackupSchedule` | Reliability → Backups schedule form | `Reliability.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/backups/retention` | `RunBackupRetention` | mutate | yes (deletes old backups) | `api.runBackupRetention` | Reliability → Backups "Run retention" (typed confirm) | `Reliability.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/backups/:id/validate` | `PostValidateBackup` | mutate | no | `api.validateBackup` | Reliability → Backups row action | `Reliability.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/backups/:id/restore` | `PostRestoreBackup` | mutate | yes | `api.restoreBackup` | Reliability → Backups row action (typed confirm `restore-orvix-backup`) | `Reliability.test.tsx` | EMBEDDED_ACTION |
| DELETE | `/admin/backups/:id` | `DeleteBackup` | mutate | yes | `api.deleteBackup` | Reliability → Backups row action (typed confirm) | `Reliability.test.tsx` | EMBEDDED_ACTION |
| GET | `/updates/check` \| `/update/check` | `CheckUpdates` / `GetUpdateCheck` | read | no | `api.checkUpdates` | Reliability → Updates | `Reliability.test.tsx` | UI_PAGE |
| GET | `/updates/changelog` | `GetChangelog` | read | no | `api.getChangelog` | Reliability → Updates (changelog panel) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/update/status` | `GetUpdateStatus` | read | no | `api.getUpdateStatus` | Reliability → Updates (status badge) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/update/history` | `GetUpdateHistory` | read | no | `api.getUpdateHistory` | Reliability → Updates (history table) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/update/preflight` | `GetUpdatePreflight` | read | no | `api.getUpdatePreflight` | Reliability → Updates (preflight panel, shown before Apply) | `Reliability.test.tsx` | UI_PAGE |
| POST | `/update/check` | `PostUpdateCheck` | mutate | no | `api.postUpdateCheck` | Reliability → Updates "Check now" | `Reliability.test.tsx` | EMBEDDED_ACTION |
| POST | `/update/run` | `PostUpdateRun` | mutate | yes | `api.runUpdate` | Reliability → Updates "Run update" (typed confirm) | `Reliability.test.tsx` | EMBEDDED_ACTION |
| POST | `/updates/apply/:module` | `ApplyUpdate` | mutate | yes | `api.applyModuleUpdate` | Reliability → Updates per-module "Apply" (typed confirm) | `Reliability.test.tsx` | EMBEDDED_ACTION |
| GET | `/monitoring/health` | `GetMonitoringHealth` | read | no | `api.getMonitoringHealth` | Reliability → Monitoring | existing (`SystemHealth`) | UI_PAGE |
| GET | `/monitoring/alerts` | `GetMonitoringAlerts` | read | no | `api.getMonitoringAlerts` | Reliability → Monitoring (alerts tab) | `Reliability.test.tsx` | UI_PAGE |
| POST | `/monitoring/alerts/:id/resolve` | `PostMonitoringAlertResolve` | mutate | no | `api.resolveMonitoringAlert` | Reliability → Monitoring alert row action | `Reliability.test.tsx` | EMBEDDED_ACTION |
| GET | `/monitoring/capacity` | `GetMonitoringCapacity` | read | no | `api.getMonitoringCapacity` | Reliability → Monitoring (capacity tab) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/monitoring/snapshot` | `GetMonitoringSnapshot` | read | no | `api.getMonitoringSnapshot` | Reliability → Monitoring (snapshot tab) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/monitoring/alert-providers` | `GetMonitoringProviders` | read | no | `api.getMonitoringProviders` | Reliability → Monitoring (providers tab) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/monitoring/alert-deliveries` | `ListAlertDeliveries` | read | no | `api.listAlertDeliveries` | Reliability → Monitoring (deliveries tab) | `Reliability.test.tsx` | UI_PAGE |
| GET | `/metrics` | Prometheus `metrics.Handler()` | read | no | linked, not embedded | Reliability → Monitoring "Open raw metrics" (external link, plain-text Prometheus exposition, not JSON — unsuitable to embed) | `Reliability.test.tsx` (link present) | UI_PAGE (link-out) |
| GET | `/admin/storage/volumes` | `ListStorageVolumes` | read | no | `api.listStorageVolumes` | Reliability → Storage | `Reliability.test.tsx` | UI_PAGE |
| GET | `/admin/cluster/status` | `AdminClusteringStatus` | read | no | `api.getClusterStatus` | Reliability → Cluster | `Reliability.test.tsx` | UI_PAGE |
| GET | `/admin/runtime` | `GetAdminRuntime` | read | no | `api.getAdminRuntime` | Overview (runtime strip) | `App.test.tsx` | UI_PAGE |
| GET | `/firewall/rules` | `ListFirewallRules` | read | no | `api.listFirewallRules` | Security → Firewall | existing (`Firewall`) | UI_PAGE |
| GET | `/firewall/logs` | `ListFirewallLogs` | read | no | `api.listFirewallLogs` | Security → Firewall | existing (`Firewall`) | UI_PAGE |
| POST | `/firewall/rules` | `CreateFirewallRule` | mutate | no | `api.createFirewallRule` | Security → Firewall "Add rule" | existing (`Firewall`) | EMBEDDED_ACTION |
| GET | `/audit/logs` | `ListAuditLogs` | read | no | `api.listPlatformAuditLogs` | Security → Audit Log | `Security.test.tsx` | UI_PAGE |
| GET | `/admin/ssl/certificates` | `AdminSslListCertificates` | read | no | `api.listSslCertificates` | Security → SSL/ACME | `Security.test.tsx` | UI_PAGE |
| GET | `/admin/ssl/certificates/reload` | `AdminSslReloadCertificates` | read | no | `api.reloadSslCertificates` (GET variant) | Security → SSL/ACME "Reload" | `Security.test.tsx` | EMBEDDED_ACTION |
| POST | `/admin/ssl/certificates/reload` | `AdminSslReloadCertificates` | mutate | no | `api.reloadSslCertificates` | Security → SSL/ACME "Reload" | `Security.test.tsx` | EMBEDDED_ACTION |
| GET | `/admin/ssl/expiry-warnings` | `AdminSslExpiryWarnings` | read | no | `api.getSslExpiryWarnings` | Security → SSL/ACME (warnings banner) | `Security.test.tsx` | UI_PAGE |
| GET | `/admin/ssl/acme/status` | `AdminSslAcmeStatus` | read | no | `api.getAcmeStatus` | Security → SSL/ACME (ACME panel) | `Security.test.tsx` | UI_PAGE |
| POST | `/admin/ssl/certificates` | `AdminSslUploadCertificate` | mutate | no | `api.uploadSslCertificate` | Security → SSL/ACME "Upload certificate" | `Security.test.tsx` | EMBEDDED_ACTION |
| DELETE | `/admin/ssl/certificates/:id` | `AdminSslDeleteCertificate` | mutate | yes | `api.deleteSslCertificate` | Security → SSL/ACME row action (typed confirm) | `Security.test.tsx` | EMBEDDED_ACTION |
| GET | `/admin/security/antivirus` | `AdminAntivirusStatus` | read | no | `api.getAntivirusStatus` | Security → Antivirus | `Security.test.tsx` | UI_PAGE |
| GET | `/guardian/logs` | `ListGuardianLogs` | read | no | `api.listGuardianLogs` | Security → Guardian | `Security.test.tsx` | UI_PAGE |
| POST | `/guardian/analyze` | `AnalyzeEmail` | mutate | no | — | — | — | MACHINE_ONLY — accepts raw email content for on-demand analysis; it is an integration/API primitive for the mail pipeline, not an operator action with a meaningful standalone form (no email content exists to paste in from the console) |
| GET | `/heal/history` | `ListHealHistory` | read | no | `api.listHealHistory` | Security → Self-Heal | `Security.test.tsx` | UI_PAGE |
| POST | `/heal/check/:name` | `RunHealCheck` | mutate | no | `api.runHealCheck` | Security → Self-Heal "Run check" | `Security.test.tsx` | EMBEDDED_ACTION |
| GET | `/admin/log-rules` | `ListLogRules` | read | no | `api.listLogRules` | Security → Log Rules | `Security.test.tsx` | UI_PAGE |
| POST | `/admin/log-rules` | `CreateLogRule` | mutate | no | `api.createLogRule` | Security → Log Rules "Add rule" | `Security.test.tsx` | EMBEDDED_ACTION |
| DELETE | `/admin/log-rules/:id` | `DeleteLogRule` | mutate | yes | `api.deleteLogRule` | Security → Log Rules row action (typed confirm) | `Security.test.tsx` | EMBEDDED_ACTION |
| GET | `/admin/fs/browse` | `AdminFsBrowse` | read | no | — | — | — | LOCAL_ROOT_CLI_ONLY — raw server filesystem directory listing. Exposing arbitrary FS traversal in a remotely-reachable web console (even to PSA) meaningfully widens the blast radius of a compromised PSA session/CSRF bypass; this class of capability belongs behind local root shell access, not a browser UI |
| GET | `/admin/fs/read` | `AdminFsRead` | read | no | — | — | — | LOCAL_ROOT_CLI_ONLY — same rationale as `/admin/fs/browse`; raw arbitrary file content read |
| GET | `/admin/settings` | `AdminSettingsGet` | read | no | `api.getAdminSettings` | Configuration → Settings | `Configuration.test.tsx` | UI_PAGE |
| PATCH | `/admin/settings` | `AdminSettingsPatch` | mutate | no | `api.patchAdminSettings` | Configuration → Settings form | `Configuration.test.tsx` | EMBEDDED_ACTION |
| GET | `/admin/settings/protocol/:protocol` | `ListProtocolSettings` | read | no | `api.getProtocolSettings` | Configuration → Settings (per-protocol panel) | `Configuration.test.tsx` | UI_PAGE |
| PATCH | `/admin/settings/protocol/:protocol` | `PatchProtocolSettings` | mutate | no | `api.patchProtocolSettings` | Configuration → Settings (per-protocol form) | `Configuration.test.tsx` | EMBEDDED_ACTION |
| GET | `/feature-flags` | `ListFeatureFlags` | read | no | `api.listFeatureFlags` | Configuration → Feature Flags | `Configuration.test.tsx` | UI_PAGE |
| PUT | `/feature-flags/:id` | `UpdateFeatureFlag` | mutate | no | `api.updateFeatureFlag` | Configuration → Feature Flags row toggle | `Configuration.test.tsx` | EMBEDDED_ACTION |
| GET | `/modules` | `ListModules` | read | no | `api.listModules` | Configuration → Modules | existing (`Modules`) | UI_PAGE |
| GET | `/license` | `GetLicense` | read | no | `fetch("/api/v1/license")` | Configuration → License | existing (`LicenseStatus`) | UI_PAGE |
| POST | `/license/validate` | `ValidateLicense` | mutate | no | `api.validateLicense` | Configuration → License "Re-validate" | `Configuration.test.tsx` | EMBEDDED_ACTION |
| GET | `/admin/mfa/status` | `MFAStatusGet` | read | no | (self-scoped `/account/mfa/status` used instead) | — | — | MACHINE_ONLY — duplicate of the self-scoped `/account/mfa/*` family already surfaced on the shared Security/Account page for every portal; the `/admin/mfa/*` family exists for a distinct historical admin-panel-only MFA flow that the current SecurityPage.tsx does not use, and adding a second, parallel MFA UI for the same PSA identity would be confusing rather than clarifying |
| POST | `/admin/mfa/setup/begin` | `MFASetupBegin` | mutate | no | (see above) | — | — | MACHINE_ONLY (see above) |
| POST | `/admin/mfa/setup/verify` | `MFASetupVerify` | mutate | no | (see above) | — | — | MACHINE_ONLY (see above) |
| POST | `/admin/mfa/disable` | `MFADisable` | mutate | yes | (see above) | — | — | MACHINE_ONLY (see above) |
| GET | `/console/reports` | `AdminReports` | read | no | — | — | — | MACHINE_ONLY — legacy reporting export endpoint; its output is superseded by `/platform/dashboard` + `/admin/summary` for on-screen use |
| GET | `/console/internal/overview` | `InternalOverview` | read | no | — | — | — | MACHINE_ONLY — pre-dates `/platform/dashboard`; kept for internal tooling/scripts, superseded on-screen by Overview |
| GET | `/console/internal/tenants` | `InternalTenants` | read | no | — | — | — | MACHINE_ONLY — superseded on-screen by `/platform/organizations` |
| GET | `/console/internal/domain-intelligence` | `InternalDomainIntelligence` | read | no | — | — | — | MACHINE_ONLY — internal analytics export, not an operator workflow |
| GET | `/console/internal/security-ops` | `InternalSecurityOps` | read | no | — | — | — | MACHINE_ONLY — superseded on-screen by the Security group (Firewall/Audit/SSL/Antivirus/Guardian/Self-Heal/Log Rules) |
| GET | `/console/internal/mail-flow-ops` | `InternalMailFlowOps` | read | no | — | — | — | MACHINE_ONLY — superseded on-screen by Mail Operations (Queue) |

**Self-scoped, portal-agnostic (not platform-owned, not in this matrix's scope):**
`/account/*` (profile, sessions, mfa/status, mfa/setup/*) — already surfaced on the shared `AccountSettingsPage`/`SecurityPage`/`PreferencesPage` for both portals.

**Zero unclassified platform routes.** Every `platformMW`-gated route above carries
a classification. `MACHINE_ONLY`/`LOCAL_ROOT_CLI_ONLY` rows are the ones this
console intentionally does not surface, each with a stated reason — not omitted
for lack of time.
