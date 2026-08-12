package mailcontrol

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	admindomain "github.com/orvix/orvix/internal/admin/domain"
	adminmailbox "github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// Service orchestrates the existing production admin services for the
// Platform Super Admin mail-control surface. It never fabricates a
// tenant: every operation requires an explicit tenantID, and every
// resource is re-verified tenant-owned through the underlying service
// (which already scopes by tenant_id in its SQL).
type Service struct {
	domains  *admindomain.Service
	mailboxes *adminmailbox.Service
	repo     *Repository
	audit    *audit.ExtendedStore
	clock    func() time.Time
}

type Ports struct {
	Domains   *admindomain.Service
	Mailboxes *adminmailbox.Service
	Audit     *audit.ExtendedStore
}

func NewService(repo *Repository, ports Ports) *Service {
	return &Service{
		domains:   ports.Domains,
		mailboxes: ports.Mailboxes,
		repo:      repo,
		audit:     ports.Audit,
		clock:     func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) now() time.Time { return s.clock() }

func (s *Service) requireTenant(tenantID uint) error {
	if tenantID == 0 {
		return ErrTenantRequired
	}
	return nil
}

func (s *Service) auditRecord(ctx context.Context, actorID uint, action, target string, tenantID uint, result, reason string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, &audit.ExtendedEntry{
		ActorID: actorID, TenantID: tenantID, Action: action, Target: target,
		Result: result, Reason: reason, Timestamp: s.now(),
	})
}

// ── Domains ────────────────────────────────────────────────────────

func (s *Service) ListDomains(ctx context.Context, f PlatformDomainFilter) (*PlatformDomainList, error) {
	if f.TenantID == 0 {
		return nil, ErrTenantRequired
	}
	statusPtr := stringPtrIf(f.Status != "", f.Status)
	tenantPtr := uintPtrIf(f.TenantID != 0, f.TenantID)
	domains, total, err := s.domains.ListDomains(ctx, admindomain.DomainFilter{
		TenantID: tenantPtr, Status: statusPtr, Search: f.Search, Limit: sanitizeLimit(f.Limit), Offset: f.Offset,
	})
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list platform domains", err)
	}
	out := make([]PlatformDomain, 0, len(domains))
	for _, d := range domains {
		mode, _ := s.domains.GetMailAccessMode(ctx, d.ID, f.TenantID)
		out = append(out, PlatformDomain{
			ID: d.ID, TenantID: f.TenantID, Name: d.Name, Status: d.Status, Plan: d.Plan,
			Description: d.Description, MailboxCount: d.MailboxCount, AliasCount: d.AliasCount,
			DKIMEnabled: d.DKIMEnabled, DKIMSelector: d.DKIMSelector, DMARCEnabled: d.DMARCEnabled,
			MailAccessMode: string(mode), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		})
	}
	return &PlatformDomainList{Domains: out, Total: total, Limit: sanitizeLimit(f.Limit), Offset: f.Offset}, nil
}

