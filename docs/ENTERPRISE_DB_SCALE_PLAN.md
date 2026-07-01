# Enterprise DB Scale Plan — Orvix Enterprise Mail

> Status: **Prepared for high-scale production architecture.**
> The Orvix backend is now suitable for a **Postgres production
> deployment** with billion-email-class query patterns. This
> document is the source of truth for what is implemented, what is
> scale-ready, and what is **not yet proven** by load testing.
>
> Allowed wording on the public website or marketing material:
>
> * "prepared for high-scale production architecture"
> * "Postgres production path implemented"
> * "large-dataset query patterns indexed and cursor-paginated"
>
> Forbidden wording until the project has actually been benchmarked
> at billion-row scale on the target hardware:
>
> * "supports one billion emails"
> * "unlimited scale"
> * "production GA"
> * "100% enterprise complete"

## 1. What is implemented now

### 1.1 Database mode abstraction

`internal/database/mode` provides the production-safety gate and
the runtime health surface for the two supported modes.

| Mode     | Status     | Use                                                |
| -------- | ---------- | -------------------------------------------------- |
| `sqlite` | Supported  | Local dev, CI, single-host installs, smoke tests   |
| `postgres` | Supported | Production target                                  |

The configuration layer rejects SQLite in production:

```go
// ValidateDriverDSN(driver, dsn, isProduction) error
//   - driver=sqlite + isProduction=true  → hard error
//   - driver=postgres + any isProduction  → accepted
//   - driver=sqlite + isProduction=false → accepted (dev)
```

The fail-closed check is wired into `config.Load`, so a misconfigured
production deployment cannot boot.

The `Health` struct is the canonical database health snapshot:

```json
{
  "mode": "postgres",
  "driver": "postgres",
  "production": true,
  "connected": true,
  "ping_latency_ms": 1,
  "pool_max_open": 25,
  "pool_max_idle": 5,
  "pool_in_use": 2,
  "pool_idle": 3,
  "schema_version": 3,
  "checked_at": "2026-07-01T18:00:00Z"
}
```

The DSN is **never** returned in this payload. Postgres DSN strings
that include passwords are redacted by `safeErr`.

### 1.2 Schema and indexes

`migrations/001_initial.sql` defines the foundation tables.
`migrations/002_admin_settings.sql` adds the persisted admin
settings table for PATCH /api/v1/admin/settings.
`migrations/003_scale_indexes.sql` adds the scale-ready indexes
that back the cursor-paginated hot paths:

| Index                                                      | Backs                                                  |
| ---------------------------------------------------------- | ------------------------------------------------------ |
| `idx_coremail_messages_mailbox_folder_id`                  | Webmail message list (cursor pagination)               |
| `idx_coremail_messages_mailbox_folder_received`            | "Newest first" mailbox list                           |
| `idx_coremail_messages_mailbox_folder_unread`              | Folder sidebar unread badge                           |
| `idx_coremail_queue_created_at`                            | Admin queue overview                                  |
| `idx_coremail_queue_status_next_attempt`                   | Delivery worker hot loop                              |
| `idx_audit_logs_action_created`                            | Incident-response audit queries                       |
| `idx_coremail_messages_deleted_at`                        | Retention sweeper                                    |

The pre-existing `coremail/storage.Indexes()` (run on schema
bootstrap) covers the rest of the mailbox hot paths.

### 1.3 Cursor pagination

`MessageSQLRepo.ListByCursor` is the scale-ready replacement for
the OFFSET-based `List` method. The query uses an `id < cursor`
or `id > cursor` predicate, which is index-backed and constant
cost per page. The webmail UI is expected to migrate to this
path; the legacy OFFSET path remains for internal admin tools
that paginate < 10k rows.

Coverage:

* First-page (no cursor) — newest N rows
* Before-cursor (scroll older) — descending
* After-cursor (poll for new) — ascending
* `HasMore` is computed without a `COUNT(*)` (limit+1 trick)
* No total count returned (counting at billion-row scale is
  multi-second; `CountByFolder` is the cheap alternative)

