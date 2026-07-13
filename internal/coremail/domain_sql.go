package coremail

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Ensure DomainSQLRepo implements DomainRepository at compile time.
var _ DomainRepository = (*DomainSQLRepo)(nil)

// DomainSQLRepo implements DomainRepository using database/sql.
type DomainSQLRepo struct {
	db          *sql.DB
	dialect     *dbdialect.Info
	dialectInit sync.Once
}

func NewDomainSQLRepo(db *sql.DB) *DomainSQLRepo {
	return &DomainSQLRepo{db: db}
}

func (r *DomainSQLRepo) getDialect() *dbdialect.Info {
	r.dialectInit.Do(func() {
		d, err := dbdialect.Detect(r.db)
		if err != nil {
			d = dbdialect.FromDriver("sqlite")
		}
		r.dialect = d
	})
	return r.dialect
}

// qf replaces ? placeholders with $N for PostgreSQL.
func (r *DomainSQLRepo) qf(sql string) string {
	if !r.getDialect().IsPostgres() {
		return sql
	}
	var b []byte
	idx := 0
	for i := 0; i < len(sql); i++ {
		if sql[i] == '?' {
			idx++
			b = append(b, []byte(fmt.Sprintf("$%d", idx))...)
		} else {
			b = append(b, sql[i])
		}
	}
	return string(b)
}

func (r *DomainSQLRepo) execer(tx interface{}) interface {
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

func (r *DomainSQLRepo) Create(ctx context.Context, d *Domain, tx interface{}) error {
	if d.Status == "" {
		d.Status = DomainActive
	}
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now

	e := r.execer(tx)
	args := []interface{}{
		d.Name, d.TenantID, d.ResellerID, string(d.Status), d.Plan, d.Description,
		d.MaxMailboxes, d.MaxAliases, d.MaxQuotaMB,
		boolToInt(d.DKIMEnabled), d.DKIMSelector, boolToInt(d.DMARCEnabled), boolToInt(d.MTASTSEnabled),
		d.CatchallAddress, d.AbuseContact, d.Labels,
		d.CreatedAt, d.UpdatedAt,
	}

	if r.getDialect().IsPostgres() {
		args := []interface{}{
			d.Name, d.TenantID, d.ResellerID, string(d.Status), d.Plan, d.Description,
			d.MaxMailboxes, d.MaxAliases, d.MaxQuotaMB,
			d.DKIMEnabled, d.DKIMSelector, d.DMARCEnabled, d.MTASTSEnabled,
			d.CatchallAddress, d.AbuseContact, d.Labels,
			d.CreatedAt, d.UpdatedAt,
		}
		row := e.QueryRowContext(ctx, r.qf(`
			INSERT INTO coremail_domains
				(name, tenant_id, reseller_id, status, plan, description,
				 max_mailboxes, max_aliases, max_quota_mb,
				 dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled,
				 catchall_address, abuse_contact, labels,
				 mailbox_count, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
			RETURNING id
		`), args...)
		if err := row.Scan(&d.ID); err != nil {
			return fmt.Errorf("create domain: %w", err)
		}
		return nil
	}

	res, err := e.ExecContext(ctx, r.qf(`
		INSERT INTO coremail_domains
			(name, tenant_id, reseller_id, status, plan, description,
			 max_mailboxes, max_aliases, max_quota_mb,
			 dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled,
			 catchall_address, abuse_contact, labels,
			 mailbox_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
	`), args...)
	if err != nil {
		return fmt.Errorf("create domain: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create domain: get id: %w", err)
	}
	d.ID = uint(id)
	return nil
}

func (r *DomainSQLRepo) GetByID(ctx context.Context, id uint, tx interface{}) (*Domain, error) {
	e := r.execer(tx)
	row := e.QueryRowContext(ctx, r.qf(`
		SELECT id, name, tenant_id, COALESCE(reseller_id,0), status, plan,
		       COALESCE(description,''), max_mailboxes, max_aliases, max_quota_mb,
		       dkim_enabled, COALESCE(dkim_selector,''), dmarc_enabled, mtasts_enabled,
		       COALESCE(catchall_address,''), COALESCE(abuse_contact,''), COALESCE(labels,''),
		       mailbox_count, created_at, updated_at, deleted_at
		FROM coremail_domains WHERE id = ? AND deleted_at IS NULL`), id)
	return scanDomain(row)
}

func (r *DomainSQLRepo) GetByName(ctx context.Context, name string, tx interface{}) (*Domain, error) {
	e := r.execer(tx)
	row := e.QueryRowContext(ctx, r.qf(`
		SELECT id, name, tenant_id, COALESCE(reseller_id,0), status, plan,
		       COALESCE(description,''), max_mailboxes, max_aliases, max_quota_mb,
		       dkim_enabled, COALESCE(dkim_selector,''), dmarc_enabled, mtasts_enabled,
		       COALESCE(catchall_address,''), COALESCE(abuse_contact,''), COALESCE(labels,''),
		       mailbox_count, created_at, updated_at, deleted_at
		FROM coremail_domains WHERE name = ? AND deleted_at IS NULL`), name)
	return scanDomain(row)
}

func (r *DomainSQLRepo) List(ctx context.Context, filter DomainFilter, tx interface{}) ([]Domain, int64, error) {
	e := r.execer(tx)
	filter.Pagination = filter.Pagination.Normalize()

	var where []string
	var args []interface{}
	where = append(where, "deleted_at IS NULL")

	if filter.TenantID != nil {
		where = append(where, "tenant_id = ?")
		args = append(args, *filter.TenantID)
	}
	if filter.ResellerID != nil {
		where = append(where, "reseller_id = ?")
		args = append(args, *filter.ResellerID)
	}
	if filter.Status != nil {
		where = append(where, "status = ?")
		args = append(args, string(*filter.Status))
	}
	if filter.Search != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+filter.Search+"%")
	}

	clause := strings.Join(where, " AND ")

	var total int64
	countRow := e.QueryRowContext(ctx, r.qf("SELECT COUNT(*) FROM coremail_domains WHERE "+clause), args...)
	if err := countRow.Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("list domains count: %w", err)
	}

	rows, err := e.QueryContext(ctx, r.qf(`
		SELECT id, name, tenant_id, COALESCE(reseller_id,0), status, plan,
		       COALESCE(description,''), max_mailboxes, max_aliases, max_quota_mb,
		       dkim_enabled, COALESCE(dkim_selector,''), dmarc_enabled, mtasts_enabled,
		       COALESCE(catchall_address,''), COALESCE(abuse_contact,''), COALESCE(labels,''),
		       mailbox_count, created_at, updated_at, deleted_at
		FROM coremail_domains WHERE `+clause+`
		ORDER BY created_at DESC LIMIT ? OFFSET ?`),
		append(args, filter.Pagination.Limit, filter.Pagination.Offset)...,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("list domains: %w", err)
	}
	defer rows.Close()

	var domains []Domain
	for rows.Next() {
		d, err := scanDomain(rows)
		if err != nil {
			return nil, 0, err
		}
		domains = append(domains, *d)
	}
	return domains, total, rows.Err()
}

