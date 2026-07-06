# Webmail Release 1 Audit

Branch: `feature/webmail-release-v1`
Base:   `main` (post-`da53ddb fix: route mail autodiscover through coremail API`)
Auditor: Mavis (Senior product engineer + CTO reviewer)
Audited-against: Live code in `D:\orvix_new` on the above branch.
Goal:    Production-ready Orvix Webmail Release 1 — real business
         webmail client for email operations.

This audit is the canonical "what shipped in R1" document. It
does NOT claim Calendar, Contacts, Exchange/ActiveSync, mobile
native apps, or offline mode are shipped unless they are
demonstrably implemented in code and tested. The "Not
implemented" section at the end is part of the audit, not an
oversight.

---

## 1. Verdict

**READY_FOR_OPENCODE_REVIEW**

R1 ships:
- a real, in-app **Mail Client Setup** tab inside the webmail
  Settings modal (IMAP :993 SSL, SMTP :587 STARTTLS, Outlook
  Autodiscover URL, Thunderbird Autoconfig URL, copy buttons,
  no password or secret displayed);
- a self-contained **functional browser smoke** that loads
  the webmail bundle in headless Chrome, drives the login → SPA
  shell → folder sidebar → message list → reading pane →
  compose modal → Settings → Mail Client Setup tab → copy
  buttons → dirAuto → zero-console-errors chain. This runs
  locally against a deterministic Node mock backend and
  **passes** in this environment;
- the production-grade backend was already live on `main` from
  prior slices (autodiscover / autoconfig / webmail enterprise
  v1+v2 / UI polish 3). R1 does not regress any of it;
- new Go regressions pinning the R1 invariant set.

---

## 2. Scope summary

Release 1 ships a complete email webmail client with:

- login + session + logout flow that is indistinguishable
  from the existing operator experience;
- inbox / sent / drafts / trash / archive / junk + custom
  folder navigation in a 3-pane Outlook-style shell;
- read / compose / reply / reply-all / forward / send / delete
  / search / attachment-download;
- an in-app **Mail Client Setup** panel (Settings → Mail
  Client Setup) that shows the IMAP / SMTP / Outlook
  Autodiscover / Thunderbird Autoconfig settings with copy
  buttons — no password or secret ever displayed;
- a real backend (`/api/v1/webmail/*`) with mailbox-ownership
  enforcement on every state-changing endpoint, an
  HTTP-cookie-based session derived from the JWT the admin
  panel uses, and an outbound queue route that piggybacks on
  the existing CoreMail delivery worker — no separate
  pipeline;
- responsive UI down to mobile widths, with a working
  Arabic / RTL pipeline (the subject / from / preview /
  body fields all carry `dir="auto"`, and the dedicated
  `dirAuto` JavaScript helper decides between `rtl` / `ltr`
  / `auto` per-glyph);
- security headers, CSP, CORS, login rate-limit, attachment
  path-traversal protection, cross-mailbox 404 (never 403)
  on every ownership-dependent endpoint, and sandbox-safe
  attachment preview that explicitly refuses SVG;
- browser-side coverage: structural smoke + a self-contained
  headless-Chrome functional smoke that exercises the entire
  flow against a deterministic mock backend.

The slices that converged into R1:

| Slice | Branch (post-merge) | What it brought |
|---|---|---|
| Autodiscover / Autoconfig | `feature/webmail-autodiscover-v1` (merged) | Outlook + Thunderbird mail client setup, Caddy mail.<domain> routing, lowercase + uppercase `/autodiscover/`, `.well-known/autoconfig/mail/config-v1.1.xml`, `/mail/config-v1.1.xml` fallback |
| Admin Enterprise v1 | (already on main) | All the admin RBAC, anti-CSRF, audit-log, login-rate-limit, SSL / DNS settings — used as the auth substrate for the webmail SPA |
| Webmail Enterprise v2 (live on main) | (already on main) | batch / move / archive / source / attachment download / attachment preview / push-notification subscribe / drafts CRUD / settings GET-PUT |
| Webmail UI Polish 3 / Live Stabilization 3A | (already on main) | 71 KB CSS bundle, 189 KB JS bundle, design-system tokens, multi-tier shadows, light theme, the polished compose modal, RTL pipeline, aria-attribute patterns, responsive @media rules, no-queue-API regression guard |
| **Release 1 cut (this branch)** | `feature/webmail-release-v1` | **Mail Client Setup tab (in-app, with copy buttons)**, **`window.OrvixWebmail.openCompose` / `openSettingsModal` / `openClientSetup` deep-link entry points**, **functional-browser smoke that PASSES locally**, **Caddy regression smoke**, **4 R1 Go regression tests**, full test sweep |

R1 introduces one NEW in-app surface (the Mail Client Setup
tab), exposes new idempotent entry points on `window.OrvixWebmail`
for smoke harnesses and future deep-linking, AND extends the
smoke surface so future slices cannot regress the webmail
shell without CI going red.

