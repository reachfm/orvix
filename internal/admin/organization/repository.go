package organization

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type OrganizationRepo struct {
	root    *sql.DB
	db      organizationDB
	dialect *dbdialect.Info
}

type organizationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func NewOrganizationRepo(db *sql.DB) *OrganizationRepo {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &OrganizationRepo{root: db, db: db, dialect: d}
}

func (r *OrganizationRepo) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return r.root.BeginTx(ctx, nil)
}

func (r *OrganizationRepo) WithTx(tx *sql.Tx) *OrganizationRepo {
	return &OrganizationRepo{root: r.root, db: tx, dialect: r.dialect}
}

func (r *OrganizationRepo) GetByID(ctx context.Context, id uint) (*Organization, error) {
	row := r.db.QueryRowContext(ctx,
		"SELECT id, name, slug, domain, plan, max_domains, max_mailboxes, COALESCE(logo_url,''), COALESCE(primary_color,'#4F7CFF'), active, created_at, updated_at FROM tenants WHERE id = "+r.dialect.Placeholder(1)+" AND deleted_at IS NULL", id)
	return scanOrg(row)
}

func (r *OrganizationRepo) List(ctx context.Context, filter OrganizationFilter) ([]Organization, int64, error) {
	var where []string
	var args []interface{}
	where = append(where, "deleted_at IS NULL")

	if filter.Search != "" {
		where = append(where, "(name LIKE "+r.dialect.Placeholder(1)+" OR slug LIKE "+r.dialect.Placeholder(2)+" OR domain LIKE "+r.dialect.Placeholder(3)+")")
		s := "%" + filter.Search + "%"
		args = append(args, s, s, s)
	}
	if filter.Active != nil {
		if *filter.Active {
			where = append(where, "active = 1")
		} else {
			where = append(where, "active = 0")
		}
	}

	clause := strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants WHERE "+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}

	query := "SELECT id, name, slug, domain, plan, max_domains, max_mailboxes, COALESCE(logo_url,''), COALESCE(primary_color,'#4F7CFF'), active, created_at, updated_at FROM tenants WHERE " + clause + " ORDER BY created_at DESC LIMIT " + r.dialect.Placeholder(len(args)+1) + " OFFSET " + r.dialect.Placeholder(len(args)+2)
	args = append(args, filter.Limit, filter.Offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var orgs []Organization
	for rows.Next() {
		o, err := scanOrg(rows)
		if err != nil {
			return nil, 0, err
		}
		orgs = append(orgs, *o)
	}
	return orgs, total, rows.Err()
}

func (r *OrganizationRepo) Create(ctx context.Context, o *Organization) (*Organization, error) {
	now := time.Now().UTC()
	o.CreatedAt = now
	o.UpdatedAt = now
	if o.Plan == "" {
		o.Plan = "smb"
	}
	if o.MaxDomains == 0 {
		o.MaxDomains = 10
	}
	if o.MaxMailboxes == 0 {
		o.MaxMailboxes = 500
	}
	if o.PrimaryColor == "" {
		o.PrimaryColor = "#4F7CFF"
	}

	res, err := r.db.ExecContext(ctx,
		"INSERT INTO tenants (name, slug, domain, plan, max_domains, max_mailboxes, logo_url, primary_color, active, created_at, updated_at) VALUES ("+r.dialect.Placeholders(11)+")",
		o.Name, o.Slug, o.Domain, o.Plan, o.MaxDomains, o.MaxMailboxes, o.LogoURL, o.PrimaryColor, boolToInt(o.Active), o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("create organization: %w", err)
	}
	id, _ := res.LastInsertId()
	o.ID = uint(id)
	return o, nil
}

func (r *OrganizationRepo) Update(ctx context.Context, o *Organization) error {
	o.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		"UPDATE tenants SET name="+r.dialect.Placeholder(1)+", domain="+r.dialect.Placeholder(2)+", plan="+r.dialect.Placeholder(3)+", max_domains="+r.dialect.Placeholder(4)+", max_mailboxes="+r.dialect.Placeholder(5)+", logo_url="+r.dialect.Placeholder(6)+", primary_color="+r.dialect.Placeholder(7)+", updated_at="+r.dialect.Placeholder(8)+" WHERE id="+r.dialect.Placeholder(9)+" AND deleted_at IS NULL",
		o.Name, o.Domain, o.Plan, o.MaxDomains, o.MaxMailboxes, o.LogoURL, o.PrimaryColor, o.UpdatedAt, o.ID)
	return err
}

func (r *OrganizationRepo) SetActive(ctx context.Context, id uint, active bool) error {
	_, err := r.db.ExecContext(ctx, "UPDATE tenants SET active="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND deleted_at IS NULL", boolToInt(active), time.Now().UTC(), id)
	return err
}

func (r *OrganizationRepo) ExistsBySlug(ctx context.Context, slug string, excludeID uint) (bool, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM tenants WHERE slug="+r.dialect.Placeholder(1)+" AND id!="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL", slug, excludeID).Scan(&count)
	return count > 0, err
}

func (r *OrganizationRepo) CountAdmins(ctx context.Context, tenantID uint) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM users WHERE tenant_id="+r.dialect.Placeholder(1)+" AND role IN ('admin','superadmin') AND deleted_at IS NULL", tenantID).Scan(&count)
	return count, err
}

func scanOrg(row interface {
	Scan(dest ...interface{}) error
}) (*Organization, error) {
	var o Organization
	var active int
	err := row.Scan(&o.ID, &o.Name, &o.Slug, &o.Domain, &o.Plan, &o.MaxDomains, &o.MaxMailboxes, &o.LogoURL, &o.PrimaryColor, &active, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	o.Active = active != 0
	return &o, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
