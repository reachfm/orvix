# Canonical role fixtures for test authors

## Purpose

Every handler test that needs an "admin" identity must go through one of the
canonical seed helpers in `internal/api/handlers/testhelpers_role_test.go`.
Raw `INSERT INTO users ... role='admin'` fixtures are the single largest
source of hidden platform-vs-tenant identity bugs, because `'admin'` is
ambiguous: the same literal can mean "platform superuser" or "tenant admin"
depending on what surrounding SQL the test happens to write. The helpers
force the author to pick one, and to bind the correct tenant scope, at the
call site.

This document tells test authors which helper to reach for.

## Helpers

Signatures (see `testhelpers_role_test.go` for full godoc):

```go
type SeededUser struct {
    ID       uint
    Email    string
    Password string // ephemeral; do not log
}

// Canonical roles.
func seedPlatformSuperAdmin(t *testing.T, db *sql.DB, email string) SeededUser
func seedTenantAdmin       (t *testing.T, db *sql.DB, email string, tenantID uint) SeededUser
func seedTenantOperator    (t *testing.T, db *sql.DB, email string, tenantID uint) SeededUser
func seedTenantSupport     (t *testing.T, db *sql.DB, email string, tenantID uint) SeededUser
func seedTenantReadOnly    (t *testing.T, db *sql.DB, email string, tenantID uint) SeededUser

// Pinned-password variants — use when the test's downstream login flow
// needs a specific plaintext (e.g. a shared helper hardcodes it).
func seedPlatformSuperAdminWithPassword(t *testing.T, db *sql.DB, email, password string) SeededUser
func seedTenantAdminWithPassword       (t *testing.T, db *sql.DB, email string, tenantID uint, password string) SeededUser

// Legacy — for migration/normalization tests only. Deliberately verbose
// so the intent is obvious at review time.
func seedLegacyAdminForMigrationTest(t *testing.T, db *sql.DB, email string, tenantID *uint) SeededUser
```

All helpers:

- Write the exact canonical role literal and the correct tenant binding.
- Return the seeded row's `ID`, `Email`, and the plaintext `Password` used
  (so a follow-up login flow in the same test can reuse it).
- Never log the password, cookie, token, or any auth header.

## Route → helper decision table

| Route family                                                     | Helper                          | Rationale                                                                                     |
| ---------------------------------------------------------------- | ------------------------------- | --------------------------------------------------------------------------------------------- |
| `POST/GET /admin/backups*`, `/admin/firewall*`, `/admin/license*`, `/admin/cluster*` | `seedPlatformSuperAdmin`        | Platform-only permissions in the rbac map (`license.write`, `backups.write`, ...).            |
| `/admin/queue*`, `/admin/settings*`, `/admin/mfa*`, `/admin/users*`, `/admin/mailing/public-folder*` | `seedPlatformSuperAdmin`        | Platform operations; the tests exercise the platform admin scope.                             |
| `/admin/domains/:id/*` (advanced tenant domain ops)              | `seedTenantAdmin`               | Tenant-scoped admin routes; require correct tenant binding.                                   |
| `/enterprise/*` (enterprise mutations, parity smoke)             | `seedTenantAdmin`               | Enterprise routes are tenant-scoped in the current router.                                    |
| Tenant isolation matrices (`tenant_isolation_*_test.go`)         | `seedTenantAdmin`               | Two admins in two different tenant_ids to exercise cross-tenant denial.                       |
| Mailbox / domain provisioning / domain-list isolation            | `seedTenantAdmin`               | Provisioning and listing routes are tenant-scoped.                                            |
| Webmail admin flows                                              | `seedTenantAdmin`               | Webmail admin is tenant-scoped.                                                               |
| `/queue/*` (top-level, not `/admin/queue`)                       | `seedPlatformSuperAdmin`        | Cross-tenant queue action is platform-scoped in the current rbac map.                         |
| Operator-specific assertions (queue actions, mailbox write only) | `seedTenantOperator`            | When the assertion is specifically that operator can (or cannot) do X.                        |
| Read-only assertions                                             | `seedTenantReadOnly`            | Only when the test's intent is specifically "read-only must be denied write".                 |
| Legacy role migration / normalization tests                      | `seedLegacyAdminForMigrationTest` | Only in tests that exercise the deprecated `role='admin'` normalization path.                 |

