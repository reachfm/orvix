package coremail

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MailboxStatus represents the operational status of a mailbox.
type MailboxStatus string

const (
	MailboxActive    MailboxStatus = "active"
	MailboxSuspended MailboxStatus = "suspended"
	MailboxLocked    MailboxStatus = "locked"
	MailboxDeleted   MailboxStatus = "deleted"
)

// Mailbox represents an email mailbox (user account) on the Orvix server.
type Mailbox struct {
	ID        uint          `json:"id"`
	DomainID  uint          `json:"domain_id"`
	TenantID  uint          `json:"tenant_id"`
	LocalPart string        `json:"local_part"`
	Email     string        `json:"email"`
	Name      string        `json:"name"`

	// Authentication.
	PasswordHash     string     `json:"-"`
	AuthScheme      AuthScheme `json:"auth_scheme"`
	MFAEnabled      bool       `json:"mfa_enabled"`
	MFASecret       string     `json:"-"`
	AppPasswords    string     `json:"-"`

	// Status.
	Status MailboxStatus `json:"status"`

	// Quota.
	QuotaMB   int64 `json:"quota_mb"`   // max allowed (0 = domain default)
	UsedBytes int64 `json:"used_bytes"` // current usage
	MsgCount  int   `json:"msg_count"`  // current message count

	// Enterprise.
	IsAdmin    bool   `json:"is_admin"`
	IsForwarder bool  `json:"is_forwarder"`
	ForwardTo  string `json:"forward_to,omitempty"`
	Labels     string `json:"labels,omitempty"`

	// Protocol access.
	AllowSMTP    bool `json:"allow_smtp"`
	AllowIMAP    bool `json:"allow_imap"`
	AllowPOP3    bool `json:"allow_pop3"`
	AllowJMAP    bool `json:"allow_jmap"`
	AllowWebmail bool `json:"allow_webmail"`

	// Abuse prevention.
	SendLimitPerHour  int `json:"send_limit_per_hour"`
	RecvLimitPerHour  int `json:"recv_limit_per_hour"`

	// Metadata.
	LastLogin  *time.Time `json:"last_login,omitempty"`
	LastIP     string     `json:"last_ip,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

// GetID returns the mailbox ID (implements mailboxIfce for IMAP authenticator).
func (m *Mailbox) GetID() uint { return m.ID }

// MailboxFilter represents search/filter criteria for mailbox queries.
type MailboxFilter struct {
	DomainID   *uint
	TenantID   *uint
	Status     *MailboxStatus
	Search     string // email or name contains
	IsAdmin    *bool
	Pagination Pagination
}

// MailboxRepository defines the contract for mailbox persistence.
type MailboxRepository interface {
	Create(ctx context.Context, m *Mailbox, tx interface{}) error
	GetByID(ctx context.Context, id uint, tx interface{}) (*Mailbox, error)
	GetByEmail(ctx context.Context, email string, tx interface{}) (*Mailbox, error)
	List(ctx context.Context, filter MailboxFilter, tx interface{}) ([]Mailbox, int64, error)
	Update(ctx context.Context, m *Mailbox, tx interface{}) error
	Delete(ctx context.Context, id uint, tx interface{}) error
	UpdateQuota(ctx context.Context, id uint, deltaBytes int64, deltaMsgs int, tx interface{}) error
	UpdateLastLogin(ctx context.Context, id uint, ip string, tx interface{}) error
	CountByDomain(ctx context.Context, domainID uint, tx interface{}) (int64, error)
	CountByTenant(ctx context.Context, tenantID uint, tx interface{}) (int64, error)
	SumUsedBytesByDomain(ctx context.Context, domainID uint, tx interface{}) (int64, error)
	Exists(ctx context.Context, email string, tx interface{}) (bool, error)
}

// Ensure MailboxSQLRepo implements MailboxRepository at compile time.
var _ MailboxRepository = (*MailboxSQLRepo)(nil)

// MailboxSQLRepo implements MailboxRepository using database/sql.
type MailboxSQLRepo struct {
	db *sql.DB
}

func NewMailboxSQLRepo(db *sql.DB) *MailboxSQLRepo {
	return &MailboxSQLRepo{db: db}
}

func (r *MailboxSQLRepo) execer(tx interface{}) interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
} {
	if tx != nil {
		if t, ok := tx.(*sql.Tx); ok {
			return t
		}
	}
	return r.db
}

func (r *MailboxSQLRepo) Create(ctx context.Context, m *Mailbox, tx interface{}) error {
	if m.Status == "" {
		m.Status = MailboxActive
	}
	if m.AuthScheme == "" {
		m.AuthScheme = AuthSchemeArgon2ID
	}
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now

	e := r.execer(tx)
	res, err := e.ExecContext(ctx, `
		INSERT INTO coremail_mailboxes
			(domain_id, tenant_id, local_part, email, name,
			 password_hash, auth_scheme, mfa_enabled, mfa_secret, app_passwords,
			 status, quota_mb, used_bytes, msg_count,
			 is_admin, is_forwarder, forward_to, labels,
			 send_limit_per_hour, recv_limit_per_hour,
			 last_login, last_ip, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.DomainID, m.TenantID, m.LocalPart, m.Email, m.Name,
		m.PasswordHash, string(m.AuthScheme), boolToInt(m.MFAEnabled), m.MFASecret, m.AppPasswords,
		string(m.Status), m.QuotaMB,
		boolToInt(m.IsAdmin), boolToInt(m.IsForwarder), m.ForwardTo, m.Labels,
		m.SendLimitPerHour, m.RecvLimitPerHour,
		m.LastLogin, m.LastIP, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create mailbox: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create mailbox: get id: %w", err)
	}
	m.ID = uint(id)
	return nil
}

