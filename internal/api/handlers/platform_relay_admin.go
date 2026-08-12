package handlers

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/platform/relay"
)

// ── Platform relay administration (Mail-Control Phase B) ───────────
//
// Every route is platformMW-gated (platform_super_admin / legacy
// super_admin only) and RBAC-permissioned (relay.read / relay.write /
// relay.test). Mutations require:
//   - strict JSON (a malformed body is a 400, never a silent default);
//   - CSRF (enforced by platformMW);
//   - an Idempotency-Key header for create/update/rotate/test — a
//     retried request replays the original redacted response, and a
//     changed request body under the same key conflicts;
//   - typed confirmation (X-Confirm) for delete/disable/rotate;
//   - a current version for guarded (optimistic-concurrency) updates.
//
// Credentials: encrypted at rest by the service; never returned by
// any read; never present in responses, audit records, outbox events,
// or errors.

// relayService returns the wired relay service or a typed 503.
func (h *Handler) relayService() (*relay.Service, error) {
	if h.relaySvc == nil {
		return nil, kernel.NewError(kernel.ErrCodeUnavailable, "relay service is unavailable")
	}
	return h.relaySvc, nil
}

// relayActor builds the audit actor from the request context.
func (h *Handler) relayActor(c fiber.Ctx) relay.AuditActor {
	role, _ := c.Locals("role").(auth.Role)
	return relay.AuditActor{
		ID:        h.platformActorID(c),
		Role:      string(role),
		RequestID: strings.TrimSpace(c.Get("X-Request-ID")),
		IP:        c.IP(),
		UserAgent: c.Get("User-Agent"),
	}
}

// platformIdempotent enforces the platform mutation idempotency
// contract for the given scope. The request body hash binds the key to
// the exact payload: a replay with the same body returns the stored
// response; a different body under the same key is a 409 conflict; a
// missing key is a 400. On handler failure the in-flight claim is
// abandoned so a client retry is treated as a fresh attempt.
func (h *Handler) platformIdempotent(c fiber.Ctx, scope string, run func() (int, any, error)) error {
	if h.platformIdem == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "idempotent platform mutations are unavailable",
			"code":  string(kernel.ErrCodeUnavailable),
		})
	}
	key := strings.TrimSpace(c.Get("Idempotency-Key"))
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "Idempotency-Key header is required",
			"code":  string(kernel.ErrCodeValidation),
		})
	}
	hash := kernel.RequestHash(c.Body())
	stored, replay, err := h.platformIdem.Begin(c.Context(), scope, key, hash, time.Now().UTC())
	if err != nil {
		if errors.Is(err, kernel.ErrIdempotencyInFlight) {
			return c.Status(fiber.StatusConflict).JSON(fiber.Map{
				"error": "a request with this idempotency key is already in progress",
				"code":  string(kernel.ErrCodeConflict),
			})
		}
		return errorResponse(c, err)
	}
	if replay {
		c.Set("Content-Type", "application/json")
		c.Set("X-Idempotency-Replay", "true")
		return c.Status(stored.StatusCode).SendString(stored.ResponseBody)
	}
	status, body, runErr := run()
	if runErr != nil {
		_ = h.platformIdem.Abandon(c.Context(), scope, key)
		return errorResponse(c, runErr)
	}
	if err := h.platformIdem.Complete(c.Context(), scope, key, status, body, time.Now().UTC()); err != nil {
		return errorResponse(c, err)
	}
	return c.Status(status).JSON(body)
}

// typedConfirm enforces the X-Confirm typed-confirmation convention
// for destructive platform actions (mirrors the Phase A mailbox purge
// convention). Confirmation never travels in query strings. Returns a
// non-nil *fiber.Error so callers can simply `return err` — fiber
// renders it with the exact 428 status.
func typedConfirm(c fiber.Ctx, expected string) error {
	typed := strings.TrimSpace(c.Get("X-Confirm"))
	if typed == "" || typed != expected {
		return fiber.NewError(fiber.StatusPreconditionRequired, "typed confirmation required")
	}
	return nil
}

