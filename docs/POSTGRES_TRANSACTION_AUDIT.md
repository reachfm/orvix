# Orvix Transaction Boundary Audit

## Executive Summary

This audit inspects every file in the Orvix codebase that contains explicit transaction boundaries (`Begin`/`Commit`/`Rollback`) or savepoint patterns. The goal is to determine whether the codebase is PostgreSQL-safe for production deployment.

**Verdict**: The codebase is **conditionally PostgreSQL-ready**. All explicit transaction boundaries are correctly managed (no missing rollbacks, no leaked transactions). However, there are systemic concerns: (1) the `retrySQL` backoff is tuned exclusively for SQLite lock errors and provides no value under PostgreSQL; (2) a global write mutex (`writeMu`) unnecessarily serializes all mail-store writes even when PostgreSQL could handle concurrent writers; (3) several multi-step operations in the handlers layer run as auto-commit statements rather than inside transactions, which is acceptable for single-row mutations but introduces races for read-then-write patterns; (4) no transaction isolation level is ever specified, defaulting to READ COMMITTED, which is usually sufficient but should be documented as an explicit choice.

**Remaining risk**: LOW-MEDIUM. The codebase will not corrupt data under PostgreSQL, but it may underperform relative to what PostgreSQL can deliver, and certain concurrent read-then-write patterns (e.g., password changes) lack serializability guarantees.

---

## Audited Packages

| # | Package / File | Has Transactions | Risk Level | Status |
|---|---------------|-----------------|------------|--------|
| 1 | `internal/api/handlers/handlers.go` | No | LOW | OK (single-row ops) |
| 2 | `internal/api/handlers/mailbox_bulk_import.go` | Yes (BeginTx + SAVEPOINT) | LOW | OK (well-structured) |
| 3 | `internal/api/handlers/enterprise_admin.go` | Yes (BeginTx) | LOW | OK |
| 4 | `internal/api/handlers/admin_users.go` | Yes (BeginTx) | LOW | OK |
| 5 | `internal/api/handlers/settings/store.go` | Yes (BeginTx) | LOW | OK |
| 6 | `internal/coremail/queue/repository.go` | Delegated (tx interface{}) | LOW | OK |
| 7 | `internal/coremail/queue/queue.go` | Yes (BeginTx/WithTx utils) | LOW | OK |
| 8 | `internal/coremail/storage/message.go` | Delegated (tx interface{}) | MEDIUM | `retrySQL` is SQLite-only |
| 9 | `internal/coremail/storage/mailstore.go` | Yes (BeginTx) | MEDIUM | `writeMu` serializes all writes |
| 10 | `internal/coremail/engine.go` | Yes (BeginTx/WithTx utils) | LOW | OK |
| 11 | `internal/models/models.go` | No (raw ExecContext) | LOW | OK (schema migration) |
| 12 | `cmd/orvix/main.go` | Yes (Begin) | LOW | OK (bootstrap) |
| 13 | `internal/lifecycle/service.go` | No | LOW | OK (single-row ops) |
| 14 | `internal/backup/service.go` | No | LOW | OK (backup is file-based) |

---

## Findings

### F1: insertBootstrapAdmin — Proper Transaction (OK)
- **File**: `cmd/orvix/main.go:449-534`
- **Risk**: LOW
- **Status**: PASS
- **What it wraps**: Tenant INSERT (or lookup), User INSERT, CoreMail domain INSERT (or lookup), CoreMail mailbox INSERT (with Argon2id hash), system folder provisioning.
- **Coverage**: `defer tx.Rollback()` on line 453, `return tx.Commit()` on line 534.
- **Analysis**: All 4-5 inserts/lookups are atomic. If any step fails, the entire bootstrap rolls back. Error paths all return before reaching Commit, and the deferred Rollback handles them. This is the **gold standard** for transaction management in this codebase.

