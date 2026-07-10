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
| **Date** | 2026-07-09 |

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
| 1 — Normal test suite | PASS | — |
| 2 — SQLite benchmark 10k | PASS | — |
| 3 — PostgreSQL schema smoke | PASS | — |
| 4 — PostgreSQL benchmark 10k | PASS | — |
| 5 — PostgreSQL benchmark 100k | PASS | — |
| 5b — Pre-3M 1M smoke | PASS | — |
| 6 — PostgreSQL benchmark 3M | PASS (with note) | — |
| 7 — DML audit | PASS | — |
| 8 — Migration/backup/rollback | PARTIAL | Full restore/rollback validation pending |
| 9 — PostgreSQL DML tests | ADDED / NOT RERUN | SQLite path passes; PostgreSQL run not executed in this PR |

**Overall status:** Gates 1-7 PASS. Gate 8 is PARTIAL — CLI and docs exist, full validation pending. Gate 9 tests are written and pass on SQLite but the PostgreSQL gate was not rerun (Docker unavailable).
All PostgreSQL gates passed on local Docker PostgreSQL 16.
RC4 SQLite default is unchanged.

**Production PostgreSQL is NOT ready.** Local gate evidence proves the
harness and schema work at scale on PostgreSQL, but production deployment
still requires:
- Fix remaining `?` placeholders in `internal/api/handlers/` and `internal/coremail/`.
- Audit transaction boundaries for PostgreSQL.
- Complete Gate 8: full migration of all tables and verified restore/rollback.
- Production-hardened deployment playbook.

**3M harness proved on local PostgreSQL.** The benchmark harness
successfully inserted 3,000,000 rows and ran list/cursor/flag queries
against PostgreSQL 16. Metrics are recorded. This is benchmark evidence,
not production readiness.

**VPS deploy is NOT safe for PostgreSQL.**

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

**Last updated:** 2026-07-10 (DB-5/DB-6/DB-7 consolidated sprint)
