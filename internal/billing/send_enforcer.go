package billing

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

type SendIdentity struct {
	TenantID  uint `json:"tenant_id"`
	MailboxID uint `json:"mailbox_id"`
}

type SendEnforcementResult struct {
	Allowed   bool   `json:"allowed"`
	Reason    string `json:"reason,omitempty"`
	Limit     int    `json:"limit"`
	Used      int    `json:"used"`
	Remaining int    `json:"remaining"`
}

type SendReservationResult struct {
	SendEnforcementResult
	EventID  string `json:"event_id"`
	Count    int    `json:"count"`
	Existing bool   `json:"existing"`
}

type SendEnforcer struct {
	db      *sql.DB
	dialect *dbdialect.Info
	svc     *Service
	quota   *QuotaService
}

func NewSendEnforcer(db *sql.DB, svc *Service, quota *QuotaService) *SendEnforcer {
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &SendEnforcer{db: db, dialect: dialect, svc: svc, quota: quota}
}

func (e *SendEnforcer) AllowSend(ctx context.Context, id SendIdentity, count int) *SendEnforcementResult {
	if id.TenantID == 0 {
		return &SendEnforcementResult{Allowed: false, Reason: "invalid tenant"}
	}
	if count <= 0 {
		return &SendEnforcementResult{Allowed: false, Reason: "invalid recipient count"}
	}
	sub, err := e.svc.GetSubscription(id.TenantID)
	if err != nil {
		return &SendEnforcementResult{Allowed: false, Reason: "no active subscription"}
	}
	if sub.Status == SubSuspended || sub.Status == SubCancelled || sub.Status == SubExpired {
		return &SendEnforcementResult{Allowed: false, Reason: "subscription is " + string(sub.Status)}
	}
	var sentToday int64
	if err := e.db.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(recipient_count), 0) FROM send_events WHERE tenant_id = "+e.dialect.Placeholder(1)+" AND event_type IN ('send','reservation') AND created_at >= "+e.dialect.Placeholder(2),
		id.TenantID, time.Now().UTC().Truncate(24*time.Hour)).Scan(&sentToday); err != nil {
		return &SendEnforcementResult{Allowed: false, Reason: "quota lookup failed"}
	}
	result := e.quota.CanSendEmail(id.TenantID, sentToday)
	if result != nil && !result.Allowed {
		return &SendEnforcementResult{
			Allowed:   false,
			Reason:    result.Reason,
			Limit:     result.Limit,
			Used:      result.Used,
			Remaining: result.Remaining,
		}
	}
	remaining := sub.SendLimitDay - int(sentToday)
	if remaining < count {
		if remaining < 0 {
			remaining = 0
		}
		return &SendEnforcementResult{
			Allowed:   false,
			Reason:    "send limit exceeded",
			Limit:     sub.SendLimitDay,
			Used:      int(sentToday),
			Remaining: remaining,
		}
	}
	return &SendEnforcementResult{
		Allowed:   true,
		Limit:     sub.SendLimitDay,
		Used:      int(sentToday),
		Remaining: remaining,
	}
}

