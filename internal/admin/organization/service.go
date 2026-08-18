package organization

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"github.com/orvix/orvix/internal/audit"
	entrbac "github.com/orvix/orvix/internal/enterprise/rbac"
	"github.com/orvix/orvix/internal/platform/kernel"
)

type Service struct {
	repo       *OrganizationRepo
	auditStore *audit.ExtendedStore
	rbac       *entrbac.Evaluator
}

func NewService(repo *OrganizationRepo, auditStore *audit.ExtendedStore, rbac *entrbac.Evaluator) *Service {
	return &Service{repo: repo, auditStore: auditStore, rbac: rbac}
}

func (s *Service) GetOrganization(ctx context.Context, id uint) (*Organization, error) {
	o, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrganizationNotFound
	}
	return o, nil
}

func (s *Service) ListOrganizations(ctx context.Context, filter OrganizationFilter) ([]Organization, int64, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) CreateOrganization(ctx context.Context, req CreateOrganizationRequest, platformTenantID uint) (*Organization, error) {
	exists, err := s.repo.ExistsBySlug(ctx, req.Slug, 0)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrOrganizationExists
	}
	if req.Name == "" {
		req.Name = req.Slug
	}
	if req.Plan == "" {
		req.Plan = "smb"
	}
	if req.MaxDomains == 0 {
		req.MaxDomains = 10
	}
	if req.MaxMailboxes == 0 {
		req.MaxMailboxes = 500
	}
	o := &Organization{
		Name: req.Name, Slug: req.Slug, Domain: req.Domain,
		Plan: req.Plan, MaxDomains: req.MaxDomains, MaxMailboxes: req.MaxMailboxes, Active: true,
	}
	var created *Organization
	entry := &audit.ExtendedEntry{Action: "organization.create", TenantID: platformTenantID, Result: "success"}
	if err := s.mutateWithAudit(ctx, entry, func(repo *OrganizationRepo) error {
		var createErr error
		created, createErr = repo.Create(ctx, o)
		if createErr == nil {
			entry.Target, entry.TargetID = fmt.Sprintf("tenant:%d", created.ID), created.ID
		}
		return createErr
	}); err != nil {
		// A concurrent request can win the INSERT between our pre-check
		// (ExistsBySlug, above) and this transaction's commit — the
		// database's own UNIQUE constraint on tenants.slug is the real
		// enforcement point, this pre-check is only an optimization to
		// avoid a doomed transaction in the common case. Translate the
		// resulting UNIQUE violation into the same stable
		// ErrOrganizationExists a caller already handles, instead of a
		// raw 500 for what is actually a legitimate conflict.
		if kernel.IsUniqueViolation(err) {
			return nil, ErrOrganizationExists
		}
		return nil, err
	}
	return created, nil
}

// CreateOrganizationWithOwner provisions an organization AND its initial
// owner invitation in ONE transaction (the org row, the invitation row and
// the audit record commit or roll back together). It is the platform
// super-admin organization-creation path: the PSA never invents an owner
// user or password — the initial tenant_admin owner is established through
// the same real invitation/activation model members use, so an organization
// can never be created ownerless AND active. The owner invitation is
// REQUIRED: a PSA-created organization without a designated owner is
// rejected before any row is written.
//
// The returned rawToken is the one-time invitation token (shown once at
// creation); only its SHA-256 hash is persisted.
func (s *Service) CreateOrganizationWithOwner(ctx context.Context, req CreateOrganizationRequest, platformTenantID, actorID uint, ownerEmail string) (*Organization, *OrganizationInvitation, string, error) {
	ownerEmail = strings.TrimSpace(strings.ToLower(ownerEmail))
	if ownerEmail == "" {
		return nil, nil, "", fmt.Errorf("owner_email is required: a PSA-created organization must have a designated owner")
	}
	if _, err := mail.ParseAddress(ownerEmail); err != nil {
		return nil, nil, "", fmt.Errorf("owner_email is not a valid email address")
	}
	exists, err := s.repo.ExistsBySlug(ctx, req.Slug, 0)
	if err != nil {
		return nil, nil, "", err
	}
	if exists {
		return nil, nil, "", ErrOrganizationExists
	}
	if req.Name == "" {
		req.Name = req.Slug
	}
	if req.Plan == "" {
		req.Plan = "smb"
	}
	if req.MaxDomains == 0 {
		req.MaxDomains = 10
	}
	if req.MaxMailboxes == 0 {
		req.MaxMailboxes = 500
	}
	o := &Organization{
		Name: req.Name, Slug: req.Slug, Domain: req.Domain,
		Plan: req.Plan, MaxDomains: req.MaxDomains, MaxMailboxes: req.MaxMailboxes, Active: true,
	}
	inv, rawToken, err := newInvitationToken(0, ownerEmail, "tenant_admin", 7)
	if err != nil {
		return nil, nil, "", fmt.Errorf("generate owner invitation: %w", err)
	}
	inv.InviterID = actorID

	var created *Organization
	entry := &audit.ExtendedEntry{
		Action: "organization.create", TenantID: platformTenantID, ActorID: actorID, Result: "success",
		Reason: "platform-initiated organization creation",
	}
	if err := s.mutateWithAudit(ctx, entry, func(repo *OrganizationRepo) error {
		var createErr error
		created, createErr = repo.Create(ctx, o)
		if createErr != nil {
			return createErr
		}
		inv.OrganizationID = created.ID
		inv.InviterID = actorID
		if err := repo.CreateInvitation(ctx, inv); err != nil {
			return fmt.Errorf("create owner invitation: %w", err)
		}
		entry.Target, entry.TargetID = fmt.Sprintf("tenant:%d", created.ID), created.ID
		return nil
	}); err != nil {
		if kernel.IsUniqueViolation(err) {
			return nil, nil, "", ErrOrganizationExists
		}
		return nil, nil, "", err
	}
	return created, inv, rawToken, nil
}

