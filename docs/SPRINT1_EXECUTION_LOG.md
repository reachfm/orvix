# Enterprise Security Sprint 1 — Execution Log

Branch: `security/sprint-1-rc1` (off `main` @ `903fa7b`).
Engineer: implementation. CTO: owner/approver.
Policy: commits on this branch only. No merge, no push, no deploy.

Verification gate applied to every finding: `gofmt` clean, `go vet ./...`,
`go build ./...`, `go test -p 1 -count=1 ./... -timeout=1200s` (authoritative
sequential suite — the project's canonical command; parallel runs cause SQLite
lock contention). Security regression = `govulncheck ./...`. PostgreSQL +
SQLite regression are both covered by the full suite (the suite exercises both
dialects). CI regression = the same commands the workflows run.

Fix order (CTO-mandated): C-1, C-2, C-3, H-4, H-5, H-6, H-8, H-9.

---

## C-1 — Cross-tenant IDOR on mailbox & domain administration

### Risk analysis (pre-implementation)
- **Risk:** Broken tenant isolation on the admin API. Authorization decisions
  used the resource's numeric id/name only, never the caller's tenant.
- **Attack scenario:** Tenant A's admin authenticates normally, then calls
  `PATCH /api/v1/mailboxes/{id}/password` (or `DELETE /api/v1/mailboxes/{id}`,
  `DELETE /api/v1/domains/{name}`, etc.) with an id belonging to Tenant B.
  Server applies the mutation because it never checks ownership → Tenant A
  resets Tenant B's mailbox password and reads/hijacks the mailbox, or deletes
  Tenant B's mailboxes/domains. IDs are sequential, so enumeration is trivial.
- **Business impact:** Complete failure of the multi-tenant isolation guarantee
  — cross-tenant account takeover and data destruction. Contractual/GDPR breach;
  disqualifies the product for any multi-tenant customer. This is the single
  highest-severity finding.
- **Regression risk:** Medium. The change adds an ownership predicate to 13
  handlers and activates `TenantMiddleware` (previously defined-but-unused).
  Risk vectors: (a) legitimate same-tenant access must still succeed;
  (b) super-admin cross-tenant operations must still be allowed; (c) the
  middleware must not panic (it did, latently — see below). All three are
  covered by the new regression suite plus the existing handler suites.
- **Implementation strategy:** (1) Wire `auth.TenantMiddleware` into the
  authenticated route group so `tenant_id` is resolved into request context.
  (2) Add a single `callerOwnsTenant(c, ownerTenantID)` helper (super-admin may
  cross; others must match) and apply it to every mailbox/domain lookup,
  returning 404 (not 403) on mismatch so existence isn't disclosed. (3) Add the
  `tenant_id` predicate to the corresponding UPDATE/DELETE statements as
  defence-in-depth. (4) Fix the latent `Raw().Scan()` panic in the custom
  SQLite GORM dialector by using the raw `*sql.DB` handle. (5) Fix `CreateDomain`
  hard-coding `tenant_id = 0`.
- **Rollback strategy:** Single commit; `git revert` restores prior behaviour.
  No schema change, no data migration, no config change — fully reversible.

### Implementation
Files: `internal/api/handlers/handlers.go`, `internal/api/handlers/saas_admin.go`,
`internal/api/router.go`, `internal/auth/tenant.go`, plus new
`internal/api/handlers/tenant_isolation_test.go` (12 regression tests).

### Evidence
- `gofmt`: added lines conformant (see formatting note); `gofmt -d` filtered to
  added tokens (`callerOwnsTenant`, `tenant_id = ?`, `TenantMiddleware`) → no
  proposed changes to any added line.
- `go vet ./...` → exit 0.
- `go build ./...` → exit 0.
- `go test -p 1 -count=1 ./... -timeout=1200s` → **TEST_EXIT:0**, 69 packages
  `ok`, 0 failures, 0 panics.
- Security regression (targeted): `go test ./internal/api/handlers/ -run
  TestTenantIsolation -v -count=1` → **12/12 PASS** (ok 12.040s). Covers
  cross-tenant GET/UPDATE-password/DELETE/status/quota mailbox, bulk mailbox,
  GET/UPDATE/DELETE/bulk domain, mailbox audit — all return 404 across tenants;
  same-tenant access still 200.
