# Orvix × Stalwart Enterprise — Parity Audit

> Branch: `feature/orvix-admin-enterprise-parity`
> Base: `f474b05` (main, post Admin Visual Renaissance v2 + login security polish)
> Author: parity audit, run before any UI work on this branch
> Stalwart benchmark: <https://stalw.art/enterprise/> — quality bar only, no copied
> logos / strings / icons / assets.

This document is the **truth** for what Orvix already ships versus what Stalwart
Enterprise advertises. It is the contract this branch implements against: every
visible surface is either (a) backed by a real endpoint, or (b) explicitly hidden
or honest-labeled.

---

## 1. Status legend

| Status | Meaning |
| --- | --- |
| **SHIPPED** | Real backend endpoint + real admin surface + tests contract pinning it. |
| **PARTIAL** | Backend exists, UI surfaces only the known-good subset, missing pieces hidden. |
| **BACKEND_MISSING** | No data model / endpoint at all. UI is hidden. |
| **UI_MISSING** | Backend endpoint exists, no admin surface yet — implement in this branch. |
| **IMPLEMENT** | Will be implemented in this branch on top of real data. |
| **HIDE_FROM_UI** | Sidebar link removed / page renders an honest "Not in this build" panel. |

---

## 2. Feature matrix

### 2.1 Dashboard & navigation

| # | Feature | Stalwart Enterprise surface | Orvix status | Notes / action in this branch |
| --- | --- | --- | --- | --- |
| 1 | Operator Dashboard (KPIs, alerts, licenses, activity) | Enterprise dashboard with role-aware widgets | **PARTIAL** (`release/admin/modules/pages/dashboard.js`) | Implements Overview / Network / Security / Delivery / Performance / Storage **tabs** in this branch. Source: `/admin/summary`, `/admin/runtime`, `/monitoring/alerts`, `/admin/queue/summary`, `/monitoring/capacity`. No fabricated charts. |
| 2 | Light/dark skin | Theme switcher | **SHIPPED** (`:root.theme-light` + `orvix_theme` localStorage) | Extend contact-sheet to capture `light-dashboard` + `light-login`. |

### 2.2 Login & access security

| # | Feature | Stalwart Enterprise surface | Orvix status | Notes / action in this branch |
| --- | --- | --- | --- | --- |
| 3 | Single Sign-On (OIDC / SAML) | SSO via OIDC / SAML providers | **BACKEND_MISSING** | `s_s_o_configs` table exists but no admin API. Hide from sidebar. |
| 4 | Two-factor auth (TOTP) | Admin MFA | **SHIPPED** (`/api/v1/admin/mfa/*`, `/auth/mfa/verify`) | Honesty on login: text only says "MFA" when `/admin/mfa/status` returns enabled. Otherwise MFA field stays hidden. |
| 5 | Per-IP rate limiting + lockout | Rate limiter + lockouts | **SHIPPED** (`/admin/login-protection/status`, `/admin/login-protection/lockouts`, POST `/lockouts/:key/clear`) | Honesty: `release/admin/modules/pages/login-protection.js` says "rate-limit + lockout" — **never** "Fail2Ban". OS integration is separate. |
| 6 | Brute-force audit log | Audit log feed | **SHIPPED** (`/audit/logs`, audit_failed_login events) | Covered by audit-page table. |
| 7 | App-specific passwords | "App passwords" | **BACKEND_MISSING** | Hide from UI; webmail IMAP uses account password. |
| 8 | Role / grant management | Admin roles, custom grants | **SHIPPED** (`/admin/admin-groups`, `/admin/admin-users`, grants `monitoring.read`, `monitoring.resolve`, etc.) | Cover in admin-groups page; nothing extra this branch. |

### 2.3 Tenant & account management

| # | Feature | Stalwart Enterprise surface | Orvix status | Notes / action in this branch |
| --- | --- | --- | --- | --- |
| 9 | Multi-tenant scoping | Tenant isolation | **BACKEND_MISSING for admin API** — the `tenants` table exists with `id, name, slug, domain, plan, max_domains, max_mailboxes, logo_url, primary_color, active, reseller_id`, and `internal/auth/tenant.go` writes `c.Locals("tenant_id")`. **There is no `/api/v1/admin/tenants` route.** | Implement honest "Tenants" page in this branch — read-only — showing the row from `current_tenant` middleware and a truthful "Multi-tenant management UI is not exposed in this build" note for write operations. |
| 10 | Reseller hierarchy | Reseller portal | **BACKEND_MISSING** | `resellers` table exists with no admin API. Hide "Resellers" from sidebar; do not render a page. |
| 11 | Account self-service (end user) | Account manager | **BACKEND_MISSING** | No `/api/v1/account/*` route. Hide "Account manager" from sidebar. |
| 12 | Domain manager | Domain lifecycle (create, DKIM, DMARC, MTA-STS, catch-all, status) | **SHIPPED** (`/domains`, PATCH `/domains/:name`, `/domains/:name/audit`, MTA-STS) | Already polished in `admin-final-domain-settings`. Keep. |
| 13 | Mailbox / user manager | User CRUD + quota + protocols + bulk import | **SHIPPED** (`/users`, `/mailboxes`, PATCH `/mailboxes/:id/{password,status,quota,protocols}`, `/mailboxes/import`, `/mailboxes/import/dry-run`) | Cover in `accounts.js`. |

