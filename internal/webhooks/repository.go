package webhooks

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type Repository struct {
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRepository(db *sql.DB) *Repository {
	d, _ := dbdialect.Detect(db)
	if d == nil {
		d = dbdialect.FromDriver("sqlite")
	}
	return &Repository{db: db, dialect: d}
}

func (r *Repository) EnsureSchema(ctx context.Context) error {
	ts := r.dialect.TimestampType()
	autoInc := r.dialect.AutoIncrement()
	if _, err := r.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS webhook_subscriptions ("+
		"id "+autoInc+", "+
		"tenant_id INTEGER NOT NULL DEFAULT 0, "+
		"scope TEXT NOT NULL, "+
		"url TEXT NOT NULL, "+
		"events TEXT NOT NULL DEFAULT '[]', "+
		"secret_encrypted TEXT NOT NULL DEFAULT '', "+
		"active INTEGER NOT NULL DEFAULT 1, "+
		"suspended INTEGER NOT NULL DEFAULT 0, "+
		"version INTEGER NOT NULL DEFAULT 1, "+
		"created_at "+ts+" NOT NULL, "+
		"updated_at "+ts+" NOT NULL)"); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS webhook_deliveries ("+
		"id "+autoInc+", "+
		"event_id TEXT NOT NULL, "+
		"subscription_id INTEGER NOT NULL, "+
		"status TEXT NOT NULL DEFAULT 'pending', "+
		"attempt_count INTEGER NOT NULL DEFAULT 0, "+
		"http_status INTEGER NOT NULL DEFAULT 0, "+
		"error TEXT NOT NULL DEFAULT '', "+
		"next_attempt_at "+ts+", "+
		"created_at "+ts+" NOT NULL, "+
		"updated_at "+ts+" NOT NULL)")
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *Repository) InsertSubscription(ctx context.Context, s *Subscription) error {
	ev, _ := json.Marshal(s.Events)
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, "INSERT INTO webhook_subscriptions (tenant_id, scope, url, events, secret_encrypted, active, suspended, version, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		s.TenantID, s.Scope, s.URL, string(ev), s.SecretEncrypted, boolInt(s.Active), boolInt(s.Suspended), 1, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	s.ID = uint(id)
	s.CreatedAt = now
	s.UpdatedAt = now
	s.Version = 1
	return nil
}

func (r *Repository) UpdateSubscription(ctx context.Context, s *Subscription) error {
	s.UpdatedAt = time.Now().UTC()
	ev, _ := json.Marshal(s.Events)
	res, err := r.db.ExecContext(ctx, "UPDATE webhook_subscriptions SET url=?, events=?, active=?, suspended=?, version=version+1, updated_at=? WHERE id=? AND version=?",
		s.URL, string(ev), boolInt(s.Active), boolInt(s.Suspended), s.UpdatedAt, s.ID, s.Version)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errStale
	}
	s.Version++
	return nil
}

var errStale = &whError{"concurrent modification detected"}

func (r *Repository) GetSubscription(ctx context.Context, id uint) (*Subscription, error) {
	var s Subscription
	var evJSON string
	var active, suspended int
	err := r.db.QueryRowContext(ctx, "SELECT id, tenant_id, scope, url, events, secret_encrypted, active, suspended, version, created_at, updated_at FROM webhook_subscriptions WHERE id=?", id).
		Scan(&s.ID, &s.TenantID, &s.Scope, &s.URL, &evJSON, &s.SecretEncrypted, &active, &suspended, &s.Version, &s.CreatedAt, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(evJSON), &s.Events)
	s.Active = active == 1
	s.Suspended = suspended == 1
	return &s, nil
}

func (r *Repository) ListSubscriptions(ctx context.Context, tenantID uint, scope string, onlyActive bool) ([]Subscription, error) {
	q := "SELECT id, tenant_id, scope, url, events, secret_encrypted, active, suspended, version, created_at, updated_at FROM webhook_subscriptions WHERE 1=1"
	var args []interface{}
	if tenantID > 0 {
		q += " AND tenant_id=?"
		args = append(args, tenantID)
	}
	if scope != "" {
		q += " AND scope=?"
		args = append(args, scope)
	}
	if onlyActive {
		q += " AND active=1 AND suspended=0"
	}
	q += " ORDER BY id DESC"
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubscriptions(rows)
}

func scanSubscriptions(rows *sql.Rows) ([]Subscription, error) {
	var out []Subscription
	for rows.Next() {
		var s Subscription
		var evJSON string
		var active, suspended int
		if err := rows.Scan(&s.ID, &s.TenantID, &s.Scope, &s.URL, &evJSON, &s.SecretEncrypted, &active, &suspended, &s.Version, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(evJSON), &s.Events)
		s.Active = active == 1
		s.Suspended = suspended == 1
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repository) InsertDelivery(ctx context.Context, d *Delivery) error {
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, "INSERT INTO webhook_deliveries (event_id, subscription_id, status, attempt_count, created_at, updated_at) VALUES (?,?,?,?,?,?)",
		d.EventID, d.SubscriptionID, d.Status, 0, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	d.ID = uint(id)
	d.CreatedAt = now
	d.UpdatedAt = now
	return nil
}

func (r *Repository) UpdateDelivery(ctx context.Context, d *Delivery) error {
	d.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, "UPDATE webhook_deliveries SET status=?, attempt_count=?, http_status=?, error=?, next_attempt_at=?, updated_at=? WHERE id=?",
		d.Status, d.AttemptCount, d.HTTPStatus, d.RedactedError, d.NextAttemptAt, d.UpdatedAt, d.ID)
	return err
}

func (r *Repository) GetDelivery(ctx context.Context, id uint) (*Delivery, error) {
	var d Delivery
	var next sql.NullTime
	err := r.db.QueryRowContext(ctx, "SELECT id, event_id, subscription_id, status, attempt_count, http_status, error, next_attempt_at, created_at, updated_at FROM webhook_deliveries WHERE id=?", id).
		Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.Status, &d.AttemptCount, &d.HTTPStatus, &d.RedactedError, &next, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if next.Valid {
		d.NextAttemptAt = &next.Time
	}
	return &d, nil
}

func (r *Repository) PendingDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, "SELECT id, event_id, subscription_id, status, attempt_count, http_status, error, next_attempt_at, created_at, updated_at FROM webhook_deliveries WHERE status IN ('pending','failed') AND (next_attempt_at IS NULL OR next_attempt_at <= ?) ORDER BY created_at ASC LIMIT ?", time.Now().UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.Status, &d.AttemptCount, &d.HTTPStatus, &d.RedactedError, &d.NextAttemptAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repository) DeliveryHistory(ctx context.Context, subscriptionID uint, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := r.db.QueryContext(ctx, "SELECT id, event_id, subscription_id, status, attempt_count, http_status, error, next_attempt_at, created_at, updated_at FROM webhook_deliveries WHERE subscription_id=? ORDER BY created_at DESC LIMIT ?", subscriptionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.EventID, &d.SubscriptionID, &d.Status, &d.AttemptCount, &d.HTTPStatus, &d.RedactedError, &d.NextAttemptAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type whError struct{ msg string }

func (e *whError) Error() string { return e.msg }
