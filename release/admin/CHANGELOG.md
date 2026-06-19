# Orvix Admin Console — Enterprise 2 — Changelog

Branch: `feature/admin-enterprise-2`
Base: `a40ac9a` ("WEBMAIL-LIVE-STABILIZATION-3A: fix duplicate Move to... in reading pane toolbar")

A focused enterprise upgrade of the Orvix Admin Console. No backend
changes — every change is a frontend upgrade on top of the existing
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
  shadow, motion, radius and spacing — every component references
  tokens, no raw hex.
- **Focus rings only on `:focus-visible`** (keyboard, never mouse).
- **`prefers-reduced-motion: reduce`** collapses every transition /
  animation to ~0ms.
- **Logical properties** (`margin-inline-*`, `padding-inline-*`,
  `border-inline-*`, `inset-inline-*`) for RTL correctness in the
  drawer / topbar / table.

### 2. Sidebar / topbar

- Sidebar becomes the canonical nav (the previous HTML had a flat
  `<nav>` list — the new client renders it from a single source of
  truth so the keyboard-shortcut overlay and the sidebar never drift).
- Topbar carries the admin email, role badge, avatar, a per-section
  Refresh button, and Sign Out.
- Sticky behaviour so the sidebar and the topbar stay visible on
  long tables.

### 3. Dashboard

- Service-status grid (API, SMTP, IMAP, POP3, JMAP, Database, Redis /
  Queue, License) sourced from `/api/v1/health` + admin summary; "Not
  available" is shown if a metric is missing — never fabricated.
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
- DKIM shows `v=DKIM1; k=rsa; p=YOUR-PUBLIC-KEY` placeholder with a
  warning that public-key generation happens at install time — no
  fake keygen UI.
- PTR / rDNS warning that it is provider-controlled.
- Manual `dig MX / dig TXT` verification block.
- DMARC record is generated with `p=quarantine` and a `rua=` report
  address.
- No fake green check — there is no live DNS resolver in this build,
  the panel says so explicitly.

### 8. Backups

- Status grid (last backup, schedule, health, count).
- Actions: Backup now (with confirmation), Run retention, Download
  (with confirmation), Delete (with typed-confirm).
- Honest "Restore is not exposed in this build" note instead of a
  fake restore button.

### 9. Updates

- Version / SHA / channel / status / latest cards.
- Preflight panel — every check rendered as a status badge + title +
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
- "Out of scope in this build" list — honest about what is not
  implemented (CSV import, in-place restore, in-UI DKIM keygen, direct
  config file edit).

### 13. UX primitives

- **Drawer** — right slide-in for domain / mailbox / queue detail.
- **Modal** — centered for every form / confirmation. Three sizes.
- **Toast** — top-right stack, capped at 4, FIFO.
- **Confirm-danger** — Promise-based; supports an optional
  typed-confirmation gate.
- **Copy-to-clipboard** — async Clipboard API + textarea fallback.
- **Empty / error / skeleton** states shared across every section.
- **Keyboard shortcuts** — `g <letter>` jumps between sections,
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
- No external CDN / fonts / imports — every asset is local.
- No inline event handlers in the HTML (every event is wired from JS).

## Out of scope

- **CSV bulk import** — backend has no endpoint yet.
- **In-place backup restore** — backend has no restore endpoint; UI
  surfaces this honestly.
- **Inline DKIM key generation** — backend has no keygen endpoint;
  install-time only.
- **Live DNS resolver** — backend does not run one in this build.
- **Real-time push / WebSocket** — not in scope for ADMIN-ENTERPRISE-2.

## Files changed

- `release/admin/index.html` — replaced with the new shell (login,
  app frame, toast stack, semantic IDs).
- `release/admin/styles.css` — replaced with the enterprise design
  system (two themes, premium components, responsive, reduced-motion).
- `release/admin/app.js` — replaced with the modular enterprise
  client (helpers, drawers, modals, toasts, ten page renderers).
- `release/admin/CHANGELOG.md` — this file, new.
- `internal/api/handlers/admin_frontend_test.go` — new static-analysis
  tests mirroring the webmail test pattern (security guards, asset
  presence, no-queue-leak, no-unsafe-innerhtml patterns, responsive
  breakpoints, reduced motion, aria-label sweep).
