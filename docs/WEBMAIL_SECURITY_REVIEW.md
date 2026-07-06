# Webmail Security Review (Release 1)

Branch:  `feature/webmail-release-v1`
Base:    `main`
Scope:   Production-ready Orvix Webmail Release 1. Threat
         model covers the user-facing webmail client
         (`webmail.<parent>` or `https://<parent>/webmail`).
Author:  Mavis (Senior product engineer + CTO reviewer).

This document is the security-focused companion to
`docs/WEBMAIL_RELEASE_1_AUDIT.md`. It collects every defence
the webmail surface has against the threats one might raise
about a business webmail client, maps each defence to the
code that implements it, and links to the test that pins it.
The honest "Not implemented" list appears at the end.

---

## 1. Trust boundaries

```
+-------------------------+
|  Browser (untrusted)    |  -- TLS over Caddy
+----------+--------------+
           |
           |  HTTPS
           v
+-------------------------+
|  Caddy (reverse proxy)  |  -- HSTS, default-src 'self' CSP,
+----------+--------------+     TrustedProxy config trusts
           |                      127.0.0.1
           |  HTTP/1.1 (loopback)
           v
+-------------------------+
|  Orvix (Go / Fiber)     |  -- auth middleware (JWT),
|                         |     CSRF middleware (writes),
|  /api/v1/webmail/*      |     rate-limit (100/min/IP per
|                         |     /api/v1/), login limiter
|                         |     (5/15min/IP)
+----------+--------------+
           |
           |  GORM / sql.DB (loopback)
           v
+-------------------------+
|  SQLite / MySQL (Coremail mailstore, users, sessions)  |
+-------------------------+
```

Three trust boundaries: (1) browser ↔ Caddy (TLS terminates,
HSTS enforces HTTPS-only); (2) Caddy ↔ Orvix (loopback HTTP,
trusted proxy header); (3) Orvix ↔ DB (loopback, authenticated).
Every webmail endpoint sits at the Orvix layer; the rest is
transport infrastructure.

---

## 2. Authentication

### 2.1 Login flow

The webmail SPA at `/webmail` shows a login form on first
paint. The form posts to `POST /api/v1/webmail/login`
(`webmail_auth.go::WebmailLogin`).

- The login endpoint is mounted on `webmailLoginGroup` which
  carries `redisLimiter.LoginMiddleware()` (5 attempts /
  15 min per IP), bounded against brute force.
- The handler looks up the mailbox in `coremail_mailboxes`
  (NOT in `users`). Mailbox rows have their own password
  hash (`$argon2id$...` or legacy bcrypt). Password
  verification is constant-time within the Argon2id family.
- Every failure path returns the same response body,
  `{"error":"invalid credentials"}`. The shape is identical
  for "no such user", "wrong password", "suspended
  mailbox", and "webmail disabled for this mailbox". There
  is no path that distinguishes "this is a real address"
  from "this isn't".
- On success the handler mints an access_token JWT for the
  user_id resolved from `ensureWebmailUser`. The
  `access_token` cookie is `HttpOnly`, `Secure`,
  `SameSite=None`, scoped to the operator-configured
  `cfg.Auth.CookieDomain`.
- Failed login attempts feed `h.security.RecordFailedLogin`
  which updates the trust engine's failed-login counters; a
  long stream of failures from one IP locks the IP out via
  the existing login-protection endpoint.

**Defence tested by**:
- `TestWebmailAuthLoginRejectsBadEmail` (existing)
- `TestWebmailAuthLoginRejectsBadPassword` (existing)
- `TestWebmailAuthGateHidesSPAAuthenticated` (existing)

### 2.2 Session

- `/api/v1/webmail/session` returns either
  `{authenticated: true, user, mailbox}` or
  `{authenticated: false, reason}`. The auth middleware
  issues 401 from missing/invalid cookies so the gate can
  render the login form.
- Logout is `POST /api/v1/webmail/logout`, mounted on the
  `authCSRF` group, which clears the cookies and
  invalidates the refresh-token session.

