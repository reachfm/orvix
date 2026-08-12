package deliverability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/platform/kernel"
)

type Service struct {
	repo   *Repository
	audit  *audit.ExtendedStore
	outbox *kernel.OutboxRepository
	clock  kernel.Clock
}

func NewService(repo *Repository, auditStore *audit.ExtendedStore, outbox *kernel.OutboxRepository, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, audit: auditStore, outbox: outbox, clock: clock}
}

// RecordDeliveryOutcome fans one delivery attempt's outcome out to
// every applicable reputation dimension in one call — the caller
// (delivery worker integration) does not need to know which
// dimensions exist. eventKey must be stable and unique per real-world
// event (e.g. "attempt:<queue_entry_id>:<attempt_number>") so a retry
// of the same recording call is a no-op, not a double-count.
func (s *Service) RecordDeliveryOutcome(ctx context.Context, eventKey string, tenantID uint, sendingDomain, recipientDomain, relayProvider string, sigType SignalType, latencyMS int64) error {
	if !sigType.IsValid() {
		return ErrInvalidSignalType
	}
	now := s.clock.Now()
	dims := []struct {
		dim Dimension
		val string
	}{
		{DimensionTenant, fmt.Sprintf("%d", tenantID)},
		{DimensionSendingDomain, sendingDomain},
		{DimensionRecipientDomain, recipientDomain},
	}
	if relayProvider != "" {
		dims = append(dims, struct {
			dim Dimension
			val string
		}{DimensionRelayProvider, relayProvider})
	}
	for _, d := range dims {
		if d.val == "" {
			continue
		}
		if _, err := s.repo.RecordSignal(ctx, &Signal{
			EventKey: eventKey, TenantID: tenantID, Dimension: d.dim, DimensionValue: d.val,
			Type: sigType, LatencyMS: latencyMS, RecordedAt: now,
		}); err != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "record deliverability signal", err)
		}
	}
	return nil
}

// RecordFromBounce classifies a delivery.DeliveryResult via the
// EXISTING delivery.ClassifyBounce (reused, not reimplemented) and
// records the corresponding signal, additionally suppressing the
// recipient on a hard (permanent, non-transient) bounce.
func (s *Service) RecordFromBounce(ctx context.Context, eventKey string, tenantID uint, sendingDomain, recipientAddress, relayProvider string, result *delivery.DeliveryResult, attemptCount int) error {
	recipientDomain := domainOf(recipientAddress)
	sigType := SignalDelivered
	if !result.Success {
		if result.TempFail {
			sigType = SignalTempFail
		} else {
			sigType = SignalPermFail
			bt := delivery.ClassifyBounce(result.StatusCode, result.StatusMsg)
			if bt != delivery.BounceUndetermined && bt != delivery.BounceTimeout && bt != delivery.BounceUnavailable {
				sigType = SignalBounce
			}
		}
	}
	if err := s.RecordDeliveryOutcome(ctx, eventKey, tenantID, sendingDomain, recipientDomain, relayProvider, sigType, result.DurationMs); err != nil {
		return err
	}
	if sigType == SignalBounce {
		bt := delivery.ClassifyBounce(result.StatusCode, result.StatusMsg)
		if bt == delivery.BounceUserUnknown {
			_, err := s.addSuppressionInternal(ctx, tenantID, recipientAddress, SuppressionHardBounce, "smtp_5xx", 0, "", nil)
			return err
		}
	}
	return nil
}

func domainOf(addr string) string {
	i := strings.LastIndex(addr, "@")
	if i < 0 {
		return ""
	}
	return strings.ToLower(addr[i+1:])
}

// Metrics returns the aggregated window for one dimension value.
func (s *Service) Metrics(ctx context.Context, dim Dimension, dimValue string, windowStart, windowEnd time.Time) (*WindowMetrics, error) {
	if !windowEnd.After(windowStart) {
		return nil, ErrInvalidWindow
	}
	// Normalized to UTC regardless of the caller's input zone: signals
	// are always recorded in UTC (kernel.Clock's contract), and SQLite
	// stores timestamps as TEXT — a local-zoned bound compared against
	// a UTC-zoned stored value is a lexicographic string comparison
	// across two different offset representations, which silently
	// excludes matching rows rather than erroring.
	m, err := s.repo.Aggregate(ctx, dim, dimValue, windowStart.UTC(), windowEnd.UTC())
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "aggregate deliverability metrics", err)
	}
	return m, nil
}