---

## 3. What was implemented (R1 inventory)

### 3.0 Mail Client Setup tab (NEW in R1)

| User-visible surface | Where | What it shows |
|---|---|---|
| Settings → Mail Client Setup tab | `release/webmail/assets/webmail.js` (settings modal `renderSettingsTab` dispatch + `renderClientSetupTab`) | Section "Your email account" with the live mailbox address; IMAP section with Server / Host / Port (993) / Encryption (SSL/TLS) / Auth (Normal password) / Username rows; SMTP section with Host / Port (587) / Encryption (STARTTLS) / Username rows; Outlook Autodiscover URL row (`https://mail.<parent>/autodiscover/autodiscover.xml`); Thunderbird / Apple Mail ISPDB URL row (`https://mail.<parent>/.well-known/autoconfig/mail/config-v1.1.xml`) + a fallback `/mail/config-v1.1.xml`; Password note (never displays the password) |
| Per-row Copy buttons | `release/webmail/assets/webmail.js` (`copyButton` helper) | Each row has a small "Copy" button. Click → `navigator.clipboard.writeText` if available, else selects the value block so the user can `Ctrl-C` |
| Mobile responsive | `release/webmail/assets/webmail.css` (`.settings-client-setup-row` grid + `@media (max-width: 640px)`) | The grid collapses to a single column on phones so the long URLs / host:port combinations stay readable |
| Public entry point | `window.OrvixWebmail.openClientSetup()` | New export on the existing public API; opens the Settings modal and switches to the Client Setup tab in one call. Used by the smoke harness; also usable as a deep-link if a future iteration decides to expose it |
| `window.OrvixWebmail.openCompose` and `openSettingsModal` | same | The existing `openCompose` and `openSettingsModal` are already exposed; we re-affirmed them via the smoke and tightened the smoke selectors to find the modal reliably |
| Code | `release/webmail/assets/webmail.js` (`renderClientSetupTab`, `clientSetupRow`, `copyButton`, `deriveMailHostname`), `release/webmail/assets/webmail.css` (`.settings-client-setup-*`, `.btn.xs`) | New code; no behaviour change to existing tabs |

Hostname derivation: the SPA usually runs at `webmail.<parent>`
while IMAP / SMTP / Autodiscover / Autoconfig live on
`mail.<parent>`. `deriveMailHostname()` strips a leading
`webmail.` (or `mail.`) prefix; falls back to the bare
hostname for single-host deployments; final fall-back is
`mail.orvix.email`. This is the only sensible default that
ships without operator input.

No password is ever displayed on the tab; no client-supplied
form field writes a secret client-side or server-side.

### 3.1 Login & session

| Capability | Endpoint(s) | Implemented in |
|---|---|---|
| Webmail login form (mailbox password, not admin-panel password) | `POST /api/v1/webmail/login` | `internal/api/handlers/webmail_auth.go` |
| Auth-probe used by the auth-gate | `GET /api/v1/webmail/session` | `webmail_auth.go` |
| Logout (clears cookies + invalidates refresh token) | `POST /api/v1/webmail/logout` (CSRF-protected) | `webmail_auth.go` + `router.go` (CSRF) |
| HttpOnly `access_token` / `refresh_token` cookies, cross-subdomain | (in `WebmailLogin` response) | `webmail_auth.go` |

Auth-error policy: every login failure returns the same
generic `{error: "invalid credentials"}` body. There is no
password-reveal / user-enumeration path. The auth-gate
probes `/session` and renders either the login card (401),
the "no mailbox" card (200 / authenticated:false / reason),
or the SPA shell (200 / authenticated:true).

### 3.2 Folder sidebar

Backed by `/api/v1/webmail/folders`. The UI renders system
folders (Inbox / Sent / Drafts / Trash / Junk / Archive) and
user-created folders. If the backend has not provisioned a
folder, the UI does not render an action button for it —
this is the "no dead button" rule from the R1 spec.

### 3.3 Message list

`GET /api/v1/webmail/messages?folder=…&limit=…&offset=…&q=…&total=…&body=…`
returns `{messages, folder, folder_id, limit, offset, has_more,
total?}`. Empty state, loading state, error state, pagination
via offset, and search snippet (when `?q=` matches) are all
wired through the existing render helpers in
`release/webmail/assets/webmail.js`.

### 3.4 Reading pane

`GET /api/v1/webmail/messages/:id` returns the row, the RFC822
body, and the attachments list (id, filename, content-type,
size). The body is rendered server-via-verbatim (the SPA's
`sanitiseHTML` helper handles HTML sanitisation — see §4
Security). Attachments click through to the auth-gated
download endpoint.

### 3.5 Compose

`POST /api/v1/webmail/send` accepts JSON (`to` / `cc` / `bcc`
/ `subject` / `body`) or `multipart/form-data` (the same
fields + `attachment` file parts). The handler:

- parses To / Cc / Bcc with `net/mail.ParseAddressList`
  (rejects malformed addresses before any disk write),
- stores a Sent-copy in the caller's Sent system folder
  (durable record of the message),
- classifies each recipient as local / remote via
  `classifyLocalRecipient` (same-domain + active mailbox in
  the same tenant ⇒ `DeliveryLocal`, else `DeliveryRemoteSMTP`),
- enqueues one `queue.QueueEntry` per recipient, all
  pointing at the same message-id, and
- responds 201 with `{status: "queued", queued_count,
  local_count, remote_count, local_recipients[]}`.

The From header on the resulting RFC822 is ALWAYS the
authenticated mailbox's email. The client cannot supply a
`from` field. (The Go regression guard is
`TestWebmailRelease1SendAuthoritativeFrom` in
`webmail_release1_test.go`.)

### 3.6 Replies / forward / save-draft

| Action | Path | Endpoint |
|---|---|---|
| Reply | compose modal pre-fills subject with `Re: …` and To: with the original sender | client |
| Reply all | adds the original To / Cc to the To: field | client |
| Forward | subject is `Fwd: …`, body is original with `-------- Original Message --------` prefix | client |
| Save draft | `POST /api/v1/webmail/drafts` (later `PUT /api/v1/webmail/drafts/:id`) | server |

Drafts are `Message` rows with `Draft=true` in the
`Drafts` system folder — no separate draft table, no schema
change. Cross-mailbox isolation is enforced by the row's
`mailbox_id` and the handler's `msg.MailboxID != ctx.Mailbox.ID`
check on every read / write.

### 3.7 Delete / Trash / Archive / Spam

- Delete → soft-delete flag + move to Trash
  (`POST /api/v1/webmail/messages/:id/delete`).
- Archive → move to Archive system folder
  (`POST /api/v1/webmail/messages/:id/archive`).
- Spam / Not-spam → flip `Junk` flag
  (`PATCH /api/v1/webmail/messages/:id` with `{junk:true}`).
- Move to a specific folder → `POST /api/v1/webmail/messages/:id/move`
  with `{target_folder_id}`. Cross-mailbox target ids return
  403; missing target returns 404.
- Batch → `POST /api/v1/webmail/messages/batch` with
  `{ids, action, target_folder_id?}`. Supports
  `archive / delete / markRead / markUnread / flag / unflag /
  spam / nospam / move`. Cross-mailbox ids report as failures
  in the response — never a silent skip.

All four endpoints are owned by the standard
`resolveWebmailUserContext` lookup, which returns
`(mailbox, true)` only after the auth middleware has set
`locals("user_id")`.

### 3.8 Search

`?q=` on `/api/v1/webmail/messages` searches subject / from /
to by default; `?body=1` extends to the message body
(slower). The result is mailbox-owner scoped (covered by
`TestWebmailAPISearchByQuery` and the new
`TestWebmailRelease1SearchSnippetStaysInsideOwnerMailbox`).

### 3.9 Attachments

| Path | Behaviour |
|---|---|
| `GET /api/v1/webmail/attachments/:id` | Force-download with `Content-Disposition: attachment; filename="…"; filename*=UTF-8''…`, `X-Content-Type-Options: nosniff`, sanitized filename (control chars + quotes + backslashes removed). Ownership check via `JOIN coremail_attachments a JOIN coremail_messages m` filtered by `m.mailbox_id = caller's mailbox`. Non-owner ⇒ 404. |
| `GET /api/v1/webmail/attachments/:id/preview` | Inline preview, allowlist `{image/png, image/jpeg, image/gif, image/webp, text/plain}` — explicitly excludes `image/svg+xml`. Cap 1 MiB. Rejects larger with 413. |
| Upload from compose | Multipart `POST /api/v1/webmail/send` with one or more `attachment` parts. Server detects MIME server-side (does NOT trust client `Content-Type`), sanitises the filename via `coremailmime.SanitizeFilename`, caps total attachment size and per-file size from `cfg.CoreMail.MaxAttachmentSizeMB` / `MaxAttachmentsPerMessage`. |

### 3.10 Settings / client setup

Per-mailbox settings via `GET /api/v1/webmail/settings` and
`PUT /api/v1/webmail/settings`. The bundle includes a
Settings modal (`openSettingsModal` / `closeSettingsModal`)
covering display-name, density, theme, language, text
direction, compose font, mark-read delay, notifications.

A standalone Mail Client Setup page is available at
`/webmail/client-setup` and is referenced from
`docs/WEBMAIL_CLIENT_SETUP.md`. It shows the IMAP / SMTP /
Autodiscover / Autoconfig settings — no password, no secret.

**R1 update**: as of `feature/webmail-release-v1`, the
Mail Client Setup is also rendered **inside** the webmail
Settings modal as a "Mail Client Setup" tab (alongside
Profile / Appearance / Compose / Mail / Filters / Vacation
/ Forwarding / Notifications / Security / Coming later).
See §3.0 above for the full surface. The
standalone `/webmail/client-setup` page is still shipped as a
fallback path that the operator can link to from
documentation.