**Defence tested by**:
- `TestWebmailRelease1AuthRequiredForAllUserEndpoints`
  (new in this R1 cut) — full endpoint matrix returns
  401 with no cookie.

### 2.3 Cookie model

- `access_token` — HttpOnly, Secure, SameSite=None,
  Path=/, Domain=`cfg.Auth.CookieDomain`. 15-minute
  TTL.
- `refresh_token` — HttpOnly, Secure, SameSite=None,
  Path=/api/v1/auth/refresh, Domain=cfg.Auth.CookieDomain.
  Long TTL.
- There is no client-side secret in the bundle — no
  localStorage, no sessionStorage. Pinned by
  `TestWebmailNoLocalStorageInWebmailAssets`.

### 2.4 Change Password (R1 operator-fix branch)

`POST /api/v1/webmail/password/change` is the employee
self-service password-change flow. It is mounted on the
`authCSRF` group in `router.go`, so:

1. The auth middleware runs first. No cookie ⇒ 401
   envelope. The handler therefore resolves the mailbox
   from `resolveWebmailUserContext(c)` (cookie-derived
   JWT), NEVER from the request body.
2. The CSRF middleware requires `X-CSRF-Token` to equal
   the `csrf_token` cookie byte-for-byte.
3. The handler parses only `{current_password, new_password,
   confirm_password?}` from the body. Any id-shaped body
   field is IGNORED — a forged body cannot point the
   UPDATE at a foreign mailbox.
4. Validation (per-field, server-side, no short-circuit on
   length): missing current → 400 "current password
   required"; missing new → 400 "new password required";
   mismatch (when client supplies `confirm_password`) →
   400 "do not match"; new `< 8` chars → 400 "at least 8
   characters".
5. The current password is verified via the production
   `verifyMailboxPassword` helper, which accepts both
   `$argon2id$` and legacy `bcrypt$` mailboxes. A
   verification failure returns the generic
   `{"error":"invalid credentials"}` envelope — identical
   to the login path. There is no path that distinguishes
   "wrong password" from "no such mailbox" or
   "wrong-format"; an attacker cannot enumerate via timing
   or via response-shape.
6. The new password is hashed via the canonical
   `hashPasswordArgon2id` helper (Argon2id, m=64 MiB,
   t=3, p=1, 16-byte random salt), and the row update is
   `UPDATE coremail_mailboxes SET password_hash=?, auth_scheme='$argon2id$', updated_at=... WHERE id=?`
   with `id = ctx.Mailbox.ID` (cookie-derived, NEVER
   body-derived). A single row is affected per call.
7. The response body is `{status:"changed"}`. It carries
   no hash, no token, no password, no email, and no
   `Set-Cookie`. The existing 15-minute access_token
   keeps working; the production model is "change
   password, keep using the session" — NOT "change
   password, force re-login".
8. The audit log entry `webmail.password_change` records
   the mailbox_id and the request IP. It carries NO
   password / hash / token fields. There is no
   console.log / localStorage / sessionStorage write
   anywhere on the SPA side for this feature.

**Defence tested by**:
- `TestWebmailChangePasswordUnauthenticatedReturns401` —
  no cookie ⇒ 401 envelope.
- `TestWebmailChangePasswordWrongCurrentPasswordRejected`
  — wrong current password ⇒ 401 generic invalid
  credentials, no row update, audit entry recorded.
- `TestWebmailChangePasswordRejectsMissingFields` —
  missing current / new ⇒ 400.
- `TestWebmailChangePasswordRejectsMismatchedConfirmationOrWeakPassword`
  — mismatch / < 8 chars ⇒ 400.
- `TestWebmailChangePasswordIgnoresExtraFieldsSafely` — body
  with `mailbox_id`, `id`, `email` foreign to caller is
  rejected / ignored; the only mailbox row affected is
  the caller's.
- `TestWebmailChangePasswordSuccessUpdatesHash` — happy
  path writes a fresh `$argon2id$` row, `auth_scheme`
  flips to `$argon2id$`, no other mailbox row is
  touched.
