package domain

import (
	"context"
	"database/sql"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// dkimSelectorHistoryRepo persists an append-only audit trail of every
// DKIM selector transition (generate/rotate/revoke) for a domain,
// closing the "DKIM ... selector-history" gap: coremail_dkim_config
// only ever holds the current selector, so without this table a
// rotation or revocation destroys the record of what was in use
// before it.
type dkimSelectorHistoryRepo struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func newDKIMSelectorHistoryRepo(db *sql.DB) *dkimSelectorHistoryRepo {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &dkimSelectorHistoryRepo{db: db, dialect: d}
}

func (r *dkimSelectorHistoryRepo) ensureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	_, err := r.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS coremail_dkim_selector_history (
		id `+autoInc+`,
		domain TEXT NOT NULL,
		tenant_id INTEGER NOT NULL,
		selector TEXT NOT NULL,
		action TEXT NOT NULL,
		created_at `+ts+` NOT NULL
	)`)
	return err
}

type DKIMSelectorHistoryEntry struct {
	Domain    string    `json:"domain"`
	TenantID  uint      `json:"tenant_id"`
	Selector  string    `json:"selector"`
	Action    string    `json:"action"`
	CreatedAt time.Time `json:"created_at"`
}

func (r *dkimSelectorHistoryRepo) record(ctx context.Context, tx *sql.Tx, domainName string, tenantID uint, selector, action string, now time.Time) error {
	var err error
	q := `INSERT INTO coremail_dkim_selector_history (domain, tenant_id, selector, action, created_at) VALUES (` + r.dialect.Placeholders(5) + `)`
	if tx != nil {
		_, err = tx.ExecContext(ctx, q, domainName, tenantID, selector, action, now)
	} else {
		_, err = r.db.ExecContext(ctx, q, domainName, tenantID, selector, action, now)
	}
	return err
}

func (r *dkimSelectorHistoryRepo) listByDomain(ctx context.Context, domainName string) ([]DKIMSelectorHistoryEntry, error) {
	// Ordered by id, not created_at: within a fast test (or a busy
	// production burst) two history rows can land in the same
	// timestamp tick, and id (monotonic by insertion) is the only
	// reliable ordering signal in that case.
	rows, err := r.db.QueryContext(ctx,
		`SELECT domain, tenant_id, selector, action, created_at FROM coremail_dkim_selector_history WHERE domain=`+r.dialect.Placeholder(1)+` ORDER BY id DESC`,
		domainName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []DKIMSelectorHistoryEntry
	for rows.Next() {
		var e DKIMSelectorHistoryEntry
		if err := rows.Scan(&e.Domain, &e.TenantID, &e.Selector, &e.Action, &e.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}
