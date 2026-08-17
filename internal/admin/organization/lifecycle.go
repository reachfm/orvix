package organization

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
)

type SuspensionState string

const (
	SuspensionNone    SuspensionState = ""
	SuspensionManual  SuspensionState = "manual"
	SuspensionBilling SuspensionState = "billing"
	SuspensionAbuse   SuspensionState = "abuse"
	SuspensionLegal   SuspensionState = "legal"
)

type DeletionState string

const (
	DeletionNone      DeletionState = ""
	DeletionRequested DeletionState = "deletion_requested"
	DeletionRetention DeletionState = "retention"
	DeletionCompleted DeletionState = "deleted"
)

var (
	ErrOrganizationSuspended    = errors.New("organization is suspended")
	ErrOrganizationDeleting     = errors.New("organization is pending deletion")
	ErrOrganizationDeleted      = errors.New("organization has been deleted")
	ErrSuspensionAlreadyActive  = errors.New("organization is already suspended for this reason")
	ErrDeletionAlreadyRequested = errors.New("deletion already requested for this organization")
	ErrRetentionPeriodActive    = errors.New("organization is in retention period and cannot be fully deleted yet")
)

type SuspensionRecord struct {
	ID             uint            `json:"id"`
	OrganizationID uint            `json:"organization_id"`
	Reason         SuspensionState `json:"reason"`
	SuspendedBy    uint            `json:"suspended_by"`
	Note           string          `json:"note"`
	SuspendedAt    time.Time       `json:"suspended_at"`
	ReactivatedAt  *time.Time      `json:"reactivated_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type DeletionRecord struct {
	ID                 uint          `json:"id"`
	OrganizationID     uint          `json:"organization_id"`
	RequestedBy        uint          `json:"requested_by"`
	State              DeletionState `json:"state"`
	RetentionUntil     *time.Time    `json:"retention_until,omitempty"`
	RequestedAt        time.Time     `json:"requested_at"`
	ConfirmedAt        *time.Time    `json:"confirmed_at,omitempty"`
	RetentionExpiresAt *time.Time    `json:"retention_expires_at,omitempty"`
	CancelledAt        *time.Time    `json:"cancelled_at,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
}

func (s *Service) SuspendOrganization(ctx context.Context, orgID, suspendedBy uint, reason SuspensionState, note string) error {
	org, err := s.repo.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	if org == nil {
		return ErrOrganizationNotFound
	}
	// Atomic conditional transition (active=true -> active=false) closes
	// the TOCTOU race a separate GetByID+SetActive would have: only the
	// request that wins the WHERE active=true clause proceeds to record
	// a suspension row. A loser (or a genuinely-already-suspended org)
	// gets the same stable ErrOrganizationSuspended, not a duplicate
	// suspension record.
	applied, err := s.repo.SetActiveIfCurrentlyIs(ctx, orgID, true, false)
	if err != nil {
		return err
	}
	if !applied {
		return ErrOrganizationSuspended
	}
	return s.repo.RecordSuspension(ctx, orgID, suspendedBy, reason, note)
}

func (s *Service) ReactivateOrganization(ctx context.Context, orgID uint) error {
	org, err := s.repo.GetByID(ctx, orgID)
	if err != nil {
		return err
	}
	if org == nil {
		return ErrOrganizationNotFound
	}
	applied, err := s.repo.SetActiveIfCurrentlyIs(ctx, orgID, false, true)
	if err != nil {
		return err
	}
	if !applied {
		// Already active: idempotent no-op, matching the prior behavior
		// (a second reactivate of an already-active org is not an error).
		return nil
	}
	return s.repo.CloseActiveSuspension(ctx, orgID)
}

func (s *Service) RequestDeletion(ctx context.Context, orgID, requestedBy uint) error {
	exists, err := s.repo.HasActiveDeletionRequest(ctx, orgID)
	if err != nil {
		return err
	}
	if exists {
		return ErrDeletionAlreadyRequested
	}
	now := time.Now().UTC()
	retentionUntil := now.AddDate(0, 0, 30)
	return s.repo.CreateDeletionRequest(ctx, orgID, requestedBy, now, retentionUntil)
}

func (s *Service) CancelDeletion(ctx context.Context, orgID uint) error {
	return s.repo.CancelDeletionRequest(ctx, orgID)
}

