# Orvix PostgreSQL Staging Runbook

**DB-3** — how to run PostgreSQL schema compatibility, migration, and runtime
tests on a staging machine.

**Scope declaration:** This runbook covers the **metadata/admin PostgreSQL
foundation**. CoreMail operational storage (messages, attachments, queue)
remains SQLite-only. Full PostgreSQL deployment is not supported by this branch.

This runbook assumes:
- PostgreSQL is installed and running on the staging host (or local docker).
- You have a database and user created.
- You have the Orvix repo checked out.
- You never print the PostgreSQL DSN/password in terminal output or logs.

---

## 1. Prerequisites

### Create a PostgreSQL database and user

```bash
sudo -u postgres psql <<SQL
CREATE USER orvix_bench WITH PASSWORD 'strong-bench-password';
CREATE DATABASE orvix_bench OWNER orvix_bench;
GRANT ALL PRIVILEGES ON DATABASE orvix_bench TO orvix_bench;
SQL
```

### Set env vars (NEVER commit these, NEVER echo them)

```bash
export ORVIX_BENCH_DSN="host=localhost port=5432 user=orvix_bench dbname=orvix_bench password=strong-bench-password sslmode=disable"
```

**Always use these env vars so the DSN never appears in shell history or command output.**

---

## 2. Schema Compatibility Smoke

Verifies `MigrateAllPostgres()` creates 12 core tables with indexes.

```bash
ORVIX_RUN_POSTGRES_SCHEMA_TEST=1 \
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
go test -v -timeout 2m ./internal/models/ -run TestPostgresProductionSchemaCompat
```

**Expected output:**

```
=== RUN   TestPostgresProductionSchemaCompat
    models_test.go:...: all 12 core postgres tables created and verified
    models_test.go:...: index idx_tenants_deleted_at verified
    ... (11 index lines)
    models_test.go:...: postgres production schema smoke: PASS
--- PASS: TestPostgresProductionSchemaCompat
```

**Failure modes:**

| Symptom | Likely cause |
|---------|-------------|
| `connect to postgres: ...` | Wrong DSN, password, host, or database name |
| `MigrateAllPostgres: ...` | Permission denied (GRANT CREATE on database) |
| `PostgresSchemaCompatible: missing tables: ...` | Table creation partially failed |
| `index ... not found` | Index not created — check DDL |

---

## 3. Benchmark: 10k Rows (SQLite, local)

Fast smoke — no PostgreSQL needed. Runs in-memory.

```bash
ORVIX_RUN_DB_LOADTEST=1 \
ORVIX_DB_DRIVER=sqlite \
ORVIX_LOADTEST_ROWS=10000 \
go test -v -timeout 5m ./internal/storage/loadtest/ -run "SchemaCompat|Benchmark"
```

---

## 4. Benchmark: 10k Rows (PostgreSQL, staging)

```bash
ORVIX_RUN_DB_LOADTEST=1 \
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
ORVIX_LOADTEST_ROWS=10000 \
go test -v -timeout 5m ./internal/storage/loadtest/ -run "SchemaCompat|Benchmark"
```

**Collect these metrics:**

| Test | Metric | 10k |
|------|--------|-----|
| `TestBenchmarkInsert` | rows/sec | _________ |
| `TestBenchmarkListLatest` | avg latency | _________ |
| `TestBenchmarkCursorPagination` | avg per page | _________ |
| `TestBenchmarkFlagUpdates` | avg per update | _________ |

---

## 5. Benchmark: 100k Rows (PostgreSQL, staging)

```bash
ORVIX_RUN_DB_LOADTEST=1 \
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
ORVIX_LOADTEST_ROWS=100000 \
go test -v -timeout 15m ./internal/storage/loadtest/ -run "BenchmarkInsert"
```

**Collect these metrics:**

| Metric | 100k |
|--------|------|
| rows/sec | _________ |

---

## 6. Benchmark: 3,000,000 Rows (PostgreSQL, staging)

**WARNING:** This will take 30 minutes to 2 hours depending on hardware.

```bash
ORVIX_RUN_DB_LOADTEST=1 \
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
ORVIX_LOADTEST_ROWS=3000000 \
ORVIX_LOADTEST_MAILBOXES=100 \
ORVIX_LOADTEST_BATCH_SIZE=1000 \
go test -v -timeout 4h ./internal/storage/loadtest/ -run "BenchmarkInsert"
```

Then run the query tests against the 3M-row dataset:

```bash
# List latest (do NOT re-insert — use the existing 3M rows):
ORVIX_RUN_DB_LOADTEST=1 \
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
ORVIX_LOADTEST_ROWS=10 \
go test -v -timeout 1h ./internal/storage/loadtest/ -run "BenchmarkList|BenchmarkCursor|BenchmarkFlag"
```

