# PostgreSQL DML Compatibility Audit

Audit of all raw SQL and database access patterns for PostgreSQL risk.
Updated for branch `db/postgres-production-readiness`.

---

## Finding 1: `PRAGMA table_info()`

| Field | Detail |
|-------|--------|
| **File** | `internal/models/models.go:1256` |
| **Function** | `sqliteColumns()` |
| **SQLite behavior** | `PRAGMA table_info(<table>)` returns column metadata |
| **PostgreSQL risk** | PostgreSQL does not have PRAGMA. Must use `information_schema.columns` |
| **Fix required** | Replace with `SELECT column_name FROM information_schema.columns WHERE table_name = $1` |
| **Status** | **NOT FIXED**. `sqliteColumns()` is only used by `MigrateAllRaw()` which is the SQLite-only migration path. PostgreSQL uses `MigrateAllPostgres()` which does not call `PRAGMA`. Acceptable because SQLite path remains unchanged and PostgreSQL path does not use this function. |

---

## Finding 2: `sqlite_master` query

| Field | Detail |
|-------|--------|
| **File** | `internal/models/models.go:1153` |
| **Function** | `MigrateAllRaw()` critical table verification |
| **SQLite behavior** | `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?` |
| **PostgreSQL risk** | `sqlite_master` does not exist in PostgreSQL |
| **Fix required** | Replace with `information_schema.tables` query |
| **Status** | **FIXED** — `MigrateAllPostgres()` uses `information_schema.tables`. SQLite path unchanged. |

---

## Finding 3: `datetime('now')` in DML

| Field | Detail |
|-------|--------|
| **Files** | `internal/api/handlers/admin_mfa.go:336`, `internal/tlsmgmt/service.go:548`, `internal/monitoring/service.go:693,750`, `internal/trust/repository.go:25,44,57,113` |
| **SQLite behavior** | `datetime('now')` returns current timestamp as TEXT |
| **PostgreSQL risk** | `datetime()` does not exist in PostgreSQL. Must use `NOW()` or pass `time.Now().UTC()` as a parameter |
| **Fix required** | Replace all `datetime('now')` calls with driver-aware expression or parameter |
| **Status** | **FIXED** — All listed call sites now use either `dbdialect.Info.NowExpr()` (driver-aware `NOW()` / `datetime('now')`) or `time.Now().UTC()` as a query parameter. Tests added in `internal/trust/repository_dml_test.go` and existing `internal/api/handlers/admin_mfa_test.go` cover the changed behavior on SQLite; PostgreSQL tests are gated by `ORVIX_RUN_POSTGRES_DML_TEST`. |

---

## Finding 4: `INSERT OR REPLACE`

| Field | Detail |
|-------|--------|
| **Files** | `internal/trust/repository.go:44,113`, `internal/tlsmgmt/service.go:547`, `internal/backup/service.go:408`, `internal/backup/scheduler.go:99` |
| **SQLite behavior** | `INSERT OR REPLACE` upserts by primary key or unique constraint |
| **PostgreSQL risk** | `INSERT OR REPLACE` does not exist. Must use `INSERT ... ON CONFLICT ... DO UPDATE` |
| **Fix required** | Replace with driver-aware upsert |
| **Status** | **FIXED** — All listed call sites now use `dbdialect.Info.Upsert()` which generates `INSERT ... ON CONFLICT (...) DO UPDATE` for PostgreSQL and `INSERT OR REPLACE` for SQLite. Tests added in `internal/trust/repository_dml_test.go` and existing package tests in `internal/tlsmgmt`, `internal/backup` cover SQLite behavior. |

---

## Finding 5: `INTEGER` as boolean

| Field | Detail |
|-------|--------|
| **Files** | 50+ columns across all tables in `models.go` |
| **SQLite behavior** | `INTEGER` with 0/1 values |
| **PostgreSQL risk** | Works but loses type safety. Native `BOOLEAN` is preferred |
| **Fix required** | Convert to `BOOLEAN` in PostgreSQL DDL. Application code handles both because `database/sql` scans INTEGER into bool |
| **Status** | **FIXED** — all `MigrateAllPostgres()` tables use `BOOLEAN` for flag columns. SQLite path unchanged. |

---

## Finding 6: `?` placeholders

| Field | Detail |
|-------|--------|
| **Files** | Raw SQL in `internal/trust/`, `internal/tlsmgmt/`, `internal/monitoring/`, `internal/backup/`, `internal/lifecycle/`, `internal/api/handlers/admin_mfa.go` |
| **SQLite behavior** | `?` positional parameter |
| **PostgreSQL risk** | PostgreSQL natively uses `$1, $2, ...` positional parameters. The Go `database/sql` driver does NOT automatically translate `?` to `$N` for PostgreSQL. |
| **Fix required** | Every raw SQL query must use driver-aware placeholders: `$N` for PostgreSQL, `?` for SQLite. |
| **Status** | **PARTIALLY FIXED** — All raw SQL in the packages listed above now uses `dbdialect.Info.Placeholder(n)` so the same code produces `$N` for PostgreSQL and `?` for SQLite. A large number of raw SQL queries remain in `internal/api/handlers/`, `internal/coremail/`, and other packages; these still use `?` and are **NOT YET FIXED**. The helper exists to fix them incrementally. |

### Fixed placeholder call sites

- `internal/trust/repository.go`
- `internal/tlsmgmt/service.go`
- `internal/monitoring/service.go`
- `internal/backup/service.go`
- `internal/backup/retention.go`
- `internal/backup/scheduler.go`
- `internal/lifecycle/service.go`
- `internal/api/handlers/admin_mfa.go` (changed to parameter)

