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
		"SELECT id, domain_id, tenant_id, email, local_part, name, status, quota_mb, used_bytes, msg_count, is_admin, allow_smtp, allow_imap, allow_pop3, allow_jmap, mfa_enabled, send_limit_per_hour, last_login, COALESCE(last_ip,''), created_at, updated_at FROM coremail_mailboxes WHERE id = "+r.dialect.Placeholder(1)+" AND tenant_id = "+r.dialect.Placeholder(2)+" AND deleted_at IS NULL",
		id, tenantID)
	return scanAdminMailbox(row)
}

func (r *AdminMailboxRepo) List(ctx context.Context, filter MailboxFilter) ([]AdminMailbox, int64, error) {
	var where []string
	var args []interface{}
	where = append(where, "deleted_at IS NULL")

	if filter.TenantID != nil {
		where = append(where, "tenant_id = "+r.dialect.Placeholder(len(args)+1))
		args = append(args, *filter.TenantID)
	}
	if filter.DomainID != nil {
		where = append(where, "domain_id = "+r.dialect.Placeholder(len(args)+1))
		args = append(args, *filter.DomainID)
	}
	if filter.Status != nil {
		where = append(where, "status = "+r.dialect.Placeholder(len(args)+1))
		args = append(args, string(*filter.Status))
	}
	if filter.Search != "" {
		where = append(where, "(email LIKE "+r.dialect.Placeholder(len(args)+1)+" OR name LIKE "+r.dialect.Placeholder(len(args)+2)+")")
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
		FROM coremail_mailboxes WHERE ` + clause + ` ORDER BY created_at DESC LIMIT ` + r.dialect.Placeholder(len(args)+1) + ` OFFSET ` + r.dialect.Placeholder(len(args)+2)
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
		VALUES (`+r.dialect.Placeholders(19)+`)`,
		m.DomainID, m.TenantID, m.LocalPart, m.Email, m.Name, passwordHash, "bcrypt",
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
		`UPDATE coremail_mailboxes SET name=`+r.dialect.Placeholder(1)+`, quota_mb=`+r.dialect.Placeholder(2)+`, is_admin=`+r.dialect.Placeholder(3)+`, allow_smtp=`+r.dialect.Placeholder(4)+`, allow_imap=`+r.dialect.Placeholder(5)+`, allow_pop3=`+r.dialect.Placeholder(6)+`, allow_jmap=`+r.dialect.Placeholder(7)+`, send_limit_per_hour=`+r.dialect.Placeholder(8)+`, updated_at=`+r.dialect.Placeholder(9)+`
		 WHERE id=`+r.dialect.Placeholder(10)+` AND tenant_id=`+r.dialect.Placeholder(11)+` AND deleted_at IS NULL`,
		m.Name, m.QuotaMB, boolToInt(m.IsAdmin), boolToInt(m.AllowSMTP), boolToInt(m.AllowIMAP),
		boolToInt(m.AllowPOP3), boolToInt(m.AllowJMAP), m.SendLimit, m.UpdatedAt, m.ID, m.TenantID,
	)
	return err
}

func (r *AdminMailboxRepo) UpdateStatus(ctx context.Context, id, tenantID uint, status AdminMailboxStatus) error {
	now := time.Now().UTC()
	if status == AdminMailboxDeleted {
		_, err := r.db.ExecContext(ctx,
			"UPDATE coremail_mailboxes SET status="+r.dialect.Placeholder(1)+", deleted_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4),
			string(status), now, id, tenantID)
		return err
	}
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_mailboxes SET status="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4)+" AND deleted_at IS NULL",
		string(status), now, id, tenantID)
	return err
}

func (r *AdminMailboxRepo) UpdateStatusBulk(ctx context.Context, ids []uint, tenantID uint, status AdminMailboxStatus) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]interface{}, 0, len(ids)+3)
	args = append(args, string(status), time.Now().UTC(), tenantID)
	for i, id := range ids {
		placeholders[i] = r.dialect.Placeholder(i + 4)
		args = append(args, id)
	}
	query := fmt.Sprintf("UPDATE coremail_mailboxes SET status="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE tenant_id="+r.dialect.Placeholder(3)+" AND id IN (%s) AND deleted_at IS NULL",
		strings.Join(placeholders, ","))
	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *AdminMailboxRepo) UpdatePassword(ctx context.Context, id, tenantID uint, passwordHash string) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_mailboxes SET password_hash="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4)+" AND deleted_at IS NULL",
		passwordHash, time.Now().UTC(), id, tenantID)
	return err
}

func (r *AdminMailboxRepo) CountByTenant(ctx context.Context, tenantID uint) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+r.dialect.Placeholder(1)+" AND deleted_at IS NULL", tenantID).Scan(&count)
	return count, err
}

func (r *AdminMailboxRepo) CountByDomain(ctx context.Context, domainID, tenantID uint) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id="+r.dialect.Placeholder(1)+" AND tenant_id="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL", domainID, tenantID).Scan(&count)
	return count, err
}

func (r *AdminMailboxRepo) CountByStatus(ctx context.Context, tenantID uint, status AdminMailboxStatus) (int64, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+r.dialect.Placeholder(1)+" AND status="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL", tenantID, status).Scan(&count)
	return count, err
}

func (r *AdminMailboxRepo) SumQuotaUsed(ctx context.Context, tenantID uint) (int64, error) {
	var total sql.NullInt64
	err := r.db.QueryRowContext(ctx,
		"SELECT SUM(used_bytes) FROM coremail_mailboxes WHERE tenant_id="+r.dialect.Placeholder(1)+" AND deleted_at IS NULL", tenantID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func (r *AdminMailboxRepo) ExistsByEmail(ctx context.Context, email string, excludeID uint) (bool, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE email="+r.dialect.Placeholder(1)+" AND id!="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL", email, excludeID).Scan(&count)
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
