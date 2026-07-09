# Orvix PostgreSQL Enterprise Foundation

**Version:** 1.0.3-rc4+
**Status:** Foundation phase — SQLite still default, PostgreSQL wired but not yet load-proven
**Branch:** `db/postgres-enterprise-foundation`

---

## 1. Current SQLite Model Inventory

Orvix manages **66 tables** across 9 schema-defining packages. Full details in the DB audit but summary:

| Category | Tables | Count |
|----------|--------|-------|
| Admin / Platform | `licenses`, `feature_flags`, `tenants`, `resellers`, `l_dap_configs`, `s_s_o_configs`, `alert_configs`, `firewall_rules`, `firewall_logs`, `guardian_logs`, `heal_histories`, `provisioned_domains`, `coremail_audit`, `sessions`, `update_histories`, `users`, `domains`, `mailboxes`, `api_keys` | 20 |
| CoreMail operational | `coremail_domains`, `coremail_mailboxes`, `coremail_aliases`, `coremail_account_classes`, `coremail_domain_groups`, `coremail_domain_group_members`, `coremail_mailing_lists`, `coremail_mailing_list_members`, `coremail_public_folders`, `coremail_public_folder_members`, `coremail_admin_groups`, `coremail_admin_group_members`, `coremail_acl_rules`, `coremail_log_rules`, `coremail_quarantine_index`, `coremail_dkim_config`, `mfa_recovery_codes`, `security_events`, `coremail_acceptance_rules`, `coremail_incoming_msg_rules`, `coremail_migration_sources`, `coremail_migration_source_secrets`, `coremail_backup_targets`, `coremail_backup_target_secrets`, `coremail_uploaded_certificates` | 25 |
| Storage / mail | `coremail_folders`, `coremail_messages`, `coremail_attachments`, `coremail_retention_policies`, `push_subscriptions`, `coremail_user_settings`, `coremail_rules`, `coremail_vacation`, `coremail_vacation_history`, `coremail_forwarding` | 10 |
| Queue | `coremail_queue` | 1 |
| Delivery | `coremail_delivery_attempts` | 1 |
| Backup | `backup_registry`, `backup_schedule_config` | 2 |
| Monitoring | `monitoring_alerts`, `monitoring_alert_deliveries` | 2 |
| TLS | `tls_certificates` | 1 |
| AV | `coremail_av_quarantine` | 1 |
| Trust / Security | `coremail_lockouts`, `coremail_trust_scores` | 2 |
| Settings | `admin_settings` | 1 |
| Updater | `update_history` | 1 |

---

## 2. Target PostgreSQL Metadata Architecture

### What PostgreSQL stores (metadata only)

PostgreSQL is the **metadata database** — the source of truth for:

- **Tenant/domain/user/mailbox** state (identity, routing, auth, billing pointers)
- **Folder/message metadata** (subject, from, to, flags, dates, folder assignment, rfc822_path pointer, sha256)
- **Attachment metadata** (filename, content-type, size, sha256, storage_path pointer)
- **Queue state** (entries, status, attempts, leases, SMTP diagnostics)
- **Delivery history** (per-attempt status, duration, remote host/IP, TLS state)
- **Session/security** (opaque token hashes, MFA secrets, recovery codes, lockouts)
- **Audit trail** (actor, action, target, result, IP, timestamp)
- **Security events** (login attempts, lockouts, rate-limit counters)
- **Admin settings** (key-value operational config)
- **Backup/schedule** metadata
- **TLS certificate** registry

### What stays outside PostgreSQL

| Data | Storage | Rationale |
|------|---------|-----------|
| **Mail bodies (RFC822)** | Filesystem (local NFS/S3 path) | Messages can be 25MB+. Storing in PG bloats WAL, slows backups, burns expensive IOPS. `rfc822_path` column points to filesystem or object storage. |
| **Attachments (binary)** | Filesystem (local NFS/S3 path) | Same as bodies. `storage_path` column. Comment in code reads "future: S3 key". |
| **DKIM private keys** | Inline in DB (encrypted) | Small cryptographic blobs; acceptable in metadata DB with encryption-at-rest |
| **Migration/backup secrets** | Inline in DB (encrypted) | `password_enc` columns use `config.EncryptString` |
| **Uploaded certificates** | Filesystem | Path stored in DB; PEM files on disk |

