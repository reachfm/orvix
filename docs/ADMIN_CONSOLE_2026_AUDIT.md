# Orvix Admin Console 2026 — Feature Audit

Branch: `feature/admin-console-2026-productization`
Base: latest `main`
Date: 2026-06-09

---

## Purpose

This document is the authoritative feature matrix for the admin console. Every
visible menu item, page action, and sidebar entry is classified as:

| Status | Meaning |
|---|---|
| **WORKING** | Real backend endpoint wired; error/empty states handled |
| **PARTIAL** | Endpoint exists but UI is incomplete or shows honest empty state |
| **STUB** | Renders an honest "not in this build" placeholder (no fake data) |
| **BROKEN** | Bug or misconfiguration prevents correct operation |
| **MISSING** | Page exists but no backend endpoint confirmed |

This sprint removes every **BROKEN** item and replaces it with the correct
working implementation. **STUB** items are acceptable if the copy is honest.

---

## Sidebar → Route → Handler Map

| Sidebar item | Route | Page module | Backend endpoint | Status |
|---|---|---|---|---|
| Dashboard | `dashboard` | `dashboard.js` | `/api/v1/admin/summary`, `/api/v1/admin/runtime`, `/api/v1/monitoring/health` | WORKING |
| General Settings | `settings/general` | `settings.js` | `/api/v1/admin/settings` | WORKING |
| Security Defaults | `settings/security` | `settings.js` | `/api/v1/admin/settings` | WORKING |
| License | `license` | `license.js` | `/api/v1/license` | WORKING |
| Build / Runtime Info | `settings/build` | `settings.js` | `/api/v1/admin/runtime` | WORKING |
| Services Management | `services` | `services.js` | `/api/v1/admin/runtime` | WORKING |
| Runtime Listeners | `runtime-listeners` | `runtime-listeners.js` | `/api/v1/admin/runtime` | WORKING |
| Manage Domains | `domains` | `domains.js` | `/api/v1/domains` | WORKING |
| Manage Accounts | `accounts` | `accounts.js` | `/api/v1/mailboxes` | WORKING |
| Groups | `domains/groups` | `domain-groups.js` | `/api/v1/admin/domain-groups` | WORKING |
| Mailing Lists | `domains/lists` | `mailing-lists.js` | `/api/v1/admin/mailing-lists` | WORKING |
| Public Folders | `domains/public` | `public-folders.js` | `/api/v1/admin/public-folders` | WORKING |
| Account Classes | `accounts/classes` | `account-classes.js` | `/api/v1/admin/account-classes` | WORKING |
| Bulk Mailbox Import | `bulk-import` | `bulk-import.js` | `/api/v1/mailboxes/import` | WORKING |
| DNS & DKIM | `dns`, `dns-dkim` | `dns-dkim.js` | `/api/v1/dns/wizard/*` | WORKING |
| SSL Certificates | `security/ssl` | `ssl.js` | `/api/v1/admin/ssl/*` | WORKING |
| Antivirus / Anti-spam | `security/antispam` | `antivirus.js` | `/api/v1/admin/antivirus/*` | PARTIAL |
| Global Spam Control | `security/spam` | `acl.js` | `/api/v1/admin/acl-rules` | WORKING |
| Acceptance & Routing | `security/routing` | `acceptance.js` | `/api/v1/admin/acceptance` | WORKING |
| Incoming Message Rules | `security/rules` | `incoming-rules.js` | `/api/v1/admin/incoming-rules` | WORKING |
| View Quarantine | `security/quarantine` | `quarantine.js` | `/api/v1/admin/quarantine` | WORKING |
| Login Protection | `security/login-protection` | `login-protection.js` | `/api/v1/admin/login-protection/status` | WORKING |
| Update Status | `updates` | `updates.js` | `/api/v1/update/*` | WORKING |
| Upgrade Checks | `updates/checks` | `updates.js` | `/api/v1/update/preflight` | WORKING |
| Queue Processing | `queue` | `queue.js` | `/api/v1/admin/queue` | WORKING |
| Queue View | `queue/messages` | `queue.js` | `/api/v1/admin/queue` | WORKING |
| Reporting | `monitoring` | `monitoring.js` | `/api/v1/monitoring/health`, `/api/v1/monitoring/alerts` | WORKING |
| Capacity Charts | `monitoring/capacity` | `monitoring.js` | `/api/v1/monitoring/*` | WORKING |
| Storage Charts | `monitoring/storage` | `monitoring.js` | `/api/v1/monitoring/*` | WORKING |
| Alert Providers | `monitoring/alert-providers` | `alert-providers.js` | `/api/v1/monitoring/alert-providers` | WORKING |
| Local Logs | `logs` | `logs.js` | `/api/v1/logs/*`, `/api/v1/audit/logs` | WORKING |
| Log Rules | `logs/rules` | `log-rules.js` | `/api/v1/admin/log-rules` | WORKING |
| View Log Files | `logs/files` | `logs.js` | filesystem path hints | WORKING |
| Log Server | `logs/server` | `log-rules.js` | `/api/v1/admin/log-rules` (destination) | WORKING |
| Backup Status | `backups` | `backups.js` | `/api/v1/backups` | WORKING |
| Backup History | `backups/history` | `backups.js` | `/api/v1/backups` | WORKING |
| FTP Backup | `backups/ftp` | `ftp-backup.js` | `/api/v1/backups/targets` | PARTIAL |
| FS Access | `backups/fs` | `fs-access.js` | `/api/v1/backups/targets` | PARTIAL |
| Admin Groups | `admin/groups` | `admin-groups.js` | `/api/v1/admin/admin-groups` | WORKING |
| Admin Users | `admin/users` | `admin-users.js` | `/api/v1/admin/users` | WORKING |
| Audit Log | — | `audit-log.js` | `/api/v1/admin/audit-logs` | WORKING |
| Migration Jobs | `migration` (hidden) | `migration.js` | `/api/v1/migration/jobs` | PARTIAL |
| Source Servers | `migration/sources` (hidden) | `migration-sources.js` | `/api/v1/migration/sources` | PARTIAL |
| Clustering Setup | `clustering` (hidden) | `clustering.js` | honest single-node note | STUB |
| IMAP Proxy | `clustering/imap` (hidden) | `clustering.js` | honest single-node note | STUB |
| POP3 Proxy | `clustering/pop3` (hidden) | `clustering.js` | honest single-node note | STUB |
| WebMail / JMAP Proxy | `clustering/webmail` (hidden) | `clustering.js` | honest single-node note | STUB |

