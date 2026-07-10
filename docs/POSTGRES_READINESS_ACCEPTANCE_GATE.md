# PostgreSQL Readiness Acceptance Gate

Each gate must PASS before the next gate can be attempted.
Gates 1-3 must pass in CI. Gates 4-7 require staging hardware.

---

## Gate 1 — Normal Go test suite

| Field | Value |
|-------|-------|
| **Command** | `go test ./... -timeout=600s` |
| **Expected** | All packages PASS, zero failures |
| **Status** | PASS |
| **Date** | 2026-07-10 |
| **Note** | Re-verified after CTO-review blocker fixes (bootstrap booleans, RETURNING id, MFA column additions). |

## Gate 2 — SQLite benchmark 10k

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=sqlite ORVIX_LOADTEST_ROWS=10000 go test -v -timeout=5m ./internal/storage/loadtest/ -run "SchemaCompat\|Benchmark"` |
| **Expected** | All tests PASS with real metrics |
| **Status** | PASS |
| **Date** | 2026-07-09 |
| **Insert rate** | 19,059 rows/sec |
| **List latency** | 1.92ms avg |
| **Cursor pagination** | 558µs avg/page |
| **Flag update** | 268µs avg/update |

## Gate 3 — PostgreSQL schema smoke

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_POSTGRES_SCHEMA_TEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN=<dsn> go test -v -timeout 2m ./internal/models/ -run TestPostgresProductionSchemaCompat` |
| **Expected** | All 59 tables + indexes created and verified. NO public tables dropped. |
| **Status** | PASS — local Docker PostgreSQL 16 |
| **Date** | 2026-07-10 |
| **Duration** | 1.809s |

## Gate 4 — PostgreSQL benchmark 10k

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN=<dsn> ORVIX_LOADTEST_ROWS=10000 go test -v -timeout 10m ./internal/storage/loadtest/ -run "SchemaCompat\|Benchmark"` |
| **Expected** | Insert, list, cursor, flag-update tests all PASS with PostgreSQL metrics |
| **Status** | PASS — local Docker PostgreSQL 16 |
| **Date** | 2026-07-10 |
| **Duration** | 3.350s |
| **Insert rate** | 24,202 rows/sec |
| **List latest** | 5.10ms avg latency |
| **Cursor pagination** | 1.35ms avg per page |
| **Flag updates** | 2.67ms avg per update |

## Gate 5 — PostgreSQL benchmark 100k

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN=<dsn> ORVIX_LOADTEST_ROWS=100000 go test -v -timeout 30m ./internal/storage/loadtest/ -run "Benchmark"` |
| **Expected** | Insert rate sustained at 100k rows |
| **Status** | PASS — local Docker PostgreSQL 16 |
| **Date** | 2026-07-10 |
| **Duration** | 13.849s |
| **Insert rate** | 35,432 rows/sec |
| **List latest** | 3.78ms avg latency |
| **Cursor pagination** | 4.05ms avg per page |
| **Flag updates** | 2.69ms avg per update |

