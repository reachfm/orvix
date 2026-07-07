# Orvix Admin Console â€” Enterprise 2 â€” Changelog

## 2026 Sprint: Admin Console 2026 Productization

Branch: `feature/admin-console-2026-productization`
Base: latest `main`

### Phase 1 â€” Feature audit, bug fixes, smoke test hardening

#### Feature audit
- Created `docs/ADMIN_CONSOLE_2026_AUDIT.md` â€” full feature matrix classifying every
  visible admin menu item, page action, and sidebar entry as WORKING / PARTIAL /
  STUB / BROKEN / MISSING. This document is the authoritative reference for the
  current state of the admin console.

#### Bug fixes
- **B-1 (HIGH):** Fixed `admin/users` registered twice in `app.js`. The second
  registration (`auditLog.renderAuditLogPage`) was silently overwriting the first
  (`adminUsersPage.renderAdminUsersPage`), making the Admin Users page inaccessible
  from the sidebar. Removed the duplicate line and added `admin/audit-log` as the
  correct standalone route for the audit log page.

- **C-1 (LOW):** Removed dead import of `renderPlannedPage` from `security.js`.
  The import was never called; the MFA section renders honest "No MFA telemetry
  reported." copy and the remaining security sections use `badge("planned")` chips.

#### Smoke test hardening
- Added check 38 to `smoke-admin-ui.sh`: banned-string grep that scans all
  `release/admin/` assets for forbidden placeholder strings (`"coming soon"`,
  `"future release"`, `"not implemented"`, `"mock"`, `"fake"`, etc.) as visible
  UI text. Excludes `_planned.js`, form `placeholder=` attributes, and code
  comments.

- Extended `smoke-admin-functional-browser.mjs`:
  - Added `admin/users`, `admin/audit-log`, and `runtime-listeners` route
    navigations (regression guard for B-1 fix).
  - Added DOM-based banned-string check after login to catch rendered
    placeholder strings at runtime.
  - Extended mock server with `/api/v1/admin/users`, `/api/v1/admin/audit-logs`,
    and `/api/v1/admin/runtime` endpoints so the new route tests pass.

#### Confirmed solid (no changes needed)
- MFA backend (`admin_mfa.go`) is fully implemented with TOTP setup/verify/disable
  flows â€” the `security.js` frontend gracefully handles the "no MFA configured"
  state.
- Backend test coverage is already comprehensive: `admin_users_test.go` (210 L),
  `admin_queue_test.go` (616 L), `admin_queue_operations_test.go` (343 L),
  `admin_runtime_test.go` (1108 L), `admin_mfa_test.go` all exist and cover
  CRUD, RBAC, CSRF, and error paths.
- All sidebar routes are registered; no routes silently fall through to
  `renderPlannedPage`.
- Clustering / migration pages are hidden from sidebar (`hide: true`) and render
  honest single-node / no-jobs states — acceptable stubs, not broken features.

---

## 2026 Final Polish — Domain + Settings + Asset Paths

Branch: `feature/admin-final-domain-settings-polish`
Base: `715db0d` (Phase 2 productization)
Date: 2026-07-07

This is the final practical admin polish sprint. No new attack
surface; every change makes the existing surfaces more useful
and verifiable.

### Phase 3 — Final Polish

#### Domain provisioning UI

- `pages/domains.js` rewrites the "Add Domain" modal from a single
  field to a 12-field enterprise modal: status (active/suspended),
  plan (smb/enterprise/education/free), description, max_mailboxes,
  max_aliases, max_quota_mb, dkim_enabled + dkim_selector,
  dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact.
  Two-column responsive grid in the large modal.
- Manage Domains table gains columns: Plan, Mailboxes used/max,
  Quota MB, DKIM, Updated. Each domain row shows live state from
  the backend (`/api/v1/domains`).
- Domain Detail drawer renders summary panel (every persistent
  property), Mailboxes table, DNS records panel with per-row
  copy buttons + bulk "Copy all records", Audit trail, plus
  Suspend / Edit limits / Delete actions.
