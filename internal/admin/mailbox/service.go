package mailbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/orvix/orvix/internal/audit"
	entrbac "github.com/orvix/orvix/internal/enterprise/rbac"
)

type Service struct {
	repo       *AdminMailboxRepo
	auditStore *audit.ExtendedStore
	rbac       *entrbac.Evaluator
}

func NewService(repo *AdminMailboxRepo, auditStore *audit.ExtendedStore, rbac *entrbac.Evaluator) *Service {
	return &Service{repo: repo, auditStore: auditStore, rbac: rbac}
}

func (s *Service) ListMailboxes(ctx context.Context, filter MailboxFilter) ([]AdminMailbox, int64, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) GetMailbox(ctx context.Context, id, tenantID uint) (*AdminMailbox, error) {
	m, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrMailboxNotFound
	}
	return m, nil
}

func (s *Service) CreateMailbox(ctx context.Context, req CreateMailboxRequest, tenantID, domainID uint) (*CreateMailboxResponse, error) {
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return nil, ErrInvalidEmail
	}
	if req.Password == "" {
		return nil, ErrPasswordRequired
	}

	parts := strings.SplitN(req.Email, "@", 2)
	localPart := parts[0]

	exists, err := s.repo.ExistsByEmail(ctx, req.Email, 0)
	if err != nil {
		return nil, fmt.Errorf("check exists: %w", err)
	}
	if exists {
		return nil, ErrMailboxExists
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	quota := req.QuotaMB
	if quota <= 0 {
		quota = 1024
	}
	sendLimit := req.SendLimit
	if sendLimit <= 0 {
		sendLimit = 500
	}

	m := &AdminMailbox{
		DomainID:  domainID,
		TenantID:  tenantID,
		Email:     req.Email,
		LocalPart: localPart,
		Name:      req.Name,
		Status:    AdminMailboxActive,
		QuotaMB:   quota,
		SendLimit: sendLimit,
		AllowSMTP: true,
		AllowIMAP: true,
		AllowPOP3: true,
		AllowJMAP:  true,
	}

	created, err := s.repo.Create(ctx, m, string(passwordHash))
	if err != nil {
		return nil, err
	}

	s.recordAudit(ctx, "mailbox.create", fmt.Sprintf("mailbox:%d", created.ID), created.ID, tenantID, "success", "")

	resp := &CreateMailboxResponse{Mailbox: *created, Password: req.Password}
	if req.ForcePasswordChange {
		resp.Password = ""
	}
	return resp, nil
}

func (s *Service) UpdateMailbox(ctx context.Context, id, tenantID uint, req UpdateMailboxRequest) (*AdminMailbox, error) {
	m, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrMailboxNotFound
	}

	if req.Name != nil {
		m.Name = *req.Name
	}
	if req.QuotaMB != nil {
		m.QuotaMB = *req.QuotaMB
	}
	if req.SendLimit != nil {
		m.SendLimit = *req.SendLimit
	}
	if req.IsAdmin != nil {
		m.IsAdmin = *req.IsAdmin
	}
	if req.AllowSMTP != nil {
		m.AllowSMTP = *req.AllowSMTP
	}
	if req.AllowIMAP != nil {
		m.AllowIMAP = *req.AllowIMAP
	}
	if req.AllowPOP3 != nil {
		m.AllowPOP3 = *req.AllowPOP3
	}
	if req.AllowJMAP != nil {
		m.AllowJMAP = *req.AllowJMAP
	}

	if err := s.repo.Update(ctx, m); err != nil {
		return nil, err
	}

	s.recordAudit(ctx, "mailbox.update", fmt.Sprintf("mailbox:%d", m.ID), m.ID, tenantID, "success", "")
	return m, nil
}

func (s *Service) SetStatus(ctx context.Context, id, tenantID uint, status AdminMailboxStatus, reason string) error {
	m, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if m == nil {
		return ErrMailboxNotFound
	}
	if !isValidStatusTransition(m.Status, status) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, m.Status, status)
	}

	if err := s.repo.UpdateStatus(ctx, id, tenantID, status); err != nil {
		return err
	}

	action := fmt.Sprintf("mailbox.%s", status)
	s.recordAudit(ctx, action, fmt.Sprintf("mailbox:%d", id), id, tenantID, "success", reason)
	return nil
}

func (s *Service) BulkSetStatus(ctx context.Context, ids []uint, tenantID uint, status AdminMailboxStatus, reason string) (int64, error) {
	affected, err := s.repo.UpdateStatusBulk(ctx, ids, tenantID, status)
	if err != nil {
		return 0, err
	}
	action := fmt.Sprintf("mailbox.bulk_%s", status)
	s.recordAudit(ctx, action, fmt.Sprintf("mailboxes:%v", ids), 0, tenantID, "success", reason)
	return affected, nil
}

func (s *Service) ResetPassword(ctx context.Context, id, tenantID uint) (string, error) {
	m, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", ErrMailboxNotFound
	}

	newPassword := generatePassword(16)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	if err := s.repo.UpdatePassword(ctx, id, tenantID, string(passwordHash)); err != nil {
		return "", err
	}

	s.recordAudit(ctx, "mailbox.password_reset", fmt.Sprintf("mailbox:%d", id), id, tenantID, "success", "")
	return newPassword, nil
}

func (s *Service) recordAudit(ctx context.Context, action, target string, targetID, tenantID uint, result, reason string) {
	if s.auditStore == nil {
		return
	}
	e := &audit.ExtendedEntry{
		Action:   action,
		Target:   target,
		TargetID: targetID,
		TenantID: tenantID,
		Result:   result,
		Reason:   reason,
	}
	_ = s.auditStore.Record(ctx, e)
}

func isValidStatusTransition(from, to AdminMailboxStatus) bool {
	switch from {
	case AdminMailboxActive:
		return to == AdminMailboxDisabled || to == AdminMailboxSuspended
	case AdminMailboxDisabled:
		return to == AdminMailboxActive
	case AdminMailboxSuspended:
		return to == AdminMailboxActive
	case AdminMailboxDeleted:
		return false
	}
	return false
}

func generatePassword(length int) string {
	b := make([]byte, length)
	rand.Read(b)
	return hex.EncodeToString(b)[:length]
}

var (
	ErrMailboxNotFound    = fmt.Errorf("mailbox not found")
	ErrMailboxExists      = fmt.Errorf("mailbox already exists")
	ErrInvalidEmail       = fmt.Errorf("invalid email address")
	ErrPasswordRequired   = fmt.Errorf("password is required")
	ErrInvalidTransition  = fmt.Errorf("invalid status transition")
)
