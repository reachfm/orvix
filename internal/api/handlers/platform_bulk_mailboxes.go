package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/platform/bulkprovision"
	"github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// ── Platform bulk mailbox provisioning (Stage 8) ────────────────────
//
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/template            — GET, download template
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/stage                — stage an upload
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/validate             — dry run against a staged upload
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/jobs                 — create the durable job
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/execute  — submit for async durable execution
// GET  /api/v1/platform/mailboxes/bulk/:tenant_id/jobs                 — list jobs
// GET  /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId          — get one job
// GET  /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/rows     — paginated row report
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/cancel   — cancel
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/retry    — retry failed rows
//
// Security contract, matching platform_provisioning.go exactly:
//   - platformMW gate (platform_super_admin / super_admin + CSRF);
//   - canonical RBAC permission;
//   - explicit target tenant from the path, never inferred as tenant 0;
//   - strict JSON on JSON-bodied mutations; bounded multipart on the
//     upload step;
//   - required Idempotency-Key on staging and execute mutations;
//   - typed, redacted errors — no staging path, lease token, password,
//     secret, raw SQL, or internal stack ever reaches a response.
//
// A one-time encrypted-credential-download artifact is DELIBERATELY NOT
// implemented: this repository has no general-purpose secret-encryption
// service to back one safely (see bulkprovision.createOneMailbox's
// doc comment), so bulk-created mailboxes rely on ForcePasswordChange
// plus the platform's existing forgot-password/activation flow instead,
// per this feature's explicit "do not fabricate security" fallback.

// GetPlatformBulkMailboxTemplate handles
// GET /api/v1/platform/mailboxes/bulk/template?format=csv|xlsx. The
// template is generated on demand — nothing binary is committed to
// the repository.
func (h *Handler) GetPlatformBulkMailboxTemplate(c fiber.Ctx) error {
	format := strings.ToLower(c.Query("format", "csv"))
	switch format {
	case "xlsx":
		data, err := bulkprovision.TemplateXLSX()
		if err != nil {
			return errorResponse(c, kernel.Wrap(kernel.ErrCodeInternal, "generate template", err))
		}
		c.Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
		c.Set("Content-Disposition", `attachment; filename="bulk-mailbox-import-template.xlsx"`)
		c.Set("Cache-Control", "no-store")
		return c.Send(data)
	case "csv", "":
		c.Set("Content-Type", "text/csv; charset=utf-8")
		c.Set("Content-Disposition", `attachment; filename="bulk-mailbox-import-template.csv"`)
		c.Set("Cache-Control", "no-store")
		return c.Send(bulkprovision.TemplateCSV())
	default:
		return errorResponse(c, kernel.ValidationError(map[string]string{"format": "must be csv or xlsx"}))
	}
}

// bulkStageResponse is intentionally minimal: staging_id and
// source_hash are opaque, server-generated identifiers, never a
// filesystem path.
type bulkStageResponse struct {
	StagingID  string `json:"staging_id"`
	SourceHash string `json:"source_hash"`
	RowCount   int    `json:"row_count"`
	Format     string `json:"format"`
}

// sniffBulkUpload classifies the upload by CONTENT (magic bytes), not
// the client-declared filename/extension alone, and requires the two
// to agree — a mismatch is rejected outright rather than guessed at.
func sniffBulkUpload(filename string, data []byte) (format string, rows []bulkprovision.RawRow, err error) {
	ext := strings.ToLower(filepath.Ext(filename))
	isZIP := bytes.HasPrefix(data, []byte("PK\x03\x04"))
	switch {
	case ext == ".xlsx" && isZIP:
		rows, err = bulkprovision.ParseXLSX(data)
		return "xlsx", rows, err
	case ext == ".csv" && !isZIP:
		rows, err = bulkprovision.ParseCSV(data)
		return "csv", rows, err
	case ext == "" && !isZIP:
		// No extension: fall back to content alone for CSV, but never
		// for XLSX (a ZIP with no declared extension is refused rather
		// than assumed).
		rows, err = bulkprovision.ParseCSV(data)
		return "csv", rows, err
	default:
		return "", nil, bulkprovision.ErrUnsupportedFormat
	}
}

