package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvitationNotFound        = errors.New("invitation not found")
	ErrInvitationExpired         = errors.New("invitation has expired")
	ErrInvitationAlreadyUsed     = errors.New("invitation already accepted")
	ErrInvitationRevoked         = errors.New("invitation was revoked")
	ErrLastOwnerCannotTransfer   = errors.New("cannot transfer ownership: no remaining owner would exist")
	ErrPendingInvitationExists   = errors.New("a pending invitation already exists for this email")
	ErrEmailAlreadyInUse         = errors.New("an account with this email already exists")
	ErrOrganizationRequiresOwner = errors.New("organization must have an active administrator before it can be activated")
	ErrWeakPassword              = errors.New("password must be at least 8 characters")
)

// AcceptedInvitation is the result of redeeming an invitation: the new
// member identity and the organization's post-acceptance activation state.
type AcceptedInvitation struct {
	UserID             uint   `json:"user_id"`
	OrganizationID     uint   `json:"organization_id"`
	Email              string `json:"email"`
	Role               string `json:"role"`
	OrganizationActive bool   `json:"organization_active"`
}

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

// HashToken exposes the token-hash function for lookup helpers outside the
// package (e.g. the accept handler's pre-transaction platform-identity
// check). The same SHA-256 hex digest is what org_invitations.token_hash
// stores.
func HashToken(raw string) string { return hashToken(raw) }

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
	// A duplicate PENDING invitation for the same email is a stable
	// conflict, never a second concurrent invitation: two pending rows for
	// one address means two live tokens, and whichever the invitee redeems
	// first silently orphans the other — a confused, half-cancelled state
	// for the inviter and a support ticket. The inviter must revoke or
	// resend the existing pending invitation instead (resend rotates the
	// token, so the "share a fresh link" use case is fully covered).
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, "", fmt.Errorf("invitation email is required")
	}
	pending, err := s.repo.ExistsPendingInvitation(ctx, orgID, email)
	if err != nil {
		return nil, "", err
	}
	if pending {
		return nil, "", ErrPendingInvitationExists
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

// AcceptInvitation redeems a pending invitation by creating (or linking)
// the invited member's account and activating the organization. It is the
// ONLY path that transitions an organization from pending_activation to
// active: the org row, the new member user, the invitation state, and the
// audit records commit or roll back as one unit, and the invitation claim
// is atomic (WHERE status='pending'), so two concurrent redemptions cannot
// both win.
//
// Contract:
//   - The invitee's email comes from the invitation row, NEVER from the
//     request: the one-time token is the credential that authorizes this
//     exact address to join.
//   - The user is created with the invitation's role and tenant, active,
//     email_verified, and the caller-supplied password (bcrypt-hashed).
//   - An existing active user with the same email is a stable conflict
//     (users.email is UNIQUE) — the token cannot hijack an existing
//     account.
//   - A revoked, expired, or already-accepted invitation is rejected with
//     the specific typed error; a loser of the atomic claim sees
//     ErrInvitationAlreadyUsed.
//   - The organization is activated (tenants.active=1) unless a deliberate
//     suspension record exists (org_suspensions with reactivated_at IS
//     NULL) — a PSA suspension must not be silently overridden by an
//     invitation redemption.
func (s *Service) AcceptInvitation(ctx context.Context, rawToken, password string) (*AcceptedInvitation, error) {
	if len(rawToken) == 0 || len(rawToken) > 128 {
		return nil, ErrInvitationNotFound
	}
	tokenHash := hashToken(rawToken)
	inv, err := s.repo.GetInvitationByHash(ctx, tokenHash)
	if err != nil || inv == nil {
		return nil, ErrInvitationNotFound
	}
	if subtle.ConstantTimeCompare([]byte(inv.TokenHash), []byte(tokenHash)) != 1 {
		return nil, ErrInvitationNotFound
	}
	if inv.Status == InvitationAccepted {
		return nil, ErrInvitationAlreadyUsed
	}
	if inv.Status == InvitationRevoked {
		return nil, ErrInvitationRevoked
	}
	if time.Now().UTC().After(inv.ExpiresAt) {
		s.repo.SetInvitationStatus(ctx, inv.ID, InvitationExpired)
		return nil, ErrInvitationExpired
	}
	if len(password) < 8 {
		return nil, ErrWeakPassword
	}
	pwHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash invitation password: %w", err)
	}

	now := time.Now().UTC()
	org, err := s.repo.GetByID(ctx, inv.OrganizationID)
	if err != nil || org == nil {
		return nil, ErrOrganizationNotFound
	}

	// Atomic claim of the invitation. Only the request whose UPDATE affects
	// the still-pending row wins; the loser reports the stable
	// already-used conflict instead of double-activating.
	claimed, err := s.repo.ClaimInvitation(ctx, inv.ID)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, ErrInvitationAlreadyUsed
	}

	entry := &audit.ExtendedEntry{
		Action:   "invitation.accept",
		Actor:    fmt.Sprintf("email:%s", inv.Email),
		TenantID: inv.OrganizationID,
		Target:   fmt.Sprintf("tenant:%d", inv.OrganizationID),
		TargetID: inv.OrganizationID,
		Result:   "success",
		Reason:   fmt.Sprintf("invitation:%d role:%s", inv.ID, inv.Role),
	}
	return s.completeAccept(ctx, org, inv, pwHash, now, entry)
}

