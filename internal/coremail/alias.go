package coremail

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Alias represents an email alias that forwards to one or more destinations.
type Alias struct {
	ID        uint      `json:"id"`
	DomainID  uint      `json:"domain_id"`
	TenantID  uint      `json:"tenant_id"`
	FromAddr  string    `json:"from_addr"`  // full email: alias@domain
	ToAddr    string    `json:"to_addr"`    // destination(s): comma-separated
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// AliasFilter represents search/filter criteria for alias queries.
type AliasFilter struct {
	DomainID   *uint
	TenantID   *uint
	Search     string
	Pagination Pagination
}

// AliasRepository defines the contract for alias persistence.
type AliasRepository interface {
	Create(ctx context.Context, a *Alias, tx interface{}) error
	GetByID(ctx context.Context, id uint, tx interface{}) (*Alias, error)
	GetByFromAddr(ctx context.Context, fromAddr string, tx interface{}) (*Alias, error)
	ListByDomain(ctx context.Context, domainID uint, tx interface{}) ([]Alias, error)
	List(ctx context.Context, filter AliasFilter, tx interface{}) ([]Alias, int64, error)
	Update(ctx context.Context, a *Alias, tx interface{}) error
	Delete(ctx context.Context, id uint, tx interface{}) error
	Exists(ctx context.Context, fromAddr string, tx interface{}) (bool, error)
	CountByDomain(ctx context.Context, domainID uint, tx interface{}) (int64, error)
}

var _ AliasRepository = (*AliasSQLRepo)(nil)

// AliasSQLRepo implements AliasRepository using database/sql.
type AliasSQLRepo struct {
	db *sql.DB
}

func NewAliasSQLRepo(db *sql.DB) *AliasSQLRepo {
	return &AliasSQLRepo{db: db}
}

func (r *AliasSQLRepo) execer(tx interface{}) interface {
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

func (r *AliasSQLRepo) Create(ctx context.Context, a *Alias, tx interface{}) error {
	now := time.Now().UTC()
	a.CreatedAt = now
	a.UpdatedAt = now
	e := r.execer(tx)
	res, err := e.ExecContext(ctx, `
		INSERT INTO coremail_aliases (domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.DomainID, a.TenantID, a.FromAddr, a.ToAddr, boolToInt(a.Active), a.CreatedAt, a.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create alias: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("create alias: get id: %w", err)
	}
	a.ID = uint(id)
	return nil
}

func (r *AliasSQLRepo) GetByID(ctx context.Context, id uint, tx interface{}) (*Alias, error) {
	e := r.execer(tx)
	row := e.QueryRowContext(ctx, `SELECT id, domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at, deleted_at FROM coremail_aliases WHERE id=? AND deleted_at IS NULL`, id)
	return scanAlias(row)
}

func (r *AliasSQLRepo) GetByFromAddr(ctx context.Context, fromAddr string, tx interface{}) (*Alias, error) {
	e := r.execer(tx)
	row := e.QueryRowContext(ctx, `SELECT id, domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at, deleted_at FROM coremail_aliases WHERE from_addr=? AND active=1 AND deleted_at IS NULL`, fromAddr)
	return scanAlias(row)
}

func (r *AliasSQLRepo) ListByDomain(ctx context.Context, domainID uint, tx interface{}) ([]Alias, error) {
	e := r.execer(tx)
	rows, err := e.QueryContext(ctx, `SELECT id, domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at, deleted_at FROM coremail_aliases WHERE domain_id=? AND deleted_at IS NULL ORDER BY from_addr`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var aliases []Alias
	for rows.Next() {
		a, err := scanAlias(rows)
		if err != nil {
			return nil, err
		}
		aliases = append(aliases, *a)
	}
	return aliases, rows.Err()
}

func (r *AliasSQLRepo) List(ctx context.Context, filter AliasFilter, tx interface{}) ([]Alias, int64, error) {
	e := r.execer(tx)
	filter.Pagination = filter.Pagination.Normalize()
	var where []string
	var args []interface{}
	where = append(where, "deleted_at IS NULL")
	if filter.DomainID != nil {
		where = append(where, "domain_id=?")
		args = append(args, *filter.DomainID)
	}
	if filter.TenantID != nil {
		where = append(where, "tenant_id=?")
		args = append(args, *filter.TenantID)
	}
	if filter.Search != "" {
		where = append(where, "(from_addr LIKE ? OR to_addr LIKE ?)")
		s := "%" + filter.Search + "%"
		args = append(args, s, s)
	}
	clause := strings.Join(where, " AND ")

	var total int64
	e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_aliases WHERE "+clause, args...).Scan(&total)

	rows, err := e.QueryContext(ctx, `SELECT id, domain_id, tenant_id, from_addr, to_addr, active, created_at, updated_at, deleted_at FROM coremail_aliases WHERE `+clause+` ORDER BY from_addr LIMIT ? OFFSET ?`,
		append(args, filter.Pagination.Limit, filter.Pagination.Offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var aliases []Alias
	for rows.Next() {
		a, err := scanAlias(rows)
		if err != nil {
			return nil, 0, err
		}
		aliases = append(aliases, *a)
	}
	return aliases, total, rows.Err()
}

func (r *AliasSQLRepo) Update(ctx context.Context, a *Alias, tx interface{}) error {
	a.UpdatedAt = time.Now().UTC()
	e := r.execer(tx)
	_, err := e.ExecContext(ctx, `UPDATE coremail_aliases SET to_addr=?, active=?, updated_at=? WHERE id=? AND deleted_at IS NULL`,
		a.ToAddr, boolToInt(a.Active), a.UpdatedAt, a.ID)
	return err
}

func (r *AliasSQLRepo) Delete(ctx context.Context, id uint, tx interface{}) error {
	now := time.Now().UTC()
	e := r.execer(tx)
	_, err := e.ExecContext(ctx, "UPDATE coremail_aliases SET deleted_at=? WHERE id=?", now, id)
	return err
}

func (r *AliasSQLRepo) Exists(ctx context.Context, fromAddr string, tx interface{}) (bool, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_aliases WHERE from_addr=? AND deleted_at IS NULL", fromAddr).Scan(&count)
	return count > 0, err
}

func (r *AliasSQLRepo) CountByDomain(ctx context.Context, domainID uint, tx interface{}) (int64, error) {
	e := r.execer(tx)
	var count int64
	err := e.QueryRowContext(ctx, "SELECT COUNT(*) FROM coremail_aliases WHERE domain_id=? AND deleted_at IS NULL", domainID).Scan(&count)
	return count, err
}

func scanAlias(row interface {
	Scan(dest ...interface{}) error
}) (*Alias, error) {
	var a Alias
	var active int
	err := row.Scan(&a.ID, &a.DomainID, &a.TenantID, &a.FromAddr, &a.ToAddr, &active, &a.CreatedAt, &a.UpdatedAt, &a.DeletedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan alias: %w", err)
	}
	a.Active = intToBool(active)
	return &a, nil
}