func (s *Service) ConfirmDeletion(ctx context.Context, orgID uint) error {
	rec, err := s.repo.GetActiveDeletion(ctx, orgID)
	if err != nil || rec == nil {
		return errors.New("no active deletion request")
	}
	if time.Now().UTC().Before(*rec.RetentionExpiresAt) {
		return ErrRetentionPeriodActive
	}
	now := time.Now().UTC()
	if err := s.repo.SetActive(ctx, orgID, false); err != nil {
		return err
	}
	return s.repo.CompleteDeletion(ctx, orgID, now)
}

var _ *SuspensionRecord // suppress unused
var _ *DeletionRecord

func (r *OrganizationRepo) RecordSuspension(ctx context.Context, orgID, suspendedBy uint, reason SuspensionState, note string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO org_suspensions (organization_id, reason, suspended_by, note, suspended_at, created_at)
		VALUES (`+r.dialect.Placeholders(6)+`)`,
		orgID, reason, suspendedBy, note, now, now)
	return err
}

func (r *OrganizationRepo) CloseActiveSuspension(ctx context.Context, orgID uint) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`UPDATE org_suspensions SET reactivated_at = `+r.dialect.Placeholder(1)+` WHERE organization_id = `+r.dialect.Placeholder(2)+` AND reactivated_at IS NULL`, now, orgID)
	return err
}

func (r *OrganizationRepo) HasActiveDeletionRequest(ctx context.Context, orgID uint) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM org_deletions WHERE organization_id = `+r.dialect.Placeholder(1)+` AND state != 'completed' AND cancelled_at IS NULL`, orgID).Scan(&count)
	return count > 0, err
}

func (r *OrganizationRepo) CreateDeletionRequest(ctx context.Context, orgID, requestedBy uint, requestedAt time.Time, retentionUntil time.Time) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO org_deletions (organization_id, requested_by, state, requested_at, retention_expires_at, created_at)
		VALUES (`+r.dialect.Placeholders(6)+`)`,
		orgID, requestedBy, DeletionRequested, requestedAt, retentionUntil, now)
	return err
}

func (r *OrganizationRepo) GetActiveDeletion(ctx context.Context, orgID uint) (*DeletionRecord, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, organization_id, requested_by, state, retention_expires_at, requested_at, confirmed_at, cancelled_at, created_at
		FROM org_deletions WHERE organization_id = `+r.dialect.Placeholder(1)+` AND cancelled_at IS NULL AND state != 'completed'
		ORDER BY created_at DESC LIMIT 1`, orgID)
	var rec DeletionRecord
	var confirmedAt, cancelledAt, retentionExpires sql.NullTime
	err := row.Scan(&rec.ID, &rec.OrganizationID, &rec.RequestedBy, &rec.State, &retentionExpires, &rec.RequestedAt, &confirmedAt, &cancelledAt, &rec.CreatedAt)
	if err != nil {
		return nil, err
	}
	if retentionExpires.Valid {
		rec.RetentionExpiresAt = &retentionExpires.Time
	}
	if confirmedAt.Valid {
		rec.ConfirmedAt = &confirmedAt.Time
	}
	if cancelledAt.Valid {
		rec.CancelledAt = &cancelledAt.Time
	}
	return &rec, nil
}

func (r *OrganizationRepo) CancelDeletionRequest(ctx context.Context, orgID uint) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`UPDATE org_deletions SET cancelled_at = `+r.dialect.Placeholder(1)+`, state = 'retention' WHERE organization_id = `+r.dialect.Placeholder(2)+` AND cancelled_at IS NULL AND state != 'completed'`, now, orgID)
	return err
}