- New "Edit limits" modal targets the new `PATCH /api/v1/domains/:name`
  endpoint so the operator can change catch-all, DKIM selector,
  plan, and limits without losing the original domain.

#### Settings page = polished runtime overview

- `pages/settings.js` is rewritten. Old weak copy "Backend reports
  no mutable settings in this build." is gone.
- Always renders Build & runtime, Listener Bindings, Security Posture,
  Protocol toggles, Mutable settings (only if allowlist keys exist),
  Persistence footer.
- Save controls only render for keys in the backend allowlist — no
  fake mutable form.

#### Security posture rewritten

- `pages/security.js` "Posture" card uses Enabled / Disabled /
  Not configured / Not monitored / Managed by Caddy instead of
  unexplained "unknown" / "-".
- `pages/dashboard.js` security card now reads MFA / CSRF / Login
  protection / TLS posture with explicit labels and short hints.
- Warnings empty state is a positive green check instead of an
  empty row.

#### Dashboard layout polish

- New `.dash-grid-4`: 4-up on >1280px, 2-up on 720–1280px, 1-up on phones.
- Card minimum bumped from 280px → 320px so every card stays readable
  at 100% browser zoom.
- Typography bumped (page-title 22→24px, panel-head h3 14→14.5px,
  panel-body 18→20px).
- Sidebar tighter (240px max).
- Modal form grid responsive (2-up desktop, 1-up mobile).

#### Asset verification

- The previous verification flow probed `/assets/app.js` and
  `/styles.css` at the proxy root, both of which 404 because
  the admin SPA assets live under `/admin/*`. New
  `release/scripts/smoke-admin-asset-paths.sh` parses the live
  `/admin/` HTML, extracts every `<link>` and `<script>` URL,
  asserts each returns 200, and probes the *non-admin* paths to
  confirm they remain non-2xx (so the smoke is the canonical
  verification surface).

### Backend additions

| Verb | Path | Notes |
|---|---|---|
| `POST` | `/api/v1/domains` (extended) | 12 fields. Validators: DKIM selector is a DNS label (no spaces/slashes); catch-all must be on same domain; limits are non-negative; plan must be from `{smb, enterprise, education, free}`. |
| `GET` | `/api/v1/domains/:name` (extended) | Returns every persistent field, mailboxes, used counts. |
| `PATCH` | `/api/v1/domains/:name` (new) | Allowlist of 11 mutable keys. RBAC + CSRF + audit. Unknown keys hard-reject (whole patch rolled back). |

### Tests added

- `internal/api/handlers/admin_domain_advanced_test.go`:
  - `TestAdminDomainCreateAdvancedFields` — every field persists + round-trips.
  - `TestAdminDomainCreateDefaultDkimSelector` — safe default when enabled with no selector.
  - `TestAdminDomainCreateInvalidCatchall` — same-domain guard.
  - `TestAdminDomainCreateBadDkimSelector` — DNS-label allowlist.
  - `TestAdminDomainCreateNegativeLimit` — limits never wrap.
  - `TestAdminDomainPatchAllowedFields` — every mutable field round-trips.
  - `TestAdminDomainPatchUnknownFieldHardReject` — unknown keys abort the patch.
  - `TestAdminDomainPatchRBACEnforced` — non-admin cannot patch.
  - `TestAdminDomainGetReturnsAllFields` — full GET shape stable.

### Files changed

- `internal/api/handlers/handlers.go` (`CreateDomain`, `GetDomain`,
  new `PatchDomain`).
- `internal/api/router.go` — new `PATCH /api/v1/domains/:name` route.
- `internal/api/handlers/admin_domain_advanced_test.go` — new.
- `release/admin/modules/pages/domains.js` — rewrite.
- `release/admin/modules/pages/settings.js` — rewrite.
- `release/admin/modules/pages/security.js` — posture labels.
- `release/admin/modules/pages/dashboard.js` — security card
  labels, warnings empty state.
