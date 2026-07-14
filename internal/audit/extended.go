package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/metrics"
)

type ExtendedEntry struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	ActorID   uint      `json:"actor_id"`
	ActorRole string    `json:"actor_role"`
	TenantID  uint      `json:"tenant_id"`
	Action    string    `json:"action"`
	Target    string    `json:"target,omitempty"`
	TargetID  uint      `json:"target_id,omitempty"`
	Result    string    `json:"result"`
	Reason    string    `json:"reason,omitempty"`
	Before    string    `json:"before,omitempty"`
	After     string    `json:"after,omitempty"`
	RequestID string    `json:"request_id,omitempty"`
	IP        string    `json:"ip"`
	UserAgent string    `json:"user_agent"`
	Timestamp time.Time `json:"timestamp"`
}

type ExtendedQuery struct {
	TenantID *uint
	ActorID  *uint
	Action   string
	Target   string
	TargetID *uint
	Result   string
	Since    *time.Time
	Until    *time.Time
	Limit    int
	Offset   int
}

type ExtendedStore struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

type auditExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func NewExtendedStore(db *sql.DB) *ExtendedStore {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &ExtendedStore{db: db, dialect: dialect}
}

func (s *ExtendedStore) EnsureTable(ctx context.Context) error {
	d := s.dialect
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`CREATE TABLE IF NOT EXISTS orvix_audit (
		id %s,
		actor TEXT NOT NULL DEFAULT '',
		actor_id INTEGER NOT NULL DEFAULT 0,
		actor_role TEXT NOT NULL DEFAULT '',
		tenant_id INTEGER NOT NULL DEFAULT 0,
		action TEXT NOT NULL DEFAULT '',
		target TEXT NOT NULL DEFAULT '',
		target_id INTEGER NOT NULL DEFAULT 0,
		result TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		before TEXT NOT NULL DEFAULT '',
		after TEXT NOT NULL DEFAULT '',
		request_id TEXT NOT NULL DEFAULT '',
		ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT '',
		timestamp %s NOT NULL DEFAULT %s
	)`, d.AutoIncrement(), d.TimestampType(), d.NowExpr()))
	return err
}

func (s *ExtendedStore) Record(ctx context.Context, e *ExtendedEntry) error {
	return s.recordWith(ctx, s.db, e)
}

// RecordTx persists an audit entry in the caller's mutation transaction.
// The business mutation and its audit record therefore commit or roll back as
// one unit; callers must treat any audit failure as a transaction failure.
func (s *ExtendedStore) RecordTx(ctx context.Context, tx *sql.Tx, e *ExtendedEntry) error {
	if tx == nil {
		return fmt.Errorf("record extended audit: transaction is required")
	}
	return s.recordWith(ctx, tx, e)
}

func (s *ExtendedStore) recordWith(ctx context.Context, exec auditExecer, e *ExtendedEntry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	sanitizeEntry(e)
	d := s.dialect
	before, _ := json.Marshal(e.Before)
	after, _ := json.Marshal(e.After)
	_, err := exec.ExecContext(ctx,
		fmt.Sprintf(`INSERT INTO orvix_audit
			(actor, actor_id, actor_role, tenant_id, action, target, target_id,
			 result, reason, before, after, request_id, ip, user_agent, timestamp)
		 VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s)`,
			d.Placeholder(1), d.Placeholder(2), d.Placeholder(3), d.Placeholder(4),
			d.Placeholder(5), d.Placeholder(6), d.Placeholder(7), d.Placeholder(8),
			d.Placeholder(9), d.Placeholder(10), d.Placeholder(11), d.Placeholder(12),
			d.Placeholder(13), d.Placeholder(14), d.Placeholder(15),
		),
		e.Actor, e.ActorID, e.ActorRole, e.TenantID,
		e.Action, e.Target, e.TargetID,
		e.Result, e.Reason, string(before), string(after),
		e.RequestID, e.IP, e.UserAgent, e.Timestamp,
	)
	if err != nil {
		metrics.AuditWriteFailures.Inc()
		return fmt.Errorf("record extended audit: %w", err)
	}
	return nil
}

