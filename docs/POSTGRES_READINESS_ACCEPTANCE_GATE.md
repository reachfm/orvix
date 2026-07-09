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
| **Expected** | All 37 tables + indexes created and verified. NO public tables dropped. |
| **Status** | NOT RUN (no local PostgreSQL) |
| **Date** | — |

## Gate 4 — PostgreSQL benchmark 10k

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN=<dsn> ORVIX_LOADTEST_ROWS=10000 go test -v -timeout 10m ./internal/storage/loadtest/ -run "SchemaCompat\|Benchmark"` |
| **Expected** | Insert, list, cursor, flag-update tests all PASS with PostgreSQL metrics |
| **Status** | NOT RUN |
| **Date** | — |

## Gate 5 — PostgreSQL benchmark 100k

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN=<dsn> ORVIX_LOADTEST_ROWS=100000 go test -v -timeout 30m ./internal/storage/loadtest/ -run "Benchmark"` |
| **Expected** | Insert rate sustained at 100k rows |
| **Status** | NOT RUN |
| **Date** | — |

## Gate 6 — PostgreSQL benchmark 3M (staging hardware)

| Field | Value |
|-------|-------|
| **Command** | `ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN=<dsn> ORVIX_LOADTEST_ROWS=3000000 ORVIX_LOADTEST_MAILBOXES=100 ORVIX_LOADTEST_BATCH_SIZE=1000 go test -v -timeout 4h ./internal/storage/loadtest/ -run "BenchmarkInsert"` |
| **Expected** | 3M rows inserted and queried on SSD-backed PostgreSQL. Metrics recorded. |
| **Status** | NOT RUN — requires staging hardware |
| **Date** | — |

## Gate 7 — DML compatibility audit

| Field | Value |
|-------|-------|
| **Document** | `docs/POSTGRES_DML_COMPATIBILITY_AUDIT.md` |
| **Expected** | All findings documented. Blockers identified and deferred. No surprises. |
| **Status** | PASS — 10 findings audited, 4 deferred, 5 fixed, 1 safe as-is |
| **Date** | 2026-07-09 |

## Gate 8 — Migration / backup / rollback plan

| Field | Value |
|-------|-------|
| **Document** | `docs/POSTGRES_ENTERPRISE_FOUNDATION.md` Section 7-8 |
| **Expected** | Migration path documented. Rollback strategy exists. Backup/restore procedures documented. |
| **Status** | PASS — documented in foundation document |
| **Date** | 2026-07-09 |

---

## Summary

| Gate | Status | Blocker |
|------|--------|---------|
| 1 — Normal test suite | PASS | — |
| 2 — SQLite benchmark 10k | PASS | — |
| 3 — PostgreSQL schema smoke | NOT RUN | No local PostgreSQL |
| 4 — PostgreSQL benchmark 10k | NOT RUN | Gate 3 not passed |
| 5 — PostgreSQL benchmark 100k | NOT RUN | Gate 4 not passed |
| 6 — PostgreSQL benchmark 3M | NOT RUN | Staging hardware needed |
| 7 — DML audit | PASS | — |
| 8 — Migration/backup plan | PASS | — |

**Overall status:** Gates 1-2, 7-8 PASS. Gates 3-6 need PostgreSQL staging.
RC4 SQLite default is unchanged. Production PostgreSQL is NOT ready.

---

**Last updated:** 2026-07-09
