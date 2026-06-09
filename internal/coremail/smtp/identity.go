package smtp

import (
	"context"
	"fmt"

	"github.com/orvix/orvix/internal/coremail"
)

// IdentityService is the canonical identity layer that wraps CoreMail repositories
// and implements the AuthBackend interface for SMTP AUTH.
type IdentityService struct {
	engine *coremail.Engine
}

// NewIdentityService creates an identity service backed by the CoreMail engine.
func NewIdentityService(eng *coremail.Engine) *IdentityService {
	return &IdentityService{engine: eng}
}

// Compile-time interface check.
var _ AuthBackend = (*IdentityService)(nil)

// Authenticate verifies mailbox credentials and returns AuthIdentity.
func (s *IdentityService) Authenticate(ctx context.Context, username, password string) (*AuthIdentity, error) {
	mbox, err := s.engine.Mailboxes.GetByEmail(ctx, username, nil)
	if err != nil {
		return nil, fmt.Errorf("lookup: %w", err)
	}
	if mbox == nil {
		return nil, nil
	}
	if mbox.Status != coremail.MailboxActive {
		return nil, nil
	}

	if password != "" {
		if !s.engine.Auth.VerifyPassword(password, mbox.PasswordHash) {
			return nil, nil
		}
	}

	dom, err := s.engine.Domains.GetByID(ctx, mbox.DomainID, nil)
	if err != nil || dom == nil {
		return nil, fmt.Errorf("domain lookup: %w", err)
	}

	localPart, domain := splitEmailAddr(mbox.Email)
	return &AuthIdentity{
		Username:  mbox.Email,
		LocalPart: localPart,
		Domain:    domain,
		TenantID:  mbox.TenantID,
		MailboxID: mbox.ID,
		DomainID:  mbox.DomainID,
		IsAdmin:   mbox.IsAdmin,
	}, nil
}

// IsLocalDomain checks if a domain is hosted by this server.
func (s *IdentityService) IsLocalDomain(ctx context.Context, domain string) (bool, error) {
	d, err := s.engine.Domains.GetByName(ctx, domain, nil)
	if err != nil {
		return false, err
	}
	return d != nil && d.Status == coremail.DomainActive, nil
}

// ResolveSender checks if an identity is authorized to send as a given from address.
func (s *IdentityService) ResolveSender(ctx context.Context, identity *AuthIdentity, fromAddress string) (bool, error) {
	if identity == nil {
		return false, nil
	}
	// Own address is always allowed.
	if identity.Username == fromAddress {
		return true, nil
	}
	// Check aliases — if the fromAddress is an alias pointing to the identity, allow it.
	alias, err := s.engine.Aliases.GetByFromAddr(ctx, fromAddress, nil)
	if err != nil || alias == nil {
		return false, nil
	}
	return alias.ToAddr == identity.Username, nil
}

func splitEmailAddr(email string) (string, string) {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[:i], email[i+1:]
		}
	}
	return email, ""
}