func (r *MailboxSQLRepo) GetByID(ctx context.Context, id uint, tx interface{}) (*Mailbox, error) {
	e := r.execer(tx)
	row := e.QueryRowContext(ctx, `
		SELECT id, domain_id, tenant_id, local_part, email, name,
		       password_hash, auth_scheme, mfa_enabled, COALESCE(mfa_secret,''), COALESCE(app_passwords,''),
		       status, quota_mb, used_bytes, msg_count,
		       is_admin, is_forwarder, COALESCE(forward_to,''), COALESCE(labels,''),
		       send_limit_per_hour, recv_limit_per_hour,
		       last_login, COALESCE(last_ip,''), created_at, updated_at, deleted_at
		FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL`, id)
	return scanMailbox(row)
}

func (r *MailboxSQLRepo) GetByEmail(ctx context.Context, email string, tx interface{}) (*Mailbox, error) {
	e := r.execer(tx)
	row := e.QueryRowContext(ctx, `
		SELECT id, domain_id, tenant_id, local_part, email, name,
		       password_hash, auth_scheme, mfa_enabled, COALESCE(mfa_secret,''), COALESCE(app_passwords,''),
		       status, quota_mb, used_bytes, msg_count,
		       is_admin, is_forwarder, COALESCE(forward_to,''), COALESCE(labels,''),
		       send_limit_per_hour, recv_limit_per_hour,
		       last_login, COALESCE(last_ip,''), created_at, updated_at, deleted_at
		FROM coremail_mailboxes WHERE email = ? AND deleted_at IS NULL`, email)
	return scanMailbox(row)
}

