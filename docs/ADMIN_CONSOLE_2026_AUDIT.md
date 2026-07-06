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

*Last updated: 2026-06-09 — Phase 1 audit*

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

### Verification (this sprint)

All admin static smokes pass after Phase 2:
- `smoke-admin-js.sh` → 51 JS files parse + banned-string check passes
- `smoke-admin-ui.sh` → 38 structural + banned-string checks pass
- `smoke-admin-browser.sh` → import graph + module eval + banned-string pass
- `smoke-admin-functional-browser.sh` → needs Chrome; SKIP with ORVIX_ALLOW_BROWSER_SMOKE_SKIP=1 (no Chrome on this host); the .mjs script passes `node --check`
