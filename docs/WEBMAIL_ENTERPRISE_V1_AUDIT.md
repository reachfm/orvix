# Webmail Enterprise v1 — Honest Audit

**Status (this PR):** Vertical Slice 1 — email account autodiscover/autoconfig.
**Scope of this audit:** What already exists in `release/webmail/` and
`internal/api/handlers/`, and what is **still missing** for a full Outlook-class
webmail product. Anything not listed under "Implemented" must be considered
**not delivered** — this audit does not claim parity with Exchange / Outlook.com.

This document is intentionally conservative. It records only what the code in
this repository actually does today, against what the Webmail Enterprise v1
spec asks for. No future work is implied or promised.

---

## 1. Frontend assets (`release/webmail/`)

| File | Purpose | Status |
| --- | --- | --- |
| `index.html` | Webmail SPA shell, loads auth-gate + webmail.js | **Implemented** |
| `assets/auth-gate.js` | Pre-SPA session probe; shows login form / no-mailbox card / reveals SPA | **Implemented** |
| `assets/auth-gate.css` | Gate styles | **Implemented** |
| `assets/webmail.js` (~189 KB) | Core SPA: folder list, message list, reading pane, compose, drafts, attachments UI, search, rules/vacation/forwarding settings | **Implemented** |
| `assets/webmail.css` (~71 KB) | Styles | **Implemented** |
| `assets/webmail-push.js` | Web Push (RFC 8030) subscription helper, no-op when VAPID not configured | **Implemented** |
| `sw.js` | Service worker | **Implemented** |

### Frontend features — what the SPA can do today

- Login / logout / session probe (auth gate)
- Folder list (Inbox, Sent, Drafts, Trash, Junk, Archive, custom folders)
- Message list with pagination + read/unread + attachment indicator
- Reading pane with HTML rendering, attachment list/download/preview
- Compose (To/Cc/Bcc/Subject/body) with attachments, draft autosave, signature
- Drafts CRUD
- Move / delete / archive / batch operations
- Per-message source download
- Search (sender / recipient / subject / body)
- Per-mailbox rules engine (list / create / update / delete)
- Vacation responder (get / put)
- Forwarding (get / put)
- Per-mailbox settings (signature, display name, default identity)
- Web Push subscribe / unsubscribe / status / test
- Mobile responsive layout, light + dark theme
- RTL/Arabic via `dir="auto"`

### Frontend features explicitly NOT in this slice

- **Calendar UI** (month / week / day / event create/edit/delete modal) — **not implemented**
- **Contacts UI** (list / add / edit / delete / search / compose autocomplete) — **not implemented**
- Outlook-style attachment cloud placeholders, conversation threading toggle, snooze, follow-up reminders — **not implemented**

---

## 2. Backend API surface (`internal/api/handlers/webmail*.go`)

### User-facing webmail endpoints (all under `/api/v1/webmail/*`, auth + mailbox ownership enforced)

