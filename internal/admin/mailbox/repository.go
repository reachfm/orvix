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
		"SELECT m.id, m.domain_id, m.tenant_id, m.email, m.local_part, m.name, m.status, m.quota_mb, m.used_bytes, m.msg_count, m.is_admin, m.allow_smtp, m.allow_imap, m.allow_pop3, m.allow_jmap, m.mfa_enabled, m.send_limit_per_hour, COALESCE(m.mail_access_mode,'inherit'), m.version, m.last_login, COALESCE(m.last_ip,''), m.created_at, m.updated_at FROM coremail_mailboxes m WHERE m.id = "+r.dialect.Placeholder(1)+" AND m.tenant_id = "+r.dialect.Placeholder(2)+" AND m.deleted_at IS NULL",
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

	query := `SELECT m.id, m.domain_id, m.tenant_id, m.email, m.local_part, m.name, m.status, m.quota_mb, m.used_bytes, m.msg_count,
		m.is_admin, m.allow_smtp, m.allow_imap, m.allow_pop3, m.allow_jmap, m.mfa_enabled, m.send_limit_per_hour,
		COALESCE(m.mail_access_mode,'inherit'), m.version,
		m.last_login, COALESCE(m.last_ip,''), m.created_at, m.updated_at
		FROM coremail_mailboxes m WHERE ` + clause + ` ORDER BY m.created_at DESC LIMIT ` + r.dialect.Placeholder(len(args)+1) + ` OFFSET ` + r.dialect.Placeholder(len(args)+2)
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
			 allow_webmail, send_limit_per_hour, recv_limit_per_hour, mail_access_mode, version, created_at, updated_at)
		VALUES (`+r.dialect.Placeholders(21)+`)`,
		m.DomainID, m.TenantID, m.LocalPart, m.Email, m.Name, passwordHash, string(MailboxAuthSchemeArgon2id),
		string(m.Status), m.QuotaMB, boolToInt(m.IsAdmin),
		boolToInt(m.AllowSMTP), boolToInt(m.AllowIMAP), boolToInt(m.AllowPOP3), boolToInt(m.AllowJMAP),
		true, m.SendLimit, 1000, NormalizeMailAccessMode(m.MailAccessMode), 1, m.CreatedAt, m.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create mailbox: %w", err)
	}
	id, _ := res.LastInsertId()
	m.ID = uint(id)
	m.MailAccessMode = string(NormalizeMailAccessMode(m.MailAccessMode))
	m.Version = 1
	return m, nil
}

func (r *AdminMailboxRepo) Update(ctx context.Context, m *AdminMailbox) error {
	m.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`UPDATE coremail_mailboxes SET name=`+r.dialect.Placeholder(1)+`, quota_mb=`+r.dialect.Placeholder(2)+`, is_admin=`+r.dialect.Placeholder(3)+`, allow_smtp=`+r.dialect.Placeholder(4)+`, allow_imap=`+r.dialect.Placeholder(5)+`, allow_pop3=`+r.dialect.Placeholder(6)+`, allow_jmap=`+r.dialect.Placeholder(7)+`, send_limit_per_hour=`+r.dialect.Placeholder(8)+`, mail_access_mode=`+r.dialect.Placeholder(9)+`, updated_at=`+r.dialect.Placeholder(10)+`
		 WHERE id=`+r.dialect.Placeholder(11)+` AND tenant_id=`+r.dialect.Placeholder(12)+` AND deleted_at IS NULL`,
		m.Name, m.QuotaMB, boolToInt(m.IsAdmin), boolToInt(m.AllowSMTP), boolToInt(m.AllowIMAP),
		boolToInt(m.AllowPOP3), boolToInt(m.AllowJMAP), m.SendLimit, NormalizeMailAccessMode(m.MailAccessMode), m.UpdatedAt, m.ID, m.TenantID,
	)
	return err
}

// UpdateMailAccessMode is the guarded access-mode mutation. The
// tenant_id predicate is IN the SQL, so a cross-tenant id affects zero
// rows and resolves to a safe not-found contract (no disclosure that
// the mailbox exists under another tenant). The version predicate
// provides optimistic concurrency: a concurrent guarded mutation wins,
// the loser sees zero rows affected and maps to a precondition failure.
// On success it returns the new version.
func (r *AdminMailboxRepo) UpdateMailAccessMode(ctx context.Context, id, tenantID uint, mode string, expectedVersion int) (affected int64, newVersion int, err error) {
	normalized := string(NormalizeMailAccessMode(mode))
	res, err := r.db.ExecContext(ctx,
		"UPDATE coremail_mailboxes SET mail_access_mode="+r.dialect.Placeholder(1)+", version=version+1, updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4)+" AND version="+r.dialect.Placeholder(5)+" AND deleted_at IS NULL",
		normalized, time.Now().UTC(), id, tenantID, expectedVersion)
	if err != nil {
		return 0, 0, err
	}
	affected, err = res.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	return affected, expectedVersion + 1, nil
}

// GetMailAccessModeState reads the configured and effective access
// mode for a mailbox. The effective mode resolves "inherit" through
// the owning domain. A corrupt mailbox value (anything outside the
// three canonical persisted values) fails closed to internal_only;
// a corrupt domain value fails closed to internal_only as well.
// sql.ErrNoRows is returned for unknown or cross-tenant mailboxes.
func (r *AdminMailboxRepo) GetMailAccessModeState(ctx context.Context, id, tenantID uint) (configured, effective string, version int, err error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT m.mail_access_mode, d.mail_access_mode, m.version
		 FROM coremail_mailboxes m LEFT JOIN coremail_domains d ON d.id = m.domain_id
		 WHERE m.id=`+r.dialect.Placeholder(1)+` AND m.tenant_id=`+r.dialect.Placeholder(2)+` AND m.deleted_at IS NULL`,
		id, tenantID)
	var mailboxMode, domainMode sql.NullString
	if err := row.Scan(&mailboxMode, &domainMode, &version); err != nil {
		return "", "", 0, err
	}
	configured = string(NormalizeMailAccessMode(mailboxMode.String))
	effective = resolveEffectiveMailAccessMode(configured, domainMode.String)
	return configured, effective, version, nil
}

