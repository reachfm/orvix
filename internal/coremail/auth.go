package coremail

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
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

// VerifyPassword checks a password against an Argon2id or legacy bcrypt hash.
// Argon2id parameters are read from the encoded hash (m,t,p) with safety
// bounds so hashes are self-describing; legacy bcrypt hashes verify through
// golang.org/x/crypto/bcrypt.
func (s *AuthService) VerifyPassword(password, encodedHash string) bool {
	valid, _ := s.VerifyPasswordWithRehash(password, encodedHash)
	return valid
}

// VerifyPasswordWithRehash checks a password and reports whether the stored
// hash is a legacy bcrypt hash that should be upgraded to Argon2id
// (rehash-on-login). Argon2id hashes return needsRehash=false.
func (s *AuthService) VerifyPasswordWithRehash(password, encodedHash string) (valid bool, needsRehash bool) {
	if encodedHash == "" {
		return false, false
	}
	if strings.HasPrefix(encodedHash, "$2a$") || strings.HasPrefix(encodedHash, "$2b$") || strings.HasPrefix(encodedHash, "$2y$") {
		if bcrypt.CompareHashAndPassword([]byte(encodedHash), []byte(password)) == nil {
			return true, true
		}
		return false, false
	}
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false, false
	}

	params, err := parseArgon2Params(parts[3])
	if err != nil {
		return false, false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false, false
	}

	expectedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expectedHash) == 0 {
		return false, false
	}

	computedHash := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(expectedHash)))

	if len(computedHash) != len(expectedHash) {
		return false, false
	}
	if subtle.ConstantTimeCompare(computedHash, expectedHash) == 1 {
		return true, false
	}
	return false, false
}

// authenticateAndRehash verifies the password against the mailbox's stored
// hash and, on a successful legacy bcrypt match, transparently upgrades the
// stored hash to Argon2id (rehash-on-login). The upgrade is best-effort: a
// persistence failure does not fail the authentication, so mailboxes are
// never locked out by a failed migration.
func (s *AuthService) authenticateAndRehash(ctx context.Context, mbox *Mailbox, password string) bool {
	valid, needsRehash := s.VerifyPasswordWithRehash(password, mbox.PasswordHash)
	if !valid || !needsRehash {
		return valid
	}
	newHash, err := s.HashPassword(password)
	if err != nil {
		return true
	}
	updated := *mbox
	updated.PasswordHash = newHash
	updated.AuthScheme = AuthSchemeArgon2ID
	if err := s.repo.Update(ctx, &updated, nil); err != nil {
		return true
	}
	mbox.PasswordHash = newHash
	mbox.AuthScheme = AuthSchemeArgon2ID
	return true
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

	if !s.authenticateAndRehash(ctx, mbox, password) {
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

// VerifyMailboxPassword verifies a password against a mailbox's stored hash
// and performs best-effort rehash-on-login for legacy bcrypt hashes. It is
// the entry point for the SMTP AUTH identity path which already holds the
// mailbox record.
func (s *AuthService) VerifyMailboxPassword(ctx context.Context, mbox *Mailbox, password string) bool {
	if mbox == nil {
		return false
	}
	return s.authenticateAndRehash(ctx, mbox, password)
}

// argon2Params bounds parsed from a stored hash.
type argon2Params struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

// maxArgon2Memory / maxArgon2Iterations / maxArgon2Parallelism cap the
// parameters accepted from a stored hash to prevent attacker-controlled
// hashes from triggering resource exhaustion during verification.
const (
	maxArgon2Memory      = 256 * 1024 // KiB
	maxArgon2Iterations  = 10
	maxArgon2Parallelism = 8
)

// parseArgon2Params decodes "m=...,t=...,p=..." from an argon2id hash string.
func parseArgon2Params(encoded string) (argon2Params, error) {
	var p argon2Params
	for _, part := range strings.Split(encoded, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return p, fmt.Errorf("invalid argon2 parameter %q", part)
		}
		val, err := strconv.ParseUint(kv[1], 10, 32)
		if err != nil {
			return p, fmt.Errorf("invalid argon2 parameter value %q", kv[1])
		}
		switch kv[0] {
		case "m":
			p.memory = uint32(val)
		case "t":
			p.iterations = uint32(val)
		case "p":
			p.parallelism = uint8(val)
		default:
			return p, fmt.Errorf("unknown argon2 parameter %q", kv[0])
		}
	}
	if p.memory == 0 || p.iterations == 0 || p.parallelism == 0 {
		return p, fmt.Errorf("incomplete argon2 parameters")
	}
	if p.memory > maxArgon2Memory || p.iterations > maxArgon2Iterations || p.parallelism > maxArgon2Parallelism {
		return p, fmt.Errorf("argon2 parameters exceed safety bounds")
	}
	return p, nil
}

// GenerateAppPassword creates a random app-specific password.
func (s *AuthService) GenerateAppPassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "op-" + hex.EncodeToString(b), nil
}
