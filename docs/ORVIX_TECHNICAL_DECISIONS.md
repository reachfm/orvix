# Orvix Technical Decisions

Recorded: 2026-07-18

Context: PR #27 (`security: harden admin auth and SMTP boundaries`),
squash-merged into `main` at `e7f5441`.

---

## Decision 1: Argon2id as Canonical Password Hashing

**Date:** 2026-07
**PR:** #27

Argon2id is the canonical user password-hashing format for Orvix V1.
All new passwords are hashed with Argon2id using calibrated memory,
iterations, and parallelism parameters.

## Decision 2: bcrypt Migration on Login

**Date:** 2026-07
**PR:** #27

Existing bcrypt password hashes are not bulk-migrated. Instead, each
hash is upgraded to Argon2id on next successful login (rehash-on-login
pattern). This avoids a disruptive forced password reset while
gradually migrating the entire user base.

## Decision 3: SMTP TLS Before Authentication

**Date:** 2026-07
**PR:** #27

SMTP authentication (PlainAuth) is forbidden before verified TLS.
`DialSMTPWithTLS` enforces STARTTLS or implicit TLS before any
credential exchange. No plaintext credential fallback is allowed
anywhere in the codebase.

## Decision 4: Legacy adminapi Excluded from Default Builds

**Date:** 2026-07
**PR:** #27

All 5 files in `internal/adminapi/` use `//go:build legacy_adminapi`.
The canonical production Admin API is the Fiber Admin API. The legacy
adminapi remains available for reference and tagged testing only.

Build to include adminapi:
```
go build -tags legacy_adminapi ./...
```

## Decision 5: Fiber Admin API as Canonical Production API

**Date:** 2026-07

The Fiber Admin API (`internal/api/router.go`) is the authoritative
production admin interface. All RBAC enforcement, tenant isolation,
and admin operations are implemented through this API.

## Decision 6: Tenant Identity from Server-Side Principal

**Date:** 2026-07
**PR:** #27

Tenant identity must derive from the authenticated server-side
principal (JWT/session), never from a client-supplied body, query,
or path parameter. `internal/api/handlers/tenant_isolation_matrix_test.go`
validates this property exhaustively.

## Decision 7: No Plaintext SMTP Credential Fallback

**Date:** 2026-07
**PR:** #27

No code path in Orvix V1 is permitted to send SMTP credentials over an
unencrypted connection. `DialSMTPWithTLS` guarantees this invariant.
Tests in `internal/auth/smtp_tls_test.go` verify STARTTLS enforcement,
certificate rejection, and no-plaintext-fallback behavior.

## Decision 8: GitHub main + Source of Truth as Authoritative

**Date:** 2026-07-18

The GitHub `main` branch plus `docs/ORVIX_V1_SOURCE_OF_TRUTH.md`
together constitute the permanent source of truth for Orvix V1 project
state. No project status may depend on human memory, chat history, or
agent memory.

## Decision 9: Historical Reports Are Evidence, Not Current State

**Date:** 2026-07-18

Previous execution reports (audit, security, hardening) are preserved
as historical evidence of completed work. They must not be used as
current project-state authority. Each superseded report carries a
banner referencing the Source of Truth document.

## Decision 10: Production Deployment Requires Backup and Rollback Approval

**Date:** 2026-07-18

No production deployment may occur without:
1. A verified backup of the running system.
2. A documented and tested rollback procedure.
3. Explicit deployment approval recorded in
   `docs/ORVIX_DEPLOYMENT_STATE.md`.
