package audit

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// Entry is a single audit record.
type Entry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Role      string    `json:"role"`
	Action    string    `json:"action"`
	Target    string    `json:"target,omitempty"`
	Result    string    `json:"result"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"userAgent"`
	Timestamp time.Time `json:"timestamp"`
}

// Query filters for audit log.
type Query struct {
	Actor  string
	Action string
	Target string
	Result string
	Since  *time.Time
	Until  *time.Time
	Limit  int
	Offset int
}

// Store provides persistent audit log storage.
type Store struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

// NewStore creates a persistent audit store.
func NewStore(db *sql.DB) *Store {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Store{db: db, dialect: dialect}
}

// EnsureTable creates the audit table.
func (s *Store) EnsureTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS coremail_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		actor TEXT NOT NULL DEFAULT '',
		role TEXT NOT NULL DEFAULT '',
		action TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '',
		result TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		timestamp DATETIME NOT NULL
	)`)
	return err
}

// Record inserts an audit entry.
func (s *Store) Record(ctx context.Context, e *Entry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	d := s.dialect
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO coremail_audit (actor, role, action, target, result, ip, user_agent, timestamp) VALUES (`+d.Placeholder(1)+`, `+d.Placeholder(2)+`, `+d.Placeholder(3)+`, `+d.Placeholder(4)+`, `+d.Placeholder(5)+`, `+d.Placeholder(6)+`, `+d.Placeholder(7)+`, `+d.Placeholder(8)+`)`,
		e.Actor, e.Role, e.Action, e.Target, e.Result, e.IP, e.UserAgent, e.Timestamp)
	if err != nil {
		return fmt.Errorf("record audit: %w", err)
	}
	return nil
}

// Search returns audit entries matching the query.
func (s *Store) Search(ctx context.Context, q *Query) ([]Entry, int64, error) {
	if q == nil {
		q = &Query{Limit: 100}
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}

	d := s.dialect
	var where []string
	var args []interface{}
	argNum := 0

	if q.Actor != "" {
		argNum++
		where = append(where, "actor LIKE "+d.Placeholder(argNum))
		args = append(args, "%"+q.Actor+"%")
	}
	if q.Action != "" {
		argNum++
		where = append(where, "action = "+d.Placeholder(argNum))
		args = append(args, q.Action)
	}
	if q.Target != "" {
		argNum++
		where = append(where, "target LIKE "+d.Placeholder(argNum))
		args = append(args, "%"+q.Target+"%")
	}
	if q.Result != "" {
		argNum++
		where = append(where, "result = "+d.Placeholder(argNum))
		args = append(args, q.Result)
	}
	if q.Since != nil {
		argNum++
		where = append(where, "timestamp >= "+d.Placeholder(argNum))
		args = append(args, *q.Since)
	}
	if q.Until != nil {
		argNum++
		where = append(where, "timestamp <= "+d.Placeholder(argNum))
		args = append(args, *q.Until)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + buildWhere(where)
	}

	// Count.
	var total int64
	countQuery := "SELECT COUNT(*) FROM coremail_audit" + whereClause
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Data.
	query := "SELECT id, actor, role, action, target, result, ip, user_agent, timestamp FROM coremail_audit" + whereClause + " ORDER BY id DESC LIMIT " + d.Placeholder(argNum+1) + " OFFSET " + d.Placeholder(argNum+2)
	dataArgs := append(args, q.Limit, q.Offset)

	rows, err := s.db.QueryContext(ctx, query, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.Actor, &e.Role, &e.Action, &e.Target, &e.Result, &e.IP, &e.UserAgent, &e.Timestamp); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

// PurgeOlderThan removes audit entries older than the specified time.
func (s *Store) PurgeOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM coremail_audit WHERE timestamp < "+s.dialect.Placeholder(1), olderThan)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func buildWhere(conditions []string) string {
	return strings.Join(conditions, " AND ")
}