### 2.4 Delivery, queue, antispam, antivirus

| # | Feature | Stalwart Enterprise surface | Orvix status | Notes / action in this branch |
| --- | --- | --- | --- | --- |
| 14 | Queue admin (summary, list, retry / bounce / cancel / hold) | Queue management | **SHIPPED** (`/admin/queue/summary`, `/admin/queue/messages`, `:id` retry/bounce/cancel) | Cover in `queue.js`. |
| 15 | Acceptance / routing rules | Inbound rule chain | **SHIPPED** (`/admin/incoming-rules`, `/admin/acceptance`) | Cover in `acceptance.js`. |
| 16 | Antispam engine config | Spam classifier settings | **PARTIAL** (`internal/antivirus/engine.go` exposes `Policy()`, `RuntimeEnforced()`, `LastError()`. UI calls `/admin/summary` `security` slice) | Show ONLY what `engine.Policy()` reports. **F: Remove any UI element that suggests an "AI" classifier or a classifier backend that is not wired.** |
| 17 | Antivirus (ClamAV / Kaspersky) | AV engine config | **SHIPPED** (`internal/antivirus/engine.go`, `/admin/antivirus`-style page) | Cover in `antivirus.js`; show real enforced-policy. |
| 18 | Inbox rules + sieve scripts | Sieve editor | **SHIPPED** (`/admin/log-rules`) | Cover in `log-rules.js` (note: used for server-side log routing, not user sieves). |
| 19 | Quarantine admin | Quarantine release / delete | **SHIPPED** (`/admin/quarantine`, POST `:id/resolve`) | Cover in `quarantine.js`. |
| 20 | Message trace | Per-message trace | **SHIPPED** (queue detail) | Cover in `queue.js` per-message view. |

### 2.5 Observability, alerting, telemetry

| # | Feature | Stalwart Enterprise surface | Orvix status | Notes / action in this branch |
| --- | --- | --- | --- | --- |
| 21 | Live health snapshot | Subsystem health map | **SHIPPED** (`/monitoring/health`) | Render in observability tab. |
| 22 | Capacity snapshot (disk / mailboxes / messages / attachments) | Capacity dashboard | **SHIPPED** (`/monitoring/capacity`) | Render in observability tab. |
| 23 | Metrics / counters (per-endpoint, per-protocol) | Metrics stream | **SHIPPED** (`internal/observability/metrics.go` counters: SMTP accepted, auth failed, queue delivered, AV scanned, backup uploaded, etc.) | Implement **B** (Observability page) in this branch: read-only counters + 24h trend surface that returns honest "(no retention)" if backend does not retain series. |
| 24 | Alerts + delivery audit | Active alerts + delivery audit (in-app / webhook / email) | **SHIPPED** (alerts stream + `DeliveryRecord` + `InAppProvider`/`WebhookProvider`/`EmailProvider`) | Implement **C** (Alerts page) in this branch — separate route from monitoring; renders active alerts table + delivery audit feed from `Dispatcher.ListDeliveries`. POST `/monitoring/alerts/:id/resolve` already exists. |
| 25 | OpenTelemetry export | OTLP export | **PARTIAL** (counters exist; no OTLP exporter) | Honest: "Exporters are not enabled in this build." |
| 26 | Log shipping rules | Log rule routing | **SHIPPED** (`/admin/log-rules`) | Cover in `log-rules.js`. |
| 27 | Service / daemon status | Service health | **PARTIAL** (Caddy health check via frontend probe — no node-management API) | Render `runtime-listeners` page; expose honest note for systemd / supervise. |

### 2.6 Storage & data protection

| # | Feature | Stalwart Enterprise surface | Orvix status | Notes / action in this branch |
| --- | --- | --- | --- | --- |
| 28 | Storage topology (single / sharded / read-replica) | Storage map | **PARTIAL** (one backend per process; no sharding / replica routing API) | Implement **G** (Storage topology page): honest single-backend card, list mounted volumes from filesystem, show the per-data-dir message / attachment counts the admin endpoint reports. **No fake "replica" / "shard" knobs.** |
| 29 | Backups (scheduled, history, restore) | Backup manager | **SHIPPED** (`/admin/backups`, `/admin/backups/schedule`, `:id/download`, retention + metrics + health) | Cover in `backups.js`. |
| 30 | Per-mailbox archive | Archive mailbox | **BACKEND_MISSING for archive mailbox tier** (only `/webmail/messages/:id/archive` per-message state) | Honesty: archive page is per-message; no per-mailbox archive tier in this build. |
| 31 | Tamper-evident audit log | Audit log | **SHIPPED** (`/audit/logs`, audit_failed_login, etc.) | Cover in `audit-log.js`. |

