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
//
// Category mapping is evidence-driven:
//   - the worker's own suppression rejection ("recipient is
//     suppressed") records SignalSuppressed;
//   - the worker's policy rejections (sender/domain/size/recipient
//     policy reasons, which carry "limit" / "exceeds maximum")
//     record SignalPolicyReject;
//   - permanent non-bounce failures record SignalPermFail.
func (s *Service) RecordFromBounce(ctx context.Context, eventKey string, tenantID uint, sendingDomain, recipientAddress, relayProvider string, result *delivery.DeliveryResult, attemptCount int) error {
	recipientDomain := domainOf(recipientAddress)
	sigType := SignalDelivered
	if !result.Success {
		if result.TempFail {
			sigType = SignalTempFail
		} else {
			msg := strings.ToLower(result.StatusMsg)
			switch {
			case strings.Contains(msg, "recipient is suppressed"):
				sigType = SignalSuppressed
			case strings.Contains(msg, "policy") || strings.Contains(msg, "exceeds maximum") || strings.Contains(msg, " limit "):
				sigType = SignalPolicyReject
			default:
				sigType = SignalPermFail
				bt := delivery.ClassifyBounce(result.StatusCode, result.StatusMsg)
				if bt != delivery.BounceUndetermined && bt != delivery.BounceTimeout && bt != delivery.BounceUnavailable {
					sigType = SignalBounce
				}
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

// ── Events and aggregation (platform surface) ──────────────────────

// maxWindowSpan bounds an aggregation/event window (90 days).
const maxWindowSpan = 90 * 24 * time.Hour

// normalizeWindow validates and UTC-normalizes a query window,
// rejecting inverted windows and abusive spans.
func normalizeWindow(start, end time.Time) (time.Time, time.Time, error) {
	if end.IsZero() || start.IsZero() || !end.After(start) {
		return time.Time{}, time.Time{}, ErrInvalidWindow
	}
	start, end = start.UTC(), end.UTC()
	if end.Sub(start) > maxWindowSpan {
		return time.Time{}, time.Time{}, kernel.ValidationError(map[string]string{"window": "time window must not exceed 90 days"})
	}
	return start, end, nil
}

// SafeEvent is the platform-facing event projection — EventKey (the
// internal idempotency key) is intentionally excluded; recipient
// evidence is the domain-level dimension value only.
type SafeEvent struct {
	ID             uint       `json:"id"`
	TenantID       uint       `json:"tenant_id"`
	Dimension      Dimension  `json:"dimension"`
	DimensionValue string     `json:"dimension_value"`
	Type           SignalType `json:"type"`
	Category       Category   `json:"category"`
	LatencyMS      int64      `json:"latency_ms,omitempty"`
	RecordedAt     time.Time  `json:"recorded_at"`
}

// ListEvents returns the tenant's delivery events with filters,
// bounded pagination, and deterministic ordering (newest first).
func (s *Service) ListEvents(ctx context.Context, f EventFilter) ([]SafeEvent, int64, error) {
	if f.TenantID == 0 {
		return nil, 0, kernel.ValidationError(map[string]string{"tenant_id": "explicit tenant required"})
	}
	if f.Start != nil && f.End != nil {
		st, en, err := normalizeWindow(*f.Start, *f.End)
		if err != nil {
			return nil, 0, err
		}
		f.Start, f.End = &st, &en
	}
	signals, total, err := s.repo.ListEvents(ctx, f)
	if err != nil {
		return nil, 0, kernel.Wrap(kernel.ErrCodeInternal, "list deliverability events", err)
	}
	out := make([]SafeEvent, 0, len(signals))
	for _, sg := range signals {
		out = append(out, SafeEvent{
			ID: sg.ID, TenantID: sg.TenantID, Dimension: sg.Dimension,
			DimensionValue: sg.DimensionValue, Type: sg.Type, Category: categoryOf(sg.Type),
			LatencyMS: sg.LatencyMS, RecordedAt: sg.RecordedAt,
		})
	}
	return out, total, nil
}

// GetEvent returns one event, tenant-scoped.
func (s *Service) GetEvent(ctx context.Context, id, tenantID uint) (*SafeEvent, error) {
	sg, err := s.repo.GetSignal(ctx, id, tenantID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get deliverability event", err)
	}
	if sg == nil {
		return nil, kernel.NotFound("deliverability event")
	}
	return &SafeEvent{
		ID: sg.ID, TenantID: sg.TenantID, Dimension: sg.Dimension,
		DimensionValue: sg.DimensionValue, Type: sg.Type, Category: categoryOf(sg.Type),
		LatencyMS: sg.LatencyMS, RecordedAt: sg.RecordedAt,
	}, nil
}

// TenantMetrics is the complete platform metrics response for one
// tenant over one window: totals, real rates, failure-category
// breakdown, domain breakdown, provider breakdown, and time buckets.
type TenantMetrics struct {
	TenantID     uint           `json:"tenant_id"`
	WindowStart  time.Time      `json:"window_start"`
	WindowEnd    time.Time      `json:"window_end"`
	Volume       int64          `json:"volume"`
	Delivered    int64          `json:"delivered"`
	Failed       int64          `json:"failed"`
	Deferred     int64          `json:"deferred"`
	Bounced      int64          `json:"bounced"`
	PolicyDenied int64          `json:"policy_denied"`
	Suppressed   int64          `json:"suppressed"`
	Complaints   int64          `json:"complaints"`
	DeliveryRate float64        `json:"delivery_rate"`
	BounceRate   float64        `json:"bounce_rate"`
	FailureRate  float64        `json:"failure_rate"`
	DeferredRate float64        `json:"deferred_rate"`
	ByCategory   []BreakdownRow `json:"by_category"`
	ByDomain     []BreakdownRow `json:"by_domain"`
	ByProvider   []BreakdownRow `json:"by_provider"`
	TimeBuckets  []TimeBucket   `json:"time_buckets"`
	BucketSize   string         `json:"bucket_size"`
}

// TimeBucket is one UTC-aligned aggregation bucket.
type TimeBucket struct {
	Start     string `json:"start"`
	Delivered int64  `json:"delivered"`
	Failed    int64  `json:"failed"`
	Other     int64  `json:"other"`
	Total     int64  `json:"total"`
}

// MetricsSummary computes the tenant-wide metrics response. Rates are
// computed only where the denominator (volume) is real. Buckets are
// hourly for spans ≤ 48h and daily beyond, bounded to 200 buckets.
func (s *Service) MetricsSummary(ctx context.Context, tenantID uint, windowStart, windowEnd time.Time) (*TenantMetrics, error) {
	if tenantID == 0 {
		return nil, kernel.ValidationError(map[string]string{"tenant_id": "explicit tenant required"})
	}
	start, end, err := normalizeWindow(windowStart, windowEnd)
	if err != nil {
		return nil, err
	}
	totals, err := s.repo.AggregateTenant(ctx, tenantID, start, end)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "aggregate tenant metrics", err)
	}
	failed := totals.PermFail + totals.Complaints
	deferred := totals.TempFail + totals.Throttled
	byCategory, err := s.repo.CategoryBreakdown(ctx, tenantID, start, end)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "category breakdown", err)
	}
	byDomain, err := s.repo.DimensionBreakdown(ctx, tenantID, DimensionSendingDomain, start, end, 50)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "domain breakdown", err)
	}
	byProvider, err := s.repo.DimensionBreakdown(ctx, tenantID, DimensionRelayProvider, start, end, 50)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "provider breakdown", err)
	}
	granularity := "hour"
	if end.Sub(start) > 48*time.Hour {
		granularity = "day"
	}
	rawBuckets, err := s.repo.TimeBuckets(ctx, tenantID, start, end, granularity, 200)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "time buckets", err)
	}
	buckets := make([]TimeBucket, 0, len(rawBuckets))
	for _, b := range rawBuckets {
		buckets = append(buckets, TimeBucket{Start: b.Key, Total: b.Count})
	}

	m := &TenantMetrics{
		TenantID: tenantID, WindowStart: start, WindowEnd: end,
		Volume: totals.Volume, Delivered: totals.Delivered, Failed: failed,
		Deferred: deferred, Bounced: totals.Bounced, PolicyDenied: totals.PolicyDeny,
		Suppressed: totals.Suppressed, Complaints: totals.Complaints,
		ByCategory: byCategory, ByDomain: byDomain, ByProvider: byProvider,
		TimeBuckets: buckets, BucketSize: granularity,
	}
	if totals.Volume > 0 {
		m.DeliveryRate = float64(totals.Delivered) / float64(totals.Volume)
		m.BounceRate = float64(totals.Bounced) / float64(totals.Volume)
		m.FailureRate = float64(failed) / float64(totals.Volume)
		m.DeferredRate = float64(deferred) / float64(totals.Volume)
	}
	return m, nil
}

