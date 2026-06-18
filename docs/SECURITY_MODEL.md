# Orvix Webmail — Security Model

This document captures the security posture of the
webmail user-facing API (`/api/v1/webmail/*`) and the
client (`release/webmail/`). It is the canonical
reference for any future reviewer who needs to know
what protections are in place and what to look for
when a change touches webmail.

## 1. Threat model

The webmail client is a single-page application served
from a subdomain (`webmail.<domain>`). It is reached
through the same reverse proxy as the admin panel and
the API. The user is an authenticated mailbox holder
with one or more mailboxes; the administrator is a
separate role that does **not** have any elevated
access to the webmail endpoints (admin endpoints live
under `/api/v1/admin/*` and `/api/v1/men/*`).

Threats in scope:

1. **Cross-tenant / cross-mailbox IDOR** — the
   application stores many tenants' mailboxes in the
   same database. Every read or write on a message,
   folder, draft, or attachment id must verify the
   caller's mailbox id matches the row's owner.
2. **Cross-site request forgery (CSRF)** — state
   changes from the webmail SPA must not be issuable
   from a third-party page that holds the user's
   session cookie.
3. **HTML email XSS** — incoming messages are
   untrusted. The body is rendered as HTML for
   `Content-Type: text/html` messages and as escaped
   plain text otherwise. Inline scripts, event
   handlers, and `javascript:` URLs must never reach
   the DOM.
4. **Header injection** — outgoing subject, To/Cc/Bcc,
   and reply-to fields are CRLF-stripped before being
   written into the RFC822. The subject and From name
   are also backslash- and quote-escaped.
5. **Attachment path traversal** — the download and
   preview endpoints accept only uint ids (the URL
   `:id` segment is parsed with `parseMessageID` which
   rejects any non-digit character). The server-side
   JOIN between `coremail_attachments` and
   `coremail_messages` enforces that the parent
   message belongs to the caller's mailbox, and the
   `coremail_domains` / `coremail_mailboxes` lookups
   in the local-routing path are tenant-scoped.
6. **Token theft via XSS** — there are no tokens in
   `localStorage` or `sessionStorage`. The access
   token and refresh token are HttpOnly cookies set
   by the auth middleware; the JS never reads or
   writes them.
7. **SVG / executable attachment** — the preview
   endpoint refuses `image/svg+xml` (script-capable
   XML) and other content types that the renderer
   cannot safely show inline. A separate `download`
   endpoint still serves the file as an attachment
   so the user can save it.

## 2. Authentication

- The webmail API is mounted under the
  `protected` group in `internal/api/router.go`,
  which applies `apikeys.Middleware()` then
  `auth.Middleware()`. Missing or invalid cookies
  return 401 before any handler runs.
- Cookies are `HttpOnly`, `Secure` (HTTPS only),
  `SameSite=None` with the `Domain` attribute set to
  the parent domain so `webmail.<parent>` and
  `admin.<parent>` share the session. The
  SameSite=None+Secure pairing requires HTTPS, which
  is the only supported deployment topology in
  production.
- Webmail login (`POST /api/v1/webmail/login`) is
  rate-limited (5 attempts per 15 minutes by default)
  to match the admin login. The limiter is wired
  through the same Redis (or in-memory fallback)
  rate limiter that protects `/auth/login`.
- Session hijacking is mitigated by `Secure` cookies
  and a short access-token TTL (15 minutes by default
  — see `cfg.Auth.AccessTokenTTL`). The refresh
  token has a longer TTL and is rotated on use.

## 3. CSRF posture

The current cookie model uses **SameSite=None+Secure**
for secure cross-subdomain operation between
`admin.<domain>` and `webmail.<domain>`. This is
required for the deployed SSO shape, but it is not a
CSRF defence by itself. `SameSite=None` explicitly
allows cross-site cookie attachment when the cookie is
also marked `Secure`.

Current CSRF resistance for webmail JSON APIs relies on
the combination of:

- HTTPS-only `Secure` cookies.
- Authentication middleware on every webmail endpoint.
- JSON-only request parsing for state-changing handlers.
- Browser CORS preflight for cross-origin JSON requests.
- Strict allowlisted origins; non-allowlisted origins
  cannot complete credentialed JSON requests.
- No form-compatible state-changing webmail endpoints.
- The auth-gate integration: the webmail SPA never
  exposes its session token to JavaScript, so a token
  cannot be lifted from browser storage.

A subset of endpoints — the ones that change
authentication state — are explicitly CSRF-protected
through the `authCSRF` sub-group:

- `POST /api/v1/auth/logout`
- `POST /api/v1/auth/logout-all`
- `POST /api/v1/auth/change-password`
- `POST /api/v1/webmail/logout`

These are the endpoints where the cost of a CSRF
false negative is highest (the user is logged out
against their will, or a password is changed). The
CSRF token is bound to the session id and verified on
every request.

Explicit CSRF tokens are currently used only where
they are already implemented. Future hardening can add
per-request CSRF token validation, or a required custom
header on every webmail state-changing endpoint, if the
deployment model changes or if webmail is exposed to a
broader cross-origin threat model.

## 4. Authorisation

Every state-changing or detail-returning webmail
handler resolves the caller's mailbox through
`webmailUserContext` and applies one of three
ownership checks:

- **By message id**: the handler calls
  `MailStore.LoadMessage(ctx, id, nil)` and asserts
  `msg.MailboxID == ctx.Mailbox.ID`. Foreign mailboxes
  get a 404 (not 403) so the response does not leak
  the existence of a message in another mailbox.
- **By folder id**: the handler calls
  `MailStore.Folders.GetByID(ctx, folderID, nil)` and
  asserts `folder.MailboxID == ctx.Mailbox.ID`. Same
  404-on-mismatch rule.
