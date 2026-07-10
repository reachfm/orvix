# PostgreSQL Migration Runbook

This runbook describes how to migrate Orvix **metadata/admin tables** from
SQLite to PostgreSQL. CoreMail operational storage (messages, attachments,
queue) remains SQLite-only and is out of scope for this branch.

**Status:** Implemented and tested for the 10 metadata tables:
`tenants`, `users`, `domains`, `mailboxes`, `api_keys`, `sessions`,
`coremail_audit`, `security_events`, `feature_flags`, `licenses`.

**What is tested:**
- SQLite-to-PostgreSQL migration of all 10 metadata tables.
- Boolean conversion, mailbox `local_part`/`email` semantics, and sequence
  synchronization.
- `pg_dump` / `pg_restore` round-trip of the migrated metadata database.
- Runtime startup against the restored database.

**What is NOT supported:**
- Migration of CoreMail message, attachment, or queue data.
- Full PostgreSQL product deployment.
- Production VPS cutover without further CoreMail storage work.

---

## Prerequisites

- PostgreSQL 15+ server accessible from the Orvix host.
- A PostgreSQL user with CREATE SCHEMA, CREATE TABLE, INSERT, SELECT privileges.
- Source SQLite file (`/var/lib/orvix/orvix.db` by default).
- Sufficient disk space for both SQLite and PostgreSQL datasets during cutover.
- A maintenance window if migrating a production deployment.

---

## 1. Pre-migration safety

### 1.1 Back up SQLite

Before any migration activity, create a file-level backup:

```bash
sqlite3 /var/lib/orvix/orvix.db ".backup '/var/lib/orvix/orvix.db.pre-pg-$(date +%Y%m%d-%H%M%S)'"
```

Or copy the file while the service is stopped:

```bash
systemctl stop orvix
cp /var/lib/orvix/orvix.db /var/lib/orvix/orvix.db.pre-pg-$(date +%Y%m%d-%H%M%S)
```

### 1.2 Record pre-migration row counts

```bash
sqlite3 /var/lib/orvix/orvix.db <<'EOF'
SELECT 'tenants', COUNT(*) FROM tenants;
SELECT 'users', COUNT(*) FROM users;
SELECT 'domains', COUNT(*) FROM coremail_domains;
SELECT 'mailboxes', COUNT(*) FROM coremail_mailboxes;
SELECT 'messages', COUNT(*) FROM coremail_messages;
SELECT 'queue', COUNT(*) FROM coremail_queue;
EOF
```

Save this output for post-migration verification.

### 1.3 Prepare PostgreSQL target

Create a dedicated database and user if needed:

```sql
CREATE DATABASE orvix;
CREATE USER orvix WITH ENCRYPTED PASSWORD '<strong-password>';
GRANT ALL PRIVILEGES ON DATABASE orvix TO orvix;
```

Set the DSN in the environment (do not log this value):

```bash
export ORVIX_DB_DRIVER=postgres
export ORVIX_DB_DSN='host=localhost port=5432 user=orvix password=<strong-password> dbname=orvix sslmode=disable'
```

### Required integration-test environment variables

The automated PostgreSQL tests are gated behind environment variables so they
only run when a PostgreSQL server is available:

| Variable | Value to run | Purpose |
|----------|--------------|---------|
| `ORVIX_DB_DRIVER` | `postgres` | Selects PostgreSQL dialect/paths. |
| `ORVIX_DB_DSN` | keyword or URL DSN | Connection string (keep secret). |
| `ORVIX_RUN_POSTGRES_SCHEMA_TEST` | `1` | Runs `TestPostgresProductionSchemaCompat`. |
| `ORVIX_RUN_POSTGRES_DML_TEST` | `1` | Runs PostgreSQL DML integration tests. |
| `ORVIX_RUN_POSTGRES_MIGRATE_TEST` | `1` | Runs full 10-table migration tests. |
| `ORVIX_RUN_POSTGRES_BACKUP_TEST` | `1` | Runs `pg_dump`/`pg_restore` round-trip test. |

**Do not commit passwords.** Pass these through CI secrets or a local `.env` file
that is excluded from version control.

---

## 2. Dry-run migration

The migration CLI defaults to dry-run mode.

```bash
orvix migrate \
  --from sqlite \
  --to postgres \
  --sqlite-path /var/lib/orvix/orvix.db \
  --postgres-dsn "$ORVIX_DB_DSN" \
  --dry-run true
```

Expected output:
- Source and target metadata.
- Table-by-table source and target row counts.
- "Dry-run complete. No changes written."

If the target schema is non-empty, the CLI exits with an error unless you add
`--allow-non-empty-target`.

---

## 3. Execute migration

After reviewing the dry-run output, run the migration with `--dry-run false`:

```bash
orvix migrate \
  --from sqlite \
  --to postgres \
  --sqlite-path /var/lib/orvix/orvix.db \
  --postgres-dsn "$ORVIX_DB_DSN" \
  --dry-run false
```

You will be prompted to type `migrate` unless you also pass `--skip-confirm`.

The CLI currently migrates these core metadata tables:

- tenants
- users
- domains
- mailboxes
- api_keys
- sessions
- coremail_audit
- security_events
- feature_flags
- licenses

Tables **NOT** migrated by the CLI:

- `coremail_messages`
- `coremail_attachments`
- `coremail_folders`
- `coremail_queue`
- `coremail_delivery_attempts`
- `backup_registry`
- `monitoring_alerts`
- `tls_certificates`
- trust scores / lockouts

CoreMail operational tables (`coremail_*` message/attachment/queue storage)
remain SQLite-only by design in this branch. These tables require dedicated
bulk-copy logic and a PostgreSQL-compatible storage backend and are left for
future migration work.

---

## 4. Post-migration verification

### 4.1 Row count verification

```bash
psql "$ORVIX_DB_DSN" <<'EOF'
SELECT 'tenants', COUNT(*) FROM tenants;
SELECT 'users', COUNT(*) FROM users;
SELECT 'domains', COUNT(*) FROM coremail_domains;
SELECT 'mailboxes', COUNT(*) FROM coremail_mailboxes;
EOF
```

Compare with the pre-migration SQLite counts.

### 4.2 Checksum verification

The CLI does not yet produce per-table checksums. As a manual cross-check:

```bash
# SQLite
sqlite3 /var/lib/orvix/orvix.db "SELECT group_concat(email) FROM users ORDER BY email" | sha256sum

# PostgreSQL
psql "$ORVIX_DB_DSN" -c "SELECT string_agg(email, ',' ORDER BY email) FROM users" -t | sha256sum
```

### 4.3 Application smoke test

Start Orvix pointed at PostgreSQL and verify:

- Admin login succeeds.
- Domain list loads.
- Mailbox list loads.
- Audit log loads.

---

## 5. PostgreSQL logical backup and restore validation

Before cutover, create a PostgreSQL logical backup:

```bash
pg_dump -Fc "$ORVIX_DB_DSN" > orvix_pre_cutover_$(date +%Y%m%d-%H%M%S).dump
```

Store this backup separately from the SQLite backup.

### Automated backup/restore integration test

The repository contains an integration test that validates the full cycle:

```bash
export ORVIX_DB_DRIVER=postgres
export ORVIX_DB_DSN='host=localhost port=5432 user=orvix password=<strong-password> dbname=orvix sslmode=disable'
export ORVIX_RUN_POSTGRES_MIGRATE_TEST=1
export ORVIX_RUN_POSTGRES_BACKUP_TEST=1

go test -v -count=1 -timeout=20m ./cmd/orvix -run TestPostgresBackupRestoreAndRuntime
```

This test:

1. Creates fresh source and destination databases on the configured server.
2. Migrates all 10 metadata tables from SQLite to the source database.
3. Runs `pg_dump --format=custom --no-owner --no-privileges` to a temporary file.
4. Runs `pg_restore --exit-on-error --no-owner --no-privileges` into the destination database.
5. Verifies all 10 table row counts, mailbox local_part/email semantics, and field values.
6. Inserts a new row using `DEFAULT id` to prove sequences are synchronized.
7. Starts the full Orvix runtime against the restored database and verifies health/admin login.

The test uses `pg_dump`/`pg_restore` from `PATH` and does not depend on a
specific container name or hardcoded credentials.

---

## 6. Rollback

### Decision flow

1. **If migration fails before cutover:**
   - Keep Orvix running on SQLite.
   - Drop the partial PostgreSQL schema:
     ```sql
     DROP SCHEMA public CASCADE;
     CREATE SCHEMA public;
     ```
   - Fix the root cause and retry.

2. **If cutover fails after service restart:**
   - Stop Orvix.
   - Revert `ORVIX_DB_DRIVER` to `sqlite` and `ORVIX_DB_DSN` to the SQLite path.
   - Restore the SQLite backup if the PostgreSQL migration mutated the SQLite file
     (it should not, but verify).
   - Start Orvix.

3. **If data corruption is detected after cutover:**
   - Stop writes immediately.
   - Restore from the pre-cutover `pg_dump` or the SQLite backup, depending on
     which is more recent and consistent.

---

## 7. Remaining gaps

- Bulk migration of messages, attachments, queue, and other large tables.
- Incremental / online migration (currently offline).
- Per-table SHA256 checksums inside the CLI.
- Parallel table copy for large datasets.
- Migration of file-backed data (mail store, attachments) — these are not in
  the database and are copied separately via backup/restore tooling.

---

## db/postgres-final-closure Sprint Update

**Date:** 2026-07-11

**Status:** Migration CLI hardened and backup/restore integration test added.

Completed:
- 10 metadata tables migrate with explicit column mappings.
- `mailboxes.local_part` is preserved and target `email` is built from
  `local_part || '@' || domains.domain`.
- Boolean columns are converted from SQLite INTEGER to PostgreSQL BOOLEAN.
- PostgreSQL SERIAL/BIGSERIAL sequences are synchronized after migration so
  new inserts using `DEFAULT id` do not collide with migrated explicit IDs.
- `pg_dump`/`pg_restore` round-trip test uses portable PostgreSQL client tools
  from PATH, creates/drops test databases through `database/sql`, and validates
  counts, semantics, sequences, and runtime startup.

Remaining gaps listed in Section 7 still apply:
- Bulk migration of messages, attachments, queue, and other large tables.
- Incremental / online migration (currently offline).
- Per-table SHA256 checksums inside the CLI.
- Parallel table copy for large datasets.
- Migration of file-backed data (mail store, attachments)

---

**Last updated:** 2026-07-10 (`db/postgres-final-closure` sprint)
