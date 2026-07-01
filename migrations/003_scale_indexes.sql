-- ENTERPRISE-BACKEND-COMPLETION: scale-ready indexes.
--
-- The previous schema in migrations/001_initial.sql plus the
-- coremail storage tables covers the basic "is this query
-- reasonable" bar. This migration adds indexes that are
-- required for hot paths in a billion-email-capable backend:
--
-- 1. Cursor pagination on (mailbox_id, folder_id, id) so the
--    webmail list endpoint can stream messages without using
--    OFFSET (which scales linearly with the page depth and
--    is the documented show-stopper for billion-row tables).
-- 2. (mailbox_id, folder_id, received_date DESC) for the
--    "newest first" mailbox list, which is the dominant read.
-- 3. (mailbox_id, folder_id, seen) for the unread counter
--    that powers the badge in the webmail sidebar.
-- 4. (created_at DESC) on coremail_queue for queue overview
--    pages.
-- 5. (status, next_attempt_at) on coremail_queue so the
--    delivery worker can pull ready jobs efficiently.
-- 6. (action, created_at) on audit_logs for action-filtered
--    audit queries that are common in incident response.
-- 7. (deleted_at) on coremail_messages so the retention
--    sweeper does not full-scan the messages table.
--
-- All indexes are CREATE INDEX IF NOT EXISTS so this
-- migration is idempotent and safe to re-run on a populated
-- database. Postgres-compatible syntax (modernc.org/sqlite
-- accepts it as well; the index is opaque to either
-- backend).

-- 1. Cursor pagination: (mailbox_id, folder_id, id) — id is
--    monotonic so a strict "id < cursor" predicate is stable
--    even when messages are inserted/removed concurrently.
CREATE INDEX IF NOT EXISTS idx_coremail_messages_mailbox_folder_id
    ON coremail_messages(mailbox_id, folder_id, id);

-- 2. "Newest first" mailbox list: (mailbox_id, folder_id, received_date DESC).
--    Hot path for the webmail Inbox view.
CREATE INDEX IF NOT EXISTS idx_coremail_messages_mailbox_folder_received
    ON coremail_messages(mailbox_id, folder_id, received_date);

-- 3. Unread counter: (mailbox_id, folder_id, seen).
--    Hot path for the folder sidebar badge.
CREATE INDEX IF NOT EXISTS idx_coremail_messages_mailbox_folder_unread
    ON coremail_messages(mailbox_id, folder_id, seen);

-- 4. Queue overview: (created_at) — used by admin queue list.
CREATE INDEX IF NOT EXISTS idx_coremail_queue_created_at
    ON coremail_queue(created_at);

-- 5. Delivery worker hot path: (status, next_attempt_at).
--    The worker scans this index to lease the next ready job.
CREATE INDEX IF NOT EXISTS idx_coremail_queue_status_next_attempt
    ON coremail_queue(status, next_attempt_at);

-- 6. Audit log action filter: (action, created_at).
--    Incident-response queries that filter by action and
--    recency dominate the audit read path.
CREATE INDEX IF NOT EXISTS idx_audit_logs_action_created
    ON audit_logs(action, created_at);

-- 7. Retention sweeper: (deleted_at) — partial scan to find
--    tombstones for the next purge cycle.
CREATE INDEX IF NOT EXISTS idx_coremail_messages_deleted_at
    ON coremail_messages(deleted_at);
