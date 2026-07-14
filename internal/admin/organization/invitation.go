package organization

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvitationNotFound      = errors.New("invitation not found")
	ErrInvitationExpired       = errors.New("invitation has expired")
	ErrInvitationAlreadyUsed   = errors.New("invitation already accepted")
	ErrInvitationRevoked       = errors.New("invitation was revoked")
	ErrCannotInviteSelf        = errors.New("cannot invite yourself to your own organization")
	ErrOwnerCannotBeRemoved    = errors.New("cannot remove the organization owner")
	ErrLastOwnerCannotTransfer = errors.New("cannot transfer ownership: no remaining owner would exist")
)

type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationExpired  InvitationStatus = "expired"
	InvitationRevoked  InvitationStatus = "revoked"
)

type OrganizationInvitation struct {
	ID             uint             `json:"id"`
	OrganizationID uint             `json:"organization_id"`
	InviterID      uint             `json:"inviter_id"`
	Email          string           `json:"email"`
	Token          string           `json:"-"`
	Role           string           `json:"role"`
	Status         InvitationStatus `json:"status"`
	ExpiresAt      time.Time        `json:"expires_at"`
	AcceptedAt     *time.Time       `json:"accepted_at,omitempty"`
	RevokedAt      *time.Time       `json:"revoked_at,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type TransferRequest struct {
	ID               uint       `json:"id"`
	OrganizationID   uint       `json:"organization_id"`
	FromUserID       uint       `json:"from_user_id"`
	ToUserID         uint       `json:"to_user_id"`
	Token            string     `json:"-"`
	Status           string     `json:"status"`
	ExpiresAt        time.Time  `json:"expires_at"`
	AcceptedAt       *time.Time `json:"accepted_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

func generateInviteToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) CreateInvitation(ctx context.Context, orgID, inviterID uint, email, role string, expiryDays int) (*OrganizationInvitation, error) {
	org, err := s.repo.GetByID(ctx, orgID)
	if err != nil || org == nil {
		return nil, ErrOrganizationNotFound
	}
	token, err := generateInviteToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}
	if expiryDays <= 0 {
		expiryDays = 7
	}
	inv := &OrganizationInvitation{
		OrganizationID: orgID,
		InviterID:      inviterID,
		Email:          email,
		Token:          token,
		Role:           role,
		Status:         InvitationPending,
		ExpiresAt:      time.Now().UTC().AddDate(0, 0, expiryDays),
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	if err := s.repo.CreateInvitation(ctx, inv); err != nil {
		return nil, err
	}
	return inv, nil
}

func (s *Service) AcceptInvitation(ctx context.Context, token string, userID uint) error {
	inv, err := s.repo.GetInvitationByToken(ctx, token)
	if err != nil {
		return ErrInvitationNotFound
	}
	if inv.Status == InvitationAccepted {
		return ErrInvitationAlreadyUsed
	}
	if inv.Status == InvitationRevoked {
		return ErrInvitationRevoked
	}
	if time.Now().UTC().After(inv.ExpiresAt) {
		s.repo.SetInvitationStatus(ctx, inv.ID, InvitationExpired)
		return ErrInvitationExpired
	}
	now := time.Now().UTC()
	return s.repo.AcceptInvitation(ctx, inv.ID, userID, now)
}

func (s *Service) RevokeInvitation(ctx context.Context, invID, orgID uint) error {
	inv, err := s.repo.GetInvitationByID(ctx, invID)
	if err != nil || inv == nil || inv.OrganizationID != orgID {
		return ErrInvitationNotFound
	}
	if inv.Status == InvitationAccepted {
		return ErrInvitationAlreadyUsed
	}
	return s.repo.SetInvitationStatus(ctx, inv.ID, InvitationRevoked)
}

func (s *Service) ListInvitations(ctx context.Context, orgID uint) ([]OrganizationInvitation, error) {
	return s.repo.ListInvitations(ctx, orgID)
}

func (r *OrganizationRepo) CreateInvitation(ctx context.Context, inv *OrganizationInvitation) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO org_invitations (organization_id, inviter_id, email, token, role, status, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.OrganizationID, inv.InviterID, inv.Email, inv.Token, inv.Role, inv.Status, inv.ExpiresAt, inv.CreatedAt, inv.UpdatedAt)
	return err
}

func (r *OrganizationRepo) GetInvitationByToken(ctx context.Context, token string) (*OrganizationInvitation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, organization_id, inviter_id, email, token, role, status, expires_at, accepted_at, revoked_at, created_at, updated_at
		FROM org_invitations WHERE token = ?`, token)
	return scanInvitation(row)
}

func (r *OrganizationRepo) GetInvitationByID(ctx context.Context, id uint) (*OrganizationInvitation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, organization_id, inviter_id, email, token, role, status, expires_at, accepted_at, revoked_at, created_at, updated_at
		FROM org_invitations WHERE id = ?`, id)
	return scanInvitation(row)
}

func (r *OrganizationRepo) SetInvitationStatus(ctx context.Context, id uint, status InvitationStatus) error {
	now := time.Now().UTC()
	var revokedAt *time.Time
	if status == InvitationRevoked {
		revokedAt = &now
	}
	_, err := r.db.ExecContext(ctx,
		"UPDATE org_invitations SET status=?, revoked_at=?, updated_at=? WHERE id=?",
		status, revokedAt, now, id)
	return err
}

func (r *OrganizationRepo) AcceptInvitation(ctx context.Context, id, userID uint, acceptedAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE org_invitations SET status=?, accepted_at=?, updated_at=? WHERE id=?",
		InvitationAccepted, acceptedAt, acceptedAt, id)
	return err
}

func (r *OrganizationRepo) ListInvitations(ctx context.Context, orgID uint) ([]OrganizationInvitation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, organization_id, inviter_id, email, token, role, status, expires_at, accepted_at, revoked_at, created_at, updated_at
		FROM org_invitations WHERE organization_id = ? ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invs []OrganizationInvitation
	for rows.Next() {
		inv, err := scanInvitation(rows)
		if err != nil {
			return nil, err
		}
		invs = append(invs, *inv)
	}
	return invs, rows.Err()
}

func scanInvitation(s interface{ Scan(dest ...interface{}) error }) (*OrganizationInvitation, error) {
	var inv OrganizationInvitation
	err := s.Scan(&inv.ID, &inv.OrganizationID, &inv.InviterID, &inv.Email, &inv.Token, &inv.Role, &inv.Status, &inv.ExpiresAt, &inv.AcceptedAt, &inv.RevokedAt, &inv.CreatedAt, &inv.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &inv, err
}