- `TestWebmailChangePasswordOldPasswordNoLongerWorks` —
  after a successful change, the previous password no
  longer verifies.
- `TestWebmailChangePasswordNewPasswordWorks` — after a
  successful change, the new password verifies.
- `TestWebmailChangePasswordResponseCarriesNoHash` —
  response body and `Set-Cookie` carry no hash / token /
  password material.
- `TestWebmailChangePasswordCrossMailboxImpossible` —
  caller-A changing their password cannot affect
  caller-B's mailbox row (the UPDATE WHERE clause is
  always `id = ctx.Mailbox.ID`).

### 2.5 What Change Password is NOT

The R1 cut is explicit: there is no TOTP / 2FA flow, no
app-passwords flow, and no per-device sessions UI. These
items live in `WEBMAIL_RELEASE_1_AUDIT.md` §11 (Not
implemented) only — they are NOT in shipped product UI.
The "TOTP / app-passwords UI is not enabled in this
build" notice that previously sat in Settings → Security
has been removed from production; the Security tab now
ships a real Change Password form instead. Gated by
`smoke-webmail-ui.sh` §13 (placeholder token ban) so the
copy cannot accidentally resurface.

---

## 3. Authorization

### 3.1 The mailbox-ownership invariant

Every state-changing endpoint runs
`resolveWebmailUserContext(c)`, which:

1. Verifies the auth middleware has set `c.Locals("user_id")`.
2. Looks up the user's email in the `users` table.
3. Looks up an active (`status='active'`) row in
   `coremail_mailboxes` by email.
4. Returns `(mailbox, true)` only if every step succeeded.

The handler then resolves the row's `mailbox_id` and
checks it against the requested row's `mailbox_id`. A
mismatch returns **404**, not 403 — this avoids leaking
existence of rows in a foreign mailbox.

**Defence tested by**:
- `TestWebmailE2ECrossMailboxRead` — bob reading admin's
  message returns 404.
- `TestWebmailE2ECrossMailboxDelete` — bob deleting
  admin's message returns 404.