func (s *Service) GetDomain(ctx context.Context, id, tenantID uint) (*PlatformDomain, error) {
	if err := s.requireTenant(tenantID); err != nil {
		return nil, err
	}
	d, err := s.domains.GetDomain(ctx, id, tenantID)
	if err != nil {
		if errors.Is(err, admindomain.ErrDomainNotFound) {
			return nil, kernel.NotFound("domain")
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get platform domain", err)
	}
	if d == nil {
		return nil, kernel.NotFound("domain")
	}
	mode, _ := s.domains.GetMailAccessMode(ctx, d.ID, tenantID)
	aliases, _ := s.domains.CountAliasesByDomain(ctx, d.ID, tenantID)
	return &PlatformDomain{
		ID: d.ID, TenantID: tenantID, Name: d.Name, Status: d.Status, Plan: d.Plan,
		Description: d.Description, MailboxCount: d.MailboxCount, AliasCount: int(aliases),
		DKIMEnabled: d.DKIMEnabled, DKIMSelector: d.DKIMSelector, DMARCEnabled: d.DMARCEnabled,
		MailAccessMode: string(mode), CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}, nil
}

// SetDomainStatus applies an allowed lifecycle transition for an
// explicit tenant-owned domain through the production admin service.
// Ownership is verified first: a cross-tenant id yields NOT_FOUND, and
// the guarded tenant-scoped UPDATE would otherwise silently affect zero
// rows.
func (s *Service) SetDomainStatus(ctx context.Context, id, tenantID uint, status, reason string, actorID uint) error {
	if err := s.requireTenant(tenantID); err != nil {
		return err
	}
	if _, err := s.domains.GetDomain(ctx, id, tenantID); err != nil {
		if errors.Is(err, admindomain.ErrDomainNotFound) {
			return kernel.NotFound("domain")
		}
		return kernel.Wrap(kernel.ErrCodeInternal, "verify platform domain ownership", err)
	}
	if err := s.domains.SetDomainStatus(ctx, id, tenantID, status, reason); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "set platform domain status", err)
	}
	s.auditRecord(ctx, actorID, "platform.domain.status", fmt.Sprintf("domain:%d status:%s", id, status), tenantID, "success", reason)
	return nil
}

// SetMailAccessMode sets the canonical domain mail-access mode used by
// the SMTP policy path. It validates the mode and re-checks ownership
// through the production service, which enforces tenant_id in SQL.
func (s *Service) SetMailAccessMode(ctx context.Context, id, tenantID uint, mode string, actorID uint) error {
	if err := s.requireTenant(tenantID); err != nil {
		return err
	}
	if _, ok := admindomain.ParseMailAccessMode(mode); !ok {
		return kernel.ValidationError(map[string]string{"mail_access_mode": "must be internal_only or internal_external"})
	}
	if err := s.domains.SetMailAccessMode(ctx, id, tenantID, mode); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "set mail access mode", err)
	}
	s.auditRecord(ctx, actorID, "platform.domain.mail_access_mode", fmt.Sprintf("domain:%d mode:%s", id, mode), tenantID, "success", "")
	return nil
}

// ── Mailboxes ──────────────────────────────────────────────────────

func (s *Service) ListMailboxes(ctx context.Context, f PlatformMailboxFilter) (*PlatformMailboxList, error) {
	if err := s.requireTenant(f.TenantID); err != nil {
		return nil, err
	}
	statusPtr := (*adminmailbox.AdminMailboxStatus)(nil)
	if f.Status != "" {
		v := adminmailbox.AdminMailboxStatus(f.Status)
		statusPtr = &v
	}
	tenantPtr := f.TenantID
	var domainPtr *uint
	if f.DomainID > 0 {
		domainPtr = &f.DomainID
	}
	boxes, total, err := s.mailboxes.ListMailboxes(ctx, adminmailbox.MailboxFilter{
		DomainID: domainPtr, TenantID: &tenantPtr, Status: statusPtr, Search: f.Search, Limit: sanitizeLimit(f.Limit), Offset: f.Offset,
	})
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list platform mailboxes", err)
	}
	out := make([]PlatformMailbox, 0, len(boxes))
	for _, m := range boxes {
		out = append(out, PlatformMailbox{
			ID: m.ID, TenantID: f.TenantID, DomainID: m.DomainID, Email: m.Email, Name: m.Name,
			Status: string(m.Status), IsAdmin: m.IsAdmin, QuotaMB: m.QuotaMB, UsedBytes: m.UsedBytes,
			CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
		})
	}
	return &PlatformMailboxList{Mailboxes: out, Total: total, Limit: sanitizeLimit(f.Limit), Offset: f.Offset}, nil
}