// PostPlatformBulkMailboxStage handles
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/stage. Accepts a
// bounded multipart upload (field "file"), classifies and parses it,
// and — only once parsing itself has succeeded — persists the exact
// validated bytes to the confined staging store, keyed by a
// server-generated ID and content hash.
func (h *Handler) PostPlatformBulkMailboxStage(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil || h.bulkMailboxStaging == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	fh, ferr := c.FormFile("file")
	if ferr != nil || fh == nil {
		return errorResponse(c, kernel.ValidationError(map[string]string{"file": "a multipart file upload named \"file\" is required"}))
	}
	if fh.Size <= 0 || fh.Size > bulkprovision.MaxUploadBytes {
		return errorResponse(c, bulkprovision.ErrUploadTooLarge)
	}
	f, oerr := fh.Open()
	if oerr != nil {
		return errorResponse(c, kernel.Wrap(kernel.ErrCodeInternal, "open upload", oerr))
	}
	defer f.Close()
	// Never trust the declared Size for the actual read bound: read at
	// most MaxUploadBytes+1 so an oversized/lied-about upload is
	// detected here rather than exhausting memory.
	data, rerr := io.ReadAll(io.LimitReader(f, bulkprovision.MaxUploadBytes+1))
	if rerr != nil {
		return errorResponse(c, kernel.Wrap(kernel.ErrCodeInternal, "read upload", rerr))
	}
	if int64(len(data)) > bulkprovision.MaxUploadBytes {
		return errorResponse(c, bulkprovision.ErrUploadTooLarge)
	}

	format, rows, perr := sniffBulkUpload(fh.Filename, data)
	if perr != nil {
		return bulkProvisionError(c, perr)
	}
	if len(rows) == 0 {
		return bulkProvisionError(c, bulkprovision.ErrEmptyFile)
	}

	stagingID, hash, _, serr := h.bulkMailboxStaging.Store(data, 0)
	if serr != nil {
		return errorResponse(c, kernel.Wrap(kernel.ErrCodeInternal, "stage upload", serr))
	}

	actorID := h.platformActorID(c)
	scope := "platform.bulkmailbox.stage:POST:tenant:" + strconv.FormatUint(uint64(tenantID), 10) + ":actor:" + strconv.FormatUint(uint64(actorID), 10) + ":hash:" + hash
	resp := bulkStageResponse{StagingID: stagingID, SourceHash: hash, RowCount: len(rows), Format: format}
	return h.platformIdempotent(c, scope, func() (int, any, any, error) {
		return fiber.StatusCreated, resp, resp, nil
	})
}

// readStagedRows re-reads and re-parses a staged upload, verifying its
// content hash first. Validate/CreateJob NEVER trust a client-supplied
// row list or validation result — only what is re-derived from the
// hash-verified staged bytes.
func (h *Handler) readStagedRows(stagingID, expectedHash, format string) ([]bulkprovision.RawRow, error) {
	if err := h.bulkMailboxStaging.Verify(stagingID, expectedHash); err != nil {
		return nil, bulkprovision.ErrSourceHashMismatch
	}
	data, err := h.bulkMailboxStaging.Read(stagingID)
	if err != nil {
		return nil, kernel.NotFound("staged upload")
	}
	switch format {
	case "xlsx":
		return bulkprovision.ParseXLSX(data)
	case "csv":
		return bulkprovision.ParseCSV(data)
	default:
		return nil, bulkprovision.ErrUnsupportedFormat
	}
}

type bulkValidateRequest struct {
	StagingID  string `json:"staging_id"`
	SourceHash string `json:"source_hash"`
	Format     string `json:"format"`
	DomainID   uint   `json:"domain_id"`
}