### Object storage boundary

The `Message.Attachment` struct already has a forward-looking comment:

```go
StoragePath string `json:"storage_path"` // path on local disk; future: S3 key
```

When S3/MinIO object storage is implemented:
1. Add an `ObjectStore` interface (`Put(ctx, key, reader)`, `Get(ctx, key)`, `Delete(ctx, key)`)
2. Filesystem backend implements it for single-node/dev
3. S3 backend implements it for production
4. `StoragePath` becomes the object key
5. `rfc822_path` in messages becomes the object key for email bodies

---

## 3. Tables That Need Additional Indexes

### Critical for 3M-message scale

| Table | Recommended Index | Reason |
|-------|------------------|--------|
| `coremail_messages` | `(mailbox_id, received_date DESC)` | Webmail "latest messages" query sorts by date per mailbox |
| `coremail_messages` | `(folder_id, id)` | Folder listing queries filter by folder + use cursor pagination on id |
| `coremail_queue` | `(status, created_at)` | Admin queue dashboard queries filter by status, paginate by date |
| `coremail_queue` | `(status, deleted_at)` | Soft-delete cleanup scans |
| `coremail_audit` | `(actor, timestamp DESC)` | Audit trail lookup by user |
| `coremail_audit` | `(target_type, target_id, timestamp DESC)` | Audit per-resource queries |
| `security_events` | `(email, event_type, created_at DESC)` | Login protection queries filter by email + event type |
| `coremail_delivery_attempts` | `(queue_entry_id, attempted_at)` | Per-entry attempt history in time order |
| `coremail_attachments` | `(message_id, id)` | Attachment listing for message view |

### Existing indexes that already cover key patterns

- `coremail_messages`: `(mailbox_id, folder_id)`, `message_id`, `internet_message_id`, `from_address`, `subject`, `received_date`, `(mailbox_id, folder_id, seen, deleted, junk)`, `purged_at`
- `coremail_queue`: `(status, next_attempt_at, priority)`, `(tenant_id, status, created_at)`, `(domain_id, status, created_at)`, `(recipient_domain, status, created_at)`, `message_id`, partial index on `(status, lease_expires_at)`, `(status, completed_at)`, partial on `(status, dead_letter_at)`, `(tenant_id, status, id)`
- `coremail_audit`: `timestamp`
- `security_events`: `email`, `event_type`, `created_at`

---

## 4. Tables That May Need Partitioning Later

At 3M+ messages scale, consider partitioning:

| Table | Partition Key | Strategy |
|-------|--------------|----------|
| `coremail_messages` | `received_date` (monthly) | Range partition. Webmail queries always include a mailbox filter; combining mailbox index with partition pruning keeps query time constant regardless of total volume. |
| `coremail_queue` | `created_at` (weekly) | Range partition. Completed entries are purged regularly; active entries stay in recent partitions. |
| `coremail_delivery_attempts` | `attempted_at` (monthly) | Range partition. Most queries are per queue entry; partition by time for retention cleanup. |
| `coremail_audit` | `timestamp` (monthly) | Range partition. Audit queries are time-bounded; old partitions can be archived/dropped. |
| `security_events` | `created_at` (monthly) | Range partition. Security events roll up by (ip, email, event_type) per time window. |

**Note:** Partitioning requires PostgreSQL 12+ declarative partitioning. None of this is implemented yet — it is noted here for the planning phase.

---

## 5. Pagination Gaps Identified

