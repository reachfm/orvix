package observability

import (
	"context"
	"strconv"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// NotificationSender is the provider port for alert notifications
// (email, webhook, Slack, PagerDuty, ...). Credentials for concrete
// implementations must be stored via the project's existing
// secret-reference mechanism (see internal/platform/relay's use of
// internal/config's AES-GCM helpers) — this package never handles a
// plaintext notification-provider credential itself.
type NotificationSender interface {
	Send(ctx context.Context, alert Alert, rule Rule) error
}

type Service struct {
	repo   *Repository
	audit  *audit.ExtendedStore
	outbox *kernel.OutboxRepository
	clock  kernel.Clock
	sender NotificationSender
}

func NewService(repo *Repository, auditStore *audit.ExtendedStore, outbox *kernel.OutboxRepository, sender NotificationSender, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, audit: auditStore, outbox: outbox, sender: sender, clock: clock}
}

func (s *Service) CreateRule(ctx context.Context, rule Rule) (*Rule, error) {
	if rule.Name == "" || rule.MetricName == "" || rule.Comparator == "" {
		return nil, ErrInvalidRule
	}
	if rule.Scope == "" {
		rule.Scope = "global"
	}
	if rule.CooldownSeconds <= 0 {
		rule.CooldownSeconds = 300
	}
	now := s.clock.Now()
	rule.Enabled = true
	rule.CreatedAt, rule.UpdatedAt = now, now
	if err := s.repo.CreateRule(ctx, &rule); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create alert rule", err)
	}
	return &rule, nil
}

// MetricSample is one observed value for one metric+scope at a point
// in time — the input to Evaluate.
type MetricSample struct {
	MetricName string
	Scope      string
	Value      float64
}

// Evaluate runs every enabled rule against the supplied samples for
// one evaluation tick. This is pure orchestration over Repository
// methods — no metric collection happens here (reused from
// internal/observability.MetricsCollector.Snapshot() by the caller,
// which maps the snapshot into MetricSamples).
func (s *Service) Evaluate(ctx context.Context, samples []MetricSample) error {
	rules, err := s.repo.ListEnabledRules(ctx)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "list enabled rules", err)
	}
	now := s.clock.Now()
	for _, rule := range rules {
		for _, sample := range samples {
			if sample.MetricName != rule.MetricName {
				continue
			}
			if rule.Scope != "" && rule.Scope != "global" && rule.Scope != sample.Scope {
				continue
			}
			if err := s.evaluateOne(ctx, rule, sample, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) evaluateOne(ctx context.Context, rule Rule, sample MetricSample, now time.Time) error {
	condition := rule.Comparator.Evaluate(sample.Value, rule.Threshold)

	alert, created, err := s.repo.GetOrCreateAlert(ctx, rule.ID, sample.Scope, sample.Value, now)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "get or create alert", err)
	}
	if !created {
		if err := s.repo.UpdateValue(ctx, alert.ID, sample.Value); err == nil {
			alert.Value = sample.Value
			// UpdateValue increments the DB row's version; the in-memory
			// copy must track it or every subsequent TransitionState
			// call in this function compares against a stale version
			// and spuriously reports a conflict.
			alert.Version++
		}
	}

	if !condition {
		// Condition cleared: resolve if it was pending/firing/acked.
		if alert.State == AlertPending || alert.State == AlertFiring || alert.State == AlertAcknowledged {
			ok, err := s.repo.TransitionState(ctx, alert.ID, alert.State, AlertResolved, alert.Version, now, map[string]any{"resolved_at": now})
			if err != nil {
				return kernel.Wrap(kernel.ErrCodeInternal, "resolve alert", err)
			}
			if ok && s.outbox != nil {
				_ = s.outbox.Enqueue(ctx, s.repo.db, "observability.alert.resolved", strconv.FormatUint(uint64(alert.ID), 10), map[string]any{"rule_id": rule.ID, "scope": sample.Scope}, now)
			}
		}
		return nil
	}

	switch alert.State {
	case AlertPending:
		if created {
			return nil // just started observing; wait for Duration
		}
		if now.Sub(alert.FirstObservedAt) >= rule.Duration {
			ok, err := s.repo.TransitionState(ctx, alert.ID, AlertPending, AlertFiring, alert.Version, now, map[string]any{"fired_at": now, "last_notified_at": now})
			if err != nil {
				return kernel.Wrap(kernel.ErrCodeInternal, "fire alert", err)
			}
			if ok {
				s.notify(ctx, *alert, rule)
				if s.outbox != nil {
					_ = s.outbox.Enqueue(ctx, s.repo.db, "observability.alert.firing", strconv.FormatUint(uint64(alert.ID), 10), map[string]any{"rule_id": rule.ID, "scope": sample.Scope, "severity": rule.Severity}, now)
				}
			}
		}
	case AlertFiring:
		// Renotify only after cooldown — dedup/throttling.
		if alert.LastNotifiedAt == nil || now.Sub(*alert.LastNotifiedAt) >= time.Duration(rule.CooldownSeconds)*time.Second {
			ok, err := s.repo.TransitionState(ctx, alert.ID, AlertFiring, AlertFiring, alert.Version, now, map[string]any{"last_notified_at": now})
			if err == nil && ok {
				s.notify(ctx, *alert, rule)
			}
		}
	case AlertResolved:
		// Condition re-triggered after a prior resolution: start a
		// fresh pending observation window rather than immediately
		// re-firing — a flapping condition must re-earn Duration.
		_, err := s.repo.TransitionState(ctx, alert.ID, AlertResolved, AlertPending, alert.Version, now, map[string]any{"first_observed_at": now, "fired_at": nil, "resolved_at": nil})
		if err != nil {
			return kernel.Wrap(kernel.ErrCodeInternal, "re-arm resolved alert", err)
		}
	case AlertSilenced:
		if alert.SilencedUntil != nil && !alert.SilencedUntil.After(now) {
			// Silence expired: treat like Firing (fall through by re-evaluating next tick after transition).
			_, _ = s.repo.TransitionState(ctx, alert.ID, AlertSilenced, AlertFiring, alert.Version, now, map[string]any{"last_notified_at": now})
		}
		// While actively silenced, no notification.
	}
	return nil
}