- PostgreSQL + SQLite regression: both dialects exercised by the full suite
  (config/models/coremail/api packages) → included in the 69-pkg pass.

### Formatting note (applies to the whole sprint)
`main` carries pre-existing repo-wide `gofmt` drift (e.g. 217 lines in
`handlers.go` alone, in functions unrelated to any finding). Running `gofmt -w`
on touched files would rewrite that pre-existing drift and bury each security
change under formatting noise, violating "do not refactor working code." There
is no `gofmt` gate in `main`'s CI. Therefore: **every line this sprint adds is
`gofmt`-conformant** (verified per finding with `gofmt -d` filtered to the added
tokens), but pre-existing drift in untouched code is left as-is and flagged as a
separate, out-of-scope cleanup item.

---

## C-2 — SQL-injection CVE in shipped PostgreSQL driver (pgx)

### Risk analysis (pre-implementation)
- **Risk:** Known SQL-injection vulnerability (GO-2026-5004) in
  `github.com/jackc/pgx/v5 v5.6.0`, the driver used for the PostgreSQL metadata
  layer.
- **Attack scenario:** Under queries using `$`-style placeholders adjacent to
  dollar-quoted string literals, pgx's client-side sanitizer can mis-parse the
  statement, allowing crafted input to break out of a parameter. Reachable in
  this codebase via `ExportDomainsCSV`.
- **Business impact:** Data exfiltration/manipulation of the metadata DB (users,
  tenants, licenses). A shipped SQL-injection CVE in the DB driver is a release
  blocker for a security RC.
- **Regression risk:** Low-Medium. A patch-level driver bump (5.6.0 → 5.9.2)
  within the same major version; API-compatible. Risk is limited to behavioural
  differences in query handling, fully covered by the PostgreSQL + full suite.
- **Implementation strategy:** `go get github.com/jackc/pgx/v5@v5.9.2`,
  `go mod tidy`; verify with the full suite and `govulncheck` (GO-2026-5004 must
  disappear from reachable results). No application code change.
- **Rollback strategy:** revert the go.mod/go.sum commit. No code or data
  changes.

### Implementation
`go get github.com/jackc/pgx/v5@v5.9.2` + `go mod tidy`. Only `go.mod`/`go.sum`
changed. No application code touched. `go mod tidy` additionally pruned two
already-unused modules (`gorm.io/driver/sqlite`, `mattn/go-sqlite3`) — verified
unimported (`grep` finds only a *comment* referencing the former; the project
uses `modernc.org/sqlite` directly, still present at v1.51.0). Safe: all SQLite
paths pass in the full suite. SQLite/Postgres architecture unchanged.

