package policy

import (
	"context"
	"database/sql"
	"strconv"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Repository persists policy state.
type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewRepository creates a policy repository.
func NewRepository(db *sql.DB) *Repository {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: dialect}
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
	q := `INSERT INTO coremail_policies (scope, target, mode, updated_at) VALUES (` + r.dialect.Placeholders(4) + `)
		 ON CONFLICT (scope, target) DO UPDATE SET mode = ` + r.dialect.Excluded("mode") + `, updated_at = ` + r.dialect.Excluded("updated_at")
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, q, scope, target, int(mode), now)
	return err
}

// DeletePolicy removes a policy record.
func (r *Repository) DeletePolicy(ctx context.Context, scope, target string) error {
	q := "DELETE FROM coremail_policies WHERE scope=" + r.dialect.Placeholder(1) + " AND target=" + r.dialect.Placeholder(2)
	_, err := r.db.ExecContext(ctx, q, scope, target)
	return err
}

func parseUintTarget(target string) (uint, bool) {
	id, err := strconv.ParseUint(target, 10, 64)
	if err != nil {
		return 0, false
	}
	return uint(id), true
}
