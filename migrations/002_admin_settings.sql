-- ENTERPRISE-BACKEND-COMPLETION: persisted admin settings.
--
-- The previous `PATCH /api/v1/admin/settings` endpoint returned
-- not_implemented because there was no durable backing store. This
-- migration adds a key/value table that the new admin settings
-- implementation reads on boot and updates on PATCH.
--
-- Format: one row per logical setting. The key is the canonical
-- dotted path (e.g. "mail_listeners.submission_enabled"); value is
-- a JSON-encoded payload (number / string / bool — never raw
-- secrets). The table is intentionally small and uncached; the
-- admin settings page is the only hot reader.
--
-- Indexes:
--   - PRIMARY KEY (key): point read by canonical path.
--   - idx_admin_settings_section: range scan by section for the
--     GET endpoint and the audit log.

CREATE TABLE IF NOT EXISTS admin_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    section TEXT NOT NULL,
    requires_restart INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_by INTEGER
);

CREATE INDEX IF NOT EXISTS idx_admin_settings_section
    ON admin_settings(section);

CREATE INDEX IF NOT EXISTS idx_admin_settings_updated_at
    ON admin_settings(updated_at);