### Evidence
- `go build ./...` → exit 0.
- Security regression: `govulncheck ./...` → **GO-2026-5004 (pgx SQL-injection)
  = 0 occurrences (cleared)**. (govulncheck still exits 3 because of Go-stdlib
  CVEs from the 1.25.0 toolchain — those are H-8's scope, addressed next.)
- `go test -p 1 -count=1 ./... -timeout=1200s` → **TEST_EXIT:0**, 69 `ok` +
  4 `[no test files]`, 0 failures (matches C-1 baseline package count).
- PostgreSQL + SQLite regression: full suite exercises both dialects → pass.

**Verification-integrity incident (recorded for traceability):** the first C-2
suite invocation was accidentally launched with a trailing `&`, leaving an
orphaned `go test` that ran concurrently with a relaunch; both wrote to the same
log (two `TEST_EXIT:0` lines, 59 vs 69 `ok`). Per rule 10 this was classified as
an **environmental/process artifact** (no `FAIL` lines in either; both exited 0),
not a regression, and proven by a clean isolated rerun to a fresh log with no
orphaned processes → single `TEST_EXIT:0`, full 69-package pass. The clean rerun
is the authoritative C-2 evidence.

**Scope note for H-8 (discovered here):** at toolchain 1.25.0 govulncheck now
reports **"Your code is affected by 29 vulnerabilities from 1 module and the Go
standard library."** — materially more than the audit's original 7, because the
vuln DB has grown since 2026-07-11. H-8 must therefore (a) pick a Go patch
release new enough to clear *all* current stdlib CVEs (may be > 1.25.12), and
(b) bump the offending non-stdlib module (`x/net` and any other). H-8 will
re-run govulncheck to prove **0 reachable** as its exit gate.

---

## C-3 — Encryption key architecture (persist + fail-safe)

### Risk analysis (pre-implementation)
- **Risk:** `config.getEncryptionKey()` generates a random in-memory AES key
  when `ORVIX_ENCRYPTION_KEY` is unset and never persists it, so the key changes
  every restart. `config.EncryptString` protects **license keys and license
  metadata** (`models.go:38,45`) today; the standard installer never sets the
  env var.
- **Attack scenario:** Not primarily an attacker scenario — it is a
  data-integrity/availability failure: after any restart, previously encrypted
  license data becomes permanently undecryptable (silent data loss). Secondarily,
  it blocks a real at-rest control (H-6 MFA) from being trustworthy.
- **Business impact:** Silent loss of encrypted license data on restart; blocks
  H-6; undermines any "secrets encrypted at rest" claim for enterprise.
- **Regression risk:** Medium. `getEncryptionKey` is on the hot path of every
  Encrypt/Decrypt. The change must (a) keep the `ORVIX_ENCRYPTION_KEY` env path
  byte-for-byte unchanged (compat), (b) not break tests/CI that write to a
  non-persistent path (must fall back gracefully, never panic/fail the process),
  (c) keep the `sync.Once` semantics.
- **Implementation strategy:** mirror the **proven** JWT key pattern
  (`auth.loadOrGenerateKey`): priority order (1) `ORVIX_ENCRYPTION_KEY` hex env
  (unchanged); (2) persisted key file at `ORVIX_ENCRYPTION_KEY_PATH` or default
  `/var/lib/orvix/encryption_key` (same dir the installer already creates for
  `jwt_key.pem`, `0600`); (3) generate + persist to that file; (4) only if the
  write fails, fall back to an in-memory key **with a loud warning** (same
  degradation contract as the JWT key — does not fail the process, so tests/CI
  are unaffected). New unit test proves cross-"restart" decrypt when the key is
  persisted, and that the env var still wins.
- **Rollback strategy:** single commit revert restores prior behaviour. The key
  file is additive; no existing data is migrated or rewritten. (Data encrypted
  under a prior ephemeral key was already unrecoverable — this fix prevents
  future loss, it cannot recover already-lost data; noted for the CTO.)

### Implementation
Files: `internal/config/crypto.go` (impl), new
`internal/config/crypto_keylifecycle_test.go` (5 tests).
- Split the resolution logic out of the `sync.Once` wrapper into
  `resolveEncryptionKey()` (testable without process restarts).
- Resolution order: (1) `ORVIX_ENCRYPTION_KEY` hex env — **unchanged**; (2)
  persisted key file at `ORVIX_ENCRYPTION_KEY_PATH` or default
  `/var/lib/orvix/encryption_key`; (3) generate + persist (`persistEncryptionKey`
  writes hex, `0600`, dir `0700`); (4) on persist failure, in-memory key with a
  loud `log` warning (same degradation contract as `auth.loadOrGenerateKey`).
- Also split `Encrypt`/`Decrypt` into key-parameterized `encryptWithKey`/
  `decryptWithKey` cores (no behaviour change) so the round-trip-across-restart
  path is directly testable.
- **No installer change required:** the key self-persists to the same
  `/var/lib/orvix` directory the app already writes `jwt_key.pem` to at runtime
  (installer already creates it, orvix-owned) — keeps the fix minimal.

### Evidence
- `gofmt -l` on both C-3 files → clean.
- `go build ./...` → exit 0; `go vet ./internal/config/` → exit 0.
- `go test ./internal/config/ -run TestEncryptionKey -v` → 4 PASS +
  1 SKIP (FilePermissions skips on Windows — POSIX-mode assertion). Covers:
  persist-and-survive-restart, 0600 perms, env-var priority, invalid-env
  rejected, end-to-end encrypt→restart→decrypt.
- Full `config` package: `go test ./internal/config/ -count=1` → ok 63.4s
  (incl. installer_test) — no regression.
- Full suite: `go test -p 1 -count=1 ./... -timeout=1200s` → **TEST_EXIT:0**,
  69 `ok` + 4 `[no test files]`, 0 failures (single clean run). PostgreSQL +
  SQLite regression covered by the suite.

---

## H-4 — PostgreSQL metadata backup

### Risk analysis (pre-implementation)
- **Risk:** `backup.Service.snapshotDB` issues SQLite `VACUUM INTO` only; on a
  Postgres metadata deployment the backup of users/tenants/licenses/auth fails
  or is skipped — no recovery path for the most critical data.
- **Attack scenario:** Availability/DR, not attacker-driven: disk loss or
  corruption with no valid metadata backup ⇒ unrecoverable tenants/users.
- **Business impact:** Total metadata loss on a Postgres deployment; violates
  any enterprise DR/RPO commitment.
- **Regression risk:** Low-Medium. Adds a dialect branch; the SQLite path is
  unchanged. Postgres path shells to `pg_dump`; must fail loudly (never emit an
  empty/OK archive) when `pg_dump`/DSN is absent, and must not affect SQLite
  deployments.
- **Implementation strategy:** detect dialect; for Postgres run
  `pg_dump -Fc` to the archive and record `DatabaseFormat` in the manifest;
  keep SQLite `VACUUM INTO` for SQLite. Fail-closed on missing tool/DSN.
- **Rollback strategy:** single-commit revert; no schema/data change.

### Implementation
Files: `internal/backup/service.go`, `internal/backup/types.go`,
`internal/api/handlers/backups.go`, new `internal/backup/postgres_snapshot_test.go`.
- `snapshotDB` branches on `s.dialect.IsPostgres()`: SQLite keeps `VACUUM INTO`;
  Postgres calls new `snapshotPostgres` → `pg_dump --format=custom --file=…
  --no-password <dsn>`.
- **Fails loudly**: errors if `SetPostgresDSN` was never called or `pg_dump` is
  absent from PATH — no partial/fake success (a backup silently omitting the DB
  is worse than one that errors).
- `BackupManifest.DatabaseFormat` records `sqlite` vs `postgres-custom` so a
  restore operator knows to use `pg_restore` not a file-copy.
- `backups.go` wires `svc.SetPostgresDSN(h.cfg.Database.DSN)`.

### Evidence
- gofmt: added lines conformant (`gofmt -d` shows no change to added tokens;
  service.go/types.go pre-existing drift confirmed via stash-check, left
  untouched); new test file clean.
- `go vet ./internal/backup/ ./internal/api/handlers/` → exit 0;
  `go build ./...` → exit 0.
- `go test ./internal/backup/ -count=1` → ok 12.8s (incl. the new
  postgres_snapshot fail-path test).
- Full suite: `go test -p 1 -count=1 ./... -timeout=1200s` → **TEST_EXIT:0**,
  69 `ok` + 4 `[no test files]`, 0 failures (single clean run). SQLite path
  (VACUUM INTO) unchanged and green; Postgres path covered by the fail-path unit
  test (the pg_dump success path runs in CI where postgresql-client exists).

### Risk analysis (pre-implementation)
- **Risk:** CSRF is applied per-route; several state-changing admin endpoints
  are mounted outside the CSRF group and are forgeable; session cookies are
  `SameSite=None`.
- **Attack scenario:** A logged-in admin visits an attacker page that
  auto-submits `POST /api/v1/admin/...` (migration start, domain provisioning,
  compliance policy). The session cookie rides along (`SameSite=None`), the
  request lacks CSRF protection ⇒ forged admin action executes.
- **Business impact:** Admin-action forgery (config/provisioning tampering),
  cross-site.
- **Regression risk:** Medium. Enforcing CSRF on the whole admin group could
  break legitimate flows that don't send a token. Mitigation: API-key/Bearer
  requests are exempted (they are not CSRF-eligible and carry no cookie); the
  admin UI already fetches and sends the CSRF token. Covered by the CSRF +
  admin route test suites.
- **Implementation strategy:** mount `csrf.Middleware()` on the admin group by
  default (deny-by-default) with an explicit Bearer/API-key bypass in the CSRF
  manager.
- **Rollback strategy:** single-commit revert.

### Implementation
Files: `internal/api/router.go`, `internal/auth/csrf.go`, new
`internal/api/handlers/csrf_admin_group_coverage_test.go`,
`internal/auth/csrf_apikey_bypass_test.go`.
- `admin := protected.Group("", RequireAnyRole(...), r.csrf.Middleware())` —
  CSRF now covers the entire admin group; the old `men := admin.Group("",
  r.csrf.Middleware())` becomes `men := admin` (single enforcement point).
- `csrf.Middleware()` no-ops on GET/HEAD/OPTIONS and on API-key
  (Bearer)-authenticated requests (which carry no CSRF cookie and are not
  CSRF-eligible), so read routes and the provisioning API are unaffected.
- **Router-region note:** this edits the `admin`/`men` groups; C-1 edited the
  `protected` group + Router struct — non-overlapping regions of `router.go`.

### Evidence
- gofmt: added lines conformant (`gofmt -d` shows no change to added router
  lines; new/csrf test files clean); pre-existing drift untouched.
- `go build ./...` → exit 0; `go vet ./internal/api/... ./internal/auth/...`
  → exit 0.
- Security regression (targeted), 6/6 PASS:
  - `TestCSRF_CompliancePoliciesRejectsMissingToken` — previously-unprotected
    `POST /compliance/policies` now 403 without a token.
  - `TestCSRF_CompliancePoliciesAcceptsValidToken` — valid token → accepted.
  - `TestCSRF_CollaborationMailboxesRejectsMissingToken` — `POST
    /collaboration/mailboxes` now protected.
  - `TestCSRF_AdminGroupGETStillWorksWithoutToken` — admin GETs unaffected.
  - `TestCSRFMiddleware_APIKeyRequestBypassesCSRF` — Bearer/API-key requests
    correctly bypass CSRF.
  - `TestCSRFMiddleware_SessionRequestStillRequiresCSRF` — cookie/session
    mutations still require the token.
- Full suite: `go test -p 1 -count=1 ./... -timeout=1200s` → **TEST_EXIT:0**,
  69 `ok` + 4 `[no test files]`, 0 failures (single clean run).

---

## H-6 — MFA secret protection (depends on C-3)

### Risk analysis (pre-implementation)
- **Risk:** TOTP secret stored reversibly (base64). DB read ⇒ permanent MFA
  bypass.
- **Attack scenario:** Backup theft / rogue DBA / SQLi-elsewhere reads
  `mfa_secret_raw`, decodes base64, generates valid codes indefinitely.
- **Business impact:** MFA provides no protection against DB compromise.
- **Regression risk:** Medium, and **gated on C-3**: encrypting the secret with
  `config.Encrypt` is only safe once the encryption key persists (C-3), else
  enrolled users are locked out after a restart. Sequenced C-3 → H-6. Enrollment
  + verification round-trip covered by MFA tests.
- **Implementation strategy:** encrypt the secret at rest with AES-256-GCM
  (`config.Encrypt`) on write; decrypt on verify. No protocol/UX change.
- **Rollback strategy:** single-commit revert.
- **MIGRATION IMPACT (corrected after reviewing the actual patch — CTO please
  note):** the verify path uses `config.Decrypt` with **no base64 fallback**.
  This is the correct security-first choice (it does not retain the vulnerable
  plaintext-readable path), but it means any MFA enrollment created under the
  old base64 scheme will **fail to verify after this deploys — affected users
  must re-enroll MFA once.** On a fresh/pre-RC deployment with no existing MFA
  enrollments this is a non-issue. A backward-compatible "decrypt-or-legacy-
  base64, then re-encrypt on read" migration is possible but deliberately NOT
  included, because keeping a base64-readable path alive would re-open the exact
  finding. Flagged for the CTO as a one-time deployment/runbook note.

### Implementation
Files: `internal/api/handlers/admin_mfa.go`, `internal/api/handlers/admin_mfa_test.go`.
- Replaced base64 encode/decode of the pending + confirmed MFA secret with
  `config.Encrypt` (write) / `config.Decrypt` (verify); removed the now-unused
  `base64Decode` helper (build confirms no other callers).
- Relies on C-3's persistent key so encrypted secrets survive restart.

### Evidence
- gofmt: `admin_mfa.go` added lines conformant; new test clean.
- `go build ./...` exit 0 (confirms `base64Decode` removal safe + `config.Encrypt`
  resolves against the C-3-refactored crypto); `go vet` exit 0.
- MFA tests 12/12 PASS incl. `TestMFASecretStoredEncryptedNotBase64` (stored
  value is genuine AES-GCM ciphertext, not base64) and full `TestMFASetupFlow`
  + `TestMFALoginFlowEnforcesChallengeAndRecoveryCodes` (enroll→verify
  round-trips on the encrypted secret — proves the C-3 dependency end-to-end).
- Full suite: `go test -p 1 -count=1 ./... -timeout=1200s` → **TEST_EXIT:0**,
  69 `ok` + 4 `[no test files]`, 0 failures (single clean run).

### H-8 scoping note (determined during the H-6 wait)
Highest "Fixed in" across all 29 currently-reachable CVEs = **go1.25.12**; the
only non-stdlib module CVE is `golang.org/x/net` (fixed v0.55.0). The planned
H-8 bump (toolchain 1.25.12 + x/net v0.55.0) is therefore sufficient; H-8's exit
gate is `govulncheck ./...` = 0 reachable.

---

## H-8 — Toolchain + x/net CVEs

### Risk analysis (pre-implementation)
- **Risk:** 6 reachable CVEs from Go 1.25.0 stdlib + `x/net v0.54.0` (crypto/tls
  ECH leak, crypto/x509, net/textproto, net/mail quadratic parse ×2, idna).
- **Attack scenario:** DoS via quadratic mail-address parsing on the SMTP/webmail
  ingest path; TLS/x509 edge issues.
- **Business impact:** Remote DoS surface on mail ingestion; shipped known CVEs.
- **Regression risk:** Low-Medium. Toolchain patch bump (1.25.0 → 1.25.12) and
  `x/net` minor (0.54 → 0.55); GOTOOLCHAIN=auto fetches the toolchain. Covered by
  full build + suite.
- **Implementation strategy:** set `go 1.25.12` / toolchain, `go get
  golang.org/x/net@v0.55.0`, `go mod tidy`; verify `govulncheck` = 0 reachable.
- **Rollback strategy:** revert go.mod/go.sum.

### Implementation
`go get golang.org/x/net@v0.55.0`; `go get go@1.25.12` (GOTOOLCHAIN=auto fetched
go1.25.12); `go mod tidy`. Only `go.mod`/`go.sum` changed. No application code.

### Evidence
- `go build ./...` → exit 0 on **go1.25.12**.
- **Security gate — `govulncheck ./...` → "No vulnerabilities found. Your code
  is affected by 0 vulnerabilities." (exit 0)** — all 29 previously-reachable
  CVEs cleared (was 29 at 1.25.0 with pgx already bumped; now 0).
- Full suite (on go1.25.12, which also re-validates every prior sprint commit
  under the new toolchain): `go test -p 1 -count=1 ./... -timeout=1200s` →
  **TEST_EXIT:0**, 69 `ok` + 4 `[no test files]`, 0 failures (single clean run).

---

## H-9 — JWT revocation on logout

### Risk analysis (pre-implementation)
- **Risk:** `ValidateAccessToken` checks signature+expiry only; logout deletes
  the opaque session but the stateless RS256 access token stays valid until
  expiry (≤15 min) after logout.
- **Attack scenario:** An access token captured (XSS-exfil, shared device,
  proxy log) remains usable for up to 15 min after the user logs out — logout
  gives a false sense of containment.
- **Business impact:** Bounded but real session-fixation/containment gap;
  enterprise auth is expected to honour logout immediately.
- **Regression risk:** Medium. Introduces a per-request revocation check and a
  new table. Must: (a) treat legacy tokens without `jti` as non-revoked (they
  expire in ≤15 min) so nothing breaks at rollout; (b) never fail logout if the
  revoke write errors; (c) add the table in **both** SQLite (`models.go`) and
  Postgres (`postgres_migrations.go`) schema paths; (d) keep the check an
  indexed PK lookup. Perf: adds one small indexed lookup per authenticated
  request (documented; acceptable for RC, cache-able later).
- **Implementation strategy:** add a `jti` claim to access tokens; new
  `revoked_tokens(jti PK, expires_at)` table (both dialects); `RevokeToken` /
  `IsTokenRevoked` on the authenticator; `ValidateAccessToken` rejects revoked
  jtis; `Logout`/`LogoutAll` revoke the presented token's jti with its exp;
  opportunistic cleanup of expired rows. New unit tests: revoked token rejected,
  legacy no-jti token still accepted, logout revokes.
- **Rollback strategy:** single-commit revert; table is additive (harmless if
  left in place).

### Implementation
Files: `internal/auth/auth.go`, `internal/api/handlers/handlers.go`,
`internal/models/models.go`, `internal/models/postgres_migrations.go`, new
`internal/auth/auth_revocation_test.go`.
- `GenerateAccessToken` adds a random 128-bit `jti` claim (`newJTI`).
- `ValidateAccessToken` rejects a token whose `jti` is revoked; legacy tokens
  without a jti are treated as non-revocable (expire within the access TTL).
- `RevokeAccessToken(tokenString)` parses the token (unverified — signature
  already trusted at issue), extracts jti+exp, and records it until exp.
- `revoked_tokens(jti PRIMARY KEY, expires_at INTEGER/BIGINT)` added to BOTH
  the SQLite (`models.go`) and Postgres (`postgres_migrations.go`) schema paths.
- `Logout`/`LogoutAll` revoke the presented access token (cookie or Bearer) via
  a shared `revokeCurrentAccessToken` helper (best-effort; never blocks logout).
- Storage uses **raw `*sql.DB` + `dbdialect`** (portable `Upsert`/`Placeholder`),
  NOT GORM — see debugging note below. `expires_at` is a Unix integer for
  unambiguous cross-dialect comparison.
- Fail-safe: `isTokenRevoked` returns false (not revoked) if the store is
  nil/unavailable, so a store outage can never lock every user out. Perf: one
  indexed lookup per authenticated request (documented; cache-able later).

### Debugging note (rule 10 — real defects found & fixed, classified with evidence)
Initial implementation used GORM (`db.Save`/`db.Model().Count()`). Tests failed
at *validation* (a revoked token still validated). Root-caused with a throwaway
diagnostic that compared paths against a real migrated DB:
1. GORM `Save`/`Table().Create()` returned `err=nil` but persisted **0 rows**
   under the custom modernc SQLite dialector — even for the `sessions` table.
   Raw `*sql.DB` `INSERT`/`COUNT` on the same table worked (count=1). ⇒ **real
   defect** in the GORM-over-custom-dialector path (same class as C-1's
   `Raw().Scan` panic). Fixed by moving revocation storage to raw `*sql.DB` +
   `dbdialect` placeholders/upsert.
2. After the raw-SQL fix, tests still reported FAIL — but with **no assertion
   error**, only a Windows `t.TempDir` cleanup error ("file in use") because the
   test helper never closed the SQLite handle. ⇒ **test-hygiene defect**, not a
   logic failure. Fixed by closing the DB in `t.Cleanup`.
Both were proven by isolated reruns before proceeding.

### Evidence
- gofmt: all H-9 files clean (added lines conformant).
- `go vet ./internal/auth/ ./internal/api/handlers/ ./internal/models/` → exit 0;
  `go build ./...` → exit 0.
- H-9 tests 5/5 PASS: `TestRevokedTokenRejected` (revoked → rejected),
  `TestNonRevokedTokenStillValid`, `TestLegacyTokenWithoutJTIStillValid`
  (pre-H-9 token still valid), `TestRevocationFailsSafeWithoutStore` (nil store
  → fail-safe), `TestExpiredRevocationsPruned`.
- Full suite: `go test -p 1 -count=1 ./... -timeout=1200s` → **TEST_EXIT:0**,
  69 `ok` + 4 `[no test files]`, 0 failures (single clean run) on go1.25.12.
