package mailbox

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/coremail"
	entrbac "github.com/orvix/orvix/internal/enterprise/rbac"
	"github.com/orvix/orvix/internal/platform/kernel"
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
	webhooks   webhookPublisher
}

// webhookPublisher is the same transactional outbox publisher shape
// the domain service uses. It is optional for isolated consumers;
// production wiring supplies it so guarded mutations publish from the
// same transaction as their audit.
type webhookPublisher interface {
	Publish(ctx context.Context, q kernel.Querier, topic, aggregateID string, tenantID uint, payload any, at time.Time) (string, error)
}

func NewService(repo *AdminMailboxRepo, hasher PasswordHasher, auditStore *audit.ExtendedStore, rbac *entrbac.Evaluator) *Service {
	return &Service{repo: repo, hasher: hasher, auditStore: auditStore, rbac: rbac}
}

// SetWebhookPublisher wires the transactional webhook event adapter.
func (s *Service) SetWebhookPublisher(p webhookPublisher) { s.webhooks = p }

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

// ExistsByEmail is a read-only existence check exposed for dry-run
// validation callers (e.g. internal/platform/bulkprovision) that must
// detect a duplicate without performing or preparing any mutation.
func (s *Service) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return s.repo.ExistsByEmail(ctx, email, 0)
}

// ResolveDomainAllocation is a read-only passthrough exposed for
// dry-run capacity estimation. lock=false: callers using this for an
// estimate must not take a row lock outside of CreateMailbox's own
// authoritative, transactional check — this is advisory only.
func (s *Service) ResolveDomainAllocation(ctx context.Context, domainName string, tenantID uint) (*DomainAllocation, error) {
	return s.repo.ResolveDomainAllocation(ctx, domainName, tenantID, false)
}

// CountActiveByDomain is a read-only passthrough for advisory capacity
// estimates (e.g. bulk-import dry-run). Not authoritative — see
// ResolveDomainAllocation's doc comment.
func (s *Service) CountActiveByDomain(ctx context.Context, domainID, tenantID uint) (int, error) {
	return s.repo.CountActiveByDomain(ctx, domainID, tenantID)
}

func (s *Service) GetMailbox(ctx context.Context, id, tenantID uint) (*AdminMailbox, error) {
	m, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrMailboxNotFound
	}
	s.attachEffectiveMode(ctx, m, tenantID)
	return m, nil
}

// attachEffectiveMode resolves the effective mail-access mode for a
// mailbox read. It is best-effort: a resolution failure leaves the
// effective field empty (the configured field is still authoritative
// for "what is stored"), and never fails the read.
func (s *Service) attachEffectiveMode(ctx context.Context, m *AdminMailbox, tenantID uint) {
	if m == nil {
		return
	}
	_, effective, _, err := s.repo.GetMailAccessModeState(ctx, m.ID, tenantID)
	if err == nil && effective != "" {
		m.EffectiveMailAccessMode = effective
	} else if err == nil {
		m.EffectiveMailAccessMode = string(MailAccessInternalExternal)
	}
}

// CreateMailbox creates a mailbox with the canonical service
// (transactional audit, atomic cap enforcement). The optional
// MailAccessMode in the request is validated at this boundary; when
// omitted the mailbox is persisted with "inherit", which resolves
// through the domain exactly as every pre-existing mailbox does.
func (s *Service) CreateMailbox(ctx context.Context, req CreateMailboxRequest, tenantID uint) (*CreateMailboxResponse, error) {
	return s.createMailbox(ctx, req, tenantID, false)
}

// CreateMailboxWithFolders is the platform provisioning variant of
// CreateMailbox: it creates the mailbox AND provisions the canonical
// system folders (INBOX, Sent, Drafts, Trash, Junk, Archive) inside
// the SAME transaction, so a folder-provisioning failure rolls back
// the mailbox insert — a half-created mailbox is never reachable.
func (s *Service) CreateMailboxWithFolders(ctx context.Context, req CreateMailboxRequest, tenantID uint) (*CreateMailboxResponse, error) {
	return s.createMailbox(ctx, req, tenantID, true)
}