## Gate 5b — Pre-3M smoke: 1,000,000 rows

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN=<dsn> ORVIX_LOADTEST_ROWS=1000000 go test -v -timeout 30m ./internal/storage/loadtest/ -run "Benchmark"` |
| **Expected** | Sustained insert rate at 1M rows, list/pagination/flag queries remain fast |
| **Status** | PASS — local Docker PostgreSQL 16 |
| **Date** | 2026-07-10 |
| **Duration** | 116.842s |
| **Insert rate** | 33,523 rows/sec |
| **List latest** | 5.59ms avg latency |
| **Cursor pagination** | 876µs avg per page |
| **Flag updates** | 2.67ms avg per update |

## Gate 6 — PostgreSQL benchmark 3M

| Field | Value |
|-------|-------|
| **Command** | Separate runs for insert, list, cursor, flags (see evidence section below) |
| **Expected** | 3M rows inserted and queried. Metrics recorded. |
| **Status** | PASS WITH NOTE — local Docker PostgreSQL 16 |
| **Date** | 2026-07-10 |
| **Insert (3M)** | 33,489 rows/sec, 1m29.581s elapsed |
| **List latest (3M)** | 41.75ms avg latency, 2,000 queries |
| **Cursor pagination (3M)** | 2.33ms avg per page, 501 pages |
| **Flag updates (3M)** | 2.99ms avg per update, 1,000 ops |
| **Note** | Windows PowerShell I/O timing issue caused "Test I/O incomplete" warning after 24m — NOT a PostgreSQL query failure. PG activity inspection confirmed no blocked queries. |

## Gate 7 — DML compatibility audit

| Field | Value |
|-------|-------|
| **Document** | `docs/POSTGRES_DML_COMPATIBILITY_AUDIT.md` |
| **Expected** | All findings documented. Known blockers fixed or explicitly listed. No surprises. |
| **Status** | PASS — 11 findings audited. Fixed: sqlite_master, datetime('now') DML in trust/tls/monitoring/backup/lifecycle/MFA, INSERT OR REPLACE, INTEGER-as-boolean, CURRENT_TIMESTAMP, last_insert_rowid(). Partially fixed: `?` placeholders in core packages. Not fixed: handlers/coremail placeholders, transaction boundaries, LIMIT/OFFSET scaling. |
| **Date** | 2026-07-10 |

## Gate 8 — Migration / backup / rollback

| Field | Value |
|-------|-------|
| **Document** | `docs/POSTGRES_MIGRATION_RUNBOOK.md`, `docs/POSTGRES_ENTERPRISE_FOUNDATION.md` |
| **Expected** | Executable migration CLI exists. Dry-run lists row counts. Core metadata tables can be migrated. Backup/restore/rollback documented. Row counts verified. |
| **Status** | PARTIAL — `orvix migrate` CLI exists with dry-run and core-table migration. SQLite backup commands documented. PostgreSQL logical backup and rollback flow documented. Full migration of messages/attachments/queue and end-to-end restore/rollback validation NOT COMPLETE. |
| **Date** | 2026-07-10 |

## Gate 9 — PostgreSQL DML integration tests

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_POSTGRES_DML_TEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN=<dsn> go test -v ./internal/trust ./internal/models -run "Postgres\|DML"` |
| **Expected** | Trust repository upsert, datetime/now replacement, placeholder helper, and schema compatibility all pass on PostgreSQL. SQLite equivalents still pass without env vars. |
| **Status** | ADDED / NOT RERUN — tests written in `internal/trust/repository_dml_test.go` and `internal/models/postgres_dml_test.go`. SQLite path passes. PostgreSQL path was NOT executed in PR #10 because Docker was unavailable. |
| **Date** | 2026-07-10 |

---

## Summary

| Gate | Status | Blocker |
|------|--------|---------|
| 1 — Normal test suite | PASS (re-verified after CTO-review fixes) | — |
| 2 — SQLite benchmark 10k/100k | PASS (re-verified) | — |
| 3 — PostgreSQL schema smoke | PASS (previous sprint) — NOT RERUN | Docker unavailable this sprint |
| 4 — PostgreSQL benchmark 10k | PASS (previous sprint) — NOT RERUN | Docker unavailable this sprint |
| 5 — PostgreSQL benchmark 100k | PASS (previous sprint) — NOT RERUN | Docker unavailable this sprint |
| 5b — Pre-3M 1M smoke | PASS (previous sprint) — NOT RERUN | Docker unavailable this sprint |
| 6 — PostgreSQL benchmark 3M | PASS with note (previous sprint) — NOT RERUN | Docker unavailable this sprint |
| 7 — DML audit | PASS — 12 findings (Finding 12 added) | — |
| 8 — Migration/backup/rollback | STILL NOT RUN | Docker daemon unavailable |
| 9 — PostgreSQL DML tests | STILL NOT RUN | Docker daemon unavailable |

**Overall status:** Gates 1-2 PASS (re-verified in `db/postgres-final-closure`). Gates 3-6 PASS (previous sprint; NOT RERUN — Docker unavailable). Gate 7 PASS (updated with Finding 12 and fixed handlers). Gates 8-9 STILL NOT RUN (Docker daemon unavailable).

