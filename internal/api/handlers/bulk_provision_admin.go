package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/platform/bulkprovision"
)

// PostBulkProvisionValidate handles POST /admin/domains/:id/mailboxes/bulk/validate.
// Dry-run only — never mutates.
func (h *Handler) PostBulkProvisionValidate(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk provisioning service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	domainID, domainName, ok := h.resolveBulkDomain(c, tenantID)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or unknown domain"})
	}
	raw, err := parseBulkInput(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	res, err := h.bulkProvisionSvc.Validate(c.Context(), tenantID, domainID, domainName, raw)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(res)
}

// PostBulkProvisionCreateJob handles POST /admin/domains/:id/mailboxes/bulk/jobs.
// Runs Validate again server-side (never trusts a client-supplied
// validation result) and persists the durable job.
func (h *Handler) PostBulkProvisionCreateJob(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk provisioning service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	actorID, _ := c.Locals("user_id").(uint)
	domainID, domainName, ok := h.resolveBulkDomain(c, tenantID)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid or unknown domain"})
	}
	raw, err := parseBulkInput(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	strategy := bulkprovision.Strategy(c.Query("strategy", string(bulkprovision.StrategyPartial)))
	idemKey := c.Get("Idempotency-Key")

	res, err := h.bulkProvisionSvc.Validate(c.Context(), tenantID, domainID, domainName, raw)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	job, err := h.bulkProvisionSvc.CreateJob(c.Context(), tenantID, domainID, actorID, strategy, idemKey, res)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"job": job})
}

// PostBulkProvisionExecute handles POST /admin/mailboxes/bulk/jobs/:jobId/execute.
func (h *Handler) PostBulkProvisionExecute(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk provisioning service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	jobID, ok := parseBulkJobID(c)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid job id"})
	}
	job, err := h.bulkProvisionSvc.GetJobForHandler(c.Context(), jobID, tenantID)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	domainName, err := h.domainAdminSvc.GetDomainNameByID(c.Context(), job.DomainID, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	finalJob, rows, err := h.bulkProvisionSvc.Execute(c.Context(), jobID, tenantID, job.DomainID, domainName)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"job": finalJob, "rows": rows})
}

// PostBulkProvisionCancel handles POST /admin/mailboxes/bulk/jobs/:jobId/cancel.
func (h *Handler) PostBulkProvisionCancel(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk provisioning service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	jobID, ok := parseBulkJobID(c)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid job id"})
	}
	job, err := h.bulkProvisionSvc.Cancel(c.Context(), jobID, tenantID)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"job": job})
}

// PostBulkProvisionRetry handles POST /admin/mailboxes/bulk/jobs/:jobId/retry.
func (h *Handler) PostBulkProvisionRetry(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk provisioning service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	jobID, ok := parseBulkJobID(c)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid job id"})
	}
	job, err := h.bulkProvisionSvc.GetJobForHandler(c.Context(), jobID, tenantID)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	domainName, err := h.domainAdminSvc.GetDomainNameByID(c.Context(), job.DomainID, tenantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	finalJob, rows, err := h.bulkProvisionSvc.RetryFailedRows(c.Context(), jobID, tenantID, domainName)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"job": finalJob, "rows": rows})
}

// GetBulkProvisionJob handles GET /admin/mailboxes/bulk/jobs/:jobId — the
// downloadable structured result (job + all rows).
func (h *Handler) GetBulkProvisionJob(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk provisioning service not available"})
	}
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	jobID, ok := parseBulkJobID(c)
	if !ok {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid job id"})
	}
	job, rows, err := h.bulkProvisionSvc.GetJobWithRows(c.Context(), jobID, tenantID)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"job": job, "rows": rows})
}

func (h *Handler) resolveBulkDomain(c fiber.Ctx, tenantID uint) (uint, string, bool) {
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 || h.domainAdminSvc == nil {
		return 0, "", false
	}
	d, err := h.domainAdminSvc.GetDomain(c.Context(), uint(idVal), tenantID)
	if err != nil || d == nil {
		return 0, "", false
	}
	return d.ID, d.Name, true
}

func parseBulkJobID(c fiber.Ctx) (uint, bool) {
	v, err := strconv.ParseUint(c.Params("jobId"), 10, 64)
	if err != nil || v == 0 {
		return 0, false
	}
	return uint(v), true
}

// parseBulkInput accepts either multipart/CSV file upload (field
// "file") or a raw JSON array body, selected by Content-Type.
func parseBulkInput(c fiber.Ctx) ([]bulkprovision.RawRow, error) {
	ct := c.Get("Content-Type")
	if fh, ferr := c.FormFile("file"); ferr == nil && fh != nil {
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		defer f.Close()
		buf := make([]byte, fh.Size)
		if _, err := f.Read(buf); err != nil {
			return nil, err
		}
		return bulkprovision.ParseCSV(buf)
	}
	if len(c.Body()) > 0 {
		if ct == "text/csv" {
			return bulkprovision.ParseCSV(c.Body())
		}
		return bulkprovision.ParseJSON(c.Body())
	}
	return nil, bulkprovision.ErrEmptyFile
}

func bulkProvisionError(c fiber.Ctx, err error) error {
	switch err {
	case bulkprovision.ErrJobNotFound:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	case bulkprovision.ErrEmptyFile, bulkprovision.ErrTooManyRows, bulkprovision.ErrUnsupportedFormat:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	case bulkprovision.ErrJobNotReady, bulkprovision.ErrJobNotCancellable, bulkprovision.ErrJobNotRetryable, bulkprovision.ErrVersionConflict:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
}
