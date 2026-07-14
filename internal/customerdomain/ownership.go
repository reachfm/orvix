package customerdomain

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
	ErrOwnershipAlreadyVerified  = errors.New("domain ownership already verified")
	ErrOwnershipNoTokenGenerated = errors.New("no ownership token generated; generate one first")
	ErrOwnershipTokenInvalid     = errors.New("TXT record not found or does not match the expected token")
)

type OwnershipStatus string

const (
	OwnershipPending  OwnershipStatus = "pending"
	OwnershipVerified OwnershipStatus = "verified"
)

type DomainOwnership struct {
	ID               uint            `json:"id"`
	DomainID         uint            `json:"domain_id"`
	TokenHash        string          `json:"-"`
	Status           OwnershipStatus `json:"status"`
	TokenGeneratedAt time.Time       `json:"token_generated_at"`
	TokenRotatedAt   *time.Time      `json:"token_rotated_at,omitempty"`
	VerifiedAt       *time.Time      `json:"verified_at,omitempty"`
	LastCheckAt      *time.Time      `json:"last_check_at,omitempty"`
	LastError        string          `json:"last_error,omitempty"`
	CheckCount       int             `json:"check_count"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type OwnershipVerificationResult struct {
	Status   OwnershipStatus `json:"status"`
	Token    string          `json:"token,omitempty"`
	Hostname string          `json:"hostname"`
	Expected string          `json:"expected_value,omitempty"`
	Error    string          `json:"error,omitempty"`
}

func generateOwnershipToken() (raw string, hash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	raw = "orvix-verify=" + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(h[:]), nil
}

type OwnershipRepo struct {
	db *sql.DB
}

func NewOwnershipRepo(db *sql.DB) *OwnershipRepo {
	return &OwnershipRepo{db: db}
}

func (r *OwnershipRepo) GetByDomain(ctx context.Context, domainID uint) (*DomainOwnership, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, domain_id, token_hash, status, token_generated_at, token_rotated_at,
		verified_at, last_check_at, last_error, check_count, created_at, updated_at
		FROM domain_ownership WHERE domain_id = ?`, domainID)
	return scanOwnership(row)
}

func (r *OwnershipRepo) Upsert(ctx context.Context, rec *DomainOwnership) error {
	if rec.ID > 0 {
		_, err := r.db.ExecContext(ctx,
			`UPDATE domain_ownership SET token_hash=?, status=?, token_generated_at=?,
			token_rotated_at=?, verified_at=?, last_check_at=?, last_error=?, check_count=?, updated_at=?
			WHERE id=?`,
			rec.TokenHash, rec.Status, rec.TokenGeneratedAt,
			rec.TokenRotatedAt, rec.VerifiedAt, rec.LastCheckAt, rec.LastError, rec.CheckCount, rec.UpdatedAt,
			rec.ID)
		return err
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO domain_ownership (domain_id, token_hash, status, token_generated_at,
		token_rotated_at, verified_at, last_check_at, last_error, check_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.DomainID, rec.TokenHash, rec.Status, rec.TokenGeneratedAt,
		rec.TokenRotatedAt, rec.VerifiedAt, rec.LastCheckAt, rec.LastError, rec.CheckCount, rec.CreatedAt, rec.UpdatedAt)
	return err
}

func scanOwnership(s interface {
	Scan(dest ...interface{}) error
}) (*DomainOwnership, error) {
	var o DomainOwnership
	err := s.Scan(&o.ID, &o.DomainID, &o.TokenHash, &o.Status,
		&o.TokenGeneratedAt, &o.TokenRotatedAt, &o.VerifiedAt, &o.LastCheckAt,
		&o.LastError, &o.CheckCount, &o.CreatedAt, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &o, err
}

func (s *Service) GenerateOwnershipToken(ctx context.Context, domainID, tenantID uint) (*OwnershipVerificationResult, error) {
	domain, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return nil, ErrDomainNotFound
	}
	if domain.TenantID != tenantID {
		return nil, ErrDomainNotFound
	}

	orepo := NewOwnershipRepo(s.db)
	existing, _ := orepo.GetByDomain(ctx, domainID)
	if existing != nil && existing.Status == OwnershipVerified {
		return nil, ErrOwnershipAlreadyVerified
	}

	rawToken, tokenHash, err := generateOwnershipToken()
	if err != nil {
		return nil, fmt.Errorf("generate token: %w", err)
	}

	now := time.Now().UTC()
	rec := &DomainOwnership{
		DomainID:         domainID,
		TokenHash:        tokenHash,
		Status:           OwnershipPending,
		TokenGeneratedAt: now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if existing != nil {
		rec.ID = existing.ID
		rec.TokenRotatedAt = &now
		rec.CheckCount = existing.CheckCount
	}

	if err := orepo.Upsert(ctx, rec); err != nil {
		return nil, err
	}

	return &OwnershipVerificationResult{
		Status:   OwnershipPending,
		Token:    rawToken,
		Hostname: "_orvix-verify." + domain.Name,
		Expected: rawToken,
	}, nil
}

func (s *Service) VerifyOwnershipTXT(ctx context.Context, domainID, tenantID uint, txtRecords []string) (*OwnershipVerificationResult, error) {
	domain, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return nil, ErrDomainNotFound
	}
	if domain.TenantID != tenantID {
		return nil, ErrDomainNotFound
	}

	orepo := NewOwnershipRepo(s.db)
	rec, err := orepo.GetByDomain(ctx, domainID)
	if err != nil || rec == nil {
		return nil, ErrOwnershipNoTokenGenerated
	}

	now := time.Now().UTC()
	rec.LastCheckAt = &now
	rec.CheckCount++
	rec.UpdatedAt = now

	for _, txt := range txtRecords {
		h := sha256.Sum256([]byte(txt))
		if hex.EncodeToString(h[:]) == rec.TokenHash {
			rec.Status = OwnershipVerified
			rec.VerifiedAt = &now
			rec.LastError = ""
			orepo.Upsert(ctx, rec)
			return &OwnershipVerificationResult{
				Status:   OwnershipVerified,
				Hostname: "_orvix-verify." + domain.Name,
			}, nil
		}
	}

	rec.Status = OwnershipPending
	rec.LastError = "TXT record does not match expected token"
	orepo.Upsert(ctx, rec)
	return &OwnershipVerificationResult{
		Status: OwnershipPending,
		Error:  ErrOwnershipTokenInvalid.Error(),
	}, nil
}

func (s *Service) GetOwnershipStatus(ctx context.Context, domainID, tenantID uint) (*OwnershipVerificationResult, error) {
	domain, err := s.domains.GetByID(ctx, domainID, nil)
	if err != nil {
		return nil, ErrDomainNotFound
	}
	if domain.TenantID != tenantID {
		return nil, ErrDomainNotFound
	}
	orepo := NewOwnershipRepo(s.db)
	rec, err := orepo.GetByDomain(ctx, domainID)
	if err != nil || rec == nil {
		return nil, ErrOwnershipNoTokenGenerated
	}
	return &OwnershipVerificationResult{
		Status:   rec.Status,
		Hostname: "_orvix-verify." + domain.Name,
	}, nil
}
