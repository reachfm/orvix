package coremail

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// AuthService handles password hashing, verification, and mailbox authentication.
type AuthService struct {
	repo        MailboxRepository
	domainRepo  DomainRepository
	aliasRepo   AliasRepository
	argon2Time  uint32
	argon2Mem   uint32
	argon2Thrds uint8
	argon2KeyL  uint32
}

// AuthConfig holds configuration for the authentication service.
type AuthConfig struct {
	Argon2Time    uint32 // number of passes (iterations)
	Argon2Memory  uint32 // KiB of memory
	Argon2Threads uint8  // number of threads/cores
	Argon2KeyLen  uint32 // output key length in bytes
}

func DefaultAuthConfig() AuthConfig {
	return AuthConfig{
		Argon2Time:    3,
		Argon2Memory:  64 * 1024,
		Argon2Threads: 4,
		Argon2KeyLen:  32,
	}
}

func NewAuthService(repo MailboxRepository, domainRepo DomainRepository, aliasRepo AliasRepository, cfg AuthConfig) *AuthService {
	return &AuthService{
		repo:        repo,
		domainRepo:  domainRepo,
		aliasRepo:   aliasRepo,
		argon2Time:  cfg.Argon2Time,
		argon2Mem:   cfg.Argon2Memory,
		argon2Thrds: cfg.Argon2Threads,
		argon2KeyL:  cfg.Argon2KeyLen,
	}
}

// HashPassword creates an Argon2id password hash with a random salt.
// Format: $argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>
func (s *AuthService) HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, s.argon2Time, s.argon2Mem, s.argon2Thrds, s.argon2KeyL)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		s.argon2Mem, s.argon2Time, s.argon2Thrds, b64Salt, b64Hash), nil
}

// VerifyPassword checks a password against an Argon2id hash.
func (s *AuthService) VerifyPassword(password, encodedHash string) bool {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	computedHash := argon2.IDKey([]byte(password), salt, s.argon2Time, s.argon2Mem, s.argon2Thrds, s.argon2KeyL)

	if len(computedHash) != len(expectedHash) {
		return false
	}

	return hmac.Equal(computedHash, expectedHash)
}

// AuthenticateMailbox verifies credentials and returns the mailbox if valid.
// Checks: active mailbox, correct password, active domain, domain not suspended.
func (s *AuthService) AuthenticateMailbox(ctx context.Context, email, password string) (*Mailbox, error) {
	mbox, err := s.repo.GetByEmail(ctx, email, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if mbox == nil {
		return nil, fmt.Errorf("auth: mailbox not found")
	}
	if mbox.Status != MailboxActive {
		return nil, fmt.Errorf("auth: mailbox %s", string(mbox.Status))
	}

	if !s.VerifyPassword(password, mbox.PasswordHash) {
		return nil, fmt.Errorf("auth: invalid credentials")
	}

	domain, err := s.domainRepo.GetByID(ctx, mbox.DomainID, nil)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	if domain == nil || domain.Status != DomainActive {
		return nil, fmt.Errorf("auth: domain %s", string(DomainSuspended))
	}

	return mbox, nil
}

// ResolveAddress checks if an email address is a valid local mailbox,
// an alias, or a forwarder, returning the final delivery target(s).
func (s *AuthService) ResolveAddress(ctx context.Context, email string) ([]string, error) {
	// Check direct mailbox.
	mbox, err := s.repo.GetByEmail(ctx, email, nil)
	if err == nil && mbox != nil && mbox.Status == MailboxActive {
		if mbox.IsForwarder && mbox.ForwardTo != "" {
			return strings.Split(mbox.ForwardTo, ","), nil
		}
		return []string{email}, nil
	}

	// Check alias.
	alias, err := s.aliasRepo.GetByFromAddr(ctx, email, nil)
	if err == nil && alias != nil && alias.Active {
		return strings.Split(alias.ToAddr, ","), nil
	}

	// Check domain catchall.
	parts := strings.SplitN(email, "@", 2)
	if len(parts) == 2 {
		domain, err := s.domainRepo.GetByName(ctx, parts[1], nil)
		if err == nil && domain != nil && domain.CatchallAddress != "" {
			return []string{domain.CatchallAddress}, nil
		}
	}

	return nil, fmt.Errorf("address not found: %s", email)
}

// GenerateAppPassword creates a random app-specific password.
func (s *AuthService) GenerateAppPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "op-" + hex.EncodeToString(b), nil
}