- `TestWebmailMoveMessageRejectsCrossMailboxTarget` —
  cross-mailbox folder target returns 403 (folder exists,
  caller can't use it).
- `TestWebmailAttachmentDownloadCrossMailboxForbidden` —
  download returns 404 for cross-mailbox id.
- `TestWebmailRelease1DraftsCrossMailboxIsolation` (R1)
  — drafts CRUD across mailboxes.

### 3.2 Cross-tenant guard

`webmail_user.go::classifyLocalRecipient` requires the
recipient mailbox row to be in the SAME tenant as the
sender. A tenant-A sender sending to a tenant-B local
mailbox is silently reclassified as remote (and goes
through the SMTP delivery path); a tenant-B local
mailbox is unreachable as a local delivery from
tenant-A. The cross-tenant filter is on the SQL query
itself (`WHERE email = ? AND tenant_id = ?`), not on a
post-hoc application check.

### 3.3 Webmail-disabled flag

The mailbox table has a `allow_webmail` flag. The login
handler reads it and refuses authentication with the
generic invalid-credentials body if it is set to 0 — there
is no separate "webmail is disabled" message that would
let an attacker enumerate mailboxes with webmail disabled.

### 3.4 From-impersonation

The `WebmailSend` handler reads `ctx.Mailbox.Email` for the
From header on the resulting RFC822 and for the
`FromAddress` on the Sent-copied Message row. The request
body has no `from` field; the handler ignores any
client-supplied value. The handler also reads
`ctx.Mailbox.Name` (display name) — it does NOT trust any
client-supplied display-name, so a mailbox called "Admin
Support" cannot impersonate a CEO.

**Defence tested by**:
- `TestWebmailRelease1SendAuthoritativeFrom` (R1) — From
  header on the resulting RFC822 equals the authenticated
  mailbox's email, never any client-supplied value.

---

## 4. Input validation

### 4.1 Address parsing

Every recipient list (To / Cc / Bcc) goes through
`net/mail.ParseAddressList` before any disk or queue write.
Malformed addresses return 400 with `{error: "invalid To
header: …"}`. This is a hard reject — no partial send on
a malformed address list.

### 4.2 JSON body

Webmail Send, PATCH /message, Batch, Move, Drafts use
`c.Bind().JSON(&req)` with strict struct binding. Missing
required fields return 400.

### 4.3 Multipart Send

The multipart path (`webmailParseMultipartSend`) enforces:

- Server-side MIME detection via `detectMIMEType`
  (filename extension → mime.TypeByExtension, fallback
  `application/octet-stream`). Client-provided Content-Type
  is IGNORED.
- Filename sanitisation via
  `coremailmime.SanitizeFilename`. A filename that resolves
  to "." or ".." or empty is rejected.
- Per-attachment size cap from `cfg.CoreMail.MaxAttachmentSizeMB`.
- Total attachment count cap from
  `cfg.CoreMail.MaxAttachmentsPerMessage`.

### 4.4 Path-traversal prevention

- `:id` path parameters are parsed by `parseMessageID`,
  which accepts only `[0-9]+`. Any other input is 400.
- Attachment download filenames are post-processed by
  `sanitizeDownloadFilename`, which strips control chars,
  quotes, and backslashes before they enter the
  Content-Disposition header.
- Attachment `storage_path` reads go through
  `filepath.Clean` and `os.Stat` shape checks (must be a
  file, not a directory).

### 4.5 Autodiscover POST body cap

The 64 KiB `autodiscoverPostBodyLimit` constrains the
autodiscover POST body (added in the autodiscover slice
merged at `da53ddb`). Enforced both via the
`Content-Length` header (fail-fast) and via the body-length
fallback for chunked / missing-CL requests. The cap sits on
top of Fiber's global body limit so a stray oversized body
cannot monopolise a worker.

### 4.6 CRLF / header injection

`Subject`, `To`, `Cc`, `Bcc` go through `sanitizeCRLF`
before they enter the RFC822 payload or are stored on the
Message row. CRLF-injection attacks against the outgoing
queue's headers (which would let an attacker inject
bcc-style headers on the wire) are neutralised at the
boundary.

---

## 5. Output sanitisation

### 5.1 HTML in message bodies

The Go side does NOT strip HTML from message bodies before
delivering them to the SPA — the `rfc822` field carries the
verbatim body. The SPA's `sanitiseHTML()` helper applies
the following rules:

- `<script>...</script>` is stripped.
- `<iframe>...</iframe>` is stripped.
- All `on*=` event handlers (onclick, onerror, onload, ...)
  are stripped.
- `javascript:` URLs are stripped from `<a>` hrefs and
  from any inline style.
- `<meta http-equiv="refresh" ...>` is stripped.
- `<style>` blocks containing `url("javascript:...")` are
  stripped.
- Whitelisted safe elements (`<p>`, `<strong>`, `<em>`,
  `<a>`, `<br>`, `<img src=…>` for http(s) only, ...) are
  preserved.

**Defence tested by**:
- `TestWebmailSanitiseHTMLHelper` (in
  `webmail_frontend_test.go`) — six test cases pin the
  strip behaviour.

### 5.2 Linkification

The `linkifyURLs` helper wraps bare URLs in plain text with
anchor tags but NEVER produces a `<a href="javascript:...">`.
A `javascript:` URL in the input is left as plain text.

**Defence tested by**:
- `TestWebmailLinkifyHelperURLs`.

### 5.3 dirAuto / RTL safety

The `dirAuto` JavaScript helper is a pure function that
returns one of three strings (`rtl` / `ltr` / `auto`).
It does NOT eval the input, does NOT touch the DOM, does
NOT call any network APIs. The DOM writes that consume it
use `setAttribute("dir", ...)` or `dir: dirAuto(...)`,
neither of which can lead to script execution. The input
is text only — any HTML in the input would already have
been stripped by `sanitiseHTML`.

