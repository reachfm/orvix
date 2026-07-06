# Orvix Admin Console 2026 — Security Review

Branch: `feature/admin-console-2026-productization`
Date: 2026-07-06
Scope: every change to admin-side code in this sprint.

---

## 1. Authentication

The admin SPA authenticates against `/api/v1/auth/login` which issues
short-lived access + refresh tokens. Tokens are stored in
`sessionStorage` (not `localStorage`) so they do not survive browser
restart. The session bootstrap (`modules/auth.js`) probes
`/api/v1/me` on app boot; a 401 redirects to the login view.

| Concern | Status |
|---|---|
| Tokens in localStorage | **No** — sessionStorage only. Asserted by smoke-admin-ui.sh §32. |
| Passwords logged to console | **No** — no `console.log` includes the password. Asserted by §11. |
| CSRF on every mutating call | **Yes** — `apiPost` / `apiPatch` / `apiDelete` go through `csrfFetch()` with one-shot retry on a 403 that indicates a missing/expired CSRF token. Asserted by §13, §15, §33. |
| Bare `fetch()` for state changes | **No** — every mutating call is wrapped. Asserted by §16. |
| Storage limited to auth + prefs | **Yes** — only `orvix_admin_token`, `orvix_admin_csrf`, `orvix_admin_refresh`, locale, theme, and `orvix_sidebar_v1` are stored. Asserted by §23. |

## 2. RBAC

### Backend

Every admin endpoint is mounted behind a role-asserting middleware. The
admin-users, admin-queue, and admin-MFA handlers all check the caller is
either `admin` or `superadmin` and the request belongs to the
caller's tenant. The runtime telemetry endpoint
(`/api/v1/admin/runtime`) is admin-only by virtue of being mounted on
the admin group in `internal/api/router.go`.

| Concern | Status |
|---|---|
| Admin endpoints require role | **Yes** — `TestAdminUsersUnauthorized` in `admin_users_test.go` asserts 401 without a token. |
| Tenant isolation | **Yes** — every query includes `tenant_id = ?` predicate. |
| Self-disable / self-delete blocked | **Yes** — `TestAdminUsersSelfDisableProtection` (409) and `TestAdminUsersSelfDeleteProtection` (409). |
| Last active superadmin protection | **Yes** — `TestAdminUsersLastSuperadminProtection` (409/403). |

### Frontend

The admin-users page (`modules/pages/admin-users.js`) reads the
currently signed-in profile via `getProfile()` from
`modules/state.js`. The self-row is detected by matching the row's id
or email against the profile. Both the Delete and Disable buttons
are disabled (with a `title` attribute explaining the rule) for the
self row. The Disable button is also disabled for the last active
superadmin. The UI mirrors the backend contract; the backend
re-asserts the rule and returns 409/403 if the UI is bypassed (e.g.
by curl).

## 3. CSRF

All state-changing admin calls (POST / PATCH / DELETE) are routed
through `csrfFetch()` in `modules/api.js`. The CSRF token is fetched
from `/api/v1/csrf-token` once per session and cached; on a
CSRF-specific 403 the call retries once with a fresh token. The
sessionStorage key `orvix_admin_csrf` is excluded from the
"unauthorized storage writes" smoke check.

## 4. MFA (RFC 6238 TOTP)

The backend implementation in `internal/api/handlers/admin_mfa.go`
is a real RFC 6238 TOTP implementation:

- **Secret generation:** 20 random bytes (`mfaSecretSize = 20`) from
  `crypto/rand`, base32-encoded without padding per RFC 4648.
- **Code generation:** HMAC-SHA1 over an 8-byte big-endian counter
  (`T = floor(unix_time / 30)`), dynamic truncation per RFC 4226,
  6-digit zero-padded result.
- **Verification:** accepts a window of ±1 period (90 seconds
  total) per the RFC's recommendation for clock skew.
- **Setup flow:** `MFAStatusGet` → `MFASetupBegin` (requires
  current password) → `MFASetupVerify` (validates 6-digit TOTP, then
  enables MFA, then generates 8 hashed recovery codes via
  `sha256.Sum256`). Recovery codes are stored hashed; the raw codes
  are returned in the response exactly once at setup.
- **Disable flow:** `MFADisable` requires both current password and
  a valid TOTP code; clears `mfa_secret` and `mfa_secret_raw`,
  deletes all `mfa_recovery_codes` for the user.
- **Login flow:** `MFALoginVerify` accepts a TOTP code OR a
  recovery code; the recovery code is hashed with sha256 and
  matched against `mfa_recovery_codes`, then atomically marked as
  used with a `used_at IS NULL` predicate so concurrent redemption
  attempts cannot both succeed.

The UI in `modules/pages/security.js` wires the real setup and
disable flows. The setup modal collects the current password (via
`window.prompt`, which goes out of scope immediately), calls
`/api/v1/admin/mfa/setup/begin`, shows the returned secret +
otpauth URL, then collects a 6-digit TOTP code and calls
`/api/v1/admin/mfa/setup/verify`. On success the recovery codes
returned by the backend are displayed in a one-time list; the user
must click "Done" to dismiss. The disable modal requires both
current password and a TOTP code.