func (s *ExtendedStore) Search(ctx context.Context, q *ExtendedQuery) ([]ExtendedEntry, int64, error) {
	if q == nil {
		q = &ExtendedQuery{Limit: 100}
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}

	d := s.dialect
	var where []string
	var args []interface{}
	argNum := 0

	if q.TenantID != nil {
		argNum++
		where = append(where, fmt.Sprintf("tenant_id = %s", d.Placeholder(argNum)))
		args = append(args, *q.TenantID)
	}
	if q.ActorID != nil {
		argNum++
		where = append(where, fmt.Sprintf("actor_id = %s", d.Placeholder(argNum)))
		args = append(args, *q.ActorID)
	}
	if q.Action != "" {
		argNum++
		where = append(where, fmt.Sprintf("action = %s", d.Placeholder(argNum)))
		args = append(args, q.Action)
	}
	if q.Target != "" {
		argNum++
		where = append(where, fmt.Sprintf("target LIKE %s", d.Placeholder(argNum)))
		args = append(args, "%"+q.Target+"%")
	}
	if q.TargetID != nil {
		argNum++
		where = append(where, fmt.Sprintf("target_id = %s", d.Placeholder(argNum)))
		args = append(args, *q.TargetID)
	}
	if q.Result != "" {
		argNum++
		where = append(where, fmt.Sprintf("result = %s", d.Placeholder(argNum)))
		args = append(args, q.Result)
	}
	if q.Since != nil {
		argNum++
		where = append(where, fmt.Sprintf("timestamp >= %s", d.Placeholder(argNum)))
		args = append(args, *q.Since)
	}
	if q.Until != nil {
		argNum++
		where = append(where, fmt.Sprintf("timestamp <= %s", d.Placeholder(argNum)))
		args = append(args, *q.Until)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = " WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM orvix_audit"+whereClause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := fmt.Sprintf(
		"SELECT id, actor, actor_id, actor_role, tenant_id, action, target, target_id, result, reason, before, after, request_id, ip, user_agent, timestamp FROM orvix_audit%s ORDER BY id DESC LIMIT %s OFFSET %s",
		whereClause, d.Placeholder(argNum+1), d.Placeholder(argNum+2),
	)
	dataArgs := append(args, q.Limit, q.Offset)

	rows, err := s.db.QueryContext(ctx, query, dataArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var entries []ExtendedEntry
	for rows.Next() {
		var e ExtendedEntry
		if err := rows.Scan(&e.ID, &e.Actor, &e.ActorID, &e.ActorRole, &e.TenantID,
			&e.Action, &e.Target, &e.TargetID, &e.Result, &e.Reason,
			&e.Before, &e.After, &e.RequestID, &e.IP, &e.UserAgent, &e.Timestamp); err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
	}
	return entries, total, rows.Err()
}

func sanitizeEntry(e *ExtendedEntry) {
	e.After = sanitizeJSON(e.After, sensitiveFields)
	e.Before = sanitizeJSON(e.Before, sensitiveFields)
}

var sensitiveFields = map[string]bool{
	"password": true, "password_hash": true, "passwordhash": true,
	"secret": true, "token": true, "key": true, "private_key": true,
	"privatekey": true, "credential": true, "session_token": true,
	"sessiontoken": true, "authorization": true, "cookie": true,
	"hash": true,
}

func sanitizeJSON(input string, sensitiveFields map[string]bool) string {
	if input == "" {
		return ""
	}
	var val interface{}
	if err := json.Unmarshal([]byte(input), &val); err != nil {
		return `"[REDACTED: invalid audit metadata]"`
	}
	redacted := recursiveRedact(val, sensitiveFields)
	out, _ := json.Marshal(redacted)
	return string(out)
}

func recursiveRedact(val interface{}, sensitiveFields map[string]bool) interface{} {
	switch v := val.(type) {
	case map[string]interface{}:
		for k, sub := range v {
			if isSensitive(k, sensitiveFields) {
				v[k] = "[REDACTED]"
			} else {
				v[k] = recursiveRedact(sub, sensitiveFields)
			}
		}
		return v
	case []interface{}:
		for i, sub := range v {
			v[i] = recursiveRedact(sub, sensitiveFields)
		}
		return v
	default:
		return val
	}
}

func isSensitive(key string, sensitiveFields map[string]bool) bool {
	lower := strings.ToLower(key)
	for sf := range sensitiveFields {
		if strings.Contains(lower, sf) {
			return true
		}
	}
	return false
}

func ActorEntry(c fiber.Ctx, action, target, result string, targetID uint) *ExtendedEntry {
	userID, _ := c.Locals("user_id").(uint)
	role, _ := c.Locals("role").(string)
	if role == "" {
		r, _ := c.Locals("role").(interface{})
		role = fmt.Sprintf("%v", r)
	}
	tenantID, _ := c.Locals("tenant_id").(uint)
	return &ExtendedEntry{
		Actor:     fmt.Sprintf("user:%d", userID),
		ActorID:   userID,
		ActorRole: role,
		TenantID:  tenantID,
		Action:    action,
		Target:    target,
		TargetID:  targetID,
		Result:    result,
		IP:        c.IP(),
		UserAgent: c.Get("User-Agent"),
		Timestamp: time.Now().UTC(),
	}
}
