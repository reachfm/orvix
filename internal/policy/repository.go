package policy

import (
	"context"
	"database/sql"
	"strconv"
	"time"
)

// Repository persists policy state to SQLite.
type Repository struct {
	db *sql.DB
}

// NewRepository creates a policy repository.
func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// PolicySnapshot contains all persisted policy scopes.
type PolicySnapshot struct {
	DefaultMode PolicyMode
	Tenants     map[uint]TenantPolicy
	Domains     map[string]DomainPolicy
	Mailboxes   map[uint]MailboxPolicy
}

// LoadAll loads all policies from the database.
func (r *Repository) LoadAll(ctx context.Context) (*PolicySnapshot, error) {
	snap := &PolicySnapshot{
		DefaultMode: AllowAll,
		Tenants:     make(map[uint]TenantPolicy),
		Domains:     make(map[string]DomainPolicy),
		Mailboxes:   make(map[uint]MailboxPolicy),
	}

	rows, err := r.db.QueryContext(ctx, "SELECT scope, target, mode, updated_at FROM coremail_policies")
	if err != nil {
		return snap, err
	}
	defer rows.Close()

	for rows.Next() {
		var scope, target string
		var modeInt int
		var updatedAt time.Time
		if err := rows.Scan(&scope, &target, &modeInt, &updatedAt); err != nil {
			return snap, err
		}
		mode := PolicyMode(modeInt)

		switch scope {
		case "default":
			snap.DefaultMode = mode
		case "tenant":
			if id, ok := parseUintTarget(target); ok {
				snap.Tenants[id] = TenantPolicy{TenantID: id, Mode: mode, UpdatedAt: updatedAt.Unix()}
			}
		case "domain":
			snap.Domains[target] = DomainPolicy{Domain: target, Mode: mode, UpdatedAt: updatedAt.Unix()}
		case "mailbox":
			if id, ok := parseUintTarget(target); ok {
				snap.Mailboxes[id] = MailboxPolicy{MailboxID: id, Mode: mode, UpdatedAt: updatedAt.Unix()}
			}
		}
	}
	return snap, rows.Err()
}

// SavePolicy inserts or updates a policy record.
func (r *Repository) SavePolicy(ctx context.Context, scope, target string, mode PolicyMode) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO coremail_policies (scope, target, mode, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(scope, target) DO UPDATE SET mode = ?, updated_at = ?`,
		scope, target, int(mode), time.Now(), int(mode), time.Now())
	return err
}

// DeletePolicy removes a policy record.
func (r *Repository) DeletePolicy(ctx context.Context, scope, target string) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM coremail_policies WHERE scope=? AND target=?", scope, target)
	return err
}

func parseUintTarget(target string) (uint, bool) {
	id, err := strconv.ParseUint(target, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}
