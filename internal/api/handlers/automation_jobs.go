package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	platformjobs "github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
)

type automationJobRequest struct {
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	MaxAttempts int             `json:"max_attempts,omitempty"`
	RunAfter    *time.Time      `json:"run_after,omitempty"`
}

func (h *Handler) automationService(c fiber.Ctx) (*platformjobs.Service, error) {
	if h.jobSvc == nil {
		return nil, c.Status(503).JSON(fiber.Map{"error": fiber.Map{"code": "UNAVAILABLE", "message": "automation jobs are unavailable"}})
	}
	return h.jobSvc, nil
}

func automationActor(c fiber.Ctx) string {
	if id, ok := c.Locals("user_id").(uint); ok && id > 0 {
		if method, _ := c.Locals("auth_method").(string); method == "apikey" {
			if keyID, ok := c.Locals("api_key_id").(uint); ok && keyID > 0 {
				return fmt.Sprintf("api_key:%d:user:%d", keyID, id)
			}
		}
		return fmt.Sprintf("user:%d", id)
	}
	return ""
}

func automationTenantScope(c fiber.Ctx) (uint, platformjobs.Scope, error) {
	tenantID, err := auth.RequireTenantID(c)
	return tenantID, platformjobs.ScopeTenant, err
}

func automationPlatformScope(fiber.Ctx) (uint, platformjobs.Scope, error) {
	return 0, platformjobs.ScopePlatform, nil
}

type automationScopeResolver func(fiber.Ctx) (uint, platformjobs.Scope, error)

func decodeAutomationRequest(c fiber.Ctx, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(c.Body()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return kernel.ValidationError(map[string]string{"body": "request body must be valid JSON with known fields"})
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return kernel.ValidationError(map[string]string{"body": "request body must contain one JSON value"})
	}
	return nil
}

func writeAutomationError(c fiber.Ctx, err error) error {
	apiErr := kernel.AsAPIError(err)
	body := fiber.Map{"code": apiErr.Code, "message": apiErr.Message}
	if len(apiErr.Fields) > 0 {
		body["fields"] = apiErr.Fields
	}
	return c.Status(apiErr.HTTPStatus()).JSON(fiber.Map{"error": body})
}

func (h *Handler) submitAutomationJob(c fiber.Ctx, resolve automationScopeResolver) error {
	tenantID, scope, err := resolve(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": fiber.Map{"code": "FORBIDDEN", "message": "tenant context required"}})
	}
	service, err := h.automationService(c)
	if err != nil {
		return err
	}
	var request automationJobRequest
	if err = decodeAutomationRequest(c, &request); err != nil {
		return writeAutomationError(c, err)
	}
	idempotencyKey := strings.TrimSpace(c.Get("Idempotency-Key"))
	submission := platformjobs.Submission{TenantID: tenantID, Scope: scope, Actor: automationActor(c), Type: request.Type, Payload: request.Payload, IdempotencyKey: idempotencyKey, CorrelationID: strings.TrimSpace(c.Get("X-Request-ID")), MaxAttempts: request.MaxAttempts}
	if request.RunAfter != nil {
		submission.RunAfter = request.RunAfter.UTC()
	}
	job, replay, err := service.Submit(c.Context(), submission)
	if err != nil {
		return writeAutomationError(c, err)
	}
	action := "automation.job.submit"
	if replay {
		action = "automation.job.submit.replay"
	}
	h.writeAudit(c, action, fmt.Sprintf("job:%d type:%s", job.ID, job.Type))
	return c.Status(202).JSON(fiber.Map{"job": job, "idempotent_replay": replay})
}

func (h *Handler) listAutomationJobs(c fiber.Ctx, resolve automationScopeResolver) error {
	tenantID, scope, err := resolve(c)
	if err != nil {
		return c.Status(403).JSON(fiber.Map{"error": fiber.Map{"code": "FORBIDDEN", "message": "tenant context required"}})
	}
	service, err := h.automationService(c)
	if err != nil {
		return err
	}
	page, err := strconv.Atoi(c.Query("page", "1"))
	if err != nil || page < 1 {
		return writeAutomationError(c, kernel.ValidationError(map[string]string{"page": "must be a positive integer"}))
	}
	pageSize, err := strconv.Atoi(c.Query("page_size", "25"))
	if err != nil || pageSize < 1 || pageSize > kernel.MaxPageSize {
		return writeAutomationError(c, kernel.ValidationError(map[string]string{"page_size": "must be between 1 and 200"}))
	}
	result, err := service.List(c.Context(), platformjobs.ListFilter{TenantID: tenantID, Scope: scope, Status: platformjobs.Status(c.Query("status")), Type: c.Query("type"), Page: kernel.PageRequest{Page: page, PageSize: pageSize}})
	if err != nil {
		return writeAutomationError(c, err)
	}
	return c.JSON(result)
}