func (s *Service) UpdateOrganization(ctx context.Context, id uint, req UpdateOrganizationRequest) (*Organization, error) {
	o, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrganizationNotFound
	}
	if req.Name != nil {
		o.Name = *req.Name
	}
	if req.Domain != nil {
		o.Domain = *req.Domain
	}
	if req.Plan != nil {
		o.Plan = *req.Plan
	}
	if req.MaxDomains != nil {
		o.MaxDomains = *req.MaxDomains
	}
	if req.MaxMailboxes != nil {
		o.MaxMailboxes = *req.MaxMailboxes
	}
	if req.LogoURL != nil {
		o.LogoURL = *req.LogoURL
	}
	if req.PrimaryColor != nil {
		o.PrimaryColor = *req.PrimaryColor
	}
	entry := &audit.ExtendedEntry{Action: "organization.update", Target: fmt.Sprintf("tenant:%d", id), TargetID: id, TenantID: id, Result: "success"}
	return o, s.mutateWithAudit(ctx, entry, func(repo *OrganizationRepo) error { return repo.Update(ctx, o) })
}

func (s *Service) SetOrganizationActive(ctx context.Context, id uint, active bool, reason string) error {
	_, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	action := "organization.disable"
	if active {
		action = "organization.enable"
	}
	entry := &audit.ExtendedEntry{Action: action, Target: fmt.Sprintf("tenant:%d", id), TargetID: id, TenantID: id, Result: "success", Reason: reason}
	return s.mutateWithAudit(ctx, entry, func(repo *OrganizationRepo) error { return repo.SetActive(ctx, id, active) })
}

func (s *Service) GetOrganizationDetail(ctx context.Context, id uint) (*OrganizationDetail, error) {
	o, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if o == nil {
		return nil, ErrOrganizationNotFound
	}
	detail := &OrganizationDetail{Organization: *o}
	if o.Active {
		detail.StatusLabel = "active"
	} else {
		detail.StatusLabel = "disabled"
	}
	// Populate the real counters the Platform Organizations drawer renders
	// (Domains / Mailboxes / Admin users). These were declared on the
	// detail struct but never computed, so the drawer always showed 0 —
	// including "Admin users = 0" for every signup-created organization.
	// Domain/Mailbox counts come from the SAME coremail_* tables the
	// customer portal's own Domains/Mailboxes pages list, so the drawer
	// number matches what the customer sees. AdminCount comes from
	// CountAdmins, whose canonical role set counts true tenant
	// administrators (tenant_admin plus legacy admin/superadmin
	// pre-normalization rows) and never webmail RoleUser rows.
	if domainCount, derr := s.countCoremailDomains(ctx, id); derr == nil {
		detail.DomainCount = domainCount
	}
	if mailboxCount, merr := s.countCoremailMailboxes(ctx, id); merr == nil {
		detail.MailboxCount = mailboxCount
	}
	if adminCount, aerr := s.repo.CountAdmins(ctx, id); aerr == nil {
		detail.AdminCount = adminCount
	}
	return detail, nil
}

