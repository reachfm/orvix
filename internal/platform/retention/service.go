package retention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// PurgeTarget is the port to the actual data store a purge operates
// over (mailbox messages, archived attachments, etc.) — deliberately
// abstract so this package never hardcodes a destructive DELETE
// against production mail tables itself; a concrete adapter over
// internal/coremail/storage is a follow-up wiring step, same pattern
// as internal/platform/dr's BackupOperator port.
type PurgeTarget interface {
	// CountEligible returns how many items in scope are eligible for
	// purge under the resolved policy (older than retention+recovery
	// windows) — used for the dry-run PurgePlan.
	CountEligible(ctx context.Context, scopeKind string, scopeID uint, olderThan time.Time) (int, error)
	// PurgeBatch deletes up to batchSize eligible items and returns how
	// many were actually removed — called repeatedly until it returns
	// 0, bounding each individual database operation.
	PurgeBatch(ctx context.Context, scopeKind string, scopeID uint, olderThan time.Time, batchSize int) (int, error)
}

type Service struct {
	repo   *Repository
	target PurgeTarget
	audit  *audit.ExtendedStore
	outbox *kernel.OutboxRepository
	clock  kernel.Clock
}

func NewService(repo *Repository, target PurgeTarget, auditStore *audit.ExtendedStore, outbox *kernel.OutboxRepository, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, target: target, audit: auditStore, outbox: outbox, clock: clock}
}

func (s *Service) CreatePolicy(ctx context.Context, p Policy) (*Policy, error) {
	if p.Level == "" || levelRank[p.Level] == 0 && p.Level != LevelPlatform {
		return nil, ErrInvalidPolicy
	}
	now := s.clock.Now()
	p.CreatedAt, p.UpdatedAt = now, now
	if err := s.repo.CreatePolicy(ctx, &p); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create retention policy", err)
	}
	return &p, nil
}

// ResolvePolicy returns the single most-specific applicable policy —
// deterministic: mailbox beats domain beats tenant beats platform.
// Ties at the same level (shouldn't happen given unique scoping, but
// handled defensively) resolve to the most recently updated policy.
func (s *Service) ResolvePolicy(ctx context.Context, tenantID, domainID, mailboxID uint, category string) (*Policy, error) {
	candidates, err := s.repo.ListApplicable(ctx, tenantID, domainID, mailboxID, category)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list applicable retention policies", err)
	}
	var best *Policy
	for i := range candidates {
		c := &candidates[i]
		if best == nil || levelRank[c.Level] > levelRank[best.Level] ||
			(levelRank[c.Level] == levelRank[best.Level] && c.UpdatedAt.After(best.UpdatedAt)) {
			best = c
		}
	}
	return best, nil
}

// ── Legal holds ──────────────────────────────────────────────────

func (s *Service) PlaceLegalHold(ctx context.Context, scopeKind string, scopeID uint, caseRef, reason string, actorID uint, endsAt *time.Time) (*LegalHold, error) {
	if reason == "" {
		return nil, kernel.ValidationError(map[string]string{"reason": "a reason is required for a legal hold"})
	}
	now := s.clock.Now()
	h := &LegalHold{ScopeKind: scopeKind, ScopeID: scopeID, CaseRef: caseRef, Reason: reason, ActorID: actorID, StartedAt: now, EndsAt: endsAt, CreatedAt: now}
	if err := s.repo.CreateHold(ctx, h); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "place legal hold", err)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "retention.legal_hold.place", ActorID: actorID, Result: "success", Reason: reason, Target: fmt.Sprintf("%s:%d", scopeKind, scopeID)})
	}
	return h, nil
}

func (s *Service) ReleaseLegalHold(ctx context.Context, id uint, actorID uint) error {
	ok, err := s.repo.ReleaseHold(ctx, id)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "release legal hold", err)
	}
	if !ok {
		return ErrHoldNotFound
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "retention.legal_hold.release", ActorID: actorID, Result: "success", TargetID: id})
	}
	return nil
}

// IsHeld reports whether scopeKind/scopeID currently has an active
// legal hold — the single check both PlanPurge and ExecutePurge run
// before touching any data.
func (s *Service) IsHeld(ctx context.Context, scopeKind string, scopeID uint) (bool, error) {
	holds, err := s.repo.ActiveHoldsForScope(ctx, scopeKind, scopeID, s.clock.Now())
	if err != nil {
		return false, kernel.Wrap(kernel.ErrCodeInternal, "check legal hold", err)
	}
	return len(holds) > 0, nil
}

// ── Purge planning and execution ─────────────────────────────────