func (s *Service) notify(ctx context.Context, alert Alert, rule Rule) {
	if s.sender == nil {
		return
	}
	_ = s.sender.Send(ctx, alert, rule)
}

// Acknowledge, Resolve (manual), Silence are operator actions.

func (s *Service) Acknowledge(ctx context.Context, alertID, actorID uint) error {
	a, err := s.repo.GetAlert(ctx, alertID)
	if err != nil {
		return err
	}
	if a.State != AlertFiring {
		return ErrNotFiring
	}
	now := s.clock.Now()
	ok, err := s.repo.TransitionState(ctx, alertID, AlertFiring, AlertAcknowledged, a.Version, now, map[string]any{"acknowledged_at": now, "acknowledged_by": actorID})
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "acknowledge alert", err)
	}
	if !ok {
		return ErrVersionConflict
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "observability.alert.acknowledge", TargetID: alertID, ActorID: actorID, Result: "success"})
	}
	return nil
}

func (s *Service) Silence(ctx context.Context, alertID uint, until time.Time, actorID uint) error {
	a, err := s.repo.GetAlert(ctx, alertID)
	if err != nil {
		return err
	}
	if a.State != AlertFiring && a.State != AlertAcknowledged && a.State != AlertPending {
		return ErrNotFiring
	}
	now := s.clock.Now()
	ok, err := s.repo.TransitionState(ctx, alertID, a.State, AlertSilenced, a.Version, now, map[string]any{"silenced_until": until})
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "silence alert", err)
	}
	if !ok {
		return ErrVersionConflict
	}
	if s.audit != nil {
		_ = s.audit.Record(ctx, &audit.ExtendedEntry{Action: "observability.alert.silence", TargetID: alertID, ActorID: actorID, Result: "success"})
	}
	return nil
}

func (s *Service) ListAlerts(ctx context.Context, state AlertState, afterID uint, limit int) ([]Alert, error) {
	return s.repo.ListAlerts(ctx, state, afterID, limit)
}

// ── SLO burn rate ────────────────────────────────────────────────

// ComputeBurnRate is a pure function over caller-supplied good/total
// counts (typically sourced from internal/platform/deliverability's
// Aggregate for a delivery-latency/success SLO, reused not
// duplicated) — this package does not query metrics tables itself.
func ComputeBurnRate(slo SLO, windowStart, windowEnd time.Time, total, good int64) BurnRate {
	br := BurnRate{SLOName: slo.Name, WindowStart: windowStart, WindowEnd: windowEnd, Total: total, Good: good}
	errorBudget := 1 - slo.Target
	br.ErrorBudget = errorBudget
	if total == 0 {
		return br
	}
	br.SuccessRatio = float64(good) / float64(total)
	failureRatio := 1 - br.SuccessRatio
	if errorBudget > 0 {
		br.ErrorBudgetUsed = failureRatio / errorBudget
		br.BurnRate = br.ErrorBudgetUsed
	}
	return br
}