| Endpoint | Method | Handler | Status |
| --- | --- | --- | --- |
| `/api/v1/webmail/login` | POST | `WebmailLogin` | Implemented |
| `/api/v1/webmail/logout` | POST | `WebmailLogout` | Implemented |
| `/api/v1/webmail/session` | GET | `WebmailSession` | Implemented |
| `/api/v1/webmail/me` | GET | `WebmailMe` | Implemented |
| `/api/v1/webmail/folders` | GET | `WebmailFolders` | Implemented |
| `/api/v1/webmail/messages` | GET | `WebmailMessages` | Implemented |
| `/api/v1/webmail/messages/:id` | GET | `WebmailMessage` | Implemented |
| `/api/v1/webmail/messages/:id` | PATCH | `WebmailUpdateMessage` | Implemented (read/star flags) |
| `/api/v1/webmail/messages/:id/source` | GET | `WebmailMessageSource` | Implemented |
| `/api/v1/webmail/messages/:id/archive` | POST | `WebmailArchive` | Implemented |
| `/api/v1/webmail/messages/:id/delete` | POST | `WebmailDelete` | Implemented |
| `/api/v1/webmail/messages/:id/move` | POST | `WebmailMoveMessage` | Implemented |
| `/api/v1/webmail/messages/batch` | POST | `WebmailMessageBatch` | Implemented |
| `/api/v1/webmail/attachments/:id` | GET | `WebmailAttachmentDownload` | Implemented |
| `/api/v1/webmail/attachments/:id/preview` | GET | `WebmailAttachmentPreview` | Implemented |
| `/api/v1/webmail/folders/:id/read-all` | POST | `WebmailMarkFolderRead` | Implemented |
| `/api/v1/webmail/send` | POST | `WebmailSend` | Implemented — enqueues through the same `queue.QueueEngine` SMTP receiver + delivery worker use |
| `/api/v1/webmail/drafts` | GET / POST | `WebmailListDrafts` / `WebmailSaveDraft` | Implemented |
| `/api/v1/webmail/drafts/:id` | GET / PUT / DELETE | `WebmailGetDraft` / `WebmailSaveDraft` / `WebmailDeleteDraft` | Implemented |
| `/api/v1/webmail/push/subscribe` | POST | `PushSubscribe` | Implemented |
| `/api/v1/webmail/push/unsubscribe` | POST | `PushUnsubscribe` | Implemented |
| `/api/v1/webmail/push/status` | GET | `PushStatus` | Implemented |
| `/api/v1/webmail/push/test` | POST | `PushTest` | Implemented |
| `/api/v1/webmail/settings` | GET / PUT | `WebmailGetSettings` / `WebmailPutSettings` | Implemented |
| `/api/v1/webmail/rules` | GET / POST | `WebmailListRules` / `WebmailCreateRule` | Implemented |
| `/api/v1/webmail/rules/:id` | PUT / DELETE | `WebmailUpdateRule` / `WebmailDeleteRule` | Implemented |
| `/api/v1/webmail/vacation` | GET / PUT | `WebmailGetVacation` / `WebmailPutVacation` | Implemented |
| `/api/v1/webmail/forwarding` | GET / PUT | `WebmailGetForwarding` / `WebmailPutForwarding` | Implemented |

### Send / queue / attachment wiring

- Outbound mail is enqueued through `internal/coremail/queue.QueueEngine` — the same engine the SMTP receiver feeds — so there is **no parallel send path**.
- Attachments are stored as `coremail_attachments` rows; upload is through the same compose endpoint; download / preview are scoped to the caller's mailbox.
- Search uses the existing coremail search path, scoped to the caller's mailbox.

### Admin webmail management (`/api/v1/webmail/*`, admin role)

| Endpoint | Method | Status |
| --- | --- | --- |
| `/api/v1/webmail/accounts` | GET | Implemented |
| `/api/v1/webmail/sessions` | GET | Implemented |
| `/api/v1/webmail/activity/:mailboxId` | GET | Implemented |
| `/api/v1/webmail/storage/:mailboxId` | GET | Implemented |
| `/api/v1/webmail/sessions/:id/revoke` | POST | Implemented |
| `/api/v1/webmail/sessions/revoke-all` | POST | Implemented |
| `/api/v1/webmail/controls/force-logout/:mailboxId` | POST | Implemented |
| `/api/v1/webmail/controls/unlock/:mailboxId` | POST | Implemented |
| `/api/v1/webmail/controls/reset-preferences/:mailboxId` | POST | Implemented |
| `/api/v1/webmail/controls/clear-counters/:mailboxId` | POST | Implemented |

---

## 3. Calendar — status

| Item | Status |
| --- | --- |
| Calendar UI inside webmail | **NOT implemented** — no calendar route in the SPA |
| `GET /api/v1/webmail/calendar/events` | **NOT implemented** |
| `POST /api/v1/webmail/calendar/events` | **NOT implemented** |
| `GET /api/v1/webmail/calendar/events/:id` | **NOT implemented** |
| `PATCH /api/v1/webmail/calendar/events/:id` | **NOT implemented** |
| `DELETE /api/v1/webmail/calendar/events/:id` | **NOT implemented** |
| ICS import / export | **NOT implemented** |
| Outlook calendar sync (EAS / CalDAV / Exchange) | **NOT implemented** and **will not be claimed** in this audit |
| Admin-side calendar handlers (`/api/v1/calendar/events`) | Implemented but operate at the admin boundary only — they are not the user-facing webmail calendar |

