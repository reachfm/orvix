package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/platform/importer"
	"github.com/orvix/orvix/internal/platform/kernel"
)

func (h *Handler) SetImportService(svc *importer.Service) {
	h.importSvc = svc
}

// ImportService returns the wired import service, or nil when the router
// could not construct it (e.g. missing staging directory). Handlers must
// 503 on nil rather than panic.
func (h *Handler) ImportService() *importer.Service { return h.importSvc }

// ── Tenant-scoped handlers ─────────────────────────────────────────

func (h *Handler) CreateImport(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)
	if tenantID == 0 && scope == "tenant" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant context required"})
	}

	body := c.Body()
	if len(body) == 0 || len(body) > int(importer.MaxSourceBytes) {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{"error": "body too large or empty"})
	}
	sourceType, err := importer.DetectSourceType(body)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unable to detect source type"})
	}

	params := importer.CreateImportParams{
		TenantID:       tenantID,
		Scope:          scope,
		Actor:          actorFromContext(c),
		SourceType:     sourceType,
		ConflictPolicy: conflictPolicyFromQuery(c),
		SchemaVersion:  1,
		SourceName:     c.Get("X-Import-Name", "upload"),
	}

	job, err := h.importSvc.Create(c.Context(), params, body)
	if err != nil {
		return errorResponse(c, err)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":          job.ID,
		"status":      job.Status,
		"source_type": job.SourceType,
		"source_hash": job.SourceHash,
	})
}

func (h *Handler) ListImports(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)

	filter := importer.ImportFilter{
		TenantID: tenantID,
		Scope:    scope,
		Status:   importer.ImportStatus(c.Query("status")),
		Page:     kernel.PageRequest{Page: importQueryInt(c, "page", 1), PageSize: importQueryInt(c, "page_size", 25)},
	}

	result, err := h.importSvc.List(c.Context(), filter)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(result)
}

func (h *Handler) GetImport(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)
	id := parseImportID(c)

	job, err := h.importSvc.Get(c.Context(), id, tenantID, scope)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(job)
}

func (h *Handler) ValidateImport(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)
	id := parseImportID(c)

	report, err := h.importSvc.Validate(c.Context(), id, tenantID, scope)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(report)
}

func (h *Handler) GetImportReport(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)
	id := parseImportID(c)

	report, err := h.importSvc.GetReport(c.Context(), id, tenantID, scope)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(report)
}

func (h *Handler) ExecuteImport(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)
	id := parseImportID(c)

	idempotencyKey := c.Get("Idempotency-Key")
	if idempotencyKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Idempotency-Key header is required"})
	}

	confirmation := c.Get("X-Import-Confirm", c.FormValue("confirm"))

	job, err := h.importSvc.Execute(c.Context(), id, tenantID, scope, idempotencyKey, confirmation)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"id": job.ID, "status": job.Status})
}

func (h *Handler) ResumeImport(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)
	id := parseImportID(c)

	idempotencyKey := c.Get("Idempotency-Key")
	if idempotencyKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Idempotency-Key header is required"})
	}

	job, err := h.importSvc.Resume(c.Context(), id, tenantID, scope, idempotencyKey)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"id": job.ID, "status": job.Status})
}

func (h *Handler) CancelImport(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)
	id := parseImportID(c)

	job, err := h.importSvc.Cancel(c.Context(), id, tenantID, scope)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"id": job.ID, "status": job.Status})
}

func (h *Handler) CompensateImport(c fiber.Ctx) error {
	if err := checkImportService(h, c); err != nil {
		return err
	}
	scope := importScope(c)
	tenantID := h.tenantIDForScope(c, scope)
	id := parseImportID(c)

	idempotencyKey := c.Get("Idempotency-Key")
	if idempotencyKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Idempotency-Key header is required"})
	}

	confirmation := c.Get("X-Import-Confirm", c.FormValue("confirm"))

	job, err := h.importSvc.Compensate(c.Context(), id, tenantID, scope, idempotencyKey, confirmation)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"id": job.ID, "status": job.Status})
}

// ── Helpers ────────────────────────────────────────────────────────

func checkImportService(h *Handler, c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	return nil
}

func importScope(c fiber.Ctx) string {
	path := c.Path()
	if contains(path, "/platform/imports") {
		return "platform"
	}
	return "tenant"
}

func (h *Handler) tenantIDForScope(c fiber.Ctx, scope string) uint {
	if scope == "platform" {
		return 0
	}
	return tenantIDFromContext(c)
}

func parseImportID(c fiber.Ctx) uint {
	var id uint
	fmt.Sscanf(c.Params("id"), "%d", &id)
	return id
}

func tenantIDFromContext(c fiber.Ctx) uint {
	if v := c.Locals("tenant_id"); v != nil {
		switch t := v.(type) {
		case uint:
			return t
		case int:
			return uint(t)
		case int64:
			return uint(t)
		}
	}
	return 0
}

func actorFromContext(c fiber.Ctx) string {
	if v := c.Locals("user_id"); v != nil {
		return fmt.Sprintf("%v", v)
	}
	return "anonymous"
}

func conflictPolicyFromQuery(c fiber.Ctx) importer.ConflictPolicy {
	policy := importer.ConflictPolicy(c.Query("conflict_policy"))
	if policy.Valid() {
		return policy
	}
	return importer.ConflictFail
}

func errorResponse(c fiber.Ctx, err error) error {
	if err == nil {
		return nil
	}
	kerr := kernel.AsAPIError(err)
	return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message, "code": string(kerr.Code)})
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findStr(s, substr))
}

func findStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func importQueryInt(c fiber.Ctx, key string, defaultVal int) int {
	val := c.Query(key)
	if val == "" {
		return defaultVal
	}
	var n int
	if _, err := fmt.Sscanf(val, "%d", &n); err == nil && n > 0 {
		return n
	}
	return defaultVal
}

var _ = json.Marshal
