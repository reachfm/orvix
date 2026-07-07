# Orvix Admin Console 2026 — Security Review

Branch: `feature/admin-final-domain-settings-polish`
Date: 2026-07-07
Scope: every change to admin-side code in this sprint.

## Phase 3 — admin-final-domain-settings-polish (this branch)

The Phase 3 sprint targets final production polish, not new
attack surface. The security posture is therefore identical to
Phase 2 except for the new domain write paths:

| Change | Security implication |
|---|---|
| `POST /api/v1/domains` accepts 12 enterprise fields | New validators: DKIM selector must be a DNS label (no spaces/slashes); catch-all address must be on the same domain and parse as email; abuse contact parses as email; limits must be non-negative; plan must be from a small enum. All validators return structured JSON `error` instead of raw DB errors. |
| `GET /api/v1/domains/:name` returns full shape | Read-only; new fields are safe primitives (booleans, ints, strings). Audit log is unchanged. |
| `PATCH /api/v1/domains/:name` (new) | RBAC (admin only), CSRF, allowlist of 11 mutable keys. Unknown key aborts the entire patch. Audit-logged. Test coverage: `TestAdminDomainPatchAllowedFields`, `TestAdminDomainPatchUnknownFieldHardReject`, `TestAdminDomainPatchRBACEnforced`. |
| Add Domain modal exposes advanced fields | No XSS risk: every value goes through `el('input', ..., { value: ... })` which uses `setAttribute` (escapes by default). |
| Settings page rewritten as runtime overview | Save controls only render for keys in the backend allowlist. No fake mutable form. |
| Security posture replaced `unknown` with explicit labels | Read-only re-presentation; no new sensitive data exposed. |

The new test file `internal/api/handlers/admin_domain_advanced_test.go`
covers the security guarantees on the new write paths:

- `TestAdminDomainPatchRBACEnforced` — non-admin JWT cannot
  patch a domain.
- `TestAdminDomainPatchUnknownFieldHardReject` — unknown keys
  cause the entire patch to be rolled back.
- `TestAdminDomainCreateBadDkimSelector` — DNS-label validator
  rejects spaces and slashes.
- `TestAdminDomainCreateInvalidCatchall` — cross-domain catch-all
  is rejected.
- `TestAdminDomainCreateNegativeLimit` — limits cannot wrap.

---

(Sections 1–10 below are unchanged from Phase 2 and are kept
verbatim so this document remains a complete security audit.)

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

*Last updated: 2026-07-07 — Phase 3 final polish (domain create/patch advanced fields)*

---

## Phase 4 — admin-2026-control-panel (premium polish)

This sprint is a frontend productization pass on top of Phase 3.
The new attack surface is bounded: two new PATCH endpoints
(`/api/v1/admin/mailing-lists/:id` and
`/api/v1/admin/public-folders/:id`) plus the shared form builder
client-side.

### New write endpoints

| Endpoint | RBAC | CSRF | Audit | Allowlist | Notes |
|---|---|---|---|---|---|
| `PATCH /api/v1/admin/mailing-lists/:id` | admin | yes | `mailing_list.update` | 7 mutable keys: display_name, description, subscription_policy, status, moderation_required, archive_enabled, max_members | Unknown keys hard-reject. Subscription policy enum (open / closed / moderated / announce). Status enum (active / suspended / archived). Max members non-negative. Immutable: address, domain_id (changing would orphan subscribers and break DKIM alignment). |
| `PATCH /api/v1/admin/public-folders/:id` | admin | yes | `public_folder.update` | 3 mutable keys: display_name, description, read_only | Unknown keys hard-reject. read_only validates as bool. Immutable: owner_mailbox_id, folder_path (changing would orphan IMAP subscriptions). |

### New tests (security coverage)

- `internal/api/handlers/admin_domain_v2_test.go` (new):
  - `TestAdminMailingListPatchRBACEnforced` — non-admin JWT gets
    non-200 on PATCH.
  - `TestAdminMailingListPatchUnknownFieldHardReject` — unknown
    key aborts the entire PATCH; the valid field is not applied
    (atomic rollback verified via GET).
  - `TestAdminMailingListPatchAllowedFields` — every editable
    field persists via re-fetch.
  - `TestAdminPublicFolderPatchUnknownFieldHardReject`,
    `TestAdminPublicFolderPatchAllowedFields`,
    `TestAdminPublicFolderPatchReadOnlyValidator` — same shape
    for the public-folder endpoint; boolean validator on
    `read_only` rejects string input.

### Client-side new surface

- `release/admin/modules/form.js` — new shared form-builder
  module. Pages pass field-group definitions; the module renders
  the modal, validates per-field (required, email regex, URL
  regex, numeric range, custom `validate(value, values)`), and
  runs the submit handler. No DOM `innerHTML` is used for dynamic
  values (every value goes through `el()` → `setAttribute` /
  `textContent`). The submit handler is async and surfaces
  thrown errors in a form-level error banner without leaking
  raw backend internals to the operator (only the
  shape-normalised error message is rendered).
- All new modal pages (`admin-groups.js`, `acl.js`, `acceptance.js`,
  `incoming-rules.js`, `mailing-lists.js`, `public-folders.js`)
  use `openFormModal()` or `openFormDrawer()` exclusively. The
  legacy `confirm()` and `alert()` calls were removed in favour
  of the Promise-based `confirmDanger()` helper that supports a
  typed-confirmation gate for destructive operations.
- No new inline event handlers in `index.html`. No new
  localStorage / sessionStorage usage. Every dynamic value still
  routes through the shared `esc()` helper or `el()` text node.
- The functional browser smoke now opens every previously-empty
  modal and asserts each carries a non-trivial field count, so a
  regression cannot re-introduce "blank modal" UI without the
  guard firing.

### Open security contract guarantees carried forward

| Guarantee | Phase 4 status |
|---|---|
| Tokens in `sessionStorage` only | unchanged |
| Password never logged to console | unchanged |
| CSRF on every mutating admin call | unchanged (csrfFetch + one-shot retry) |
| Bare `fetch()` for state changes | unchanged (every PATCH goes through `apiPatch`) |
| Tenant isolation via JWT envelope | unchanged |
| Self-disable / self-delete blocked | unchanged |
| Last-superadmin protection | unchanged |
| Runtime telemetry never leaks bearer tokens / private keys | unchanged |
| Banned-string banned-placeholders grep | extended to cover the form-builder source path |
| No inline event handlers / `innerHTML` for untrusted data | unchanged |
| No mock enterprise claims / fake charts | added explicit "no unknown runtime data" + "no warnings = healthy" copy |

*Last updated: 2026-07-07 — Phase 4 premium polish (modal rebuilds + 2 new PATCH endpoints)*
