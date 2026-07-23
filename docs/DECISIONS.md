# ORVIX Architecture Decisions

Permanent engineering history. Entries are never deleted, only appended or annotated as superseded.

---

### 2026-07-23 — `requireTenantActive`: repoint at `tenants`, not a new `organizations` table

**Decision:** Fix `requireTenantActive` (`internal/api/router.go`) to query the canonical `tenants` table instead of the nonexistent `organizations` table, rather than creating a duplicate `organizations` table to satisfy the broken code.

**Reason:** `internal/admin/organization/repository.go:43` already treats "organization" as a pure alias for a `tenants` row ("An organization IS a tenant" — see that file's own comment). No other code anywhere creates or expects a distinct `organizations` table. Introducing one would create two sources of truth for the same concept.

**Additional finding during the fix:** the original code used `r.db.Raw(sql).Scan(&sql.NullBool{})` (GORM-level raw scan). This pattern itself panics with a nil-pointer dereference inside `database/sql.(*Rows).Close` when used this way in this GORM version (v1.31.2) — confirmed via a stack trace captured by temporarily enabling `recover.EnableStackTrace` during debugging (reverted immediately after diagnosis, not part of the shipped fix). The fix therefore also switches to the plain `*sql.DB.QueryRow(...).Scan(...)` pattern already used consistently everywhere else in this codebase (`customer_mail.go`, `admin_queue.go`, `handlers.go`, etc.) — not just a table-name change.

**Impact:** Every `/api/v1/enterprise/*` mutation now correctly checks tenant-active status without panicking. Regression tests added: `TestRequireTenantActive_ActiveTenantSucceeds`, `TestRequireTenantActive_InactiveTenantRejected`, `TestRequireTenantActive_MissingTenantRejectedSafely` (`internal/api/handlers/enterprise_mutation_smoke_test.go`).

**Alternatives rejected:** Creating an `organizations` table as a view or alias over `tenants` — rejected as unnecessary complexity when the existing code (`organization_admin.go`, `internal/admin/organization/`) already treats them as the same concept without a second table.

---

### 2026-07-23 — `coremail_groups` / `coremail_group_members`: add real migrations

**Decision:** Add `coremail_groups` and `coremail_group_members` tables to both migration paths (`internal/models/models.go` for SQLite, `internal/models/postgres_migrations.go` for PostgreSQL), modeled directly on the existing `coremail_domain_groups`/`coremail_domain_group_members` pair for schema-convention consistency (tenant-scoped, soft-delete, `UNIQUE(tenant_id, name)` on the group, `UNIQUE(group_id, email)` on membership).

**Reason:** `ListGroups`/`CreateGroup`/`DeleteGroup`/`AddGroupMember`/`RemoveGroupMember` (`internal/api/handlers/customer_mail.go`) are live, RBAC-gated, routed endpoints with real handler logic and real customer-facing intent — not dead code. They were simply missing their schema. Deriving the schema from the domain-groups convention (rather than inventing a new shape) keeps the codebase's migration style consistent.

**Bug found and fixed in the same pass:** `ListGroups`' SQL selected 4 columns (`id, name, description, created_at`) but only scanned 3 destinations — a latent bug invisible until the table actually existed and returned a row (previously the query always failed earlier with "no such table"). Fixed by adding the missing `CreatedAt` scan destination.

**Impact:** The customer Groups feature is now functional end-to-end (verified via `TestEnterpriseGroups_FullRouterRoundTrip` and `TestGroupsSchema_MigrationCreatesRequiredTables`, both new). Duplicate group names for the same tenant are now rejected via the `UNIQUE(tenant_id, name)` constraint rather than silently accepted.

**Alternatives rejected:** None — this was a straightforward "add the missing schema" fix with no meaningful alternative design.

---

### 2026-07-23 — `queue_attempts`: repoint at `coremail_delivery_attempts`, do not create a new table

**Decision:** Do not create a `queue_attempts` table. Instead, fix `admin_queue.go`'s `AdminQueueDetail` handler to query the real, actively-written `coremail_delivery_attempts` table, and wire that table's existing (but previously uncalled) DDL (`internal/coremail/delivery.AttemptHistoryTable()`/`AttemptHistoryIndexes()`) into the SQLite bootstrap path (`ensureCoreMailBootstrapSchema` in `cmd/orvix/main.go`) — it was already present in `postgres_migrations.go` for PostgreSQL but never invoked for SQLite.

**Reason:** Evidence check (`grep -rln "INSERT INTO queue_attempts"` / `"INSERT INTO coremail_delivery_attempts"` across `internal/`) showed **zero writers** to `queue_attempts` anywhere, but `internal/coremail/delivery/history.go` actively writes attempt history to `coremail_delivery_attempts` on every delivery attempt. Creating `queue_attempts` would have produced a permanently-empty speculative table — exactly what this task's instructions explicitly warned against. The column names also differ (`queue_entry_id` not `queue_id`, `status`/`status_msg` not `result`/`error_message`, `status_code` as `INTEGER` not `TEXT`) — the original query was a rename-drift bug, not a missing-feature gap.

**Impact:** `GET /admin/queue/messages/:id` attempt-history now returns real data instead of silently-always-empty. Both dialects create the table (Postgres already did; SQLite now does too via the bootstrap wiring).

**Alternatives rejected:** Creating `queue_attempts` as specified literally — rejected per direct evidence that nothing writes to it and a differently-named, differently-shaped, actively-used table already serves the exact same purpose.

---

### 2026-07-23 — `GET /admin/mailboxes`: implement `ListMailboxes`, fix the copy-paste routing bug

**Decision:** Add a new `ListMailboxes` handler (`internal/api/handlers/handlers.go`) and repoint `admin.Get("/mailboxes", ...)` at it instead of `r.h.ListUsers`.

**Reason:** No `ListMailboxes` handler existed at all — the route was a copy-paste of the `/users` line above it in `router.go`, never corrected. `ExportMailboxesCSV` provided a query pattern to model from, but notably has **no tenant scoping at all** (a separate, out-of-scope finding — see `docs/MASTER_TODO.md`); `GetMailbox` (singular) provided the correct tenant-scoping convention (`isSuperRole(c)` bypasses scoping, otherwise filter by `scopedTenantID(c)`), which `ListMailboxes` follows.

**Impact:** `GET /api/v1/mailboxes` (note: the "admin" group has an empty URL prefix — this is not literally `/api/v1/admin/mailboxes`, a corrected understanding from this pass) now returns mailbox-shaped data (`id`, `email`, `domain`, `status`, `is_admin`, `created_at`), correctly tenant-scoped for non-super-admin callers. Verified via `TestAdminMailboxesRoute_ReturnsMailboxesNotUsers`.

**Regression caught and fixed in the same pass:** an existing test, `TestOpsV2_MailboxFilters` (`internal/api/handlers/ops_layer_v2_test.go`), already exercised `GET /api/v1/mailboxes?q=...&status=...&admin=...` expecting the `q`/`status`/`admin` filter behavior `ListUsers` provided. The first version of `ListMailboxes` dropped this filtering, breaking that test. Fixed by porting the identical filter-building logic from `ListUsers` into `ListMailboxes` before finalizing the fix — full `internal/api/handlers` package suite re-run green afterward.

**Alternatives rejected:** Reusing `ExportMailboxesCSV`'s untenanted query — rejected because it would have reintroduced a cross-tenant data leak in a different endpoint.

---

### 2026-07-23 — Delete `release/admin/CHANGELOG.md`

**Decision:** Confirm and finalize the deletion of `release/admin/CHANGELOG.md` (489 lines, legacy vanilla-JS admin console changelog) as part of the documentation-cleanup commit.

**Reason:** Verified via `grep -rln "CHANGELOG.md" release/scripts/*.sh Makefile` and a repo-wide grep for `release/admin/CHANGELOG` across `*.sh`/`*.go`/`Makefile` — zero references in any build, runtime, or release script. Not loaded by `release/admin/index.html` (which loads only the Vite SPA bundle). Not part of the intentional minimal-toolchain fallback (`release/admin/modules/`, `release/admin/app.js`, which *are* referenced by `release/scripts/lib-admin-build.sh` and were explicitly preserved). Purely historical release notes for a superseded admin-console generation.

**Impact:** 489 lines removed from tracked docs. No build, test, or runtime behavior affected — confirmed by a full green test suite and all three frontend builds both before and after this deletion was first made.

**Alternatives rejected:** Restoring the file — rejected because no evidence of current use exists; restoring a proven-dead file would just re-introduce noise without informational value (its content is fully preserved in git history if ever needed).

---

### 2026-06-06 (approx.) — Move from Stalwart-embedded mail engine to native `internal/coremail`

**Decision:** Replace the original architecture (Orvix as an orchestration layer around an embedded/managed Stalwart Mail Server subprocess) with a fully native Go mail engine (`internal/coremail`) implementing SMTP, Submission, IMAP, POP3, JMAP, DKIM/SPF/DMARC, queue, and delivery directly.

**Reason:** Not recorded in available project history — inferred from the fact that the entire original architecture (documented in the now-deleted `MVP.md`, `HANDOFF.md`, `AGENT_INSTRUCTIONS.md`, etc.) assumed Stalwart as the mail engine, while the current, actively-developed codebase has zero Stalwart runtime code and a mature native engine instead.

**Impact:** All mail-protocol code lives under `internal/coremail/`. No external mail-server dependency, no subprocess management, no REST-API/webhook bridge to a third party.

**Alternatives rejected:** Continuing to wrap Stalwart (evidenced by the parallel, unrelated `D:\Orvix Enterprise Mail` repository outside this workspace's scope, which still contains a full Stalwart integration on a different module path — treated as an abandoned parallel branch, not merged).

---

### 2026-07-23 — Stalwart zero-tolerance documentation sweep

**Decision:** Delete 14 documentation files whose entire subject matter was the obsolete Stalwart-embedded architecture (`MVP.md`, `HANDOFF.md`, `AGENT_INSTRUCTIONS.md`, `WORK_CONTEXT.md`, `VPS_TEST_PLAN.md`, `ROADMAP.md`, `RELEASE_AUDIT.md`, `PROGRESS.md`, `ORVIX_STATUS_MATRIX.md`, `ORVIX_INDEPENDENT_AUDIT.md`, `FULL_FEATURE_PLAN.md`, `AUDIT.md`, `release/RELEASE_NOTES.md`, `docs/RC4_RELEASE_ACCEPTANCE_GATE.md`). Rename `docs/ORVIX_STALWART_ENTERPRISE_PARITY_AUDIT.md` → `docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md` and scrub its Stalwart branding, since it is a current, active competitive-benchmark document, not legacy architecture documentation. Scrub comment-only Stalwart references from 10 source/script files. Explicitly preserve two protective regression tests (`installer_test.go`, `release_packaging_test.go`) that assert Stalwart is absent.

**Reason:** These 14 files could not be scrubbed of the word "Stalwart" without gutting their entire subject — they describe an architecture that no longer exists and are actively misleading (e.g. `MVP.md`'s premise was "Built on Stalwart Mail Server"). Six weeks stale (single-commit batch, 2026-06-06) relative to ongoing development.

**Impact:** 2972 lines removed from tracked documentation. Zero production code touched. Verified via full test suite (80 packages, 0 failures) and all three frontend builds passing after the change.

**Alternatives rejected:** Scrubbing the word in-place instead of deleting — rejected because it would have produced incoherent documents (e.g. `MVP.md` with "Stalwart" replaced by a placeholder throughout its 896 lines reads as nonsense, since the whole document's structure depends on that premise).

---

### 2026-07-23 — Tenant-authorization fix scoped strictly to `customer_mail.go`, not the underlying `requireTenantActive` bug

**Decision:** Fix the confirmed cross-tenant IDOR in `DeleteAlias`/`DeleteGroup`/`AddGroupMember`/`RemoveGroupMember` (`internal/api/handlers/customer_mail.go`) by adding `auth.RequireTenantID` checks and tenant-scoped SQL, without also fixing the separately-discovered bug where `requireTenantActive` (`router.go:890`) queries a nonexistent `organizations` table.

**Reason:** The two bugs are independent. Fixing `requireTenantActive` requires either a schema migration (creating `organizations`) or a router.go change (repointing the query at `tenants`) — both are out of scope for a change explicitly authorized as "only `customer_mail.go` and its directly relevant tests." Mixing them into one commit would have made the fix harder to review and revert independently.

**Impact:** The alias/group tenant-authorization fix is real and tested (12 new tests, verified to fail against the pre-fix code), but cannot currently be exercised end-to-end through the live HTTP router, because `requireTenantActive` rejects/panics on every `/enterprise` mutation regardless of this fix. Tests were therefore written at the handler level, bypassing the full router, with two test-local-only tables (`coremail_groups`, `coremail_group_members`) created since neither exists in production schema either.

**Alternatives rejected:** Fixing `requireTenantActive` in the same commit — rejected per the explicit scope boundary set for that task. Recorded here as the next required fix, tracked in `docs/MASTER_TODO.md`.

---

### 2026-07-23 — Documentation audit files (`.cleanup-audit/`, `.audit-readonly/`, `.drive-audit-readonly/`, `.project-baseline/`) kept untracked and out of the Stalwart zero-tolerance sweep

**Decision:** Treat this session's own audit-trail scratch directories as out of scope for the Stalwart-reference sweep, even though they contain historical mentions of "Stalwart" in their own narrative text.

**Reason:** These directories are meta-audit output describing the investigation itself (e.g. "confirmed zero Stalwart references remain"), not product documentation. They are untracked and were never part of the shipped codebase.

**Impact:** None on production code or shipped documentation. Noted for transparency in every relevant report produced this session.

**Alternatives rejected:** Scrubbing these too — considered unnecessary since they're not tracked, not shipped, and removing "Stalwart" from a sentence like "confirmed zero Stalwart references remain" would make the audit trail itself unreadable.

---

*Companion documents: `docs/PROJECT_MAP.md`, `docs/CODEBASE_INDEX.md`, `docs/MASTER_TODO.md`, `docs/ROADMAP.md`.*