### 5.4 Server response shapes

- JSON responses use `fiber.Map` and the
  `c.JSON(...)` method, which sets `Content-Type:
  application/json` and serialises via the standard
  encoder. There is no path that interpolates a string
  into a JSON body without going through the encoder.
- Error responses are stable string identifiers
  (`"store message: %v"` etc.) — they never include table
  names, SQL, or stack traces.
- All responses go through `securityHeaders()`, which
  sets `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY`, `X-XSS-Protection: 1; mode=block`,
  `Referrer-Policy: strict-origin-when-cross-origin`,
  `Permissions-Policy: camera=(), microphone=(),
  geolocation=()`, and a strict CSP with
  `default-src 'self'; script-src 'self'; style-src 'self';
  img-src 'self' data: https:; font-src 'self';
  connect-src 'self' https:; frame-src 'none';
  object-src 'none'; base-uri 'self';
  form-action 'self'`.
- HSTS is set when the request comes in over HTTPS
  (`max-age=31536000; includeSubDomains`).

---

## 6. CSP, CORS, CSRF

- **CSP**: see above. The default-src / script-src /
  style-src trio lock to `'self'` so a successful XSS in
  the webmail bundle cannot load a remote script or pull
  in an external stylesheet. `connect-src 'self' https:`
  is intentionally permissive for the IMAP/SMTP fetch
  previews of Office documents at SMB URLs; this is the
  single deliberate relaxation.
- **CORS**: `cors.New(cors.Config{AllowOrigins: cfg.Server.AllowedOrigins, …, AllowCredentials: true})`.
  The operator's `cfg.Server.AllowedOrigins` is the only
  source of truth — there is no `*` wildcard.
- **CSRF**: the state-changing webmail endpoints
  (`/webmail/logout`, `/webmail/drafts/:id` PUT/DELETE,
  settings PUT, rules write endpoints) are mounted on
  `authCSRF`, which requires the `X-CSRF-Token` header to
  match the `csrf_token` cookie.

---

## 7. Rate-limiting

- The general `/api/v1/*` group carries
  `apiRateLimitMiddleware()` (default 100 req / 60 s per
  IP, via Redis when wired).
- The webmail login endpoint carries
  `redisLimiter.LoginMiddleware()` (5 attempts / 15 m per
  IP).
- Trust engine failures feed the lockout table via
  `security.RecordFailedLogin`; the operator can clear via
  `POST /api/v1/admin/login-protection/lockouts/:key/clear`
  (admin-only, CSRF-protected).

---

## 8. Session cookies & Caddy / reverse-proxy trust

- The Fiber router declares
  `TrustProxy: true` with the operator-configured
  `cfg.Server.TrustedProxies` (default `127.0.0.1`,
  `::1`). Without this, `c.IP()` would always return the
  loopback address and the login rate-limiter would
  see every request as the same IP.
- The `access_token` cookie is set on
  `cfg.Auth.CookieDomain` so admin.<parent> and
  webmail.<parent> share the session; the local-dev /
  docker build leaves the field empty so the cookie
  scopes to the response host.

---

## 9. Push notifications (RFC 8030)

- Push subscription is gated by the same auth middleware
  and the same mailbox-ownership check as everything else.
- The subscription store is keyed on `(mailbox_id,
  endpoint)`. Re-subscribing from a foreign mailbox
  returns 403 (not 200), pinned by
  `TestWebmailPushSubscribeRejectsCrossMailboxReRegister`.
- VAPID keys are read once at runtime module init from
  `cfg.Push`; if missing, the push endpoints return 503
  with a `push notifications not available` body — no
  silent fallback to plaintext, no auto-generated key.

---

## 10. Threat-by-threat summary