> **Honest statement:** Orvix does not implement any calendar-sync protocol
> (EAS, CalDAV, Exchange ActiveSync, JMAP Calendar). Any client that requires
> calendar sync cannot be served by Orvix today. This is the same answer
> regardless of "Outlook" or "Thunderbird" branding.

---

## 4. Contacts — status

| Item | Status |
| --- | --- |
| Contacts UI inside webmail | **NOT implemented** |
| `GET /api/v1/webmail/contacts` | **NOT implemented** |
| `POST /api/v1/webmail/contacts` | **NOT implemented** |
| `PATCH /api/v1/webmail/contacts/:id` | **NOT implemented** |
| `DELETE /api/v1/webmail/contacts/:id` | **NOT implemented** |
| Contacts autocomplete in compose To/Cc/Bcc | **NOT implemented** |
| Admin-side contact handlers (`/api/v1/contacts`) | Implemented but operate at the admin boundary only |

---

## 5. Mail client setup (autodiscover / autoconfig)

| Item | Status |
| --- | --- |
| Outlook Autodiscover (`/autodiscover/autodiscover.xml`) | **Implemented (this slice)** |
| Outlook Autodiscover (uppercase `/Autodiscover/Autodiscover.xml`) | **Implemented (this slice)** |
| Thunderbird autoconfig (`/.well-known/autoconfig/mail/config-v1.1.xml`) | **Implemented (this slice)** |
| Thunderbird autoconfig (`/mail/config-v1.1.xml`) | **Implemented (this slice)** |
| Apple `.mobileconfig` profile | **NOT implemented** — we do not sign profiles and we will not ship an unsigned one |
| DNS records documentation | `docs/WEBMAIL_CLIENT_SETUP.md` (this slice) |

### What the autodiscover / autoconfig endpoints cover

- **Email account setup only** — IMAP/SMTP host, port, TLS, username.
- **NOT** calendar sync, **NOT** global address list, **NOT** mobile device
  management (MDM) profile enrollment, **NOT** Exchange features.

### Domain validation

All four endpoints validate the requested domain by looking up `coremail_domains`
(`deleted_at IS NULL`, case-insensitive). The behaviour per failure mode is:

