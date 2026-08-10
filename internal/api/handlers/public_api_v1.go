package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	domainadmin "github.com/orvix/orvix/internal/admin/domain"
	mailboxadmin "github.com/orvix/orvix/internal/admin/mailbox"
	"github.com/orvix/orvix/internal/api/publicv1"
	"github.com/orvix/orvix/internal/platform/kernel"
)

func publicTenantID(c fiber.Ctx) (uint, error) {
	tenantID, ok := c.Locals("api_key_tenant_id").(uint)
	if !ok || tenantID == 0 {
		return 0, errors.New("tenant API key required")
	}
	return tenantID, nil
}

func parsePublicID(c fiber.Ctx, name string) (uint, error) {
	v, err := strconv.ParseUint(c.Params(name), 10, 64)
	if err != nil || v == 0 {
		return 0, errors.New("invalid id")
	}
	return uint(v), nil
}

func publicPage(c fiber.Ctx) (kernel.PageRequest, bool) {
	page := 1
	pageSize := kernel.DefaultPageSize
	var err error
	if raw := c.Query("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			_ = publicv1.WriteError(c, fiber.StatusBadRequest, "INVALID_PAGINATION", "page must be a positive integer.", publicv1.ErrorDetail{Field: "page", Reason: "must be at least 1"})
			return kernel.PageRequest{}, false
		}
	}
	if raw := c.Query("page_size"); raw != "" {
		pageSize, err = strconv.Atoi(raw)
		if err != nil || pageSize < 1 || pageSize > kernel.MaxPageSize {
			_ = publicv1.WriteError(c, fiber.StatusBadRequest, "INVALID_PAGINATION", fmt.Sprintf("page_size must be between 1 and %d.", kernel.MaxPageSize), publicv1.ErrorDetail{Field: "page_size", Reason: "outside allowed range"})
			return kernel.PageRequest{}, false
		}
	}
	return (kernel.PageRequest{Page: page, PageSize: pageSize, Search: c.Query("search")}).Normalize(), true
}

func pageMeta(req kernel.PageRequest, total int64) publicv1.PageMeta {
	totalPages := 0
	if total > 0 {
		totalPages = (int(total) + req.PageSize - 1) / req.PageSize
	}
	return publicv1.PageMeta{Page: req.Page, PageSize: req.PageSize, TotalCount: int(total), TotalPages: totalPages}
}

func publicMeta(c fiber.Ctx) publicv1.Metadata {
	return publicv1.Metadata{RequestID: publicv1.RequestID(c)}
}

func toPublicDomain(d domainadmin.AdminDomain) publicv1.Domain {
	return publicv1.Domain{ID: d.ID, Name: d.Name, Status: d.Status, Plan: d.Plan, Description: d.Description,
		MaxMailboxes: d.MaxMailboxes, MaxAliases: d.MaxAliases, MaxQuotaMB: d.MaxQuotaMB,
		DefaultMailboxQuotaMB: d.DefaultMailboxQuotaMB, MailboxCount: d.MailboxCount, AliasCount: d.AliasCount,
		StorageUsedBytes: d.StorageUsedBytes, CreatedAt: d.CreatedAt.UTC(), UpdatedAt: d.UpdatedAt.UTC(), DNSLastCheckedAt: utcTimePtr(d.DNSLastCheckedAt)}
}

func toPublicMailbox(m mailboxadmin.AdminMailbox) publicv1.Mailbox {
	return publicv1.Mailbox{ID: m.ID, DomainID: m.DomainID, Email: m.Email, LocalPart: m.LocalPart, Name: m.Name,
		Status: string(m.Status), QuotaMB: m.QuotaMB, UsedBytes: m.UsedBytes, MessageCount: m.MsgCount,
		SendLimitPerHour: m.SendLimit, CreatedAt: m.CreatedAt.UTC(), UpdatedAt: m.UpdatedAt.UTC()}
}

func utcTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func (h *Handler) PublicOrganization(c fiber.Ctx) error {
	if h.orgAdminSvc == nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Organization service is unavailable.")
	}
	tenantID, err := publicTenantID(c)
	if err != nil {
		return publicv1.WriteError(c, 403, "TENANT_REQUIRED", "A tenant-bound API key is required.")
	}
	o, err := h.orgAdminSvc.GetOrganization(c.Context(), tenantID)
	if err != nil || o == nil {
		return publicv1.WriteError(c, 404, "ORGANIZATION_NOT_FOUND", "Organization not found.")
	}
	data := publicv1.Organization{ID: o.ID, Name: o.Name, Slug: o.Slug, Domain: o.Domain, Plan: o.Plan, MaxDomains: o.MaxDomains, MaxMailboxes: o.MaxMailboxes, Active: o.Active, CreatedAt: o.CreatedAt.UTC(), UpdatedAt: o.UpdatedAt.UTC()}
	return c.JSON(struct {
		Data publicv1.Organization `json:"data"`
		Meta publicv1.Metadata     `json:"meta"`
	}{data, publicMeta(c)})
}