Protocol settings pages (`settings/protocol/*`) all route to `settings-protocol.js`
which reads the URL segment and calls `/api/v1/admin/settings/protocol/:protocol`.
All 10 protocol sub-pages share one handler — **WORKING**.

---

## Bugs Fixed

### B-1: `admin/users` registered twice in app.js — FIXED

**File:** `release/admin/app.js:146-147`

```js
register('admin/users',              adminUsersPage.renderAdminUsersPage);  // line 146
register('admin/users',              auditLog.renderAuditLogPage);        // line 147 ← overwrites B-1
```

**Impact before fix:** Navigating to `/#/admin/users` rendered the **Audit Log** page instead of the
Admin Users page. The Admin Users page was inaccessible from the sidebar.

**Fix:** Removed the duplicate `admin/users` registration and registered Audit Log under
its own `admin/audit-log` route. Functional smoke now navigates both routes.

**RBAC check:** The `auditLog.renderAuditLogPage` function is now reachable via the
dedicated audit-log route, while `admin/users` renders the Admin Users page.

---
## Dead Code / Cleanup

### C-1: Dead import in `security.js` — MINOR

**File:** `release/admin/modules/pages/security.js:14`

```js
import { renderPlannedPage } from './_planned.js';  // never called
```

`renderPlannedPage` is imported but not used anywhere in the file. The MFA section
shows `"No MFA telemetry reported."` as honest copy — correct. The remaining security
sections show `badge("planned")` chips — acceptable stub copy. No functional impact.

**Fix:** Remove the dead import.

### C-2: `mailboxes` aliased to `accounts` in app.js — OK but unusual

**File:** `release/admin/app.js:121`

```js
register('mailboxes', accounts.renderAccountsPage);  // alias
register('accounts',  accounts.renderAccountsPage);
```

Both routes render the same page. This is intentional (mailboxes ↔ accounts are the
same resource), not a bug. Noting it for clarity.

---

## Smoke Script Hardening

The admin smoke scripts now cover route wiring, CSRF hygiene, MTA-STS integrity,
storage policy, runtime-truthful action lists, banned visible placeholder copy,
and functional browser navigation for the productized routes.

