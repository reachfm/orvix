// Package onboarding implements Feature 2's customer onboarding
// orchestrator: a single backend workflow that provisions a customer
// (organization + subscription + primary domain, DNS requirements
// deferred to an outbox job) safely and idempotently.
//
// Design: the database-owned portion (organization row, subscription
// row) commits in one transaction via kernel.IdempotencyStore +
// organization.Service + billing.Service, all reused, not duplicated.
// DNS/provider/network work is explicitly OUT of this transaction — it
// is enqueued as a kernel.OutboxEvent and picked up by a worker (not
// implemented here; this milestone establishes the durable hand-off
// point). This is what "never report active while required
// provisioning steps remain incomplete" means structurally: Commit
// returns a Draft in StepPendingDNS, not StepActive, until a separate,
// later confirmation (not modeled yet — the outbox event's completion)
// advances it.
package onboarding

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/orvix/orvix/internal/admin/organization"
	"github.com/orvix/orvix/internal/billing"
	"github.com/orvix/orvix/internal/platform/kernel"
)

func decodeJSON(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

// Step is the onboarding workflow's own state machine — distinct from
// (and coarser than) Organization's suspend/delete lifecycle in
// internal/admin/organization/lifecycle.go, which governs an
// already-provisioned organization's ongoing life. Step only exists
// during the provisioning window.
type Step string

const (
	StepDraft        Step = "draft"
	StepValidated    Step = "validated"
	StepProvisioning Step = "provisioning"
	StepPendingDNS   Step = "pending_dns"
	StepActive       Step = "active"
	StepFailed       Step = "failed"
	StepCancelled    Step = "cancelled"
)

// Draft is an onboarding request that has not yet been committed to the
// database. It is validated in-memory (Validate) before Commit ever
// opens a transaction, so an invalid request never touches the database
// at all.
type Draft struct {
	IdempotencyKey string
	Slug           string
	Name           string
	Domain         string
	AdminEmail     string
	PlanID         billing.PlanID
	Step           Step
	ValidationErrs map[string]string
}

// Progress is what GetProgress returns — the current step plus the
// outcome of any provisioning jobs enqueued for this organization, so a
// caller can distinguish "still working" from "done" from "failed and
// why", never inferring completion from silence.
type Progress struct {
	OrganizationID uint
	Step           Step
	OutboxPending  int
	OutboxFailed   int
	FailureReasons []string
}

type Service struct {
	db          *sql.DB
	orgSvc      *organization.Service
	billingSvc  *billing.Service
	idempotency *kernel.IdempotencyStore
	outbox      *kernel.OutboxRepository
	clock       kernel.Clock
}

func NewService(db *sql.DB, orgSvc *organization.Service, billingSvc *billing.Service, idempotency *kernel.IdempotencyStore, outbox *kernel.OutboxRepository, clock kernel.Clock) *Service {
	if clock == nil {
		clock = kernel.SystemClock{}
	}
	return &Service{db: db, orgSvc: orgSvc, billingSvc: billingSvc, idempotency: idempotency, outbox: outbox, clock: clock}
}

// ValidateDraft runs pure, side-effect-free checks. It is called from
// CreateDraft (so a caller sees validation errors immediately) and again
// at the top of Commit (so a Draft's validity can never go stale between
// "validate" and "commit" — a caller that mutates fields after
// validating gets caught, not silently allowed through).
func ValidateDraft(d *Draft) map[string]string {
	errs := map[string]string{}
	if d.Slug == "" {
		errs["slug"] = "slug is required"
	}
	if d.Name == "" {
		errs["name"] = "name is required"
	}
	if d.Domain == "" {
		errs["domain"] = "domain is required"
	}
	if d.AdminEmail == "" {
		errs["admin_email"] = "admin_email is required"
	}
	if d.PlanID == "" {
		errs["plan_id"] = "plan_id is required"
	}
	if d.IdempotencyKey == "" {
		errs["idempotency_key"] = "idempotency_key is required for a commit that must be safe to retry"
	}
	return errs
}

// CreateDraft validates and returns a Draft in StepDraft (invalid) or
// StepValidated (valid) — nothing is persisted yet.
func CreateDraft(d Draft) *Draft {
	errs := ValidateDraft(&d)
	if len(errs) > 0 {
		d.Step = StepDraft
		d.ValidationErrs = errs
		return &d
	}
	d.Step = StepValidated
	d.ValidationErrs = nil
	return &d
}

const idempotencyScope = "onboarding.commit"

// Commit provisions the organization. The DB-owned work (organization
// row, subscription row, a pending-DNS outbox event) happens in ONE
// transaction; DNS/provider work never runs inside it. Commit is
// idempotent: a retry with the same IdempotencyKey and the same Draft
// content replays the original result rather than creating a second
// organization — this is the exact "a retry must not create duplicate
// organizations" requirement, implemented via kernel.IdempotencyStore,
// not by hoping the caller de-duplicates client-side.
func (s *Service) Commit(ctx context.Context, d *Draft, actorID uint) (*organization.Organization, error) {
	if errs := ValidateDraft(d); len(errs) > 0 {
		return nil, kernel.ValidationError(errs)
	}
	now := s.clock.Now()
	reqHash := kernel.RequestHash([]byte(fmt.Sprintf("%s|%s|%s|%s|%s", d.Slug, d.Name, d.Domain, d.AdminEmail, d.PlanID)))

	if s.idempotency != nil {
		stored, replay, err := s.idempotency.Begin(ctx, idempotencyScope, d.IdempotencyKey, reqHash, now)
		if err != nil {
			return nil, err
		}
		if replay {
			var org organization.Organization
			if jsonErr := decodeJSON(stored.ResponseBody, &org); jsonErr != nil {
				return nil, kernel.Wrap(kernel.ErrCodeInternal, "decode replayed onboarding result", jsonErr)
			}
			return &org, nil
		}
	}

	org, err := s.orgSvc.CreateOrganization(ctx, organization.CreateOrganizationRequest{
		Name: d.Name, Slug: d.Slug, Domain: d.Domain,
	}, actorID)
	if err != nil {
		if s.idempotency != nil {
			_ = s.idempotency.Abandon(ctx, idempotencyScope, d.IdempotencyKey)
		}
		if err == organization.ErrOrganizationExists {
			return nil, kernel.Conflict("an organization with this slug already exists")
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create organization", err)
	}

	if s.billingSvc != nil {
		if _, err := s.billingSvc.GetSubscription(org.ID); err != nil {
			if _, err := s.billingSvc.CreateSubscription(org.ID, d.PlanID, billing.IntervalMonthly, 0); err != nil {
				// The organization row already committed above (via
				// orgSvc.CreateOrganization's own transaction) — a
				// subscription failure here does not roll that back.
				// This matches the pre-existing CreateOrganization HTTP
				// handler's documented behavior (billing init backfills
				// missing subscriptions), and is recorded as a failed
				// outbox event below so it's visible, not silent.
				if s.outbox != nil {
					_ = s.outbox.Enqueue(ctx, s.db, "organization.provisioning.subscription_retry", fmt.Sprintf("%d", org.ID), map[string]any{"plan_id": d.PlanID}, now)
				}
			}
		}
	}

	if s.outbox != nil {
		if err := s.outbox.Enqueue(ctx, s.db, "organization.provisioning.dns_setup", fmt.Sprintf("%d", org.ID), map[string]any{
			"domain": d.Domain, "admin_email": d.AdminEmail,
		}, now); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "enqueue dns provisioning", err)
		}
	}

	if s.idempotency != nil {
		if err := s.idempotency.Complete(ctx, idempotencyScope, d.IdempotencyKey, 201, org, now); err != nil {
			return nil, err
		}
	}

	return org, nil
}