func (h *Handler) PublicListDomains(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Domain service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	p, ok := publicPage(c)
	if !ok {
		return nil
	}
	var status *string
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		if _, valid := domainadmin.ParseDomainStatus(raw); !valid {
			return publicv1.WriteError(c, 400, "INVALID_FILTER", "Unsupported domain status.", publicv1.ErrorDetail{Field: "status", Reason: "unsupported value"})
		}
		status = &raw
	}
	rows, total, err := h.domainAdminSvc.ListDomains(c.Context(), domainadmin.DomainFilter{TenantID: &tenantID, Status: status, Search: p.Search, Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	data := make([]publicv1.Domain, 0, len(rows))
	for _, row := range rows {
		data = append(data, toPublicDomain(row))
	}
	return c.JSON(publicv1.DomainList{Data: data, Page: pageMeta(p, total), Meta: publicMeta(c)})
}

func (h *Handler) PublicGetDomain(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Domain service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	id, err := parsePublicID(c, "id")
	if err != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid domain id.")
	}
	d, err := h.domainAdminSvc.GetDomain(c.Context(), id, tenantID)
	if err != nil || d == nil {
		return publicv1.WriteError(c, 404, "DOMAIN_NOT_FOUND", "Domain not found.")
	}
	return c.JSON(publicv1.DomainResponse{Data: toPublicDomain(*d), Meta: publicMeta(c)})
}

func (h *Handler) PublicCreateDomain(c fiber.Ctx) error {
	if h.domainAdminSvc == nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Domain service is unavailable.")
	}
	var req publicv1.CreateDomainRequest
	if err := c.Bind().JSON(&req); err != nil {
		return publicv1.WriteError(c, 400, "INVALID_REQUEST", "Invalid JSON request.")
	}
	normalized, err := domainadmin.ValidateDomainName(req.Name)
	if err != nil {
		return publicv1.WriteError(c, 422, domainadmin.CodeInvalidDomainName, "Invalid domain name.", publicv1.ErrorDetail{Field: "name", Reason: "invalid domain"})
	}
	tenantID, _ := publicTenantID(c)
	result, err := h.domainAdminSvc.ProvisionDomain(c.Context(), domainadmin.CreateDomainRequest{Name: normalized, Description: req.Description, Status: req.Status, MaxMailboxes: req.MaxMailboxes, MaxAliases: req.MaxAliases, MaxQuotaMB: req.MaxQuotaMB}, tenantID)
	if err != nil {
		return publicDomainError(c, err)
	}
	return c.Status(201).JSON(publicv1.DomainResponse{Data: toPublicDomain(*result.Domain), Meta: publicMeta(c)})
}

func (h *Handler) PublicUpdateDomain(c fiber.Ctx) error {
	var req publicv1.UpdateDomainRequest
	if err := c.Bind().JSON(&req); err != nil {
		return publicv1.WriteError(c, 400, "INVALID_REQUEST", "Invalid JSON request.")
	}
	tenantID, _ := publicTenantID(c)
	id, err := parsePublicID(c, "id")
	if err != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid domain id.")
	}
	d, err := h.domainAdminSvc.UpdateDomain(c.Context(), id, tenantID, domainadmin.UpdateDomainRequest{Description: req.Description, MaxMailboxes: req.MaxMailboxes, MaxAliases: req.MaxAliases, MaxQuotaMB: req.MaxQuotaMB})
	if err != nil {
		return publicDomainError(c, err)
	}
	return c.JSON(publicv1.DomainResponse{Data: toPublicDomain(*d), Meta: publicMeta(c)})
}

func (h *Handler) PublicSetDomainStatus(c fiber.Ctx) error {
	var req publicv1.StatusRequest
	if err := c.Bind().JSON(&req); err != nil {
		return publicv1.WriteError(c, 400, "INVALID_REQUEST", "Invalid JSON request.")
	}
	if _, ok := domainadmin.ParseDomainStatus(req.Status); !ok {
		return publicv1.WriteError(c, 422, domainadmin.CodeDomainStatusInvalid, "Unsupported domain status.")
	}
	tenantID, _ := publicTenantID(c)
	id, err := parsePublicID(c, "id")
	if err != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid domain id.")
	}
	if err := h.domainAdminSvc.SetDomainStatus(c.Context(), id, tenantID, req.Status, req.Reason); err != nil {
		return publicDomainError(c, err)
	}
	d, err := h.domainAdminSvc.GetDomain(c.Context(), id, tenantID)
	if err != nil {
		return publicDomainError(c, err)
	}
	return c.JSON(publicv1.DomainResponse{Data: toPublicDomain(*d), Meta: publicMeta(c)})
}