func (s *Service) PlanPurge(ctx context.Context, scopeKind string, scopeID uint, olderThan time.Time) (*PurgePlan, error) {
	held, err := s.IsHeld(ctx, scopeKind, scopeID)
	if err != nil {
		return nil, err
	}
	plan := &PurgePlan{ScopeKind: scopeKind, ScopeID: scopeID, GeneratedAt: s.clock.Now()}
	if held {
		// Still report the count that WOULD be eligible if not for the
		// hold, so an operator can see the hold is the only thing
		// blocking cleanup — never silently zero.
		if s.target != nil {
			n, _ := s.target.CountEligible(ctx, scopeKind, scopeID, olderThan)
			plan.HeldCount = n
		}
		return plan, nil
	}
	if s.target != nil {
		n, err := s.target.CountEligible(ctx, scopeKind, scopeID, olderThan)
		if err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "count eligible for purge", err)
		}
		plan.EligibleCount = n
	}
	return plan, nil
}

// ExecutePurge requires the exact typed confirmation phrase, rechecks
// the legal hold at execution time (a hold placed between PlanPurge
// and ExecutePurge must still block — this is the concurrency
// guarantee), and is idempotent on idempotencyKey: a retried call
// with the same key returns the original result without purging
// again.
func (s *Service) ExecutePurge(ctx context.Context, scopeKind string, scopeID uint, olderThan time.Time, confirmation, idempotencyKey string, actorID uint) (int, error) {
	if confirmation != PurgeConfirmationPhrase {
		return 0, ErrConfirmationRequired
	}
	if idempotencyKey != "" {
		if count, found, err := s.repo.GetPurgeExecution(ctx, idempotencyKey); err == nil && found {
			return count, nil
		}
	}
	held, err := s.IsHeld(ctx, scopeKind, scopeID)
	if err != nil {
		return 0, err
	}
	if held {
		return 0, ErrLegalHoldActive
	}

	total := 0
	if s.target != nil {
		for {
			// Re-check the hold every batch, not just once at the top:
			// a hold placed mid-execution (a genuinely concurrent
			// legal-hold-vs-purge race) must stop further batches
			// immediately, not just block the NEXT purge attempt.
			held, err := s.IsHeld(ctx, scopeKind, scopeID)
			if err != nil {
				return total, err
			}
			if held {
				break
			}
			n, err := s.target.PurgeBatch(ctx, scopeKind, scopeID, olderThan, 1000)
			if err != nil {
				return total, kernel.Wrap(kernel.ErrCodeInternal, "purge batch", err)
			}
			total += n
			if n == 0 {
				break
			}
		}
	}

	now := s.clock.Now()
	if idempotencyKey != "" {
		_ = s.repo.RecordPurgeExecution(ctx, idempotencyKey, scopeKind, scopeID, total, now)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "retention.purge.execute", ActorID: actorID, Result: "success", Target: fmt.Sprintf("%s:%d", scopeKind, scopeID), After: fmt.Sprintf("purged=%d", total)})
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, s.repo.db, "retention.purge.completed", fmt.Sprintf("%s:%d", scopeKind, scopeID), map[string]any{"purged": total}, now)
	}
	_ = s.RecordCustody(ctx, "purge", scopeKind, scopeID, actorID, total, nil)
	return total, nil
}

// ListActiveHolds returns the active legal holds for a scope.
func (s *Service) ListActiveHolds(ctx context.Context, scopeKind string, scopeID uint) ([]LegalHold, error) {
	out, err := s.repo.ActiveHoldsForScope(ctx, scopeKind, scopeID, s.clock.Now())
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list active legal holds", err)
	}
	return out, nil
}

// ── Chain of custody ─────────────────────────────────────────────

// ListCustodyEvents returns chain-of-custody evidence records for a
// scope, paginated, newest first.
func (s *Service) ListCustodyEvents(ctx context.Context, scopeKind string, scopeID uint, limit, offset int) ([]ChainOfCustodyEvent, error) {
	out, err := s.repo.ListCustodyEvents(ctx, scopeKind, scopeID, limit, offset)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list chain of custody events", err)
	}
	return out, nil
}

func (s *Service) RecordCustody(ctx context.Context, operation, scopeKind string, scopeID uint, actorID uint, recordCount int, content []byte) error {
	hash := ""
	if content != nil {
		sum := sha256.Sum256(content)
		hash = hex.EncodeToString(sum[:])
	}
	e := &ChainOfCustodyEvent{Operation: operation, ScopeKind: scopeKind, ScopeID: scopeID, ActorID: actorID, ContentHash: hash, RecordCount: recordCount, CreatedAt: s.clock.Now()}
	if err := s.repo.RecordCustody(ctx, e); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "record chain of custody", err)
	}
	return nil
}