// countCoremailDomains counts non-deleted coremail_domains rows for the
// tenant — the same table and predicate ListAdminDomains uses.
func (s *Service) countCoremailDomains(ctx context.Context, tenantID uint) (int, error) {
	var count int
	err := s.repo.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_domains WHERE tenant_id="+s.repo.dialect.Placeholder(1)+" AND deleted_at IS NULL", tenantID).Scan(&count)
	return count, err
}

// countCoremailMailboxes counts non-deleted coremail_mailboxes rows for
// the tenant — the same table and predicate ListAdminMailboxes uses.
func (s *Service) countCoremailMailboxes(ctx context.Context, tenantID uint) (int, error) {
	var count int
	err := s.repo.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM coremail_mailboxes WHERE tenant_id="+s.repo.dialect.Placeholder(1)+" AND deleted_at IS NULL", tenantID).Scan(&count)
	return count, err
}

func (s *Service) ListMembers(ctx context.Context, orgID uint) ([]OrganizationMember, error) {
	rows, err := s.repo.db.QueryContext(ctx,
		`SELECT id, email, COALESCE(role, 'user'), COALESCE(name, ''), created_at FROM users WHERE tenant_id = `+s.repo.dialect.Placeholder(1)+` ORDER BY id ASC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []OrganizationMember
	for rows.Next() {
		var m OrganizationMember
		if err := rows.Scan(&m.ID, &m.Email, &m.Role, &m.Name, &m.CreatedAt); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

// UpdateMemberRole refuses to demote the organization's last active
// tenant_admin away from that role — an equivalent lockout to deleting
// or suspending them, since a demoted-to-tenant_readonly last admin
// leaves nobody able to administer the org either.
func (s *Service) UpdateMemberRole(ctx context.Context, memberID, orgID uint, role string) error {
	if !isValidOrgMemberRole(role) {
		return fmt.Errorf("invalid organization member role: %s", role)
	}
	if !isAdminLikeRole(role) {
		isLast, err := s.isLastActiveAdmin(ctx, memberID, orgID)
		if err != nil {
			return err
		}
		if isLast {
			return ErrLastActiveAdmin
		}
	}
	res, err := s.repo.db.ExecContext(ctx,
		"UPDATE users SET role = "+s.repo.dialect.Placeholder(1)+", token_version = COALESCE(token_version, 0) + 1 WHERE id = "+s.repo.dialect.Placeholder(2)+" AND tenant_id = "+s.repo.dialect.Placeholder(3),
		role, memberID, orgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// isValidOrgMemberRole reports whether role is a canonical tenant role
// permitted for organization members. platform_super_admin, legacy roles,
// and unknown/empty roles are rejected.
func isValidOrgMemberRole(role string) bool {
	switch role {
	case "tenant_admin", "tenant_operator", "tenant_support", "tenant_readonly":
		return true
	}
	return false
}

// ErrLastActiveAdmin is returned when a removal or suspension would
// leave an organization with zero active tenant admins — an org that
// can never be administered again is a self-inflicted lockout, not a
// legitimate operator action.
var ErrLastActiveAdmin = fmt.Errorf("cannot remove or suspend the organization's last active admin")

// ErrMemberNotFound is returned when memberID does not belong to orgID
// (or does not exist at all) — RemoveMember/SetMemberActive/
// UpdateMemberRole must never silently no-op a 0-row UPDATE/DELETE, that
// looks like success to a caller who then wrongly believes the action
// took effect.
var ErrMemberNotFound = fmt.Errorf("organization member not found")

// adminLikeRoles matches CountAdmins' own role set exactly (including
// the legacy 'admin'/'superadmin' roles it counts for pre-normalization
// data) — using a narrower set here would let a legacy-role admin's
// removal slip past the last-admin guard that CountAdmins itself would
// have caught.
func isAdminLikeRole(role string) bool {
	switch role {
	case "admin", "superadmin", "tenant_admin":
		return true
	}
	return false
}

func (s *Service) isLastActiveAdmin(ctx context.Context, memberID, orgID uint) (bool, error) {
	var role string
	var active int
	err := s.repo.db.QueryRowContext(ctx,
		"SELECT role, active FROM users WHERE id = "+s.repo.dialect.Placeholder(1)+" AND tenant_id = "+s.repo.dialect.Placeholder(2),
		memberID, orgID).Scan(&role, &active)
	if err != nil {
		return false, ErrMemberNotFound
	}
	if !isAdminLikeRole(role) || active == 0 {
		return false, nil // not an active admin at all; removing/suspending it can't be "the last one"
	}
	count, err := s.repo.CountAdmins(ctx, orgID)
	if err != nil {
		return false, err
	}
	return count <= 1, nil
}

// RemoveMember deletes memberID from orgID, refusing if memberID is the
// organization's last active tenant admin (see ErrLastActiveAdmin) or
// does not belong to orgID at all (see ErrMemberNotFound).
func (s *Service) RemoveMember(ctx context.Context, memberID, orgID uint) error {
	isLast, err := s.isLastActiveAdmin(ctx, memberID, orgID)
	if err != nil {
		return err
	}
	if isLast {
		return ErrLastActiveAdmin
	}
	res, err := s.repo.db.ExecContext(ctx,
		"DELETE FROM users WHERE id = "+s.repo.dialect.Placeholder(1)+" AND tenant_id = "+s.repo.dialect.Placeholder(2),
		memberID, orgID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

// SetMemberActive activates or suspends an individual organization
// member — distinct from SetOrganizationActive, which suspends the
// WHOLE organization. Suspending bumps token_version so any of that
// member's currently-valid JWTs are rejected on their very next
// request (ValidateAccessToken compares embedded token_version against
// the live row), not merely on their next login — an "active-immediately"
// session revocation, not an eventual one. Refuses to suspend the
// organization's last active tenant admin for the same lockout reason
// RemoveMember does.
func (s *Service) SetMemberActive(ctx context.Context, memberID, orgID uint, active bool) error {
	if !active {
		isLast, err := s.isLastActiveAdmin(ctx, memberID, orgID)
		if err != nil {
			return err
		}
		if isLast {
			return ErrLastActiveAdmin
		}
	}
	activeVal := 0
	if active {
		activeVal = 1
	}
	query := "UPDATE users SET active = " + s.repo.dialect.Placeholder(1) + " WHERE id = " + s.repo.dialect.Placeholder(2) + " AND tenant_id = " + s.repo.dialect.Placeholder(3)
	args := []any{activeVal, memberID, orgID}
	if !active {
		// Only bump token_version on suspend — reactivating does not
		// need to invalidate anything, and doing so would pointlessly
		// force a re-login the member never lost.
		query = "UPDATE users SET active = " + s.repo.dialect.Placeholder(1) + ", token_version = COALESCE(token_version, 0) + 1 WHERE id = " + s.repo.dialect.Placeholder(2) + " AND tenant_id = " + s.repo.dialect.Placeholder(3)
	}
	res, err := s.repo.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMemberNotFound
	}
	return nil
}

func (s *Service) GetSuspensionStatus(ctx context.Context, orgID uint) (*SuspensionRecord, error) {
	row := s.repo.db.QueryRowContext(ctx,
		`SELECT id, organization_id, reason, suspended_by, COALESCE(note, ''), suspended_at, reactivated_at, created_at
		FROM org_suspensions WHERE organization_id = `+s.repo.dialect.Placeholder(1)+` AND reactivated_at IS NULL
		ORDER BY id DESC LIMIT 1`, orgID)
	var rec SuspensionRecord
	err := row.Scan(&rec.ID, &rec.OrganizationID, &rec.Reason, &rec.SuspendedBy, &rec.Note, &rec.SuspendedAt, &rec.ReactivatedAt, &rec.CreatedAt)
	if err != nil {
		return nil, nil
	}
	return &rec, nil
}

func (s *Service) mutateWithAudit(ctx context.Context, entry *audit.ExtendedEntry, mutate func(*OrganizationRepo) error) error {
	if s.auditStore == nil {
		return mutate(s.repo)
	}
	tx, err := s.repo.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin organization mutation: %w", err)
	}
	defer tx.Rollback()
	if err := mutate(s.repo.WithTx(tx)); err != nil {
		return err
	}
	if err := s.auditStore.RecordTx(ctx, tx, entry); err != nil {
		return err
	}
	return tx.Commit()
}

var (
	ErrOrganizationNotFound = fmt.Errorf("organization not found")
	ErrOrganizationExists   = fmt.Errorf("organization already exists")
)