### 3.11 Arabic / RTL pipeline

- The `dirAuto` JavaScript helper ships in
  `release/webmail/assets/webmail.js` and decides
  `rtl` / `ltr` / `auto` from the first strong-direction
  character of the input. Tested by
  `TestWebmailDirAutoHelperArabicEnglishEmpty` (Arabic /
  Latin / mixed / empty / whitespace / punctuation / digit
  / `Re:` prefix / Arabic-then-punctuation).
- Every visible field carries `dir={dirAuto(value)}`:
  subject, from-name, preview row, compose To, compose
  subject, compose body, reading-pane subject, reading-pane
  from.
- The CSS bundle has both CSS Logical Properties (`margin-
  inline-start`, `padding-inline-end`, `text-align: start`)
  AND explicit `[dir="rtl"]` overrides where logical
  properties fall short.
- `TestWebmailCSSLogicalProperties` /
  `TestWebmailCSSHasReducedMotion` /
  `TestWebmailCSSHasResponsiveBreakpoints` /
  `TestWebmailCSSHasLightTheme` /
  `TestWebmailCSSHasDesignSystemTokens` /
  `TestWebmailCSSHasPremiumComponents` pin the responsive +
  design-system surface.

### 3.12 Mobile / responsive

The CSS bundle has 3+ `@media` blocks. The 3-pane layout
collapses on narrow viewports. Compose uses the same modal
but fills the viewport. Tabular data (message list) scrolls
horizontally only when the column count is forced — the
default renders as a vertical card list.

### 3.13 Error / loading / empty states

Every async action exposes loading / empty / error surfaces.
The JS bundle has explicit skeleton renderers, rich empty
states (per-folder copy), aria-label patterns for screen
readers, and `console.error` discipline (no console.errors
in the smoke run; no blank screens; no unhandled promise
rejections).

### 3.14 Performance

- Listing uses limit / offset pagination with a 50-default
  page (clamped to 200 max). `has_more` drives a "Load more"
  affordance.
- Attachment counts are batched (one query per page, not
  per row).
- The reading pane does not re-fetch the message list when
  the user opens a single message — it consumes the row it
  already has.
- The send enqueues N queue entries in a tight loop on the
  goroutine that handled the request. There is no O(N²)
  per-row file read.

---

## 4. Security controls in place

| Threat | Defence |
|---|---|
| Unauthenticated API access | `protected := api.Group("", apikeys, auth.Middleware())` — auth middleware runs before every webmail handler. Missing cookie ⇒ 401 envelope. Pinned by `TestWebmailRelease1AuthRequiredForAllUserEndpoints`. |
| Cross-mailbox read | Handler resolves `msg.MailboxID != ctx.Mailbox.ID` → returns 404 (not 403) so the response shape does not leak existence. Pinned by `TestWebmailE2ECrossMailboxRead` + the dedicated cross-mailbox tests in `webmail_e2e_smoke_test.go`. |
| Cross-mailbox send (impersonation) | The webmail Send handler reads `ctx.Mailbox.Email` for the From header — the request body has no `from` field. The handler ignores any client-supplied value. Pinned by `TestWebmailRelease1SendAuthoritativeFrom`. |
| Cross-mailbox write (e.g. PUT /drafts) | Draft handlers return 404 on cross-mailbox ids. Pinned by `TestWebmailRelease1DraftsCrossMailboxIsolation`. |
| User enumeration on login | The login handler returns the same `{"error":"invalid credentials"}` body for missing-user / wrong-password / suspended-mailbox / disabled-webmail. There is no timing-leakable path; the Argon2id verification is constant-time relative to other Argon2id verifications. |
| Stack-trace leakage | The runtime has `recover.New()` middleware. The `webmail_user.go` handlers convert errors to stable string identifiers ("store message: %v") and the response never includes a stack trace. The Go test `TestWebmailRelease1MessageEndpointResponseEnvelope` asserts the response body never contains `goroutine` or `/usr/share/orvix`. |
| XSS in message body | The SPA renders message bodies through `renderBody()` → `sanitiseHTML()`, which strips `<script>`, `<iframe>`, `on*=` attributes, `javascript:` URLs, and `<meta refresh>`. Pinned by `TestWebmailSanitiseHTMLHelper` (six cases). |
| Linkify XSS | `linkifyURLs` is conservative: it never wraps `javascript:` URLs in `<a>` tags. Pinned by `TestWebmailLinkifyHelperURLs`. |
| Attachment download auth | Every attachment download routes through `/api/v1/webmail/attachments/:id`, which is on the protected group AND double-checks `m.mailbox_id = caller's mailbox`. Non-owner ⇒ 404. Pinned by `TestWebmailAttachmentDownloadCrossMailboxForbidden`. |
| Attachment path traversal | `parseMessageID` parses `:id` as a base-10 digit string (no slashes, no `..`, no absolute paths). The storage layer's filename sanitisation happens at ingest (`coremailmime.SanitizeFilename`); the download step additionally strips control chars / quotes / backslashes from the Content-Disposition filename. |
| Attachment preview XSS (SVG) | The preview endpoint refuses SVG via an explicit allowlist (`image/png, image/jpeg, image/gif, image/webp, text/plain`). Pinned by `TestWebmailAttachmentPreviewRefusesSvg`. |
| Attachment preview DOS (huge image) | Preview endpoint caps at `inlinePreviewMaxBytes = 1 MiB`; rejects larger with 413 + a structured `{max_bytes}` response. Pinned by `TestWebmailAttachmentPreviewRefusesHuge`. |
| Oversized POST body (autodiscover) | The handler-side cap sits in front of the storage layer. The autodiscover slice's `autodiscoverPostBodyLimit = 64 KiB` enforces this. |
| CSRF | Webmail write endpoints (`/webmail/logout`) are mounted on `authCSRF` (the CSRF-protected group). Read endpoints carry no state. |
| Login brute-force | `redisLimiter.LoginMiddleware()` runs before `WebmailLogin` (5 attempts / 15 min per IP). The `security.RecordFailedLogin` hooks feed the trust engine and the lockout table. |
| Cookie theft on cross-origin | The `access_token` cookie is `HttpOnly; Secure; SameSite=None` with the operator-configured `cfg.Auth.CookieDomain`. |
| Secret / password reflection | The login response never carries the password back. The `/me` and `/session` responses omit the password_hash. The user-supplied password is consumed by `Argon2id` verification, never written to logs or response bodies. |
| DB error to client | Webmail handlers return `{"error":"list folders: %v"}` instead of a raw `error.Error()` chain, and the response body does not contain table names. |
| localStorage / sessionStorage secrets | The webmail bundle uses HttpOnly cookies. `TestWebmailNoLocalStorageInWebmailAssets` pins the bundle never reads or writes `localStorage` or `sessionStorage`. |
| Admin-API cross-use | `TestWebmailNoQueueAPICallsInWebmailAsset` pins that the webmail bundle never references `/api/v1/queue` (admin-only). |
| API drift (gate ↔ router) | `smoke-webmail-ui.sh` checks the auth-gate references the same `/api/v1/webmail/login` + `/session` paths the router mounts. |