// IsSuppressed is the enforcement check for the real outbound path.
// The repository performs a single indexed point lookup (tenant,
// address) — never a scan — and the worker treats a store error as
// "not suppressed" (fail-open for delivery; see the delivery worker's
// documented contract). Normalization (lowercase) means case variants
// cannot bypass an active suppression.
func (s *Service) IsSuppressed(ctx context.Context, tenantID uint, address string) (bool, error) {
	suppressed, _, err := s.repo.IsSuppressed(ctx, tenantID, strings.ToLower(address), s.clock.Now())
	if err != nil {
		return false, kernel.Wrap(kernel.ErrCodeInternal, "check suppression", err)
	}
	return suppressed, nil
}

// ── Suppression lifecycle (operator surface) ───────────────────────

// AddSuppression is the operator-facing mutation — audited, reasoned,
// tenant-scoped. Duplicate requests are idempotent (atomic upsert);
// concurrent duplicates produce one logical suppression.
func (s *Service) AddSuppression(ctx context.Context, tenantID uint, address string, reason SuppressionReason, source string, actorID uint, notes string, expiresAt *time.Time) (*Suppression, error) {
	if address == "" {
		return nil, kernel.ValidationError(map[string]string{"address": "address is required"})
	}
	return s.addSuppressionInternal(ctx, tenantID, address, reason, source, actorID, notes, expiresAt)
}