- `release/admin/styles.css` — `dash-grid-4`, modal form grid,
  warning-empty, sidebar polish.
- `release/scripts/smoke-admin-asset-paths.sh` — new.
- `release/scripts/smoke-admin-functional-browser.mjs` —
  extended mock server (PATCH, GET single domain, DNS plan,
  settings, summary, mfa/status, license), Add Domain
  ≥6 inputs assertion, Settings overview copy assertion.
- `release/admin/CHANGELOG.md` — this entry.
- `docs/ADMIN_CONSOLE_2026_AUDIT.md` — Phase 3 delta.
- `docs/ADMIN_CONSOLE_2026_SECURITY_REVIEW.md` — Phase 3 delta.

### Honest known limitations

Documented in the audit doc and the dashboard itself; no fake
UI copy:

| Limitation | Where it is documented |
|---|---|
| Some runtime settings are restart-required | Settings → Persistence footer |
| DKIM private key server-side only | Settings page + Domain Detail drawer |
| Catch-all must be on same domain | Domain modal help text + backend validator |
| Live DNS resolver not run | Domain Detail drawer (DNS section) |
| No real-time push / WebSocket | Audit doc |
| Clustering / migration pages hidden | Sidebar `hide: true`, `clustering.js` honest "single-node" note |

---

## Enterprise 2

A focused enterprise upgrade of the Orvix Admin Console. No backend
changes â€” every change is a frontend upgrade on top of the existing
admin API surface (`/api/v1/admin/*`, `/api/v1/queue`,
`/api/v1/backups`, `/api/v1/update/*`, `/api/v1/monitoring/*`,
`/api/v1/audit/logs`, `/api/v1/license`, `/api/v1/dns/wizard/*`,
`/api/v1/domains`, `/api/v1/mailboxes`, `/api/v1/users`). The webmail
client and the CoreMail engine are untouched.

## What's new

### 1. Design system

- **Two-theme token system.** `:root` is the dark default;
  `:root.theme-light` is the light opt-in. The JS sets the class when
  the OS reports `prefers-color-scheme: light` (embedders can force
  either side by toggling the class on `<html>` before the script
  loads).
- **Semantic tiers** for background, text, border, accent, status,
  shadow, motion, radius and spacing â€” every component references
  tokens, no raw hex.
- **Focus rings only on `:focus-visible`** (keyboard, never mouse).
- **`prefers-reduced-motion: reduce`** collapses every transition /
  animation to ~0ms.
- **Logical properties** (`margin-inline-*`, `padding-inline-*`,
  `border-inline-*`, `inset-inline-*`) for RTL correctness in the
  drawer / topbar / table.

### 2. Sidebar / topbar

- Sidebar becomes the canonical nav (the previous HTML had a flat
  `<nav>` list â€” the new client renders it from a single source of
  truth so the keyboard-shortcut overlay and the sidebar never drift).
- Topbar carries the admin email, role badge, avatar, a per-section
  Refresh button, and Sign Out.
- Sticky behaviour so the sidebar and the topbar stay visible on
  long tables.

### 3. Dashboard

- Service-status grid (API, SMTP, IMAP, POP3, JMAP, Database, Redis /
  Queue, License) sourced from `/api/v1/health` + admin summary; "Not
  available" is shown if a metric is missing â€” never fabricated.
- Mail-stats grid (total / active / suspended / pending / deferred /
  bounced / delivered) from `/api/v1/admin/summary`.
- System panel: version, commit, hostname, uptime (if reported), disk
  (if reported), license mode, build time.
- Warnings panel: license public-key missing, queue has bounced /
  deferred entries, DKIM missing for known domains. Each warning
  carries a clear "next step" hint.

### 4. Domains

- List with search + status filter, columns: domain, plan, status,
  mailboxes, created, actions.