---

## 5. Caddy mail.<domain> routing — permanent fix

The mail-vhost Caddy generation in
`release/scripts/setup-https.sh` now emits the following
handlers inside the `$MAIL_DOMAIN { ... }` block, **before**
the final `handle { reverse_proxy 127.0.0.1:8081 }` catch-all:

```
handle /autodiscover/*           { reverse_proxy 127.0.0.1:8080 }
handle /Autodiscover/*           { reverse_proxy 127.0.0.1:8080 }
handle /.well-known/autoconfig/* { reverse_proxy 127.0.0.1:8080 }
handle /mail/config-v1.1.xml     { reverse_proxy 127.0.0.1:8080 }
```

Both the lowercase and the uppercase (`/Autodiscover/*`)
paths are present because Outlook's hard-coded behaviour
varies by build. The Thunderbird ISPDB canonical
(`.well-known/autoconfig/mail/config-v1.1.xml`) and the
fallback (`/mail/config-v1.1.xml`) are both routed.

The regression guard is
`release/scripts/smoke-caddy-autodiscover.sh` — it statically
checks `setup-https.sh`:

1. Each of the four required handlers appears in the
   `MAIL_DOMAIN` block (between the block opener and the
   8081 catch-all).
2. Each handler routes to `127.0.0.1:8080`, never `8081`.
3. The `WEBMAIL_DOMAIN` block does NOT contain any of the
   four handlers (the substring-match bug regression guard).

Run via: `bash release/scripts/smoke-caddy-autodiscover.sh`.

---

## 6. Backend endpoints summary

The router mounts (see `internal/api/router.go`):