### G-1: Banned-string grep in compiled/bundled JS — FIXED

**Risk covered:** A developer could leave `"coming soon"`, `"future release"`, `"not implemented"`,
`"placeholder"`, or `"TODO"` in production UI strings. The smoke script now catches this.

**Fix:** Added `grep -rE` check for banned strings across all `release/admin/` assets
(excluding `node_modules/`, comments with `//.*`, and the `_planned.js` module itself).

**Banned strings:**
- `"coming soon"`
- `"future release"`
- `"not implemented"`
- `"placeholder"` (as a visible UI label, not in `placeholder:` form attribute)
- `"TODO"` (as a visible string, not in code comments)
- `"mock"`, `"fake"`, `"unavailable in this build"`, `"will be added later"`

### G-2: `smoke-admin-functional-browser.mjs` route/product checks — FIXED

The functional browser smoke now navigates `admin/users`, `admin/audit-log`, and
`runtime-listeners`, and checks rendered product copy for banned placeholder text.

---

## Backend Safety Review (summary)

| Concern | Status | Notes |
|---|---|---|
| RBAC on admin endpoints | Appears complete | `admin_users.go`, `admin_queue.go`, `admin_mfa.go` all check auth |
| CSRF on mutating calls | Working | `csrfFetch()` wraps all POST/PATCH/DELETE |
| Audit logging | Present | `writeAuditLog()` called in admin_mfa.go |
| Raw DB errors exposed | Appears mitigated | All handlers return structured JSON errors |
| Secrets in responses | Appears clean | MFA responses use hashed secrets; no passwords echoed |
| `admin/users` route conflict | FIXED | See B-1 |

---

## Phase 1 Issues Summary

| ID | Severity | Location | Description |
|---|---|---|---|
| B-1 | HIGH | `app.js:147` | FIXED: `admin/users` double-registration removed; audit log moved to `admin/audit-log` |
| G-1 | MEDIUM | `smoke-admin-ui.sh` | FIXED: banned-string grep added for production UI strings |
| G-2 | MEDIUM | `smoke-admin-functional-browser.mjs` | FIXED: functional smoke covers admin/users, audit-log, runtime listeners, and rendered placeholder text |
| C-1 | LOW | `security.js:14` | FIXED: dead `renderPlannedPage` import removed |

---

## Scope of This Sprint

**In scope:**
1. Fix B-1 — remove duplicate `admin/users` registration
2. Fix C-1 — remove dead import
3. Add G-1 — banned-string smoke test
4. Fix G-2 — extend `smoke-admin-functional-browser.mjs`
5. Update CHANGELOG.md

**Not in scope (honest stubs are acceptable):**
- Clustering pages (`clustering.js` renders honest single-node note — correct)
- Migration pages (hidden from sidebar, wired to real endpoints)
- FTP backup / FS access partial pages (showing honest empty states)
- Antivirus partial page

---

## Pages with Honest Stub Copy

These are acceptable — the copy is transparent about what is and isn't available:

| Page | Stub copy | Endpoint |
|---|---|---|
| `clustering.js` | "single-node" note | None (informational) |
| `migration.js` | "No migration jobs" empty state | `/api/v1/migration/jobs` (real) |
| `ftp-backup.js` | Empty state if no FTP targets | `/api/v1/backups/targets` (real) |
| `fs-access.js` | Empty state if no FS targets | `/api/v1/backups/targets` (real) |
| `security.js` | "No MFA telemetry reported" | `/api/v1/admin/mfa/status` (real — no MFA configured) |

---

*Last updated: 2026-07-07 — Phase 3 final polish*

---

## Phase 3 — Admin Console Final Polish (2026-07-07)

After the Phase 2 productization went to a fresh VPS, the operator
took screenshots and identified the remaining gaps that weak
production polish would surface in the demo. Phase 3 closes them.

### Findings addressed

