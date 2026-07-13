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

	created, err := s.repo.Create(ctx, o)
	if err != nil {
		return nil, err
	}

	s.recordAudit(ctx, "organization.create", fmt.Sprintf("tenant:%d", created.ID), created.ID, platformTenantID, "success", "")
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

	if err := s.repo.Update(ctx, o); err != nil {
		return nil, err
	}
	s.recordAudit(ctx, "organization.update", fmt.Sprintf("tenant:%d", id), id, id, "success", "")
	return o, nil
}

func (s *Service) SetOrganizationActive(ctx context.Context, id uint, active bool, reason string) error {
	_, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.SetActive(ctx, id, active); err != nil {
		return err
	}
	action := "organization.disable"
	if active {
		action = "organization.enable"
	}
	s.recordAudit(ctx, action, fmt.Sprintf("tenant:%d", id), id, id, "success", reason)
	return nil
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

var (
	ErrOrganizationNotFound = fmt.Errorf("organization not found")
	ErrOrganizationExists   = fmt.Errorf("organization already exists")
)

// Ensure bcrypt import is used
var _ = bcrypt.GenerateFromPassword