### Remaining placeholder call sites (NOT FIXED)

- `internal/api/handlers/*.go` (many handlers: webmail_auth.go, handlers.go, saas_admin.go, etc.)
- `internal/coremail/storage/*.go`
- `internal/coremail/queue/*.go`
- `internal/coremail/smtp/*.go`
- `internal/coremail/rules/*.go`
- `internal/models/models_test.go` (SQLite-only tests, acceptable)

---

## Finding 7: `CURRENT_TIMESTAMP` differences

| Field | Detail |
|-------|--------|
| **Files** | `models.go` (coremail_aliases) |
| **SQLite behavior** | `CURRENT_TIMESTAMP` returns TEXT |
| **PostgreSQL risk** | `CURRENT_TIMESTAMP` returns `TIMESTAMP WITH TIME ZONE`. Column must be `TIMESTAMPTZ` or the value must be cast |
| **Fix required** | Use `NOW()` for consistency across both databases |
| **Status** | **FIXED** — `MigrateAllPostgres()` uses `DEFAULT NOW()`. SQLite path unchanged. |

---

## Finding 8: `LIMIT/OFFSET` scaling

| Field | Detail |
|-------|--------|
| **Files** | `internal/api/handlers/handlers.go`, `saas_admin.go`, admin queue dashboard queries |
| **SQLite behavior** | `LIMIT <n> OFFSET <m>` works |
| **PostgreSQL risk** | `OFFSET` becomes slow at high row counts — PostgreSQL must scan and discard OFFSET rows |
| **Fix required** | Use cursor-based pagination (`WHERE id > cursor LIMIT n`) for high-growth tables. Webmail already uses cursor pagination. Admin queue and audit endpoints need updating |
| **Status** | **NOT FIXED** — deferred. Webmail message list already uses cursor pagination. Admin endpoints documented as needing update. Not a blocker for schema/DML compatibility. |

---

## Finding 9: `last_insert_rowid()`

| Field | Detail |
|-------|--------|
| **File** | `internal/lifecycle/service.go:265` |
| **SQLite behavior** | `SELECT last_insert_rowid()` returns the last inserted rowid |
| **PostgreSQL risk** | `last_insert_rowid()` does not exist |
| **Fix required** | Use `INSERT ... RETURNING id` for PostgreSQL |
| **Status** | **FIXED** — `saveUpgrade()` now uses `INSERT ... RETURNING id` for PostgreSQL and `last_insert_rowid()` for SQLite. Existing lifecycle tests cover SQLite behavior. |

---

## Finding 10: Transaction boundaries

| Field | Detail |
|-------|--------|
| **Files** | Throughout codebase |
| **SQLite behavior** | Single-writer, serialized via mutex. WAL mode allows concurrent reads |
| **PostgreSQL risk** | MVCC allows concurrent writes. Isolation level defaults to `READ COMMITTED`. Long transactions hold locks. Implicit transactions (auto-commit) behave differently |
| **Fix required** | Audit transaction boundaries. Ensure explicit `BEGIN`/`COMMIT` where needed. Use `SELECT ... FOR UPDATE` for lease claims. |
| **Status** | **NOT FIXED** — requires full transaction audit. Queue lease pattern already uses conditional UPDATE which is correct for PostgreSQL. |

---

## Finding 11: Queue lease / locking

| Field | Detail |
|-------|--------|
| **File** | `internal/coremail/queue/repository.go:261-325` |
| **SQLite behavior** | Two-step SELECT + UPDATE with `WHERE status IN (...)` |
| **PostgreSQL risk** | Works correctly — the `WHERE status IN (...)` clause in the UPDATE prevents double-claim in PostgreSQL because the row is not re-read between SELECT and UPDATE. The lease claim is atomic at the UPDATE level |
| **Fix required** | None — pattern is PostgreSQL-safe. For higher concurrency, consider `SELECT ... FOR UPDATE SKIP LOCKED` |
| **Status** | **N/A** — already safe |

---

## Summary

| Finding | Status | Risk |
|---------|--------|------|
| PRAGMA table_info | Not fixed (SQLite-only path) | Low — PG path does not use it |
| sqlite_master | Fixed (PG path) | Done |
| datetime('now') DML | Fixed (listed call sites) | Done for listed files |
| INSERT OR REPLACE | Fixed (listed call sites) | Done for listed files |
| INTEGER as boolean | Fixed (PG path) | Done |
| ? placeholders | Partially fixed | Medium — many handlers/storage queries remain |
| CURRENT_TIMESTAMP | Fixed (PG path) | Done |
| LIMIT/OFFSET scaling | Deferred | Performance, not blocking |
| last_insert_rowid() | Fixed | Done |
| Transaction boundaries | Audit needed | Medium risk |
| Queue lease | Safe as-is | No issue |

**Overall DML compatibility:** Core schema DDL is PostgreSQL-ready (59 tables). Application DML in the trust, TLS, monitoring, backup, lifecycle, and MFA packages has been made driver-aware. A significant number of raw SQL queries in `internal/api/handlers/` and `internal/coremail/` still use `?` placeholders and would fail on PostgreSQL if executed. Those packages are not exercised by the current PostgreSQL test gates and remain **NOT FIXED**.

**Production PostgreSQL is NOT ready** until:
1. All remaining `?` placeholder queries are converted.
2. Transaction boundaries are audited for PostgreSQL.
3. Migration CLI is validated end-to-end with real data (partial — dry-run and core tables implemented).
4. Backup/restore/rollback procedures are executed and verified.
5. Staging gates (10k/100k/1M/3M) pass on PostgreSQL staging hardware.

---

**Last updated:** 2026-07-10
