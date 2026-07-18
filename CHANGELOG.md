# Changelog

All notable changes to Orvix are documented in this file.

## [Unreleased]

### Security
- **Argon2id password migration** — New passwords are hashed with Argon2id.
  Existing bcrypt hashes are automatically upgraded to Argon2id on next
  successful login (rehash-on-login). (`internal/auth/password.go`)
- **TLS-secured SMTP authentication** — SMTP PlainAuth is forbidden before
  verified TLS. Shared `DialSMTPWithTLS` helper enforces STARTTLS or
  implicit TLS. No plaintext credential fallback. (`internal/auth/smtp_tls.go`)
- **Alert SMTP TLS hardening** — Alert notifications now use
  `DialSMTPWithTLS` instead of unprotected `smtp.SendMail`.
- **Tenant isolation hardening** — Platform and Tenant RBAC roles are
  enforced with canonical permission maps. Tenant identity derives from
  the authenticated server-side principal, never from client-supplied
  parameters. 12 tenant-isolation regression tests added.
  (`internal/auth/rbac/rbac.go`, `internal/enterprise/rbac/evaluator.go`,
  `internal/api/handlers/tenant_isolation_matrix_test.go`)

### Changed
- **Legacy adminapi quarantined** — All files in `internal/adminapi/` carry
  `//go:build legacy_adminapi`. Default builds exclude the legacy admin
  API. The canonical production API is the Fiber Admin API.
- **JMAP integration test decoupled** — JMAP default tests no longer depend
  on adminapi. A test-only harness provides equivalent admin coverage.
- **PostgreSQL installer-test environment isolation** — Config harness
  tests explicitly set `ORVIX_DB_DRIVER=sqlite` to prevent CI environment
  leakage.

### Fixed
- RBAC route guards now recognize `RolePlatformSuperAdmin`.
- `Handler.Login` and `Handler.WebmailLogin` now perform conditional
  password rehash after bcrypt verification.
- PostgreSQL Readiness CI gate repaired for config harness tests.

## [v1.0.3-rc5] - Prior Release

Pre-existing release candidate. See release notes for v1.0.3-rc5 for
details. PR #27 security changes are not included in this tag.
