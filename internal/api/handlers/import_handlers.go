package handlers

import (
	"encoding/json"
	"fmt"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/platform/importer"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// SetImportService wires the import bounded context into the handler.
func (h *Handler) SetImportService(svc *importer.Service) {
	h.importSvc = svc
}

// CreateImport handles POST /api/v1/imports
func (h *Handler) CreateImport(c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	tenantID := tenantIDFromContext(c)
	if tenantID == 0 {
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

	hash := importer.HashSource(body)
	actor := actorFromContext(c)

	params := importer.CreateImportParams{
		TenantID:       tenantID,
		Scope:          "tenant",
		Actor:          actor,
		SourceType:     sourceType,
		ConflictPolicy: conflictPolicyFromRequest(c, body),
		SchemaVersion:  1,
		SourceHash:     hash,
		SourceName:     c.Get("X-Import-Name", "upload"),
	}

	job, err := h.importSvc.Create(c.Context(), params)
	if err != nil {
		kerr := kernel.AsAPIError(err)
		return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":          job.ID,
		"status":      job.Status,
		"source_type": job.SourceType,
		"source_hash": job.SourceHash,
	})
}

// ListImports handles GET /api/v1/imports
func (h *Handler) ListImports(c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	tenantID := tenantIDFromContext(c)
	if tenantID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant context required"})
	}

	filter := importer.ImportFilter{
		TenantID: tenantID,
		Scope:    "tenant",
		Status:   importer.ImportStatus(c.Query("status")),
		Page:     kernel.PageRequest{Page: importQueryInt(c, "page", 1), PageSize: importQueryInt(c, "page_size", 25)},
	}

	result, err := h.importSvc.List(c.Context(), filter)
	if err != nil {
		kerr := kernel.AsAPIError(err)
		return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message})
	}
	return c.JSON(result)
}

// GetImport handles GET /api/v1/imports/:id
func (h *Handler) GetImport(c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	tenantID := tenantIDFromContext(c)
	id := parseID(c, "id")

	job, err := h.importSvc.Get(c.Context(), id, tenantID, "tenant")
	if err != nil {
		kerr := kernel.AsAPIError(err)
		return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message})
	}
	return c.JSON(job)
}

// ValidateImport handles POST /api/v1/imports/:id/validate
func (h *Handler) ValidateImport(c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	tenantID := tenantIDFromContext(c)
	id := parseID(c, "id")

	body := c.Body()
	if len(body) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "body is required"})
	}

	report, err := h.importSvc.Validate(c.Context(), id, tenantID, "tenant", body)
	if err != nil {
		kerr := kernel.AsAPIError(err)
		return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message})
	}
	return c.JSON(report)
}

// GetImportReport handles GET /api/v1/imports/:id/report
func (h *Handler) GetImportReport(c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	tenantID := tenantIDFromContext(c)
	id := parseID(c, "id")

	report, err := h.importSvc.GetReport(c.Context(), id, tenantID, "tenant")
	if err != nil {
		kerr := kernel.AsAPIError(err)
		return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message})
	}
	return c.JSON(report)
}

// ExecuteImport handles POST /api/v1/imports/:id/execute
func (h *Handler) ExecuteImport(c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	tenantID := tenantIDFromContext(c)
	id := parseID(c, "id")

	idempotencyKey := c.Get("Idempotency-Key", "X-Idempotency-Key")
	if idempotencyKey == "" {
		idempotencyKey = c.Query("idempotency_key")
	}
	if idempotencyKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Idempotency-Key header is required"})
	}

	body := c.Body()
	job, err := h.importSvc.Execute(c.Context(), id, tenantID, "tenant", body, idempotencyKey)
	if err != nil {
		kerr := kernel.AsAPIError(err)
		return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message})
	}
	return c.JSON(fiber.Map{"id": job.ID, "status": job.Status, "succeeded_rows": job.SucceededRows, "failed_rows": job.FailedRows})
}

// CancelImport handles POST /api/v1/imports/:id/cancel
func (h *Handler) CancelImport(c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	tenantID := tenantIDFromContext(c)
	id := parseID(c, "id")

	job, err := h.importSvc.Cancel(c.Context(), id, tenantID, "tenant")
	if err != nil {
		kerr := kernel.AsAPIError(err)
		return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message})
	}
	return c.JSON(fiber.Map{"id": job.ID, "status": job.Status})
}

// CompensateImport handles POST /api/v1/imports/:id/compensate
func (h *Handler) CompensateImport(c fiber.Ctx) error {
	if h.importSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "import service not available"})
	}
	tenantID := tenantIDFromContext(c)
	id := parseID(c, "id")

	job, err := h.importSvc.Compensate(c.Context(), id, tenantID, "tenant")
	if err != nil {
		kerr := kernel.AsAPIError(err)
		return c.Status(kerr.HTTPStatus()).JSON(fiber.Map{"error": kerr.Message})
	}
	return c.JSON(fiber.Map{"id": job.ID, "status": job.Status})
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

func conflictPolicyFromRequest(c fiber.Ctx, body []byte) importer.ConflictPolicy {
	policy := c.Query("conflict_policy")
	if policy == "" {
		var env struct {
			ConflictPolicy string `json:"conflict_policy"`
		}
		if json.Unmarshal(body, &env) == nil && env.ConflictPolicy != "" {
			policy = env.ConflictPolicy
		}
	}
	if importer.ConflictPolicy(policy).Valid() {
		return importer.ConflictPolicy(policy)
	}
	return importer.ConflictFail
}

func parseID(c fiber.Ctx, param string) uint {
	var id uint
	fmt.Sscanf(c.Params(param), "%d", &id)
	return id
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
