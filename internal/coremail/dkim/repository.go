package dkim

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Repository defines DKIM configuration persistence operations.
type Repository interface {
	Create(ctx context.Context, cfg *DKIMConfig, tx interface{}) error
	GetByDomain(ctx context.Context, domain string, tx interface{}) (*DKIMConfig, error)
	Update(ctx context.Context, cfg *DKIMConfig, tx interface{}) error
	Delete(ctx context.Context, domain string, tx interface{}) error
	List(ctx context.Context, tx interface{}) ([]DKIMConfig, error)
}

var _ Repository = (*SQLRepo)(nil)

// SQLRepo implements Repository using database/sql. It is dialect-aware
// (SQLite and PostgreSQL) so it can be used by both the delivery/signing
// read path and the enterprise admin write path without a second, parallel
// implementation of the same table.
type SQLRepo struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewSQLRepo(db *sql.DB) *SQLRepo {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &SQLRepo{db: db, dialect: d}
}

func (r *SQLRepo) ph(n int) string { return r.dialect.Placeholder(n) }

func (r *SQLRepo) exec(tx interface{}) interface {
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

func (r *SQLRepo) Create(ctx context.Context, cfg *DKIMConfig, tx interface{}) error {
	now := time.Now().UTC()
	cfg.CreatedAt = now
	cfg.UpdatedAt = now
	e := r.exec(tx)
	res, err := e.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO coremail_dkim_config (domain, selector, private_key_pem, enabled, created_at, updated_at)
		VALUES (%s, %s, %s, %s, %s, %s)`,
		r.ph(1), r.ph(2), r.ph(3), r.ph(4), r.ph(5), r.ph(6)),
		cfg.Domain, cfg.Selector, cfg.PrivateKeyPEM, boolToInt(cfg.Enabled), cfg.CreatedAt, cfg.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create dkim config: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	cfg.ID = uint(id)
	return nil
}

func (r *SQLRepo) GetByDomain(ctx context.Context, domain string, tx interface{}) (*DKIMConfig, error) {
	e := r.exec(tx)
	row := e.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT id, domain, selector, private_key_pem, enabled, created_at, updated_at
		FROM coremail_dkim_config WHERE domain = %s`, r.ph(1)), domain)
	return scan(row)
}

func (r *SQLRepo) Update(ctx context.Context, cfg *DKIMConfig, tx interface{}) error {
	cfg.UpdatedAt = time.Now().UTC()
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, fmt.Sprintf(`
		UPDATE coremail_dkim_config SET selector=%s, private_key_pem=%s, enabled=%s, updated_at=%s
		WHERE domain=%s`,
		r.ph(1), r.ph(2), r.ph(3), r.ph(4), r.ph(5)),
		cfg.Selector, cfg.PrivateKeyPEM, boolToInt(cfg.Enabled), cfg.UpdatedAt, cfg.Domain)
	return err
}

func (r *SQLRepo) Delete(ctx context.Context, domain string, tx interface{}) error {
	e := r.exec(tx)
	_, err := e.ExecContext(ctx, fmt.Sprintf("DELETE FROM coremail_dkim_config WHERE domain=%s", r.ph(1)), domain)
	return err
}

func (r *SQLRepo) List(ctx context.Context, tx interface{}) ([]DKIMConfig, error) {
	e := r.exec(tx)
	rows, err := e.QueryContext(ctx, fmt.Sprintf(`
		SELECT id, domain, selector, private_key_pem, enabled, created_at, updated_at
		FROM coremail_dkim_config ORDER BY domain`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var configs []DKIMConfig
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			return nil, err
		}
		configs = append(configs, *c)
	}
	return configs, rows.Err()
}

func scan(row interface {
	Scan(dest ...interface{}) error
}) (*DKIMConfig, error) {
	var c DKIMConfig
	var enabled int
	err := row.Scan(&c.ID, &c.Domain, &c.Selector, &c.PrivateKeyPEM, &enabled, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan dkim config: %w", err)
	}
	c.Enabled = enabled == 1
	return &c, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
