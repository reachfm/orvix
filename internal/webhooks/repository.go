package webhooks

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRepository(db *sql.DB) *Repository {
	d, err := dbdialect.Detect(db)
	if err != nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

func (r *Repository) q(query string) string { return r.dialect.Rewrite(query) }

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts, autoInc := r.dialect.TimestampType(), r.dialect.AutoIncrement()
	statements := []string{
		`CREATE TABLE IF NOT EXISTS webhook_subscriptions (
			id ` + autoInc + `, tenant_id INTEGER NOT NULL DEFAULT 0, scope TEXT NOT NULL,
			url TEXT NOT NULL, events TEXT NOT NULL DEFAULT '[]', secret_encrypted TEXT NOT NULL DEFAULT '',
			active INTEGER NOT NULL DEFAULT 1, suspended INTEGER NOT NULL DEFAULT 0,
			version INTEGER NOT NULL DEFAULT 1, failure_count INTEGER NOT NULL DEFAULT 0,
			created_at ` + ts + ` NOT NULL, updated_at ` + ts + ` NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS webhook_events (
			id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, event_type TEXT NOT NULL,
			schema_version INTEGER NOT NULL, occurred_at ` + ts + ` NOT NULL,
			payload TEXT NOT NULL, created_at ` + ts + ` NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (
			id ` + autoInc + `, event_id TEXT NOT NULL, subscription_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending', attempt_count INTEGER NOT NULL DEFAULT 0,
			http_status INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
			response_excerpt TEXT NOT NULL DEFAULT '', next_attempt_at ` + ts + `,
			replay_of_delivery_id INTEGER, replay_key TEXT NOT NULL DEFAULT 'original',
			lease_token TEXT NOT NULL DEFAULT '', lease_until ` + ts + `,
			created_at ` + ts + ` NOT NULL, updated_at ` + ts + ` NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS webhook_delivery_attempts (
			id ` + autoInc + `, delivery_id INTEGER NOT NULL, attempt_number INTEGER NOT NULL,
			status TEXT NOT NULL, http_status INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '', response_excerpt TEXT NOT NULL DEFAULT '',
			started_at ` + ts + ` NOT NULL, completed_at ` + ts + `)`,
	}
	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure webhook schema: %w", err)
		}
	}
	columns := []struct{ table, name, definition string }{
		{"webhook_subscriptions", "failure_count", "INTEGER NOT NULL DEFAULT 0"},
		{"webhook_deliveries", "response_excerpt", "TEXT NOT NULL DEFAULT ''"},
		{"webhook_deliveries", "replay_of_delivery_id", "INTEGER"},
		{"webhook_deliveries", "replay_key", "TEXT NOT NULL DEFAULT ''"},
		{"webhook_deliveries", "lease_token", "TEXT NOT NULL DEFAULT ''"},
		{"webhook_deliveries", "lease_until", ts},
	}
	replayColumnAdded := false
	for _, column := range columns {
		added, err := r.ensureColumn(ctx, column.table, column.name, column.definition)
		if err != nil {
			return err
		}
		if column.name == "replay_key" {
			replayColumnAdded = added
		}
	}
	if replayColumnAdded {
		concat := "'legacy:' || id"
		if r.dialect.IsPostgres() {
			concat = "'legacy:' || id::text"
		}
		if _, err := r.db.ExecContext(ctx, "UPDATE webhook_deliveries SET replay_key="+concat); err != nil {
			return fmt.Errorf("migrate webhook replay keys: %w", err)
		}
	}
	indexes := []string{
		`CREATE INDEX IF NOT EXISTS idx_webhook_events_tenant_type ON webhook_events(tenant_id,event_type,occurred_at)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_webhook_delivery_identity ON webhook_deliveries(event_id,subscription_id,replay_key)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_delivery_due ON webhook_deliveries(status,next_attempt_at,lease_until)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_attempt_delivery ON webhook_delivery_attempts(delivery_id,attempt_number)`,
	}
	for _, statement := range indexes {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("ensure webhook index: %w", err)
		}
	}
	return nil
}

