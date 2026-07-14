# Orvix Release Hardening — Work Log 2026-07-11

Companion to `docs/CTO_AUDIT_2026-07-11.md`. This records the fixes made in
the overnight hardening pass toward Enterprise v1. **Nothing here has been
pushed, merged, or deployed** — every change is an uncommitted working-tree
change on its own local branch off `origin/main` (tip `903fa7b` = PR #10),
awaiting owner review.

Authoritative verification for every branch: `go build ./...` (pass),
`go vet ./...` (pass), and the full sequential suite
`go test -p 1 -count=1 ./... -timeout=1200s` → **69 packages, 0 failures**.
Each branch was verified in isolation (concurrent runs caused false
timing/DB-deadline failures under load; those were re-run alone and passed).

## Branches (recommended merge order)

### 1. `fix/tenant-isolation-mailbox-domain` — CRITICAL
Closes a cross-tenant IDOR: 13 mailbox/domain admin handlers scoped by
numeric id only, never `tenant_id`. Any tenant admin could reset the
password of, or delete, another tenant's mailbox by guessing an id. This
was the load-bearing blocker for the multi-tenant SaaS model.
- Added `callerOwnsTenant` helper (404 on mismatch; super-admin may cross).
- Applied to Get/Update(Password/Status/Quota/Protocols)/Delete Mailbox,
  Bulk Mailbox/Domain Status, Get/Update/Delete Domain, and the audit reads.
- Wired the previously-dead `auth.TenantMiddleware` into the protected
  router group (it was defined but never registered, so `tenant_id` was
  never available to check).
- Fixed a latent panic that surfaced when the middleware first ran: the
  custom modernc-sqlite GORM dialector breaks `Raw().Scan()` into a scalar
  — switched to the raw `*sql.DB` pattern used elsewhere.
- Fixed `CreateDomain`, which hard-coded `tenant_id = 0` for every
  API-created domain (harmless before tenant checks existed; orphaned every
  new domain once they did).
- Added `internal/api/handlers/tenant_isolation_test.go` — 12 regression
  tests over real HTTP + login + CSRF, incl. a same-tenant sanity check.
- Files: 4 changed, +130 / -39.

### 2. `fix/security-dep-bumps` — CRITICAL (real CVE)
`govulncheck` found 7 reachable CVEs on `main`, incl. **GO-2026-5004, a
SQL-injection in `github.com/jackc/pgx/v5` v5.6.0** (the shipped Postgres
driver), reached via `ExportDomainsCSV`.
- `pgx` v5.6.0 → v5.9.2, `golang.org/x/net` v0.54.0 → v0.55.0, Go toolchain
  1.25.0 → 1.25.12 (the latter clears 5 stdlib CVEs in crypto/tls,
  crypto/x509, net/textproto, net/mail).
- Post-fix `govulncheck ./...` → **"No vulnerabilities found"** in called
  code (exit 0).
- Files: go.mod / go.sum, +11 / -15. Note: bumping the `go` directive to
  1.25.12 means CI/build hosts need Go ≥ 1.25.12 (GOTOOLCHAIN=auto fetches
  it automatically, as it did here).

### 3. `fix/postgres-metadata-backup` — HIGH
`internal/backup/service.go`'s `snapshotDB` only ran SQLite `VACUUM INTO`,
which is invalid syntax against a PostgreSQL connection — so on a Postgres
deployment **every backup failed outright** while the metadata DB (users,
tenants, licenses, auth) went unbacked-up.
- Added a dialect branch that shells out to `pg_dump -Fc` for Postgres;
  manifest records `DatabaseFormat` (sqlite vs postgres-custom) so a
  restore operator knows the format.
- Fails loudly (no silent/empty backup) if `pg_dump` is missing or the DSN
  is unset.
- Added `internal/backup/postgres_snapshot_test.go` (fail-path coverage;
  the pg_dump success path runs in CI where postgresql-client exists).
- Files: 3 changed, +75 / -4.

### 4. `fix/csrf-global-enforcement` — HIGH
CSRF was opt-in per route; several state-changing admin routes (migration
start, domain provisioning, compliance policies, collaboration mailboxes)
were mounted with no CSRF check and were forgeable (session cookie sent
automatically).
- Moved `csrf.Middleware()` onto the whole `admin` group (deny-by-default).
- Added an API-key bypass in the CSRF middleware (Bearer-token requests
  aren't CSRF targets and were never issued a CSRF cookie) so API
  integrations don't break under the stricter default.
- Added router-level coverage tests + a CSRF-manager unit test.
- Files: 2 changed, +25 / -2.

### 5. `fix/mfa-secret-encryption` — MEDIUM
MFA TOTP secrets were stored reversibly (base64) despite a comment claiming
they were hashed; DB-only compromise yielded working codes.
- Now encrypted at rest with AES-256-GCM via the existing `config.Encrypt`.
- Added a regression test asserting the stored value is genuine ciphertext
  (not plain base64) and round-trips via `config.Decrypt`.
- Files: 2 changed, +95 / -10.

### 6. `chore/add-security-scanning-ci` — MEDIUM
No security/vuln scanning existed in CI.
- Added `.github/workflows/security-scan.yml`: **govulncheck as a blocking
  gate** (high signal, real CVEs) and **gosec as informational**
  (`continue-on-error`).
- Rationale documented in-file: gosec's medium+ output here is dominated by
  correct-by-design patterns (opportunistic TLS in SMTP delivery per RFC
  7435, HMAC-SHA1 required by TOTP, math/rand for jitter) that must not be
  "fixed"; a blocking gosec gate would reject correct code.
- Depends on branch #2 landing first (else the govulncheck gate is red).

### 7. `chore/remove-dead-dkim-verify` — LOW (landmine cleanup)
Deleted `dkim.Signer.Verify()`, dead code that returned `true` without
checking the RSA signature — unused today, dangerous if wired in later. The
real verification path in `verifier.go` is untouched.
- Files: 1 changed, -49.

## gosec triage (informational — NOT bugs)
The following gosec HIGH findings are correct-by-design and must not be
changed (doing so breaks the product): TLS `InsecureSkipVerify` in outbound
SMTP delivery (`delivery/transport.go` — opportunistic TLS, RFC 7435);
`math/rand` for retry jitter (`delivery/retry.go`); HMAC-SHA1 in MFA
(`admin_mfa.go` — required by RFC 6238 TOTP). Genuinely worth a later look
(minor): default cache secret in `licensingauthority/service.go:16` (has an
env override), and SSH host-key skip in `backup/targets/uploader.go:509`.

## Recommended path to deploy (owner-driven)
1. Review each branch's diff.
2. Commit each branch and open PRs in the order above (2 before 6).
3. Merge to `main` via the normal PR flow.
4. Update the VPS from the merged `main` using `release/upgrade.sh` (it
   pre-flight-backs-up and auto-rolls-back on failure) — owner runs this.
5. Live-test on the VPS: admin login, create a second tenant + mailbox,
   confirm cross-tenant access is now blocked, run a backup and confirm the
   Postgres dump is present, verify MFA enroll/login.

## Not done tonight (tracked, not started)
Still open from the CTO audit toward v1: IMAP APPEND/SEARCH/IDLE (client
compat), real full-text search (Phase 8), DSN/bounce generation, DMARC hard
enforcement, SQLite single-connection load test, installer TLS/firewall
automation, audit-log tamper-evidence.