- **By batch**: the batch handler walks every id in
  the request and applies the message-id ownership
  check individually. Cross-mailbox ids are reported
  as per-id failures in the response
  (`{action, total, succeeded, failed, errors}`),
  never as a single 403 that aborts the whole batch.

The attachment endpoints use a single JOIN that
folds the ownership check into the same query:
`SELECT ... FROM coremail_attachments a JOIN
coremail_messages m ON m.id = a.message_id WHERE
a.id = ? AND m.mailbox_id = ? AND m.purged_at IS
NULL`. A foreign attachment id produces no rows and
returns 404.

The `classifyLocalRecipient` helper in
`webmail_user.go` enforces the cross-tenant guard
at the local-delivery path: a sender in tenant A
cannot route to a mailbox in tenant B through the
local path because the mailbox lookup is scoped to
`tenant_id = sender.TenantID`. The remote_smtp path
is the safe default for any recipient that does not
match both the local domain and the same-tenant
mailbox.

## 5. Input handling

- **To / Cc / Bcc**: parsed with
  `mail.ParseAddressList`. Malformed addresses return
  400 before any disk or queue write.
- **Subject / header fields**: CRLF stripped via
  `sanitizeCRLF` before the value is written to the
  RFC822. The `From` name is also backslash- and
  quote-escaped via `escapeHeader` so a header value
  cannot break out of its quoted-string.
- **Filename**: outgoing attachment filenames (when
  that feature is implemented) must go through
  `sanitizeFilenameForStorage`; the incoming path
  already does.
- **Search query**: the storage layer LIKE-escapes
  the query string with `%q%` and parameterises the
  placeholders — no SQL injection.
- **Body size**: the `body_limit` field in the Fiber
  config (`cfg.Server.BodyLimit`) caps the request
  body. The webmail send endpoint is small in
  practice (no attachments in this pack) so the
  default is comfortable.

## 6. Output handling

- **HTML body**: rendered through `sanitiseHTML`
  which strips `<script>`, `<style>`, `<iframe>`,
  `<object>`, `<embed>`, `<form>`, `on*` event
  attributes, `javascript:` URLs, and `<meta
  http-equiv=refresh>`. The same helper is used for
  the read-pane rendering path and the snippet
  preview returned to the list view.
- **External images**: not auto-loaded. The current
  client renders `<img src="https://...">` but the
  CSP (see §8) restricts `img-src` to `self`, `data:`,
  and `https:` — that allows the image to load if the
  user is online, but does not pull resources from
  non-HTTPS hosts. The behaviour matches Outlook
  Web Access: images are not proxied, they load
  directly. A future "always show images from this
  sender" toggle is not in scope for Webmail
  Enterprise 2.
- **Attachment preview**: the server enforces an
  allowlist of safe content types
  (`image/png`, `image/jpeg`, `image/gif`,
  `image/webp`, `text/plain`) and a 1 MB cap. SVG is
  refused even when the client asks for it.

## 7. Cookies and storage

The webmail client never reads or writes any
storage. The greps `TestWebmailNoLocalStorageInWebmailAssets`
and `TestWebmailNoQueueAPICallsInWebmailAsset` are
the regression guards.

## 8. Content Security Policy

The middleware `securityHeaders()` in
`internal/api/router.go` sets a strict CSP on every
response:

```
default-src 'self';
script-src 'self';
style-src 'self';
img-src 'self' data: https:;
font-src 'self';
connect-src 'self' https:;
frame-src 'none';
object-src 'none';
base-uri 'self';
form-action 'self'
```

- `script-src 'self'` blocks inline scripts and
  remote script hosts. The webmail client is a
  vanilla-JS bundle; there is no inline `<script>`.
- `style-src 'self'` is consistent — the stylesheet
  is a single bundle under `/assets/`.
- `frame-src 'none'` blocks iframes entirely; a
  malicious HTML email cannot embed the user's
  authenticated session in a frame.
- `object-src 'none'` blocks `<object>` and
  `<embed>` which were the second-most-common XSS
  vector after `<script>`.
- HSTS is set on HTTPS responses with
  `max-age=31536000; includeSubDomains` so the
  browser refuses to downgrade to HTTP for any
  subdomain of the cookie domain.

## 9. Audit / logging

The webmail handlers do not log:

- the access token / refresh token value
- the CSRF token value
- the request body's `password` field
- the request body's full `body` / `rfc822` content
- the cookie header

The handler log lines (when emitted) carry the
mailbox id, the message id, and the action — the
minimum needed to investigate a support ticket.
The `h.logger.Warn` calls in the new code paths
(e.g. attachment count failure) carry only
operational context (mailbox id, message id,
error string), never message content.

## 10. Out of scope (deliberate)

- **Outgoing attachments**. The webmail send endpoint
  is text-only. Adding real outgoing attachments
  requires multipart parsing, an upload endpoint,
  per-attachment ownership through the queue, and a
  fresh security review. The current compose modal
  does not show an "Attach" affordance and the
  forward banner explicitly tells the user that
  attachments are not forwarded yet. This is the
  conservative default and matches the user's brief.
- **HTML compose**. The send endpoint builds a
  `text/plain` RFC822 unconditionally. The brief
  explicitly forbade HTML compose in this pack.
- **WebSocket / SSE push**. Real-time notifications
  are not in scope. The list refreshes on user
  action (refresh button, folder switch) and the
  poll cadence is dictated by how often the user
  clicks.
- **Conversation / thread view**. `Message.ThreadID`
  exists in the schema but is never populated.
- **End-to-end encryption**. Not in scope.