The implementation is tested end-to-end in
`internal/coremail/storage/cursor_test.go`:

* 25-row dataset walked page-by-page with a 7-row limit produces
  25 unique ids and no duplicates across pages
* Empty result is honest (zero messages, `HasMore=false`,
  `NextCursor=0`)
* `MailboxID=0` is rejected
* `Limit > MaxPageSize` is clamped

### 1.4 Metadata / body separation

The `coremail_messages` row stores only metadata
(`subject`, `from_address`, `received_date`, `size_bytes`, flags).
The RFC822 body lives in a separate file on disk
(`rfc822_path` + `sha256`). Attachment bytes are NOT stored in
`coremail_attachments`; only metadata (`filename`, `size_bytes`,
`storage_path`, `sha256`, `content_type`) is in the row, with the
content on disk.

This means:

* Mailbox list queries never load a body
* Attachment metadata queries never load an attachment blob
* Folder counts use the metadata columns and an index, not a
  full row scan

### 1.5 Admin settings persistence

`PATCH /api/v1/admin/settings` now writes to a DB-backed
key/value table with an explicit allowlist. Unknown fields and
unsafe fields are hard-rejected; secret-shaped fields are
redacted in responses and audit logs; restart-required fields
are flagged honestly. See `internal/api/handlers/settings` and
`internal/api/handlers/admin_settings.go` for the implementation
and tests.

### 1.6 Backup health semantics

`GET /api/v1/admin/backups/health` now distinguishes:

| Status                       | Meaning                                                |
| ---------------------------- | ------------------------------------------------------ |
| `ok`                         | Recent (≤24h) successful backup, directory writable    |
| `warning`                    | Most recent backup 24–72h old                          |
| `critical`                   | Most recent backup >72h old, or directory missing     |
| `no_backups`                 | Fresh install with no completed backups yet            |
| `directory_missing`          | Configured backup directory does not exist             |
| `directory_not_writable`     | Directory exists but cannot be written to             |
| `scheduler_disabled`         | Scheduler disabled and manual backup is stale          |

The previous release conflated `no_backups` with `critical`,
producing misleading alerts on every fresh install. That is
fixed in `internal/backup/service.go::GetBackupHealth`.

## 2. What is scale-ready now (architecturally)

* **Postgres production path implemented.** The full schema runs
  unchanged on Postgres 14+. The gorm.io/driver/postgres dialector
  is wired in `config.NewDatabase`. Pool settings (25/5/300) are
  the production default.
* **Cursor pagination on hot paths.** Mailbox list, queue list,
  and audit log list can be migrated from OFFSET to cursor
  pagination without a schema change beyond the indexes already
  shipped in `003_scale_indexes.sql`.
* **Metadata / body separation.** All body and attachment bytes
  live on the filesystem (or, in the production-scale
  recommendation, an object store). The relational rows hold
  only what is needed to render the inbox list and search
  results.
* **Tenant/domain/mailbox isolation.** Every hot query carries
  a `mailbox_id` or `tenant_id` predicate. Cross-tenant
  queries are not possible at the SQL level.
* **Migration safety.** All migrations are `CREATE … IF NOT
  EXISTS` and additive — no `DROP`, no `RENAME`, no `ALTER`
  COLUMN on a populated table. New indexes can be added
  online with `CREATE INDEX CONCURRENTLY` on Postgres without
  blocking writes.
* **Queue scalability.** The delivery worker leases jobs
  through a conditional `UPDATE` keyed on
  `status, next_attempt_at, lease_expires_at` (see
  `internal/coremail/queue/repository.go::LeaseNext`). The
  `idx_coremail_queue_status_next_attempt` index backs the
  hot path so a billion-row queue can serve a single
  worker just as well as it serves a fleet of workers.
* **Retention and archive strategy.** The retention sweeper
  reads `coremail_messages.deleted_at` and the
  `coremail_messages.purged_at` columns. The
  `idx_coremail_messages_deleted_at` index (migration 003)
  is the only access path; the sweeper never touches the
  body on disk for messages that have not been flagged for
  purge.

## 3. What is NOT yet proven

