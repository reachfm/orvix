package webmailmgmt

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/coremail"
)

type Service struct {
	engine  *coremail.Engine
	storeDB *sql.DB
}

func NewService(engine *coremail.Engine, storeDB *sql.DB) *Service {
	return &Service{engine: engine, storeDB: storeDB}
}

func (s *Service) ensureTables(ctx context.Context) error {
	if s.engine == nil || s.engine.DB == nil {
		return fmt.Errorf("engine not available")
	}
	_, err := s.engine.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS webmail_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox_id INTEGER NOT NULL,
			ip TEXT NOT NULL,
			user_agent TEXT DEFAULT '',
			created_at DATETIME NOT NULL,
			last_seen_at DATETIME NOT NULL,
			expires_at DATETIME,
			revoked_at DATETIME
		)
	`)
	if err != nil {
		return err
	}
	if _, err := s.engine.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_webmail_sessions_mailbox ON webmail_sessions(mailbox_id, revoked_at)`); err != nil {
		return err
	}
	_, err = s.engine.DB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS webmail_login_activity (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			mailbox_id INTEGER NOT NULL,
			success INTEGER NOT NULL DEFAULT 0,
			ip TEXT NOT NULL,
			user_agent TEXT DEFAULT '',
			attempted_at DATETIME NOT NULL
		)
	`)
	if err != nil {
		return err
	}
	_, err = s.engine.DB.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_webmail_login_activity_mailbox ON webmail_login_activity(mailbox_id)`)
	return err
}

func (s *Service) withDB(ctx context.Context) error {
	if err := s.ensureTables(ctx); err != nil {
		return err
	}
	if s.engine == nil || s.engine.DB == nil {
		return fmt.Errorf("database not available")
	}
	return nil
}