func (e *SendEnforcer) ReserveSend(ctx context.Context, id SendIdentity, eventID string, count int) (*SendReservationResult, error) {
	if id.TenantID == 0 {
		return &SendReservationResult{SendEnforcementResult: SendEnforcementResult{Allowed: false, Reason: "invalid tenant"}}, nil
	}
	if eventID == "" {
		return &SendReservationResult{SendEnforcementResult: SendEnforcementResult{Allowed: false, Reason: "invalid event id"}}, nil
	}
	if count <= 0 {
		return &SendReservationResult{SendEnforcementResult: SendEnforcementResult{Allowed: false, Reason: "invalid recipient count"}}, nil
	}

	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var existingType string
	var existingCount int
	err = tx.QueryRowContext(ctx,
		"SELECT event_type, recipient_count FROM send_events WHERE tenant_id = "+e.dialect.Placeholder(1)+" AND event_id = "+e.dialect.Placeholder(2),
		id.TenantID, eventID).Scan(&existingType, &existingCount)
	if err == nil {
		if existingType == "send" || existingType == "reservation" {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &SendReservationResult{
				SendEnforcementResult: SendEnforcementResult{Allowed: true},
				EventID:               eventID,
				Count:                 existingCount,
				Existing:              true,
			}, nil
		}
	} else if err != sql.ErrNoRows {
		return nil, err
	}

	var status SubscriptionStatus
	var limit int
	query := "SELECT status, send_limit_day FROM subscriptions WHERE tenant_id = " + e.dialect.Placeholder(1)
	if e.dialect.IsPostgres() {
		query += " FOR UPDATE"
	}
	if err := tx.QueryRowContext(ctx, query, id.TenantID).Scan(&status, &limit); err != nil {
		if err == sql.ErrNoRows {
			return &SendReservationResult{SendEnforcementResult: SendEnforcementResult{Allowed: false, Reason: "no active subscription"}}, nil
		}
		return nil, err
	}
	if status == SubSuspended || status == SubCancelled || status == SubExpired {
		return &SendReservationResult{SendEnforcementResult: SendEnforcementResult{Allowed: false, Reason: "subscription is " + string(status), Limit: limit}}, nil
	}

	var used int
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(recipient_count), 0) FROM send_events WHERE tenant_id = "+e.dialect.Placeholder(1)+" AND event_type IN ('send','reservation') AND created_at >= "+e.dialect.Placeholder(2),
		id.TenantID, time.Now().UTC().Truncate(24*time.Hour)).Scan(&used); err != nil {
		return nil, err
	}
	remaining := limit - used
	if remaining < count {
		if remaining < 0 {
			remaining = 0
		}
		return &SendReservationResult{SendEnforcementResult: SendEnforcementResult{
			Allowed: false, Reason: "send limit exceeded", Limit: limit, Used: used, Remaining: remaining,
		}}, nil
	}

	now := time.Now().UTC()
	var res sql.Result
	if e.dialect.IsPostgres() {
		res, err = tx.ExecContext(ctx,
			`INSERT INTO send_events (event_id, tenant_id, mailbox_id, recipient_count, event_type, created_at)
			VALUES ($1, $2, $3, $4, 'reservation', $5) ON CONFLICT (event_id, tenant_id) DO NOTHING`,
			eventID, id.TenantID, id.MailboxID, count, now)
	} else {
		res, err = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO send_events (event_id, tenant_id, mailbox_id, recipient_count, event_type, created_at)
			VALUES (?, ?, ?, ?, 'reservation', ?)`,
			eventID, id.TenantID, id.MailboxID, count, now)
	}
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if err := tx.QueryRowContext(ctx,
			"SELECT event_type, recipient_count FROM send_events WHERE tenant_id = "+e.dialect.Placeholder(1)+" AND event_id = "+e.dialect.Placeholder(2),
			id.TenantID, eventID).Scan(&existingType, &existingCount); err != nil {
			return nil, err
		}
		if existingType == "send" || existingType == "reservation" {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &SendReservationResult{
				SendEnforcementResult: SendEnforcementResult{Allowed: true},
				EventID:               eventID,
				Count:                 existingCount,
				Existing:              true,
			}, nil
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &SendReservationResult{
		SendEnforcementResult: SendEnforcementResult{Allowed: true, Limit: limit, Used: used, Remaining: remaining},
		EventID:               eventID,
		Count:                 count,
	}, nil
}

func (e *SendEnforcer) FinalizeSendReservation(ctx context.Context, id SendIdentity, eventID string, acceptedCount int) error {
	if eventID == "" || id.TenantID == 0 {
		return nil
	}
	return e.withTx(func(tx *sql.Tx) error {
		var eventType string
		var currentCount int
		err := tx.QueryRowContext(ctx,
			"SELECT event_type, recipient_count FROM send_events WHERE tenant_id = "+e.dialect.Placeholder(1)+" AND event_id = "+e.dialect.Placeholder(2),
			id.TenantID, eventID).Scan(&eventType, &currentCount)
		if err != nil {
			return err
		}
		if eventType == "send" {
			return nil
		}
		if eventType != "reservation" {
			return fmt.Errorf("send event %q is not a reservation", eventID)
		}
		if acceptedCount <= 0 {
			_, err := tx.ExecContext(ctx,
				"DELETE FROM send_events WHERE tenant_id = "+e.dialect.Placeholder(1)+" AND event_id = "+e.dialect.Placeholder(2)+" AND event_type = 'reservation'",
				id.TenantID, eventID)
			return err
		}
		now := time.Now().UTC()
		if _, err := tx.ExecContext(ctx,
			"UPDATE send_events SET event_type = 'send', recipient_count = "+e.dialect.Placeholder(1)+", created_at = "+e.dialect.Placeholder(2)+" WHERE tenant_id = "+e.dialect.Placeholder(3)+" AND event_id = "+e.dialect.Placeholder(4)+" AND event_type = 'reservation'",
			acceptedCount, now, id.TenantID, eventID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO abuse_send_counts (day_key, tenant_id, emails_sent, created_at)
			VALUES (`+e.dialect.Placeholder(1)+`, `+e.dialect.Placeholder(2)+`, `+e.dialect.Placeholder(3)+`, `+e.dialect.Placeholder(4)+`)
			ON CONFLICT (day_key) DO UPDATE SET emails_sent = abuse_send_counts.emails_sent + `+e.dialect.Placeholder(5),
			fmt.Sprintf("send:%d:%s", id.TenantID, now.Format("2006-01-02")), id.TenantID, acceptedCount, now, acceptedCount); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO usage_records (tenant_id, period_start, period_end, emails_sent, created_at)
			VALUES (`+e.dialect.Placeholder(1)+`, `+e.dialect.Placeholder(2)+`, `+e.dialect.Placeholder(3)+`, `+e.dialect.Placeholder(4)+`, `+e.dialect.Placeholder(5)+`)
			ON CONFLICT (tenant_id, period_start) DO UPDATE SET emails_sent = usage_records.emails_sent + `+e.dialect.Placeholder(6),
			id.TenantID,
			time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC),
			time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC),
			acceptedCount, now, acceptedCount); err != nil {
			return err
		}
		return nil
	})
}

func (e *SendEnforcer) CancelSendReservation(ctx context.Context, id SendIdentity, eventID string) error {
	if eventID == "" || id.TenantID == 0 {
		return nil
	}
	_, err := e.db.ExecContext(ctx,
		"DELETE FROM send_events WHERE tenant_id = "+e.dialect.Placeholder(1)+" AND event_id = "+e.dialect.Placeholder(2)+" AND event_type = 'reservation'",
		id.TenantID, eventID)
	return err
}

func (e *SendEnforcer) RecordSend(ctx context.Context, id SendIdentity, eventID string, count int) error {
	if eventID == "" || count <= 0 || id.TenantID == 0 {
		return nil
	}
	return e.withTx(func(tx *sql.Tx) error {
		now := time.Now().UTC()
		var result sql.Result
		var err error
		if e.dialect.IsPostgres() {
			result, err = tx.ExecContext(ctx,
				`INSERT INTO send_events (event_id, tenant_id, mailbox_id, recipient_count, event_type, created_at)
				VALUES ($1, $2, $3, $4, 'send', $5) ON CONFLICT (event_id, tenant_id) DO NOTHING`,
				eventID, id.TenantID, id.MailboxID, count, now)
		} else {
			result, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO send_events (event_id, tenant_id, mailbox_id, recipient_count, event_type, created_at)
				VALUES (?, ?, ?, ?, 'send', ?)`,
				eventID, id.TenantID, id.MailboxID, count, now)
		}
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO abuse_send_counts (day_key, tenant_id, emails_sent, created_at)
			VALUES (`+e.dialect.Placeholder(1)+`, `+e.dialect.Placeholder(2)+`, `+e.dialect.Placeholder(3)+`, `+e.dialect.Placeholder(4)+`)
			ON CONFLICT (day_key) DO UPDATE SET emails_sent = abuse_send_counts.emails_sent + `+e.dialect.Placeholder(5),
			fmt.Sprintf("send:%d:%s", id.TenantID, now.Format("2006-01-02")), id.TenantID, count, now, count); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO usage_records (tenant_id, period_start, period_end, emails_sent, created_at)
			VALUES (`+e.dialect.Placeholder(1)+`, `+e.dialect.Placeholder(2)+`, `+e.dialect.Placeholder(3)+`, `+e.dialect.Placeholder(4)+`, `+e.dialect.Placeholder(5)+`)
			ON CONFLICT (tenant_id, period_start) DO UPDATE SET emails_sent = usage_records.emails_sent + `+e.dialect.Placeholder(6),
			id.TenantID,
			time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC),
			time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC),
			count, now, count); err != nil {
			return err
		}
		return nil
	})
}

