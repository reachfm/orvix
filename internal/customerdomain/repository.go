package customerdomain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// VerificationRepo persists DNS verification snapshots and manages
// concurrent verification claims with database-backed enforcement.
type VerificationRepo struct {
	db          *sql.DB
	dialect     *dbdialect.Info
	dialectInit sync.Once
}

// NewVerificationRepo creates a verification snapshot repository.
func NewVerificationRepo(db *sql.DB) *VerificationRepo {
	return &VerificationRepo{db: db}
}

func (r *VerificationRepo) getDialect() *dbdialect.Info {
	r.dialectInit.Do(func() {
		d, err := dbdialect.Detect(r.db)
		if err != nil {
			d = dbdialect.FromDriver("sqlite")
		}
		r.dialect = d
	})
	return r.dialect
}

// DDL returns the SQLite and PostgreSQL DDL for the two tables
// managed by this repository. Used by the migration framework.
func VerificationTablesDDL(dialect *dbdialect.Info) (verifications, claims string) {
	if dialect.IsPostgres() {
		verifications = `CREATE TABLE IF NOT EXISTS customer_domain_verifications (
			id BIGSERIAL PRIMARY KEY,
			domain_id BIGINT NOT NULL,
			score INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			mx_status TEXT NOT NULL DEFAULT '',
			spf_status TEXT NOT NULL DEFAULT '',
			dkim_status TEXT NOT NULL DEFAULT '',
			dmarc_status TEXT NOT NULL DEFAULT '',
			evidence TEXT NOT NULL DEFAULT '',
			checked_at TIMESTAMP NOT NULL,
			created_at TIMESTAMP NOT NULL,
			CONSTRAINT chk_cdv_score CHECK (score >= 0 AND score <= 100)
		)`
		claims = `CREATE TABLE IF NOT EXISTS customer_domain_verification_claims (
			domain_id BIGINT PRIMARY KEY,
			claimed_until TIMESTAMP NOT NULL
		)`
	} else {
		verifications = `CREATE TABLE IF NOT EXISTS customer_domain_verifications (
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
		)`
		claims = `CREATE TABLE IF NOT EXISTS customer_domain_verification_claims (
			domain_id INTEGER PRIMARY KEY,
			claimed_until DATETIME NOT NULL
		)`
	}
	return
}

// VerificationIndexesDDL returns index DDL for the verifications table.
func VerificationIndexesDDL(dialect *dbdialect.Info) []string {
	if dialect.IsPostgres() {
		return []string{
			`CREATE INDEX IF NOT EXISTS idx_cdv_domain ON customer_domain_verifications(domain_id)`,
			`CREATE INDEX IF NOT EXISTS idx_cdv_created ON customer_domain_verifications(domain_id, created_at DESC)`,
		}
	}
	return []string{
		`CREATE INDEX IF NOT EXISTS idx_cdv_domain ON customer_domain_verifications(domain_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cdv_created ON customer_domain_verifications(domain_id, created_at)`,
	}
}