- Per-domain detail drawer (right slide-in) with Overview kv-list
  and a per-domain DNS records block (MX / SPF / DKIM / DMARC, copy
  buttons, PTR warning, manual `dig` verification block).
- Create / suspend / enable / delete flows via modal dialogs.
  Delete uses typed-confirmation if the domain has mailboxes.

### 5. Mailboxes

- List with search + domain + status + admin filters.
- Create modal with local-part + domain (dropdown from
  `/api/v1/domains`) + display name + password + confirm password.
  Password is never logged or echoed back after submit.
- Edit modal (display name, status), password reset modal, suspend /
  enable inline action, delete with confirmation.
- Per-mailbox detail drawer.

### 6. Queue diagnostics

- List with status filter (all / pending / deferred / bounced /
  delivered) and free-text search across from / to / last error.
- Detail drawer with: summary kv-list, last-error `<pre>` (escaped),
  per-attempt timeline (if present), status / enhanced status code,
  remote host / IP, TLS version, attempts, delivery mode,
  auto-diagnosis label (Delivered / Deferred / DNS-PTR-reputation /
  TLS handshake / Bounced / Local / Unknown).
- Actions: retry (with confirmation), delete (with typed-confirm),
  copy diagnostic (formatted multi-line block, clipboard-safe), refresh.

### 7. DNS / DKIM wizard

- Per-domain panel listing MX, SPF, DKIM and DMARC records to publish.
- Copy buttons on each record value.
- DKIM row shows honest "DKIM not configured â€” public key missing"
  message when no real key is present. The fake placeholder record
  is removed and the value is not copy-ready. No fake keygen UI.
- PTR / rDNS warning that it is provider-controlled.
- Manual `dig MX / dig TXT` verification block.
- DMARC record is generated with `p=quarantine` and a `rua=` report
  address.
- No fake green check â€” there is no live DNS resolver in this build,
  the panel says so explicitly.

### 8. Backups

- Status grid (last backup, schedule, health, count).
- Actions: Backup now (with confirmation), Run retention, Download
  (with confirmation), Delete (with typed-confirm).
- Honest "Restore is not exposed in this build" note instead of a
  fake restore button.

### 9. Updates

- Version / SHA / channel / status / latest cards.
- Preflight panel â€” every check rendered as a status badge + title +
  detail.
- History table.
- Check for updates, Apply update (typed-confirm), Refresh.

### 10. Monitoring / health

- Overall status card, open-alerts card, capacity card.
- Subsystems grid (every component returned by
  `/api/v1/monitoring/health`).
- Active alerts table with inline Resolve action.

### 11. Audit / logs

- Filters: severity, source / actor, since date.
- Server-side application log fallback block showing the exact
  `journalctl -u orvix` commands the operator can run on the host.

### 12. Settings

- Host configuration kv-list (hostname, version, commit, build time,
  status).
- Security posture kv-list (CSRF on writes, token storage policy, CSP,
  TLS / HSTS, queue-endpoint exposure to webmail).
- License kv-list (mode, expires, public key status, tier, seats).
- "Out of scope in this build" list â€” honest about what is not
  implemented (CSV import, in-place restore, in-UI DKIM keygen, direct
  config file edit).

### 13. UX primitives

- **Drawer** â€” right slide-in for domain / mailbox / queue detail.
- **Modal** â€” centered for every form / confirmation. Three sizes.
- **Toast** â€” top-right stack, capped at 4, FIFO.
- **Confirm-danger** â€” Promise-based; supports an optional
  typed-confirmation gate.
- **Copy-to-clipboard** â€” async Clipboard API + textarea fallback.
- **Empty / error / skeleton** states shared across every section.
- **Keyboard shortcuts** â€” `g <letter>` jumps between sections,
  `?` opens the shortcut overlay, `Esc` closes any overlay,
  `r` refreshes the current section.

### 14. Security guarantees preserved

- No localStorage / sessionStorage *added*. The pre-existing
  `orvix_admin_token` sessionStorage entry is preserved verbatim so
  the live `/admin/login` JWT handshake keeps working.