func (s *Service) GetMailbox(ctx context.Context, id, tenantID uint) (*PlatformMailbox, error) {
	if err := s.requireTenant(tenantID); err != nil {
		return nil, err
	}
	m, err := s.mailboxes.GetMailbox(ctx, id, tenantID)
	if err != nil {
		if errors.Is(err, adminmailbox.ErrMailboxNotFound) {
			return nil, kernel.NotFound("mailbox")
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get platform mailbox", err)
	}
	if m == nil {
		return nil, kernel.NotFound("mailbox")
	}
	return &PlatformMailbox{
		ID: m.ID, TenantID: tenantID, DomainID: m.DomainID, Email: m.Email, Name: m.Name,
		Status: string(m.Status), IsAdmin: m.IsAdmin, QuotaMB: m.QuotaMB, UsedBytes: m.UsedBytes,
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}, nil
}

// verifyMailboxOwnership fails closed with NOT_FOUND when the mailbox
// does not belong to the explicit tenant.
func (s *Service) verifyMailboxOwnership(ctx context.Context, id, tenantID uint) error {
	if _, err := s.mailboxes.GetMailbox(ctx, id, tenantID); err != nil {
		if errors.Is(err, adminmailbox.ErrMailboxNotFound) {
			return kernel.NotFound("mailbox")
		}
		return kernel.Wrap(kernel.ErrCodeInternal, "verify platform mailbox ownership", err)
	}
	return nil
}

// UpdateMailboxStatus transitions a mailbox lifecycle state through the
// production admin service (tenant-scoped SQL).
func (s *Service) UpdateMailboxStatus(ctx context.Context, id, tenantID uint, status, reason string, actorID uint) error {
	if err := s.requireTenant(tenantID); err != nil {
		return err
	}
	if err := s.verifyMailboxOwnership(ctx, id, tenantID); err != nil {
		return err
	}
	if err := s.mailboxes.SetStatus(ctx, id, tenantID, adminmailbox.AdminMailboxStatus(status), reason); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "set platform mailbox status", err)
	}
	s.auditRecord(ctx, actorID, "platform.mailbox.status", fmt.Sprintf("mailbox:%d status:%s", id, status), tenantID, "success", reason)
	return nil
}

// UpdateMailboxQuota updates quota through the production admin service
// which enforces domain-bound ceilings.
func (s *Service) UpdateMailboxQuota(ctx context.Context, id, tenantID uint, quotaMB int64, actorID uint) error {
	if err := s.requireTenant(tenantID); err != nil {
		return err
	}
	if quotaMB <= 0 {
		return kernel.ValidationError(map[string]string{"quota_mb": "must be a positive integer"})
	}
	if err := s.verifyMailboxOwnership(ctx, id, tenantID); err != nil {
		return err
	}
	req := adminmailbox.UpdateMailboxRequest{QuotaMB: &quotaMB}
	if _, err := s.mailboxes.UpdateMailbox(ctx, id, tenantID, req); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "set platform mailbox quota", err)
	}
	s.auditRecord(ctx, actorID, "platform.mailbox.quota", fmt.Sprintf("mailbox:%d quota_mb:%d", id, quotaMB), tenantID, "success", "")
	return nil
}

// ResetMailboxPassword uses the production secure reset service. It
// returns the generated password exactly once (the service contract
// supports generation); the caller must never log or cache it.
func (s *Service) ResetMailboxPassword(ctx context.Context, id, tenantID uint, actorID uint) (string, error) {
	if err := s.requireTenant(tenantID); err != nil {
		return "", err
	}
	if err := s.verifyMailboxOwnership(ctx, id, tenantID); err != nil {
		return "", err
	}
	pw, err := s.mailboxes.ResetPassword(ctx, id, tenantID)
	if err != nil {
		return "", kernel.Wrap(kernel.ErrCodeInternal, "reset platform mailbox password", err)
	}
	s.auditRecord(ctx, actorID, "platform.mailbox.password_reset", fmt.Sprintf("mailbox:%d", id), tenantID, "success", "")
	return pw, nil
}

