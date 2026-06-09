package domainregistry

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Repository handles domain persistence.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a domain repository backed by the given DB.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// EnsureTable creates the domains table if it doesn't exist.
func (r *Repository) EnsureTable(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS domain_registry (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		status TEXT NOT NULL DEFAULT 'active',
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`)
	return err
}

func (r *Repository) Create(ctx context.Context, d *Domain) error {
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now
	if d.Status == "" {
		d.Status = DomainActive
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO domain_registry (name, status, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		d.Name, d.Status, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create domain: %w", err)
	}
	id, _ := res.LastInsertId()
	d.ID = uint(id)
	return nil
}

func (r *Repository) GetByID(ctx context.Context, id uint) (*Domain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, name, status, created_at, updated_at FROM domain_registry WHERE id=?`, id)
	return scanDomain(row)
}

func (r *Repository) GetByName(ctx context.Context, name string) (*Domain, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, name, status, created_at, updated_at FROM domain_registry WHERE name=?`, name)
	return scanDomain(row)
}

func (r *Repository) List(ctx context.Context) ([]Domain, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, status, created_at, updated_at FROM domain_registry ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var domains []Domain
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.ID, &d.Name, &d.Status, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		domains = append(domains, d)
	}
	return domains, rows.Err()
}

func (r *Repository) Update(ctx context.Context, d *Domain) error {
	d.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`UPDATE domain_registry SET name=?, status=?, updated_at=? WHERE id=?`,
		d.Name, d.Status, d.UpdatedAt, d.ID)
	return err
}

func (r *Repository) Delete(ctx context.Context, id uint) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM domain_registry WHERE id=?`, id)
	return err
}

func (r *Repository) Exists(ctx context.Context, name string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM domain_registry WHERE name=?`, name).Scan(&count)
	return count > 0, err
}

type scannable interface {
	Scan(dest ...interface{}) error
}

func scanDomain(row scannable) (*Domain, error) {
	var d Domain
	err := row.Scan(&d.ID, &d.Name, &d.Status, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}
