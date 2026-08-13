package security

import (
	"context"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/platform/kernel"
)

type Service struct {
	repo  *Repository
	clock kernel.Clock
}

func NewService(repo *Repository, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{repo: repo, clock: clock}
}

// RecordEvent is the single normalization choke point every existing
// subsystem (antivirus, antispam, ACL, auth) should call into. Detail
// is truncated (never rejected) at MaxDetailLength — a caller that
// accidentally passes something too long gets it safely shortened
// rather than losing the whole security event to a validation error,
// which would be worse for an audit trail than a truncated one.
func (s *Service) RecordEvent(ctx context.Context, tenantID uint, category Category, severity Severity, sourceSystem, actor, detail string) (*Event, error) {
	if !category.IsValid() {
		return nil, ErrInvalidCategory
	}
	if severity == "" {
		severity = SeverityInfo
	}
	detail = redactAndTruncate(detail)
	e := &Event{
		TenantID: tenantID, Category: category, Severity: severity,
		SourceSystem: sourceSystem, Actor: actor, Detail: detail, CreatedAt: s.clock.Now(),
	}
	if err := s.repo.Record(ctx, e); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "record security event", err)
	}
	return e, nil
}

// redactAndTruncate is the one place Detail is sanitized before
// persistence: strips anything that looks like it could be a
// credential/token fragment (best-effort keyword scrub) and caps
// length. This is deliberately conservative — it's a safety net for
// an accidental over-share by a calling subsystem, not a substitute
// for that subsystem never passing a secret in the first place.
func redactAndTruncate(detail string) string {
	lower := strings.ToLower(detail)
	for _, kw := range []string{"password=", "passwd=", "secret=", "token=", "authorization:", "api_key=", "apikey="} {
		if idx := strings.Index(lower, kw); idx >= 0 {
			detail = detail[:idx] + "[redacted]"
			break
		}
	}
	if len(detail) > MaxDetailLength {
		// Truncate on a rune boundary, not a raw byte index — a byte
		// cut could land mid-UTF-8-sequence and either corrupt the
		// last character or (for some invalid sequences) confuse
		// downstream string handling.
		truncated := []rune(detail)
		if len(truncated) > MaxDetailLength {
			truncated = truncated[:MaxDetailLength]
		}
		detail = string(truncated) + "…"
	}
	return detail
}

func (s *Service) ListEvents(ctx context.Context, f ListFilter) ([]Event, error) {
	return s.repo.List(ctx, f)
}

// PurgeRetention deletes events older than retention, batch by batch,
// stopping once a batch affects 0 rows. maxBatches bounds total work
// per call so a single retention sweep can never run unbounded.
func (s *Service) PurgeRetention(ctx context.Context, retention time.Duration, maxBatches int) (int64, error) {
	cutoff := s.clock.Now().Add(-retention)
	var total int64
	if maxBatches <= 0 {
		maxBatches = 100
	}
	for i := 0; i < maxBatches; i++ {
		n, err := s.repo.PurgeOlderThan(ctx, cutoff, 1000)
		if err != nil {
			return total, kernel.Wrap(kernel.ErrCodeInternal, "purge security event retention", err)
		}
		total += n
		if n == 0 {
			break
		}
	}
	return total, nil
}