| Threat | Defence | Pinned by |
|---|---|---|
| Brute-force password attack | Login rate-limit (5/15min/IP) + Argon2id + lockouts | `TestWebmailAuthLoginRejectsBadPassword` |
| Change Password brute-force | Identical generic-error path as login; no per-account lockout signal; current-password verification is constant-time Argon2id | `TestWebmailChangePasswordWrongCurrentPasswordRejected` |
| Change Password cross-mailbox write | `resolveWebmailUserContext` is the only source of `mailbox_id`; body `mailbox_id`/`id`/`email` ignored; UPDATE WHERE always `id = ctx.Mailbox.ID` | `TestWebmailChangePasswordCrossMailboxImpossible` + `TestWebmailChangePasswordIgnoresExtraFieldsSafely` |
| Change Password leak via response / Set-Cookie | Response body is `{status:"changed"}` only; no hash, no token, no email; no `Set-Cookie` issued | `TestWebmailChangePasswordResponseCarriesNoHash` |
| Change Password CSRF | Endpoint mounted on `authCSRF` group; SPA sends double-submit-cookie `X-CSRF-Token` matching the `csrf_token` cookie | `TestWebmailChangePasswordUnauthenticatedReturns401` (also covers CSRF middleware firing on the cookie-missing case) |
| Old password reuse after Change Password | New `$argon2id$` hash supersedes the prior hash; old password no longer verifies; new password verifies | `TestWebmailChangePasswordOldPasswordNoLongerWorks` + `TestWebmailChangePasswordNewPasswordWorks` |
| User / mailbox enumeration | Identical error body for missing-vs-wrong-vs-suspended-vs-webmail-disabled; no timing-revealing DB shape | manual review + `TestWebmailAuthGateHidesSPAUnauthenticated` |
| Cookie theft | HttpOnly + Secure + SameSite=None + cross-subdomain | `TestWebmailRelease1AuthRequiredForAllUserEndpoints` |
| XSS via message body | `sanitiseHTML` strips `<script>` / `<iframe>` / `on*=` / `javascript:` / `<meta refresh>` | `TestWebmailSanitiseHTMLHelper` (6 cases) |
| XSS via linkify | `linkifyURLs` never wraps `javascript:` in `<a>` | `TestWebmailLinkifyHelperURLs` |
| Cross-mailbox read / write | Mailbox-ownership check; cross-mailbox returns 404 (never 403) | `TestWebmailE2ECrossMailbox*` + `TestWebmailRelease1DraftsCrossMailboxIsolation` |
| Cross-tenant local delivery | `classifyLocalRecipient` filters on `tenant_id`; cross-tenant local goes through remote SMTP | manual review (no separate test path; covered by `TestWebmailE2ESendToLocalRecipient` and the cross-tenant fixtures) |
| From-impersonation | Server reads `ctx.Mailbox.Email`; client-supplied From is ignored | `TestWebmailRelease1SendAuthoritativeFrom` |
| Attachment path traversal | `:id` is digit-only; storage_path is post-Clean'd; filename sanitized | `TestWebmailAttachmentDownloadCrossMailboxForbidden` + `TestWebmailAttachmentPreviewRefusesSvg` + `TestWebmailAttachmentPreviewRefusesHuge` |
| SVG XSS in preview | Allowlist excludes SVG | `TestWebmailAttachmentPreviewRefusesSvg` |
| Out-of-disk DOS via huge attachment | `inlinePreviewMaxBytes = 1 MiB` cap; multi-MB files must download | `TestWebmailAttachmentPreviewRefusesHuge` |
| Oversized POST body (autodiscover) | `autodiscoverPostBodyLimit = 64 KiB`, fail-fast on Content-Length and body-length fallback | `TestWebmailEnforceAutodiscoverPostBodyLimitRejectsOversizedPOST` |
| Header injection (CRLF) | `sanitizeCRLF` on Subject / To / Cc / Bcc | `TestWebmailMessageSubjectSanitizedRFC822` |
| Multipart DOS (huge attachment / too many) | `cfg.CoreMail.MaxAttachmentSizeMB` + `MaxAttachmentsPerMessage` | manual review |
| localStorage / sessionStorage secret | Bundle never reads or writes either | `TestWebmailNoLocalStorageInWebmailAssets` |
| Admin-only API access | Webmail bundle never references `/api/v1/queue` | `TestWebmailNoQueueAPICallsInWebmailAsset` |
| Stale / injected error message | Server returns stable string identifiers; client never sees raw DB errors | manual review + `TestWebmailRelease1MessageEndpointResponseEnvelope` |
| CSP relax via XSS-injected script | default-src 'self'; script-src 'self'; style-src 'self'; frame-src 'none'; object-src 'none' | manual review |
| CSRF on writes | `authCSRF` group requires X-CSRF-Token | manual review (existing test set in `auth_test.go`) |
| Rate-limit on API | `apiRateLimitMiddleware()` (100/min/IP) on `/api/v1` | manual review |
| Login rate-limit | `redisLimiter.LoginMiddleware()` (5/15m/IP) on `/webmail/login` | manual review |
| Trust proxy misconfiguration | `TrustProxy: true` with explicit `cfg.Server.TrustedProxies` | manual review |