func (r *MailboxSQLRepo) List(ctx context.Context, filter MailboxFilter, tx interface{}) ([]Mailbox, int64, error) {
	e := r.execer(tx)
	filter.Pagination = filter.Pagination.Normalize()

	var where []string
	var args []interface{}
	where = append(where, "deleted_at IS NULL")

	if filter.DomainID != nil {
		where = append(where, "domain_id = ?")
		args = append(args, *filter.DomainID)
	}
	if filter.TenantID != nil {
		where = append(where, "tenant_id = ?")
		args = append(args, *filter.TenantID)
	}
	if filter.Status != nil {
		where = append(where, "status = ?")
		args = append(args, string(*filter.Status))
	}
	if filter.IsAdmin != nil {
		where = append(where, "is_admin = ?")
		args = append(args, boolToInt(*filter.IsAdmin))
	}
	if filter.Search != "" {
		where = append(where, "(email LIKE ? OR name LIKE ?)")
		s := "%" + filter.Search + "%"
		args = append(args, s, s)
	}

	clause := strings.Join(where, " AND ")

	var total int64
	countRow := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_mailboxes WHERE "+clause, args...)
	if err := countRow.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list mailboxes count: %w", err)
	}

	rows, err := e.QueryContext(ctx, `
		SELECT id, domain_id, tenant_id, local_part, email, name,
		       password_hash, auth_scheme, mfa_enabled, COALESCE(mfa_secret,''), COALESCE(app_passwords,''),
		       status, quota_mb, used_bytes, msg_count,
		       is_admin, is_forwarder, COALESCE(forward_to,''), COALESCE(labels,''),
		       send_limit_per_hour, recv_limit_per_hour,
		       last_login, COALESCE(last_ip,''), created_at, updated_at, deleted_at
		FROM coremail_mailboxes WHERE `+clause+`
		ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		append(args, filter.Pagination.Limit, filter.Pagination.Offset)...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list mailboxes: %w", err)
	}
	defer rows.Close()

	var mailboxes []Mailbox
	for rows.Next() {
		m, err := scanMailbox(rows)
		if err != nil {
			return nil, 0, err
		}
		mailboxes = append(mailboxes, *m)
	}
	return mailboxes, total, rows.Err()
}

func (r *MailboxSQLRepo) Update(ctx context.Context, m *Mailbox, tx interface{}) error {
	m.UpdatedAt = time.Now().UTC()
	e := r.execer(tx)
	_, err := e.ExecContext(ctx, `
		UPDATE coremail_mailboxes SET
			name=?, password_hash=?, auth_scheme=?, mfa_enabled=?, mfa_secret=?, app_passwords=?,
			status=?, quota_mb=?, is_admin=?, is_forwarder=?, forward_to=?, labels=?,
			send_limit_per_hour=?, recv_limit_per_hour=?, updated_at=?
		WHERE id = ? AND deleted_at IS NULL`,
		m.Name, m.PasswordHash, string(m.AuthScheme), boolToInt(m.MFAEnabled), m.MFASecret, m.AppPasswords,
		string(m.Status), m.QuotaMB, boolToInt(m.IsAdmin), boolToInt(m.IsForwarder), m.ForwardTo, m.Labels,
		m.SendLimitPerHour, m.RecvLimitPerHour, m.UpdatedAt, m.ID,
	)
	return err
}

func (r *MailboxSQLRepo) Delete(ctx context.Context, id uint, tx interface{}) error {
	e := r.execer(tx)
	now := time.Now().UTC()
	_, err := e.ExecContext(ctx, "UPDATE coremail_mailboxes SET status=?, deleted_at=? WHERE id=?", string(MailboxDeleted), now, id)
	return err
}

func (r *MailboxSQLRepo) UpdateQuota(ctx context.Context, id uint, deltaBytes int64, deltaMsgs int, tx interface{}) error {
	e := r.execer(tx)
	_, err := e.ExecContext(ctx,
		"UPDATE coremail_mailboxes SET used_bytes = used_bytes + ?, msg_count = msg_count + ? WHERE id = ? AND deleted_at IS NULL",
		deltaBytes, deltaMsgs, id)
	return err
}

func (r *MailboxSQLRepo) UpdateLastLogin(ctx context.Context, id uint, ip string, tx interface{}) error {
	now := time.Now().UTC()
	e := r.execer(tx)
	_, err := e.ExecContext(ctx, "UPDATE coremail_mailboxes SET last_login=?, last_ip=? WHERE id=?", now, ip, id)
	return err
}

func (r *MailboxSQLRepo) CountByDomain(ctx context.Context, domainID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id=? AND deleted_at IS NULL", domainID).Scan(&count)
	return count, err
}

func (r *MailboxSQLRepo) CountByTenant(ctx context.Context, tenantID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id=? AND deleted_at IS NULL", tenantID).Scan(&count)
	return count, err
}

func (r *MailboxSQLRepo) SumUsedBytesByDomain(ctx context.Context, domainID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var total sql.NullInt64
	err := e.QueryRowContext(ctx, "SELECT SUM(used_bytes) FROM coremail_mailboxes WHERE domain_id=? AND deleted_at IS NULL", domainID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func (r *MailboxSQLRepo) Exists(ctx context.Context, email string, tx interface{}) (bool, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_mailboxes WHERE email=? AND deleted_at IS NULL", email).Scan(&count)
	return count > 0, err
}

func scanMailbox(row interface {
	Scan(dest ...interface{}) error
}) (*Mailbox, error) {
	var m Mailbox
	var status, authScheme string
	var mfaEnabled, isAdmin, isForwarder int
	err := row.Scan(
		&m.ID, &m.DomainID, &m.TenantID, &m.LocalPart, &m.Email, &m.Name,
		&m.PasswordHash, &authScheme, &mfaEnabled, &m.MFASecret, &m.AppPasswords,
		&status, &m.QuotaMB, &m.UsedBytes, &m.MsgCount,
		&isAdmin, &isForwarder, &m.ForwardTo, &m.Labels,
		&m.SendLimitPerHour, &m.RecvLimitPerHour,
		&m.LastLogin, &m.LastIP, &m.CreatedAt, &m.UpdatedAt, &m.DeletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan mailbox: %w", err)
	}
	m.Status = MailboxStatus(status)
	m.AuthScheme = AuthScheme(authScheme)
	m.MFAEnabled = intToBool(mfaEnabled)
	m.IsAdmin = intToBool(isAdmin)
	m.IsForwarder = intToBool(isForwarder)
	// Default all protocols to allowed. The runtime can override
	// via admin PATCH /mailboxes/:id/protocols when the DB columns
	// exist. Callers that need protocol flags should use
	// scanMailboxWithProtocols instead.
	m.AllowSMTP = true
	m.AllowIMAP = true
	m.AllowPOP3 = true
	m.AllowJMAP = true
	m.AllowWebmail = true
	return &m, nil
}

// scanMailboxWithProtocols is like scanMailbox but handles the 31-column
// SELECT that includes the 5 protocol access columns (allow_smtp,
// allow_imap, allow_pop3, allow_jmap, allow_webmail). The protocol
// columns must be the last 5 columns in the SELECT.
func scanMailboxWithProtocols(row interface {
	Scan(dest ...interface{}) error
}) (*Mailbox, error) {
	var m Mailbox
	var status, authScheme string
	var mfaEnabled, isAdmin, isForwarder int
	var allowSMTP, allowIMAP, allowPOP3, allowJMAP, allowWebmail int
	err := row.Scan(
		&m.ID, &m.DomainID, &m.TenantID, &m.LocalPart, &m.Email, &m.Name,
		&m.PasswordHash, &authScheme, &mfaEnabled, &m.MFASecret, &m.AppPasswords,
		&status, &m.QuotaMB, &m.UsedBytes, &m.MsgCount,
		&isAdmin, &isForwarder, &m.ForwardTo, &m.Labels,
		&m.SendLimitPerHour, &m.RecvLimitPerHour,
		&m.LastLogin, &m.LastIP, &m.CreatedAt, &m.UpdatedAt, &m.DeletedAt,
		&allowSMTP, &allowIMAP, &allowPOP3, &allowJMAP, &allowWebmail,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan mailbox: %w", err)
	}
	m.Status = MailboxStatus(status)
	m.AuthScheme = AuthScheme(authScheme)
	m.MFAEnabled = intToBool(mfaEnabled)
	m.IsAdmin = intToBool(isAdmin)
	m.IsForwarder = intToBool(isForwarder)
	m.AllowSMTP = intToBool(allowSMTP)
	m.AllowIMAP = intToBool(allowIMAP)
	m.AllowPOP3 = intToBool(allowPOP3)
	m.AllowJMAP = intToBool(allowJMAP)
	m.AllowWebmail = intToBool(allowWebmail)
	return &m, nil
}
