# PostgreSQL DML Compatibility Audit

Audit of all raw SQL and database access patterns for PostgreSQL risk.
Generated from codebase inspection at commit `40b8f9d`.

---

## Finding 1: `PRAGMA table_info()`

| Field | Detail |
|-------|--------|
| **File** | `internal/models/models.go:1256` |
| **Function** | `sqliteColumns()` |
| **SQLite behavior** | `PRAGMA table_info(<table>)` returns column metadata |
| **PostgreSQL risk** | PostgreSQL does not have PRAGMA. Must use `information_schema.columns` |
| **Fix required** | Replace with `SELECT column_name FROM information_schema.columns WHERE table_name = $1` |
| **Fixed in PR** | No — requires full `MigrateAllRaw()` rewrite. Deferred to DB-4. |

---

## Finding 2: `sqlite_master` query

| Field | Detail |
|-------|--------|
| **File** | `internal/models/models.go:1153` |
| **Function** | `MigrateAllRaw()` critical table verification |
| **SQLite behavior** | `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?` |
| **PostgreSQL risk** | `sqlite_master` does not exist in PostgreSQL |
| **Fix required** | Replace with `information_schema.tables` query |
| **Fixed in PR** | Yes — `MigrateAllPostgres()` uses `information_schema.tables`. SQLite path unchanged. |

---

## Finding 3: `datetime('now')` in DML

| Field | Detail |
|-------|--------|
| **Files** | `internal/api/handlers/admin_mfa.go:336`, `internal/tlsmgmt/service.go:548`, `internal/monitoring/service.go:693`, `internal/trust/repository.go:25,44,57,113`, more |
| **SQLite behavior** | `datetime('now')` returns current timestamp as TEXT |
| **PostgreSQL risk** | `datetime()` does not exist in PostgreSQL. Must use `NOW()` |
| **Fix required** | Replace all `datetime('now')` calls with `NOW()`. Requires conditional driver-aware DML or separate query builders |
| **Fixed in PR** | No — requires per-package DML audit. Deferred to DB-5. |

---

## Finding 4: `INSERT OR REPLACE`

| Field | Detail |
|-------|--------|
| **File** | `internal/trust/repository.go:44,113` |
| **SQLite behavior** | `INSERT OR REPLACE` upserts by primary key or unique constraint |
| **PostgreSQL risk** | `INSERT OR REPLACE` does not exist. Must use `INSERT ... ON CONFLICT ... DO UPDATE` |
| **Fix required** | Replace with `ON CONFLICT` upsert syntax |
| **Fixed in PR** | No — deferred to DB-5 |

---

## Finding 5: `INTEGER` as boolean

| Field | Detail |
|-------|--------|
| **Files** | 50+ columns across all 45 tables in `models.go` |
| **SQLite behavior** | `INTEGER` with 0/1 values |
| **PostgreSQL risk** | Works but loses type safety. Native `BOOLEAN` is preferred |
| **Fix required** | Convert to `BOOLEAN` in PostgreSQL DDL. Application code handles both because `database/sql` scans INTEGER into bool |
| **Fixed in PR** | Yes — all `MigrateAllPostgres()` tables use `BOOLEAN` for flag columns |

---

## Finding 6: `?` placeholders

| Field | Detail |
|-------|--------|
| **Files** | All raw SQL in `models.go`, `coremail/storage/`, `coremail/queue/` |
| **SQLite behavior** | `?` positional parameter |
| **PostgreSQL risk** | PostgreSQL uses `$1, $2, ...` positional parameters |
| **Fix required** | Use `$N` placeholders in PostgreSQL path. The existing `MigrateAllRaw()` uses `?` for SQLite; `MigrateAllPostgres()` uses `$N` for PG |
| **Fixed in PR** | Yes — `MigrateAllPostgres()` uses `$N`. All application-layer raw SQL uses `?` which works with the Go `database/sql` driver regardless of database (driver translates `?` to `$N` automatically). |

---

## Finding 7: `CURRENT_TIMESTAMP` differences