- No unsafe `innerHTML` for untrusted data. Every dynamic value goes
  through the shared `esc()` helper or is rendered as a DOM node
  (`el('td', { text: ... })`).
- CSRF on every state-changing call (the existing `csrfFetch()` with
  one-shot retry on a CSRF-specific 403).
- No external CDN / fonts / imports â€” every asset is local.
- No inline event handlers in the HTML (every event is wired from JS).

## Out of scope

- **CSV bulk import** â€” backend has no endpoint yet.
- **In-place backup restore** â€” backend has no restore endpoint; UI
  surfaces this honestly.
- **Inline DKIM key generation** â€” backend has no keygen endpoint;
  install-time only.
- **Live DNS resolver** â€” backend does not run one in this build.
- **Real-time push / WebSocket** â€” not in scope for ADMIN-ENTERPRISE-2.

## Files changed

- `release/admin/index.html` â€” replaced with the new shell (login,
  app frame, toast stack, semantic IDs).
- `release/admin/styles.css` â€” replaced with the enterprise design
  system (two themes, premium components, responsive, reduced-motion).
- `release/admin/app.js` â€” replaced with the modular enterprise
  client (helpers, drawers, modals, toasts, ten page renderers).
- `release/admin/CHANGELOG.md` â€” this file, new.
- `internal/api/handlers/admin_frontend_test.go` â€” new static-analysis
  tests mirroring the webmail test pattern (security guards, asset
  presence, no-queue-leak, no-unsafe-innerhtml patterns, responsive
  breakpoints, reduced motion, aria-label sweep).

---

## 2026 Sprint: Admin Console 2026 Control Panel — Premium Polish

Branch: `feature/admin-2026-control-panel`
Base: `4817388` (Phase 3 merged on main)
Date: 2026-07-07

This is a serious production-grade pass: every previously-near-empty modal
is now a fully usable enterprise workflow, and the dashboard / runtime
listeners / antivirus pages render as premium control-room views.

### Design system

- New shared form builder (`release/admin/modules/form.js`) — every
  admin modal uses the same primitives: field groups with title +
  subtitle, inline validation, error banner, switch / select / number /
  email / textarea / code / kv-list / members / checkbox-grid /
  switch / static field kinds, keyboard submit (Enter), Esc to close,
  submit-state spinner, sticky foot with primary + cancel actions.
- Premium dark control-room polish in `release/admin/styles.css`:
  * `.kpi` hero strip with KPI label + value + trend chip (good /
    bad / warn / flat).
  * `.list-card-grid` + `.list-card` for RBAC groups, mailing
    lists, and public folders (each card has title, tag row, meta,
    action row; never a "blank" appearance).
  * `.health-block` + `.health-grid` for premium card presentation
    (Antivirus, Runtime Listeners, Security Posture) — each subsystem
    gets its own health block with state pill, kv table, and hint.
  * `.ops-section` page shell for premium pages.
  * `.warning-empty` + `.empty-state-strong` for honest positive empty
    states.
  * `.modal.modal-lg` widened to 880px so wide forms never need
    horizontal scroll.
  * Sidebar 248px width with grouped sections.

### Pages rebuilt

- `pages/admin-groups.js` — list-card-grid render + new create / edit
  modal with grouped sections (Identity / Grants / Custom grants), a
  real checkbox-grid RBAC picker (organised by Domains / Mailboxes /
  Queue / Backups / DNS-etc), inline description helper, members modal
  with per-member remove + add-by-id flow.
- `pages/acl.js` — premium runtime-table + form with action dropdown
  (allow/deny), protocol dropdown (all/smtp/imap/pop3/jmap/webmail),
  priority with recommended-band hint, IPv4 CIDR validator, note
  textarea. Empty state offers a clear hint.