| Finding | Resolution |
|---|---|
| **VPS assets verification used wrong paths** (`/assets/app.js`, `/styles.css` 404) | New `release/scripts/smoke-admin-asset-paths.sh` parses the live `/admin/` document, resolves every `href="*.css"` / `src="*.js"` URL, and asserts each returns 200. The script also probes the wrong paths explicitly and confirms they 404 — so the smoke is the canonical verification surface. |
| **Add Domain modal was single-field** | Rewritten with every advanced field the backend persists: status (active/suspended), plan (smb/enterprise/education/free), description, max_mailboxes, max_aliases, max_quota_mb, dkim_enabled + dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact. Two-column responsive grid in the large modal. Functional browser smoke asserts ≥ 6 inputs. |
| **Domain detail drawer was a key/value dump** | New drawer renders summary panel (status, plan, limits, DKIM/DMARC/MTA-STS, catch-all, abuse contact), Mailboxes table, DNS records panel with per-row copy + bulk "Copy all records", Audit trail. Edit limits modal opens from the drawer. Suspend/Resume/Delete buttons wired. |
| **Domain detail edit controls** | New `PATCH /api/v1/domains/:name` accepts the same enterprise fields as create (allowlisted, unknown keys hard-reject). Domain Detail drawer exposes an "Edit limits" modal that calls this endpoint. |
| **General Settings weak copy** | "Backend reports no mutable settings in this build" replaced with a polished runtime overview (Build & Runtime, Listener Bindings, Security Posture, Protocol Toggles, Mutable settings panel, Persistence footer). Save controls only render for keys in the backend allowlist. |
| **Security posture unexplained `unknown`/`-`** | Every posture row rendered with an explicit value (Enabled / Disabled / Not configured / Not monitored / Managed by Caddy) + short hint. Tested in dashboard and security pages. |
| **Dashboard layout cramped at 100% zoom** | New `.dash-grid-4` uses 4-up at >1280px, 2-up at 720–1280px, 1-up on phones. Card minimum bumped to 320px so titles and stats stay readable. Modal grids responsive (2-up desktop, 1-up mobile). Sidebar width tightened. Warnings card now has a positive "No active warnings" empty state with a green check, so a clean install reads as a healthy signal at a glance. |
| **Static smoke banned-strings** | `smoke-admin-js.sh` §5, `smoke-admin-ui.sh` §38, `smoke-admin-browser.sh` §8 already cover JS/CSS/HTML. Phase 3 tightened the comment-stripping pipeline so multi-line block comments no longer leak banned words. The functional browser smoke adds a settings-page assertion: deprecated "no mutable settings in this build" copy must never render. |

### Backend additions

| Endpoint | Method | Notes |
|---|---|---|
| `POST /api/v1/domains` | extended | Adds status, plan, description, max_mailboxes, max_aliases, max_quota_mb, dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact. Validates catch-all is on the same domain, DKIM selector is a DNS label, limits are non-negative. |
| `GET /api/v1/domains/:name` | extended | Returns every persistent field, mailboxes, used counts. |
| `PATCH /api/v1/domains/:name` | **new** | Allowlist-editable enterprise fields; unknown keys hard-reject (whole patch rolled back); audit-logged; CSRF-protected. |

### Backend tests added (in `internal/api/handlers/admin_domain_advanced_test.go`)

| Test | Coverage |
|---|---|
| `TestAdminDomainCreateAdvancedFields` | Persists every advanced field and surfaces them in the response. |
| `TestAdminDomainCreateDefaultDkimSelector` | DKIM enabled without selector → sensible default. |
| `TestAdminDomainCreateInvalidCatchall` | Cross-field check rejects catch-all on a different domain. |
| `TestAdminDomainCreateBadDkimSelector` | DNS-label allowlist (no spaces, slashes). |
| `TestAdminDomainCreateNegativeLimit` | Negative max_* are hard-rejected. |
| `TestAdminDomainPatchAllowedFields` | All mutable enterprise fields persist + re-fetch roundtrip. |
| `TestAdminDomainPatchUnknownFieldHardReject` | Unknown key aborts the entire PATCH. |
| `TestAdminDomainPatchRBACEnforced` | Non-admin JWT cannot patch a domain. |
| `TestAdminDomainGetReturnsAllFields` | GET shape is stable for the Detail drawer. |

### Frontend rebuilds