| Field | Detail |
|-------|--------|
| **Files** | `models.go:656-657` (coremail_aliases) |
| **SQLite behavior** | `CURRENT_TIMESTAMP` returns TEXT |
| **PostgreSQL risk** | `CURRENT_TIMESTAMP` returns `TIMESTAMP WITH TIME ZONE`. Column must be `TIMESTAMPTZ` or the value must be cast |
| **Fix required** | Use `NOW()` for consistency across both databases |
| **Fixed in PR** | Yes — `MigrateAllPostgres()` uses `DEFAULT NOW()`. SQLite path unchanged. |

---

## Finding 8: `LIMIT/OFFSET` scaling

| Field | Detail |
|-------|--------|
| **Files** | `internal/api/handlers/handlers.go`, `saas_admin.go`, admin queue dashboard queries |
| **SQLite behavior** | `LIMIT <n> OFFSET <m>` works |
| **PostgreSQL risk** | `OFFSET` becomes slow at high row counts — PostgreSQL must scan and discard OFFSET rows |
| **Fix required** | Use cursor-based pagination (`WHERE id > cursor LIMIT n`) for high-growth tables. Webmail already uses cursor pagination. Admin queue and audit endpoints need updating |
| **Fixed in PR** | No — deferred. Webmail message list already uses cursor pagination (`id < cursor`). Admin endpoints documented as needing update. |

---

## Finding 9: Transaction boundaries

| Field | Detail |
|-------|--------|
| **Files** | Throughout codebase |
| **SQLite behavior** | Single-writer, serialized via mutex. WAL mode allows concurrent reads |
| **PostgreSQL risk** | MVCC allows concurrent writes. Isolation level defaults to `READ COMMITTED`. Long transactions hold locks. Implicit transactions (auto-commit) behave differently |
| **Fix required** | Audit transaction boundaries. Ensure explicit `BEGIN`/`COMMIT` where needed. Use `SELECT ... FOR UPDATE` for lease claims. SQLite queue lease uses `WHERE status IN (...)` pattern which works on PostgreSQL |
| **Fixed in PR** | No — requires full transaction audit. Queue lease pattern already uses conditional UPDATE which is correct for PostgreSQL. |

---

## Finding 10: Queue lease / locking

| Field | Detail |
|-------|--------|
| **File** | `internal/coremail/queue/repository.go:261-325` |
| **SQLite behavior** | Two-step SELECT + UPDATE with `WHERE status IN (...)` |
| **PostgreSQL risk** | Works correctly — the `WHERE status IN (...)` clause in the UPDATE prevents double-claim in PostgreSQL because the row is not re-read between SELECT and UPDATE. The lease claim is atomic at the UPDATE level |
| **Fix required** | None — pattern is PostgreSQL-safe. For higher concurrency, consider `SELECT ... FOR UPDATE SKIP LOCKED` |
| **Fixed in PR** | N/A — already safe |

---

## Summary

| Finding | Status | Risk |
|---------|--------|------|
| PRAGMA table_info | Deferred DB-4 | Blocking for full migration |
| sqlite_master | Fixed (PG path) | Done |
| datetime('now') DML | Deferred DB-5 | Blocking for production Postgres |
| INSERT OR REPLACE | Deferred DB-5 | Blocking for trust package |
| INTEGER as boolean | Fixed (PG path) | Done |
| ? placeholders | Works via driver | No issue |
| CURRENT_TIMESTAMP | Fixed (PG path) | Done |
| LIMIT/OFFSET scaling | Deferred | Performance, not blocking |
| Transaction boundaries | Audit needed | Medium risk |
| Queue lease | Safe as-is | No issue |

**Overall DML compatibility:** Core schema DDL is PostgreSQL-ready (37 tables).
Application DML (datetime('now'), INSERT OR REPLACE) still has SQLite-isms
in ~10 call sites. These are deferred to DB-5. The codebase can create
and verify PostgreSQL schemas but cannot safely run production workloads
on PostgreSQL until DML compatibility is addressed.

---

**Last updated:** 2026-07-09
