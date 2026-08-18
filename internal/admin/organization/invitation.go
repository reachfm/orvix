package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
	TokenHash      string           `json:"-"`
	Role           string           `json:"role"`
	Status         InvitationStatus `json:"status"`
	ExpiresAt      time.Time        `json:"expires_at"`
	AcceptedAt     *time.Time       `json:"accepted_at,omitempty"`
	RevokedAt      *time.Time       `json:"revoked_at,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

func generateInviteToken() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(h[:]), nil
}

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// newInvitationToken builds a pending invitation for the given
// organization/role with a freshly generated one-time token. The
// role must already be validated by the caller (isValidOrgMemberRole).
// expiryDays <= 0 falls back to 7 days.
func newInvitationToken(orgID uint, email, role string, expiryDays int) (*OrganizationInvitation, string, error) {
	if !isValidOrgMemberRole(role) {
		return nil, "", fmt.Errorf("invalid organization member role: %s", role)
	}
	rawToken, tokenHash, err := generateInviteToken()
	if err != nil {
		return nil, "", fmt.Errorf("generate token: %w", err)
	}
	if expiryDays <= 0 {
		expiryDays = 7
	}
	now := time.Now().UTC()
	inv := &OrganizationInvitation{
		OrganizationID: orgID,
		Email:          email,
		TokenHash:      tokenHash,
		Role:           role,
		Status:         InvitationPending,
		ExpiresAt:      now.AddDate(0, 0, expiryDays),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	return inv, rawToken, nil
}

func (s *Service) CreateInvitation(ctx context.Context, orgID, inviterID uint, email, role string, expiryDays int) (*OrganizationInvitation, string, error) {
	org, err := s.repo.GetByID(ctx, orgID)
	if err != nil || org == nil {
		return nil, "", ErrOrganizationNotFound
	}
	// The invited member's role must be a canonical tenant administration
	// role. RoleUser ("user" — a per-mailbox webmail end-user) and every
	// other non-administrative or platform role is rejected: an invitation
	// must never mint an identity that either cannot administer anything
	// or that carries a role the member-role policy does not allow
	// (UpdateMemberRole uses the same allowlist via isValidOrgMemberRole).
	inv, rawToken, err := newInvitationToken(orgID, email, role, expiryDays)
	if err != nil {
		return nil, "", err
	}
	inv.InviterID = inviterID
	if err := s.repo.CreateInvitation(ctx, inv); err != nil {
		return nil, "", err
	}
	return inv, rawToken, nil
}

func (s *Service) AcceptInvitation(ctx context.Context, rawToken string, userID uint) error {
	if len(rawToken) == 0 || len(rawToken) > 128 {
		return ErrInvitationNotFound
	}
	tokenHash := hashToken(rawToken)
	inv, err := s.repo.GetInvitationByHash(ctx, tokenHash)
	if err != nil || inv == nil {
		return ErrInvitationNotFound
	}
	if subtle.ConstantTimeCompare([]byte(inv.TokenHash), []byte(tokenHash)) != 1 {
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

func (s *Service) RotateInvitationToken(ctx context.Context, invID, orgID uint) (*OrganizationInvitation, string, error) {
	inv, err := s.repo.GetInvitationByID(ctx, invID)
	if err != nil || inv == nil || inv.OrganizationID != orgID {
		return nil, "", ErrInvitationNotFound
	}
	if inv.Status != InvitationPending {
		return nil, "", fmt.Errorf("cannot rotate token: invitation status is %s", inv.Status)
	}
	rawToken, tokenHash, err := generateInviteToken()
	if err != nil {
		return nil, "", err
	}
	if err := s.repo.RotateInvitationToken(ctx, inv.ID, tokenHash); err != nil {
		return nil, "", err
	}
	inv.TokenHash = tokenHash
	return inv, rawToken, nil
}

func (r *OrganizationRepo) CreateInvitation(ctx context.Context, inv *OrganizationInvitation) error {
	if r.dialect.IsPostgres() {
		if err := r.db.QueryRowContext(ctx,
			`INSERT INTO org_invitations (organization_id, inviter_id, email, token_hash, role, status, expires_at, created_at, updated_at)
			VALUES (`+r.dialect.Placeholders(9)+`) RETURNING id`,
			inv.OrganizationID, inv.InviterID, inv.Email, inv.TokenHash, inv.Role, inv.Status, inv.ExpiresAt, inv.CreatedAt, inv.UpdatedAt,
		).Scan(&inv.ID); err != nil {
			return err
		}
		return nil
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO org_invitations (organization_id, inviter_id, email, token_hash, role, status, expires_at, created_at, updated_at)
		VALUES (`+r.dialect.Placeholders(9)+`)`,
		inv.OrganizationID, inv.InviterID, inv.Email, inv.TokenHash, inv.Role, inv.Status, inv.ExpiresAt, inv.CreatedAt, inv.UpdatedAt)
	if err != nil {
		return err
	}
	if id, idErr := res.LastInsertId(); idErr == nil {
		inv.ID = uint(id)
	}
	return nil
}