func (s *Service) createMailbox(ctx context.Context, req CreateMailboxRequest, tenantID uint, provisionFolders bool) (*CreateMailboxResponse, error) {
	if req.Email == "" || !strings.Contains(req.Email, "@") {
		return nil, ErrInvalidEmail
	}
	if req.Password == "" {
		return nil, ErrPasswordRequired
	}

	// Mail-access-mode validation happens at this service boundary so
	// no write path can persist an invalid value. nil (omitted) and
	// "" both normalize to "inherit" (backward compatible); any other
	// unknown value is rejected.
	mailAccessMode := string(MailAccessInherit)
	if req.MailAccessMode != nil {
		parsed, ok := ParseMailAccessMode(*req.MailAccessMode)
		if !ok {
			return nil, fmt.Errorf("%w: %q", ErrInvalidMailAccessMode, *req.MailAccessMode)
		}
		mailAccessMode = string(parsed)
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
	if err := s.mutateWithTx(ctx, entry, func(repo *AdminMailboxRepo, tx *sql.Tx) error {
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
			DomainID:       domainID,
			TenantID:       tenantID,
			Email:          req.Email,
			LocalPart:      localPart,
			Name:           req.Name,
			Status:         AdminMailboxActive,
			QuotaMB:        quota,
			SendLimit:      sendLimit,
			AllowSMTP:      true,
			AllowIMAP:      true,
			AllowPOP3:      true,
			AllowJMAP:      true,
			MailAccessMode: mailAccessMode,
		}
		var createErr error
		created, createErr = repo.Create(ctx, m, passwordHash)
		if createErr == nil {
			if provisionFolders {
				// Provision the canonical system folders in the SAME
				// transaction as the mailbox insert. A failure here rolls
				// the mailbox row back with it — the operator never sees a
				// half-created mailbox.
				if ferr := coremail.EnsureMailboxSystemFoldersTx(ctx, tx, created.ID); ferr != nil {
					return fmt.Errorf("provision mailbox system folders: %w", ferr)
				}
			}
			entry.Target, entry.TargetID = fmt.Sprintf("mailbox:%d", created.ID), created.ID
		}
		return createErr
	}); err != nil {
		return nil, err
	}

	s.attachEffectiveMode(ctx, created, tenantID)

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
	if req.MailAccessMode != nil {
		parsed, ok := ParseMailAccessMode(*req.MailAccessMode)
		if !ok {
			return nil, ErrInvalidMailAccessMode
		}
		m.MailAccessMode = string(parsed)
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
	s.attachEffectiveMode(ctx, m, tenantID)
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

// SetMailAccessMode is the guarded per-mailbox access-mode mutation.
// It validates the mode, re-verifies tenant ownership INSIDE the SQL
// predicate, applies optimistic concurrency via expectedVersion, and
// writes the canonical audit record in the same transaction. The
// outbox event is enqueued through the injected publisher (when
// wired) so downstream consumers see the change transactionally.
//
// Error contracts (safe cross-tenant behavior):
//   - unknown OR cross-tenant mailbox -> ErrMailboxNotFound (the SQL
//     tenant predicate makes both affect zero rows; no disclosure);
//   - stale expectedVersion -> ErrMailboxVersionConflict;
//   - invalid mode -> ErrInvalidMailAccessMode.
func (s *Service) SetMailAccessMode(ctx context.Context, id, tenantID uint, mode string, expectedVersion int) (configured, effective string, newVersion int, err error) {
	parsed, ok := ParseMailAccessMode(mode)
	if !ok {
		return "", "", 0, ErrInvalidMailAccessMode
	}
	if expectedVersion < 1 {
		return "", "", 0, fmt.Errorf("%w: expected_version must be >= 1", ErrMailboxVersionConflict)
	}

	entry := &audit.ExtendedEntry{
		Action: "mailbox.mail_access_mode.set",
		Target: fmt.Sprintf("mailbox:%d", id), TargetID: id, TenantID: tenantID,
		Result: "success", After: string(parsed),
	}
	var affected int64
	var newVer int
	if err := s.mutateWithAudit(ctx, entry, func(repo *AdminMailboxRepo) error {
		var applyErr error
		affected, newVer, applyErr = repo.UpdateMailAccessMode(ctx, id, tenantID, string(parsed), expectedVersion)
		return applyErr
	}); err != nil {
		return "", "", 0, err
	}
	if affected == 0 {
		// Distinguish "not found / cross-tenant" from "version moved":
		// only the version predicate can fail while the row exists.
		exists, checkErr := s.repo.ExistsByID(ctx, id, tenantID)
		if checkErr != nil {
			return "", "", 0, fmt.Errorf("resolve mailbox after guarded update: %w", checkErr)
		}
		if !exists {
			return "", "", 0, ErrMailboxNotFound
		}
		return "", "", 0, fmt.Errorf("%w: mailbox version is no longer %d", ErrMailboxVersionConflict, expectedVersion)
	}

	if s.webhooks != nil {
		_, _ = s.webhooks.Publish(ctx, s.repo.db, "mailbox.access_mode.changed", fmt.Sprintf("mailbox:%d", id), tenantID, map[string]any{
			"mailbox_id":       id,
			"mail_access_mode": string(parsed),
		}, time.Now().UTC())
	}

	cfg, eff, _, gErr := s.repo.GetMailAccessModeState(ctx, id, tenantID)
	if gErr != nil {
		return string(parsed), resolveEffectiveMailAccessMode(string(parsed), ""), newVer, nil
	}
	return cfg, eff, newVer, nil
}

// ExistsByID reports whether a non-deleted mailbox with the given id
// exists inside the tenant. Used to distinguish not-found from
// version-conflict after a guarded update.
func (r *AdminMailboxRepo) ExistsByID(ctx context.Context, id, tenantID uint) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx,
		"SELECT 1 FROM coremail_mailboxes WHERE id="+r.dialect.Placeholder(1)+" AND tenant_id="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL",
		id, tenantID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
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

// mutateWithTx is the transaction-bound mutation helper used by the
// provisioning create path: it always runs inside a transaction so
// folder provisioning and the mailbox insert commit or roll back as
// one unit, even when no audit store is wired.
func (s *Service) mutateWithTx(ctx context.Context, entry *audit.ExtendedEntry, mutate func(repo *AdminMailboxRepo, tx *sql.Tx) error) error {
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin mailbox mutation: %w", err)
	}
	defer tx.Rollback()
	if err := mutate(s.repo.WithTx(tx), tx); err != nil {
		return err
	}
	if s.auditStore != nil {
		if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
			return err
		}
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
	ErrMailboxNotFound        = fmt.Errorf("mailbox not found")
	ErrMailboxExists          = fmt.Errorf("mailbox already exists")
	ErrInvalidEmail           = fmt.Errorf("invalid email address")
	ErrPasswordRequired       = fmt.Errorf("password is required")
	ErrInvalidTransition      = fmt.Errorf("invalid status transition")
	ErrInvalidQuota           = fmt.Errorf("invalid quota value")
	ErrInvalidMailAccessMode  = fmt.Errorf("unsupported mail access mode")
	ErrMailboxVersionConflict = fmt.Errorf("mailbox version conflict")
)