// completeAccept performs the user creation + org activation + audit commit
// inside one transaction (the invitation was already claimed atomically
// above; a failure here rolls back the user/activation but the claim stays
// accepted — the caller's response must reflect whatever actually
// committed).
func (s *Service) completeAccept(ctx context.Context, org *Organization, inv *OrganizationInvitation, pwHash []byte, now time.Time, entry *audit.ExtendedEntry) (*AcceptedInvitation, error) {
	var result AcceptedInvitation
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin invitation accept: %w", err)
	}
	defer tx.Rollback()
	repo := s.repo.WithTx(tx)

	userID, err := repo.CreateUser(ctx, inv.Email, string(pwHash), inv.Role, inv.OrganizationID)
	if err != nil {
		if kernel.IsUniqueViolation(err) {
			return nil, ErrEmailAlreadyInUse
		}
		return nil, err
	}

	// Activate the organization unless a deliberate suspension record is
	// open. A PSA-created organization starts pending_activation
	// (active=0); this is the step that makes it operational.
	if !org.Active {
		suspended, err := repo.HasOpenSuspension(ctx, org.ID)
		if err != nil {
			return nil, err
		}
		if !suspended {
			if err := repo.SetActiveTx(ctx, org.ID, true); err != nil {
				return nil, err
			}
			org.Active = true
		}
	}

	entry.ActorID = userID
	entry.ActorRole = inv.Role
	if s.auditStore != nil {
		if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	result = AcceptedInvitation{
		UserID:             userID,
		OrganizationID:     inv.OrganizationID,
		Email:              inv.Email,
		Role:               inv.Role,
		OrganizationActive: org.Active,
	}
	return &result, nil
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

// ExistsPendingInvitation reports whether orgID has a still-pending
// invitation for the given (already normalized) email — the guard behind
// the duplicate-pending-invitation conflict.
func (r *OrganizationRepo) ExistsPendingInvitation(ctx context.Context, orgID uint, email string) (bool, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM org_invitations WHERE organization_id="+r.dialect.Placeholder(1)+" AND email="+r.dialect.Placeholder(2)+" AND status='pending'",
		orgID, email).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// HasPendingOwnerInvitation reports whether orgID currently has a pending
// tenant_admin invitation — the pending_activation lifecycle signal for a
// PSA-created organization that has not yet been activated by its owner.
func (r *OrganizationRepo) HasPendingOwnerInvitation(ctx context.Context, orgID uint) (bool, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM org_invitations WHERE organization_id="+r.dialect.Placeholder(1)+" AND role='tenant_admin' AND status='pending'",
		orgID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ClaimInvitation atomically transitions a pending invitation to
// accepted. Returns claimed=false when the row is no longer pending (an
// earlier request already redeemed it, or it was revoked/expired in
// between) — the caller must treat that as ErrInvitationAlreadyUsed, never
// as a silent no-op.
func (r *OrganizationRepo) ClaimInvitation(ctx context.Context, id uint) (bool, error) {
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx,
		"UPDATE org_invitations SET status="+r.dialect.Placeholder(1)+", accepted_at="+r.dialect.Placeholder(2)+", updated_at="+r.dialect.Placeholder(3)+" WHERE id="+r.dialect.Placeholder(4)+" AND status='pending'",
		InvitationAccepted, now, now, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CreateUser inserts a new user row for an accepted invitation within the
// caller's transaction. Dialect-aware (PostgreSQL RETURNING id).
func (r *OrganizationRepo) CreateUser(ctx context.Context, email, passwordHash, role string, tenantID uint) (uint, error) {
	now := time.Now().UTC()
	if r.dialect.IsPostgres() {
		var id uint
		err := r.db.QueryRowContext(ctx,
			`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
			VALUES (`+r.dialect.Placeholders(8)+`) RETURNING id`,
			now, now, email, passwordHash, role, tenantID, true, true).Scan(&id)
		return id, err
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO users (created_at, updated_at, email, password_hash, role, tenant_id, active, email_verified)
		VALUES (`+r.dialect.Placeholders(8)+`)`,
		now, now, email, passwordHash, role, tenantID, true, true)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}

// HasOpenSuspension reports whether orgID currently has a deliberate
// suspension record (reactivated_at IS NULL). Invitation acceptance must
// not silently override an operator's explicit suspension.
func (r *OrganizationRepo) HasOpenSuspension(ctx context.Context, orgID uint) (bool, error) {
	var count int64
	err := r.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM org_suspensions WHERE organization_id="+r.dialect.Placeholder(1)+" AND reactivated_at IS NULL",
		orgID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// SetActiveTx sets tenants.active within the caller's transaction.
func (r *OrganizationRepo) SetActiveTx(ctx context.Context, id uint, active bool) error {
	val := 0
	if active {
		val = 1
	}
	_, err := r.db.ExecContext(ctx,
		"UPDATE tenants SET active="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND deleted_at IS NULL",
		val, time.Now().UTC(), id)
	return err
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