func (h *Handler) PublicDeleteDomain(c fiber.Ctx) error {
	tenantID, _ := publicTenantID(c)
	id, err := parsePublicID(c, "id")
	if err != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid domain id.")
	}
	if err := h.domainAdminSvc.DeleteDomain(c.Context(), id, tenantID); err != nil {
		return publicDomainError(c, err)
	}
	return c.JSON(publicv1.DeleteResponse{Deleted: true, Meta: publicMeta(c)})
}

func publicDomainError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, domainadmin.ErrDomainNotFound):
		return publicv1.WriteError(c, 404, domainadmin.CodeDomainNotFound, "Domain not found.")
	case errors.Is(err, domainadmin.ErrDomainAlreadyExists):
		return publicv1.WriteError(c, 409, domainadmin.CodeDomainAlreadyExists, "Domain already exists.")
	case errors.Is(err, domainadmin.ErrDomainHasMailboxes):
		return publicv1.WriteError(c, 409, domainadmin.CodeDomainHasMailboxes, "Domain has mailboxes and cannot be deleted.")
	case errors.Is(err, domainadmin.ErrDomainHasDependencies):
		return publicv1.WriteError(c, 409, domainadmin.CodeDomainHasDependencies, "Domain has dependencies and cannot be deleted.")
	case errors.Is(err, domainadmin.ErrInvalidDomainName), errors.Is(err, domainadmin.ErrInvalidDomainStatus), errors.Is(err, domainadmin.ErrInvalidLimit), errors.Is(err, domainadmin.ErrLimitContradiction):
		return publicv1.WriteError(c, 422, "VALIDATION_ERROR", "The domain request is invalid.")
	default:
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
}

func (h *Handler) PublicListMailboxes(c fiber.Ctx) error {
	if h.mailboxAdminSvc == nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Mailbox service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	p, ok := publicPage(c)
	if !ok {
		return nil
	}
	var domainID *uint
	if raw := c.Query("domain_id"); raw != "" {
		v, e := strconv.ParseUint(raw, 10, 64)
		if e != nil || v == 0 {
			return publicv1.WriteError(c, 400, "INVALID_FILTER", "Invalid domain_id.")
		}
		vv := uint(v)
		domainID = &vv
	}
	var status *mailboxadmin.AdminMailboxStatus
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		s := mailboxadmin.AdminMailboxStatus(raw)
		switch s {
		case mailboxadmin.AdminMailboxActive, mailboxadmin.AdminMailboxDisabled, mailboxadmin.AdminMailboxSuspended:
			status = &s
		default:
			return publicv1.WriteError(c, 400, "INVALID_FILTER", "Unsupported mailbox status.")
		}
	}
	rows, total, err := h.mailboxAdminSvc.ListMailboxes(c.Context(), mailboxadmin.MailboxFilter{TenantID: &tenantID, DomainID: domainID, Status: status, Search: p.Search, Limit: p.Limit(), Offset: p.Offset()})
	if err != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	data := make([]publicv1.Mailbox, 0, len(rows))
	for _, row := range rows {
		data = append(data, toPublicMailbox(row))
	}
	return c.JSON(publicv1.MailboxList{Data: data, Page: pageMeta(p, total), Meta: publicMeta(c)})
}

func (h *Handler) PublicGetMailbox(c fiber.Ctx) error {
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid mailbox id.")
	}
	m, e := h.mailboxAdminSvc.GetMailbox(c.Context(), id, tenantID)
	if e != nil || m == nil {
		return publicv1.WriteError(c, 404, "MAILBOX_NOT_FOUND", "Mailbox not found.")
	}
	return c.JSON(publicv1.MailboxResponse{Data: toPublicMailbox(*m), Meta: publicMeta(c)})
}

func (h *Handler) PublicCreateMailbox(c fiber.Ctx) error {
	var req publicv1.CreateMailboxRequest
	if e := c.Bind().JSON(&req); e != nil {
		return publicv1.WriteError(c, 400, "INVALID_REQUEST", "Invalid JSON request.")
	}
	if _, e := mail.ParseAddress(req.Email); e != nil || req.Password == "" {
		return publicv1.WriteError(c, 422, "VALIDATION_ERROR", "A valid email and password are required.")
	}
	tenantID, _ := publicTenantID(c)
	resp, e := h.mailboxAdminSvc.CreateMailbox(c.Context(), mailboxadmin.CreateMailboxRequest{Email: req.Email, Password: req.Password, Name: req.Name, QuotaMB: req.QuotaMB, SendLimit: req.SendLimitPerHour}, tenantID)
	if e != nil {
		return publicMailboxError(c, e)
	}
	return c.Status(201).JSON(publicv1.MailboxResponse{Data: toPublicMailbox(resp.Mailbox), Meta: publicMeta(c)})
}

