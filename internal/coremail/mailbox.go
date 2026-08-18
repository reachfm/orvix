package coremail

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
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
	ID        uint   `json:"id"`
	DomainID  uint   `json:"domain_id"`
	TenantID  uint   `json:"tenant_id"`
	LocalPart string `json:"local_part"`
	Email     string `json:"email"`
	Name      string `json:"name"`

	// Authentication.
	PasswordHash string     `json:"-"`
	AuthScheme   AuthScheme `json:"auth_scheme"`
	MFAEnabled   bool       `json:"mfa_enabled"`
	MFASecret    string     `json:"-"`
	AppPasswords string     `json:"-"`

	// Status.
	Status MailboxStatus `json:"status"`

	// Quota.
	QuotaMB   int64 `json:"quota_mb"`   // max allowed (0 = domain default)
	UsedBytes int64 `json:"used_bytes"` // current usage
	MsgCount  int   `json:"msg_count"`  // current message count

	// Enterprise.
	IsAdmin     bool   `json:"is_admin"`
	IsForwarder bool   `json:"is_forwarder"`
	ForwardTo   string `json:"forward_to,omitempty"`
	Labels      string `json:"labels,omitempty"`

	// Protocol access.
	AllowSMTP    bool `json:"allow_smtp"`
	AllowIMAP    bool `json:"allow_imap"`
	AllowPOP3    bool `json:"allow_pop3"`
	AllowJMAP    bool `json:"allow_jmap"`
	AllowWebmail bool `json:"allow_webmail"`

	// Abuse prevention.
	SendLimitPerHour int `json:"send_limit_per_hour"`
	RecvLimitPerHour int `json:"recv_limit_per_hour"`

	// MailAccessMode is the per-mailbox mail-access policy
	// (MAILBOX-ACCESS-MODE-PHASE1). Canonical persisted values:
	// "inherit" (resolve through the domain), "internal_only",
	// "internal_external". The empty string is read as "inherit" for
	// rows created before the column existed.
	MailAccessMode string `json:"mail_access_mode"`

	// Version is the optimistic-concurrency guard used by guarded
	// mailbox mutations (the platform access-mode route).
	Version int `json:"version"`

	// Metadata.
	LastLogin *time.Time `json:"last_login,omitempty"`
	LastIP    string     `json:"last_ip,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
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
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewMailboxSQLRepo constructs the repository and detects the SQL
// dialect EAGERLY — never lazily inside a transaction (a probe on a
// single-connection SQLite pool would deadlock). NewEngine always
// runs before any transaction.
func NewMailboxSQLRepo(db *sql.DB) *MailboxSQLRepo {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &MailboxSQLRepo{db: db, dialect: dialect}
}

// getDialect returns the dialect detected at construction time.
func (r *MailboxSQLRepo) getDialect() *dbdialect.Info {
	return r.dialect
}

// qf rewrites ? placeholders to $N for PostgreSQL (no-op on SQLite).
func (r *MailboxSQLRepo) qf(sql string) string {
	return r.getDialect().Rewrite(sql)
}

// boolValue converts a bool into the dialect's column literal:
// INTEGER 0/1 on SQLite, native bool on PostgreSQL.
func (r *MailboxSQLRepo) boolValue(b bool) interface{} {
	if r.getDialect().IsPostgres() {
		return b
	}
	return boolToInt(b)
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
	insert := `
		INSERT INTO coremail_mailboxes
			(domain_id, tenant_id, local_part, email, name,
			 password_hash, auth_scheme, mfa_enabled, mfa_secret, app_passwords,
			 status, quota_mb, used_bytes, msg_count,
			 is_admin, is_forwarder, forward_to, labels,
			 send_limit_per_hour, recv_limit_per_hour,
			 last_login, last_ip, mail_access_mode, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`
	args := []interface{}{
		m.DomainID, m.TenantID, m.LocalPart, m.Email, m.Name,
		m.PasswordHash, string(m.AuthScheme), r.boolValue(m.MFAEnabled), m.MFASecret, m.AppPasswords,
		string(m.Status), m.QuotaMB,
		r.boolValue(m.IsAdmin), r.boolValue(m.IsForwarder), m.ForwardTo, m.Labels,
		m.SendLimitPerHour, m.RecvLimitPerHour,
		m.LastLogin, m.LastIP, accessModeOrDefault(m.MailAccessMode), m.CreatedAt, m.UpdatedAt,
	}
	if r.getDialect().IsPostgres() {
		var id uint
		if err := e.QueryRowContext(ctx, r.qf(insert)+" RETURNING id", args...).Scan(&id); err != nil {
			return fmt.Errorf("create mailbox: %w", err)
		}
		m.ID = id
		return nil
	}
	res, err := e.ExecContext(ctx, insert, args...)
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
	row := e.QueryRowContext(ctx, r.qf(`
		SELECT id, domain_id, tenant_id, local_part, email, name,
		       password_hash, auth_scheme, mfa_enabled, COALESCE(mfa_secret,''), COALESCE(app_passwords,''),
		       status, quota_mb, used_bytes, msg_count,
		       is_admin, is_forwarder, COALESCE(forward_to,''), COALESCE(labels,''),
		       send_limit_per_hour, recv_limit_per_hour,
		       last_login, COALESCE(last_ip,''), COALESCE(mail_access_mode,'inherit'), version, created_at, updated_at, deleted_at
		FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL`), id)
	return scanMailbox(row)
}

func (r *MailboxSQLRepo) GetByEmail(ctx context.Context, email string, tx interface{}) (*Mailbox, error) {
	e := r.execer(tx)
	row := e.QueryRowContext(ctx, r.qf(`
		SELECT id, domain_id, tenant_id, local_part, email, name,
		       password_hash, auth_scheme, mfa_enabled, COALESCE(mfa_secret,''), COALESCE(app_passwords,''),
		       status, quota_mb, used_bytes, msg_count,
		       is_admin, is_forwarder, COALESCE(forward_to,''), COALESCE(labels,''),
		       send_limit_per_hour, recv_limit_per_hour,
		       last_login, COALESCE(last_ip,''), COALESCE(mail_access_mode,'inherit'), version, created_at, updated_at, deleted_at
		FROM coremail_mailboxes WHERE email = ? AND deleted_at IS NULL`), email)
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
		args = append(args, r.boolValue(*filter.IsAdmin))
	}
	if filter.Search != "" {
		where = append(where, "(email LIKE ? OR name LIKE ?)")
		s := "%" + filter.Search + "%"
		args = append(args, s, s)
	}

	clause := strings.Join(where, " AND ")

	var total int64
	countRow := e.QueryRowContext(ctx, r.qf("SELECT COUNT(*) FROM coremail_mailboxes WHERE "+clause), args...)
	if err := countRow.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list mailboxes count: %w", err)
	}

	rows, err := e.QueryContext(ctx, r.qf(`
		SELECT id, domain_id, tenant_id, local_part, email, name,
		       password_hash, auth_scheme, mfa_enabled, COALESCE(mfa_secret,''), COALESCE(app_passwords,''),
		       status, quota_mb, used_bytes, msg_count,
		       is_admin, is_forwarder, COALESCE(forward_to,''), COALESCE(labels,''),
		       send_limit_per_hour, recv_limit_per_hour,
		       last_login, COALESCE(last_ip,''), COALESCE(mail_access_mode,'inherit'), version, created_at, updated_at, deleted_at
		FROM coremail_mailboxes WHERE `+clause+`
		ORDER BY created_at DESC LIMIT ? OFFSET ?`),
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
	_, err := e.ExecContext(ctx, r.qf(`
		UPDATE coremail_mailboxes SET
			name=?, password_hash=?, auth_scheme=?, mfa_enabled=?, mfa_secret=?, app_passwords=?,
			status=?, quota_mb=?, is_admin=?, is_forwarder=?, forward_to=?, labels=?,
			send_limit_per_hour=?, recv_limit_per_hour=?, mail_access_mode=?, updated_at=?
		WHERE id = ? AND deleted_at IS NULL`),
		m.Name, m.PasswordHash, string(m.AuthScheme), r.boolValue(m.MFAEnabled), m.MFASecret, m.AppPasswords,
		string(m.Status), m.QuotaMB, r.boolValue(m.IsAdmin), r.boolValue(m.IsForwarder), m.ForwardTo, m.Labels,
		m.SendLimitPerHour, m.RecvLimitPerHour, accessModeOrDefault(m.MailAccessMode), m.UpdatedAt, m.ID,
	)
	return err
}

func (r *MailboxSQLRepo) Delete(ctx context.Context, id uint, tx interface{}) error {
	e := r.execer(tx)
	now := time.Now().UTC()
	_, err := e.ExecContext(ctx, r.qf("UPDATE coremail_mailboxes SET status=?, deleted_at=? WHERE id=?"), string(MailboxDeleted), now, id)
	return err
}

func (r *MailboxSQLRepo) UpdateQuota(ctx context.Context, id uint, deltaBytes int64, deltaMsgs int, tx interface{}) error {
	e := r.execer(tx)
	_, err := e.ExecContext(ctx, r.qf(
		"UPDATE coremail_mailboxes SET used_bytes = used_bytes + ?, msg_count = msg_count + ? WHERE id = ? AND deleted_at IS NULL"),
		deltaBytes, deltaMsgs, id)
	return err
}

func (r *MailboxSQLRepo) UpdateLastLogin(ctx context.Context, id uint, ip string, tx interface{}) error {
	now := time.Now().UTC()
	e := r.execer(tx)
	_, err := e.ExecContext(ctx, r.qf("UPDATE coremail_mailboxes SET last_login=?, last_ip=? WHERE id=?"), now, ip, id)
	return err
}

func (r *MailboxSQLRepo) CountByDomain(ctx context.Context, domainID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, r.qf("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id=? AND deleted_at IS NULL"), domainID).Scan(&count)
	return count, err
}

func (r *MailboxSQLRepo) CountByTenant(ctx context.Context, tenantID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, r.qf("SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id=? AND deleted_at IS NULL"), tenantID).Scan(&count)
	return count, err
}

func (r *MailboxSQLRepo) SumUsedBytesByDomain(ctx context.Context, domainID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var total sql.NullInt64
	err := e.QueryRowContext(ctx, r.qf("SELECT SUM(used_bytes) FROM coremail_mailboxes WHERE domain_id=? AND deleted_at IS NULL"), domainID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}

func (r *MailboxSQLRepo) Exists(ctx context.Context, email string, tx interface{}) (bool, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, r.qf("SELECT COUNT(*) FROM coremail_mailboxes WHERE email=? AND deleted_at IS NULL"), email).Scan(&count)
	return count > 0, err
}

// scanMailbox handles both dialects: SQLite stores flag columns as
// INTEGER 0/1, PostgreSQL as BOOLEAN — databaseBoolValue covers both.
func scanMailbox(row interface {
	Scan(dest ...interface{}) error
}) (*Mailbox, error) {
	var m Mailbox
	var status, authScheme string
	var mfaEnabled, isAdmin, isForwarder interface{}
	err := row.Scan(
		&m.ID, &m.DomainID, &m.TenantID, &m.LocalPart, &m.Email, &m.Name,
		&m.PasswordHash, &authScheme, &mfaEnabled, &m.MFASecret, &m.AppPasswords,
		&status, &m.QuotaMB, &m.UsedBytes, &m.MsgCount,
		&isAdmin, &isForwarder, &m.ForwardTo, &m.Labels,
		&m.SendLimitPerHour, &m.RecvLimitPerHour,
		&m.LastLogin, &m.LastIP, &m.MailAccessMode, &m.Version, &m.CreatedAt, &m.UpdatedAt, &m.DeletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan mailbox: %w", err)
	}
	m.Status = MailboxStatus(status)
	m.AuthScheme = AuthScheme(authScheme)
	m.MFAEnabled = databaseBoolValue(mfaEnabled)
	m.IsAdmin = databaseBoolValue(isAdmin)
	m.IsForwarder = databaseBoolValue(isForwarder)
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
	var mfaEnabled, isAdmin, isForwarder interface{}
	var allowSMTP, allowIMAP, allowPOP3, allowJMAP, allowWebmail interface{}
	err := row.Scan(
		&m.ID, &m.DomainID, &m.TenantID, &m.LocalPart, &m.Email, &m.Name,
		&m.PasswordHash, &authScheme, &mfaEnabled, &m.MFASecret, &m.AppPasswords,
		&status, &m.QuotaMB, &m.UsedBytes, &m.MsgCount,
		&isAdmin, &isForwarder, &m.ForwardTo, &m.Labels,
		&m.SendLimitPerHour, &m.RecvLimitPerHour,
		&m.LastLogin, &m.LastIP, &m.MailAccessMode, &m.Version, &m.CreatedAt, &m.UpdatedAt, &m.DeletedAt,
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
	m.MFAEnabled = databaseBoolValue(mfaEnabled)
	m.IsAdmin = databaseBoolValue(isAdmin)
	m.IsForwarder = databaseBoolValue(isForwarder)
	m.AllowSMTP = databaseBoolValue(allowSMTP)
	m.AllowIMAP = databaseBoolValue(allowIMAP)
	m.AllowPOP3 = databaseBoolValue(allowPOP3)
	m.AllowJMAP = databaseBoolValue(allowJMAP)
	m.AllowWebmail = databaseBoolValue(allowWebmail)
	return &m, nil
}

// accessModeOrDefault normalizes a mailbox access-mode value for
// persistence: the empty string (rows written before the column
// existed) is stored as the canonical "inherit" value so every write
// boundary persists one of the three canonical values.
func accessModeOrDefault(mode string) string {
	if mode == "" {
		return "inherit"
	}
	return mode
}

// databaseBoolValue converts a scanned flag column to bool. SQLite
// returns INTEGER 0/1; PostgreSQL returns native bool.
func databaseBoolValue(v interface{}) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	case int:
		return x != 0
	case []byte:
		s := strings.ToLower(string(x))
		return s == "1" || s == "true" || s == "t"
	case string:
		s := strings.ToLower(x)
		return s == "1" || s == "true" || s == "t"
	default:
		return false
	}
}