**Collect these metrics:**

| Metric | 3M |
|--------|-----|
| Insert rate (rows/sec) | _________ |
| List latest avg latency | _________ |
| Cursor pagination avg/page | _________ |
| Flag update avg/update | _________ |
| Total wall time | _________ |
| Peak memory (MB) | _________ |
| Disk IOPS (avg) | _________ |

---

## 7. How to Capture Metrics

Pipe output to a log file:

```bash
ORVIX_RUN_DB_LOADTEST=1 \
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
ORVIX_LOADTEST_ROWS=100000 \
go test -v -timeout 15m ./internal/storage/loadtest/ -run "BenchmarkInsert" \
  2>&1 | tee benchmark-100k-$(date +%Y%m%d-%H%M%S).log
```

Extract metrics from the log:

```bash
grep -E 'insert:|rows=|rate=' benchmark-100k-*.log
```

---

## 8. How to Clean Up Benchmark Tables

The benchmark tests drop their tables at the end. If a test crashes, clean up manually:

```bash
psql "$ORVIX_BENCH_DSN" -c "DROP TABLE IF EXISTS loadtest_coremail_messages CASCADE"
```

---

## 9. How to Decide PASS / FAIL

### Schema smoke

| Result | Verdict |
|--------|--------|
| All 12 tables created + indexes verified | PASS |
| Any table missing | FAIL — check MigrateAllPostgres DDL |
| Any index missing | FAIL — check postgresIndexes |

### Benchmark

Use the following placeholder thresholds until real staging measurements exist:

| Test | FAIL if | Current threshold (placeholder) |
|------|---------|-------------------------------|
| Insert | rate < 500 rows/sec | TODO: tighten after real measurement |
| List latest | avg > 500ms | TODO: tighten after real measurement |
| Cursor pagination | avg per page > 100ms | TODO: tighten after real measurement |
| Flag updates | avg per update > 100ms | TODO: tighten after real measurement |

**After the first real staging run, record the actual numbers and update the thresholds.**

---

## 10. Migration, Runtime, and Backup/Restore Tests

### 10.1 Full 10-table migration

```bash
ORVIX_RUN_POSTGRES_MIGRATE_TEST=1 \
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
go test -v -count=1 -timeout=15m ./cmd/orvix -run TestMigrateAll10TablesWithRowVerification
```

**Expected:** 26 rows migrated across 10 metadata tables; row counts match;
mailbox `local_part`/`email` semantics verified.

### 10.2 Runtime startup on PostgreSQL

```bash
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
go test -v -count=1 -timeout=15m ./cmd/orvix -run TestStartupPostgresRuntime
```

**Expected:** migrations, feature-flag seed, admin seed, module InitAll/StartAll,
health 200, admin login 200, clean StopAll.

> CoreMail remains disabled by default; this tests the supported hybrid
> architecture (PostgreSQL metadata, SQLite CoreMail storage).

### 10.3 Backup/restore round-trip

Requires `pg_dump` and `pg_restore` in PATH.

```bash
ORVIX_RUN_POSTGRES_BACKUP_TEST=1 \
ORVIX_RUN_POSTGRES_MIGRATE_TEST=1 \
ORVIX_DB_DRIVER=postgres \
ORVIX_DB_DSN="$ORVIX_BENCH_DSN" \
go test -v -count=1 -timeout=20m ./cmd/orvix -run TestPostgresBackupRestoreAndRuntime
```

**Expected:** source and destination databases created, migrated data dumped and
restored, counts/semantics/sequences verified, runtime starts against restored DB.

---

## 11. Never Print or Log the DSN

- Always use env vars.
- Never `echo $ORVIX_BENCH_DSN`.
- Never commit the DSN to git.
- Never include the DSN in error logs.
- The benchmark code never logs the DSN — only the driver name.
- If you accidentally expose the DSN, rotate the password immediately.

---

## 12. What Is Still Not Migrated (DB-4+)

These 33+ tables from MigrateAllRaw are NOT yet covered by `MigrateAllPostgres()`:

- Resellers, LDAP configs, SSO configs, alert configs
- Firewall rules/logs, guardian logs, heal histories
- Provisioned domains, update histories
- CoreMail domains, aliases, account classes, domain groups
- Mailing lists, public folders, admin groups
- ACL rules, log rules, quarantine index, DKIM config
- Acceptance rules, incoming message rules
- Migration source secrets, backup target secrets
- Uploaded certificates

All of these use `INTEGER PRIMARY KEY AUTOINCREMENT` and need the same
PostgreSQL-native conversion.

---

**Last updated:** 2026-07-11