If a route does not appear in this table, look at the middleware chain in
`internal/api/router.go`: routes mounted under the `platform` or
`internalOps` groups use `seedPlatformSuperAdmin`; routes gated by a
`RequireTenantScope`-style middleware use `seedTenantAdmin`. When in
doubt, seed a `seedTenantAdmin` first and let the test fail with `403
insufficient permissions` — that failure tells you the route needs
`seedPlatformSuperAdmin` instead.

## Staging two-identity contract

The staging acceptance harness (`release/scripts/staging/*`) uses **two
distinct identities** by design:

- **Bootstrap identity** — created by `cmd/orvix/main.go`'s
  `seedAdminUser` when the service first starts. In the canonical
  migration this row carries `role='platform_super_admin'` with
  `tenant_id=NULL`. Use this identity only for platform-level operations
  (license, backup, cluster, cross-tenant admin).
- **Tenant admin identity** — provisioned by the staging setup script
  against a specific tenant. Carries `role='tenant_admin'` with
  `tenant_id > 0`. Use this identity for every tenant-scoped assertion
  (domain provisioning, mailbox creation, tenant settings, enterprise
  routes).

Sharing one identity across both scopes was the original bug this
migration exists to prevent: a `platform_super_admin` "happens to work"
against tenant routes only because the current tenant middleware falls
back to no-tenant behaviour for the platform role. Any change to that
middleware would silently break every test that relied on the shared
identity. The staging split makes those tests fail loudly instead.

## `seedLegacyAdminForMigrationTest` — intended usage

Only three legitimate uses exist:

1. **Migration path tests** — verifying that a `role='admin'` row is
   correctly normalised into a canonical role at read time
   (see `internal/auth/auth.go:NormalizeRole`).
2. **Denial regression tests** — locking in that the legacy literal does
   not silently regain a permission it must not hold (see
   `canonical_role_denial_test.go:TestLegacyAdminFixtureIsStrictlyLessThanPlatformSuperAdmin`).
3. **Legacy-row isolation tests** — verifying tenant-isolation still holds
   for a pre-migration admin row.

Any other use is a bug: reach for a canonical helper instead.

## Anti-patterns

Do not do any of the following. Each pattern hides a real permission bug
behind a passing test.

- **Raw `INSERT INTO users ... role='admin'`.** Forbidden outside the
  three whitelisted files (`testhelpers_role_test.go`,
  `canonical_role_gates_test.go`, `enterprise_parity_test.go`). Migrate
  to a helper.
- **`role='superadmin'` or `role='super_admin'`.** These are deprecated
  aliases; use `seedPlatformSuperAdmin`.
- **`seedPlatformSuperAdmin` for a tenant-scoped assertion.** The
  platform identity "works" against tenant routes only by accident of
  current middleware behaviour; the test's intent must match the route's
  scope.
- **`seedTenantAdmin(t, db, email, 0)`.** `tenant_id=0` is not a valid
  tenant. Use a real tenant id (usually 1) or, if the test genuinely
  requires the no-tenant edge case, use `seedLegacyAdminForMigrationTest`
  with a `nil` or `&zero` tenant pointer and comment why.
- **Reading the returned `Password` field into a log statement.** The
  helpers guarantee no auth-material logging; do not undo that in the
  caller.
- **Sharing a single seeded row across two tests.** Every test must seed
  its own row; helpers use email uniqueness (typically `t.Name()`) to
  keep parallel runs isolated.