The following items are architecturally correct but require a
load-test campaign on the target production hardware before
the project can claim "tested at billion-row scale":

* **Actual billion-row insertion throughput.** Inserting a
  billion rows into `coremail_messages` and verifying the
  indexes stay balanced is a multi-day campaign. The
  `internal/coremail/storage` benchmarks cover the query
  shape, not the bulk-ingest path.
* **Postgres partition strategy at scale.** The schema is
  ready for partitioning, but the partition boundaries
  (by `tenant_id`, `domain_id`, `mailbox_id`, or
  `received_date`) are a per-deployment decision and have
  not been exercised at scale.
* **Object storage for bodies.** The `rfc822_path` /
  `storage_path` columns can be repointed to an S3 or
  S3-compatible object store, but the runtime currently
  reads them off the local filesystem. A real billion-email
  install will want bodies on object storage.
* **Hot-standby / read-replica routing.** The single-pool
  `gorm.DB` does not currently route reads to a replica.
  At billion-row scale the read load on the primary is
  significant; the project should add a read-replica
  dialector with failover.
* **Backup / restore at petabyte scale.** The backup
  service streams tar.gz archives of the database + bodies
  to a configurable directory. At petabyte scale, the
  operator should switch to a streaming pg_dump + S3 layout
  and use Postgres-native PITR.
* **Disaster recovery RPO / RTO.** No formal SLA has been
  measured. The backup / restore path is staged-only in
  this release; live restore requires a maintenance
  window.

## 4. Recommended production topology

The recommended Postgres topology for a real billion-email-class
deployment:

```
                    ┌──────────────────────────┐
                    │  Postgres Primary (R/W)  │
                    │  - tenant_id, mailbox_id │
                    │    partitions            │
                    │  - queue partition by    │
                    │    status, time          │
                    │  - audit log partition   │
                    │    by created_at (month) │
                    └──────────┬───────────────┘
                               │
                ┌──────────────┴──────────────┐
                │                             │
        ┌───────▼────────┐           ┌────────▼────────┐
        │ Read Replica 1 │   ...     │ Read Replica N  │
        │  - webmail     │           │ - admin queries │
        │  - jmap        │           │                 │
        └────────────────┘           └─────────────────┘

            ┌───────────────────────────┐
            │  S3 / MinIO / equivalent  │
            │  - mail bodies (rfc822)   │
            │  - attachments            │
            │  - backup tarballs        │
            └───────────────────────────┘
```

Notes:

* `coremail_messages` is partitioned by `mailbox_id` for the
  large installations where the row count per partition
  matters more than cross-mailbox aggregates. Smaller
  installations can partition by `received_date` and drop
  old partitions instead of running retention deletes.
* `coremail_queue` is partitioned by `status` and
  `created_at`. Active and recent-delivery jobs live in a
  small set of hot partitions; deferred / dead-letter jobs
  can live in cold partitions that are dropped or archived
  on a schedule.
* `audit_logs` is partitioned by `created_at` (monthly). The
  `idx_audit_logs_action_created` index is local to the
  partition, so retention becomes a `DROP PARTITION`
  operation rather than a row-by-row delete.
* Object storage for bodies and attachments decouples the
  database size from the message volume. The relational
  rows remain small; the body bytes live in S3.
* Read replicas let the webmail / JMAP read path scale
  horizontally without overloading the primary.

## 5. Index strategy

The shipped indexes are the minimum viable set for the
documented hot paths. They are documented in
`migrations/003_scale_indexes.sql` with the query they back.

The expected query plan for each hot path:

| Query                                                  | Index used                                                 |
| ------------------------------------------------------ | ---------------------------------------------------------- |
| `GET /api/v1/webmail/folders/:id`                      | `idx_coremail_folders_mailbox_path`                        |
| `GET /api/v1/webmail/messages?folder=N&before=C`       | `idx_coremail_messages_mailbox_folder_id`                  |
| `GET /api/v1/webmail/messages?folder=N&sort=received`  | `idx_coremail_messages_mailbox_folder_received`            |
| Folder unread counter                                  | `idx_coremail_messages_mailbox_folder_unread`              |
| Delivery worker `LeaseNext`                            | `idx_coremail_queue_status_next_attempt`                   |
| `GET /api/v1/admin/queue/messages`                     | `idx_coremail_queue_created_at`                            |
| `GET /api/v1/admin/audit/logs?action=X`                | `idx_audit_logs_action_created`                            |
| Retention sweeper                                      | `idx_coremail_messages_deleted_at`                         |

