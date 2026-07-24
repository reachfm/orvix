package abuse

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type RateLimitService struct {
	mu      sync.Mutex
	db      *sql.DB
	dialect *dbdialect.Info
}

func NewRateLimitService(db *sql.DB) *RateLimitService {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &RateLimitService{db: db, dialect: dialect}
}

func (s *RateLimitService) CheckSendLimit(ctx context.Context, tenantID uint, mailboxID uint) (*RateLimitBucket, error) {
	plan, err := s.getPlanSendLimit(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	resetAt := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	dayKey := fmt.Sprintf("send:%d:%s", tenantID, now.Format("2006-01-02"))

	var sentToday int
	err = s.db.QueryRowContext(ctx,
		"SELECT COALESCE(emails_sent, 0) FROM abuse_send_counts WHERE day_key = "+s.dialect.Placeholder(1), dayKey).Scan(&sentToday)
	if err == sql.ErrNoRows {
		sentToday = 0
	} else if err != nil {
		return nil, err
	}

	remaining := plan - sentToday
	if remaining < 0 {
		remaining = 0
	}

	return &RateLimitBucket{
		Key:       dayKey,
		Scope:     ScopeTenant,
		Limit:     plan,
		Remaining: remaining,
		ResetAt:   resetAt,
	}, nil
}

func (s *RateLimitService) RecordSend(ctx context.Context, tenantID uint, count int) error {
	now := time.Now().UTC()
	dayKey := fmt.Sprintf("send:%d:%s", tenantID, now.Format("2006-01-02"))
	var q string
	if s.dialect.IsPostgres() {
		q = `INSERT INTO abuse_send_counts (day_key, tenant_id, emails_sent, created_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (day_key) DO UPDATE SET emails_sent = abuse_send_counts.emails_sent + $5`
	} else {
		q = `INSERT INTO abuse_send_counts (day_key, tenant_id, emails_sent, created_at)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (day_key) DO UPDATE SET emails_sent = emails_sent + ?`
	}
	_, err := s.db.ExecContext(ctx, q, dayKey, tenantID, count, now, count)
	return err
}

func (s *RateLimitService) RecordBounce(ctx context.Context, tenantID uint) error {
	now := time.Now().UTC()
	dayKey := fmt.Sprintf("bounce:%d:%s", tenantID, now.Format("2006-01-02"))
	var q string
	if s.dialect.IsPostgres() {
		q = `INSERT INTO abuse_bounce_counts (day_key, tenant_id, bounce_count, created_at)
			VALUES ($1, $2, 1, $3)
			ON CONFLICT (day_key) DO UPDATE SET bounce_count = abuse_bounce_counts.bounce_count + 1`
	} else {
		q = `INSERT INTO abuse_bounce_counts (day_key, tenant_id, bounce_count, created_at)
			VALUES (?, ?, 1, ?)
			ON CONFLICT (day_key) DO UPDATE SET bounce_count = bounce_count + 1`
	}
	_, err := s.db.ExecContext(ctx, q, dayKey, tenantID, now)
	return err
}

func (s *RateLimitService) getPlanSendLimit(ctx context.Context, tenantID uint) (int, error) {
	// No ORDER BY/tie-break needed here (unlike internal/billing's raw
	// readers): subscriptions.tenant_id is enforced unique by a UNIQUE
	// index in the real schema (see internal/billing/setup.go), so this
	// WHERE clause can only ever match 0 or 1 rows. This package's own
	// isolated test fixture (pg_test_helper.go) goes further and makes
	// tenant_id the PRIMARY KEY directly, with no separate id column at
	// all — an ORDER BY id here would not even compile against it.
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(send_limit_day, 500) FROM subscriptions WHERE tenant_id = `+s.dialect.Placeholder(1), tenantID)
	var limit int
	err := row.Scan(&limit)
	if err == sql.ErrNoRows {
		return 500, nil
	}
	return limit, err
}

func (s *RateLimitService) CheckBounceRate(ctx context.Context, tenantID uint) (float64, error) {
	now := time.Now().UTC()
	weekAgo := now.AddDate(0, 0, -7)

	var totalSent int
	err := s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = "+s.dialect.Placeholder(1)+" AND created_at >= "+s.dialect.Placeholder(2),
		tenantID, weekAgo).Scan(&totalSent)
	if err != nil {
		return 0, err
	}

	if totalSent == 0 {
		return 0, nil
	}

	var totalBounces int
	err = s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(bounce_count), 0) FROM abuse_bounce_counts WHERE tenant_id = "+s.dialect.Placeholder(1)+" AND created_at >= "+s.dialect.Placeholder(2),
		tenantID, weekAgo).Scan(&totalBounces)
	if err != nil {
		return 0, err
	}

	return float64(totalBounces) / float64(totalSent), nil
}
