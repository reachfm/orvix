package compliance

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Service provides compliance, quarantine, and abuse operations.
type Service struct {
	db *sql.DB
}

// NewService creates a compliance service.
func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

// EnsureSchema creates required tables.
func (s *Service) EnsureSchema(ctx context.Context) error {
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// ── Policies ─────────────────────────────────────────────

func (s *Service) ListPolicies(ctx context.Context) ([]Policy, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name, enabled, action, scope, value, created_at, updated_at FROM compliance_policies ORDER BY name")
	if err != nil { return nil, err }
	defer rows.Close()
	var policies []Policy
	for rows.Next() {
		var p Policy
		if err := rows.Scan(&p.ID, &p.Name, &p.Enabled, &p.Action, &p.Scope, &p.Value, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		policies = append(policies, p)
	}
	return policies, rows.Err()
}

func (s *Service) CreatePolicy(ctx context.Context, p *Policy) error {
	if p.Name == "" { return fmt.Errorf("policy name is required") }
	if p.Action == "" { return fmt.Errorf("policy action is required") }
	if p.Scope == "" { return fmt.Errorf("policy scope is required") }
	if p.Value == "" { return fmt.Errorf("policy value is required") }

	if p.Action != ActionAllow && p.Action != ActionQuarantine && p.Action != ActionReject {
		return fmt.Errorf("invalid action: %s", p.Action)
	}
	if p.Scope != ScopeDomain && p.Scope != ScopeSender && p.Scope != ScopeRecipient && p.Scope != ScopeSubject {
		return fmt.Errorf("invalid scope: %s", p.Scope)
	}

	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO compliance_policies (name, enabled, action, scope, value, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Enabled, string(p.Action), string(p.Scope), strings.ToLower(p.Value), p.CreatedAt, p.UpdatedAt)
	if err != nil { return fmt.Errorf("create policy: %w", err) }
	id, _ := res.LastInsertId()
	p.ID = uint(id)
	return nil
}

func (s *Service) UpdatePolicy(ctx context.Context, id uint, p *Policy) error {
	existing, err := s.GetPolicy(ctx, id)
	if err != nil { return err }
	if existing == nil { return fmt.Errorf("policy not found") }

	if p.Name != "" { existing.Name = p.Name }
	if p.Action != "" { existing.Action = p.Action }
	if p.Scope != "" { existing.Scope = p.Scope }
	if p.Value != "" { existing.Value = strings.ToLower(p.Value) }
	existing.Enabled = p.Enabled
	existing.UpdatedAt = time.Now().UTC()

	_, err = s.db.ExecContext(ctx,
		`UPDATE compliance_policies SET name=?, enabled=?, action=?, scope=?, value=?, updated_at=? WHERE id=?`,
		existing.Name, existing.Enabled, string(existing.Action), string(existing.Scope), existing.Value, existing.UpdatedAt, id)
	return err
}

func (s *Service) GetPolicy(ctx context.Context, id uint) (*Policy, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, name, enabled, action, scope, value, created_at, updated_at FROM compliance_policies WHERE id=?", id)
	var p Policy
	err := row.Scan(&p.ID, &p.Name, &p.Enabled, &p.Action, &p.Scope, &p.Value, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	return &p, nil
}

func (s *Service) DeletePolicy(ctx context.Context, id uint) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM compliance_policies WHERE id=?", id)
	return err
}

// ── Quarantine ───────────────────────────────────────────

func (s *Service) QuarantineMessage(ctx context.Context, msgID, sender, recipient, reason string) (*QuarantinedMessage, error) {
	q := &QuarantinedMessage{
		MessageID: msgID, Sender: sender, Recipient: recipient,
		Reason: reason, Status: QStatusQuarantined, CreatedAt: time.Now().UTC(),
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO coremail_quarantine (message_id, sender, recipient, reason, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		q.MessageID, q.Sender, q.Recipient, q.Reason, string(q.Status), q.CreatedAt)
	if err != nil { return nil, fmt.Errorf("quarantine: %w", err) }
	id, _ := res.LastInsertId()
	q.ID = uint(id)
	return q, nil
}

func (s *Service) ListQuarantine(ctx context.Context) ([]QuarantinedMessage, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, message_id, sender, recipient, reason, status, created_at, released_at, released_by FROM coremail_quarantine ORDER BY created_at DESC")
	if err != nil { return nil, err }
	defer rows.Close()
	var msgs []QuarantinedMessage
	for rows.Next() {
		var q QuarantinedMessage
		var releasedAt sql.NullTime
		if err := rows.Scan(&q.ID, &q.MessageID, &q.Sender, &q.Recipient, &q.Reason, &q.Status, &q.CreatedAt, &releasedAt, &q.ReleasedBy); err != nil {
			return nil, err
		}
		if releasedAt.Valid { q.ReleasedAt = &releasedAt.Time }
		msgs = append(msgs, q)
	}
	return msgs, rows.Err()
}

func (s *Service) GetQuarantine(ctx context.Context, id uint) (*QuarantinedMessage, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, message_id, sender, recipient, reason, status, created_at, released_at, released_by FROM coremail_quarantine WHERE id=?", id)
	var q QuarantinedMessage
	var releasedAt sql.NullTime
	err := row.Scan(&q.ID, &q.MessageID, &q.Sender, &q.Recipient, &q.Reason, &q.Status, &q.CreatedAt, &releasedAt, &q.ReleasedBy)
	if err == sql.ErrNoRows { return nil, nil }
	if err != nil { return nil, err }
	if releasedAt.Valid { q.ReleasedAt = &releasedAt.Time }
	return &q, nil
}

func (s *Service) ReleaseMessage(ctx context.Context, id uint, releasedBy string) (*QuarantinedMessage, error) {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx,
		`UPDATE coremail_quarantine SET status=?, released_at=?, released_by=? WHERE id=? AND status=?`,
		string(QStatusReleased), now, releasedBy, id, string(QStatusQuarantined))
	if err != nil { return nil, err }
	return s.GetQuarantine(ctx, id)
}

func (s *Service) DeleteQuarantine(ctx context.Context, id uint) error {
	_, err := s.db.ExecContext(ctx, "UPDATE coremail_quarantine SET status=? WHERE id=?", string(QStatusDeleted), id)
	return err
}

// ── Abuse Events ─────────────────────────────────────────

func (s *Service) ListAbuseEvents(ctx context.Context) ([]AbuseEvent, error) {
	// Aggregate from trust engine lockout events.
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, 'audit' as source, action, actor, result, timestamp FROM coremail_audit
		 WHERE action LIKE 'trust_%' OR action LIKE 'admin_login_failure'
		 ORDER BY timestamp DESC LIMIT 100`)
	if err != nil { return nil, err }
	defer rows.Close()
	var events []AbuseEvent
	for rows.Next() {
		var e AbuseEvent
		if err := rows.Scan(&e.ID, &e.Source, &e.EventType, &e.Actor, &e.Detail, &e.Timestamp); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