| Endpoint / Query | Current Pattern | Risk |
|-----------------|-----------------|------|
| Webmail message list | Cursor pagination via `id < BeforeID` | Safe — cursor-based, no OFFSET scan. **Already well-implemented.** |
| Queue admin list | SQL limit/offset via `HandleAdminGetQueue` | **Gap** — OFFSET becomes slow at millions of entries. Should use cursor pagination on `id`. |
| Audit log list | N/A (not yet paginated) | **Gap** — entire audit table returned to admin dashboard without pagination. Needs `LIMIT` + cursor. |
| Admin domain/user lists | GORM `Find` with `Offset`/`Limit` | **Gap** — brute force offset on hundreds of thousands of domains/users. Should use cursor pagination. |
| Security events query | Raw SQL `GROUP BY` with range scan | Slow at scale but bounded by rollup window. Acceptable for now. |

---

## 6. Migration Path from SQLite to PostgreSQL

### Phase 1: Foundation (this PR)

- [x] Config supports both drivers with safe defaults
- [x] Validation rejects invalid drivers, empty DSNs
- [x] Production gate refuses SQLite in production mode
- [x] Connection pool properly configured per driver
- [x] DSN secrets never logged
- [x] PostgreSQL schema readiness documented

### Phase 2: Schema compatibility (future)