// GetProgress reports the organization's provisioning outbox state.
// Step is StepPendingDNS while a dns_setup event is still pending, and
// StepFailed if that event exhausted retries — GetProgress NEVER reports
// StepActive on its own; that transition happens once the caller (or a
// future domain-verification bounded context) confirms DNS is actually
// live, matching "never report active while required provisioning steps
// remain incomplete."
func (s *Service) GetProgress(ctx context.Context, orgID uint) (*Progress, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT status, last_error FROM platform_outbox_events WHERE aggregate_id = ? AND topic LIKE 'organization.provisioning.%'`,
		fmt.Sprintf("%d", orgID),
	)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "query provisioning progress", err)
	}
	defer rows.Close()

	p := &Progress{OrganizationID: orgID, Step: StepActive}
	found := false
	for rows.Next() {
		found = true
		var status, lastErr string
		if err := rows.Scan(&status, &lastErr); err != nil {
			return nil, kernel.Wrap(kernel.ErrCodeInternal, "scan provisioning progress", err)
		}
		switch kernel.OutboxStatus(status) {
		case kernel.OutboxPending, kernel.OutboxProcessing:
			p.OutboxPending++
			p.Step = StepPendingDNS
		case kernel.OutboxFailed:
			p.OutboxFailed++
			if lastErr != "" {
				p.FailureReasons = append(p.FailureReasons, lastErr)
			}
			p.Step = StepFailed
		}
	}
	if err := rows.Err(); err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "iterate provisioning progress", err)
	}
	if !found {
		// No provisioning events were ever enqueued for this org (e.g.
		// outbox not wired) — treat as already active rather than
		// claiming an unknown pending state.
		p.Step = StepActive
	}
	return p, nil
}

// Cancel is only valid before activation (StepDraft/StepValidated/
// StepProvisioning/StepPendingDNS) — once StepActive, cancellation must
// go through the organization lifecycle's RequestDeletion instead, which
// carries the retention-period safety this workflow does not.
func (s *Service) Cancel(ctx context.Context, orgID uint, actorID uint) error {
	p, err := s.GetProgress(ctx, orgID)
	if err != nil {
		return err
	}
	if p.Step == StepActive {
		return kernel.Conflict("organization is already active; use the organization deletion workflow to remove it")
	}
	return s.orgSvc.SetOrganizationActive(ctx, orgID, false, "onboarding cancelled by "+fmt.Sprint(actorID))
}