func (e *SendEnforcer) RecordBounce(ctx context.Context, id SendIdentity, eventID string) error {
	if eventID == "" || id.TenantID == 0 {
		return nil
	}
	return e.withTx(func(tx *sql.Tx) error {
		now := time.Now().UTC()
		dayKey := fmt.Sprintf("bounce:%d:%s", id.TenantID, now.Format("2006-01-02"))
		var result sql.Result
		var err error
		if e.dialect.IsPostgres() {
			result, err = tx.ExecContext(ctx,
				`INSERT INTO send_events (event_id, tenant_id, mailbox_id, event_type, created_at)
				VALUES ($1, $2, $3, 'bounce', $4) ON CONFLICT (event_id, tenant_id) DO NOTHING`,
				eventID, id.TenantID, id.MailboxID, now)
		} else {
			result, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO send_events (event_id, tenant_id, mailbox_id, event_type, created_at)
				VALUES (?, ?, ?, 'bounce', ?)`,
				eventID, id.TenantID, id.MailboxID, now)
		}
		if err != nil {
			return err
		}
		if n, _ := result.RowsAffected(); n == 0 {
			return nil
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO abuse_bounce_counts (day_key, tenant_id, bounce_count, created_at)
			VALUES (`+e.dialect.Placeholder(1)+`, `+e.dialect.Placeholder(2)+`, 1, `+e.dialect.Placeholder(3)+`)
			ON CONFLICT (day_key) DO UPDATE SET bounce_count = abuse_bounce_counts.bounce_count + 1`,
			dayKey, id.TenantID, now)
		return err
	})
}

func (e *SendEnforcer) withTx(fn func(tx *sql.Tx) error) error {
	tx, err := e.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