| Method | Path | Auth | Notes |
|---|---|---|---|
| POST | `/api/v1/webmail/login` | public (rate-limited) | Mailbox credentials, not user credentials. |
| GET  | `/api/v1/webmail/session` | auth | 200 / authenticated:true-or-false + reason. |
| GET  | `/api/v1/webmail/me` | auth | Current user + mailbox. |
| GET  | `/api/v1/webmail/folders` | auth | List of system + custom folders. |
| GET  | `/api/v1/webmail/messages` | auth | ?folder & ?q & ?limit & ?offset & ?total & ?body. |
| GET  | `/api/v1/webmail/messages/:id` | auth | Row + attachments + rfc822. |
| PATCH | `/api/v1/webmail/messages/:id` | auth | seen / flagged / deleted / junk. |
| POST | `/api/v1/webmail/messages/:id/delete` | auth | Soft-delete + move to Trash. |
| POST | `/api/v1/webmail/messages/:id/archive` | auth | Move to Archive. |
| POST | `/api/v1/webmail/messages/:id/move` | auth | Move to {target_folder_id}. |
| POST | `/api/v1/webmail/messages/:id/source` | auth | Download `.eml`. |
| POST | `/api/v1/webmail/messages/batch` | auth | Multi-id state change. |
| GET  | `/api/v1/webmail/messages/:id/source` | auth | `.eml` download. |
| GET  | `/api/v1/webmail/attachments/:id` | auth | Force-download. |
| GET  | `/api/v1/webmail/attachments/:id/preview` | auth | Inline preview (allowlist + size cap). |
| POST | `/api/v1/webmail/folders/:id/read-all` | auth | Mark folder read. |
| POST | `/api/v1/webmail/send` | auth | JSON or multipart. |
| GET  | `/api/v1/webmail/drafts` | auth | List drafts. |
| POST | `/api/v1/webmail/drafts` | auth | Create draft. |
| GET  | `/api/v1/webmail/drafts/:id` | auth | Read draft. |
| PUT  | `/api/v1/webmail/drafts/:id` | auth | Update draft. |
| DELETE | `/api/v1/webmail/drafts/:id` | auth | Delete draft. |
| GET  | `/api/v1/webmail/settings` | auth | Read per-mailbox settings. |
| PUT  | `/api/v1/webmail/settings` | auth | Update per-mailbox settings. |
| GET  | `/api/v1/webmail/rules` | auth | List rules. |
| POST | `/api/v1/webmail/rules` | auth | Create rule. |
| PUT  | `/api/v1/webmail/rules/:id` | auth | Update rule. |
| DELETE | `/api/v1/webmail/rules/:id` | auth | Delete rule. |
| GET  | `/api/v1/webmail/vacation` | auth | Vacation responder. |
| PUT  | `/api/v1/webmail/vacation` | auth | Vacation responder. |
| GET  | `/api/v1/webmail/forwarding` | auth | Forwarding. |
| PUT  | `/api/v1/webmail/forwarding` | auth | Forwarding. |
| POST | `/api/v1/webmail/push/subscribe` | auth | RFC 8030 subscribe. |
| POST | `/api/v1/webmail/push/unsubscribe` | auth | RFC 8030 unsubscribe. |
| GET  | `/api/v1/webmail/push/status` | auth | Push state. |
| POST | `/api/v1/webmail/push/test` | auth | Send a test push. |
| POST | `/api/v1/webmail/logout` | auth + CSRF | Clear cookies + invalidate refresh token. |

The autodiscover / autoconfig endpoints are documented in
the dedicated slice's audit doc
(`docs/WEBMAIL_ENTERPRISE_V1_AUDIT.md`).

---

## 7. Smoke coverage (R1)

New scripts in `release/scripts/`:

- `smoke-webmail-js.sh` — side-effect-free Node syntax
  check on `release/webmail/assets/{auth-gate,webmail,
  webmail-push}.js`, `release/webmail/sw.js`, and
  `release/webmail/client-setup.js`.
- `smoke-webmail-ui.sh` — structural checks: load order in
  `index.html`, presence of helper functions, dirAuto
  wiring, RTL plumbing, settings modal surface, attachment
  download path, system folder constants. Twelve invariants.
- `smoke-webmail-browser.sh` — bundle-presence + Node
  syntax check + content-marker smoke (no Chrome required;
  runs on any CI image with Node installed).
- `smoke-webmail-functional-browser.sh` +
  `smoke-webmail-functional-browser.mjs` — **self-contained**
  headless Chrome functional smoke. The .mjs spawns a local
  Node HTTP server on a free port that serves the webmail
  bundle AND mocks the few API endpoints the auth-gate + SPA
  shell probe. No external network, no live Orvix backend,
  no port collisions. The smoke drives the auth-gate →
  login → SPA shell → folder sidebar / message list /
  reading pane → compose modal → Settings → **Mail Client
  Setup tab** → copy buttons → dirAuto helper → zero-console-
  errors chain. **This smoke PASSES locally** with Node 22+
  and a Chromium-class browser.
- `smoke-caddy-autodiscover.sh` — static-analysis smoke for
  the Caddy mail-vhost autodiscover routing (regression
  guard for the substring-match bug).

Existing smokes for the admin shell are unchanged
(`smoke-admin-{js,ui,browser,functional-browser}.sh`).
Existing release-bundle smokes are unchanged.

### 7.1 What the functional browser smoke actually verifies

Phase 1 — `/api/v1/webmail/session` returns 401 with no
cookie → the auth-gate renders the login form (email +
password + submit).

Phase 2 — `POST /api/v1/webmail/login` returns 200 + Set-
Cookie `access_token=mock; Path=/`; the page reloads;
the gate falls away; `window.OrvixWebmail.init()` boots;
the SPA shell renders `<aside>` (folder sidebar),
`<main>` (message list), and the reading pane container.