func (s *Service) addSuppressionInternal(ctx context.Context, tenantID uint, address string, reason SuppressionReason, source string, actorID uint, notes string, expiresAt *time.Time) (*Suppression, error) {
	now := s.clock.Now()
	sup := &Suppression{
		TenantID: tenantID, Address: strings.ToLower(strings.TrimSpace(address)), Reason: reason, Source: source,
		ActorID: actorID, Notes: notes, ExpiresAt: expiresAt, State: SuppressionActive,
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.repo.AddSuppression(ctx, sup); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "add suppression", err)
	}
	// The upsert does not populate the id; read the row back by the
	// unique (tenant, address) key to return the real id/version/state.
	sup, err := s.repo.GetSuppressionByAddress(ctx, tenantID, sup.Address)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "read back suppression", err)
	}
	if sup == nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "read back suppression", fmt.Errorf("row vanished after upsert"))
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{
			Action: "deliverability.suppression.add", Target: fmt.Sprintf("suppression:%d", sup.ID), TenantID: tenantID,
			Result: "success", Reason: string(reason), ActorID: actorID, After: sup.Address,
		})
	}
	_ = s.repo.RecordSuppressionEvent(ctx, sup.ID, tenantID, actorID, "created", string(reason), now)
	return sup, nil
}

// GetSuppression returns one suppression, tenant-scoped. A
// cross-tenant id yields ErrSuppressionNotFound.
func (s *Service) GetSuppression(ctx context.Context, id, tenantID uint) (*Suppression, error) {
	sup, err := s.repo.GetSuppression(ctx, id, tenantID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get suppression", err)
	}
	if sup == nil {
		return nil, ErrSuppressionNotFound
	}
	return sup, nil
}

// ListSuppressions returns the filtered, paginated suppression list.
func (s *Service) ListSuppressions(ctx context.Context, f SuppressionFilter) ([]Suppression, int64, error) {
	return s.repo.ListSuppressions(ctx, f)
}