func automationJobID(c fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, kernel.ValidationError(map[string]string{"id": "invalid automation job id"})
	}
	return uint(id), nil
}

func (h *Handler) getAutomationJob(c fiber.Ctx, resolve automationScopeResolver) error {
	tenantID, scope, err := resolve(c)
	if err != nil {
		return writeAutomationError(c, kernel.Forbidden("tenant context required"))
	}
	id, err := automationJobID(c)
	if err != nil {
		return writeAutomationError(c, err)
	}
	service, err := h.automationService(c)
	if err != nil {
		return err
	}
	job, err := service.Get(c.Context(), id, tenantID, scope)
	if err != nil {
		return writeAutomationError(c, err)
	}
	return c.JSON(fiber.Map{"job": job})
}

func (h *Handler) cancelAutomationJob(c fiber.Ctx, resolve automationScopeResolver) error {
	tenantID, scope, err := resolve(c)
	if err != nil {
		return writeAutomationError(c, kernel.Forbidden("tenant context required"))
	}
	id, err := automationJobID(c)
	if err != nil {
		return writeAutomationError(c, err)
	}
	service, err := h.automationService(c)
	if err != nil {
		return err
	}
	job, err := service.RequestCancellation(c.Context(), id, tenantID, scope)
	if err != nil {
		return writeAutomationError(c, err)
	}
	h.writeAudit(c, "automation.job.cancel", fmt.Sprintf("job:%d", id))
	return c.JSON(fiber.Map{"job": job})
}

func (h *Handler) retryAutomationJob(c fiber.Ctx, resolve automationScopeResolver) error {
	tenantID, scope, err := resolve(c)
	if err != nil {
		return writeAutomationError(c, kernel.Forbidden("tenant context required"))
	}
	id, err := automationJobID(c)
	if err != nil {
		return writeAutomationError(c, err)
	}
	service, err := h.automationService(c)
	if err != nil {
		return err
	}
	job, replay, err := service.ManualRetry(c.Context(), id, tenantID, scope, strings.TrimSpace(c.Get("Idempotency-Key")))
	if err != nil {
		return writeAutomationError(c, err)
	}
	h.writeAudit(c, "automation.job.retry", fmt.Sprintf("job:%d replay:%t", id, replay))
	return c.JSON(fiber.Map{"job": job, "idempotent_replay": replay})
}

func (h *Handler) SubmitTenantAutomationJob(c fiber.Ctx) error {
	return h.submitAutomationJob(c, automationTenantScope)
}
func (h *Handler) ListTenantAutomationJobs(c fiber.Ctx) error {
	return h.listAutomationJobs(c, automationTenantScope)
}
func (h *Handler) GetTenantAutomationJob(c fiber.Ctx) error {
	return h.getAutomationJob(c, automationTenantScope)
}
func (h *Handler) CancelTenantAutomationJob(c fiber.Ctx) error {
	return h.cancelAutomationJob(c, automationTenantScope)
}
func (h *Handler) RetryTenantAutomationJob(c fiber.Ctx) error {
	return h.retryAutomationJob(c, automationTenantScope)
}
func (h *Handler) SubmitPlatformAutomationJob(c fiber.Ctx) error {
	return h.submitAutomationJob(c, automationPlatformScope)
}
func (h *Handler) ListPlatformAutomationJobs(c fiber.Ctx) error {
	return h.listAutomationJobs(c, automationPlatformScope)
}
func (h *Handler) GetPlatformAutomationJob(c fiber.Ctx) error {
	return h.getAutomationJob(c, automationPlatformScope)
}
func (h *Handler) CancelPlatformAutomationJob(c fiber.Ctx) error {
	return h.cancelAutomationJob(c, automationPlatformScope)
}
func (h *Handler) RetryPlatformAutomationJob(c fiber.Ctx) error {
	return h.retryAutomationJob(c, automationPlatformScope)
}