Operators may add more indexes for their specific access
patterns, but they should keep the index footprint small —
each index costs write throughput.

## 6. Backup / restore implications

* Backups include the database snapshot, the mailstore
  tar.gz, the attachments tar.gz, and the redacted
  configuration. They are written to the directory
  configured in `cfg.Backup.Dir`.
* Restore is staged-only in this release. The admin
  endpoint `POST /api/v1/admin/backups/:id/restore` writes
  the archive to `/var/lib/orvix/restore-staging/<id>/` and
  the operator must manually swap the live data in a
  maintenance window. The endpoint requires typed
  confirmation (`confirm: "restore-orvix-backup"`).
* At petabyte scale, the operator should switch to native
  Postgres backup tooling (`pg_basebackup` + WAL-G or
  `pgBackRest`). The Orvix backup service is suitable for
  small-to-medium installations.

## 7. Operational limits

These are the limits the runtime enforces today, not the
limits of the target hardware. Operators can change them in
`orvix.yaml`.

| Limit                                | Default | Notes                                |
| ------------------------------------ | ------- | ------------------------------------ |
| Max body size (per message)          | 25 MB   | SMTP `SIZE` extension                |
| Max attachment size (per file)       | 25 MB   | `MaxAttachmentSizeMB`                |
| Max attachments per message          | 50      | `MaxAttachmentsPerMessage`           |
| Default mailbox quota                | 1 GB    | Per-mailbox `quota_mb`               |
| SMTP `MAIL FROM` rate per connection | 30/min  | Firewall default                     |
| Webmail pagination default           | 50      | `DefPageSize` (storage)              |
| Webmail pagination max               | 1000    | `MaxPageSize` (storage)              |
| Login rate limit                     | 5/15m   | Per-IP                               |
| API rate limit                       | 100/60s | Per-IP                               |

## 8. What a real billion-email benchmark would require

A proper benchmark campaign before claiming billion-email
support would need, at minimum:

1. **Hardware.** Postgres 14+ on NVMe-backed storage with at
   least 256 GiB RAM, 64 vCPUs, and a separate object store
   for bodies. The full Orvix runtime in a sibling container
   against the same network.
2. **Schema partitioning.** Decide partition boundaries and
   apply them BEFORE the bulk load, not after.
3. **Bulk load.** Insert one billion rows into
   `coremail_messages` using `COPY` (not INSERT) at a
   sustained rate. Measure ingest throughput and the
   index-build cost.
4. **Hot path tests.** Drive the webmail list endpoint
   against a 1M-message mailbox. Measure:
   * p50 / p99 / p999 latency
   * queries per second at 100ms p99
   * index-only-scan confirmations
5. **Cursor pagination test.** Page through the full mailbox
   (1M messages) using `ListByCursor` and confirm that
   page-N latency is independent of N. With OFFSET, page
   10000 must be visibly slower; with cursor, it must not.
6. **Delivery worker test.** Insert 10M queue entries and
   run the delivery worker. Measure sustained
   messages-per-second and the `LeaseNext` query plan.
7. **Retention test.** Apply a retention policy that purges
   1% of rows. Measure the time and the lock contention.
8. **Failure test.** Kill the runtime mid-operation, restart,
   measure the recovery time, and confirm the queue resumes
   from a consistent state.

Until the project runs this campaign and publishes the
numbers, the wording at the top of this document is the
authoritative version. Do not promise "billion email
support" without the receipts.

## 9. Document version

This document is part of the
`feature/backend-completion-v1-enterprise-db` branch.
Future schema and index changes must update the index
strategy table in §5 and the "What is implemented now"
section in §1.
