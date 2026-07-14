# Orvix Enterprise — Security Audit for Release Candidate

**Date:** 2026-07-11
**Scope requested by CTO:** authentication, authorization, RBAC, MFA, session
management, CSRF, API keys, secrets, encryption, logging, audit, backup,
recovery, update system.
**Audited against:** `origin/main` @ `903fa7b` (PR #10) — the current shippable
state. Code read from a clean main-equivalent worktree.
**Nature of this document:** audit only. No code changed, nothing implemented,
merged, or released. Awaiting CTO approval before any remediation.

## How to read this

Every finding lists **location, risk, evidence, recommended fix, estimated
effort**, and a **Fix status** line. Fix status is one of:
- **NONE** — no fix exists; newly identified here.
- **LOCAL BRANCH: `<name>`** — a fix was implemented on an unmerged local
  branch in a prior session and verified (full suite: 69 pkgs, 0 fail). It is
  NOT on `main` and NOT deployed. Listed so you can decide whether to adopt,
  revise, or discard it. **Nothing has been merged.**

Evidence is executable/observable: file:line, `govulncheck` output, or a
reproducible condition.

---

## CRITICAL

### C-1 — Cross-tenant IDOR on mailbox & domain administration
- **Subsystem:** authorization / tenant isolation
- **Location:** `internal/api/handlers/handlers.go` — `GetMailbox` (~1476),
  `UpdateMailboxPassword` (~1919/1933), `UpdateMailboxStatus`, `UpdateMailboxQuota`,
  `UpdateMailboxProtocols`, `DeleteMailbox` (~2258/2271), `GetDomain`,
  `UpdateDomainStatus`, `DeleteDomain`, `BulkMailboxStatus`, `BulkDomainStatus`,
  `GetMailboxAudit`, `GetDomainAudit`. Middleware `internal/auth/tenant.go`.
- **Risk:** Any authenticated tenant admin can read, modify, or delete another
  tenant's mailbox/domain by supplying its numeric id. Full cross-tenant
  account takeover (e.g. reset a victim tenant's mailbox password) and data
  destruction. Directly breaks the core isolation guarantee of the multi-tenant
  product.
- **Evidence:** every listed handler scopes its SQL by `id`/`name` only, never
  by `tenant_id`, though the column exists and is populated at creation.
  `auth.TenantMiddleware` is defined but **not registered** on any route group,
  so `tenant_id` is never even placed in request context to check against.
- **Recommended fix:** wire `TenantMiddleware` into the authenticated group;
  add an ownership check (super-admin may cross tenants; others 404 on
  mismatch) to every id/name lookup; add cross-tenant regression tests.
- **Estimated effort:** 3–5 days (fix + tests + review).
- **Fix status:** LOCAL BRANCH `fix/tenant-isolation-mailbox-domain` (also fixes
  a latent `Raw().Scan()` panic in the custom SQLite dialector and a
  `CreateDomain` `tenant_id=0` bug; adds 12 regression tests).

### C-2 — SQL-injection CVE in shipped PostgreSQL driver (pgx v5.6.0)
- **Subsystem:** secrets / data layer (SQL safety)
- **Location:** `go.mod` (`github.com/jackc/pgx/v5 v5.6.0`); reachable via
  `internal/api/handlers/handlers.go:~2937` (`ExportDomainsCSV`).
- **Risk:** GO-2026-5004 — placeholder confusion with dollar-quoted string
  literals allows SQL injection under specific query shapes. Shipping a known
  SQL-injection CVE in the database driver is disqualifying for a security
  release.
- **Evidence:** `govulncheck ./...` reports GO-2026-5004 as **reachable** (call
  trace into `sanitize.SanitizeSQL`). Fixed upstream in pgx v5.9.2.
- **Recommended fix:** `go get github.com/jackc/pgx/v5@v5.9.2`, re-run full
  suite + govulncheck.
- **Estimated effort:** 0.5–1 day (bump + verify; low code risk, well-tested).
- **Fix status:** LOCAL BRANCH `fix/security-dep-bumps` (also bumps x/net and Go
  toolchain — see H-8; post-fix govulncheck = 0 reachable vulns).

### C-3 — At-rest encryption key is ephemeral and unprovisioned → silent secret loss
- **Subsystem:** secrets / encryption / reliability
- **Location:** `internal/config/crypto.go:24-45` (`getEncryptionKey`); consumers
  `internal/models/models.go:38,45` (license key + metadata),
  `internal/api/handlers/handlers.go:2696`; installer `release/install.sh`
  (no `ORVIX_ENCRYPTION_KEY` provisioning — grep finds none).
- **Risk:** When `ORVIX_ENCRYPTION_KEY` is unset, `getEncryptionKey()` generates
  a **random 32-byte key in memory that is never persisted**. It differs on
  every process restart, so anything encrypted via `config.Encrypt` —
  **license keys and license metadata today** — becomes permanently
  undecryptable after a reboot. Encryption *appears* to succeed, then data is
  silently lost. The standard installer does not set the variable, so a
  default production deployment hits this path. This also undermines any
  future secret-at-rest feature (including the MFA-at-rest fix, M-3/H-6).
- **Evidence:** `crypto.go:36-41` allocates `make([]byte,32)` + `rand.Read`
  with no disk persistence and no error/warning when the env var is missing;
  `grep ORVIX_ENCRYPTION_KEY release/install.sh` → not found; `models.go:38,45`
  call `config.EncryptString` on persisted license fields.
- **Recommended fix:** treat the encryption key like the JWT key
  (`auth.go:loadOrGenerateKey`, which correctly persists to disk): load from a
  root-owned `0600` key file, generate-and-persist on first run, and
  **fail closed** (refuse to start) in production if no stable key is available
  rather than silently using an ephemeral one. Provision it in the installer.
- **Estimated effort:** 2–4 days (key lifecycle + installer + fail-closed guard
  + migration note for any already-encrypted data).
- **Fix status:** NONE (newly identified). Note: this must land **before or with**
  any at-rest encryption feature or that feature is unreliable.

---

## HIGH

### H-4 — PostgreSQL metadata backup silently fails (no metadata backup)
- **Subsystem:** backup / recovery
- **Location:** `internal/backup/service.go` (`snapshotDB`, ~1558).
- **Risk:** `snapshotDB` only issues SQLite `VACUUM INTO`, invalid against a
  PostgreSQL connection. On a Postgres deployment every backup of the metadata
  DB (users, tenants, licenses, auth) fails, so that data is **not backed up at
  all** — unrecoverable on disk loss. The most business-critical data has no
  recovery path.
- **Evidence:** `service.go` calls `VACUUM INTO ?` on `mailStoreDB` only; the
  metadata handle `s.db` is never dumped; no `pg_dump`/`exec.Command` present in
  the service.
- **Recommended fix:** dialect branch shelling to `pg_dump -Fc` for Postgres,
  record format in the manifest, fail loudly (never emit an empty/OK backup)
  when `pg_dump` or DSN is missing.
- **Estimated effort:** 3–5 days.
- **Fix status:** LOCAL BRANCH `fix/postgres-metadata-backup`.

### H-5 — CSRF protection is opt-in per route; several admin mutations unprotected
- **Subsystem:** CSRF / session
- **Location:** `internal/api/router.go` (admin group ~623 mounted without CSRF;
  compare CSRF sub-groups); auth cookies set `SameSite=None`
  (`internal/api/handlers/handlers.go` login paths, `webmail_auth.go`).
- **Risk:** State-changing admin routes not nested under the CSRF sub-group
  (migration start, domain provisioning, compliance policy writes,
  collaboration mailboxes, etc.) are forgeable cross-site; `SameSite=None`
  makes session cookies attach on cross-site requests, compounding it.
- **Evidence:** CSRF middleware applied only to specific sub-groups, not the
  admin group as a whole; cookie flags show `SameSite: "None"`.
- **Recommended fix:** enforce CSRF on the entire admin group by default
  (deny-by-default), with an explicit bypass only for Bearer/API-key requests
  (not CSRF targets); reconsider `SameSite`.
- **Estimated effort:** 2–3 days.
- **Fix status:** LOCAL BRANCH `fix/csrf-global-enforcement`.

### H-6 — MFA TOTP secret stored reversibly
- **Subsystem:** MFA / secrets
- **Location:** `internal/api/handlers/admin_mfa.go:95-96,160-162`.
- **Risk:** Secret stored as base64 (reversible), despite a comment claiming it
  is hashed. Read access to the DB (backup theft, rogue DBA, SQLi elsewhere)
  yields the secret and thus valid TOTP codes indefinitely — MFA gives no
  protection against DB compromise.
- **Evidence:** `pending_mfa_secret_raw` / `mfa_secret_raw` written as base64,
  not ciphertext.
- **Recommended fix:** encrypt at rest with AES-256-GCM (`config.Encrypt`).
  **Depends on C-3** — without a persistent encryption key, encrypting the MFA
  secret would make enrollment break after restart. Sequence C-3 first.
- **Estimated effort:** 2–3 days (plus C-3 as prerequisite).
- **Fix status:** LOCAL BRANCH `fix/mfa-secret-encryption` (currently uses
  `config.Encrypt`; safe only once C-3 provides a stable key).

### H-7 — Update system has no cryptographic signature verification
- **Subsystem:** update system (supply chain)
- **Location:** `internal/updater/runtime.go` (real path: `Run` →
  `runViaSystemd`), `release/upgrade.sh` (integrity check).
- **Risk:** Updates are validated by **SHA-256 checksum only** — integrity, not
  authenticity. An attacker who can serve the update feed/binary (compromised
  update host, MITM if TLS trust is broken, or a malicious mirror) can provide
  a matching checksum and push arbitrary code to every installation via the
  auto-update path. No release signing (GPG/cosign) is present. `--dev-unsafe`
  can skip verification entirely.
- **Evidence:** `upgrade.sh` implements `sha256_of_file` + `verify_checksum_fail_closed`
  but no `gpg`/`cosign`/`.sig` verification (grep finds none); `updater.go`
  `DownloadUpdate` reads bytes and does not even check the `Checksum` field
  (that stub `UpdateManager` is dead code — the live path is `RuntimeService`,
  which delegates to the systemd helper + `upgrade.sh`). SSRF protection on the
  feed URL *is* present (`validateFeedURL`/`isRejectedFeedIP`) — good.
- **Recommended fix:** sign release artifacts (minisign/cosign/GPG) and verify
  the signature against a pinned public key before apply; keep checksum as a
  secondary integrity check; gate `--dev-unsafe` behind a build tag.
- **Estimated effort:** 5–8 days (signing infra + verify path + key
  distribution).
- **Fix status:** NONE (newly identified).

### H-8 — Stdlib/x-net CVEs from Go 1.25.0 toolchain
- **Subsystem:** secrets / transport (dependency hygiene)
- **Location:** `go.mod` (`go 1.25.0`, `golang.org/x/net v0.54.0`).
- **Risk:** 6 reachable CVEs beyond C-2: crypto/tls ECH leak (GO-2026-5856),
  crypto/x509 (GO-2026-5037), net/textproto (GO-2026-5039), net/mail quadratic
  parsing x2 (GO-2026-4986/4977), x/net idna (GO-2026-5026). Includes
  DoS-via-parsing on the SMTP/mail ingest path.
- **Evidence:** `govulncheck ./...` traces (e.g. `delivery/transport.go`,
  `mime/parser.go`, `handlers.go` CreateMailbox/WebmailSend).
- **Recommended fix:** Go toolchain 1.25.0 → 1.25.12, `x/net` → v0.55.0.
- **Estimated effort:** 0.5–1 day (with C-2).
- **Fix status:** LOCAL BRANCH `fix/security-dep-bumps` (verified: 0 reachable
  vulns post-bump, full suite green).

### H-9 — Stateless access token not revoked on logout
- **Subsystem:** session management
- **Location:** `internal/auth/auth.go` — `ValidateAccessToken` (~184-210),
  `Logout` (session delete ~227).
- **Risk:** Logout deletes the opaque session row, but the RS256 access-token
  JWT is validated on signature+expiry only, with no blocklist — a stolen/leaked
  access token remains usable until it expires (≤15 min) after logout.
- **Evidence:** `ValidateAccessToken` performs no DB/revocation lookup; logout
  path deletes `sessions` rows only.
- **Recommended fix:** short TTL (already 15 min) is a partial mitigation; add a
  token-id (jti) denylist checked on validation, or switch admin surface to the
  opaque session already present. Risk is bounded by TTL.
- **Estimated effort:** 2–4 days.
- **Fix status:** NONE (bounded-risk; may be accepted for RC with documented TTL).

---

## MEDIUM

### M-10 — Granular RBAC defined but never enforced
- **Subsystem:** RBAC / authorization
- **Location:** `internal/auth/rbac/rbac.go` (full permission model + `Require`
  middleware); `internal/api/router.go` (does **not** import `auth/rbac`; admin
  group uses `auth.RequireAnyRole(admin, superadmin)` at ~623).
- **Risk:** The least-privilege personas (operator/helpdesk, readonly/auditor)
  are implemented and unit-tested but not wired to any route. The live system is
  binary "admin-or-nothing": non-admin roles are fully locked out rather than
  scoped, and the advertised delegation/least-privilege control is not in
  effect. Latent risk if any provisioning path assigns these roles expecting
  containment.
- **Evidence:** no `auth/rbac` import in `router.go`; all admin routes gated by
  the coarse role check; `rbac.Require(...)` unused outside its own tests.
- **Recommended fix:** migrate admin routes to `rbac.Require(<permission>)`
  per-endpoint; add route-level tests asserting operator/readonly are correctly
  allowed/denied.
- **Estimated effort:** 4–6 days (careful per-route mapping + tests).
- **Fix status:** NONE.

### M-11 — API key revocation IDOR (cross-user/tenant DoS)
- **Subsystem:** API keys / authorization
- **Location:** `internal/auth/apikey.go:97` (`Revoke(id)`);
  `internal/api/handlers/handlers.go:~783` (`DeleteAPIKey`).
- **Risk:** `Revoke`/`DeleteAPIKey` disable a key by numeric id with **no
  owner/tenant check**. Any authenticated user can revoke any other user's or
  tenant's API key by guessing an id → denial of service against integrations.
- **Evidence:** `Revoke` updates `WHERE id = ?` only; `List` is correctly scoped
  by `user_id`, but the mutating path is not.
- **Recommended fix:** scope `Revoke` by owning `user_id` (and tenant); 404 on
  mismatch.
- **Estimated effort:** 1 day.
- **Fix status:** NONE.

### M-12 — Audit log is not tamper-evident
- **Subsystem:** logging / audit
- **Location:** `internal/audit/audit.go:63-77` (plain `INSERT`), `:146-153`
  (`PurgeOlderThan` hard `DELETE`).
- **Risk:** Audit trail is a plain mutable table with a delete path and no
  hash-chaining, signing, or append-only enforcement. Anyone with DB write
  access can edit or erase entries undetected — fails common enterprise/
  compliance expectations (immutable audit).
- **Evidence:** schema + `Record` insert + `PurgeOlderThan` delete; no `prev_hash`
  / signature columns.
- **Recommended fix:** hash-chain each entry (`hash = H(prev_hash || entry)`),
  or ship entries to append-only/WORM storage; restrict/rate-limit purge and
  log purges themselves.
- **Estimated effort:** 4–7 days.
- **Fix status:** NONE.

### M-13 — Long-lived, MFA/CSRF-bypassing API keys (365-day default)
- **Subsystem:** API keys / secrets
- **Location:** `internal/api/handlers/handlers.go` `CreateAPIKey` (default
  `ttl = 365 * 24h`, arbitrary via `req.TTL`).
- **Risk:** A one-year bearer token that bypasses MFA and CSRF and inherits the
  creator's role is a powerful, long-lived credential; no max-TTL cap or
  rotation enforcement. (Good: the handler binds role to the *caller's own*
  role, so no privilege escalation via a role parameter.)
- **Evidence:** `CreateAPIKey` sets 365-day default TTL, no upper bound; role
  taken from `c.Locals("role")` (no escalation).
- **Recommended fix:** cap max TTL (e.g. 90 days), require justification/rotation
  for longer; surface expiry prominently.
- **Estimated effort:** 1–2 days.
- **Fix status:** NONE.

### M-14 — Dead DKIM verify landmine
- **Subsystem:** encryption / correctness
- **Location:** `internal/coremail/dkim/signer.go` `Signer.Verify()`.
- **Risk:** Dead method returns `true` after checking only the body hash, never
  the RSA signature. Unused today; a live forgery bypass if wired in later.
- **Evidence:** method returns `true` unconditionally; production path uses
  `verifier.go` (correct).
- **Recommended fix:** delete the dead method.
- **Estimated effort:** <1 day.
- **Fix status:** LOCAL BRANCH `chore/remove-dead-dkim-verify`.

### M-15 — No security scanning in CI
- **Subsystem:** operations / release
- **Location:** `.github/workflows/` (only postgres-readiness + release-bundle).
- **Risk:** No automated vuln/lint gate; C-2/H-8-class CVEs can re-enter
  unnoticed.
- **Evidence:** no govulncheck/gosec step in either workflow.
- **Recommended fix:** add govulncheck as a **blocking** gate; gosec as
  **informational** (see gosec note below).
- **Estimated effort:** 1 day.
- **Fix status:** LOCAL BRANCH `chore/add-security-scanning-ci`.

---

## LOW

### L-16 — Default cache secret fallback
- **Location:** `internal/licensingauthority/service.go:15-16`
  (`defaultCacheSecret = "orvix-cache-secret-default-change-in-production"`).
- **Risk:** If `ORVIX_CACHE_SECRET` is unset, a known default is used for cache
  signing. Env override exists; named for change.
- **Fix / effort:** require the env var in production (fail closed); ~0.5 day.
- **Fix status:** NONE.

### L-17 — SSH host key ignored on backup SFTP upload
- **Location:** `internal/backup/targets/uploader.go:509`
  (`ssh.InsecureIgnoreHostKey()`).
- **Risk:** MITM on the backup destination could capture backup archives.
- **Fix / effort:** pin/verify host key (known_hosts); ~1 day.
- **Fix status:** NONE.

### L-18 — API key rotation uses hard delete
- **Location:** `internal/auth/apikey.go:91-94` (`Rotate` hard-deletes old key).
- **Risk:** Loses audit trail of the rotated key.
- **Fix / effort:** soft-delete/disable instead; ~0.5 day.
- **Fix status:** NONE.

---

## Explicitly NOT findings (correct-by-design — do not "fix")

`gosec` reports these at HIGH; changing them **breaks the product**:
- **TLS `InsecureSkipVerify`** in outbound SMTP delivery
  (`internal/coremail/delivery/transport.go:136,220`) — opportunistic TLS per
  RFC 7435; validating certs would fail delivery to most of the internet's MX
  hosts.
- **`math/rand` for retry jitter** (`internal/coremail/delivery/retry.go:72`) —
  jitter needs no cryptographic randomness.
- **HMAC-SHA1 in MFA** (`internal/api/handlers/admin_mfa.go`) — required by
  RFC 6238 TOTP; every authenticator app depends on it.

This is why the recommended CI gate keeps **gosec informational**, not blocking.
Good controls already in place worth noting: bcrypt (admin) + Argon2id (mailbox)
password hashing; parameterized SQL throughout (the pgx CVE is a driver bug, not
app concatenation); RS256 JWT with alg-confusion guard and disk-persisted key;
SSRF protection on the update feed URL; ClamAV integration is real.

---

## Severity roll-up

| Sev | Count | IDs |
|-----|-------|-----|
| CRITICAL | 3 | C-1 tenant IDOR, C-2 pgx SQLi, C-3 ephemeral encryption key |
| HIGH | 6 | H-4 pg backup, H-5 CSRF, H-6 MFA at-rest, H-7 update signing, H-8 toolchain CVEs, H-9 token revocation |
| MEDIUM | 6 | M-10 RBAC unwired, M-11 apikey IDOR, M-12 audit tamper, M-13 apikey TTL, M-14 dead DKIM, M-15 CI scanning |
| LOW | 3 | L-16 cache secret, L-17 SFTP host key, L-18 apikey hard-delete |

**Newly identified here (no fix exists): C-3, H-7, H-9, M-10, M-11, M-12, M-13,
L-16, L-17, L-18.** These are the items that most need your direction.

**Already have unmerged local fix branches (verified, not deployed): C-1, C-2,
H-4, H-5, H-6, H-8, M-14, M-15.**

## Recommended remediation order (for approval — not yet actioned)
1. C-1, C-2, C-3 (critical; C-3 gates H-6).
2. H-4, H-5, H-8, then H-7, H-9.
3. M-10, M-11, M-12 (enterprise controls), then M-13/M-14/M-15.
4. LOW items as cleanup.

Awaiting CTO approval on scope and order before implementing anything.