func (r *OrganizationRepo) CompleteDeletion(ctx context.Context, orgID uint, at time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE org_deletions SET confirmed_at = `+r.dialect.Placeholder(1)+`, state = 'completed' WHERE organization_id = `+r.dialect.Placeholder(2)+` AND cancelled_at IS NULL`, at, orgID)
	if err != nil {
		return err
	}
	// Hard-delete tenant data after retention period
	_, err = r.db.ExecContext(ctx, `UPDATE tenants SET deleted_at = `+r.dialect.Placeholder(1)+` WHERE id = `+r.dialect.Placeholder(2), at, orgID)
	return err
}

// DeletionBlockedError is returned by PlatformScheduleDeletion when the
// organization still has dependencies that must be removed first. Callers
// (the HTTP handler) surface Blockers verbatim to the caller so the
// platform admin knows exactly what to clean up.
type DeletionBlockedError struct {
	Blockers []string
}

func (e *DeletionBlockedError) Error() string {
	return "organization has blocking dependencies: " + strings.Join(e.Blockers, "; ")
}

// ErrDeletionConfirmationMismatch is returned when the caller's typed
// confirmation string does not exactly match the organization's domain.
var ErrDeletionConfirmationMismatch = errors.New("confirmation does not match organization domain")

// ErrDeletionReasonRequired is returned when no reason was supplied.
var ErrDeletionReasonRequired = errors.New("reason is required")

// PlatformScheduleDeletion is the Platform-Admin-only entry point for
// scheduling an organization's deletion (Phase G). Unlike RequestDeletion
// above (a tenant's own self-service request, reachable only by a caller
// already scoped to that tenant), this is invoked by platform staff acting
// on ANY organization, so it enforces additional platform-side guardrails
// the self-service path doesn't need:
//
//   - a typed confirmation string that must exactly match the org's domain
//     (case-insensitive, trimmed) — guards against a misclick on the wrong
//     row in a list of organizations;
//   - a mandatory reason, recorded in the audit trail;
//   - a dependency check: the organization must have zero active domains
//     and zero active mailboxes, otherwise the caller gets back the exact
//     list of what's blocking it instead of a generic error;
//   - idempotency: calling this twice on an already-scheduled organization
//     is a no-op (reports idempotent=true) rather than erroring or creating
//     a second deletion record.
//
// This reuses the exact same underlying state machine as the self-service
// path (org_deletions table, 30-day retention window, soft state
// transitions) — see RequestDeletion/CancelDeletion/ConfirmDeletion above.
// It deliberately never issues a raw DELETE: CompleteDeletion (run later,
// after the retention window, by a separate confirm step) only flips
// tenants.deleted_at, preserving billing/audit history.
func (s *Service) PlatformScheduleDeletion(ctx context.Context, orgID, actorID uint, confirmDomain, reason string) (idempotent bool, err error) {
	org, err := s.repo.GetByID(ctx, orgID)
	if err != nil {
		return false, err
	}
	if org == nil {
		return false, ErrOrganizationNotFound
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		return false, ErrDeletionReasonRequired
	}
	if !strings.EqualFold(strings.TrimSpace(confirmDomain), org.Domain) {
		return false, ErrDeletionConfirmationMismatch
	}

	// Idempotency: an already-scheduled (and not cancelled/completed)
	// deletion is a success no-op, not an error — a platform admin double
	// clicking "Schedule Deletion" (or a retried request) must not create
	// a second deletion record or fail loudly.
	exists, err := s.repo.HasActiveDeletionRequest(ctx, orgID)
	if err != nil {
		return false, err
	}
	if exists {
		return true, nil
	}

	var blockers []string
	activeDomains, err := s.repo.CountActiveDomains(ctx, orgID)
	if err != nil {
		return false, err
	}
	if activeDomains > 0 {
		blockers = append(blockers, fmt.Sprintf("%d active domain(s)", activeDomains))
	}
	activeMailboxes, err := s.repo.CountActiveMailboxes(ctx, orgID)
	if err != nil {
		return false, err
	}
	if activeMailboxes > 0 {
		blockers = append(blockers, fmt.Sprintf("%d active mailbox(es)", activeMailboxes))
	}
	if len(blockers) > 0 {
		return false, &DeletionBlockedError{Blockers: blockers}
	}

	now := time.Now().UTC()
	retentionUntil := now.AddDate(0, 0, 30)
	entry := &audit.ExtendedEntry{
		Action:   "organization.deletion_scheduled",
		Target:   fmt.Sprintf("tenant:%d", orgID),
		TargetID: orgID,
		TenantID: orgID,
		Result:   "success",
		Reason:   reason,
	}
	err = s.mutateWithAudit(ctx, entry, func(repo *OrganizationRepo) error {
		return repo.CreateDeletionRequest(ctx, orgID, actorID, now, retentionUntil)
	})
	return false, err
}