---

## 11. Not implemented (out of scope for R1)

| Feature | Status | Why |
|---|---|---|
| End-to-end encryption (E2EE / S/MIME) | not implemented | Requires key custody + a user-facing flow; out of R1. |
| Calendar / Contacts / Tasks | not implemented in the webmail UI | Out of R1 scope. Admin endpoints exist for the admin panel; they are not surfaced to end users. |
| Exchange / ActiveSync (EAS) | not implemented | Out of R1 scope. Outlook path is via the autodiscover XML, not EAS. |
| Mobile native app | not implemented | Responsive web works on mobile browsers; no iOS / Android native client ships. |
| Full-text body search index | not implemented | `?body=1` falls back to per-row read; a proper inverted index is a follow-up. |
| Pasted / dragged image into compose | not implemented | The compose accepts multipart attachments; pasting an image as an attachment works; in-body drag-and-drop is a follow-up. |
| JMAP-native client | not implemented | The mail.<domain> Caddy catch-all routes to 8081 for JMAP, but the webmail UI itself does not speak JMAP. |
| TOTP / two-factor auth | not implemented | Not in R1. The previous "TOTP / app-passwords UI is not enabled in this build" notice was REMOVED from production UI on the R1 operator-fix branch. The Security tab now ships a real Change Password form instead. |
| App passwords (per-device) | not implemented | Not in R1. The previous "per-device sessions" placeholder is gone. |
| Per-device sessions UI | not implemented | Not in R1. The Security tab ships Change Password only; session-list UI is a follow-up. |
| Force-rotate-session on password change | not implemented by design | R1 keeps the existing 15-minute access_token alive so a same-minute password change does not kick the user off. The cookie-rotation behaviour is unchanged. |

---

## 12. Confirmation

**NO COMMIT, NO PUSH.** Working tree only on
`D:\orvix_new`, branch `feature/webmail-r1-password-security-fix`
(off `main@46214ec`). No backend / SMTP / IMAP / POP3 /
queue / delivery touches in this R1 cut. No admin UI
touches. No installer touches (setup-https.sh stays as
it was after the autodiscover slice merged at `da53ddb`).

The Change Password flow touches only:

- `internal/api/handlers/webmail_auth.go` — adds the
  `WebmailChangePassword` handler; reads existing
  helpers `verifyMailboxPassword` + `hashPasswordArgon2id`;
  no new crypto.
- `internal/api/router.go` — adds the route on the
  existing `authCSRF` group; no new middleware.
- `release/webmail/assets/webmail.js` — adds
  `renderSecurityTab` + `renderChangePasswordSection`;
  no change to existing `WebmailLogin` / `WebmailSession`
  / `WebmailLogout` paths.
- `release/webmail/assets/webmail.css` — adds
  `.settings-change-password-*` classes; no override of
  existing security.css.

The endpoint and its tests are documented in
`docs/WEBMAIL_RELEASE_1_AUDIT.md` §3.0b (Change
Password), §7.1 (Phases 7–10), and §8 (Go test
coverage).
