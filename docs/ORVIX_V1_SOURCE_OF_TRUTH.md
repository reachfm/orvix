# Orvix V1 Source of Truth

## Purpose

This document is the single authoritative project-state record for Orvix V1.
GitHub (`reachfm/orvix` main branch) plus this document together constitute the
permanent source of truth. No project status may depend on human memory, chat
history, or agent memory.

## Last Updated

2026-07-18

## A. Authoritative Repository Reference

- **Dynamic authority:** `origin/main`
- The exact current SHA of `origin/main` must be resolved from GitHub or with:
  ```
  git fetch origin
  git rev-parse origin/main
  ```
- This document must not require a new commit merely because unrelated commits advance `main`.

## B. Last State-Synchronization Merge

- **PR #37** — `docs: establish Orvix V1 source of truth`
- Merge commit: `bd91d15a8fde4cdddc14b94aaf7c738ba6616a31`
- Merged into `main`: 2026-07-18
- Current `main` must contain this commit or a descendant.
- Verification:
  ```
  git merge-base --is-ancestor \
    bd91d15a8fde4cdddc14b94aaf7c738ba6616a31 \
    origin/main
  ```

## C. Security Baseline Merge

- **PR #27** — `security: harden admin auth and SMTP boundaries`
- Merge commit: `e7f5441c25732b7583f3e57053f1e4d79f417fc5`
- Merged into `main`: 2026-07-18

## D. Repository State

| Property | Value |
|----------|-------|
| Main authority | `origin/main` |
| Exact Main HEAD | Resolve dynamically from GitHub |
| Last state-sync PR | #37 |
| Last state-sync merge | `bd91d15...` |
| Security baseline PR | #27 |
| Security baseline merge | `e7f5441...` |
| Production deployed SHA | NOT VERIFIED |
| PR #27 deployed | NO |
| PR #37 deployed | NO |
| V1 public release approved | NO |

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
| Commit `e7f5441` (security baseline) deployed | **NO** |
| Commit `bd91d15` (state-sync merge) deployed | **NO** |
| Backed up before merge | NOT VERIFIED |
| Rollback reference | NOT VERIFIED |
| Migration applied | NOT VERIFIED |

**No deployment has occurred. Neither PR #27 nor PR #37 has been deployed to any environment.**

## Known Unresolved Product Issues

These are confirmed issues existing on `origin/main` (the authoritative repository reference):

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

7. **Production links pointing to orvix.com** — Confirmed: the Sign in
   button on `https://orvix.email/api` targets `https://app.orvix.com/login`
   instead of the canonical `orvix.email` domain. Multiple CTA and link
   paths are affected across marketing, webmail, admin, and API pages.
   GitHub Issue #39.

8. **Admin portal entry route serves marketing homepage** — Confirmed:
   navigating to the Admin portal URL renders the public marketing
   homepage instead of the Admin application or login page. The SPA
   fallback or reverse-proxy routing is capturing Admin paths and
   redirecting to the marketing site. GitHub Issue #38.

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

1. Verify TenantAdmin Fiber Admin route authorization.

2. Complete Web Push and VAPID verification.

3. Run final installer, upgrade, backup, restore, and doctor gates.

4. Deploy to staging.

5. Run closed beta.

6. Deploy to production with backup and rollback approval.

## Links to Tracking GitHub Issues

- Master tracking issue: [#36](https://github.com/reachfm/orvix/issues/36) — Orvix V1 Release Gate
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
2. Update this document when project state, decisions, deployment state,
   blockers, or release status changes.
3. Do not update it merely because an unrelated commit advances `main`.
4. Exact live Main HEAD is read from `origin/main`, not hard-coded.
5. Exact deployed SHA must be recorded only after verified deployment.
6. Historical merge SHAs remain immutable evidence of past events.
7. Any deployment must update `docs/ORVIX_DEPLOYMENT_STATE.md`.
8. Any completed task must be checked off in `docs/ORVIX_V1_RELEASE_CHECKLIST.md`.
9. Historical reports must not be recycled as current-state documents.
10. No field in this document may depend on human memory or chat history.
11. The GitHub Issues tracker is the canonical task list.
