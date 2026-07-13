package mailbox

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type AdminMailboxRepo struct {
	root    *sql.DB
	db      mailboxDB
	dialect *dbdialect.Info
}

type mailboxDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewAdminMailboxRepo(db *sql.DB) *AdminMailboxRepo {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &AdminMailboxRepo{root: db, db: db, dialect: d}
}

func (r *AdminMailboxRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.root.BeginTx(ctx, nil)
}

func (r *AdminMailboxRepo) WithTx(tx *sql.Tx) *AdminMailboxRepo {
	return &AdminMailboxRepo{root: r.root, db: tx, dialect: r.dialect}
}

func (r *AdminMailboxRepo) GetByID(ctx context.Context, id, tenantID uint) (*AdminMailbox, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT id, domain_id, tenant_id, email, local_part, name, status, quota_mb, used_bytes, msg_count, is_admin, allow_smtp, allow_imap, allow_pop3, allow_jmap, mfa_enabled, send_limit_per_hour, last_login, COALESCE(last_ip,''), created_at, updated_at FROM coremail_mailboxes WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL",
		id, tenantID)
	return scanAdminMailbox(row)
}

func (r *AdminMailboxRepo) List(ctx context.Context, filter MailboxFilter) ([]AdminMailbox, int64, error) {
	var where []string
	var args []interface{}
	where = append(where, "deleted_at IS NULL")

	if filter.TenantID != nil {
		where = append(where, "tenant_id = ?")
		args = append(args, *filter.TenantID)
	}
	if filter.DomainID != nil {
		where = append(where, "domain_id = ?")
		args = append(args, *filter.DomainID)
	}
	if filter.Status != nil {
		where = append(where, "status = ?")
		args = append(args, string(*filter.Status))
	}
	if filter.Search != "" {
		where = append(where, "(email LIKE ? OR name LIKE ?)")
		s := "%" + filter.Search + "%"
		args = append(args, s, s)
	}

	clause := strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_mailboxes WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count mailboxes: %w", err)
	}

	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}

	query := `SELECT id, domain_id, tenant_id, email, local_part, name, status, quota_mb, used_bytes, msg_count,
		is_admin, allow_smtp, allow_imap, allow_pop3, allow_jmap, mfa_enabled, send_limit_per_hour,
		last_login, COALESCE(last_ip,''), created_at, updated_at
		FROM coremail_mailboxes WHERE ` + clause + ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list mailboxes: %w", err)
	}
	defer rows.Close()

	var mailboxes []AdminMailbox
	for rows.Next() {
		m, err := scanAdminMailbox(rows)
		if err != nil {
			return nil, 0, err
		}
		mailboxes = append(mailboxes, *m)
	}
	return mailboxes, total, rows.Err()
}

