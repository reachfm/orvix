package mailboxmgmt

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/orvix/orvix/internal/coremail"
)

// MailboxLimitChecker checks license limits before mailbox creation.
type MailboxLimitChecker interface {
	CanCreateMailbox(ctx context.Context) (bool, string)
}

var validLocalRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._%+-]{0,63}$`)

// Service manages mailboxes using CoreMail engine.
type Service struct {
	engine  *coremail.Engine
	limiter MailboxLimitChecker
}

// NewService creates a mailbox management service backed by CoreMail.
func NewService(engine *coremail.Engine) *Service {
	return &Service{engine: engine}
}

// SetLimitChecker attaches a license limit checker.
func (s *Service) SetLimitChecker(lc MailboxLimitChecker) {
	s.limiter = lc
}

func mailboxStatusFromCore(s coremail.MailboxStatus) MailboxStatus {
	switch s {
	case coremail.MailboxActive:
		return MailboxActive
	case coremail.MailboxSuspended:
		return MailboxSuspended
	default:
		return MailboxDisabled
	}
}

func mailboxToAdmin(m *coremail.Mailbox) *Mailbox {
	return &Mailbox{
		ID:        m.ID,
		Email:     m.Email,
		LocalPart: m.LocalPart,
		Name:      m.Name,
		Status:    mailboxStatusFromCore(m.Status),
		QuotaMB:   m.QuotaMB,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func (s *Service) CreateMailbox(ctx context.Context, req *CreateMailboxRequest) (*Mailbox, error) {
	if req == nil {
		return nil, fmt.Errorf("request cannot be nil")
	}

	// Check license limit.
	if s.limiter != nil {
		if ok, msg := s.limiter.CanCreateMailbox(ctx); !ok {
			return nil, fmt.Errorf("license limit: %s", msg)
		}
	}

	domain, err := s.engine.Domains.GetByID(ctx, req.DomainID, nil)
	if err != nil {
		return nil, fmt.Errorf("domain lookup: %w", err)
	}
	if domain == nil {
		return nil, fmt.Errorf("domain not found")
	}
	if domain.Status != coremail.DomainActive {
		return nil, fmt.Errorf("domain is not active")
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("invalid email format")
	}
	localPart := parts[0]
	if !validLocalRE.MatchString(localPart) {
		return nil, fmt.Errorf("invalid local part")
	}
	if len(localPart) > 64 {
		return nil, fmt.Errorf("local part too long (max 64 chars)")
	}
	if parts[1] != domain.Name {
		return nil, fmt.Errorf("email domain must match the selected domain")
	}

	exists, err := s.engine.Mailboxes.Exists(ctx, email, nil)
	if err != nil {
		return nil, fmt.Errorf("check existence: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("mailbox already exists: %s", email)
	}

	if len(req.Password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	if req.QuotaMB < 0 {
		return nil, fmt.Errorf("quota cannot be negative")
	}

	hash, err := s.engine.Auth.HashPassword(req.Password)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	mbox := &coremail.Mailbox{
		DomainID:     req.DomainID,
		LocalPart:    localPart,
		Email:        email,
		Name:         req.Name,
		PasswordHash: hash,
		AuthScheme:   coremail.AuthSchemeArgon2ID,
		Status:       coremail.MailboxActive,
		QuotaMB:      req.QuotaMB,
	}
	if err := s.engine.Mailboxes.Create(ctx, mbox, nil); err != nil {
		return nil, fmt.Errorf("create mailbox: %w", err)
	}
	return mailboxToAdmin(mbox), nil
}

func (s *Service) ListMailboxes(ctx context.Context, domainID *uint) ([]Mailbox, error) {
	filter := coremail.MailboxFilter{}
	if domainID != nil {
		filter.DomainID = domainID
	}
	mboxes, _, err := s.engine.Mailboxes.List(ctx, filter, nil)
	if err != nil {
		return nil, err
	}
	result := make([]Mailbox, len(mboxes))
	for i, m := range mboxes {
		result[i] = *mailboxToAdmin(&m)
	}
	return result, nil
}

func (s *Service) GetMailbox(ctx context.Context, id uint) (*Mailbox, error) {
	m, err := s.engine.Mailboxes.GetByID(ctx, id, nil)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, nil
	}
	return mailboxToAdmin(m), nil
}

func (s *Service) UpdateMailbox(ctx context.Context, id uint, req *UpdateMailboxRequest) (*Mailbox, error) {
	m, err := s.engine.Mailboxes.GetByID(ctx, id, nil)
	if err != nil {
		return nil, fmt.Errorf("get mailbox: %w", err)
	}
	if m == nil {
		return nil, fmt.Errorf("mailbox not found")
	}
	if req.Name != nil {
		m.Name = *req.Name
	}
	if req.Status != nil {
		switch *req.Status {
		case MailboxActive:
			m.Status = coremail.MailboxActive
		case MailboxSuspended:
			m.Status = coremail.MailboxSuspended
		default:
			return nil, fmt.Errorf("invalid status: %s", *req.Status)
		}
	}
	if req.QuotaMB != nil {
		if *req.QuotaMB < 0 {
			return nil, fmt.Errorf("quota cannot be negative")
		}
		m.QuotaMB = *req.QuotaMB
	}
	if err := s.engine.Mailboxes.Update(ctx, m, nil); err != nil {
		return nil, fmt.Errorf("update mailbox: %w", err)
	}
	return mailboxToAdmin(m), nil
}

func (s *Service) ResetPassword(ctx context.Context, id uint, newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	m, err := s.engine.Mailboxes.GetByID(ctx, id, nil)
	if err != nil {
		return fmt.Errorf("get mailbox: %w", err)
	}
	if m == nil {
		return fmt.Errorf("mailbox not found")
	}
	hash, err := s.engine.Auth.HashPassword(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	m.PasswordHash = hash
	return s.engine.Mailboxes.Update(ctx, m, nil)
}

func (s *Service) SuspendMailbox(ctx context.Context, id uint) (*Mailbox, error) {
	return s.UpdateMailbox(ctx, id, &UpdateMailboxRequest{Status: mboxPtr(MailboxSuspended)})
}

func (s *Service) ActivateMailbox(ctx context.Context, id uint) (*Mailbox, error) {
	return s.UpdateMailbox(ctx, id, &UpdateMailboxRequest{Status: mboxPtr(MailboxActive)})
}

func (s *Service) DeleteMailbox(ctx context.Context, id uint) error {
	m, err := s.engine.Mailboxes.GetByID(ctx, id, nil)
	if err != nil {
		return fmt.Errorf("get mailbox: %w", err)
	}
	if m == nil {
		return fmt.Errorf("mailbox not found")
	}
	return s.engine.Mailboxes.Delete(ctx, id, nil)
}

func mboxPtr(s MailboxStatus) *MailboxStatus { return &s }
