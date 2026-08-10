package billing

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

type Service struct {
	db     *sql.DB
	repo   *Repository
	audit  *audit.ExtendedStore
	outbox *kernel.OutboxRepository
	clock  kernel.Clock
}

func NewService(db *sql.DB, repo *Repository, auditStore *audit.ExtendedStore, outbox *kernel.OutboxRepository, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{db: db, repo: repo, audit: auditStore, outbox: outbox, clock: clock}
}

// ApplyAdjustment records a manual credit/debit and atomically updates
// the tenant's running balance in one transaction — the adjustment
// row and the balance update commit or roll back together, so the
// audit trail (adjustment history) and the current balance can never
// drift apart. Idempotent on idempotencyKey: a retried call with the
// same (tenantID, key) returns the ORIGINAL adjustment without
// applying a second delta.
func (s *Service) ApplyAdjustment(ctx context.Context, tenantID uint, adjType AdjustmentType, amountCents int64, currency, reason string, actorID uint, idempotencyKey string) (*Adjustment, error) {
	if amountCents <= 0 {
		return nil, ErrInvalidAmount
	}
	if reason == "" {
		return nil, ErrReasonRequired
	}
	if idempotencyKey != "" {
		if existing, err := s.repo.FindByIdempotencyKey(ctx, tenantID, idempotencyKey); err == nil && existing != nil {
			return existing, nil
		}
	}
	if existing, err := s.repo.GetBalance(ctx, nil, tenantID); err == nil && existing != nil && existing.Currency != currency {
		return nil, ErrCurrencyMismatch
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "begin adjustment transaction", err)
	}
	defer tx.Rollback()

	now := s.clock.Now()
	a := &Adjustment{TenantID: tenantID, Type: adjType, AmountCents: amountCents, Currency: currency, Reason: reason, ActorID: actorID, IdempotencyKey: idempotencyKey, CreatedAt: now}
	if err := s.repo.InsertAdjustment(ctx, tx, a); err != nil {
		if kernel.IsUniqueViolation(err) {
			// Lost a race against a concurrent identical retry — the
			// other one committed first; report ITS result, not an error.
			tx.Rollback()
			existing, ferr := s.repo.FindByIdempotencyKey(ctx, tenantID, idempotencyKey)
			if ferr == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "insert adjustment", err)
	}

	delta := amountCents
	if adjType == AdjustmentDebit {
		delta = -amountCents
	}
	if _, err := s.repo.ApplyDelta(ctx, tx, tenantID, currency, delta, now); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "apply balance delta", err)
	}

	if s.audit != nil {
		_ = s.audit.RecordTx(ctx, tx, &audit.ExtendedEntry{
			Action: "billing.adjustment.apply", TenantID: tenantID, ActorID: actorID, Result: "success",
			Reason: reason, After: fmt.Sprintf("%s %d %s", adjType, amountCents, currency),
		})
	}

	if err := tx.Commit(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "commit adjustment", err)
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, s.db, "billing.adjustment.applied", fmt.Sprintf("%d", a.ID), map[string]any{
			"tenant_id": tenantID, "type": adjType, "amount_cents": amountCents, "currency": currency,
		}, now)
	}
	return a, nil
}

func (s *Service) GetBalance(ctx context.Context, tenantID uint) (*Balance, error) {
	b, err := s.repo.GetBalance(ctx, nil, tenantID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get balance", err)
	}
	return b, nil
}