// SoftDeleteMailbox marks a mailbox deleted through the production
// service. Confirmation is validated by the handler for destructive
// actions.
func (s *Service) SoftDeleteMailbox(ctx context.Context, id, tenantID uint, actorID uint) error {
	if err := s.requireTenant(tenantID); err != nil {
		return err
	}
	if err := s.verifyMailboxOwnership(ctx, id, tenantID); err != nil {
		return err
	}
	if err := s.mailboxes.SoftDeleteMailbox(ctx, id, tenantID, "platform super admin"); err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "soft delete platform mailbox", err)
	}
	s.auditRecord(ctx, actorID, "platform.mailbox.delete", fmt.Sprintf("mailbox:%d", id), tenantID, "success", "")
	return nil
}

// ── Aliases ────────────────────────────────────────────────────────

func (s *Service) ListAliases(ctx context.Context, f PlatformAliasFilter) (*PlatformAliasList, error) {
	if err := s.requireTenant(f.TenantID); err != nil {
		return nil, err
	}
	aliases, total, err := s.repo.ListAliases(ctx, f)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list platform aliases", err)
	}
	return &PlatformAliasList{Aliases: aliases, Total: total, Limit: sanitizeLimit(f.Limit), Offset: f.Offset}, nil
}

func (s *Service) GetAlias(ctx context.Context, id, tenantID uint) (*PlatformAlias, error) {
	if err := s.requireTenant(tenantID); err != nil {
		return nil, err
	}
	a, err := s.repo.GetAlias(ctx, id, tenantID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get platform alias", err)
	}
	if a == nil {
		return nil, kernel.NotFound("alias")
	}
	return a, nil
}

// CreateAlias verifies the domain is tenant-owned, rejects a
// from-address that already exists (conflict), rejects a loop where
// from == to, and persists via the platform repository.
func (s *Service) CreateAlias(ctx context.Context, tenantID, domainID uint, fromAddr, toAddr string, actorID uint) (*PlatformAlias, error) {
	if err := s.requireTenant(tenantID); err != nil {
		return nil, err
	}
	fromAddr = strings.ToLower(strings.TrimSpace(fromAddr))
	toAddr = strings.ToLower(strings.TrimSpace(toAddr))
	if fromAddr == "" || toAddr == "" || !strings.Contains(fromAddr, "@") || !strings.Contains(toAddr, "@") {
		return nil, kernel.ValidationError(map[string]string{"from_addr": "valid email addresses required"})
	}
	if fromAddr == toAddr {
		return nil, kernel.NewError(kernel.ErrCodeValidation, "alias loop: from and to must differ")
	}
	// Domain ownership check through the production service.
	if _, err := s.domains.GetDomain(ctx, domainID, tenantID); err != nil || err == nil {
		d, derr := s.domains.GetDomain(ctx, domainID, tenantID)
		if derr != nil || d == nil {
			return nil, kernel.NotFound("domain")
		}
	}
	if err := s.requireTenant(tenantID); err != nil {
		return nil, err
	}
	a, err := s.repo.CreateAlias(ctx, tenantID, domainID, fromAddr, toAddr)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate") {
			return nil, kernel.NewError(kernel.ErrCodeConflict, "alias already exists")
		}
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "create platform alias", err)
	}
	s.auditRecord(ctx, actorID, "platform.alias.create", fmt.Sprintf("alias:%d", a.ID), tenantID, "success", "")
	return a, nil
}

func (s *Service) DeleteAlias(ctx context.Context, id, tenantID uint, actorID uint) error {
	if err := s.requireTenant(tenantID); err != nil {
		return err
	}
	ok, err := s.repo.DeleteAlias(ctx, id, tenantID)
	if err != nil {
		return kernel.Wrap(kernel.ErrCodeInternal, "delete platform alias", err)
	}
	if !ok {
		return kernel.NotFound("alias")
	}
	s.auditRecord(ctx, actorID, "platform.alias.delete", fmt.Sprintf("alias:%d", id), tenantID, "success", "")
	return nil
}

// ── Groups ─────────────────────────────────────────────────────────