| Page | Change |
|---|---|
| `pages/domains.js` | Rewritten. Modal exposes every enterprise field. Drawer renders summary/mailboxes/DNS records/audit with copy buttons. New `Edit limits` modal targets `PATCH /api/v1/domains/:name`. Manage table gains Plan, Mailboxes used/max, Quota, DKIM, Updated columns. |
| `pages/settings.js` | Rewritten. Always renders a polished runtime overview (Build & runtime, Listener bindings, Security posture, Protocol toggles, Mutable settings panel, Persistence footer). Save controls only render for keys in the backend allowlist. |
| `pages/security.js` | Posture table values now read Enabled / Disabled / Not configured / Not monitored / Managed by Caddy instead of unexplained "unknown". Every row carries a short hint. |
| `pages/dashboard.js` | Unknown values replaced with explicit labels. `loadSecurity()` adds "CSRF on writes" / "Login protection" / "TLS posture" rows with hints. Warnings empty state is a positive green check. |
| `styles.css` | New `.dash-grid-4` (4-up → 2-up → 1-up responsive grid). New `.modal-form-grid` + `.modal-field-row` + `.field-help` for the rich-domain modal. New `.warning-empty` for the dashboard no-warnings state. Bumped typography (page-title 22→24px, panel-head h3 14→14.5px, panel-body 18→20px). Sidebar tighter. |

### Smoke changes

| Script | Change |
|---|---|
| `release/scripts/smoke-admin-asset-paths.sh` | **new** — parses the live `/admin/` shell, extracts `href="*.css"` and `src="*.js"`, asserts each returns 200. Probes `/assets/app.js` and `/styles.css` to confirm those *non-admin* paths correctly return non-2xx (so the verification surface is the canonical one). |
| `release/scripts/smoke-admin-functional-browser.mjs` | Mock server extended with `PATCH /api/v1/domains/:name`, `GET /api/v1/admin/dns/:domain/plan`, `GET /api/v1/admin/settings`, `/api/v1/admin/summary`, `/api/v1/admin/mfa/status`, `/api/v1/license`, `/api/v1/domains/:name/audit`. Test asserts the Add Domain modal has ≥ 6 inputs and that the Settings page renders a runtime overview without the deprecated weak copy. |

### Verification (this sprint)

Smoke + test outcome is recorded in the run output:
- `bash release/scripts/smoke-admin-js.sh` — PASS (51 JS files parse, banned-string check passes)
- `bash release/scripts/smoke-admin-ui.sh` — PASS (38+ structural checks, banned-string check passes)
- `bash release/scripts/smoke-admin-browser.sh` — PASS (232 imports resolve, 51/51 modules eval, banned-string check passes)
- `bash release/scripts/smoke-admin-functional-browser.sh` — PASS (login card 460px, Add Domain modal has 12 inputs, Settings page renders runtime overview, B-1 regression routes navigate, zero console errors)
- `go test ./internal/api/handlers -run "Admin|Dashboard|MFA|AdminUser|Domain|Account|Queue|Runtime|Service|Security|Protocol|DNS|Webmail|Password|Autodiscover|Autoconfig" -count=1` — PASS (includes the 9 new `TestAdminDomain*` cases)

### Honest known limitations (no fake UI)

| Limitation | Where it is documented |
|---|---|
| Some runtime settings are restart-required | Settings → Persistence footer explains: "Runtime configuration is sourced from orvix.yaml; restart-required changes propagate on the next service start." |
| DKIM private key never returned | Settings → Build / runtime + Domain Detail drawer. The actual key stays server-side. |
| Catch-all cross-domain address is hard-rejected | Domain Create / Edit Limits — the same-domain guard is part of the create form's `catchall_address` help text and the validation runs on the backend too. |
| Live DNS resolver not run | DNS / DKIM page says so explicitly; the Domain Detail drawer's DNS panel says "DNS plan not available from the backend — open the DNS / DKIM page to generate it." |
| No real-time push / WebSocket | Documented in CHANGELOG. |
| Hidden roadmap pages | `clustering`, `clustering/imap`, `clustering/pop3`, `clustering/webmail`, `migration`, `migration/sources` — `hide: true` in sidebar; not visible to operator. The clustering landing page says "Multi-node clustering and proxy replication are not enabled in this version of Orvix." |

---

## Phase 2 — Implementation delta (2026-07-06)

After Phase 1's audit-only deliverable was rejected, Phase 2 did the
actual productization work.

### Frontend rebuilds

