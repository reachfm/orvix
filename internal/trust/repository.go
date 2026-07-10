package trust

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Repository persists trust state to the database.
type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewRepository creates a trust repository.
func NewRepository(db *sql.DB) *Repository {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		// Fall back to SQLite for backward compatibility with tests
		// that open a raw *sql.DB without a registered driver name.
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: dialect}
}

// ── Lockouts ─────────────────────────────────────────────

// LoadLockouts loads all active lockouts from the database.
func (r *Repository) LoadLockouts(ctx context.Context) (map[string]time.Time, error) {
	result := make(map[string]time.Time)
	rows, err := r.db.QueryContext(ctx,
		"SELECT key, expires_at FROM coremail_lockouts WHERE expires_at > "+r.dialect.NowExpr())
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var expiresAt time.Time
		if err := rows.Scan(&key, &expiresAt); err != nil {
			return result, err
		}
		result[key] = expiresAt
	}
	return result, rows.Err()
}

// SaveLockout persists a lockout record.
func (r *Repository) SaveLockout(ctx context.Context, key string, expiresAt time.Time) error {
	q := r.dialect.Upsert(
		"coremail_lockouts",
		[]string{"key", "expires_at", "created_at"},
		[]string{"key"},
		[]string{"expires_at", "created_at"},
	)
	_, err := r.db.ExecContext(ctx, q, key, expiresAt, time.Now().UTC())
	return err
}

// DeleteLockout removes a lockout record.
func (r *Repository) DeleteLockout(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM coremail_lockouts WHERE key="+r.dialect.Placeholder(1), key)
	return err
}

// DeleteExpiredLockouts removes expired lockout records.
func (r *Repository) DeleteExpiredLockouts(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, "DELETE FROM coremail_lockouts WHERE expires_at <= "+r.dialect.NowExpr())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── Trust Scores ─────────────────────────────────────────

// TrustScores contains persisted trust records for every trust scope.
type TrustScores struct {
	Users     map[string]*UserTrust
	Mailboxes map[uint]*MailboxTrust
	Domains   map[string]*DomainTrust
	IPs       map[string]*IPTrust
}

// LoadTrustScores loads all trust score records.
func (r *Repository) LoadTrustScores(ctx context.Context) (*TrustScores, error) {
	result := &TrustScores{
		Users:     make(map[string]*UserTrust),
		Mailboxes: make(map[uint]*MailboxTrust),
		Domains:   make(map[string]*DomainTrust),
		IPs:       make(map[string]*IPTrust),
	}
	rows, err := r.db.QueryContext(ctx, "SELECT scope, target, score, reason, updated_at FROM coremail_trust_scores")
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var scope, target, reason string
		var score int
		var updatedAt time.Time
		if err := rows.Scan(&scope, &target, &score, &reason, &updatedAt); err != nil {
			return result, err
		}
		switch scope {
		case "user":
			result.Users[target] = &UserTrust{Username: target, Score: TrustScore(score), Reason: reason, UpdatedAt: updatedAt}
		case "mailbox":
			if id, ok := parseUintTarget(target); ok {
				result.Mailboxes[id] = &MailboxTrust{MailboxID: id, Score: TrustScore(score), Reason: reason, UpdatedAt: updatedAt}
			}
		case "domain":
			result.Domains[target] = &DomainTrust{Domain: target, Score: TrustScore(score), Reason: reason, UpdatedAt: updatedAt}
		case "ip":
			result.IPs[target] = &IPTrust{IP: target, Score: TrustScore(score), Reason: reason, UpdatedAt: updatedAt}
		}
	}
	return result, rows.Err()
}

// SaveTrustScore persists a trust score record.
func (r *Repository) SaveTrustScore(ctx context.Context, scope, target, reason string, score TrustScore) error {
	q := r.dialect.Upsert(
		"coremail_trust_scores",
		[]string{"scope", "target", "score", "reason", "updated_at"},
		[]string{"scope", "target"},
		[]string{"score", "reason", "updated_at"},
	)
	_, err := r.db.ExecContext(ctx, q, scope, target, int(score), reason, time.Now().UTC())
	return err
}

func parseUintTarget(target string) (uint, bool) {
	var id uint64
	if _, err := fmt.Sscanf(target, "%d", &id); err != nil {
		return 0, false
	}
	return uint(id), true
}
