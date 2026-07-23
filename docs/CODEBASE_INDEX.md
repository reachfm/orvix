# ORVIX Codebase Index

**Purpose:** file-level index for the largest/highest-risk source files and every confirmed defect found during this audit. Not an exhaustive per-file listing (the repo has 340+ non-test `.go` files alone) — covers the files that matter most for safe modification.

---

## Largest files (split candidates)

| Lines | File | Risk if modified | Notes |
|---|---|---|---|
| 3657 | `internal/api/handlers/handlers.go` | HIGH | Catch-all handler file; `Handler` struct definition + `NewHandler` constructor + dozens of unrelated endpoint handlers live here. Top split candidate. |
| 2535 | `internal/api/handlers/webmail_user.go` | HIGH | All user-facing webmail endpoints (folders, messages, send, drafts). |
| 2447 | `internal/backup/service.go` | HIGH | Backup subsystem core logic — data-loss risk if broken. |
| 2438 | `internal/adminapi/server.go` | LOW (orphaned) | Legacy standalone admin server, `//go:build legacy_adminapi` gated, no references outside itself. Dead-code candidate — see Master TODO. |
| 2259 | `internal/api/handlers/enterprise_admin_v3.go` | MEDIUM-HIGH | Third generation of "enterprise admin" handlers (see Duplicate-Logic Clusters below). |
| 2086 | `internal/api/handlers/enterprise_admin.go` | MEDIUM-HIGH | First generation, still present alongside v3. |
| 1537 | `internal/api/router.go` | HIGH | Every route in the system is registered here. Contains one confirmed routing bug (below). |
| 1487 | `internal/models/models.go` | HIGH | GORM struct definitions, auto-migrated on startup. |
| 1457 | `internal/models/postgres_migrations.go` | HIGH | ~65 raw-SQL `CREATE TABLE` statements — the actual schema source of truth for most tables. |
| 1286 | `internal/updater/runtime.go` | HIGH | Self-update logic; a bad path here can brick a production install. |
| 1091 | `internal/coremail/imap/commands.go` | HIGH | IMAP protocol command implementations. |

---

## Confirmed defects (evidence-based)

### 1. Router copy-paste bug — `GET /mailboxes` wired to the wrong handler — **FIXED 2026-07-23**
- **File:** `internal/api/router.go` (was line ~1011)
- **Original evidence:** `admin.Get("/mailboxes", r.h.ListUsers)` — the mailbox-listing route called `ListUsers`, not a mailbox handler. Copy-paste of the line above it (`/users`).
- **Correction to prior note:** the "admin" group has an **empty URL prefix** — the live route is `GET /api/v1/mailboxes`, not `/api/v1/admin/mailboxes` as informally described in earlier audit passes.
- **Fix:** added a new `ListMailboxes` handler (`internal/api/handlers/handlers.go`), tenant-scoped via the same `isSuperRole`/`scopedTenantID` convention as `GetMailbox`, and repointed the route at it.
- **Verified by:** `TestAdminMailboxesRoute_ReturnsMailboxesNotUsers` (`internal/api/handlers/enterprise_mutation_smoke_test.go`).
- **New finding surfaced while fixing this:** `ExportMailboxesCSV` (same file) has **no tenant scoping at all** — a separate, still-open issue, tracked in `docs/MASTER_TODO.md`.

### 2. Schema-missing tables (four confirmed) — **ALL RESOLVED 2026-07-23**

| Table | Referenced by | Resolution |
|---|---|---|
| `organizations` | `requireTenantActive` middleware | **Fixed** — repointed the query at `tenants` (the real tenant-status table; see `internal/admin/organization/repository.go:43` for the established "organization IS a tenant" convention). No new table created. Also fixed a co-located GORM `.Raw().Scan()` nil-pointer panic bug found while diagnosing this. See `docs/DECISIONS.md`. |
| `coremail_groups` | `ListGroups`, `CreateGroup`, `DeleteGroup` | **Fixed** — migration added to both `internal/models/models.go` (SQLite) and `internal/models/postgres_migrations.go` (PostgreSQL), modeled on the existing `coremail_domain_groups` convention. |
| `coremail_group_members` | `AddGroupMember`, `RemoveGroupMember` | **Fixed** — same migration pass, modeled on `coremail_domain_group_members`. |
| `queue_attempts` | queue-detail attempt history | **Fixed differently than expected** — confirmed via `grep` that nothing anywhere writes to `queue_attempts`; it was dead/never-implemented. The real, actively-written table is `coremail_delivery_attempts` (written by `internal/coremail/delivery/history.go`), which already had ready DDL (`AttemptHistoryTable()`/`AttemptHistoryIndexes()`) that was simply never invoked for SQLite. Repointed `admin_queue.go` at the real table and wired its DDL into the SQLite bootstrap path. No `queue_attempts` table was created. |