func (r *AdminMailboxRepo) Create(ctx context.Context, m *AdminMailbox, passwordHash string) (*AdminMailbox, error) {
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now
	if m.Status == "" {
		m.Status = AdminMailboxActive
	}

	res, err := r.db.ExecContext(ctx,
		`INSERT INTO coremail_mailboxes
			(domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme,
			 status, quota_mb, is_admin, allow_smtp, allow_imap, allow_pop3, allow_jmap,
			 allow_webmail, send_limit_per_hour, recv_limit_per_hour, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'argon2id', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.DomainID, m.TenantID, m.LocalPart, m.Email, m.Name, passwordHash,
		string(m.Status), m.QuotaMB, boolToInt(m.IsAdmin),
		boolToInt(m.AllowSMTP), boolToInt(m.AllowIMAP), boolToInt(m.AllowPOP3), boolToInt(m.AllowJMAP),
		true, m.SendLimit, 1000, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create mailbox: %w", err)
	}
	id, _ := res.LastInsertId()
	m.ID = uint(id)
	return m, nil
}

func (r *AdminMailboxRepo) Update(ctx context.Context, m *AdminMailbox) error {
	m.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`UPDATE coremail_mailboxes SET name=?, quota_mb=?, is_admin=?, allow_smtp=?, allow_imap=?, allow_pop3=?, allow_jmap=?, send_limit_per_hour=?, updated_at=?
		 WHERE id=? AND tenant_id=? AND deleted_at IS NULL`,
		m.Name, m.QuotaMB, boolToInt(m.IsAdmin), boolToInt(m.AllowSMTP), boolToInt(m.AllowIMAP),
		boolToInt(m.AllowPOP3), boolToInt(m.AllowJMAP), m.SendLimit, m.UpdatedAt, m.ID, m.TenantID,
	)
	return err
}

func (r *AdminMailboxRepo) UpdateStatus(ctx context.Context, id, tenantID uint, status AdminMailboxStatus) error {
	now := time.Now().UTC()
	if status == AdminMailboxDeleted {
		_, err := r.db.ExecContext(ctx,
			"UPDATE coremail_mailboxes SET status=?, deleted_at=? WHERE id=? AND tenant_id=?",
			string(status), now, id, tenantID)
		return err
	}
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_mailboxes SET status=?, updated_at=? WHERE id=? AND tenant_id=? AND deleted_at IS NULL",
		string(status), now, id, tenantID)
	return err
}

func (r *AdminMailboxRepo) UpdateStatusBulk(ctx context.Context, ids []uint, tenantID uint, status AdminMailboxStatus) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)+2)
	args = append(args, string(status), time.Now().UTC(), tenantID)
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := fmt.Sprintf("UPDATE coremail_mailboxes SET status=?, updated_at=? WHERE tenant_id=? AND id IN (%s) AND deleted_at IS NULL",
		strings.Join(placeholders, ","))
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *AdminMailboxRepo) UpdatePassword(ctx context.Context, id, tenantID uint, passwordHash string) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_mailboxes SET password_hash=?, updated_at=? WHERE id=? AND tenant_id=? AND deleted_at IS NULL",
		passwordHash, time.Now().UTC(), id, tenantID)
	return err
}

func (r *AdminMailboxRepo) CountByTenant(ctx context.Context, tenantID uint) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id=? AND deleted_at IS NULL", tenantID).Scan(&count)
	return count, err
}

func (r *AdminMailboxRepo) CountByDomain(ctx context.Context, domainID, tenantID uint) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id=? AND tenant_id=? AND deleted_at IS NULL", domainID, tenantID).Scan(&count)
	return count, err
}

func (r *AdminMailboxRepo) CountByStatus(ctx context.Context, tenantID uint, status AdminMailboxStatus) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id=? AND status=? AND deleted_at IS NULL", tenantID, status).Scan(&count)
	return count, err
}

func (r *AdminMailboxRepo) SumQuotaUsed(ctx context.Context, tenantID uint) (int64, error) {
	var total sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		"SELECT SUM(used_bytes) FROM coremail_mailboxes WHERE tenant_id=? AND deleted_at IS NULL", tenantID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func (r *AdminMailboxRepo) ExistsByEmail(ctx context.Context, email string, excludeID uint) (bool, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE email=? AND id!=? AND deleted_at IS NULL", email, excludeID).Scan(&count)
	return count > 0, err
}

func scanAdminMailbox(row interface {
	Scan(dest ...interface{}) error
}) (*AdminMailbox, error) {
	var m AdminMailbox
	var status string
	var isAdmin, allowSMTP, allowIMAP, allowPOP3, allowJMAP, mfaEnabled int
	err := row.Scan(
		&m.ID, &m.DomainID, &m.TenantID, &m.Email, &m.LocalPart, &m.Name,
		&status, &m.QuotaMB, &m.UsedBytes, &m.MsgCount,
		&isAdmin, &allowSMTP, &allowIMAP, &allowPOP3, &allowJMAP, &mfaEnabled,
		&m.SendLimit, &m.LastLogin, &m.LastIP, &m.CreatedAt, &m.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan admin mailbox: %w", err)
	}
	m.Status = AdminMailboxStatus(status)
	m.IsAdmin = intToBool(isAdmin)
	m.AllowSMTP = intToBool(allowSMTP)
	m.AllowIMAP = intToBool(allowIMAP)
	m.AllowPOP3 = intToBool(allowPOP3)
	m.AllowJMAP = intToBool(allowJMAP)
	m.MFAEnabled = intToBool(mfaEnabled)
	return &m, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(i int) bool {
	return i != 0
}