| Decision | Verdict | Reason |
|----------|---------|--------|
| Code blockers from CTO review | **FIXED** | Bootstrap boolean inserts, RETURNING id, MFA columns, handler boolean scans/literals fixed. |
| SQLite tests | **PASS** | Full suite and SQLite benchmarks pass. |
| Full test suite | **PASS** | `go test ./...` passes after fixes. |
| Safe to merge | **NO** | PostgreSQL validation gates (8-9) not executed. |
| Safe to deploy | **NO** | PostgreSQL production gates not validated. |
| PostgreSQL staging-ready | **NO** | Docker/PostgreSQL gates not run this sprint. |
| PostgreSQL production-ready | **NO** | Migration, backup/restore, rollback, and DML integration tests not executed. |

**Production PostgreSQL is NOT ready.** RC4 SQLite default is unchanged. VPS not touched.

---

## Local PostgreSQL Gate Evidence — 2026-07-10

All PostgreSQL gates were run against local Docker PostgreSQL 16
(`postgres:16-alpine` via `tools/postgres-staging/docker-compose.yml`).

**Environment:**
- Docker Desktop 29.5.2
- PostgreSQL image: postgres:16-alpine
- Container: orvix-pg-staging
- Host port: 5433
- Windows 11, local SSD

### Gate 3 — Schema smoke

```
ORVIX_RUN_POSTGRES_SCHEMA_TEST=1 ORVIX_DB_DRIVER=postgres
go test -v -timeout 10m ./internal/models/ -run TestPostgresProductionSchemaCompat
```
- **Result:** PASS in 1.809s
- All 59 tables created and verified via information_schema
- All critical indexes verified via pg_indexes
- Inserted/queried representative row
- Isolated schema dropped cleanly via DROP SCHEMA CASCADE

### Gate 4 — PostgreSQL benchmark 10k

```
ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_LOADTEST_ROWS=10000
go test -v -timeout 10m ./internal/storage/loadtest/ -run "SchemaCompat|Benchmark"
```
- **Result:** PASS in 3.350s
- Insert: 24,202 rows/sec
- List latest: 5.10ms avg latency (200 queries)
- Cursor pagination: 1.35ms avg per page
- Flag updates: 2.67ms avg per update (1,000 ops)

### Gate 5 — PostgreSQL benchmark 100k

```
ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_LOADTEST_ROWS=100000
go test -v -timeout 30m ./internal/storage/loadtest/ -run "Benchmark"
```
- **Result:** PASS in 13.849s
- Insert: 35,432 rows/sec
- List latest: 3.78ms avg latency
- Cursor pagination: 4.05ms avg per page
- Flag updates: 2.69ms avg per update

### Gate 5b — 1M pre-3M smoke

```
ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_LOADTEST_ROWS=1000000
go test -v -timeout 30m ./internal/storage/loadtest/ -run "Benchmark"
```
- **Result:** PASS in 116.842s
- Insert: 33,523 rows/sec
- List latest: 5.59ms avg latency
- Cursor pagination: 876us avg per page
- Flag updates: 2.67ms avg per update

### Gate 6 — 3M benchmark

**3M Insert:**
```
ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_LOADTEST_ROWS=3000000
ORVIX_LOADTEST_MAILBOXES=100 ORVIX_LOADTEST_BATCH_SIZE=1000
go test -v -timeout 2h ./internal/storage/loadtest/ -run BenchmarkInsert
```
- **Result:** PASS
- Rows: 3,000,000
- Mailboxes: 100, Batch size: 1000
- Elapsed: 1m29.581s
- Insert rate: 33,489 rows/sec

**3M List latest:**
- Queries: 2,000
- Avg latency: 41.75ms
- Wall: 974ms

**3M Cursor pagination:**
- Pages: 501
- Total: 1.169s
- Avg/page: 2.33ms
- Package time: 87.636s

**3M Flag updates:**
- Ops: 1,000
- Avg latency: 2.99ms
- Wall: 780ms
- Package time: 79.658s