// IsSuppressed is the enforcement check for the real outbound path.
func (s *Service) IsSuppressed(ctx context.Context, tenantID uint, address string) (bool, error) {
	suppressed, _, err := s.repo.IsSuppressed(ctx, tenantID, strings.ToLower(address), s.clock.Now())
	if err != nil {
		return false, kernel.Wrap(kernel.ErrCodeInternal, "check suppression", err)
	}
	return suppressed, nil
}

// AddSuppression is the operator-facing mutation — audited, reasoned,
// tenant-scoped.
func (s *Service) AddSuppression(ctx context.Context, tenantID uint, address string, reason SuppressionReason, source string, actorID uint, notes string, expiresAt *time.Time) (*Suppression, error) {
	if address == "" {
		return nil, kernel.ValidationError(map[string]string{"address": "address is required"})
	}
	return s.addSuppressionInternal(ctx, tenantID, address, reason, source, actorID, notes, expiresAt)
}

func (s *Service) addSuppressionInternal(ctx context.Context, tenantID uint, address string, reason SuppressionReason, source string, actorID uint, notes string, expiresAt *time.Time) (*Suppression, error) {
	sup := &Suppression{
		TenantID: tenantID, Address: strings.ToLower(address), Reason: reason, Source: source,
		ActorID: actorID, Notes: notes, ExpiresAt: expiresAt, CreatedAt: s.clock.Now(),
	}
	if err := s.repo.AddSuppression(ctx, sup); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "add suppression", err)
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{
			Action: "deliverability.suppression.add", Target: "address", TenantID: tenantID,
			Result: "success", Reason: string(reason), After: sup.Address,
		})
	}
	return sup, nil
}

func (s *Service) RemoveSuppression(ctx context.Context, tenantID uint, address string, actorID uint) error {
	removed, err := s.repo.RemoveSuppression(ctx, tenantID, strings.ToLower(address))
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "remove suppression", err)
	}
	if !removed {
		return ErrSuppressionNotFound
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{
			Action: "deliverability.suppression.remove", Target: "address", TenantID: tenantID,
			Result: "success", ActorID: actorID, After: address,
		})
	}
	return nil
}

func (s *Service) ListSuppressions(ctx context.Context, tenantID uint, limit int, afterID uint) ([]Suppression, error) {
	return s.repo.ListSuppressions(ctx, tenantID, limit, afterID)
}

// IngestFeedback is the idempotent, provider-agnostic feedback
// ingestion port. A duplicate ProviderEventID (replay or provider
// redelivery) is a no-op, not an error — the caller always gets a
// definitive "processed" outcome either way, matching webhook replay
// semantics generally.
func (s *Service) IngestFeedback(ctx context.Context, ev FeedbackEvent) (processed bool, err error) {
	if ev.ProviderEventID == "" || ev.Address == "" || !ev.Type.IsValid() {
		return false, kernel.ValidationError(map[string]string{"event": "provider_event_id, address, and a valid type are required"})
	}
	recipientDomain := domainOf(ev.Address)
	inserted, err := s.repo.RecordSignal(ctx, &Signal{
		EventKey: "feedback:" + ev.ProviderEventID, TenantID: ev.TenantID,
		Dimension: DimensionRecipientDomain, DimensionValue: recipientDomain,
		Type: ev.Type, RecordedAt: s.clock.Now(),
	})
	if err != nil {
		return false, kernel.Wrap(kernel.ErrCodeInternal, "ingest feedback signal", err)
	}
	if !inserted {
		return false, nil // already processed — idempotent replay
	}
	if ev.Type == SignalComplaint {
		if _, err := s.addSuppressionInternal(ctx, ev.TenantID, ev.Address, SuppressionComplaint, ev.RawSource, 0, "", nil); err != nil {
			return true, err
		}
	}
	if s.outbox != nil {
		_ = s.outbox.Enqueue(ctx, s.repo.db, "deliverability.feedback.ingested", ev.ProviderEventID, map[string]any{
			"type": ev.Type, "source": ev.RawSource,
		}, s.clock.Now())
	}
	return true, nil
}