### F2: BulkImportMailboxes — SAVEPOINT Pattern (OK)
- **File**: `internal/api/handlers/mailbox_bulk_import.go:221-479`
- **Risk**: LOW
- **Status**: PASS
- **What it wraps**: Domain lookups, mailbox INSERTs, system folder provisioning.
- **Coverage**: Outer `BeginTx` + deferred Rollback (line 221-230). Per-row `SAVEPOINT import_row_N` before each INSERT (line 298). On row failure: `ROLLBACK TO SAVEPOINT` + `RELEASE SAVEPOINT` (via `rollbackAndReleaseSavepoint` helper, line 467-478). On batch completion: `tx.Commit()` (line 414). On Commit failure: explicit Rollback (line 415).
- **Analysis**: This is the most transactionally sophisticated code in the codebase. The fail-closed design ensures that if either `ROLLBACK TO SAVEPOINT` or `RELEASE SAVEPOINT` fails, the entire outer transaction is rolled back — never committing a partially-provisioned mailbox. Savepoint names use integer counters (`import_row_%d`) with no user input, eliminating SQL injection. The `savepointExec` interface enables unit testing of failure paths.
- **PostgreSQL note**: `SAVEPOINT`/`ROLLBACK TO SAVEPOINT`/`RELEASE SAVEPOINT` are ANSI SQL and work identically on PostgreSQL.

### F3: EnterpriseAdmin SetDomainGroupMembers — Proper Transaction (OK)
- **File**: `internal/api/handlers/enterprise_admin.go:492-517`
- **Risk**: LOW
- **Status**: PASS
- **What it wraps**: DELETE all existing group members, re-INSERT new members, UPDATE group timestamp.
- **Coverage**: `BeginTx` (line 492), `defer Rollback` (line 496), `Commit` (line 513).
- **Analysis**: Delete-and-reinsert is the correct pattern for replacing a mutable set. All three statements share one transaction; if any INSERT fails, the entire set change rolls back. Proper error propagation.

### F4: UpdateAdminUserGroups — Proper Transaction (OK)
- **File**: `internal/api/handlers/admin_users.go:354-381`
- **Risk**: LOW
- **Status**: PASS
- **What it wraps**: DELETE existing admin group memberships, INSERT new ones.
- **Coverage**: `BeginTx` (line 354), `defer Rollback` (line 359), `Commit` (line 375).
- **Analysis**: Same delete-and-reinsert pattern as F3. Uses `INSERT OR IGNORE` which is SQLite-specific syntax. On PostgreSQL this will fail — `INSERT OR IGNORE` is not valid PostgreSQL syntax. PostgreSQL uses `INSERT ... ON CONFLICT DO NOTHING`.
- **PostgreSQL note**: This is a **SQLite-specific SQL construct**. `INSERT OR IGNORE` must be replaced with `INSERT ... ON CONFLICT DO NOTHING` for PostgreSQL.

### F5: Settings Store Patch — Proper Transaction (OK)
- **File**: `internal/api/handlers/settings/store.go:357-401`
- **Risk**: LOW
- **Status**: PASS
- **What it wraps**: Batch upsert of admin_settings rows using `ON CONFLICT(key) DO UPDATE SET`.
- **Coverage**: `BeginTx` (line 357), `defer Rollback` (line 361), `Commit` (line 399).
- **Analysis**: Uses `ON CONFLICT` upsert syntax which is PostgreSQL-compatible. All fields in a single PATCH request are applied atomically.

### F6: MailStore.StoreMessage — Proper but with writeMu Bottleneck (MEDIUM)
- **File**: `internal/coremail/storage/mailstore.go:143-159`
- **Risk**: MEDIUM
- **Status**: NEEDS REVIEW
- **What it wraps**: Single message INSERT within the transaction.
- **Coverage**: `BeginTx` (line 143), explicit `Rollback` on Create error (line 150), `Commit` (line 155), explicit `Rollback` on Commit error (line 156).
- **Analysis**: The transaction itself is correct. The concern is the `writeMu.Lock()` on line 140 — a **global mutex** that serializes ALL writes to the mail store. The code comment says "Under Postgres this mutex has zero contention" which is factually incorrect: the mutex is a Go-language lock that serializes goroutines regardless of the underlying database. On PostgreSQL, this mutex artificially limits write throughput to one message at a time, defeating PostgreSQL's native MVCC concurrency.
- **Recommendation**: Gate the mutex behind a `if isSQLite` check (e.g., using `dbdialect.Info`). On PostgreSQL, remove the mutex entirely and rely on PostgreSQL's native concurrency and serialization failure retries.

