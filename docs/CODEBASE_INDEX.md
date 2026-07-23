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

### 1. Router copy-paste bug — `GET /admin/mailboxes` wired to the wrong handler
- **File:** `internal/api/router.go:1011`
- **Evidence:** `admin.Get("/mailboxes", r.h.ListUsers)` — the mailbox-listing route calls `ListUsers`, not a mailbox handler. Looks like a copy-paste of the line above it (`/admin/users`).
- **Impact:** `GET /api/v1/admin/mailboxes` returns a user list instead of mailboxes. A separate, correct `GetMailbox`-style handler exists (`router.go:1020`, `/mailboxes/:id`), so only the collection-list route is affected.
- **Status:** confirmed, unfixed. See `docs/MASTER_TODO.md`.

### 2. Schema-missing tables (four confirmed)
Referenced by production handler code via raw SQL; no `CREATE TABLE` exists anywhere in `internal/models/` or elsewhere in non-test code. Every call to the affected endpoints returns a database driver error ("no such table" / relation does not exist) in every environment, including fresh Postgres installs.

| Table | Referenced by | Evidence |
|---|---|---|
| `organizations` | `requireTenantActive` middleware | `internal/api/router.go:890` — `SELECT active FROM organizations WHERE id = ?`; real tenant-status data lives in the `tenants` table instead (`internal/admin/organization/repository.go:43`) |
| `coremail_groups` | `ListGroups`, `CreateGroup`, `DeleteGroup` | `internal/api/handlers/customer_mail.go` |
| `coremail_group_members` | `AddGroupMember`, `RemoveGroupMember` | `internal/api/handlers/customer_mail.go` |
| `queue_attempts` | queue-detail attempt history | `internal/api/handlers/admin_queue.go:205` — migrations only define a differently-named `coremail_delivery_attempts`, suggesting a rename drift rather than a genuinely new feature |

**Practical impact today:** `requireTenantActive` silently defaults every enterprise mutation attempt to "inactive" and returns 403 — or, when combined with other issues, panics with a nil-pointer 500 (confirmed empirically during the customer-mailbox IDOR fix in this session: a `POST /api/v1/enterprise/aliases` through the full router panics before reaching handler code). The customer Groups feature (`coremail_groups`/`coremail_group_members`) is non-functional in every environment. `queue_attempts` detail views are non-functional.

**Status:** confirmed, unfixed, tracked in `docs/MASTER_TODO.md`. Fixing requires a schema migration — deliberately out of scope for the tenant-authorization fix already applied in `customer_mail.go` (commit `9bee80e`), which added correct tenant-scoping to `DeleteAlias`/`DeleteGroup`/`AddGroupMember`/`RemoveGroupMember` without attempting to fix the underlying missing-table bug.

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