// resolveEffectiveMailAccessMode is the single effective-mode
// resolution used by the admin read path. It mirrors the canonical
// mailpolicy package semantics: an explicit mailbox mode wins; inherit
// resolves through the domain (empty domain -> the established
// internal_external default); any corrupt value fails closed to
// internal_only. The delivery-path policy service
// (internal/coremail/mailpolicy) applies the identical rules.
func resolveEffectiveMailAccessMode(mailboxMode, domainMode string) string {
	switch MailAccessMode(mailboxMode) {
	case MailAccessInternalOnly, MailAccessInternalExternal:
		return mailboxMode
	}
	// inherit (or anything unrecognized at the mailbox level — treat
	// as inherit + fail closed through the domain rules below).
	switch MailAccessMode(strings.TrimSpace(domainMode)) {
	case MailAccessInternalOnly, MailAccessInternalExternal:
		return domainMode
	case "":
		// Pre-column domain rows: the established default.
		return string(MailAccessInternalExternal)
	default:
		// Corrupt domain value: fail closed.
		return string(MailAccessInternalOnly)
	}
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
		"UPDATE coremail_mailboxes SET password_hash="+r.dialect.Placeholder(1)+", auth_scheme="+r.dialect.Placeholder(2)+", updated_at="+r.dialect.Placeholder(3)+" WHERE id="+r.dialect.Placeholder(4)+" AND tenant_id="+r.dialect.Placeholder(5)+" AND deleted_at IS NULL",
		passwordHash, string(MailboxAuthSchemeArgon2id), time.Now().UTC(), id, tenantID)
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

// ResolveDomain looks up a domain by name inside the authenticated tenant
// scope and returns its persisted ID and status. The query is tenant-scoped
// so a domain owned by another tenant yields sql.ErrNoRows (indistinguishable
// from an absent domain). Deleted domains also yield sql.ErrNoRows.
func (r *AdminMailboxRepo) ResolveDomain(ctx context.Context, name string, tenantID uint) (id uint, status string, err error) {
	var deletedAt *string
	err = r.db.QueryRowContext(ctx,
		"SELECT id, status, deleted_at FROM coremail_domains WHERE name="+r.dialect.Placeholder(1)+" AND tenant_id="+r.dialect.Placeholder(2),
		name, tenantID).Scan(&id, &status, &deletedAt)
	if err != nil {
		return 0, "", err
	}
	if deletedAt != nil {
		return 0, "", sql.ErrNoRows
	}
	return id, status, nil
}