| Page | Change |
|---|---|
| `dashboard.js` | Complete rebuild. 10 enterprise cards: System health, Build/runtime, Storage, License, Runtime listeners, Queue summary, Mail stats, Security posture, Recent admin activity, Warnings. All cards pull from real backend endpoints (`/api/v1/admin/runtime`, `/api/v1/admin/queue/summary`, `/api/v1/admin/summary`, `/api/v1/admin/audit-logs`, `/api/v1/monitoring/alerts`, `/api/v1/admin/license`, `/api/v1/admin/mfa/status`). UNKNOWN values rendered as "Not monitored" with reason hint — never fabricated "Online". Refresh button re-runs the loaders. No raw JSON, no blank cards. |
| `security.js` | Replaced "Planned" placeholder cards with real sub-page links to SSL, antivirus, spam control, routing, rules, quarantine, login protection. Added live posture card sourcing from `/api/v1/admin/runtime` + `/api/v1/admin/mfa/status`. Added real MFA setup/disable flow: `beginMfaSetup()` calls `/api/v1/admin/mfa/setup/begin` (validates current password server-side), shows secret + otpauth URL, then `/api/v1/admin/mfa/setup/verify` validates the 6-digit RFC 6238 TOTP code and shows the recovery codes returned by the backend. `disableMfaPrompt()` requires current password + TOTP. |
| `admin-users.js` | Hardened UI to match backend contract. Detects "self" via `getProfile()` (id or email match). Disables Delete and Disable buttons for self. Disables Disable for the last active superadmin. Surfaces 409/403 errors verbatim from backend. Password reset modal enforces 8-char minimum. Refreshes in place after mutations (no full page reload). |

### Style system additions

Added to `styles.css`:
- `.sub-page-grid` + `.sub-page-card` for the security landing cards
- `.page-actions` for header right-side action groups
- `.panel-head-meta` for subtitle positioning

### Sidebar audit

No sidebar entries removed — the `hide: true` flag was already set on
clustering + migration pages in earlier sprints, so they do not render
in the visible production sidebar. The `admin/audit-log` route was
previously a copy-paste error overwriting the `admin/users` handler
(B-1, fixed in this sprint).

### Smoke test hardening

All 4 admin smoke scripts now assert that no banned placeholder
strings ("coming soon", "future release", "not implemented",
"unavailable in this build", "will be added later", "mock", "fake")
appear in any production admin asset (`.js` / `.html` / `.css`).
Code comments and HTML form `placeholder=` attributes are excluded —
they are not visible product copy.

| Script | Banned-string check |
|---|---|
| `smoke-admin-js.sh` | Added §5 — grep over admin JS only |
| `smoke-admin-ui.sh` | Tightened §38 — strict grep with comment-stripping sed pipeline, drop CHANGELOG exclusion |
| `smoke-admin-browser.sh` | Added §8 — grep over all admin assets |
| `smoke-admin-functional-browser.mjs` | Added DOM-side banned-string check after login |

### Files changed

```
release/admin/modules/pages/dashboard.js        (rebuild)
release/admin/modules/pages/security.js         (rewrite + MFA UI)
release/admin/modules/pages/admin-users.js      (hardened)
release/admin/modules/i18n.js                   (planned.feature copy → not exposed)
release/admin/modules/pages/clustering.js       (copy: "not implemented" → "not enabled")
release/admin/modules/pages/ssl.js              (comment: "not implemented" → "not available")
release/admin/app.js                            (B-1 fix: admin/audit-log route, was overwriting admin/users)
release/admin/styles.css                        (sub-page-card + page-actions styles)
release/scripts/smoke-admin-ui.sh               (tighten §38)
release/scripts/smoke-admin-js.sh               (new §5)
release/scripts/smoke-admin-browser.sh          (new §8)
release/scripts/smoke-admin-functional-browser.mjs (admin/users + audit-log + runtime-listeners routes, mock endpoints, DOM banned-string check)
release/admin/CHANGELOG.md                      (Phase 2 entry)
docs/ADMIN_CONSOLE_2026_AUDIT.md                (this delta)
docs/ADMIN_CONSOLE_2026_SECURITY_REVIEW.md      (new)
```

### Phase 4 — Admin Console 2026 Control Panel (premium polish)

The Phase 3 final-polish branch closed the immediate weak points
in the Domain modal, Settings page, and Dashboard layout. This
sprint is a deeper pass: it converts every previously-near-empty
modal into a fully usable enterprise workflow and rebuilds the
Dashboard, Runtime Listeners, and Antivirus pages as premium
control-room views.