// platformRelayID parses the :id path parameter.
func platformRelayID(c fiber.Ctx) (uint, error) {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || id == 0 {
		return 0, kernel.NewError(kernel.ErrCodeValidation, "a valid relay id is required")
	}
	return uint(id), nil
}

// relayVersionFromBody extracts the guarded version from a strict JSON
// body.
func relayVersionFromBody(c fiber.Ctx) (int, error) {
	var req struct {
		Version int `json:"version"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return 0, kernel.NewError(kernel.ErrCodeValidation, "invalid request body")
	}
	if req.Version <= 0 {
		return 0, kernel.NewError(kernel.ErrCodeValidation, "a current version is required")
	}
	return req.Version, nil
}

// ListPlatformRelays handles GET /api/v1/platform/relays.
func (h *Handler) ListPlatformRelays(c fiber.Ctx) error {
	svc, err := h.relayService()
	if err != nil {
		return errorResponse(c, err)
	}
	limit := queryIntDefault(c, "limit", 50)
	if limit < 1 || limit > 200 {
		limit = 50
	}
	offset := queryIntDefault(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	f := relay.ProviderFilter{
		Scope:  relay.Scope(strings.TrimSpace(c.Query("scope"))),
		Search: strings.TrimSpace(c.Query("q")),
		Limit:  limit,
		Offset: offset,
	}
	if v := strings.TrimSpace(c.Query("tenant_id")); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			f.TenantID = uintPtr(n)
		}
	}
	if v := strings.TrimSpace(c.Query("domain_id")); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			f.DomainID = uintPtr(n)
		}
	}
	if v := strings.TrimSpace(c.Query("active")); v != "" {
		b := v == "true" || v == "1"
		f.Active = &b
	}
	relays, total, err := svc.ListRelays(c.Context(), f)
	if err != nil {
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"relays": relays, "total": total, "limit": limit, "offset": offset})
}

// GetPlatformRelay handles GET /api/v1/platform/relays/:id.
func (h *Handler) GetPlatformRelay(c fiber.Ctx) error {
	svc, err := h.relayService()
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := platformRelayID(c)
	if err != nil {
		return errorResponse(c, err)
	}
	r, err := svc.GetRelay(c.Context(), id)
	if err != nil {
		if errors.Is(err, relay.ErrProviderNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "relay not found", "code": "NOT_FOUND"})
		}
		return errorResponse(c, err)
	}
	return c.JSON(r)
}

// CreatePlatformRelay handles POST /api/v1/platform/relays.
func (h *Handler) CreatePlatformRelay(c fiber.Ctx) error {
	svc, err := h.relayService()
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		Scope           string `json:"scope"`
		TenantID        uint   `json:"tenant_id"`
		DomainID        uint   `json:"domain_id"`
		PoolID          uint   `json:"pool_id"`
		Name            string `json:"name"`
		Host            string `json:"host"`
		Port            int    `json:"port"`
		Username        string `json:"username"`
		Password        string `json:"password,omitempty"`
		ConnSecurity    string `json:"conn_security"`
		TLSValidation   string `json:"tls_validation"`
		Priority        int    `json:"priority"`
		Weight          int    `json:"weight"`
		Active          bool   `json:"active"`
		RateLimitPerMin int    `json:"rate_limit_per_min"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body", "code": "VALIDATION_FAILED"})
	}
	if req.ConnSecurity == "" {
		req.ConnSecurity = string(relay.ConnSecurityStartTLS)
	}
	if req.TLSValidation == "" {
		req.TLSValidation = string(relay.TLSValidationStrict)
	}
	in := relay.RelayCreateInput{
		Scope: relay.Scope(req.Scope), TenantID: req.TenantID, DomainID: req.DomainID, PoolID: req.PoolID,
		Name: req.Name, Host: req.Host, Port: req.Port, Username: req.Username, Password: req.Password,
		ConnSecurity: relay.ConnSecurity(req.ConnSecurity), TLSValidation: relay.TLSValidation(req.TLSValidation),
		Priority: req.Priority, Weight: req.Weight, Active: req.Active, RateLimitPerMin: req.RateLimitPerMin,
	}
	return h.platformIdempotent(c, "platform.relay.create", func() (int, any, error) {
		created, err := svc.CreateRelay(c.Context(), in, h.relayActor(c))
		if err != nil {
			return 0, nil, relayMutationError(err)
		}
		return fiber.StatusCreated, created, nil
	})
}