func (r *OrganizationRepo) GetInvitationByHash(ctx context.Context, tokenHash string) (*OrganizationInvitation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, organization_id, inviter_id, email, token_hash, role, status, expires_at, accepted_at, revoked_at, created_at, updated_at
		FROM org_invitations WHERE token_hash = `+r.dialect.Placeholder(1), tokenHash)
	return scanInvitation(row)
}

func (r *OrganizationRepo) GetInvitationByID(ctx context.Context, id uint) (*OrganizationInvitation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, organization_id, inviter_id, email, token_hash, role, status, expires_at, accepted_at, revoked_at, created_at, updated_at
		FROM org_invitations WHERE id = `+r.dialect.Placeholder(1), id)
	return scanInvitation(row)
}

func (r *OrganizationRepo) SetInvitationStatus(ctx context.Context, id uint, status InvitationStatus) error {
	now := time.Now().UTC()
	var revokedAt *time.Time
	if status == InvitationRevoked {
		revokedAt = &now
	}
	_, err := r.db.ExecContext(ctx,
		"UPDATE org_invitations SET status="+r.dialect.Placeholder(1)+", revoked_at="+r.dialect.Placeholder(2)+", updated_at="+r.dialect.Placeholder(3)+" WHERE id="+r.dialect.Placeholder(4),
		status, revokedAt, now, id)
	return err
}

func (r *OrganizationRepo) AcceptInvitation(ctx context.Context, id, userID uint, acceptedAt time.Time) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE org_invitations SET status="+r.dialect.Placeholder(1)+", accepted_at="+r.dialect.Placeholder(2)+", updated_at="+r.dialect.Placeholder(3)+" WHERE id="+r.dialect.Placeholder(4),
		InvitationAccepted, acceptedAt, acceptedAt, id)
	return err
}

func (r *OrganizationRepo) RotateInvitationToken(ctx context.Context, id uint, newTokenHash string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		"UPDATE org_invitations SET token_hash="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3),
		newTokenHash, now, id)
	return err
}

func (r *OrganizationRepo) ListInvitations(ctx context.Context, orgID uint) ([]OrganizationInvitation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, organization_id, inviter_id, email, token_hash, role, status, expires_at, accepted_at, revoked_at, created_at, updated_at
		FROM org_invitations WHERE organization_id = `+r.dialect.Placeholder(1)+` ORDER BY created_at DESC`, orgID)
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

func scanInvitation(s interface {
	Scan(dest ...interface{}) error
}) (*OrganizationInvitation, error) {
	var inv OrganizationInvitation
	if err := s.Scan(&inv.ID, &inv.OrganizationID, &inv.InviterID, &inv.Email, &inv.TokenHash, &inv.Role, &inv.Status, &inv.ExpiresAt, &inv.AcceptedAt, &inv.RevokedAt, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
		return nil, err
	}
	return &inv, nil
}