- [ ] Add `MigrateAllPostgres()` — PostgreSQL-native DDL (no SQLite PRAGMAs, no `INTEGER PRIMARY KEY` autoincrement workarounds)
- [ ] Add `schema_migrations` version tracker
- [ ] Test `MigrateAllRaw()` on PostgreSQL (currently uses `PRAGMA table_info` which PG doesn't understand)
- [ ] Add `pgx` or `lib/pq` driver import if needed
- [ ] Run full test suite with `ORVIX_DB_DRIVER=postgres`

### Phase 3: Data migration tooling (future)

- [ ] `cmd/orvix migrate --from sqlite --to postgres` CLI
- [ ] Table-by-table export/import with progress reporting
- [ ] Row-count verification after migration
- [ ] SHA256 checksum comparison for message/attachment metadata
- [ ] Downtime estimation for given dataset size

### Phase 4: Staging and production (future)

- [ ] Deploy PostgreSQL on staging VPS
- [ ] Run 3M-message load test (see Section 8)
- [ ] Verify backup/restore round-trip on PostgreSQL
- [ ] Run production acceptance gate with PostgreSQL
- [ ] Gradual rollout: tenants migrated one at a time

---

## 7. Rollback Strategy

1. Stop Orvix service
2. Restore configuration: `database.driver: sqlite`, `database.sqlite_path: <backup path>`
3. Restart service
4. Verify health endpoint
5. PostgreSQL data remains untouched (can be re-migrated later)

The migration tool (Phase 3) will be read-only from SQLite — it never modifies the source database. This makes rollback trivial: switch the config back to SQLite, restart.

---

## 8. Backup / Restore Strategy

### SQLite (current)

```bash
# Backup
sqlite3 /var/lib/orvix/orvix.db ".backup /var/backups/orvix/orvix-$(date +%Y%m%d-%H%M%S).db"

# Restore
cp /var/backups/orvix/orvix-YYYYMMDD-HHMMSS.db /var/lib/orvix/orvix.db
```

Filesystem data (mail bodies, attachments) must be backed up separately with rsync/tar. The DB-only backup is insufficient — `rfc822_path` and `storage_path` pointers in the DB must resolve to actual files on disk.

### PostgreSQL (future)

```bash
# Logical backup
pg_dump -h localhost -U orvix -d orvix -Fc -f /var/backups/orvix/orvix-$(date +%Y%m%d-%H%M%S).dump

# Physical backup (if WAL archiving configured)
pg_basebackup -h localhost -U replicator -D /var/backups/orvix/base -Ft -z

# Restore
pg_restore -h localhost -U orvix -d orvix -c /var/backups/orvix/orvix-YYYYMMDD-HHMMSS.dump
```

**Note:** PostgreSQL backups cover metadata only. Filesystem/object-storage backup is a separate concern. The two must be consistent: a DB backup taken at time T must match filesystem snapshot at time T.

---

## 9. Load-Test Scaffold (Benchmark Not Yet Wired)

### Current status

This PR adds a **scaffold only** — in-memory concurrency and timing
simulations that prove the benchmarking primitives (timers, counters,
goroutine pools, cursor-pagination simulation) compile and run correctly.
**The scaffold does NOT yet connect to any database (SQLite or PostgreSQL)
and does NOT insert real rows.**

The real PostgreSQL-backed benchmark will be wired in a follow-up DB-2
PR once the database opener integration is ready to accept an
env-var-driven DB handle inside the test.

### Scaffold tests

| Test | Runs in CI | Description |
|------|-----------|-------------|
| `TestScaffoldConfig` | Always | Verifies threshold constants are positive, env guard works, targetRows is 3M |
| `TestScaffoldSelfTests` | Only with `ORVIX_RUN_DB_LOADTEST=1` | Exercises concurrency primitives, cursor-pagination timing simulation, concurrent update timing simulation — all in-memory, no real DB |

### Planned real-benchmark metrics (DB-2)

| Metric | Placeholder Target |
|--------|-------------------|
| Insert rate | > 1,000 rows/sec sustained |
| List query (latest 50, any mailbox) | < 50ms at 3M rows |
| List query (latest 50, filtered by folder) | < 100ms at 3M rows |
| Flag update (seen/deleted) | < 10ms per update |
| Search by subject | < 500ms at 3M rows |
| Queue insert rate | > 5,000 rows/sec |
| Queue lease claim | < 10ms |

All targets above are **placeholders** — they will be measured and
tightened on real staging hardware (SSD-backed PostgreSQL) in DB-2.

### How to run the scaffold self-tests

```bash
# Scaffold self-tests (in-memory, no real DB):
ORVIX_RUN_DB_LOADTEST=1 go test -v -timeout 5m ./internal/storage/loadtest/ -run Scaffold

# Future real benchmark (NOT YET IMPLEMENTED — DB-2):
# ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres ORVIX_DB_DSN="host=..." \
#   go test -v -timeout 30m ./internal/storage/loadtest/ -run Benchmark
```

---

## 10. Clear Statement on 3M Readiness

**Orvix RC4 is NOT yet proven for 3,000,000 stored email messages.**

- The current SQLite backend has been tested with the existing integration test suite (hundreds of messages per test run).
- No production-scale load test has been run.
- PostgreSQL support exists in the connection layer but has not been load-tested.
- The migration tooling from SQLite to PostgreSQL does not yet exist.
- **This PR adds only a load-test scaffold (in-memory); the real database-backed benchmark is DB-2.**

### Main blockers to 3M email scale

1. **PostgreSQL must be load-tested** — the planned real benchmark (DB-2) must pass with actual PostgreSQL on SSD storage.
2. **Schema migration tool must exist** — moving from SQLite to PostgreSQL requires a safe, verified migration path.
3. **Object storage abstraction must be implemented** — storing 3M × 25MB messages on a single local disk is not viable. S3/MinIO integration is required for production.
4. **Partitioning must be considered** — at 10M+ messages, range partitioning on `received_date` becomes necessary for sustainable query performance.
5. **Pagination gaps must be closed** — several admin endpoints use OFFSET pagination which degrades linearly with row count.

### What IS proven and safe today

- Single-node SQLite deployment on a VPS with modest volume (tested via CI test suite).
- The PostgreSQL connection path exists and compiles/passes unit tests.
- The architecture cleanly separates metadata (DB) from blobs (filesystem) — no refactoring needed.
- Cursor-based pagination is already used in the hottest path (webmail message list).
- All 66 tables have documented schemas with indexes.

---

## 11. DB-2: Real Benchmark Harness (this PR)

### What was added

The scaffold in `internal/storage/loadtest/` (DB-1) has been upgraded to
a **real database-backed benchmark harness**.  The harness now opens a
live SQLite or PostgreSQL connection via env vars and inserts real rows
into an isolated `loadtest_coremail_messages` table that mirrors the
production `coremail_messages` schema and query patterns.

### What is real DB-backed

| Test | Description | Rows (default) |
|------|-------------|----------------|
| `TestPostgresSchemaCompat` | Creates benchmark table + indexes, inserts row, verifies count and indexes | 1 |
| `TestBenchmarkInsert` | Batch-inserts rows, reports sustained insert rate | 10,000 |
| `TestBenchmarkListLatest` | Concurrent "latest 50 per mailbox" queries, reports avg latency | 10,000+ |
| `TestBenchmarkCursorPagination` | Cursor-based `WHERE id < cursor ORDER BY id DESC LIMIT 50` pagination | 10,000+ |
| `TestBenchmarkFlagUpdates` | Concurrent seen/deleted flag updates, reports avg per-update latency | 10,000+ |

### What remains scaffold-only

`TestScaffoldConfig` and `TestScaffoldSelfTests` remain as in-memory
harness-primitive checks.  They do not connect to a database.

### Env vars

| Variable | Default | Description |
|----------|---------|-------------|
| `ORVIX_RUN_DB_LOADTEST` | (unset → skip) | Set to `1` to enable heavy tests |
| `ORVIX_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `ORVIX_DB_DSN` | auto (sqlite memory) | Full DSN for Postgres; sqlite path for SQLite |
| `ORVIX_LOADTEST_ROWS` | `10000` | Total rows to insert in insert/list tests |
| `ORVIX_LOADTEST_MAILBOXES` | `10` | Number of distinct mailboxes |
| `ORVIX_LOADTEST_BATCH_SIZE` | `500` | Rows per INSERT batch |

### How to run

```bash
# SQLite smoke (10k rows, in-memory):
ORVIX_RUN_DB_LOADTEST=1 \
  go test -v -timeout 5m ./internal/storage/loadtest/ -run "SchemaCompat|Benchmark"

# SQLite 3M staging (heavy):
ORVIX_RUN_DB_LOADTEST=1 ORVIX_LOADTEST_ROWS=3000000 \
  go test -v -timeout 2h ./internal/storage/loadtest/ -run "BenchmarkInsert"

# PostgreSQL staging:
ORVIX_RUN_DB_LOADTEST=1 ORVIX_DB_DRIVER=postgres \
  ORVIX_DB_DSN="host=HOST port=5432 user=orvix dbname=orvix password=PASS sslmode=disable" \
  ORVIX_LOADTEST_ROWS=10000 \
  go test -v -timeout 5m ./internal/storage/loadtest/ -run "SchemaCompat|Benchmark"
```

### Known limitations

- The benchmark table is `loadtest_coremail_messages` — an isolated
  mirror of the production schema.  It does NOT use foreign keys,
  DEFAULT expressions that differ per driver, or soft-delete columns.
- Indexes are created on `(mailbox_id, received_date DESC)`,
  `(mailbox_id, folder_id, id)`, and `(folder_id, id)` — matching the
  production query patterns.
- The PostgreSQL path uses `BIGSERIAL PRIMARY KEY` and `$1`-style
  placeholders; the SQLite path uses `INTEGER PRIMARY KEY AUTOINCREMENT`
  and `?`-style placeholders.
- Partial indexes, `PRAGMA`, and SQLite-specific column introspection
  are NOT used in the benchmark path.
- `MigrateAllRaw()` remains SQLite-only and requires a separate
  `MigrateAllPostgres()` in a later phase.

### Clear statement

**3M support is NOT yet proven.**  The 3M staging benchmark has not
been executed on real SSD-backed PostgreSQL hardware.  This PR adds
the harness to make that measurement possible; the actual metrics
will be recorded when staging hardware is available.

---

## 12. DB-3 / DB-4: PostgreSQL Production Schema Compatibility

### What was added (37 tables)

`MigrateAllPostgres()` in `internal/models/postgres_migrations.go` is the
PostgreSQL-native counterpart to `MigrateAllRaw()`.  It creates **37
production metadata tables** with proper PostgreSQL DDL.

**Admin / platform (13 tables):**

| Table | Unique constraint |
|-------|------------------|
| `licenses` | `key_hash` |
| `feature_flags` | `name` |
| `tenants` | `slug WHERE deleted_at IS NULL`, `domain WHERE deleted_at IS NULL` |
| `users` | `email WHERE deleted_at IS NULL` |
| `domains` | `domain WHERE deleted_at IS NULL` |
| `mailboxes` | `email WHERE deleted_at IS NULL` |
| `api_keys` | `key_hash` |
| `resellers` | `email` |
| `l_dap_configs` | — |
| `s_s_o_configs` | — |
| `alert_configs` | — |
| `provisioned_domains` | `domain` |
| `module_versions` | deferred |

**Sessions / security / audit (4 tables):**

| Table | Unique constraint |
|-------|------------------|
| `sessions` | `token_hash` |
| `coremail_audit` | — |
| `security_events` | — |
| `mfa_recovery_codes` | — |

**CoreMail operational (16 tables):**

| Table | Unique constraint |
|-------|------------------|
| `coremail_mailboxes` | — |
| `coremail_domains` | `name` |
| `coremail_aliases` | — |
| `coremail_account_classes` | `(tenant_id, name)` |
| `coremail_admin_groups` | `(tenant_id, name)` |
| `coremail_admin_group_members` | `(group_id, user_id)` |
| `coremail_acl_rules` | — |
| `coremail_quarantine_index` | — |
| `coremail_dkim_config` | `domain` |
| `coremail_acceptance_rules` | — |
| `coremail_incoming_msg_rules` | — |
| `coremail_migration_sources` | `(tenant_id, name)` |
| `coremail_migration_source_secrets` | PK = `source_id` (FK identity) |
| `coremail_backup_targets` | `(tenant_id, name)` |
| `coremail_backup_target_secrets` | PK = `target_id` (FK identity) |
| `coremail_uploaded_certificates` | `(tenant_id, name)` |

**Mail storage (4 tables):**

| Table | Unique constraint |
|-------|------------------|
| `coremail_folders` | — |
| `coremail_messages` | `message_id` |
| `coremail_attachments` | — |
| `coremail_queue` | — |
| `coremail_delivery_attempts` | — |

### Soft-delete uniqueness

Tables with soft-delete use **partial unique indexes** (`WHERE deleted_at
IS NULL`).  This correctly prevents duplicate active rows.  The SQLite
pattern `UNIQUE(col, deleted_at)` is **not** used because it allows
duplicate active rows in both databases (NULLs are distinct in UNIQUE
constraints).

Partial unique indexes:

| Table | Index name | Columns |
|-------|-----------|---------|
| `tenants` | `uq_tenants_slug_active` | `slug` |
| `tenants` | `uq_tenants_domain_active` | `domain` |
| `users` | `uq_users_email_active` | `email` |
| `domains` | `uq_domains_domain_active` | `domain` |
| `mailboxes` | `uq_mailboxes_email_active` | `email` |

### Key PostgreSQL conversions

| SQLite pattern | PostgreSQL equivalent | Affected |
|----------------|----------------------|----------|
| `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGSERIAL PRIMARY KEY` | All 37 tables |
| `DATETIME` | `TIMESTAMP` | All timestamp columns |
| `datetime('now')` | `NOW()` | Default values |
| `INTEGER` for boolean flags | `BOOLEAN` | ~80 flag columns |
| `PRAGMA table_info()` | `information_schema.columns` | N/A (not used in PG path) |
| `sqlite_master` | `information_schema.tables` | Verification queries |
| `REAL` | `DOUBLE PRECISION` | `resellers.commission` |
| `UNIQUE(col, deleted_at)` | `WHERE deleted_at IS NULL` partial index | 5 tables |

### What remains unmigrated (16 tables)

These tables are defined in `MigrateAllRaw()` but not yet in
`MigrateAllPostgres()`.  They are lower-priority operational tables
deferred to DB-5:

- `module_versions` — module install history
- `firewall_rules`, `firewall_logs` — firewall config/logs
- `guardian_logs` — AI threat analysis
- `heal_histories` — auto-heal actions
- `update_histories` — update history
- `coremail_domain_groups`, `coremail_domain_group_members` — domain grouping
- `coremail_mailing_lists`, `coremail_mailing_list_members` — mailing lists
- `coremail_public_folders`, `coremail_public_folder_members` — shared folders
- `coremail_log_rules` — log collection rules

Additional tables in other packages (backup, monitoring, TLS, AV, trust,
settings, updater) are also deferred.  See `docs/POSTGRES_DML_COMPATIBILITY_AUDIT.md`
for the full DML compatibility roadmap.

### Schema smoke test

`TestPostgresProductionSchemaCompat` in `models_test.go`:
- Creates an isolated schema (`orvix_pg_test_<timestamp>`), never drops public tables
- Runs `MigrateAllPostgres()`, verifies all 37 tables via `information_schema.tables`
- Verifies indexes via `pg_indexes` (filtered by schemaname)
- Inserts and queries a representative row
- Cleans up with `DROP SCHEMA ... CASCADE`
- Requires: `ORVIX_RUN_POSTGRES_SCHEMA_TEST=1`, `ORVIX_DB_DRIVER=postgres`, `ORVIX_DB_DSN=<dsn>`
- Skipped silently in normal CI
- Status: **NOT RUN** (no local PostgreSQL)

---

## 13. Remaining Phases

### DB-5: Complete schema + DML audit

1. Migrate remaining 16 tables from `INTEGER PRIMARY KEY AUTOINCREMENT`
   to `BIGSERIAL PRIMARY KEY`.
2. Replace all `datetime('now')` DML calls with `NOW()` (10+ call sites).
3. Replace `INSERT OR REPLACE` with `INSERT ... ON CONFLICT ... DO UPDATE`
   (2 call sites in `internal/trust/`).
4. Audit and fix `?` placeholder usage in raw SQL — verify every raw
   SQL query is either GORM-generated (driver-translated) or uses
   `$N` placeholders for the PostgreSQL path.

### DB-6: Staging 3M run

1. Execute `ORVIX_LOADTEST_ROWS=3000000` benchmark on PostgreSQL staging.
2. Record insert rate, list latency, pagination, flag update metrics.
3. Tune thresholds based on real measurements.

### DB-7: Migration tool

1. Build `cmd/orvix migrate --from sqlite --to postgres` CLI.
2. Table-by-table export/import with progress, row count, and checksum
   verification.
3. Test backup, restore, and rollback procedures on staging PostgreSQL.
4. Measure downtime for representative dataset sizes.

---

## 14. Clear Statement

**RC4 is still the SQLite default.**  `MigrateAllPostgres()` covers 37
tables with PostgreSQL-native DDL, verified by opt-in schema smoke test.
16 additional tables remain unmigrated.  DML compatibility (placeholder
translation, `datetime('now')` replacement, upsert syntax) is deferred
to DB-5.

**Production PostgreSQL is NOT ready.**  The following blocks deployment:

1. DML compatibility audit has 4 deferred findings.
2. No migration CLI exists.
3. No backup/restore/rollback has been tested on PostgreSQL.
4. No 3M staging benchmark has been run.

**3M support is NOT proven.**  No 3M benchmark has been executed on
real PostgreSQL staging hardware.  The harness, schema, and runbook
exist to make that measurement possible.

**PostgreSQL schema DDL is expanded but not live-verified in this PR.**

---

**Last updated:** 2026-07-09
**Author:** Orvix DB Engineering

---

**Last updated:** 2026-07-09
**Author:** Orvix DB Engineering