func (r *DomainSQLRepo) Update(ctx context.Context, d *Domain, tx interface{}) error {
	d.UpdatedAt = time.Now().UTC()
	e := r.execer(tx)
	_, err := e.ExecContext(ctx, `
		UPDATE coremail_domains SET
			status=?, plan=?, description=?, max_mailboxes=?, max_aliases=?, max_quota_mb=?,
			dkim_enabled=?, dkim_selector=?, dmarc_enabled=?, mtasts_enabled=?,
			catchall_address=?, abuse_contact=?, labels=?, updated_at=?
		WHERE id = ? AND deleted_at IS NULL`,
		string(d.Status), d.Plan, d.Description, d.MaxMailboxes, d.MaxAliases, d.MaxQuotaMB,
		boolToInt(d.DKIMEnabled), d.DKIMSelector, boolToInt(d.DMARCEnabled), boolToInt(d.MTASTSEnabled),
		d.CatchallAddress, d.AbuseContact, d.Labels, d.UpdatedAt, d.ID,
	)
	return err
}

func (r *DomainSQLRepo) Delete(ctx context.Context, id uint, tx interface{}) error {
	e := r.execer(tx)
	now := time.Now().UTC()
	_, err := e.ExecContext(ctx, "UPDATE coremail_domains SET status=?, deleted_at=? WHERE id=?", string(DomainDeleted), now, id)
	return err
}

func (r *DomainSQLRepo) CountByTenant(ctx context.Context, tenantID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_domains WHERE tenant_id=? AND deleted_at IS NULL", tenantID).Scan(&count)
	return count, err
}

func (r *DomainSQLRepo) CountByReseller(ctx context.Context, resellerID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_domains WHERE reseller_id=? AND deleted_at IS NULL", resellerID).Scan(&count)
	return count, err
}

func (r *DomainSQLRepo) Exists(ctx context.Context, name string, tx interface{}) (bool, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_domains WHERE name=? AND deleted_at IS NULL", name).Scan(&count)
	return count > 0, err
}

func scanDomain(row interface {
	Scan(dest ...interface{}) error
}) (*Domain, error) {
	var d Domain
	var status string
	var dkimEnabled, dmarcEnabled, mtastsEnabled int
	err := row.Scan(
		&d.ID, &d.Name, &d.TenantID, &d.ResellerID, &status, &d.Plan,
		&d.Description, &d.MaxMailboxes, &d.MaxAliases, &d.MaxQuotaMB,
		&dkimEnabled, &d.DKIMSelector, &dmarcEnabled, &mtastsEnabled,
		&d.CatchallAddress, &d.AbuseContact, &d.Labels,
		&d.MailboxCount, &d.CreatedAt, &d.UpdatedAt, &d.DeletedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan domain: %w", err)
	}
	d.Status = DomainStatus(status)
	d.DKIMEnabled = intToBool(dkimEnabled)
	d.DMARCEnabled = intToBool(dmarcEnabled)
	d.MTASTSEnabled = intToBool(mtastsEnabled)
	return &d, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(i int) bool {
	return i == 1
}
