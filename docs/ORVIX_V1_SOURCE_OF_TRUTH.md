# Orvix V1 Source of Truth

## Purpose

This document is the single authoritative project-state record for Orvix V1.
GitHub (`reachfm/orvix` main branch) plus this document together constitute the
permanent source of truth. No project status may depend on human memory, chat
history, or agent memory.

## Last Updated

2026-07-18T06:00:00Z

## Authoritative Main Commit

```
e7f5441c25732b7583f3e57053f1e4d79f417fc5
```

## Latest Merged PR

**PR #27:** `security: harden admin auth and SMTP boundaries`
- Authoritative squash-merge commit: `e7f5441`
- Merged into `main`: 2026-07-18

## Current Repository State

| Property | Value |
|----------|-------|
| Main branch | `main` |
| Main HEAD | `e7f5441c25732b7583f3e57053f1e4d79f417fc5` |
| Last merged PR | #27 |
| Open PRs | None from the security sprint |
| Deployed to production | **NO** |
| Deployed to staging | **NOT VERIFIED** |
| Current release tag | v1.0.3-rc5 (pre-exists #27) |
| V1 release tag | NOT YET CREATED |

## Completed Security Work (PR #27)

All of the following is merged into `main`:

1. **Centralized Argon2id password hashing** — `internal/auth/password.go`
   establishes Argon2id as the canonical password format with `HashPassword`
   and `VerifyPassword` functions.

2. **bcrypt compatibility and rehash-on-login** — `internal/auth/auth.go`
   adds `VerifyPasswordWithRehash()`. Both `Handler.Login` and
   `Handler.WebmailLogin` perform conditional `UPDATE` after bcrypt
   verification to upgrade stored hashes to Argon2id.

3. **SMTP authentication only after TLS** — `internal/auth/smtp_tls.go`
   provides shared `DialSMTPWithTLS` helper enforcing STARTTLS or implicit
   TLS before `smtp.PlainAuth`. No plaintext credential fallback.

4. **Alert SMTP TLS hardening** — `internal/auth/alerts.go` uses
   `DialSMTPWithTLS()` instead of unprotected `smtp.SendMail`.

5. **RBAC corrections** — `internal/auth/rbac/rbac.go` defines canonical
   role permission maps (PlatformSuperAdmin, TenantAdmin).
   `internal/enterprise/rbac/evaluator.go` adds `IsPlatformRole`,
   `IsTenantRole`, `CanManageTenant`, `CanAccessDomain`.

6. **Tenant isolation tests** — `internal/api/handlers/tenant_isolation_matrix_test.go`
   contains 12 tests covering PlatformSuperAdmin access, TenantAdmin
   cross-tenant IDOR, body/query/path override prevention, and
   unauthenticated/unknown-role rejection.

7. **Legacy adminapi quarantine** — All 5 files in `internal/adminapi/`
   now carry `//go:build legacy_adminapi`. Default `go test ./...` no
   longer compiles or tests adminapi.

8. **JMAP default-test decoupling** — `internal/coremail/jmap/rc1_integrated_test.go`
   no longer imports adminapi. A test-only harness
   `rc1_admin_test_harness_test.go` provides equivalent admin coverage.

9. **PostgreSQL installer-test environment isolation** — Config harness tests
   in `internal/config/installer_test.go` explicitly set `ORVIX_DB_DRIVER=sqlite`
   to prevent CI environment leakage from `postgres-readiness.yml`.

## Current Production Deployment Status

| Property | Status |
|----------|--------|
| Production deployed SHA | NOT VERIFIED |
| Production deployed version | NOT VERIFIED |
| Commit e7f5441 deployed | **NO** |
| Backed up before merge | NOT VERIFIED |
| Rollback reference | NOT VERIFIED |
| Migration applied | NOT VERIFIED |

**No deployment occurred as part of PR #27 or any prior security sprint step.**

## Known Unresolved Product Issues

These are confirmed issues existing on `main` (commit `e7f5441`):

1. **TenantAdmin Fiber Admin route authorization** — The Fiber Admin API
   route guard may not correctly enforce canonical TenantAdmin role
   permissions on all admin endpoints. Needs verification.

2. **Duplicate messages on self-send** — Sending a message to one's own
   address can produce duplicate deliveries. Root cause not yet
   identified.

3. **Inbound/local-delivery classification in Outbound Queue UI** —
   Messages delivered locally may appear incorrectly classified in the
   Outbound Queue admin UI.

4. **Web Push / VAPID end-to-end** — Web Push subscription and VAPID
   delivery end-to-end verification is incomplete.

5. **Argon2id auth_scheme consistency** — Codes use `$argon2id$` and
   `argon2id` in different places. Uniformity needs verification.

6. **Test timing sensitivity** — `TestMetadataArgs_FastExit` fails under
   Windows filesystem timing (~65s vs expected <10s). Pre-existing.

## Known Unresolved Engineering Findings

1. **gosec informational findings** — Multiple gosec findings exist.
   Need triage: fix, suppress with documented justification, or accept.

2. **Race detection not run on Linux** — CGO-enabled race detection
   (`go test -race`) has not been run on the security sprint changes
   under Linux. No races known on Windows.

3. **Full Linux CI for security packages** — All security packages pass
   on Linux CI. No failures attributed to the security sprint.

## Release-Gate Requirements

The following gates must pass before Orvix V1 is released:

| Gate | Status |
|------|--------|
| All CI workflows green | PASS (post-PR #27) |
| PostgreSQL Readiness | PASS |
| Installer validation | NOT VERIFIED |
| Upgrade validation | NOT VERIFIED |
| Backup validation | NOT VERIFIED |
| Restore validation | NOT VERIFIED |
| Doctor validation | NOT VERIFIED |
| Staging deployment validation | NOT VERIFIED |
| Closed beta | NOT STARTED |
| Production approval | NOT GRANTED |

## Next Exact Task

1. Create GitHub Issues for all known unresolved product issues (see
   `docs/ORVIX_V1_RELEASE_CHECKLIST.md`).

2. Verify TenantAdmin Fiber Admin route authorization.

3. Complete Web Push and VAPID verification.

4. Run final installer, upgrade, backup, restore, and doctor gates.

5. Deploy to staging.

6. Run closed beta.

7. Deploy to production with backup and rollback approval.

## Links to Tracking GitHub Issues

- Master tracking issue: **TO BE CREATED** (Orvix V1 Release Gate)
- Individual issues: See `docs/ORVIX_V1_RELEASE_CHECKLIST.md`

## Superseded Historical Documents

The following documents are historical evidence of prior work. They are
not current project status and must not be used as project-state
authority:

| Document | Status |
|----------|--------|
| `docs/ORVIX_MANDATORY_TRUTHFUL_REPORT.md` | SUPERSEDED (historical) |
| `docs/ORVIX_CLEAN_BRANCH_DELIVERY_REPORT.md` | SUPERSEDED (historical) |
| `docs/ORVIX_POSTGRESQL_READINESS_FAILURE_REPORT.md` | SUPERSEDED (historical) |
| `docs/ORVIX_SECURITY_HARDENING_DAY1_REPORT.md` | SUPERSEDED (historical) |
| `docs/ORVIX_FULL_PROJECT_AUDIT.md` | SUPERSEDED (historical) |
| `docs/ORVIX_MASTER_EXECUTION_LEDGER.md` | SUPERSEDED (historical) |
| `docs/CTO_AUDIT_2026-07-11.md` | SUPERSEDED (historical) |
| `docs/SECURITY_AUDIT_RC_2026-07-11.md` | SUPERSEDED (historical) |
| `docs/RELEASE_HARDENING_2026-07-11.md` | SUPERSEDED (historical) |

## Rules for Future Updates

1. This document is the authoritative project-state record.
2. Any merge to `main` must update the Authoritative Main Commit field.
3. Any deployment must update `docs/ORVIX_DEPLOYMENT_STATE.md`.
4. Any completed task must be checked off in `docs/ORVIX_V1_RELEASE_CHECKLIST.md`.
5. Historical reports must not be recycled as current-state documents.
6. No field in this document may depend on human memory or chat history.
7. The GitHub Issues tracker is the canonical task list.