| Condition | Outlook Autodiscover (`/autodiscover/autodiscover.xml` and `/Autodiscover/Autodiscover.xml`) | Mozilla autoconfig (`/.well-known/autoconfig/mail/config-v1.1.xml` and `/mail/config-v1.1.xml`) |
| --- | --- | --- |
| Missing or invalid email (no `?email=` / `?emailaddress=`, no `<EMailAddress>` in POST body) | **HTTP 200** with XML error envelope — `<Response><Error><ErrorCode>600</ErrorCode><Message>missing or invalid email address</Message>...</Error></Response>`. Outlook does not treat HTTP 4xx as failure for autodiscover; it expects a 200 envelope so the user sees a readable message. | **HTTP 404** with empty body. Thunderbird falls back to manual setup. |
| Unknown domain (no row in `coremail_domains` with `deleted_at IS NULL`) | **HTTP 200** with XML error envelope — `<Response><Error><ErrorCode>600</ErrorCode><Message>domain not provisioned: &lt;domain&gt;</Message>...</Error></Response>`. | **HTTP 404** with empty body. |
| Soft-deleted domain (row exists but `deleted_at IS NOT NULL`) | Same as unknown domain — **HTTP 200** with the same `domain not provisioned: <domain>` envelope. The soft-delete filter is the single source of truth. | Same as unknown domain — **HTTP 404** with empty body. |
| Oversized POST body (Autodiscover only, > 64 KiB — see [POST body size cap](#post-body-size-cap) below) | **HTTP 413** with XML error envelope — `<Response><Error><ErrorCode>413</ErrorCode><Message>request body too large</Message>...</Error></Response>`. No part of the rejected body is reflected. | N/A — autoconfig is GET-only. |
| Database / runtime error during domain lookup | **HTTP 503** with plain-text body `autodiscover temporarily unavailable`. No stack trace. | **HTTP 503** with empty body. |

Invariants across every response shape:

- **No password is ever written into a response.** The Outlook POST `<LegacyDN>`
  and `<Password>` fields (if supplied) are never read or echoed.
- **The username is the full email address the client supplied**; we never
  invent or rewrite it.
- **No part of a rejected request body is reflected** — error messages are
  static constants (`"missing or invalid email address"`, `"request body too large"`,
  `"domain not provisioned: <domain>"`).
- **No stack trace** is ever written to a response body.

### POST body size cap

The Outlook Autodiscover endpoint is public and unauthenticated, so the POST
body is capped at **64 KiB** (`autodiscoverPostBodyLimit`) on top of Fiber's
global body limit. The cap is enforced **before** the body is regex-scanned
for `<EMailAddress>` or reflected back, and uses both the `Content-Length`
header (fail fast) and the actual body length (catch chunked / missing
`Content-Length`). Real-world Outlook envelopes are always well under 4 KiB;
64 KiB is a comfortable ceiling that fits a generous envelope with whitespace
and a huge user display name, but rejects anything that smells like an attempt
to consume regex / parser CPU at scale.

This is per-endpoint — the Fiber global body limit (50 MiB by default) is
unchanged. A deployment that wants the global limit tightened too can adjust
`cfg.Server.BodyLimit` separately.

### How mail host / ports are chosen

- IMAP host: `cfg.CoreMail.Hostname` if set, else `mail.<domain>`.
- IMAP port: `cfg.CoreMail.IMAPsPort` if > 0, else `993`.
- SMTP submission host: same as IMAP host.
- SMTP submission port: `cfg.CoreMail.SubmissionPort` if > 0, else `587`.

This means a multi-tenant deployment where every tenant uses the same
`mail.<apex>` host gets the right answer out of the box, and a deployment that
overrides `cfg.CoreMail.Hostname` per-server gets the right answer too.

---

## 6. Browser smoke / E2E — status

| Item | Status |
| --- | --- |
| `release/scripts/smoke-webmail-functional-browser.sh` | **NOT present in this slice** — a future slice |
| `release/scripts/smoke-webmail-functional-browser.mjs` | **NOT present in this slice** — a future slice |
| Backend Go tests for autodiscover / autoconfig | **Implemented (this slice)** |

We deliberately do not ship a browser smoke script that pretends to verify
features that are not yet implemented. The full browser smoke is the gate for
the webmail-UI slice, not this autodiscover-only slice.

---

## 7. Tests present in this repository

The webmail handler package already includes:

- `webmail_e2e_smoke_test.go`
- `webmail_enterprise2_test.go`
- `webmail_frontend_test.go`
- `webmail_message_autoseen_test.go`
- `webmail_push_integration_test.go`
- `webmail_push_test.go`
- `webmail_routing_test.go`
- `webmail_rules_test.go`
- `webmail_settings_test.go`
- `webmail_user_test.go`
- `webmail_attachment_send_test.go`
- `webmail_auth_gate_test.go`
- `webmail_auth_login_test.go`

The autodiscover slice adds:

- `internal/api/handlers/autodiscover_test.go` (new) — unit tests for the four
  endpoints + domain validation + no-password invariant.

---

## 8. Known limitations (this slice)

1. **No `Accept` negotiation.** All four endpoints always serve
   `text/xml; charset=utf-8`. Outlook and Thunderbird both send
   `Accept: */*` or `Accept: application/xml`, so this is sufficient in
   practice; a future slice can add explicit content-type negotiation if a
   client misbehaves.
2. **No POST XML schema validation.** The Outlook POST body is parsed with a
   permissive regex for `<EMailAddress>` / `<LegacyDN>`. A future slice can
   switch to `encoding/xml` with a strict schema once we have field
   requirements from real-world Outlook clients.
3. **No per-mailbox SSL / non-standard ports.** The endpoints always return
   IMAP `993` and SMTP `587`. Per-mailbox overrides are not supported.
4. **Apple `.mobileconfig` is intentionally not generated.** Signing profiles
   correctly requires either an MDM certificate or an HTTPS-distributable
   unsigned profile that iOS will refuse. We document the manual Apple Mail
   setup instead.
5. **No DNS automation.** DNS records are documented in
   `docs/WEBMAIL_CLIENT_SETUP.md`; this slice does not modify DNS automatically.

---

## 9. Summary verdict for this slice

- **READY_FOR_CODEX_REVIEW (slice-scoped):** yes — autodiscover XML,
  autoconfig XML, tests, docs. Code in `internal/api/handlers/autodiscover.go`
  + `internal/api/handlers/autodiscover_test.go`, wired in `internal/api/router.go`,
  documented in `docs/WEBMAIL_CLIENT_SETUP.md`. The two re-review blockers
  raised by Codex (internal/config test failures and the docs mismatch on
  malformed-input behaviour) are both addressed; see the "Re-review fix log"
  section below.
- **Webmail v1 as a whole:** still incomplete — calendar, contacts, browser
  smoke, and the full Outlook-like UI polish are separate future slices and
  are NOT claimed here.

---

## 10. Re-review fix log (post Codex round 1)

### BLOCKER 1 — `internal/config` test failures

Root cause was **CRLF line endings in `release/*.sh`** on the local Windows
checkout (the repo's `.gitattributes` declares `*.sh text eol=lf`, but
`core.autocrlf=true` was producing CRLF in the working tree). The CRLF
breakage had a cascade effect across multiple tests:

- `TestReleaseInstallScriptsUseLF` directly checks for CRLF and rejects.
- `TestHTTPSSetupScriptMailDomainBlock` parses setup-https.sh with
  `strings.Split(..., "\n")`; CRLF made `line == "}"` never match.
- `TestPublicInstallerMatchesReleaseBundleLayout` looks for the literal
  substring `"\n}\n"` to find the end of `validate_bundle_layout`; CRLF
  made it never match.
- `TestInstallerNonInteractiveEnvMode`, `TestInstallPromptsFailClosedOnCurlPipeEOF`,
  `TestInstallPromptsSupportEnvNonInteractive`, and
  `TestInstallPasswordPromptReadsPromptFDAndDoesNotSpinOnConfirmationEOF`
  build a harness by string-replacing `\nmain "$@"\n`; CRLF made every
  replacement a no-op, so the unstubbed `main "$@"` ran, which called
  `require_root`, which failed with "run as root or with sudo" on the
  non-root local environment.

All 30 `release/**/*.sh` files were normalised from CRLF to LF (matching
the canonical form declared by `.gitattributes`). After the fix:

- `git diff HEAD --stat` shows no content change for the `.sh` files
  (the canonical form in HEAD is already LF; only the autocrlf-induced
  CRLF in the working tree was removed). `git status` still lists them
  as "modified" because `core.autocrlf=true` flags the working tree as
  divergent from autocrlf's expected output, but there is no diff against
  the committed tree.
- `go test ./internal/config -count=1` passes (136 tests, 0 failures).
- `go test ./... -count=1 -timeout 900s -p 4` passes (67 packages, 0 failures).

### BLOCKER 2 — Docs mismatch on malformed-input behaviour

The previous version of §5 "Domain validation" had a single sentence that
claimed malformed input returned HTTP 400 with no XML body. The actual
implementation always returns HTTP 200 with an XML `<Response><Error>`
envelope for Outlook (because Outlook does not treat HTTP 4xx as a
failure for Autodiscover — it expects a 200 envelope so the user sees a
readable message), and HTTP 404 with empty body for Mozilla autoconfig.

The docs were rewritten with a per-condition behaviour table covering:

- Missing / invalid email.
- Unknown domain (no row in `coremail_domains`).
- Soft-deleted domain (`deleted_at IS NOT NULL`).
- Oversized POST body (> 64 KiB, Autodiscover only).
- Database / runtime error during domain lookup.

And a new "POST body size cap" subsection documenting the 64 KiB
`autodiscoverPostBodyLimit` and where it sits relative to Fiber's global
body limit.

Behaviour is now exactly what the code does, no claims that depend on
unimplemented features.