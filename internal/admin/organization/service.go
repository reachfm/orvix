package organization

import (
	"context"
	"fmt"

	"golang.org/x/crypto/bcrypt"

	"github.com/orvix/orvix/internal/audit"
	entrbac "github.com/orvix/orvix/internal/enterprise/rbac"
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
		Name:         req.Name,
		Slug:         req.Slug,
		Domain:       req.Domain,
		Plan:         req.Plan,
		MaxDomains:   req.MaxDomains,
		MaxMailboxes: req.MaxMailboxes,
		Active:       true,
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
		return nil, err
	}
	return created, nil
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
	if err := s.mutateWithAudit(ctx, entry, func(repo *OrganizationRepo) error { return repo.Update(ctx, o) }); err != nil {
		return nil, err
	}
	return o, nil
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
	return s.mutateWithAudit(ctx, entry, func(repo *OrganizationRepo) error {
		return repo.SetActive(ctx, id, active)
	})
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
	return detail, nil
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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit organization mutation: %w", err)
	}
	return nil
}

var (
	ErrOrganizationNotFound = fmt.Errorf("organization not found")
	ErrOrganizationExists   = fmt.Errorf("organization already exists")
)

// Ensure bcrypt import is used
var _ = bcrypt.GenerateFromPassword