### 2.7 Branding, compliance, integrations

| # | Feature | Stalwart Enterprise surface | Orvix status | Notes / action in this branch |
| --- | --- | --- | --- | --- |
| 32 | Per-tenant branding (logo, color) | Tenant theme | **BACKEND_MISSING for admin API** (data columns exist on `tenants`) | Implement honest "Branding" page: writes to `tenants` row via a **new** `PATCH /api/v1/admin/tenants/:id/branding` endpoint that we add; uses CSRF + RBAC. If row missing, render honest empty state. Document the limit in the UI. |
| 33 | Anti-phishing / DMARC reporting | DMARC reports | **SHIPPED for DKIM/DMARC/MTA-STS** (DNS ops page) | Already covered in `dns-dkim.js`. |
| 34 | Calendar / contacts | CalDAV / CardDAV | **SHIPPED** (calendar module) | Out of scope here. |
| 35 | Compliance / GDPR / retention | Retention policies | **BACKEND_MISSING** | Hide. |

### 2.8 Clustering, sharding, replicas

| # | Feature | Stalwart Enterprise surface | Orvix status | Notes / action in this branch |
| --- | --- | --- | --- | --- |
| 36 | Multi-node clustering | Cluster manager | **PARTIAL** (`/admin/cluster/status` endpoint + `release/admin/modules/pages/clustering.js` rendering the truthful single-node state) | Keep; surface a "Cluster status" card on the new Performance tab. **Never claim replication is on.** |
| 37 | Read replicas | Read-only replicas | **BACKEND_MISSING** | Hide any "replica" UI. |
| 38 | Sharded stores | Sharding | **BACKEND_MISSING** | Hide any "shard" UI. |
| 39 | IMAP / POP3 / WebMail / JMAP proxy | Reverse proxy | **PARTIAL** (Caddy in front of the binary; `proxy_protocol` settings exist) | Show Caddy-managed state; never claim a proxy server is on the orvix process. |
| 40 | Fail2Ban (OS integration) | OS-level firewall integration | **BACKEND_MISSING in orvix process** (rate-limit + lockout is a **different** feature) | Keep login-protection page wording honest: **"Login protection: rate-limit + automatic lockout. OS-level Fail2Ban integration is not part of orvix itself."** |

---

## 3. What this branch will ship

| # | Deliverable | Source data | Acceptance contract |
| --- | --- | --- | --- |
| A | **Dashboard with Overview / Network / Security / Delivery / Performance / Storage tabs** | `/admin/summary`, `/admin/runtime`, `/admin/queue/summary`, `/monitoring/{health,capacity,snapshot,alerts}`, `/admin/cluster/status`, queue + storage metrics | Each tab renders only when its source endpoint returns data; missing source ⇒ honest empty state (not `[object Object]`, not "loading…" that never resolves). |
| B | **Observability page** at `#/observability` | `/monitoring/snapshot`, `/monitoring/health`, `/monitoring/capacity`, `/admin/runtime` | Reads only counters the backend exposes. Renders honest "(series not retained)" for trend lines that would require historical storage. |
| C | **Alerts page** at `#/alerts` | `/monitoring/alerts`, POST `/monitoring/alerts/:id/resolve`, `internal/monitoring.Dispatcher.ListDeliveries` via a new `GET /api/v1/monitoring/alert-deliveries` endpoint | Active alerts table + Delivery audit table; CSRF + RBAC (`monitoring.resolve`). |
| D | **Tenants read-only page** at `#/tenants` | New `GET /api/v1/admin/tenants/current` reading from the JWT tenant context (no write API) | Hides write UI; shows the resolved row + a clear "Multi-tenant write API is not exposed in this build" note. |
| E | **Branding page** at `#/branding` | New `GET/PATCH /api/v1/admin/tenants/:id/branding` backed by `tenants.logo_url` + `tenants.primary_color` | CSRF + admin RBAC; live preview of login shell primary color; honest note that rows must already exist. |
| F | **AI spam honesty** — antivirus / antispam page removes all fake "classifier accuracy" widgets and only renders `engine.Policy()`, `engine.RuntimeEnforced()`, `engine.LastError()` | `internal/antivirus/engine.go` + `/admin/summary.security` | New frontend test pins the absence of bogus classifications. |
| G | **Storage topology page** at `#/storage-topology` | `internal/api/handlers.AdminSummary`'s `storage` slice + `/monitoring/capacity` disk + `/admin/runtime` listeners + queue sizes | Single-backend card + per-volume file system usage derived from real `statfs` data via a new `GET /api/v1/admin/storage/volumes` endpoint. **No "replica" or "shard" controls.** |