**Note on 3M run:**
The combined 3M go test run ended with "Test I/O incomplete 24m0s
after exiting. exec: WaitDelay expired before I/O complete". This was
caused by Windows PowerShell terminal I/O / Select-mode interruption,
not by PostgreSQL query failure. PostgreSQL activity inspection showed
no active blocked query except the inspection query itself.

**Container cleanup:**
PostgreSQL container stopped and removed after test. Existing
containers (backend-postgres-1, backend-redis-1) were not touched.

---

## CTO-Review Blocker Fixes

**Date:** 2026-07-10

The following PostgreSQL compatibility blockers were identified during CTO review and are now **FIXED**:

### Bootstrap boolean inserts (`cmd/orvix/main.go`)

Seed/admin insertion queries were passing integer literals (`1`/`0`) for `BOOLEAN` columns under PostgreSQL, causing type errors.

| Column | Table | PostgreSQL branch | SQLite branch |
|--------|-------|-------------------|---------------|
| `active` | `tenants` | `true` / `false` bool parameters | `1` / `0` int parameters |
| `active` | `users` | `true` / `false` bool parameters | `1` / `0` int parameters |
| `email_verified` | `users` | `true` / `false` bool parameters | `1` / `0` int parameters |
| `is_admin` | `coremail_mailboxes` | `true` / `false` bool parameters | `1` / `0` int parameters |

### LastInsertId / `RETURNING id` (`cmd/orvix/main.go`)

Bootstrap code used `Exec` + `LastInsertId()`, which fails on PostgreSQL (`LastInsertId` is not supported by `lib/pq`).

| Driver | Pattern |
|--------|---------|
| PostgreSQL | `INSERT ... RETURNING id` with `QueryRow().Scan(&id)` |
| SQLite | `Exec` + `LastInsertId()` (unchanged) |

### Login MFA scan type (`internal/api/handlers/handlers.go`)

`mfaEnabled` was declared as `int` and scanned into a bool target, causing a scan type mismatch on PostgreSQL. Changed to `bool`.

### Missing MFA columns in PostgreSQL users table

`internal/models/postgres_migrations.go` was missing several MFA-related columns present in the SQLite schema. Added to the PostgreSQL `users` table:

- `mfa_enabled`
- `mfa_secret`
- `pending_mfa_secret`
- `pending_mfa_secret_raw`
- `mfa_secret_raw`
- `mfa_label`

### Additional boolean literal / scan fixes

| File | Fix |
|------|-----|
| `internal/api/handlers/webmail_auth.go` | `isAdmin` and `allowWebmail` scan as `bool`; `COALESCE(allow_webmail, TRUE/FALSE)` uses dialect literals. |
| `internal/api/handlers/handlers.go` | `allow_*` flags scan as `bool`; `boolToInt` removed (now returns `bool`). |
| `internal/api/handlers/admin_users.go` | `active = TRUE/FALSE` with dialect literals; placeholders fixed. |
| `internal/api/handlers/dns_ops.go` | `enabled = TRUE/FALSE` with dialect literals; placeholders fixed. |
| `internal/api/handlers/enterprise_admin_v3.go` | `boolToInt` returns `bool` for PostgreSQL compatibility. |

### Verification after fixes

- `go vet ./...`: PASS
- `go build ./...`: PASS
- `go test ./... -timeout=600s`: PASS
- SQLite benchmark 10k/100k: PASS
- PostgreSQL gates (3-6, 8-9): STILL NOT RUN — Docker daemon unavailable.

---

## db/postgres-final-closure Sprint Update

**Date:** 2026-07-10