// PostPlatformBulkMailboxValidate handles
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/validate. Pure
// dry-run: never mutates.
func (h *Handler) PostPlatformBulkMailboxValidate(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil || h.bulkMailboxStaging == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	body, err := platformMutationBody(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req bulkValidateRequest
	if err := bindStrictJSONBytes(body, &req); err != nil {
		return strictJSONError(c, err)
	}
	if req.StagingID == "" || req.SourceHash == "" || req.DomainID == 0 {
		return errorResponse(c, kernel.ValidationError(map[string]string{"staging_id": "staging_id, source_hash and domain_id are required"}))
	}
	domainName, ok := h.platformResolveDomainName(c, tenantID, req.DomainID)
	if !ok {
		return errorResponse(c, kernel.NotFound("domain"))
	}
	rows, err := h.readStagedRows(req.StagingID, req.SourceHash, req.Format)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	result, err := h.bulkProvisionSvc.Validate(c.Context(), tenantID, req.DomainID, domainName, req.SourceHash, rows)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(result)
}

type bulkCreateJobRequest struct {
	StagingID      string                       `json:"staging_id"`
	SourceHash     string                       `json:"source_hash"`
	Format         string                       `json:"format"`
	DomainID       uint                         `json:"domain_id"`
	Strategy       bulkprovision.Strategy       `json:"strategy"`
	ConflictPolicy bulkprovision.ConflictPolicy `json:"conflict_policy"`
}

// PostPlatformBulkMailboxCreateJob handles
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/jobs. Re-validates
// server-side against the hash-verified staged bytes (never trusts a
// client-supplied validation result) and persists the durable job in
// "ready" state — still zero mailbox mutation.
func (h *Handler) PostPlatformBulkMailboxCreateJob(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil || h.bulkMailboxStaging == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	body, err := platformMutationBody(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req bulkCreateJobRequest
	if err := bindStrictJSONBytes(body, &req); err != nil {
		return strictJSONError(c, err)
	}
	if req.StagingID == "" || req.SourceHash == "" || req.DomainID == 0 {
		return errorResponse(c, kernel.ValidationError(map[string]string{"staging_id": "staging_id, source_hash and domain_id are required"}))
	}
	if req.Strategy == "" {
		req.Strategy = bulkprovision.StrategyPartial
	}
	if req.ConflictPolicy == "" {
		req.ConflictPolicy = bulkprovision.ConflictFail
	}
	domainName, ok := h.platformResolveDomainName(c, tenantID, req.DomainID)
	if !ok {
		return errorResponse(c, kernel.NotFound("domain"))
	}
	rows, err := h.readStagedRows(req.StagingID, req.SourceHash, req.Format)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	actorID := h.platformActorID(c)
	idemKey := c.Get("Idempotency-Key")
	if idemKey == "" {
		return errorResponse(c, kernel.ValidationError(map[string]string{"idempotency-key": "an Idempotency-Key header is required"}))
	}

	result, err := h.bulkProvisionSvc.Validate(c.Context(), tenantID, req.DomainID, domainName, req.SourceHash, rows)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	job, err := h.bulkProvisionSvc.CreateJob(c.Context(), tenantID, req.DomainID, actorID, req.Strategy, req.ConflictPolicy, idemKey, result)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"job": job})
}

// PostPlatformBulkMailboxExecute handles
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/execute.
// Submits the job for DURABLE ASYNC execution on the generic jobs
// framework — never runs inline in this request. Two identical
// submissions (same Idempotency-Key) execute exactly once.
func (h *Handler) PostPlatformBulkMailboxExecute(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	jobsSvc := h.AutomationJobsService()
	if jobsSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "durable job execution not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	jobID, err := parseIDParam(c, "jobId")
	if err != nil {
		return errorResponse(c, err)
	}
	job, err := h.bulkProvisionSvc.GetJobForHandler(c.Context(), jobID, tenantID)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	domainName, ok := h.platformResolveDomainName(c, tenantID, job.DomainID)
	if !ok {
		return errorResponse(c, kernel.NotFound("domain"))
	}
	idemKey := c.Get("Idempotency-Key")
	if idemKey == "" {
		return errorResponse(c, kernel.ValidationError(map[string]string{"idempotency-key": "an Idempotency-Key header is required"}))
	}
	payload, merr := marshalImportJobPayload(job.ID, domainName, job.SourceHash)
	if merr != nil {
		return errorResponse(c, kernel.Wrap(kernel.ErrCodeInternal, "build execution payload", merr))
	}
	actorID := h.platformActorID(c)
	submitted, _, serr := jobsSvc.Submit(c.Context(), jobs.Submission{
		TenantID: tenantID, Scope: jobs.ScopeTenant,
		Actor: "user:" + strconv.FormatUint(uint64(actorID), 10),
		Type:  bulkprovision.ImportJobType, PayloadVersion: bulkprovision.ImportJobPayloadVersion,
		Payload: payload, IdempotencyKey: idemKey, MaxAttempts: 5,
	})
	if serr != nil {
		return errorResponse(c, kernel.Wrap(kernel.ErrCodeInternal, "submit bulk mailbox import job", serr))
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"automation_job": submitted, "import_job_id": job.ID})
}

