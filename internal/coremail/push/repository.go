package push

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type SubscriptionRepository interface {
	Create(ctx context.Context, sub *PushSubscription) error
	GetByEndpoint(ctx context.Context, endpoint string) (*PushSubscription, error)
	ListByMailbox(ctx context.Context, mailboxID uint, filter *PushSubscriptionFilter) ([]PushSubscription, error)
	UpdateLastSeen(ctx context.Context, id uint, t time.Time) error
	Update(ctx context.Context, sub *PushSubscription) error
	Delete(ctx context.Context, id uint) error
	Disable(ctx context.Context, id uint) error
	CleanupExpired(ctx context.Context, olderThan time.Duration) (int64, error)
}

type SubscriptionSQLRepo struct {
	db *sql.DB
}

func NewSubscriptionSQLRepo(db *sql.DB) *SubscriptionSQLRepo {
	return &SubscriptionSQLRepo{db: db}
}

func (r *SubscriptionSQLRepo) exec(tx interface{}) interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
} {
	if tx != nil {
		if t, ok := tx.(*sql.Tx); ok {
			return t
		}
	}
	return r.db
}

func (r *SubscriptionSQLRepo) Create(ctx context.Context, sub *PushSubscription) error {
	now := time.Now().UTC()
	sub.CreatedAt = now
	sub.UpdatedAt = now
	e := r.exec(nil)
	res, err := e.ExecContext(ctx,
		`INSERT INTO push_subscriptions (mailbox_id, endpoint, p256dh_key, auth_key, user_agent, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sub.MailboxID, sub.Endpoint, sub.P256DHKey, sub.AuthKey, sub.UserAgent, sub.CreatedAt, sub.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create push subscription: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get push subscription id: %w", err)
	}
	sub.ID = uint(id)
	return nil
}

func (r *SubscriptionSQLRepo) GetByEndpoint(ctx context.Context, endpoint string) (*PushSubscription, error) {
	e := r.exec(nil)
	row := e.QueryRowContext(ctx,
		`SELECT id, mailbox_id, endpoint, p256dh_key, auth_key, user_agent, disabled_at, last_seen_at, created_at, updated_at
		 FROM push_subscriptions WHERE endpoint = ?`, endpoint)
	return scanSubscription(row)
}

func (r *SubscriptionSQLRepo) ListByMailbox(ctx context.Context, mailboxID uint, filter *PushSubscriptionFilter) ([]PushSubscription, error) {
	query := `SELECT id, mailbox_id, endpoint, p256dh_key, auth_key, user_agent, disabled_at, last_seen_at, created_at, updated_at
		FROM push_subscriptions WHERE mailbox_id = ?`
	args := []interface{}{mailboxID}
	if filter != nil && filter.Disabled != nil {
		if *filter.Disabled {
			query += ` AND disabled_at IS NOT NULL`
		} else {
			query += ` AND disabled_at IS NULL`
		}
	}
	query += ` ORDER BY created_at DESC`
	if filter != nil && filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", filter.Limit)
	}
	e := r.exec(nil)
	rows, err := e.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list push subscriptions: %w", err)
	}
	defer rows.Close()
	var subs []PushSubscription
	for rows.Next() {
		sub, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, *sub)
	}
	return subs, rows.Err()
}

func (r *SubscriptionSQLRepo) UpdateLastSeen(ctx context.Context, id uint, t time.Time) error {
	e := r.exec(nil)
	_, err := e.ExecContext(ctx, `UPDATE push_subscriptions SET last_seen_at = ?, updated_at = ? WHERE id = ?`, t, time.Now().UTC(), id)
	return err
}

func (r *SubscriptionSQLRepo) Update(ctx context.Context, sub *PushSubscription) error {
	sub.UpdatedAt = time.Now().UTC()
	e := r.exec(nil)
	_, err := e.ExecContext(ctx,
		`UPDATE push_subscriptions SET mailbox_id=?, endpoint=?, p256dh_key=?, auth_key=?, user_agent=?, disabled_at=?, updated_at=? WHERE id=?`,
		sub.MailboxID, sub.Endpoint, sub.P256DHKey, sub.AuthKey, sub.UserAgent, sub.DisabledAt, sub.UpdatedAt, sub.ID)
	return err
}

func (r *SubscriptionSQLRepo) Delete(ctx context.Context, id uint) error {
	e := r.exec(nil)
	_, err := e.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE id = ?`, id)
	return err
}

func (r *SubscriptionSQLRepo) Disable(ctx context.Context, id uint) error {
	now := time.Now().UTC()
	e := r.exec(nil)
	_, err := e.ExecContext(ctx, `UPDATE push_subscriptions SET disabled_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return err
}

func (r *SubscriptionSQLRepo) CleanupExpired(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	e := r.exec(nil)
	res, err := e.ExecContext(ctx,
		`DELETE FROM push_subscriptions WHERE disabled_at IS NOT NULL AND disabled_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("cleanup expired: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func scanSubscription(scanner interface{ Scan(dest ...interface{}) error }) (*PushSubscription, error) {
	var s PushSubscription
	var disabledAt, lastSeenAt sql.NullTime
	var userAgent sql.NullString
	err := scanner.Scan(&s.ID, &s.MailboxID, &s.Endpoint, &s.P256DHKey, &s.AuthKey,
		&userAgent, &disabledAt, &lastSeenAt, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan subscription: %w", err)
	}
	if userAgent.Valid {
		s.UserAgent = userAgent.String
	}
	if disabledAt.Valid {
		s.DisabledAt = &disabledAt.Time
	}
	if lastSeenAt.Valid {
		s.LastSeenAt = &lastSeenAt.Time
	}
	return &s, nil
}