// UpdatePlatformRelay handles PATCH /api/v1/platform/relays/:id.
func (h *Handler) UpdatePlatformRelay(c fiber.Ctx) error {
	svc, err := h.relayService()
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := platformRelayID(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req struct {
		Version         int     `json:"version"`
		Scope           *string `json:"scope"`
		TenantID        *uint   `json:"tenant_id"`
		DomainID        *uint   `json:"domain_id"`
		PoolID          *uint   `json:"pool_id"`
		Name            *string `json:"name"`
		Host            *string `json:"host"`
		Port            *int    `json:"port"`
		Username        *string `json:"username"`
		Password        *string `json:"password"`
		ConnSecurity    *string `json:"conn_security"`
		TLSValidation   *string `json:"tls_validation"`
		Priority        *int    `json:"priority"`
		Weight          *int    `json:"weight"`
		Active          *bool   `json:"active"`
		RateLimitPerMin *int    `json:"rate_limit_per_min"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Version <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body: a positive version is required", "code": "VALIDATION_FAILED"})
	}
	in := relay.RelayUpdateInput{
		TenantID: req.TenantID, DomainID: req.DomainID, PoolID: req.PoolID,
		Name: req.Name, Host: req.Host, Port: req.Port, Username: req.Username, Password: req.Password,
		Priority: req.Priority, Weight: req.Weight, Active: req.Active, RateLimitPerMin: req.RateLimitPerMin,
	}
	if req.Scope != nil {
		v := relay.Scope(*req.Scope)
		in.Scope = &v
	}
	if req.ConnSecurity != nil {
		v := relay.ConnSecurity(*req.ConnSecurity)
		in.ConnSecurity = &v
	}
	if req.TLSValidation != nil {
		v := relay.TLSValidation(*req.TLSValidation)
		in.TLSValidation = &v
	}
	return h.platformIdempotent(c, "platform.relay.update:"+strconv.FormatUint(uint64(id), 10), func() (int, any, error) {
		updated, err := svc.UpdateRelay(c.Context(), id, req.Version, in, h.relayActor(c))
		if err != nil {
			return 0, nil, relayMutationError(err)
		}
		return fiber.StatusOK, updated, nil
	})
}

// EnablePlatformRelay handles POST /api/v1/platform/relays/:id/enable.
func (h *Handler) EnablePlatformRelay(c fiber.Ctx) error {
	return h.setRelayActive(c, true)
}

// DisablePlatformRelay handles POST /api/v1/platform/relays/:id/disable.
// Disabling removes the endpoint from routing; it requires typed
// confirmation.
func (h *Handler) DisablePlatformRelay(c fiber.Ctx) error {
	id, err := platformRelayID(c)
	if err != nil {
		return errorResponse(c, err)
	}
	if err := typedConfirm(c, "DISABLE-RELAY-"+strconv.FormatUint(uint64(id), 10)); err != nil {
		return err
	}
	return h.setRelayActive(c, false)
}

func (h *Handler) setRelayActive(c fiber.Ctx, active bool) error {
	svc, err := h.relayService()
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := platformRelayID(c)
	if err != nil {
		return errorResponse(c, err)
	}
	version, err := relayVersionFromBody(c)
	if err != nil {
		return errorResponse(c, err)
	}
	action := "enable"
	if !active {
		action = "disable"
	}
	return h.platformIdempotent(c, "platform.relay."+action+":"+strconv.FormatUint(uint64(id), 10), func() (int, any, error) {
		updated, err := svc.SetRelayActive(c.Context(), id, active, version, h.relayActor(c))
		if err != nil {
			return 0, nil, relayMutationError(err)
		}
		return fiber.StatusOK, updated, nil
	})
}

// RotatePlatformRelayCredentials handles
// POST /api/v1/platform/relays/:id/rotate-credentials. Requires typed
// confirmation (the previous credential is unrecoverable). When the
// request supplies no new password, a generated credential is
// returned EXACTLY ONCE in the response and never again.
func (h *Handler) RotatePlatformRelayCredentials(c fiber.Ctx) error {
	svc, err := h.relayService()
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := platformRelayID(c)
	if err != nil {
		return errorResponse(c, err)
	}
	if err := typedConfirm(c, "ROTATE-RELAY-"+strconv.FormatUint(uint64(id), 10)); err != nil {
		return err
	}
	var req struct {
		Version     int    `json:"version"`
		NewPassword string `json:"new_password"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Version <= 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body: a positive version is required", "code": "VALIDATION_FAILED"})
	}
	return h.platformIdempotent(c, "platform.relay.rotate:"+strconv.FormatUint(uint64(id), 10), func() (int, any, error) {
		updated, generated, err := svc.RotateRelayCredentials(c.Context(), id, req.Version, req.NewPassword, h.relayActor(c))
		if err != nil {
			return 0, nil, relayMutationError(err)
		}
		resp := fiber.Map{"relay": updated}
		if generated != "" {
			resp["generated_password"] = generated
			resp["show_once"] = true
		}
		return fiber.StatusOK, resp, nil
	})
}

// TestPlatformRelay handles POST /api/v1/platform/relays/:id/test.
// Idempotent: a replay with the same key returns the same redacted
// result without re-dialing.
func (h *Handler) TestPlatformRelay(c fiber.Ctx) error {
	svc, err := h.relayService()
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := platformRelayID(c)
	if err != nil {
		return errorResponse(c, err)
	}
	return h.platformIdempotent(c, "platform.relay.test:"+strconv.FormatUint(uint64(id), 10), func() (int, any, error) {
		result, err := svc.TestRelay(c.Context(), id, h.relayActor(c))
		if err != nil {
			return 0, nil, relayMutationError(err)
		}
		return fiber.StatusOK, result, nil
	})
}

// DeletePlatformRelay handles DELETE /api/v1/platform/relays/:id.
// Requires typed confirmation.
func (h *Handler) DeletePlatformRelay(c fiber.Ctx) error {
	svc, err := h.relayService()
	if err != nil {
		return errorResponse(c, err)
	}
	id, err := platformRelayID(c)
	if err != nil {
		return errorResponse(c, err)
	}
	if err := typedConfirm(c, "DELETE-RELAY-"+strconv.FormatUint(uint64(id), 10)); err != nil {
		return err
	}
	if err := svc.DeleteRelay(c.Context(), id, h.relayActor(c)); err != nil {
		if errors.Is(err, relay.ErrProviderNotFound) {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "relay not found", "code": "NOT_FOUND"})
		}
		return errorResponse(c, err)
	}
	return c.JSON(fiber.Map{"status": "ok", "id": id})
}

// relayMutationError maps the relay service's typed errors to stable
// kernel errors; errorResponse renders them with exact status + code.
// Raw service/SQL errors pass through AsAPIError's redaction boundary.
func relayMutationError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, relay.ErrProviderNotFound):
		return kernel.NotFound("relay")
	case errors.Is(err, relay.ErrVersionConflict):
		return kernel.NewError(kernel.ErrCodePreconditionFail, "relay was modified by another request — reload and retry")
	case errors.Is(err, relay.ErrRelayNameConflict):
		return kernel.Conflict("a relay with this name already exists in the same scope")
	case errors.Is(err, relay.ErrUnsafeTarget):
		return kernel.NewError(kernel.ErrCodeValidation, err.Error())
	case errors.Is(err, relay.ErrInvalidConnSecurity):
		return kernel.NewError(kernel.ErrCodeValidation, err.Error())
	case errors.Is(err, relay.ErrNameRequired):
		return kernel.NewError(kernel.ErrCodeValidation, err.Error())
	default:
		return err
	}
}

// uintPtr returns a pointer to v.
func uintPtr(v uint64) *uint {
	u := uint(v)
	return &u
}