// GetPlatformBulkMailboxJob handles
// GET /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId.
func (h *Handler) GetPlatformBulkMailboxJob(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	jobID, err := parseIDParam(c, "jobId")
	if err != nil {
		return errorResponse(c, err)
	}
	job, err := h.bulkProvisionSvc.GetJobForHandler(c.Context(), jobID, tenantID)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"job": job})
}

// GetPlatformBulkMailboxJobRows handles
// GET /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/rows —
// the bounded, paginated row-result report.
func (h *Handler) GetPlatformBulkMailboxJobRows(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	jobID, err := parseIDParam(c, "jobId")
	if err != nil {
		return errorResponse(c, err)
	}
	if _, err := h.bulkProvisionSvc.GetJobForHandler(c.Context(), jobID, tenantID); err != nil {
		return bulkProvisionError(c, err)
	}
	limit, offset := boundedPage(c)
	rows, total, err := h.bulkProvisionSvc.ListRowsPage(c.Context(), jobID, limit, offset)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"rows": rows, "total": total, "limit": limit, "offset": offset})
}

// GetPlatformBulkMailboxJobs handles
// GET /api/v1/platform/mailboxes/bulk/:tenant_id/jobs.
func (h *Handler) GetPlatformBulkMailboxJobs(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	limit, offset := boundedPage(c)
	jobsList, total, err := h.bulkProvisionSvc.ListJobs(c.Context(), tenantID, limit, offset)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"jobs": jobsList, "total": total, "limit": limit, "offset": offset})
}

// PostPlatformBulkMailboxCancel handles
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/cancel.
func (h *Handler) PostPlatformBulkMailboxCancel(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	jobID, err := parseIDParam(c, "jobId")
	if err != nil {
		return errorResponse(c, err)
	}
	job, err := h.bulkProvisionSvc.Cancel(c.Context(), jobID, tenantID)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"job": job})
}

// PostPlatformBulkMailboxRetry handles
// POST /api/v1/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/retry.
func (h *Handler) PostPlatformBulkMailboxRetry(c fiber.Ctx) error {
	if h.bulkProvisionSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "bulk mailbox provisioning service not available"})
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	jobID, err := parseIDParam(c, "jobId")
	if err != nil {
		return errorResponse(c, err)
	}
	job, err := h.bulkProvisionSvc.GetJobForHandler(c.Context(), jobID, tenantID)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	domainName, ok := h.platformResolveDomainName(c, tenantID, job.DomainID)
	if !ok {
		return errorResponse(c, kernel.NotFound("domain"))
	}
	finalJob, rows, err := h.bulkProvisionSvc.RetryFailedRows(c.Context(), jobID, tenantID, domainName)
	if err != nil {
		return bulkProvisionError(c, err)
	}
	return c.JSON(fiber.Map{"job": finalJob, "rows": rows})
}

func (h *Handler) platformResolveDomainName(c fiber.Ctx, tenantID, domainID uint) (string, bool) {
	if h.domainAdminSvc == nil {
		return "", false
	}
	d, err := h.domainAdminSvc.GetDomain(c.Context(), domainID, tenantID)
	if err != nil || d == nil {
		return "", false
	}
	return d.Name, true
}

func boundedPage(c fiber.Ctx) (limit, offset int) {
	limit = queryIntDefault(c, "limit", 50)
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset = queryIntDefault(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func marshalImportJobPayload(importJobID uint, domainName, sourceHash string) ([]byte, error) {
	return json.Marshal(bulkprovision.ImportJobPayload{ImportJobID: importJobID, DomainName: domainName, SourceHash: sourceHash})
}