- `pages/acceptance.js` — full rule builder: priority, scope
  (global/domain/mailbox), scope_target, sender/recipient patterns,
  source IP CIDR, action (accept/reject/quarantine — runtime-true),
  enabled switch, note textarea. Keeps the dry-run match affordance.
- `pages/incoming-rules.js` — full rule builder: priority, field
  dropdown, operator dropdown, value, action
  (reject/quarantine/tag, runtime-true), `Action value` (tag label or
  quarantine reason), apply_to dropdown, stop_processing switch,
  enabled switch, note.
- `pages/mailing-lists.js` — list-card-grid + create / edit modal
  (address + domain immutable, all other fields editable via PATCH).
- `pages/public-folders.js` — list-card-grid + create / edit modal
  (owner mailbox + folder path immutable, all other fields editable).
- `pages/runtime-listeners.js` — premium health-block view: hero
  KPI strip (active / skipped / failed / degraded / starting), one
  card per listener with TLS marker, state pill, kv table, hint per
  state ("skipped = intentionally not listening", etc.).
- `pages/antivirus.js` — premium control room: KPI strip
  (antivirus / probe / antispam / routing), one health block per
  subsystem (ClamAV / antispam / acceptance / incoming message
  rules), honest notes section, real probe response fields.
- `pages/dashboard.js` — command center: KPI hero strip
  (listeners / MFA / domains / mailboxes / queue / disk), Live runtime
  band (4-up Health / Build / Storage / License), Operations band
  (4-up Listeners / Queue / Mail stats / Security), Recent activity
  (audit log) + Warnings (positive "no active warnings" empty
  state). Real backend endpoints only — no fabricated data.

### Backend additions

| Verb | Path | Notes |
|---|---|---|
| `PATCH` | `/api/v1/admin/mailing-lists/:id` (new) | Allowlist of 7 mutable keys (display_name, description, subscription_policy, status, moderation_required, archive_enabled, max_members). Unknown keys hard-reject. Subscription policy enum enforced (open / closed / moderated / announce). Status enum enforced (active / suspended / archived). RBAC + CSRF + audit. Rejects negative max_members. |
| `PATCH` | `/api/v1/admin/public-folders/:id` (new) | Allowlist of 3 mutable keys (display_name, description, read_only). Unknown keys hard-reject. Boolean validator on read_only. RBAC + CSRF + audit. |

### Tests added

- `internal/api/handlers/admin_domain_v2_test.go` (new, 6 cases):
  - `TestAdminMailingListPatchAllowedFields` — all 7 editable keys
    applied + re-fetch via GET shows the patch.
  - `TestAdminMailingListPatchUnknownFieldHardReject` — unknown
    key aborts the entire PATCH (atomic rollback).
  - `TestAdminMailingListPatchRBACEnforced` — non-admin JWT cannot
    PATCH a mailing list.
  - `TestAdminPublicFolderPatchAllowedFields` — all 3 editable
    keys applied + re-fetch.
  - `TestAdminPublicFolderPatchUnknownFieldHardReject` — unknown
    key aborts the PATCH.
  - `TestAdminPublicFolderPatchReadOnlyValidator` — non-bool
    `read_only` rejected with 400.

### Smoke hardening

- `smoke-admin-ui.sh` BLOCKER 3 + BLOCKER 4 checks extended to
  accept either the legacy literal-source pattern OR the
  form-builder's `value: '<action>'` array pattern, so the BLOCKER
  contract is preserved across refactors.
- `smoke-admin-functional-browser.mjs` now navigates and opens
  every previously-empty modal (admin-groups, ACL, acceptance,
  incoming-rules, mailing-lists, public-folders) and asserts each
  modal exposes a non-trivial field count (>= 4 inputs minimum,
  higher for rule builders). Runtime listeners page is asserted
  to render the new `Listener overview` KPI strip and the
  runtime-truthful state labels (active / skipped / not monitored /
  failed).
- Existing banned-string, RBAC, CSRF, MFA, and banned-placeholder
  gates remain intact.
