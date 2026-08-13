package domainlifecycle

import (
	"context"
	"database/sql"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRepository(db *sql.DB) *Repository {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: dialect}
}

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_domain_lifecycle (
		id `+autoInc+`,
		tenant_id INTEGER NOT NULL,
		name TEXT UNIQUE NOT NULL,
		state TEXT NOT NULL,
		version INTEGER NOT NULL DEFAULT 1,
		created_at `+ts+` NOT NULL,
		updated_at `+ts+` NOT NULL
	)`)
	return err
}

func (r *Repository) Create(ctx context.Context, tenantID uint, name string, now time.Time) (*Domain, error) {
	d := &Domain{TenantID: tenantID, Name: name, State: StatePending, Version: 1, CreatedAt: now, UpdatedAt: now}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO platform_domain_lifecycle (tenant_id, name, state, version, created_at, updated_at) VALUES (`+r.dialect.Placeholders(6)+`)`,
		d.TenantID, d.Name, d.State, d.Version, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	d.ID = uint(id)
	return d, nil
}

func (r *Repository) GetByID(ctx context.Context, id uint) (*Domain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, name, state, version, created_at, updated_at FROM platform_domain_lifecycle WHERE id=`+r.dialect.Placeholder(1), id)
	return scanDomain(row)
}

// TransitionIfVersion performs the state change atomically, guarding
// both the expected current state and the optimistic-concurrency
// version in a single UPDATE's WHERE clause — no read-then-write race
// window. Returns (applied=false, nil) if the row didn't match (either
// someone else already moved it, or the version is stale), letting the
// caller distinguish "conflict" from "hard error".
func (r *Repository) TransitionIfVersion(ctx context.Context, id uint, expectedState State, newState State, expectedVersion int, now time.Time) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE platform_domain_lifecycle SET state=`+r.dialect.Placeholder(1)+`, version=version+1, updated_at=`+r.dialect.Placeholder(2)+`
		 WHERE id=`+r.dialect.Placeholder(3)+` AND state=`+r.dialect.Placeholder(4)+` AND version=`+r.dialect.Placeholder(5),
		newState, now, id, expectedState, expectedVersion)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func scanDomain(row interface{ Scan(...any) error }) (*Domain, error) {
	var d Domain
	err := row.Scan(&d.ID, &d.TenantID, &d.Name, &d.State, &d.Version, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrDomainNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