// ReleaseSuppression is the guarded active→released transition,
// tenant-scoped, audited, history-recorded. Releasing an already
// released/expired suppression is a no-op conflict, not an error.
func (s *Service) ReleaseSuppression(ctx context.Context, id, tenantID, actorID uint, reason string) error {
	now := s.clock.Now()
	ok, err := s.repo.ReleaseSuppression(ctx, id, tenantID, actorID, reason, now)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "release suppression", err)
	}
	if !ok {
		// Distinguish missing/cross-tenant from already-terminal so the
		// API can return 404 vs 409.
		cur, gerr := s.repo.GetSuppression(ctx, id, tenantID)
		if gerr != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "get suppression", gerr)
		}
		if cur == nil {
			return ErrSuppressionNotFound
		}
		return ErrSuppressionNotActive
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{
			Action: "deliverability.suppression.release", Target: fmt.Sprintf("suppression:%d", id), TenantID: tenantID,
			Result: "success", Reason: reason, ActorID: actorID,
		})
	}
	_ = s.repo.RecordSuppressionEvent(ctx, id, tenantID, actorID, "released", reason, now)
	return nil
}

// ReactivateSuppression is the guarded terminal→active transition
// (policy permits reactivation of released/expired suppressions),
// tenant-scoped, audited, history-recorded.
func (s *Service) ReactivateSuppression(ctx context.Context, id, tenantID, actorID uint, reason SuppressionReason, source string, notes string, expiresAt *time.Time) error {
	now := s.clock.Now()
	ok, err := s.repo.ReactivateSuppression(ctx, id, tenantID, actorID, reason, source, notes, expiresAt, now)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "reactivate suppression", err)
	}
	if !ok {
		cur, gerr := s.repo.GetSuppression(ctx, id, tenantID)
		if gerr != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "get suppression", gerr)
		}
		if cur == nil {
			return ErrSuppressionNotFound
		}
		return ErrSuppressionActive
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{
			Action: "deliverability.suppression.reactivate", Target: fmt.Sprintf("suppression:%d", id), TenantID: tenantID,
			Result: "success", Reason: string(reason), ActorID: actorID,
		})
	}
	_ = s.repo.RecordSuppressionEvent(ctx, id, tenantID, actorID, "reactivated", string(reason), now)
	return nil
}

// RemoveSuppression releases an active suppression by normalized
// address (release semantics; history preserved). The address-based
// DELETE route keeps its 404 contract for missing rows.
func (s *Service) RemoveSuppression(ctx context.Context, tenantID uint, address string, actorID uint) error {
	address = strings.ToLower(strings.TrimSpace(address))
	now := s.clock.Now()
	removed, err := s.repo.RemoveSuppression(ctx, tenantID, address, now)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "remove suppression", err)
	}
	if !removed {
		// Distinguish "never existed" from "already released".
		sup, gerr := s.repo.GetSuppressionByAddress(ctx, tenantID, address)
		if gerr != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "get suppression", gerr)
		}
		if sup == nil {
			return ErrSuppressionNotFound
		}
		return ErrSuppressionNotActive
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{
			Action: "deliverability.suppression.remove", Target: "address", TenantID: tenantID,
			Result: "success", ActorID: actorID, After: address,
		})
	}
	sup, _ := s.repo.GetSuppressionByAddress(ctx, tenantID, address)
	if sup != nil {
		_ = s.repo.RecordSuppressionEvent(ctx, sup.ID, tenantID, actorID, "released", "operator release", now)
	}
	return nil
}

// ListSuppressionEvents returns the append-only lifecycle evidence.
func (s *Service) ListSuppressionEvents(ctx context.Context, id, tenantID uint, limit int) ([]SuppressionEvent, error) {
	if _, err := s.GetSuppression(ctx, id, tenantID); err != nil {
		return nil, err
	}
	return s.repo.ListSuppressionEvents(ctx, id, tenantID, limit)
}

// ReconcileExpired is called ONLY by the background scheduler — it
// marks expired active suppressions terminal. Never invoked from the
// delivery or request path.
func (s *Service) ReconcileExpired(ctx context.Context) (int64, error) {
	n, err := s.repo.ReconcileExpired(ctx, s.clock.Now())
	if err != nil {
		return 0, kernel.Wrap(kernel.ErrCodeInternal, "reconcile expired suppressions", err)
	}
	return n, nil
}

// PurgeOldSignals applies the bounded retention policy (90 days) to
// the append-only signal store. Called by the scheduler.
func (s *Service) PurgeOldSignals(ctx context.Context, retention time.Duration) (int64, error) {
	n, err := s.repo.PurgeSignalsBefore(ctx, s.clock.Now().Add(-retention))
	if err != nil {
		return 0, kernel.Wrap(kernel.ErrCodeInternal, "purge deliverability signals", err)
	}
	return n, nil
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