**Impact of the fixes:** All four issues verified resolved via new tests in `internal/api/handlers/enterprise_mutation_smoke_test.go`: `TestRequireTenantActive_ActiveTenantSucceeds`, `TestRequireTenantActive_InactiveTenantRejected`, `TestRequireTenantActive_MissingTenantRejectedSafely`, `TestEnterpriseGroups_FullRouterRoundTrip`, `TestGroupsSchema_MigrationCreatesRequiredTables`. Full Go test suite confirmed green after all changes (see `docs/PROJECT_AUDIT_REPORT.md` for exact validation results).

**A latent bug surfaced by fixing the schema:** `ListGroups`' SQL selected 4 columns but scanned only 3 destinations — invisible while the table didn't exist (query always failed earlier), now fixed alongside the schema addition.

### 3. `context.TODO()` in production POP3 server code
- **File:** `internal/coremail/pop3/server.go:244,256`
- **Impact:** no request-scoped context/cancellation plumbed through these paths — a real (if minor) tech-debt item, not a placeholder comment.
- **Status:** unfixed, low priority.

### 4. Stale legacy vacation-reply path referenced as still-deprecated
- **File:** `internal/coremail/storage/rules.go:97,103`
- **Evidence:** `// Deprecated: use ClaimVacationReply for production paths`
- **Impact:** unclear whether the old path is still reachable from any live route — needs a follow-up trace before deciding to delete.

### 5. Webmail SPA possibly calling a stale endpoint
- **File:** `web/webmail/src/components/EmailList.tsx:28`
- **Evidence:** calls `/api/v1/queue?folder=...`; the router serves live mail listing at `/api/v1/webmail/messages` (`router.go:757`) instead.
- **Impact:** needs verification — may be intentional (queue = outbound status view) or a genuine stale reference. Flagged, not confirmed as broken.

---

## Duplicate-Logic Clusters (naming-smell, not confirmed duplication)

These are candidates for consolidation review — flagged by suspicious naming/versioning, not deep-diffed line-by-line in this pass.

1. **Three generations of "enterprise admin" handlers coexisting:** `enterprise_admin.go` (2086 lines) → `enterprise_admin_v3.go` (2259 lines, no `v2` file found) → `enterprise_admin_ssl.go`, `enterprise_parity.go`. The existence of `enterprise_parity.go` (added to reconcile a competitive feature-parity audit — see `docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md`) suggests real divergence between generations.
2. **Four separate "admin at different scope" handler files:** `domain_admin.go`, `organization_admin.go`, `platform_admin.go`, `saas_admin.go` — worth checking for overlapping CRUD/tenant-scoping logic that could share a helper.
3. **`dns_ops.go` + `dns_ops_safety.go`** — safety logic possibly duplicated between the two rather than composed.
4. **Three separate licensing packages** (`internal/license/`, `internal/licensing/`, `internal/licensingauthority/`) with overlapping names and no single obvious owner — a maintainability smell even if each is individually well-tested.

---

## Test Coverage Signals

- **238** `*_test.go` files vs **340** non-test `.go` files under `internal/`+`cmd/` — roughly 0.70 test-files-per-source-file (breadth signal, not depth).
- `internal/coremail/` — all 15 subpackages have at least one test file. Thinnest coverage (1 test file each, disproportionate to their security/compliance role): `dkim`, `spf`, `dmarc`, `antispam`, `push`, `rules`.
- The customer-mailbox IDOR fix in this session added the first dedicated tenant-isolation tests for `customer_mail.go`'s alias/group endpoints (`internal/api/handlers/customer_mail_tenant_isolation_test.go`, 12 tests) — previously zero coverage existed for these four handlers.

---

## Orphaned / Dead-Code Candidates

Confirmed via zero external import references outside their own package:

| Package | Why flagged |
|---|---|
| `internal/adminapi/` | `//go:build legacy_adminapi` gated; only self-references remain in the live tree |
| `internal/ldap/` | Single file, zero references anywhere else in the repo |
| `internal/pgbackup/` | Zero external references |
| `internal/releasebundle/` | Test-only package, zero external references |
| `internal/storage/loadtest/` | Test-only load-test helper, zero external references |

None have been deleted — per this task's non-negotiable rule ("never delete production code unless it is proven dead and unused"), these are flagged for a dedicated follow-up verification pass (confirm zero references via `go build` with them excluded, or a build-tag check) before removal. See `docs/MASTER_TODO.md`.

---

*Companion documents: `docs/PROJECT_MAP.md` (directory-level overview), `docs/FEATURE_MATRIX.md`, `docs/MASTER_TODO.md`, `docs/PROJECT_AUDIT_REPORT.md`.*
