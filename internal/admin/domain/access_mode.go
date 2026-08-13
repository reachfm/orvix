package domain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
)

// MailAccessMode mirrors coremail.MailAccessMode. Duplicated (not
// imported) deliberately: internal/admin/domain talks to
// coremail_domains directly through its own DomainAdminRepo rather
// than through coremail.DomainRepository, and the two enforcement
// points (this admin API vs. the SMTP hot path in
// internal/coremail/smtp) share only the column name and string
// values, not a Go type — keeping them decoupled means a change to
// one cannot silently break the other's compile-time contract.
type MailAccessMode string

const (
	MailAccessInternalOnly     MailAccessMode = "internal_only"
	MailAccessInternalExternal MailAccessMode = "internal_external"
)

// ParseMailAccessMode normalizes and validates a requested access
// mode string. Unknown values are rejected rather than silently
// defaulted, so a typo in an API call never accidentally opens or
// restricts external mail.
func ParseMailAccessMode(s string) (MailAccessMode, bool) {
	v := MailAccessMode(strings.ToLower(strings.TrimSpace(s)))
	switch v {
	case MailAccessInternalOnly, MailAccessInternalExternal:
		return v, true
	default:
		return "", false
	}
}

var ErrInvalidMailAccessMode = fmt.Errorf("unsupported mail access mode")

// GetMailAccessMode reads the domain's current policy.
func (r *DomainAdminRepo) GetMailAccessMode(ctx context.Context, id, tenantID uint) (MailAccessMode, error) {
	var mode string
	err := r.db.QueryRowContext(ctx,
		"SELECT mail_access_mode FROM coremail_domains WHERE id="+r.dialect.Placeholder(1)+" AND tenant_id="+r.dialect.Placeholder(2)+" AND deleted_at IS NULL",
		id, tenantID).Scan(&mode)
	if err != nil {
		return "", err
	}
	if mode == "" {
		return MailAccessInternalExternal, nil
	}
	return MailAccessMode(mode), nil
}

func (r *DomainAdminRepo) UpdateMailAccessMode(ctx context.Context, id, tenantID uint, mode MailAccessMode) error {
	_, err := r.db.ExecContext(ctx,
		"UPDATE coremail_domains SET mail_access_mode="+r.dialect.Placeholder(1)+", updated_at="+r.dialect.Placeholder(2)+" WHERE id="+r.dialect.Placeholder(3)+" AND tenant_id="+r.dialect.Placeholder(4)+" AND deleted_at IS NULL",
		string(mode), time.Now().UTC(), id, tenantID)
	return err
}

// SetMailAccessMode validates, persists, and audits a domain's mail
// access mode change. This is the ONLY sanctioned write path — the
// SMTP hot path (internal/coremail/smtp) only ever reads this column,
// so every policy change funnels through the same audit trail.
func (s *Service) SetMailAccessMode(ctx context.Context, id, tenantID uint, mode string) error {
	normalized, ok := ParseMailAccessMode(mode)
	if !ok {
		return ErrInvalidMailAccessMode
	}
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return err
	}
	if d == nil {
		return ErrDomainNotFound
	}
	entry := &audit.ExtendedEntry{
		Action:   "domain.mail_access_mode.set",
		Target:   fmt.Sprintf("domain:%d", id),
		TargetID: id,
		TenantID: tenantID,
		Result:   "success",
		After:    string(normalized),
	}
	return s.mutateWithAudit(ctx, entry, func(repo *DomainAdminRepo) error {
		return repo.UpdateMailAccessMode(ctx, id, tenantID, normalized)
	})
}

func (s *Service) GetMailAccessMode(ctx context.Context, id, tenantID uint) (MailAccessMode, error) {
	d, err := s.repo.GetByID(ctx, id, tenantID)
	if err != nil {
		return "", err
	}
	if d == nil {
		return "", ErrDomainNotFound
	}
	return s.repo.GetMailAccessMode(ctx, id, tenantID)
}