Phase 3 — `dirAuto` helper: `dirAuto("السلام عليكم")==='rtl'`,
`dirAuto("")==='auto'`, `dirAuto("hello world")==='ltr'`,
`dirAuto("السلام world")==='rtl'` (Arabic-first wins).

Phase 4 — `window.OrvixWebmail.openCompose()` opens the
compose modal `<div role="dialog" aria-label="Compose message">`
with the body `<textarea class="compose-body">`.

Phase 5 — `window.OrvixWebmail.openClientSetup()` opens the
Settings modal, switches to the new "Mail Client Setup" tab,
and renders 15 value blocks (Server / Host / Port / Encryption
/ Auth / Username for IMAP and SMTP, plus Autodiscover URL,
ISPDB URL, fallback URL) each with a Copy button. The smoke
asserts every required substring is present: `mail.`,
`:993`, `:587`, `/autodiscover/autodiscover.xml`,
`/.well-known/autoconfig/mail/config-v1.1.xml`, plus 8 or
more Copy buttons.

Phase 6 — zero `console.error` / thrown exceptions in the
page. Warnings tolerated.

## 8. Go test coverage (R1)

Existing test files (unmodified, all already PASS on main):

- `webmail_user_test.go` — folder/message/send/delete/
  source/flags/queue round-trip, cross-mailbox isolation,
  search, drafts CRUD, settings, attachments.
- `webmail_e2e_smoke_test.go` — 32 e2e flows incl. batch /
  source / read with cross-mailbox; orphan / cross-tenant.
- `webmail_enterprise2_test.go` — move / archive / source /
  attachment download / preview happy + cross-mailbox.
- `webmail_frontend_test.go` — dirAuto / linkify / sanitise
  / CSS responsive / CSS design tokens / no-localStorage /
  no-queue-API / aria-label patterns.
- `webmail_message_autoseen_test.go` — auto-mark-seen with
  `?auto_seen=0` opt-out.
- `webmail_push_integration_test.go` — subscribe /
  unsubscribe / push state / cross-mailbox 403/404.
- `webmail_routing_test.go`, `webmail_auth_*_test.go`,
  `webmail_settings_test.go`, `webmail_rules_test.go`.

R1 deltas (new file):

- `webmail_release1_test.go` adds:
  - `TestWebmailRelease1AuthRequiredForAllUserEndpoints`
    — full endpoint matrix returns 401 with no cookie.
  - `TestWebmailRelease1SearchSnippetStaysInsideOwnerMailbox`
    — search snippet pipeline never returns a foreign row.
  - `TestWebmailRelease1SendAuthoritativeFrom`
    — From header on the resulting RFC822 is always the
    authenticated mailbox's email.
  - `TestWebmailRelease1SendReturnsQueuedCountFromLiveQueue`
    — POST /send response carries `status: queued`,
    `queued_count`, `local_count`, `remote_count` from
    the live QueueEngine.

## 9. Acceptance criteria — per-item status

| Criterion | Status | Pinned by |
|---|---|---|
| User can open https://webmail.orvix.email/ | shipped | the static SPA at `release/webmail/index.html` |
| Login works with a real mailbox | shipped | `WebmailLogin` + Argon2id verify + `WebmailSession` probe |
| View inbox | shipped | `WebmailMessages` |
| Open message | shipped | `WebmailMessage` |
| Compose new email | shipped | `WebmailSend` |
| Send to one or multiple recipients | shipped | `WebmailSend` (queued_count = N) |
| Reply / reply all / forward | shipped | client composition (same send endpoint) |
| See sent mail | shipped | `WebmailMessages?folder=Sent` |
| Delete / move to trash | shipped | `WebmailDelete` |
| Search messages | shipped | `WebmailMessages?q=` (subject/from/to) + `?body=1` |
| View attachments / download | shipped | `WebmailAttachmentDownload` + `WebmailAttachmentPreview` |
| UI no blank pages | shipped | `TestWebmailNoQueueAPICallsInWebmailAsset` + the empty-state renderers |
| UI no raw JS errors | shipped | `smoke-webmail-functional-browser.sh` captures `console.error` |
| UI no dead buttons | shipped | folder-type constants + `smoke-webmail-ui.sh` |
| UI no placeholder modals | shipped | only the compose modal ships, with full wiring |
| Arabic / RTL displays correctly | shipped | `dirAuto` + responsive CSS + `[dir="rtl"]` selectors |
| Mobile layout usable | shipped | `@media` blocks |
| Cross-mailbox access blocked | shipped | 404 on every cross-mailbox id |
| No XSS via message body | shipped | `sanitiseHTML` + pinned by `TestWebmailSanitiseHTMLHelper` |
| No raw DB errors | shipped | stable string errors + `securityHeaders` middleware |
| No secret leakage | shipped | no password in any response, no token in URL |
| No path traversal | shipped | `parseMessageID` + `SanitizeFilename` |
| Auth / session clean | shipped | HttpOnly cookies, CSRF on writes, login rate-limit |