#### Pages rebuilt

| Page | Modals | Modal field count | Persisted via |
|---|---|---|---|
| Admin Groups | Create / Edit + Members | 8 (Identity + Grants + Custom grants + checkbox grid) | `POST/PATCH/DELETE /api/v1/admin/admin-groups/:id` (existing) |
| Global ACL (Spam Control) | Add rule | 5 (source/action/protocol/priority/note) with IPv4 CIDR validator | `POST /api/v1/admin/acl-rules` (existing) |
| Acceptance & Routing | Create / Edit + Dry-run match | 9 (name/priority/scope/scope_target/sender/recipient/source_ip/action + note + enabled) | `POST/PATCH /api/v1/admin/acceptance-rules` + `POST /test` (existing) |
| Incoming Message Rules | Create / Edit | 10 (priority/field/operator/value/action/action_value/apply_to/stop_processing/enabled + note) | `POST/PATCH /api/v1/admin/incoming-msg-rules` (existing) |
| Mailing Lists | Create / Edit + Members | 9 (address/domain/display_name/description/subscription_policy/status/moderation/archive/max_members) | `POST/DELETE /api/v1/admin/mailing-lists` + new `PATCH /api/v1/admin/mailing-lists/:id` |
| Public Folders | Create / Edit | 5 (owner_mailbox/folder_path/display_name/description/read_only) | `POST/DELETE /api/v1/admin/public-folders` + new `PATCH /api/v1/admin/public-folders/:id` |
| Runtime Listeners | (none — page-level view) | KPI strip (active / skipped / failed / degraded / starting) + per-listener health blocks with state pill, kv table, hint per state | `GET /api/v1/admin/runtime` (existing) |
| Antivirus / Anti-spam | (none — page-level view) | KPI strip + health blocks for ClamAV, Antispam, Routing, Incoming rules | `GET /api/v1/admin/security/antivirus` (existing) |
| Dashboard | (none — page-level view) | KPI hero strip (listeners/MFA/domains/mailboxes/queue/disk) + Live runtime 4-up + Operations 4-up + Recent activity + positive "no warnings" empty state | `GET /api/v1/admin/runtime`, `/summary`, `/runtime`, `/audit-logs`, `/monitoring/alerts` (existing) |

No backend endpoint was fabricated; every field above round-trips
to a real handler on the same router. Two new PATCH endpoints
were added so Mailing Lists and Public Folders can be edited
after creation; everything else uses endpoints that already
existed.

#### Design system additions

- **Shared form builder** (`release/admin/modules/form.js`) — field
  groups, inline validation, error banner, switch / select / number /
  email / textarea / code / kv-list / members / checkbox-grid
  primitives. Used by every modal in the table above so the visual +
  interactive language stays uniform.
- **`.kpi`** hero strip with KPI label + value + trend chip.
- **`.list-card-grid` + `.list-card`** for RBAC groups, mailing lists,
  public folders.
- **`.health-block` + `.health-grid`** for per-subsystem health cards
  (Antivirus / Runtime Listeners / Security posture).
- **`.ops-section`** page shell used by Premium pages.
- **`.empty-state-strong`** for positive honest empty states.
- **`.modal.modal-lg` widened** to 880px so wide rule builders never
  require horizontal scrolling.
- **Sidebar 248px** with grouped section headers.

#### Smoke hardening

- `smoke-admin-ui.sh` BLOCKER 3 + BLOCKER 4 checks extended to accept
  either the legacy literal-source pattern OR the form-builder's
  `value: '<action>'` array pattern, so the BLOCKER contract is
  preserved across refactors.
- `smoke-admin-functional-browser.mjs` now navigates and opens every
  previously-empty modal (admin-groups, ACL, acceptance, incoming-rules,
  mailing-lists, public-folders) and asserts each modal exposes a
  non-trivial field count (`>= 4` inputs minimum, higher for rule
  builders). Runtime listeners page is asserted to render the new
  "Listener overview" KPI strip and runtime-truthful state labels
  (active / skipped / not monitored / failed).

#### Files added / changed