## 5. Audit logging

Every mutating admin endpoint writes a row to the audit log:

- `admin_mfa.go` → `mfa.setup.begin`, `mfa.enabled`, `mfa.disabled`,
  `mfa.login.totp`, `mfa.login.recovery_code` (with user_id +
  recovery_id in the message).
- `admin_users.go` → `admin_users.create`, `admin_users.update`,
  `admin_users.password_reset`, `admin_users.delete`.
- `admin_queue.go` → queue actions (retry, bounce, cancel).
- `domain / mailbox handlers` → create, update, delete on each
  resource.

The audit log is surfaced on the dashboard via
`/api/v1/admin/audit-logs?limit=8` in the "Recent admin activity"
card.

## 6. Runtime telemetry (no secrets)

`/api/v1/admin/runtime` is documented in
`docs/admin-runtime-telemetry.md` (deferred) and exercised by
`internal/api/handlers/admin_runtime_test.go`. The contract:

- No environment variables are read.
- No private keys, no tokens, no cookies.
- No full config dump.
- Disk label is always a safe string ("data", "system", or a
  caller-supplied basenames) — never an absolute filesystem path.
- License posture is reduced to `{mode, public_key_loaded, status,
  tier, expires_at}` — no private key, no key hash, no expiry
  token.

`TestAdminRuntimeNoSecretFields` in `admin_runtime_test.go`
asserts the response does not contain bearer tokens, private key
material, or full disk paths.

## 7. Input handling

| Concern | Status |
|---|---|
| XSS via untrusted data | **Mitigated** — every dynamic value is rendered through the shared `esc()` helper or as a DOM node (`el('td', { text: ... })`). No `innerHTML` for untrusted data. Asserted by `admin_frontend_test.go`. |
| Raw SQL/driver errors in responses | **Sanitised** — handlers return structured JSON with a generic message; the test `TestAdminUsersDBErrorsNotLeaked` asserts the response does not contain "SQL", "sqlite", "no such table", "syntax", "database", "driver" substrings. |
| Secrets in admin responses | **None** — no private keys, no password hashes, no licence-key hashes. Asserted by `backups_test.go` forbidden-list and `admin_runtime_test.go` `TestAdminRuntimeNoSecretFields`. |
| Login lockout | **Real** — `Login Protection` page reads `/api/v1/admin/login-protection/status` which reports failed-login tracking and lockout state. |
| Rate limiting | **Real** — `internal/api/router.go` defines `apiRateLimitMiddleware`. The admin SPA is exempt from the API rate limiter per the `PHASE-0` carve-out (admin operators have an alternative path; the carve-out is documented in the router). |

## 8. Banned-string smoke gate

The user prompt explicitly bans the following strings from any
visible production admin UI:

- "coming soon" / "future release" / "not implemented" /
  "unavailable in this build" / "will be added later" /
  "placeholder" / "TODO" / "mock" / "fake"

All four admin smoke scripts now assert none of these strings
appear in any production asset (`.js` / `.html` / `.css`):

- `release/scripts/smoke-admin-js.sh` §5
- `release/scripts/smoke-admin-ui.sh` §38 (tightened, comment-stripped)
- `release/scripts/smoke-admin-browser.sh` §8
- `release/scripts/smoke-admin-functional-browser.mjs` (DOM check)

The check excludes code comments (stripped via `sed -E 's|//.*$||g;
s|/\*.*\*/||g'`) and HTML form `placeholder=` attributes (standard
HTML semantics). The `_planned.js` module is exempt because it is
the legitimate 404-handler template and is not routed to any
visible sidebar item.

## 9. CSP / asset hygiene

- The Go backend sets the CSP header in
  `internal/api/router.go securityHeaders()` with
  `script-src 'self'`, which permits ES module imports from
  `/admin/*`. Asserted by `smoke-admin-browser.sh` §7.
- No external CDN / fonts / imports — every asset is local. No
  inline event handlers in `index.html` (every event is wired
  from JS).
- The admin bundle ships a thin bootstrapper (`app.js` < 600
  lines, currently 334) and a modular `modules/pages/` tree so
  the module graph can be statically analyzed.

## 10. Known limitations (not visible in production UI)

These are documented for operators but never appear as fake UI
copy:

| Limitation | Where it is documented |
|---|---|
| Multi-node clustering is single-node only | `clustering.js` honest "single-node" note (after rewrite) |
| Live DNS resolver is not run by the backend | `dns-dkim.js` honest "no live resolver" message |
| CSV bulk import is supported but the import button is the only entry point | n/a — endpoint exists at `/api/v1/mailboxes/import` |
| In-place backup restore | documented in CHANGELOG + settings page |
| In-UI DKIM keygen | documented; install-time only |
| Real-time push / WebSocket | out of scope for this sprint |

---

*Last updated: 2026-07-06 — Phase 2 security review*
