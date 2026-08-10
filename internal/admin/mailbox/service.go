package mailbox

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/audit"
	entrbac "github.com/orvix/orvix/internal/enterprise/rbac"
)

// PasswordHasher abstracts password hashing so the admin mailbox service can
// share the exact Argon2id implementation used by Coremail mailbox
// authentication. coremail.AuthService implements this interface.
type PasswordHasher interface {
	HashPassword(password string) (string, error)
}

type Service struct {
	repo       *AdminMailboxRepo
	hasher     PasswordHasher
	auditStore *audit.ExtendedStore
	rbac       *entrbac.Evaluator
}

func NewService(repo *AdminMailboxRepo, hasher PasswordHasher, auditStore *audit.ExtendedStore, rbac *entrbac.Evaluator) *Service {
	return &Service{repo: repo, hasher: hasher, auditStore: auditStore, rbac: rbac}
}

func (s *Service) CountByTenant(ctx context.Context, tenantID uint) int64 {
	count, err := s.repo.CountByTenant(ctx, tenantID)
	if err != nil {
		return 0
	}
	return count
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

func (s *Service) CreateMailbox(ctx context.Context, req CreateMailboxRequest, tenantID uint) (*CreateMailboxResponse, error) {
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return nil, ErrInvalidEmail
	}
	if req.Password == "" {
		return nil, ErrPasswordRequired
	}

	parts := strings.SplitN(req.Email, "@", 2)
	localPart := parts[0]
	if localPart == "" || parts[1] == "" {
		return nil, ErrInvalidEmail
	}

	passwordHash, err := s.hashPassword(req.Password)
	if err != nil {
		return nil, err
	}

	if req.QuotaMB < 0 {
		return nil, ErrInvalidQuota
	}
	// 0 means "use the domain default"; the concrete value is resolved inside
	// the transaction below, where the domain's bounds are known.
	quota := req.QuotaMB
	sendLimit := req.SendLimit
	if sendLimit <= 0 {
		sendLimit = 500
	}

	var created *AdminMailbox
	entry := &audit.ExtendedEntry{Action: "mailbox.create", TenantID: tenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		// Resolve the domain inside the mutation transaction so the
		// eligibility check and the mailbox insert are atomic. The domain
		// lookup is tenant-scoped: a domain owned by another tenant or one
		// that is deleted resolves to the safe not-found contract.
		// Resolve the domain AND its allocation limits in the same locked
		// read, so the cap check, the quota check and the insert are one
		// atomic unit. On PostgreSQL the domain row is locked FOR UPDATE,
		// which is what makes two concurrent creations against a domain at
		// its cap deterministic: the second one blocks, re-counts after the
		// first commits, and is rejected instead of overshooting the cap.
		alloc, err := repo.ResolveDomainAllocation(ctx, parts[1], tenantID, true)
		if err != nil {
			if err == sql.ErrNoRows {
				return domain.ErrDomainNotFound
			}
			return err
		}
		domainID, status := alloc.DomainID, alloc.Status
		if status != string(domain.DomainStatusActive) {
			// Explicit status model: disabled and administratively restricted
			// states are distinct. No verification state exists on
			// coremail_domains, so unknown/legacy values fail closed with a
			// safe "unavailable" error rather than being mislabeled as
			// DNS-unverified (DOMAIN_NOT_VERIFIED is reserved).
			switch domain.DomainStatus(status) {
			case domain.DomainStatusDisabled:
				return domain.ErrDomainDisabled
			case domain.DomainStatusSuspended:
				return domain.ErrDomainSuspended
			case domain.DomainStatusLocked:
				return domain.ErrDomainLocked
			default:
				return domain.ErrDomainUnavailable
			}
		}

		exists, err := repo.ExistsByEmail(ctx, req.Email, 0)
		if err != nil {
			return fmt.Errorf("check exists: %w", err)
		}
		if exists {
			return ErrMailboxExists
		}

		// --- domain mailbox cap (transaction-safe) -----------------------
		// Counted INSIDE the transaction, after the domain row lock, so the
		// check-then-act window is closed. An inheriting domain resolves
		// against the organization plan ceiling; an unlimited domain or an
		// unlimited plan skips the check entirely.
		if capLimit, unlimited := domain.ResolveMailboxCap(alloc.MaxMailboxes, alloc.OrgMaxMailboxes); !unlimited {
			used, err := repo.CountActiveByDomain(ctx, domainID, tenantID)
			if err != nil {
				return fmt.Errorf("count domain mailboxes: %w", err)
			}
			if used >= capLimit {
				return domain.ErrMailboxLimitReached
			}
		}

		// --- per-mailbox quota bounds ------------------------------------
		maxQuotaMB, quotaUnlimited, defaultQuotaMB := domain.ResolveQuotaBounds(alloc.MaxQuotaMB, alloc.DefaultMailboxQuotaMB)
		if quota == 0 {
			// No explicit request: stamp the domain's default (which
			// ResolveQuotaBounds has already clamped to the ceiling).
			quota = defaultQuotaMB
		}
		if !quotaUnlimited && quota > maxQuotaMB {
			return domain.ErrQuotaExceedsDomain
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
		var createErr error
		created, createErr = repo.Create(ctx, m, passwordHash)
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
		if *req.QuotaMB < 0 {
			return nil, ErrInvalidQuota
		}
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
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		// Quota bounds are re-checked on the CHANGE path inside the same
		// transaction as the write. Storing a domain-level ceiling is not
		// enforcement unless every path that can raise a mailbox quota
		// honours it, and this is the other such path.
		if req.QuotaMB != nil {
			domainMaxQuotaMB, err := repo.GetDomainQuotaBounds(ctx, m.ID, tenantID)
			if err != nil {
				if err == sql.ErrNoRows {
					return ErrMailboxNotFound
				}
				return fmt.Errorf("resolve domain quota bounds: %w", err)
			}
			if maxMB, unlimited, _ := domain.ResolveQuotaBounds(domainMaxQuotaMB, 0); !unlimited && m.QuotaMB > maxMB {
				return domain.ErrQuotaExceedsDomain
			}
		}
		return repo.Update(ctx, m)
	}); err != nil {
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
	passwordHash, err := s.hashPassword(newPassword)
	if err != nil {
		return "", err
	}

	entry := &audit.ExtendedEntry{Action: "mailbox.password_reset", Target: fmt.Sprintf("mailbox:%d", id), TargetID: id, TenantID: tenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		return repo.UpdatePassword(ctx, id, tenantID, passwordHash)
	}); err != nil {
		return "", err
	}
	return newPassword, nil
}

// hashPassword produces a Coremail-compatible Argon2id hash via the injected
// hasher. The hasher is shared with the mailbox authentication path so every
// hash stored by this service verifies through coremail's AuthService.
func (s *Service) hashPassword(password string) (string, error) {
	if s.hasher == nil {
		return "", fmt.Errorf("password hasher unavailable")
	}
	hash, err := s.hasher.HashPassword(password)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return hash, nil
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
		return to == AdminMailboxDisabled || to == AdminMailboxSuspended || to == AdminMailboxDeleted
	case AdminMailboxDisabled:
		return to == AdminMailboxActive || to == AdminMailboxDeleted
	case AdminMailboxSuspended:
		return to == AdminMailboxActive || to == AdminMailboxDeleted
	case AdminMailboxDeleted:
		// Deleted is reached only via SetStatus/SoftDeleteMailbox and
		// left only via RestoreMailbox (a dedicated path with its own
		// email-conflict re-check) — never via a plain status
		// transition, which would skip that check.
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
	ErrInvalidQuota      = fmt.Errorf("invalid quota value")
)