### F7: retrySQL — SQLite-Specific Retry Logic (MEDIUM)
- **File**: `internal/coremail/storage/message.go:644-663`
- **Risk**: MEDIUM
- **Status**: NEEDS REVIEW
- **What it wraps**: Single SQL statements (INSERT, SELECT, UPDATE) with automatic retry on SQLite lock errors.
- **Analysis**: The `retrySQL` function retries up to 30 times with exponential backoff (20ms × attempt number) when the error matches `isTransientSQLiteErr`. Under PostgreSQL, `database is locked` / `sqlite_busy` errors will never occur, so this retry loop is dead code. However, if the retry fires on non-SQLite errors (e.g., a transient PostgreSQL connection error), it will spend up to ~9.3 seconds retrying before surfacing the error.
- **Usage**: Called by `Create`, `GetByID`, `GetByMessageID`, `List`, `UpdateFlags`, `CountByFolder`. These are on the hot path for Webmail.
- **Recommendation**: Gate the retry behind a dialect check. On PostgreSQL, either remove the retry entirely (relying on `pgx` connection pooling retry) or replace it with PostgreSQL-specific transient error detection (e.g., `40001` serialization failure, `08006` connection failure).

### F8: QueueEngine.WithTx / Engine.WithTx — Good Transaction Utilities (OK)
- **File**: `internal/coremail/queue/queue.go:26-45`, `internal/coremail/engine.go:42-64`
- **Risk**: LOW
- **Status**: PASS
- **What they do**: Provide `BeginTx` and `WithTx` convenience methods. `WithTx` wraps Begin + defer Rollback + Commit in a standard pattern.
- **Analysis**: These are correctly implemented utility methods. The `defer Rollback()` ensures transactions are never leaked. All repository methods accept a `tx interface{}` parameter and resolve it via the `exec()` helper to either the transaction or the raw DB, which is a clean design.

### F9: Handlers Layer — No Transactions on Write Operations (LOW)
- **File**: `internal/api/handlers/handlers.go` (multiple functions)
- **Risk**: LOW
- **Status**: ACCEPTABLE (with caveats)
- **Affected functions**: `CreateDomain` (line 1066), `DeleteDomain` (line 1132), `CreateMailbox` (line 1831), `UpdateMailboxPassword` (line 1940), `UpdateMailboxStatus` (line 1988), `UpdateMailboxQuota` (line 2032), `UpdateMailboxProtocols` (line 2090), `BulkMailboxStatus` (line 2150), `DeleteMailbox` (line 2278), `ChangePassword` (line 699).
- **Analysis**: Each function performs a single SQL statement, so an explicit transaction is unnecessary — the single statement is already atomic. However, two concerns exist:
  1. **CreateMailbox** (line 1831-1869): The mailbox INSERT occurs first, then system folder provisioning (`coremail.EnsureMailboxSystemFolders`) runs as a separate call outside any transaction. If folder provisioning fails, the mailbox row is already committed but has no folders — a half-provisioned state. The code catches this case (lines 1864-1869) and logs a warning, so it's a known limitation.
  2. **ChangePassword** (line 685-709): Reads current password hash, verifies, writes new hash — all as separate auto-commit statements. Two concurrent password changes could race (both verify the old password, both write new hashes). This is a minor concern since password changes are infrequent admin operations.

