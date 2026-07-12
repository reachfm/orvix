package customerdomain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// VerificationRepo persists DNS verification snapshots.
type VerificationRepo struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewVerificationRepo creates a verification snapshot repository.
func NewVerificationRepo(db *sql.DB) *VerificationRepo {
	dialect, _ := dbdialect.Detect(db)
	if dialect == nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &VerificationRepo{db: db, dialect: dialect}
}

// EnsureTable creates the customer_domain_verifications table if it does not exist.
func (r *VerificationRepo) EnsureTable(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS customer_domain_verifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			domain_id INTEGER NOT NULL,
			score INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			mx_status TEXT NOT NULL DEFAULT '',
			spf_status TEXT NOT NULL DEFAULT '',
			dkim_status TEXT NOT NULL DEFAULT '',
			dmarc_status TEXT NOT NULL DEFAULT '',
			evidence TEXT NOT NULL DEFAULT '',
			checked_at DATETIME NOT NULL,
			created_at DATETIME NOT NULL,
			CHECK (score >= 0 AND score <= 100)
		)
	`)
	if err != nil {
		return fmt.Errorf("customer_domain_verifications ensure table: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_cdv_domain ON customer_domain_verifications(domain_id)`)
	if err != nil {
		return err
	}
	return nil
}

// Save persists a verification snapshot.
func (r *VerificationRepo) Save(ctx context.Context, s *VerificationSnapshot) error {
	now := time.Now().UTC()
	s.CreatedAt = now
	s.CheckedAt = now

	evidence, _ := json.Marshal(s)
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO customer_domain_verifications
			(domain_id, score, status, mx_status, spf_status, dkim_status, dmarc_status, evidence, checked_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, s.DomainID, s.Score, s.Status, s.MXStatus, s.SPFStatus, s.DKIMStatus, s.DMARCStatus, string(evidence), s.CheckedAt, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("save verification: %w", err)
	}
	id, _ := res.LastInsertId()
	s.ID = uint(id)
	return nil
}

// GetLatest returns the most recent verification snapshot for a domain.
func (r *VerificationRepo) GetLatest(ctx context.Context, domainID uint) (*VerificationSnapshot, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, domain_id, score, status, mx_status, spf_status, dkim_status, dmarc_status, evidence, checked_at, created_at
		FROM customer_domain_verifications
		WHERE domain_id = ?
		ORDER BY created_at DESC LIMIT 1
	`, domainID)

	var s VerificationSnapshot
	var checkedAt, createdAt string
	err := row.Scan(&s.ID, &s.DomainID, &s.Score, &s.Status, &s.MXStatus, &s.SPFStatus, &s.DKIMStatus, &s.DMARCStatus, &s.Evidence, &checkedAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest verification: %w", err)
	}
	s.CheckedAt, _ = time.Parse("2006-01-02 15:04:05", checkedAt)
	s.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAt)
	if s.CheckedAt.IsZero() {
		s.CheckedAt, _ = time.Parse(time.RFC3339, checkedAt)
	}
	if s.CreatedAt.IsZero() {
		s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	}
	return &s, nil
}

// ExistsRecent returns true if a verification exists within the cooldown duration.
func (r *VerificationRepo) ExistsRecent(ctx context.Context, domainID uint, cooldown time.Duration) (bool, error) {
	cutoff := time.Now().UTC().Add(-cooldown)
	row := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM customer_domain_verifications
		WHERE domain_id = ? AND created_at > ?
	`, domainID, cutoff.Format("2006-01-02 15:04:05"))
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