func (s *Service) RecordSession(ctx context.Context, mailboxID uint, ip, userAgent string) error {
	if err := s.withDB(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := s.engine.DB.ExecContext(ctx, `
		INSERT INTO webmail_sessions (mailbox_id, ip, user_agent, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
	`, mailboxID, ip, userAgent, now, now)
	return err
}

func (s *Service) ListSessions(ctx context.Context, mailboxID *uint) ([]WebmailSession, error) {
	if err := s.withDB(ctx); err != nil {
		return nil, err
	}
	var rows *sql.Rows
	var err error
	if mailboxID != nil && *mailboxID > 0 {
		rows, err = s.engine.DB.QueryContext(ctx, `
			SELECT ws.id, ws.mailbox_id, cm.email, ws.ip, ws.user_agent, ws.created_at, ws.last_seen_at, ws.expires_at, ws.revoked_at
			FROM webmail_sessions ws
			LEFT JOIN coremail_mailboxes cm ON cm.id = ws.mailbox_id
			WHERE ws.revoked_at IS NULL AND ws.mailbox_id = ?
			ORDER BY ws.last_seen_at DESC, ws.id DESC
		`, *mailboxID)
	} else {
		rows, err = s.engine.DB.QueryContext(ctx, `
			SELECT ws.id, ws.mailbox_id, cm.email, ws.ip, ws.user_agent, ws.created_at, ws.last_seen_at, ws.expires_at, ws.revoked_at
			FROM webmail_sessions ws
			LEFT JOIN coremail_mailboxes cm ON cm.id = ws.mailbox_id
			WHERE ws.revoked_at IS NULL
			ORDER BY ws.last_seen_at DESC, ws.id DESC
		`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []WebmailSession
	for rows.Next() {
		var s WebmailSession
		if err := rows.Scan(&s.ID, &s.MailboxID, &s.Email, &s.IP, &s.UserAgent, &s.CreatedAt, &s.LastSeenAt, &s.ExpiresAt, &s.RevokedAt); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	if result == nil {
		result = []WebmailSession{}
	}
	return result, rows.Err()
}

func (s *Service) RevokeSession(ctx context.Context, sessionID uint) error {
	if err := s.withDB(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	res, err := s.engine.DB.ExecContext(ctx, `UPDATE webmail_sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, now, sessionID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("session not found")
	}
	return nil
}

func (s *Service) RevokeAllSessions(ctx context.Context, mailboxID uint) error {
	if err := s.withDB(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err := s.engine.DB.ExecContext(ctx, `UPDATE webmail_sessions SET revoked_at = ? WHERE mailbox_id = ? AND revoked_at IS NULL`, now, mailboxID)
	return err
}

func (s *Service) GetLoginActivity(ctx context.Context, mailboxID uint) (*LoginActivity, error) {
	if err := s.withDB(ctx); err != nil {
		return nil, err
	}
	var result LoginActivity

	if err := s.engine.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM webmail_login_activity WHERE mailbox_id = ? AND success = 1`, mailboxID).Scan(&result.SuccessfulLogins); err != nil {
		return nil, err
	}
	if err := s.engine.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM webmail_login_activity WHERE mailbox_id = ? AND success = 0`, mailboxID).Scan(&result.FailedLogins); err != nil {
		return nil, err
	}

	var lastLoginStr, lastFailedStr sql.NullString
	if err := s.engine.DB.QueryRowContext(ctx, `SELECT MAX(attempted_at) FROM webmail_login_activity WHERE mailbox_id = ? AND success = 1`, mailboxID).Scan(&lastLoginStr); err != nil {
		return nil, err
	}
	if err := s.engine.DB.QueryRowContext(ctx, `SELECT MAX(attempted_at) FROM webmail_login_activity WHERE mailbox_id = ? AND success = 0`, mailboxID).Scan(&lastFailedStr); err != nil {
		return nil, err
	}

	if lastLoginStr.Valid && lastLoginStr.String != "" {
		layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04:05.999999999-07:00"}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, lastLoginStr.String); err == nil {
				result.LastLoginAt = &t
				break
			}
		}
	}
	if lastFailedStr.Valid && lastFailedStr.String != "" {
		layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02 15:04:05.999999999-07:00"}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, lastFailedStr.String); err == nil {
				result.LastFailedLoginAt = &t
				break
			}
		}
	}

	return &result, nil
}

func (s *Service) RecordLoginActivity(ctx context.Context, mailboxID uint, success bool, ip, userAgent string) error {
	if err := s.withDB(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	successInt := 0
	if success {
		successInt = 1
	}
	_, err := s.engine.DB.ExecContext(ctx, `
		INSERT INTO webmail_login_activity (mailbox_id, success, ip, user_agent, attempted_at)
		VALUES (?, ?, ?, ?, ?)
	`, mailboxID, successInt, ip, userAgent, now)
	return err
}

func (s *Service) GetStorageMetrics(ctx context.Context, mailboxID uint) (*StorageMetrics, error) {
	if s.storeDB == nil {
		return nil, fmt.Errorf("mail store not available")
	}

	// Verify mailbox exists.
	var exists int
	if err := s.engine.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL`, mailboxID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, fmt.Errorf("mailbox not found")
	}

	var result StorageMetrics

	// MessageCount from canonical coremail_messages (not cached mailbox counters).
	if err := s.storeDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_messages WHERE mailbox_id = ? AND purged_at IS NULL`, mailboxID).Scan(&result.MessageCount); err != nil {
		result.MessageCount = 0
	}

	// MailboxSize from canonical size_bytes in coremail_messages (not cached used_bytes).
	if err := s.storeDB.QueryRowContext(ctx, `SELECT COALESCE(SUM(size_bytes), 0) FROM coremail_messages WHERE mailbox_id = ? AND purged_at IS NULL`, mailboxID).Scan(&result.MailboxSize); err != nil {
		result.MailboxSize = 0
	}

	// Sent/Received counts from authoritative folder metadata only.
	var sentFolderID, inboxFolderID *uint
	rows, err := s.storeDB.QueryContext(ctx, `SELECT id, folder_type FROM coremail_folders WHERE mailbox_id = ? AND (folder_type = 'sent' OR folder_type = 'inbox')`, mailboxID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var fid uint
			var ftype string
			if err := rows.Scan(&fid, &ftype); err == nil {
				if ftype == "sent" {
					sentFolderID = &fid
				} else if ftype == "inbox" {
					inboxFolderID = &fid
				}
			}
		}
	}

	if sentFolderID != nil {
		err = s.storeDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_messages WHERE mailbox_id = ? AND folder_id = ? AND purged_at IS NULL`, mailboxID, *sentFolderID).Scan(&result.SentCount)
		if err != nil {
			result.SentCount = 0
		}
	}

	if inboxFolderID != nil {
		err = s.storeDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_messages WHERE mailbox_id = ? AND folder_id = ? AND purged_at IS NULL`, mailboxID, *inboxFolderID).Scan(&result.ReceivedCount)
		if err != nil {
			result.ReceivedCount = 0
		}
	}

	return &result, nil
}

func (s *Service) ListAccounts(ctx context.Context, search, domainFilter, statusFilter string, adminFilter *bool) ([]WebmailAccount, error) {
	if err := s.withDB(ctx); err != nil {
		return nil, err
	}

	query := `SELECT cm.id, cm.email, cm.status, d.name, cm.is_admin, cm.last_login, cm.created_at
		FROM coremail_mailboxes cm
		JOIN coremail_domains d ON d.id = cm.domain_id
		WHERE cm.deleted_at IS NULL`
	args := []interface{}{}

	if search != "" {
		query += ` AND LOWER(cm.email) LIKE ?`
		args = append(args, "%"+strings.ToLower(search)+"%")
	}
	if domainFilter != "" {
		query += ` AND d.name = ?`
		args = append(args, domainFilter)
	}
	if statusFilter != "" {
		query += ` AND cm.status = ?`
		args = append(args, statusFilter)
	}
	if adminFilter != nil {
		if *adminFilter {
			query += ` AND cm.is_admin = 1`
		} else {
			query += ` AND cm.is_admin = 0`
		}
	}
	query += ` ORDER BY cm.created_at DESC`

	rows, err := s.engine.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []WebmailAccount
	for rows.Next() {
		var a WebmailAccount
		if err := rows.Scan(&a.MailboxID, &a.Email, &a.Status, &a.Domain, &a.IsAdmin, &a.LastLoginAt, &a.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	if result == nil {
		result = []WebmailAccount{}
	}
	return result, rows.Err()
}

func (s *Service) ForceLogoutAll(ctx context.Context, mailboxID uint) error {
	return s.RevokeAllSessions(ctx, mailboxID)
}

func (s *Service) UnlockMailbox(ctx context.Context, mailboxID uint) error {
	mb, err := s.engine.Mailboxes.GetByID(ctx, mailboxID, nil)
	if err != nil {
		return err
	}
	if mb == nil {
		return fmt.Errorf("mailbox not found")
	}
	mb.Status = coremail.MailboxActive
	return s.engine.Mailboxes.Update(ctx, mb, nil)
}

func (s *Service) ResetWebmailPreferences(ctx context.Context, mailboxID uint) error {
	// No preferences table exists in v1; this is a future capability.
	// Currently a no-op that succeeds to allow the admin UI to proceed.
	return nil
}

func (s *Service) ClearFailedLoginCounters(ctx context.Context, mailboxID uint) error {
	if err := s.withDB(ctx); err != nil {
		return err
	}
	_, err := s.engine.DB.ExecContext(ctx, `DELETE FROM webmail_login_activity WHERE mailbox_id = ? AND success = 0`, mailboxID)
	return err
}