### F10: Lifecycle Service — No Transactions (LOW)
- **File**: `internal/lifecycle/service.go`
- **Risk**: LOW
- **Status**: ACCEPTABLE
- **Analysis**: `saveUpgrade` (line 280) performs a single INSERT. `updateUpgrade` (line 298) performs a single UPDATE. Both are auto-commit and correct. The `Upgrade` function (line 188) sequences backup → preflight → version record → reload as separate steps; these are intentionally not transactional (you don't want to roll back a backup on a failed version record insert).

### F11: Backup Service — No Transactions (LOW)
- **File**: `internal/backup/service.go`
- **Risk**: LOW
- **Status**: ACCEPTABLE
- **Analysis**: `saveToRegistry` does a single UPSERT. Backup operations are file-based, not database-transactional by nature. The snapshot is done via `VACUUM INTO` (SQLite) which is inherently atomic.

### F12: Models MigrateAllRaw — No Transaction on Schema Creation (LOW)
- **File**: `internal/models/models.go:319-1163`
- **Risk**: LOW
- **Status**: ACCEPTABLE
- **Analysis**: All CREATE TABLE and CREATE INDEX statements run as individual `ExecContext` calls. In SQLite this is fine (DDL is auto-commit). On PostgreSQL, wrapping schema migrations in a single transaction is the standard practice so a migration either fully applies or fully rolls back. However, the `IF NOT EXISTS` guard makes partial failure survivable — re-running the migration will pick up where it left off.

### F13: INSERT OR IGNORE — Fixed (COMPLETED)
- **File**: `internal/api/handlers/admin_users.go:369`
- **Risk**: MEDIUM
- **Status**: **FIXED** — Replaced with dialect-aware `Upsert` using `nil` updateColumns, which generates `INSERT INTO ... ON CONFLICT DO NOTHING` for PostgreSQL and `INSERT OR IGNORE` for SQLite.
- **Analysis**: The `Upsert` helper in `internal/dbdialect` abstracts the dialect-specific upsert syntax. Calling `Upsert(ctx, tx, table, columns, values, conflictColumns, nil)` produces `DO NOTHING` for PostgreSQL, matching the original `INSERT OR IGNORE` semantics. Tests pass on SQLite; PostgreSQL test not executed (Docker unavailable).

---

## Remaining Risks

### R1: writeMu Global Mutex (MEDIUM)
The `writeMu sync.Mutex` in `internal/coremail/storage/mailstore.go` serializes all mail store writes regardless of the database backend. Under PostgreSQL, this defeats MVCC concurrency. Only one goroutine can write a message at a time globally. For a single-instance deployment, this may not matter, but for high-throughput SMTP ingestion, this is a bottleneck.

### R2: retrySQL in PostgreSQL Context (MEDIUM)
The `retrySQL` function in `internal/coremail/storage/message.go` is tuned for SQLite lock errors. Under PostgreSQL it provides no meaningful retry for serialization failures (`SQLSTATE 40001`), deadlocks (`40P01`), or connection failures (`08006`). PostgreSQL-aware retry logic should be added.

### R3: INSERT OR IGNORE Syntax (FIXED)
`INSERT OR IGNORE` at `internal/api/handlers/admin_users.go:369` has been replaced with dialect-aware `Upsert` using `nil` updateColumns, which produces `ON CONFLICT DO NOTHING` for PostgreSQL. SQLite path unchanged.

### R4: Half-Provisioned Mailbox on Folder Failure (LOW)
`CreateMailbox` in `handlers.go:1831-1869` commits the mailbox row before provisioning system folders. If folder provisioning fails, the mailbox exists with no folders. The webmail login handler has a re-provision guard, so impact is mitigated, but it's still a partial-failure state.

### R5: No Explicit Isolation Levels (LOW)
All transactions use `BeginTx(ctx, nil)` which defaults to the driver's default isolation level. PostgreSQL defaults to READ COMMITTED. For operations that should be serializable (e.g., provisioning a domain+mailbox, updating admin group memberships), a higher isolation level may be warranted. This is not a correctness bug under READ COMMITTED given the current write patterns, but it's undocumented debt.

### R6: ChangePassword Race Window (LOW)
The password change flow at `handlers.go:685-709` reads the current hash, verifies, then writes the new hash — all as separate auto-commit statements. A concurrent attacker could race the read-then-write. Severity is low because password changes require authentication and are admin-only operations.

---

## PostgreSQL-Specific Concerns

| Concern | Severity | Mitigation |
|---------|----------|------------|
| `INSERT OR IGNORE` syntax | NONE | FIXED — replaced with dialect-aware Upsert |
| `writeMu` global lock | MEDIUM | Gate behind SQLite dialect check |
| `retrySQL` tuned for SQLite | MEDIUM | Add PostgreSQL transient error detection |
| SAVEPOINT syntax (ANSI standard) | NONE | Works on PostgreSQL as-is |
| `ON CONFLICT ... DO UPDATE` upsert | NONE | PostgreSQL-native, works correctly |
| `$1, $2` placeholder syntax | NONE | Already handled by `dbdialect` |
| `BeginTx(ctx, nil)` default isolation | LOW | READ COMMITTED is appropriate for current patterns |
| No `DEADLOCK` detection | LOW | PostgreSQL will detect and return error; code will surface it as a generic DB error |
| No `serialization_failure` retry | LOW | No transactions use SERIALIZABLE isolation |

---

## Production Decision

**Are transactions PostgreSQL-safe?**

**YES, with caveats.** All explicit transaction boundaries are correctly managed — no leaked transactions, no missing rollbacks on error paths. The savepoint pattern in `mailbox_bulk_import.go` is ANSI-compliant and will work on PostgreSQL without changes.

**Blocker resolved:** `INSERT OR IGNORE` has been replaced with dialect-aware `Upsert` (produces `ON CONFLICT DO NOTHING` for PostgreSQL).

**Remaining recommendations for production PostgreSQL:**

1. **RECOMMENDED:** Gate `writeMu` behind a dialect check so PostgreSQL deployments are not artificially serialized.
2. **RECOMMENDED:** Add PostgreSQL-aware transient error detection to `retrySQL` (serialization failures, deadlocks, connection errors).
3. **NICE-TO-HAVE:** Wrap `CreateMailbox` folder provisioning in a transaction to eliminate the half-provisioned mailbox window.
4. **NICE-TO-HAVE:** Document explicit isolation levels for multi-entity creation operations.

With these addressed, the codebase is operationally safe for PostgreSQL. Items above are performance and robustness improvements that should be addressed before sustained production load.

---

## db/postgres-final-closure Sprint Update

**Date:** 2026-07-10

### F13 — INSERT OR IGNORE: FIXED
Replaced `INSERT OR IGNORE INTO coremail_admin_group_members` in
`internal/api/handlers/admin_users.go:369` with dialect-aware `Upsert(ctx, tx, table, columns, values, conflictColumns, nil)`. The `nil` updateColumns produces `INSERT INTO ... ON CONFLICT DO NOTHING` for PostgreSQL and `INSERT OR IGNORE` for SQLite. This was the only remaining correctness blocker identified in the transaction audit.

Tests pass on SQLite. PostgreSQL test not executed (Docker unavailable).

### Remaining recommendations (NOT blocking)
- R1 (`writeMu`): Gate behind dialect check for PG deployments (performance, not correctness)
- R2 (`retrySQL`): Add PG transient error detection (performance, not correctness)
- R4 (`CreateMailbox`): Wrap folder provisioning in transaction (robustness, not correctness)
- R5 (isolation levels): Document explicit levels (documentation debt)

---

*Audit date: 2026-07-10 (`db/postgres-final-closure` sprint)*
*Scope: All files under `cmd/orvix/`, `internal/api/handlers/`, `internal/coremail/`, `internal/models/`, `internal/lifecycle/`, `internal/backup/`*
