package trust

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Repository persists trust state to SQLite.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a trust repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// ── Lockouts ─────────────────────────────────────────────

// LoadLockouts loads all active lockouts from the database.
func (r *Repository) LoadLockouts(ctx context.Context) (map[string]time.Time, error) {
	result := make(map[string]time.Time)
	rows, err := r.db.QueryContext(ctx, "SELECT key, expires_at FROM coremail_lockouts WHERE expires_at > datetime('now')")
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
	_, err := r.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO coremail_lockouts (key, expires_at, created_at) VALUES (?, ?, datetime('now'))`,
		key, expiresAt)
	return err
}

// DeleteLockout removes a lockout record.
func (r *Repository) DeleteLockout(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM coremail_lockouts WHERE key=?", key)
	return err
}

// DeleteExpiredLockouts removes expired lockout records.
func (r *Repository) DeleteExpiredLockouts(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, "DELETE FROM coremail_lockouts WHERE expires_at <= datetime('now')")
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
	_, err := r.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO coremail_trust_scores (scope, target, score, reason, updated_at) VALUES (?, ?, ?, ?, datetime('now'))`,
		scope, target, int(score), reason)
	return err
}

func parseUintTarget(target string) (uint, bool) {
	var id uint64
	if _, err := fmt.Sscanf(target, "%d", &id); err != nil {
		return 0, false
	}
	return uint(id), true
}