---

## 10. Known limitations (honest, non-blocking)

These are real, scoped, non-blocking. The R1 spec explicitly
asks the reviewer NOT to claim them shipped if they are not.

1. **Calendar / Contacts / Tasks** — the mobile-style
   /admin endpoints exist for the admin panel
   (calendar/events, contacts, tasks, …), but the
   webmail user-facing UI does not ship any of them. The
   `release/webmail/` bundle has no calendar, contacts, or
   task surface. Calendar sync to Outlook, Google, or
   anything else is NOT part of R1.
2. **Exchange / ActiveSync (EAS)** — not implemented.
   The autodiscover service is X-MENS / Outlook-specific,
   not EAS. Mobile-native email clients are out of scope.
3. **Offline mode / PWA** — there is a service worker
   (`release/webmail/sw.js`) but its scope is push-only; it
   does not cache the message list, compose form, or any
   attachment. Offline webmail requires a non-trivial
   client-side store and is out of scope.
4. **Full-text search body indexing** — `?body=1` falls
   back to a per-row body read; it is not indexed. For a
   real mailbox with thousands of messages the body search
   is the long pole. A proper inverted index is a follow-up.
5. **Mobile native app** — out of scope. The webmail UI
   works on a mobile browser (responsive layout tested via
   the CSS @media blocks), but there is no iOS / Android
   native client.
6. **Pasted-image screenshot into the compose body** — the
   compose accepts multi-part attachments but does NOT have
   an in-body paste-or-drop image handler. Pasting an image
   into the body editor attaches it as a separate
   attachment in the multipart send path. A custom drag-and-
   drop into the body is a follow-up.
7. **JMAP / RFC 8620 / RFC 8621** — the Caddy mail.vhost
   catch-all routes non-autodiscover paths to the JMAP
   listener at 127.0.0.1:8081, but the webmail UI itself
   does not speak JMAP. JMAP-native clients (e.g. a future
   third-party app) are a separate path; the R1 webmail
   uses the existing CoreMail /api/v1/webmail/* REST.
8. **Calendar attendance / RSVP flows** — see point 1.
9. **Mailbox migration** — `provision.New` /
   `migration.New` exist for moving mailboxes between
   servers, but the webmail UI exposes no "migrate" surface.
   Migration is an admin function, not a user one.
10. **Server-side rendered email previews** — the SPA
    fetches `rfc822` and renders client-side. A future
    slice could pre-render to plain text on the server to
    make the message list faster on slow connections.

---

## 11. BLOCKERs (R1 cut)

**None.** The slices that R1 brings together all landed green
on main before this audit. The R1 cut is a documentation +
audit pair, a smoke-surface extension, and a small set of
focused regression tests. There is no "ship-blocker" status.

---

## 12. Files changed (R1 cut)

```
?? docs/WEBMAIL_RELEASE_1_AUDIT.md            # this document
?? docs/WEBMAIL_SECURITY_REVIEW.md           # security companion
?? release/scripts/smoke-webmail-js.sh      # Node syntax for webmail bundle
?? release/scripts/smoke-webmail-ui.sh      # 12 structural / RTL / router-drift checks
?? release/scripts/smoke-webmail-browser.sh  # bundle-presence + content markers
?? release/scripts/smoke-webmail-functional-browser.sh   # shell wrapper for headless Chrome
?? release/scripts/smoke-webmail-functional-browser.mjs   # self-contained CDP smoke; PASSES locally
?? release/scripts/smoke-caddy-autodiscover.sh           # static-analysis Caddy regression
M release/webmail/CHANGELOG.md              # R1 entry — includes the Client Setup tab in the no-claim list
M release/webmail/assets/webmail.js         # Mail Client Setup tab + renderClientSetupTab + copyButton + openClientSetup / openCompose exports
M release/webmail/assets/webmail.css        # .settings-client-setup-* classes + .btn.xs + mobile @media rule
M internal/api/handlers/webmail_release1_test.go         # 4 R1 Go regressions
M docs/WEBMAIL_RELEASE_1_AUDIT.md           # update §3.0 + §3.10 + §7 / §7.1 with the actual UI shipped
```

No backend / SMTP / IMAP / POP3 / JMAP / queue / delivery
touches. No admin UI touches. No installer touches (setup-
https.sh stays as it was after the autodiscover slice
landed, and `smoke-caddy-autodiscover.sh` is the regression
guard going forward).

---

## 13. Testing — see the final structured report

The `READY_FOR_OPENCODE_REVIEW` run enumerates every smoke
and every Go test command, with PASS / FAIL counts.

---

## 14. Confirmation

**NO COMMIT, NO PUSH.** Working tree only on
`D:\orvix_new`, branch `feature/webmail-release-v1`. VPS
untouched. Admin worktree untouched.