// DomainAllocation is the enforceable allocation state of a domain, read
// inside the caller's transaction. The sentinel encoding of the three limit
// fields is documented on domain.LimitInherit / domain.LimitUnlimited.
type DomainAllocation struct {
	DomainID              uint
	Status                string
	MaxMailboxes          int
	MaxQuotaMB            int64
	DefaultMailboxQuotaMB int64
	// OrgMaxMailboxes is the organization plan ceiling used to resolve an
	// inheriting domain. 0 or negative means the plan is unlimited.
	OrgMaxMailboxes int
}

// ResolveDomainAllocation resolves a domain by name inside the authenticated
// tenant scope AND reads the allocation limits needed to enforce mailbox caps
// and quota bounds. It is tenant-scoped exactly like ResolveDomain, so a domain
// owned by another tenant is indistinguishable from an absent one
// (sql.ErrNoRows) and cross-tenant existence is never leaked.
//
// On PostgreSQL the domain row is locked FOR UPDATE so that two concurrent
// mailbox creations against the same domain serialize on it and cannot both
// observe the same pre-insert count. SQLite serializes writers at the
// transaction level and does not support the clause.
func (r *AdminMailboxRepo) ResolveDomainAllocation(ctx context.Context, name string, tenantID uint, lock bool) (*DomainAllocation, error) {
	q := "SELECT d.id, d.status, d.deleted_at, d.max_mailboxes, d.max_quota_mb, COALESCE(d.default_mailbox_quota_mb,0) " +
		"FROM coremail_domains d WHERE d.name=" + r.dialect.Placeholder(1) + " AND d.tenant_id=" + r.dialect.Placeholder(2)
	if lock && r.dialect.IsPostgres() {
		q += " FOR UPDATE"
	}
	var a DomainAllocation
	var deletedAt *string
	if err := r.db.QueryRowContext(ctx, q, name, tenantID).Scan(
		&a.DomainID, &a.Status, &deletedAt, &a.MaxMailboxes, &a.MaxQuotaMB, &a.DefaultMailboxQuotaMB); err != nil {
		return nil, err
	}
	if deletedAt != nil {
		return nil, sql.ErrNoRows
	}
	// The organization ceiling is what an inheriting domain resolves to. A
	// missing tenant row leaves OrgMaxMailboxes at 0, which ResolveMailboxCap
	// treats as an unlimited plan — the same reading every pre-existing tenant
	// already had, so this cannot retroactively lock anyone out.
	_ = r.db.QueryRowContext(ctx,
		"SELECT COALESCE(max_mailboxes,0) FROM tenants WHERE id="+r.dialect.Placeholder(1)+" AND deleted_at IS NULL",
		tenantID).Scan(&a.OrgMaxMailboxes)
	return &a, nil
}

// CountActiveByDomain counts the live (non-soft-deleted) mailboxes on a domain
// inside the caller's transaction. It is the count the domain mailbox cap is
// enforced against.
func (r *AdminMailboxRepo) CountActiveByDomain(ctx context.Context, domainID, tenantID uint) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id="+r.dialect.Placeholder(1)+
			" AND tenant_id="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL", domainID, tenantID).Scan(&n)
	return n, err
}

// GetDomainQuotaBounds returns the per-mailbox quota ceiling of the domain that
// owns a mailbox. It is used by the quota-CHANGE path, which resolves the
// domain from the mailbox rather than from an address.
func (r *AdminMailboxRepo) GetDomainQuotaBounds(ctx context.Context, mailboxID, tenantID uint) (maxQuotaMB int64, err error) {
	err = r.db.QueryRowContext(ctx,
		"SELECT d.max_quota_mb FROM coremail_domains d JOIN coremail_mailboxes m ON m.domain_id = d.id "+
			"WHERE m.id="+r.dialect.Placeholder(1)+" AND m.tenant_id="+r.dialect.Placeholder(2)+
			" AND m.deleted_at IS NULL AND d.deleted_at IS NULL", mailboxID, tenantID).Scan(&maxQuotaMB)
	return maxQuotaMB, err
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
		&m.SendLimit, &m.MailAccessMode, &m.Version, &m.LastLogin, &m.LastIP, &m.CreatedAt, &m.UpdatedAt,
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
	m.MailAccessMode = string(NormalizeMailAccessMode(m.MailAccessMode))
	if m.MailAccessMode == "" {
		m.MailAccessMode = string(MailAccessInherit)
	}
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
