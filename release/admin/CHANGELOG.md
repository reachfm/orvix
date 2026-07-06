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
  honest single-node / no-jobs states â€” acceptable stubs, not broken features.

---

## Enterprise 2

Branch: `feature/admin-enterprise-2`
Base: `a40ac9a` ("WEBMAIL-LIVE-STABILIZATION-3A: fix duplicate Move to... in reading pane toolbar")

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