### Workstream A — Raw SQL compatibility (COMPLETED)
- Added `dialect *dbdialect.Info` field to Handler struct in handlers.go
- Converted ALL raw `?` placeholders in handlers.go (31+ occurrences) to `h.dialect.Placeholder(N)`
- Fixed boolean literals: `COALESCE(mfa_enabled, 0)` → `h.dialect.FalseLiteral()`, `COALESCE(allow_webmail, 1)` → `h.dialect.TrueLiteral()`, `is_admin = 0/1` → `FalseLiteral()/TrueLiteral()`
- Fixed admin_queue.go: dynamic WHERE clause placeholders + LIMIT/OFFSET to `dial.Placeholder(N)`
- Fixed saas_admin.go: all raw SQL placeholders in report/overview/security/intelligence endpoints
- Fixed webmail_auth.go: all raw SQL placeholders in login/change-password/ensure-user endpoints; `isAdmin`/`allowWebmail` scan as `bool`; `COALESCE(allow_webmail, TRUE/FALSE)` via dialect literals
- Fixed admin_users.go: `INSERT OR IGNORE` → dialect-aware `Upsert` with `DO NOTHING`; `active = TRUE/FALSE` via dialect literals; placeholders fixed
- Fixed dns_ops.go: `enabled = TRUE/FALSE` via dialect literals; placeholders fixed
- Fixed enterprise_admin_ssl.go: `CURRENT_TIMESTAMP` → `time.Now().UTC()` parameter, `?` → `dial.Placeholder(N)`
- Fixed enterprise_admin_v3.go: `boolToInt` returns `bool` for PostgreSQL compatibility
- Fixed lifecycle/service.go: remaining `?` placeholders, `last_insert_rowid()` verified guarded
- Fixed audit/audit.go: Added `dialect` field to Store, `Detect()` used, all `?` → `dial.Placeholder(N)`, `LIMIT ? OFFSET ?` fixed
- Fixed messagetrace/service.go: Added `dialect` field, all `?` → `dial.Placeholder(N)`, `LIMIT ? OFFSET ?` fixed
- Fixed cmd/orvix/main.go: `?` → `dial.Placeholder(N)` in seedAdminUser, verifyHash, insertBootstrapAdmin, provisionSystemFoldersTx; bootstrap booleans use bool params for PostgreSQL / int params for SQLite; `INSERT ... RETURNING id` for PostgreSQL, `LastInsertId` for SQLite

### Workstream B — CoreMail audit (COMPLETED)
- Audited coremail storage/queue/mailbox/domain/alias packages
- Confirmed intentionally SQLite-only (use sql.Open("sqlite"), PRAGMA, AUTOINCREMENT)
- Added Finding 12 to POSTGRES_DML_COMPATIBILITY_AUDIT.md

### Workstream C — Transaction audit (COMPLETED)
- Created POSTGRES_TRANSACTION_AUDIT.md (14 packages, 13 findings)
- Fixed `INSERT OR IGNORE` in admin_users.go (dialect-aware Upsert)

### Workstream D — Schema completeness / MFA columns (COMPLETED)
- Added missing MFA columns to PostgreSQL `users` table in `internal/models/postgres_migrations.go`: `mfa_enabled`, `mfa_secret`, `pending_mfa_secret`, `pending_mfa_secret_raw`, `mfa_secret_raw`, `mfa_label`

### Workstream E — PostgreSQL DML tests (NOT EXECUTED)
- Docker daemon NOT running — PostgreSQL tests NOT EXECUTED

### Workstream F — Migration CLI (NOT EXECUTED)
- Requires PostgreSQL

### Workstream G — Backup/restore/rollback (NOT EXECUTED)
- Requires PostgreSQL

### Workstream H — Load gates (PARTIAL)
- 10,000 rows: PASS on SQLite (insert=691ms, flag-updates=34ms)
- 100,000 rows: PASS on SQLite (insert=6.6s, flag-updates=39ms)
- 1M/3M: NOT RERUN IN THIS SPRINT

### Verification
- `go vet ./...`: PASS
- `go build ./...`: PASS
- `go test ./... -timeout=600s`: PASS
- `go test ./cmd/orvix/ -timeout=120s`: PASS
- `go test ./internal/api/handlers/ -timeout=300s`: PASS
- `go test ./internal/audit/ ./internal/messagetrace/ ./internal/lifecycle/`: PASS
- `go test ./internal/storage/loadtest/` (10k + 100k): PASS
- PostgreSQL schema smoke: NOT RUN (Docker unavailable)
- PostgreSQL DML tests: NOT RUN (Docker unavailable)

---

**Last updated:** 2026-07-10 (`db/postgres-final-closure` sprint)
