package abuse

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"
)

type RateLimitService struct {
	mu   sync.Mutex
	db   *sql.DB
}

func NewRateLimitService(db *sql.DB) *RateLimitService {
	return &RateLimitService{db: db}
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
		"SELECT COALESCE(emails_sent, 0) FROM abuse_send_counts WHERE day_key = ?", dayKey).Scan(&sentToday)
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
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO abuse_send_counts (day_key, tenant_id, emails_sent, created_at)
		VALUES (?, ?, ?, datetime('now'))
		ON CONFLICT(day_key) DO UPDATE SET emails_sent = emails_sent + ?`,
		dayKey, tenantID, count, count)
	return err
}

func (s *RateLimitService) RecordBounce(ctx context.Context, tenantID uint) error {
	now := time.Now().UTC()
	dayKey := fmt.Sprintf("bounce:%d:%s", tenantID, now.Format("2006-01-02"))
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO abuse_bounce_counts (day_key, tenant_id, bounce_count, created_at)
		VALUES (?, ?, 1, datetime('now'))
		ON CONFLICT(day_key) DO UPDATE SET bounce_count = bounce_count + 1`,
		dayKey, tenantID)
	return err
}

func (s *RateLimitService) getPlanSendLimit(ctx context.Context, tenantID uint) (int, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(send_limit_day, 500) FROM subscriptions WHERE tenant_id = ?`, tenantID)
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
		"SELECT COALESCE(SUM(emails_sent), 0) FROM abuse_send_counts WHERE tenant_id = ? AND created_at >= ?",
		tenantID, weekAgo).Scan(&totalSent)
	if err != nil {
		return 0, err
	}

	if totalSent == 0 {
		return 0, nil
	}

	var totalBounces int
	err = s.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(bounce_count), 0) FROM abuse_bounce_counts WHERE tenant_id = ? AND created_at >= ?",
		tenantID, weekAgo).Scan(&totalBounces)
	if err != nil {
		return 0, err
	}

	return float64(totalBounces) / float64(totalSent), nil
}