### 3.1 Out of scope for this branch (verified honest above)
- Multi-tenant **write** UI (backend has the data model, no admin API; page is honest).
- App passwords, Fail2Ban (rate-limit + lockout is on; Fail2Ban is OS integration).
- Cluster replication / read replicas / sharded stores.
- AI spam classifier.
- Resellers portal.

---

## 4. Engineering guardrails (do not regress)

1. **Honest empty states** — every panel renders a literal empty / "Not configured" / "Managed by Caddy" / "Not monitored" message when its source endpoint returns no data. The dashboard `safeCount()` / `asText()` helpers from the prior pass must be reused so a nested object can't render as `[object Object]`.
2. **No fake features** — every visible widget must have a backing endpoint reachable from the route handler. New endpoints must be:
   - gated by `csrf.RequireCSRF` for any non-GET;
   - gated by `auth.RequireAnyRole(auth.RoleAdmin, auth.RoleSuperAdmin)`;
   - audited via the existing audit store on writes;
   - covered by a contract test in `internal/api/handlers/*_test.go`.
3. **No OS-coupling claims** — login page and login-protection page must keep saying **"rate-limit + lockout"** and not **"Fail2Ban"**.
4. **Banned-string compliance** — no `coming soon`, `future release`, `not implemented`, `unavailable in this build`, `will be added later`, `mock`, `fake`, `[object Object]`. New honest labels are encouraged: "Not configured", "Not monitored", "Managed by Caddy", "Single-node build".
5. **No new dependencies** — no new Go modules, no new Node packages. Use what's already in `go.mod` / `package.json`.

---

## 5. Verification plan

After implementation:

1. **Go tests** — `go test ./internal/api/handlers/...`, `go test ./internal/observability/...`, `go test ./internal/monitoring/...`, `go test ./internal/antivirus/...`, `go test ./internal/backup/...`, full `./... -p 4`. Must stay green.
2. **Frontend contract tests** — `release/scripts/smoke-admin-js.sh`, `smoke-admin-ui.sh`, `smoke-admin-browser.sh`, `smoke-admin-functional-browser.mjs`. Must pass; new `test-admin-observability-empty-state` plus `test-admin-storage-topology-no-fake-shards` added.
3. **Smokes** — webmail JS / UI / browser / functional-browser + caddy-autodiscover. All green.
4. **Screenshot harness** — extended to capture the 15 required PNGs (`release/scripts/smoke-admin-visual-screenshots.mjs`). All produced.
5. **Staging gate** — `git status --short` lists only the files in §6. No artifacts, no `.opencode/`, no `dev/null`, no `web/`, no `release/webmail/`, no `orvix.yaml`, no `watchdog.ps1`, no unrelated docs / migrations.

---

## 6. Files expected to change

Backend (Go):

- `internal/api/handlers/handlers.go` (+ test) — register `GetAdminTenant`, `PatchAdminTenantBranding`, `ListStorageVolumes`, `ListAlertDeliveries`.
- `internal/api/router.go` — wire the 4 new admin routes under RBAC + CSRF.
- `internal/api/handlers/<new>_test.go` — minimal contract tests.

Frontend (admin SPA):

- `release/admin/modules/pages/dashboard.js` — tabbed dashboard.
- `release/admin/modules/pages/observability.js` *(new)*.
- `release/admin/modules/pages/alerts.js` *(new)*.
- `release/admin/modules/pages/storage-topology.js` *(new)*.
- `release/admin/modules/pages/tenants.js` *(new)* — read-only honest page.
- `release/admin/modules/pages/branding.js` *(new)* — backed by new PATCH endpoint.
- `release/admin/modules/pages/antivirus.js` — strip any fake classifier UI; surface only `engine.Policy()` + `LastError()`.
- `release/admin/modules/app.js` — register new routes.
- `release/admin/modules/sidebar.js` — add new entries + hide `clustering` multi-node tabs that imply replication.
- `release/admin/styles.css` — enterprise-table, observability-grid, storage-card CSS.

Docs / harness:

- `docs/ORVIX_STALWART_ENTERPRISE_PARITY_AUDIT.md` (this file).
- `release/scripts/smoke-admin-visual-screenshots.mjs` — extended for the 15 required PNGs.

---

## 7. Open questions for the user (none blocking)

All decisions above are derivable from the codebase. Nothing on the list requires
upstream clarification. If the user wants the audit doc wording changed or the
prioritization reshuffled, change here first.