// EnsureTable creates the customer_domain_verifications and claims
// tables if they do not exist.
func (r *VerificationRepo) EnsureTable(ctx context.Context) error {
	d := r.getDialect()
	verDDL, claimDDL := VerificationTablesDDL(d)
	if _, err := r.db.ExecContext(ctx, verDDL); err != nil {
		return fmt.Errorf("customer_domain_verifications ensure table: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, claimDDL); err != nil {
		return fmt.Errorf("customer_domain_verification_claims ensure table: %w", err)
	}
	for _, idx := range VerificationIndexesDDL(d) {
		if _, err := r.db.ExecContext(ctx, idx); err != nil {
			return err
		}
	}
	return nil
}

// ── Helpers ──────────────────────────────────────────────────

// qf formats a query string with the correct placeholders.
// It replaces every ? in sql with the dialect's placeholder,
// numbered sequentially from 1.
func (r *VerificationRepo) qf(sql string) string {
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

// nowExpr returns the dialect-appropriate current-timestamp SQL
// expression for use in DML.
func (r *VerificationRepo) nowExpr() string {
	return r.getDialect().NowExpr()
}

// ── Snapshot operations ──────────────────────────────────────

// Save persists a verification snapshot.
func (r *VerificationRepo) Save(ctx context.Context, s *VerificationSnapshot) error {
	now := time.Now().UTC()
	s.CreatedAt = now
	s.CheckedAt = now

	evidence, _ := json.Marshal(s)

	if r.getDialect().IsPostgres() {
		row := r.db.QueryRowContext(ctx, r.qf(`
			INSERT INTO customer_domain_verifications
				(domain_id, score, status, mx_status, spf_status, dkim_status, dmarc_status, evidence, checked_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING id
		`), s.DomainID, s.Score, s.Status, s.MXStatus, s.SPFStatus, s.DKIMStatus, s.DMARCStatus, string(evidence), s.CheckedAt, s.CreatedAt)
		if err := row.Scan(&s.ID); err != nil {
			return fmt.Errorf("save verification: %w", err)
		}
		return nil
	}

	res, err := r.db.ExecContext(ctx, r.qf(`
		INSERT INTO customer_domain_verifications
			(domain_id, score, status, mx_status, spf_status, dkim_status, dmarc_status, evidence, checked_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`), s.DomainID, s.Score, s.Status, s.MXStatus, s.SPFStatus, s.DKIMStatus, s.DMARCStatus, string(evidence), s.CheckedAt, s.CreatedAt)
	if err != nil {
		return fmt.Errorf("save verification: %w", err)
	}
	id, _ := res.LastInsertId()
	s.ID = uint(id)
	return nil
}

// GetLatest returns the most recent verification snapshot for a domain.
func (r *VerificationRepo) GetLatest(ctx context.Context, domainID uint) (*VerificationSnapshot, error) {
	row := r.db.QueryRowContext(ctx, r.qf(`
		SELECT id, domain_id, score, status, mx_status, spf_status, dkim_status, dmarc_status, evidence, checked_at, created_at
		FROM customer_domain_verifications
		WHERE domain_id = ?
		ORDER BY created_at DESC LIMIT 1
	`), domainID)

	var s VerificationSnapshot
	err := row.Scan(&s.ID, &s.DomainID, &s.Score, &s.Status, &s.MXStatus, &s.SPFStatus, &s.DKIMStatus, &s.DMARCStatus, &s.Evidence, &s.CheckedAt, &s.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get latest verification: %w", err)
	}
	return &s, nil
}

// ExistsRecent returns true if a verification exists within the cooldown duration.
func (r *VerificationRepo) ExistsRecent(ctx context.Context, domainID uint, cooldown time.Duration) (bool, error) {
	cutoff := time.Now().UTC().Add(-cooldown)
	row := r.db.QueryRowContext(ctx, r.qf(`
		SELECT COUNT(*) FROM customer_domain_verifications
		WHERE domain_id = ? AND created_at > ?
	`), domainID, cutoff)
	var count int
	if err := row.Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

// ── Claim operations (DB-backed cooldown) ────────────────────
//
// Concurrency model (multi-instance safe):
//
//  1. TryClaim performs an atomic INSERT of a claim row inside
//     an implicit transaction (the INSERT itself is atomic).
//     The PRIMARY KEY on domain_id ensures only one claim
//     succeeds — any concurrent INSERT for the same domain_id
//     is rejected by the database, not by a Go mutex.
//
//  2. DNS inspection runs AFTER the transaction commits, so no
//     DB connection or lock is held during the 15-second DNS
//     timeout.
//
//  3. SaveAndRelease persists the snapshot and deletes the
//     claim atomically in a single transaction. If the process
//     crashes between step 2 and step 3, the stale claim
//     expires after its TTL (claimed_until) and is cleaned by
//     the next TryClaim call's stale-claim deletion.

// TryClaim attempts to claim a verification for domainID.
// Returns true if the claim was acquired (caller should proceed),
// false if cooldown is active or another instance holds the claim.
//
// The entire acquire sequence is inside a single transaction so that
// DELETE, cooldown-check, and INSERT are atomic — no other connection
// can insert a claim for the same domain_id between them.
func (r *VerificationRepo) TryClaim(ctx context.Context, domainID uint, cooldown time.Duration) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()

	// Clean stale claims.
	if _, err := tx.ExecContext(ctx, r.qf(`DELETE FROM customer_domain_verification_claims WHERE claimed_until < ?`), now); err != nil {
		return false, fmt.Errorf("clean claims: %w", err)
	}

	// Check cooldown: recent snapshot exists?
	cutoff := now.Add(-cooldown)
	var snapCount int
	if err := tx.QueryRowContext(ctx, r.qf(`
		SELECT COUNT(*) FROM customer_domain_verifications
		WHERE domain_id = ? AND created_at > ?
	`), domainID, cutoff).Scan(&snapCount); err != nil {
		return false, fmt.Errorf("check cooldown: %w", err)
	}
	if snapCount > 0 {
		return false, nil // tx.Rollback via defer
	}

	// Try to insert a claim. PRIMARY KEY conflict rejects duplicates.
	claimedUntil := now.Add(cooldown)
	if _, err := tx.ExecContext(ctx, r.qf(`
		INSERT INTO customer_domain_verification_claims (domain_id, claimed_until)
		VALUES (?, ?)
	`), domainID, claimedUntil); err != nil {
		// Duplicate key — another instance holds the claim.
		return false, nil // tx.Rollback via defer
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit claim: %w", err)
	}
	return true, nil
}

// SaveAndRelease persists a verification snapshot and removes the
// claim atomically.
func (r *VerificationRepo) SaveAndRelease(ctx context.Context, snap *VerificationSnapshot, domainID uint) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	snap.CreatedAt = now
	snap.CheckedAt = now
	evidence, _ := json.Marshal(snap)

	if r.getDialect().IsPostgres() {
		row := tx.QueryRowContext(ctx, r.qf(`
			INSERT INTO customer_domain_verifications
				(domain_id, score, status, mx_status, spf_status, dkim_status, dmarc_status, evidence, checked_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING id
		`), snap.DomainID, snap.Score, snap.Status, snap.MXStatus, snap.SPFStatus, snap.DKIMStatus, snap.DMARCStatus, string(evidence), snap.CheckedAt, snap.CreatedAt)
		if err := row.Scan(&snap.ID); err != nil {
			return fmt.Errorf("save verification: %w", err)
		}
	} else {
		res, err := tx.ExecContext(ctx, r.qf(`
			INSERT INTO customer_domain_verifications
				(domain_id, score, status, mx_status, spf_status, dkim_status, dmarc_status, evidence, checked_at, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`), snap.DomainID, snap.Score, snap.Status, snap.MXStatus, snap.SPFStatus, snap.DKIMStatus, snap.DMARCStatus, string(evidence), snap.CheckedAt, snap.CreatedAt)
		if err != nil {
			return fmt.Errorf("save verification: %w", err)
		}
		id, _ := res.LastInsertId()
		snap.ID = uint(id)
	}

	if _, err := tx.ExecContext(ctx, r.qf(`DELETE FROM customer_domain_verification_claims WHERE domain_id = ?`), domainID); err != nil {
		return fmt.Errorf("release claim: %w", err)
	}

	return tx.Commit()
}
