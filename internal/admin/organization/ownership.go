package organization

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

var (
	ErrTransferNotFound     = errors.New("ownership transfer not found")
	ErrTransferExpired      = errors.New("ownership transfer has expired")
	ErrTransferAlreadyUsed  = errors.New("ownership transfer already completed")
	ErrCannotTransferToSelf = errors.New("cannot transfer ownership to yourself")
	ErrTargetNotMember      = errors.New("target user is not a member of this organization")
	ErrNotCurrentOwner      = errors.New("only the current organization owner can initiate a transfer")
)

type TransferStatus string

const (
	TransferPending   TransferStatus = "pending"
	TransferAccepted  TransferStatus = "accepted"
	TransferExpired   TransferStatus = "expired"
	TransferCancelled TransferStatus = "cancelled"
)

type OwnershipTransfer struct {
	ID             uint           `json:"id"`
	OrganizationID uint           `json:"organization_id"`
	FromUserID     uint           `json:"from_user_id"`
	ToUserID       uint           `json:"to_user_id"`
	TokenHash      string         `json:"-"`
	Status         TransferStatus `json:"status"`
	ExpiresAt      time.Time      `json:"expires_at"`
	AcceptedAt     *time.Time     `json:"accepted_at,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

func generateTransferToken() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(h[:]), nil
}

func hashTransferToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

func (s *Service) RequestOwnershipTransfer(ctx context.Context, orgID, fromUserID, toUserID uint) (*OwnershipTransfer, string, error) {
	org, err := s.repo.GetByID(ctx, orgID)
	if err != nil || org == nil {
		return nil, "", ErrOrganizationNotFound
	}
	if fromUserID == toUserID {
		return nil, "", ErrCannotTransferToSelf
	}
	count, err := s.repo.CountAdmins(ctx, orgID)
	if err != nil {
		return nil, "", err
	}
	if count <= 1 && fromUserID != toUserID {
		return nil, "", ErrLastOwnerCannotTransfer
	}
	rawToken, tokenHash, err := generateTransferToken()
	if err != nil {
		return nil, "", fmt.Errorf("generate token: %w", err)
	}
	now := time.Now().UTC()
	t := &OwnershipTransfer{
		OrganizationID: orgID,
		FromUserID:     fromUserID,
		ToUserID:       toUserID,
		TokenHash:      tokenHash,
		Status:         TransferPending,
		ExpiresAt:      now.AddDate(0, 0, 7),
		CreatedAt:      now,
	}
	if err := s.repo.CreateOwnershipTransfer(ctx, t); err != nil {
		return nil, "", err
	}
	return t, rawToken, nil
}

func (s *Service) AcceptOwnershipTransfer(ctx context.Context, rawToken string, userID uint) error {
	tokenHash := hashTransferToken(rawToken)
	t, err := s.repo.GetOwnershipTransferByHash(ctx, tokenHash)
	if err != nil || t == nil {
		return ErrTransferNotFound
	}
	if t.Status == TransferAccepted {
		return ErrTransferAlreadyUsed
	}
	if t.Status == TransferCancelled {
		return ErrTransferNotFound
	}
	if time.Now().UTC().After(t.ExpiresAt) {
		s.repo.SetTransferStatus(ctx, t.ID, TransferExpired)
		return ErrTransferExpired
	}
	if t.ToUserID != userID {
		return ErrTransferNotFound
	}
	now := time.Now().UTC()
	return s.repo.AcceptOwnershipTransfer(ctx, t.ID, now)
}

func (s *Service) CancelOwnershipTransfer(ctx context.Context, transferID, orgID, userID uint) error {
	t, err := s.repo.GetOwnershipTransferByID(ctx, transferID)
	if err != nil || t == nil || t.OrganizationID != orgID || t.FromUserID != userID {
		return ErrTransferNotFound
	}
	if t.Status == TransferAccepted {
		return ErrTransferAlreadyUsed
	}
	return s.repo.SetTransferStatus(ctx, t.ID, TransferCancelled)
}

func (s *Service) ListOwnershipTransfers(ctx context.Context, orgID uint) ([]OwnershipTransfer, error) {
	return s.repo.ListOwnershipTransfers(ctx, orgID)
}

func (r *OrganizationRepo) CreateOwnershipTransfer(ctx context.Context, t *OwnershipTransfer) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO org_ownership_transfers (organization_id, from_user_id, to_user_id, token_hash, status, expires_at, created_at)
		VALUES (`+r.dialect.Placeholders(7)+`)`,
		t.OrganizationID, t.FromUserID, t.ToUserID, t.TokenHash, t.Status, t.ExpiresAt, t.CreatedAt)
	return err
}

func (r *OrganizationRepo) GetOwnershipTransferByHash(ctx context.Context, hash string) (*OwnershipTransfer, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, organization_id, from_user_id, to_user_id, token_hash, status, expires_at, accepted_at, created_at
		FROM org_ownership_transfers WHERE token_hash = `+r.dialect.Placeholder(1), hash)
	return scanTransfer(row)
}

func (r *OrganizationRepo) GetOwnershipTransferByID(ctx context.Context, id uint) (*OwnershipTransfer, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, organization_id, from_user_id, to_user_id, token_hash, status, expires_at, accepted_at, created_at
		FROM org_ownership_transfers WHERE id = `+r.dialect.Placeholder(1), id)
	return scanTransfer(row)
}

func (r *OrganizationRepo) SetTransferStatus(ctx context.Context, id uint, status TransferStatus) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		"UPDATE org_ownership_transfers SET status = "+r.dialect.Placeholder(1)+", updated_at = "+r.dialect.Placeholder(2)+" WHERE id = "+r.dialect.Placeholder(3), status, now, id)
	return err
}

func (r *OrganizationRepo) AcceptOwnershipTransfer(ctx context.Context, id uint, acceptedAt time.Time) error {
	tx, err := r.root.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := r.GetOwnershipTransferByID(ctx, id)
	if err != nil || t == nil {
		return ErrTransferNotFound
	}
	if _, err := tx.ExecContext(ctx, "UPDATE org_ownership_transfers SET status = "+r.dialect.Placeholder(1)+", accepted_at = "+r.dialect.Placeholder(2)+" WHERE id = "+r.dialect.Placeholder(3),
		TransferAccepted, acceptedAt, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET role = 'admin' WHERE id = "+r.dialect.Placeholder(1)+" AND tenant_id = "+r.dialect.Placeholder(2),
		t.FromUserID, t.OrganizationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET role = 'superadmin' WHERE id = "+r.dialect.Placeholder(1)+" AND tenant_id = "+r.dialect.Placeholder(2),
		t.ToUserID, t.OrganizationID); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *OrganizationRepo) ListOwnershipTransfers(ctx context.Context, orgID uint) ([]OwnershipTransfer, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, organization_id, from_user_id, to_user_id, token_hash, status, expires_at, accepted_at, created_at
		FROM org_ownership_transfers WHERE organization_id = `+r.dialect.Placeholder(1)+` ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ts []OwnershipTransfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, *t)
	}
	return ts, rows.Err()
}

func scanTransfer(s interface {
	Scan(dest ...interface{}) error
}) (*OwnershipTransfer, error) {
	var t OwnershipTransfer
	if err := s.Scan(&t.ID, &t.OrganizationID, &t.FromUserID, &t.ToUserID, &t.TokenHash, &t.Status, &t.ExpiresAt, &t.AcceptedAt, &t.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}