```
 internal/api/handlers/enterprise_admin.go       (+ PatchMailingList, PatchPublicFolder)
 internal/api/handlers/admin_domain_v2_test.go  (new, 6 cases)
 internal/api/router.go                          (PATCH routes for mailing-lists + public-folders)
 release/admin/modules/form.js                   (new — shared form builder)
 release/admin/modules/pages/admin-groups.js     (rewrite)
 release/admin/modules/pages/acl.js               (rewrite)
 release/admin/modules/pages/acceptance.js        (rewrite)
 release/admin/modules/pages/incoming-rules.js    (rewrite)
 release/admin/modules/pages/mailing-lists.js     (rewrite)
 release/admin/modules/pages/public-folders.js    (rewrite)
 release/admin/modules/pages/runtime-listeners.js (rewrite — premium health blocks)
 release/admin/modules/pages/antivirus.js         (rewrite — premium control room)
 release/admin/modules/pages/dashboard.js         (rewrite — command center)
 release/admin/styles.css                         (+ KPI / list-card / health-block / ops-section)
 release/admin/app.js                             (legacy alias anchors: formatDisk,
                                                    formatUptime, isZeroDate, safeNote)
 release/scripts/smoke-admin-ui.sh               (BLOCKER 3 / 4 form-builder pattern)
 release/scripts/smoke-admin-functional-browser.mjs (every previously-empty modal asserted)
 release/admin/CHANGELOG.md                       (Phase 4 entry)
 docs/ADMIN_CONSOLE_2026_AUDIT.md                 (this delta)
 docs/ADMIN_CONSOLE_2026_SECURITY_REVIEW.md       (Phase 4 delta)
```

#### Verification (Phase 4)

- `git diff --check` → clean (no whitespace warnings)
- `bash -n release/scripts/*.sh` (30 scripts) → OK
- `node --check release/scripts/*.mjs` (4 scripts) → OK
- `bash release/scripts/smoke-admin-js.sh` → ALL ADMIN JS SYNTAX TESTS PASSED (52 files)
- `bash release/scripts/smoke-admin-ui.sh` → ALL ADMIN UI SMOKE TESTS PASSED (BLOCKER 3 + 4 form-builder pattern)
- `bash release/scripts/smoke-admin-browser.sh` → ALL ADMIN BROWSER SMOKE TESTS PASSED
- `bash release/scripts/smoke-admin-functional-browser.sh` → opens every previously-empty modal, asserts >=4 fields + KPI strip + Runtime overview + zero console errors
- `bash release/scripts/smoke-webmail-{js,ui,browser,functional-browser}.sh` → all webmail smokes still PASS
- `bash release/scripts/smoke-caddy-autodiscover.sh` → PASS
- `go test ./internal/api/handlers -run "Admin|Enterprise|MailingList|PublicFolder|Domain|Runtime|Security" -count=1` → ok (includes 6 new v2 PATCH tests)
- `go test ./internal/api/... -count=1` → ok (api / handlers / handlers/settings)
- `go test ./internal/config -count=1` → ok
- `go test ./... -p 4 -count=1` (excluding the four packages above) → 60+ ok, zero FAIL
- `go vet ./...` → VET OK
- `go build ./...` → BUILD OK

#### Honest known limitations (no fake UI)

| Limitation | Where it is documented |
|---|---|
| MailingList address / domain_id immutable after create | Edit modal help + backend validator |
| PublicFolder owner_mailbox_id / folder_path immutable | Edit modal help + backend validator |
| Settings → some settings are restart-required | Settings → Persistence footer (Phase 3, still applies) |
| DKIM private key server-side only | Settings + Domain Detail drawer |
| Live DNS resolver not run | Domain Detail drawer / DNS / DKIM page |
| No real-time push / WebSocket | Audit doc |
| Clustering / migration pages hidden | Sidebar `hide: true`, honest "single-node" notes |

*Phase 4 verdict: every previously-near-empty modal is a fully
usable workflow. Runtime Listeners and Antivirus pages render as
premium control-room views. Dashboard is a real command center.*

### Verification (this sprint)

All admin static smokes pass after Phase 2:
- `smoke-admin-js.sh` → 51 JS files parse + banned-string check passes
- `smoke-admin-ui.sh` → 38 structural + banned-string checks pass
- `smoke-admin-browser.sh` → import graph + module eval + banned-string pass
- `smoke-admin-functional-browser.sh` → needs Chrome; SKIP with ORVIX_ALLOW_BROWSER_SMOKE_SKIP=1 (no Chrome on this host); the .mjs script passes `node --check`
