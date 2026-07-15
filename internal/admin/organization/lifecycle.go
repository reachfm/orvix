package organization

import (
	"context"
	"database/sql"
	"errors"
	"time"
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
	if err != nil || org == nil {
		return ErrOrganizationNotFound
	}
	if !org.Active {
		return ErrOrganizationSuspended
	}
	if err := s.repo.SetActive(ctx, orgID, false); err != nil {
		return err
	}
	return s.repo.RecordSuspension(ctx, orgID, suspendedBy, reason, note)
}

func (s *Service) ReactivateOrganization(ctx context.Context, orgID uint) error {
	org, err := s.repo.GetByID(ctx, orgID)
	if err != nil || org == nil {
		return ErrOrganizationNotFound
	}
	if org.Active {
		return nil
	}
	if err := s.repo.SetActive(ctx, orgID, true); err != nil {
		return err
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