func (h *Handler) PublicUpdateMailbox(c fiber.Ctx) error {
	var req publicv1.UpdateMailboxRequest
	if e := c.Bind().JSON(&req); e != nil {
		return publicv1.WriteError(c, 400, "INVALID_REQUEST", "Invalid JSON request.")
	}
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid mailbox id.")
	}
	m, e := h.mailboxAdminSvc.UpdateMailbox(c.Context(), id, tenantID, mailboxadmin.UpdateMailboxRequest{Name: req.Name, QuotaMB: req.QuotaMB, SendLimit: req.SendLimitPerHour, AllowSMTP: req.AllowSMTP, AllowIMAP: req.AllowIMAP, AllowPOP3: req.AllowPOP3, AllowJMAP: req.AllowJMAP})
	if e != nil {
		return publicMailboxError(c, e)
	}
	return c.JSON(publicv1.MailboxResponse{Data: toPublicMailbox(*m), Meta: publicMeta(c)})
}

func (h *Handler) PublicSetMailboxStatus(c fiber.Ctx) error {
	var req publicv1.StatusRequest
	if e := c.Bind().JSON(&req); e != nil {
		return publicv1.WriteError(c, 400, "INVALID_REQUEST", "Invalid JSON request.")
	}
	s := mailboxadmin.AdminMailboxStatus(req.Status)
	switch s {
	case mailboxadmin.AdminMailboxActive, mailboxadmin.AdminMailboxDisabled, mailboxadmin.AdminMailboxSuspended:
	default:
		return publicv1.WriteError(c, 422, "INVALID_STATUS", "Unsupported mailbox status.")
	}
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid mailbox id.")
	}
	if e = h.mailboxAdminSvc.SetStatus(c.Context(), id, tenantID, s, req.Reason); e != nil {
		return publicMailboxError(c, e)
	}
	m, e := h.mailboxAdminSvc.GetMailbox(c.Context(), id, tenantID)
	if e != nil {
		return publicMailboxError(c, e)
	}
	return c.JSON(publicv1.MailboxResponse{Data: toPublicMailbox(*m), Meta: publicMeta(c)})
}

func (h *Handler) PublicDeleteMailbox(c fiber.Ctx) error {
	tenantID, _ := publicTenantID(c)
	id, e := parsePublicID(c, "id")
	if e != nil {
		return publicv1.WriteError(c, 400, "INVALID_ID", "Invalid mailbox id.")
	}
	if e = h.mailboxAdminSvc.SoftDeleteMailbox(c.Context(), id, tenantID, "public API deletion"); e != nil {
		return publicMailboxError(c, e)
	}
	return c.JSON(publicv1.DeleteResponse{Deleted: true, Meta: publicMeta(c)})
}

func publicMailboxError(c fiber.Ctx, err error) error {
	if errors.Is(err, mailboxadmin.ErrMailboxNotFound) {
		return publicv1.WriteError(c, 404, "MAILBOX_NOT_FOUND", "Mailbox not found.")
	}
	return publicv1.WriteError(c, 422, "MAILBOX_OPERATION_FAILED", "The mailbox operation could not be completed.")
}

func (h *Handler) PublicUsage(c fiber.Ctx) error {
	if h.usageSvc == nil {
		return publicv1.WriteError(c, 503, "SERVICE_UNAVAILABLE", "Usage service is unavailable.")
	}
	tenantID, _ := publicTenantID(c)
	u, e := h.usageSvc.GetCurrentUsage(tenantID)
	if e != nil {
		return publicv1.WriteError(c, 500, "INTERNAL_ERROR", "The request could not be completed.")
	}
	data := publicv1.Usage{PeriodStart: u.PeriodStart.UTC(), PeriodEnd: u.PeriodEnd.UTC(), MailboxesUsed: u.MailboxesUsed, DomainsUsed: u.DomainsUsed, StorageUsedMB: u.StorageUsedMB, EmailsSent: u.EmailsSent, EmailsReceived: u.EmailsReceived, APICalls: u.APICalls}
	return c.JSON(publicv1.UsageResponse{Data: data, Meta: publicMeta(c)})
}

// publicSQLDB returns the shared database handle used by the alias/group
// adapters. These resources do not yet have a dedicated service, so this
// adapter owns their parameterized, tenant-scoped SQL without reusing the
// legacy handlers' SQLite-only queries.
func (h *Handler) publicSQLDB() (*sql.DB, error) {
	if h.db == nil {
		return nil, errors.New("database unavailable")
	}
	return h.db.DB()
}
