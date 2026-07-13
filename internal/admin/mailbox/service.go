package mailbox

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
		AllowJMAP: true,
	}

	var created *AdminMailbox
	entry := &audit.ExtendedEntry{Action: "mailbox.create", TenantID: tenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		var createErr error
		created, createErr = repo.Create(ctx, m, string(passwordHash))
		if createErr == nil {
			entry.Target, entry.TargetID = fmt.Sprintf("mailbox:%d", created.ID), created.ID
		}
		return createErr
	}); err != nil {
		return nil, err
	}

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

	entry := &audit.ExtendedEntry{Action: "mailbox.update", Target: fmt.Sprintf("mailbox:%d", m.ID), TargetID: m.ID, TenantID: tenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error { return repo.Update(ctx, m) }); err != nil {
		return nil, err
	}
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

	action := fmt.Sprintf("mailbox.%s", status)
	entry := &audit.ExtendedEntry{Action: action, Target: fmt.Sprintf("mailbox:%d", id), TargetID: id, TenantID: tenantID, Result: "success", Reason: reason}
	return s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		return repo.UpdateStatus(ctx, id, tenantID, status)
	})
}

func (s *Service) BulkSetStatus(ctx context.Context, ids []uint, tenantID uint, status AdminMailboxStatus, reason string) (int64, error) {
	var affected int64
	action := fmt.Sprintf("mailbox.bulk_%s", status)
	entry := &audit.ExtendedEntry{Action: action, Target: fmt.Sprintf("mailboxes:%v", ids), TenantID: tenantID, Result: "success", Reason: reason}
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		var updateErr error
		affected, updateErr = repo.UpdateStatusBulk(ctx, ids, tenantID, status)
		return updateErr
	}); err != nil {
		return 0, err
	}
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

	newPassword := generatePassword(24)
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	entry := &audit.ExtendedEntry{Action: "mailbox.password_reset", Target: fmt.Sprintf("mailbox:%d", id), TargetID: id, TenantID: tenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		return repo.UpdatePassword(ctx, id, tenantID, string(passwordHash))
	}); err != nil {
		return "", err
	}
	return newPassword, nil
}

func (s *Service) mutateWithAudit(ctx context.Context, entry *audit.ExtendedEntry, mutate func(*AdminMailboxRepo) error) error {
	if s.auditStore == nil {
		return mutate(s.repo)
	}
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin mailbox mutation: %w", err)
	}
	defer tx.Rollback()
	if err := mutate(s.repo.WithTx(tx)); err != nil {
		return err
	}
	if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit mailbox mutation: %w", err)
	}
	return nil
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
	if length < 24 {
		length = 24
	}
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed while generating mailbox password")
	}
	out := base64.RawURLEncoding.EncodeToString(b)
	if len(out) < length {
		return out
	}
	return out[:length]
}

var (
	ErrMailboxNotFound   = fmt.Errorf("mailbox not found")
	ErrMailboxExists     = fmt.Errorf("mailbox already exists")
	ErrInvalidEmail      = fmt.Errorf("invalid email address")
	ErrPasswordRequired  = fmt.Errorf("password is required")
	ErrInvalidTransition = fmt.Errorf("invalid status transition")
)