func (r *Repository) ensureColumn(ctx context.Context, table, column, definition string) (bool, error) {
	exists := false
	if r.dialect.IsPostgres() {
		err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2)`, table, column).Scan(&exists)
		if err != nil {
			return false, err
		}
	} else {
		rows, err := r.db.QueryContext(ctx, "PRAGMA table_info("+table+")")
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull, pk int
			var defaultValue any
			if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
				return false, err
			}
			if name == column {
				exists = true
			}
		}
	}
	if exists {
		return false, nil
	}
	if _, err := r.db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition); err != nil {
		return false, fmt.Errorf("add webhook column %s.%s: %w", table, column, err)
	}
	return true, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (r *Repository) InsertSubscription(ctx context.Context, s *Subscription) error {
	events, err := json.Marshal(s.Events)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	args := []any{s.TenantID, s.Scope, s.URL, string(events), s.SecretEncrypted, boolInt(s.Active), boolInt(s.Suspended), 1, 0, now, now}
	query := r.q(`INSERT INTO webhook_subscriptions (tenant_id,scope,url,events,secret_encrypted,active,suspended,version,failure_count,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if r.dialect.IsPostgres() {
		query += " RETURNING id"
		if err := r.db.QueryRowContext(ctx, query, args...).Scan(&s.ID); err != nil {
			return err
		}
	} else {
		res, err := r.db.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		s.ID = uint(id)
	}
	s.CreatedAt, s.UpdatedAt, s.Version, s.FailureCount = now, now, 1, 0
	return nil
}

func (r *Repository) UpdateSubscription(ctx context.Context, s *Subscription) error {
	s.UpdatedAt = time.Now().UTC()
	events, err := json.Marshal(s.Events)
	if err != nil {
		return err
	}
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE webhook_subscriptions SET url=?,events=?,secret_encrypted=?,active=?,suspended=?,failure_count=?,version=version+1,updated_at=? WHERE id=? AND tenant_id=? AND version=?`),
		s.URL, string(events), s.SecretEncrypted, boolInt(s.Active), boolInt(s.Suspended), s.FailureCount, s.UpdatedAt, s.ID, s.TenantID, s.Version)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errStale
	}
	s.Version++
	return nil
}

var errStale = &whError{"concurrent modification detected"}

func (r *Repository) GetSubscription(ctx context.Context, id uint) (*Subscription, error) {
	return r.GetSubscriptionForTenant(ctx, id, 0)
}

func (r *Repository) GetSubscriptionForTenant(ctx context.Context, id, tenantID uint) (*Subscription, error) {
	query := `SELECT id,tenant_id,scope,url,events,secret_encrypted,active,suspended,version,failure_count,created_at,updated_at FROM webhook_subscriptions WHERE id=?`
	args := []any{id}
	if tenantID > 0 {
		query += " AND tenant_id=?"
		args = append(args, tenantID)
	}
	var s Subscription
	var events string
	var active, suspended int
	err := r.db.QueryRowContext(ctx, r.q(query), args...).Scan(&s.ID, &s.TenantID, &s.Scope, &s.URL, &events, &s.SecretEncrypted, &active, &suspended, &s.Version, &s.FailureCount, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(events), &s.Events); err != nil {
		return nil, err
	}
	s.Active, s.Suspended = active == 1, suspended == 1
	return &s, nil
}

func (r *Repository) ListSubscriptions(ctx context.Context, tenantID uint, scope string, onlyActive bool) ([]Subscription, error) {
	query := `SELECT id,tenant_id,scope,url,events,secret_encrypted,active,suspended,version,failure_count,created_at,updated_at FROM webhook_subscriptions WHERE 1=1`
	args := []any{}
	if tenantID > 0 {
		query += " AND tenant_id=?"
		args = append(args, tenantID)
	}
	if scope != "" {
		query += " AND scope=?"
		args = append(args, scope)
	}
	if onlyActive {
		query += " AND active=1 AND suspended=0"
	}
	query += " ORDER BY id DESC"
	rows, err := r.db.QueryContext(ctx, r.q(query), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var s Subscription
		var events string
		var active, suspended int
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Scope, &s.URL, &events, &s.SecretEncrypted, &active, &suspended, &s.Version, &s.FailureCount, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(events), &s.Events); err != nil {
			return nil, err
		}
		s.Active, s.Suspended = active == 1, suspended == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) InsertEventAndFanoutTx(ctx context.Context, tx *sql.Tx, event Event) (bool, error) {
	query := `INSERT OR IGNORE INTO webhook_events (id,tenant_id,event_type,schema_version,occurred_at,payload,created_at) VALUES (?,?,?,?,?,?,?)`
	if r.dialect.IsPostgres() {
		query = `INSERT INTO webhook_events (id,tenant_id,event_type,schema_version,occurred_at,payload,created_at) VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`
	}
	res, err := tx.ExecContext(ctx, query, event.ID, event.TenantID, event.Type, event.SchemaVersion, event.OccurredAt, string(event.Payload), time.Now().UTC())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	rows, err := tx.QueryContext(ctx, r.q(`SELECT id,events FROM webhook_subscriptions WHERE tenant_id=? AND scope='tenant' AND active=1 AND suspended=0`), event.TenantID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	type match struct {
		id     uint
		events string
	}
	var matches []match
	for rows.Next() {
		var m match
		if err := rows.Scan(&m.id, &m.events); err != nil {
			return false, err
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	rows.Close()
	for _, m := range matches {
		var events []string
		if json.Unmarshal([]byte(m.events), &events) != nil || !hasEvent(events, event.Type) {
			continue
		}
		q := `INSERT OR IGNORE INTO webhook_deliveries (event_id,subscription_id,status,attempt_count,http_status,error,response_excerpt,replay_key,lease_token,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`
		if r.dialect.IsPostgres() {
			q = `INSERT INTO webhook_deliveries (event_id,subscription_id,status,attempt_count,http_status,error,response_excerpt,replay_key,lease_token,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(event_id,subscription_id,replay_key) DO NOTHING`
		}
		if _, err := tx.ExecContext(ctx, q, event.ID, m.id, "pending", 0, 0, "", "", "original", "", event.OccurredAt, event.OccurredAt); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (r *Repository) GetEvent(ctx context.Context, id string) (*Event, error) {
	var e Event
	var payload string
	err := r.db.QueryRowContext(ctx, r.q(`SELECT id,tenant_id,event_type,schema_version,occurred_at,payload FROM webhook_events WHERE id=?`), id).Scan(&e.ID, &e.TenantID, &e.Type, &e.SchemaVersion, &e.OccurredAt, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	e.Payload = json.RawMessage(payload)
	return &e, nil
}

func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (r *Repository) ClaimDeliveries(ctx context.Context, limit int, now time.Time, lease time.Duration) ([]Delivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT id FROM webhook_deliveries WHERE ((status IN ('pending','retrying') AND (next_attempt_at IS NULL OR next_attempt_at<=?)) OR (status='processing' AND lease_until<?)) ORDER BY id LIMIT ?`), now, now, limit)
	if err != nil {
		return nil, err
	}
	var ids []uint
	for rows.Next() {
		var id uint
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []Delivery
	for _, id := range ids {
		token, err := randomToken()
		if err != nil {
			return nil, err
		}
		until := now.Add(lease)
		res, err := r.db.ExecContext(ctx, r.q(`UPDATE webhook_deliveries SET status='processing',lease_token=?,lease_until=?,updated_at=? WHERE id=? AND ((status IN ('pending','retrying') AND (next_attempt_at IS NULL OR next_attempt_at<=?)) OR (status='processing' AND lease_until<?))`), token, until, now, id, now, now)
		if err != nil {
			return nil, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			continue
		}
		d, err := r.getDelivery(ctx, id, 0)
		if err != nil {
			return nil, err
		}
		d.LeaseToken = token
		d.LeaseUntil = &until
		out = append(out, *d)
	}
	return out, nil
}

// PendingDeliveries is a read-only compatibility helper used by diagnostics
// and tests. Workers must use ClaimDeliveries so ownership is fenced.
func (r *Repository) PendingDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT id FROM webhook_deliveries WHERE status IN ('pending','retrying') ORDER BY id LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	var ids []uint
	for rows.Next() {
		var id uint
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var out []Delivery
	for _, id := range ids {
		delivery, err := r.GetDelivery(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, *delivery)
	}
	return out, nil
}

func (r *Repository) InsertAttempt(ctx context.Context, d Delivery, started time.Time) (uint, error) {
	q := r.q(`INSERT INTO webhook_delivery_attempts (delivery_id,attempt_number,status,started_at) VALUES (?,?,?,?)`)
	args := []any{d.ID, d.AttemptCount + 1, "processing", started}
	if r.dialect.IsPostgres() {
		q += " RETURNING id"
		var id uint
		err := r.db.QueryRowContext(ctx, q, args...).Scan(&id)
		return id, err
	}
	res, err := r.db.ExecContext(ctx, q, args...)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	return uint(id), err
}

func (r *Repository) CompleteAttempt(ctx context.Context, id uint, status string, httpStatus int, safeErr, excerpt string, completed time.Time) error {
	_, err := r.db.ExecContext(ctx, r.q(`UPDATE webhook_delivery_attempts SET status=?,http_status=?,error=?,response_excerpt=?,completed_at=? WHERE id=?`), status, httpStatus, safeErr, excerpt, completed, id)
	return err
}

func (r *Repository) CompleteDelivery(ctx context.Context, d Delivery, status string, httpStatus int, safeErr, excerpt string, next *time.Time, now time.Time) error {
	res, err := r.db.ExecContext(ctx, r.q(`UPDATE webhook_deliveries SET status=?,attempt_count=attempt_count+1,http_status=?,error=?,response_excerpt=?,next_attempt_at=?,lease_token='',lease_until=NULL,updated_at=? WHERE id=? AND status='processing' AND lease_token=?`), status, httpStatus, safeErr, excerpt, next, now, d.ID, d.LeaseToken)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (r *Repository) RecordFailure(ctx context.Context, sub *Subscription, suspendAt int) error {
	sub.FailureCount++
	if sub.FailureCount >= suspendAt {
		sub.Suspended = true
	}
	return r.UpdateSubscription(ctx, sub)
}

func (r *Repository) ResetFailures(ctx context.Context, sub *Subscription) error {
	if sub.FailureCount == 0 {
		return nil
	}
	sub.FailureCount = 0
	return r.UpdateSubscription(ctx, sub)
}

func (r *Repository) InsertDelivery(ctx context.Context, d *Delivery) error {
	return r.insertReplay(ctx, d, "original")
}
func (r *Repository) insertReplay(ctx context.Context, d *Delivery, key string) error {
	now := time.Now().UTC()
	q := r.q(`INSERT INTO webhook_deliveries (event_id,subscription_id,status,attempt_count,http_status,error,response_excerpt,replay_of_delivery_id,replay_key,lease_token,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`)
	args := []any{d.EventID, d.SubscriptionID, d.Status, 0, 0, "", "", d.ReplayOf, key, "", now, now}
	if r.dialect.IsPostgres() {
		q += " ON CONFLICT(event_id,subscription_id,replay_key) DO NOTHING RETURNING id"
		err := r.db.QueryRowContext(ctx, q, args...).Scan(&d.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrReplayExists
		}
		if err != nil {
			return err
		}
	} else {
		q = "INSERT OR IGNORE" + strings.TrimPrefix(q, "INSERT")
		res, err := r.db.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrReplayExists
		}
		id, _ := res.LastInsertId()
		d.ID = uint(id)
	}
	d.CreatedAt, d.UpdatedAt = now, now
	return nil
}

func (r *Repository) GetDelivery(ctx context.Context, id uint) (*Delivery, error) {
	return r.getDelivery(ctx, id, 0)
}
func (r *Repository) GetDeliveryForTenant(ctx context.Context, id, tenantID uint) (*Delivery, error) {
	return r.getDelivery(ctx, id, tenantID)
}
func (r *Repository) getDelivery(ctx context.Context, id, tenantID uint) (*Delivery, error) {
	q := `SELECT d.id,d.event_id,d.subscription_id,d.status,d.attempt_count,d.http_status,d.error,d.response_excerpt,d.next_attempt_at,d.replay_of_delivery_id,d.lease_token,d.lease_until,d.created_at,d.updated_at FROM webhook_deliveries d JOIN webhook_subscriptions s ON s.id=d.subscription_id WHERE d.id=?`
	args := []any{id}
	if tenantID > 0 {
		q += " AND s.tenant_id=?"
		args = append(args, tenantID)
	}
	var d Delivery
	var next, lease sql.NullTime
	var replay sql.NullInt64
	err := r.db.QueryRowContext(ctx, r.q(q), args...).Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.Status, &d.AttemptCount, &d.HTTPStatus, &d.RedactedError, &d.ResponseExcerpt, &next, &replay, &d.LeaseToken, &lease, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if next.Valid {
		d.NextAttemptAt = &next.Time
	}
	if lease.Valid {
		d.LeaseUntil = &lease.Time
	}
	if replay.Valid {
		v := uint(replay.Int64)
		d.ReplayOf = &v
	}
	return &d, nil
}

func (r *Repository) DeliveryHistory(ctx context.Context, subscriptionID uint, limit int) ([]Delivery, error) {
	return r.DeliveryHistoryForTenant(ctx, subscriptionID, 0, limit, 0)
}
func (r *Repository) DeliveryHistoryForTenant(ctx context.Context, subscriptionID, tenantID uint, limit, offset int) ([]Delivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q := `SELECT d.id,d.event_id,d.subscription_id,d.status,d.attempt_count,d.http_status,d.error,d.response_excerpt,d.next_attempt_at,d.replay_of_delivery_id,d.lease_token,d.lease_until,d.created_at,d.updated_at FROM webhook_deliveries d JOIN webhook_subscriptions s ON s.id=d.subscription_id WHERE d.subscription_id=?`
	args := []any{subscriptionID}
	if tenantID > 0 {
		q += " AND s.tenant_id=?"
		args = append(args, tenantID)
	}
	q += " ORDER BY d.id DESC LIMIT ? OFFSET ?"
	args = append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx, r.q(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		var next, lease sql.NullTime
		var replay sql.NullInt64
		if err := rows.Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.Status, &d.AttemptCount, &d.HTTPStatus, &d.RedactedError, &d.ResponseExcerpt, &next, &replay, &d.LeaseToken, &lease, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if next.Valid {
			d.NextAttemptAt = &next.Time
		}
		if replay.Valid {
			v := uint(replay.Int64)
			d.ReplayOf = &v
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repository) Attempts(ctx context.Context, deliveryID uint) ([]Attempt, error) {
	rows, err := r.db.QueryContext(ctx, r.q(`SELECT id,delivery_id,attempt_number,status,http_status,error,response_excerpt,started_at,completed_at FROM webhook_delivery_attempts WHERE delivery_id=? ORDER BY attempt_number`), deliveryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attempt
	for rows.Next() {
		var a Attempt
		var completed sql.NullTime
		if err := rows.Scan(&a.ID, &a.DeliveryID, &a.AttemptNumber, &a.Status, &a.HTTPStatus, &a.RedactedError, &a.ResponseExcerpt, &a.StartedAt, &completed); err != nil {
			return nil, err
		}
		if completed.Valid {
			a.CompletedAt = &completed.Time
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repository) CreateManualReplay(ctx context.Context, original Delivery) (*Delivery, error) {
	d := &Delivery{EventID: original.EventID, SubscriptionID: original.SubscriptionID, Status: "pending", ReplayOf: &original.ID}
	err := r.insertReplay(ctx, d, fmt.Sprintf("manual:%d", original.ID))
	if errors.Is(err, ErrReplayExists) {
		var existingID uint
		scanErr := r.db.QueryRowContext(ctx, r.q(`SELECT id FROM webhook_deliveries WHERE event_id=? AND subscription_id=? AND replay_key=?`), original.EventID, original.SubscriptionID, fmt.Sprintf("manual:%d", original.ID)).Scan(&existingID)
		if scanErr != nil {
			return nil, scanErr
		}
		return r.GetDelivery(ctx, existingID)
	}
	return d, err
}

func (r *Repository) DeleteSubscription(ctx context.Context, id, tenantID uint) error {
	res, err := r.db.ExecContext(ctx, r.q(`DELETE FROM webhook_subscriptions WHERE id=? AND tenant_id=?`), id, tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

var (
	ErrLeaseLost    = &whError{"webhook delivery lease lost"}
	ErrReplayExists = &whError{"webhook replay already exists"}
)