func (s *Service) ListGroups(ctx context.Context, f PlatformGroupFilter) (*PlatformGroupList, error) {
	if err := s.requireTenant(f.TenantID); err != nil {
		return nil, err
	}
	groups, total, err := s.repo.ListGroups(ctx, f)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "list platform groups", err)
	}
	return &PlatformGroupList{Groups: groups, Total: total, Limit: sanitizeLimit(f.Limit), Offset: f.Offset}, nil
}

func (s *Service) GetGroup(ctx context.Context, id, tenantID uint) (*PlatformGroup, error) {
	if err := s.requireTenant(tenantID); err != nil {
		return nil, err
	}
	g, err := s.repo.GetGroup(ctx, id, tenantID)
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "get platform group", err)
	}
	if g == nil {
		return nil, kernel.NotFound("group")
	}
	return g, nil
}

func (s *Service) ListGroupMembers(ctx context.Context, id, tenantID uint) ([]string, error) {
	if err := s.requireTenant(tenantID); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetGroup(ctx, id, tenantID); err != nil || err == nil {
		g, gerr := s.repo.GetGroup(ctx, id, tenantID)
		if gerr != nil || g == nil {
			return nil, kernel.NotFound("group")
		}
	}
	return s.repo.ListGroupMembers(ctx, id, tenantID)
}

// ── Bulk mailbox operations ────────────────────────────────────────

// BulkMailboxStatus applies a status transition to up to 500 mailboxes
// within one explicit tenant/domain. It reports per-row style totals
// via RowsAffected and rejects invalid actions.
func (s *Service) BulkMailboxStatus(ctx context.Context, req BulkMailboxRequest, actorID uint) (*BulkMailboxResult, error) {
	if err := s.requireTenant(req.TenantID); err != nil {
		return nil, err
	}
	if len(req.IDs) == 0 || len(req.IDs) > 500 {
		return nil, kernel.ValidationError(map[string]string{"ids": "between 1 and 500 ids required"})
	}
	var status string
	switch req.Action {
	case BulkMailboxSuspend:
		status = "suspended"
	case BulkMailboxReactivate:
		status = "active"
	case BulkMailboxDelete:
		status = "deleted"
	default:
		return nil, kernel.ValidationError(map[string]string{"action": "must be suspend, reactivate, or delete"})
	}
	if status == "deleted" {
		// Soft-delete via the production lifecycle service per row so
		// the soft-delete semantics and audit stay consistent.
		succeeded := 0
		var failed []BulkMailboxFailure
		for _, id := range req.IDs {
			if err := s.mailboxes.SoftDeleteMailbox(ctx, id, req.TenantID, "platform bulk delete"); err != nil {
				failed = append(failed, BulkMailboxFailure{ID: id, Error: "delete failed"})
				continue
			}
			succeeded++
		}
		s.auditRecord(ctx, actorID, "platform.mailbox.bulk_delete", fmt.Sprintf("tenant:%d succeeded:%d total:%d", req.TenantID, succeeded, len(req.IDs)), req.TenantID, "success", req.Reason)
		return &BulkMailboxResult{Total: len(req.IDs), Succeeded: succeeded, Failed: failed}, nil
	}
	n, err := s.repo.BulkSetMailboxStatus(ctx, req.TenantID, req.DomainID, req.IDs, status, s.now())
	if err != nil {
		return nil, kernel.Wrap(kernel.ErrCodeInternal, "bulk mailbox status", err)
	}
	s.auditRecord(ctx, actorID, "platform.mailbox.bulk_status", fmt.Sprintf("tenant:%d action:%s affected:%d", req.TenantID, req.Action, n), req.TenantID, "success", req.Reason)
	return &BulkMailboxResult{Total: len(req.IDs), Succeeded: int(n)}, nil
}

// ── helpers ────────────────────────────────────────────────────────

func stringPtrIf(cond bool, v string) *string {
	if !cond {
		return nil
	}
	return &v
}

func uintPtrIf(cond bool, v uint) *uint {
	if !cond {
		return nil
	}
	return &v
}

var _ = errors.Is
