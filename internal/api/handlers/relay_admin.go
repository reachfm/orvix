package handlers

import (
	"errors"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/platform/relay"
)

// relayActor builds the audit actor for a relay administration request from
// authenticated request state only - never from the request body.
func relayActor(c fiber.Ctx) relay.AuditActor {
	id, _ := c.Locals("user_id").(uint)
	role, _ := c.Locals("role").(string)
	reqID, _ := c.Locals("request_id").(string)
	return relay.AuditActor{ID: id, Role: role, RequestID: reqID, IP: c.IP(), UserAgent: c.Get("User-Agent")}
}

// relayError converts a service error into a status code and a SAFE client
// message.
//
// Every relay handler previously answered `fiber.Map{"error": err.Error()}`,
// which serialises whatever the lower layers produced straight to the client.
// That surface reaches the browser and is trivially readable in DevTools, and
// the wrapped errors carry driver text: SQL fragments, constraint and column
// names, connection DSNs, and file paths. Only errors this package defines as
// client-facing are echoed; anything else becomes a generic message, with the
// detail left for server-side logs.
func relayError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, relay.ErrProviderNotFound), errors.Is(err, relay.ErrPoolNotFound), errors.Is(err, relay.ErrOverrideNotFound):
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, relay.ErrVersionConflict):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, relay.ErrRelayNameConflict):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, relay.ErrCrossTenantProvider):
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	case errors.Is(err, relay.ErrInvalidConnSecurity),
		errors.Is(err, relay.ErrUnsafeTarget),
		errors.Is(err, relay.ErrNameRequired),
		errors.Is(err, relay.ErrInsecureCredentialTransport):
		return relayError(c, err)
	}
	// Field-level validation errors are safe: they name request fields, not
	// internal state.
	var kerr *kernel.Error
	if errors.As(err, &kerr) && kerr.Code == kernel.ErrCodeValidation {
		if len(kerr.Fields) > 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": kerr.Message, "fields": kerr.Fields})
		}
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": kerr.Message})
	}
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "relay request failed"})
}

// PostRelayPool handles POST /admin/relay/pools.
func (h *Handler) PostRelayPool(c fiber.Ctx) error {
	if h.relaySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "relay service not available"})
	}
	tenantID, _ := auth.RequireTenantID(c)
	var req struct {
		Scope      string `json:"scope"`
		Name       string `json:"name"`
		Strategy   string `json:"strategy"`
		DomainID   uint   `json:"domain_id"`
		DirectOnly bool   `json:"direct_only"`
	}
	c.Bind().JSON(&req)
	pool := relay.Pool{Scope: relay.Scope(req.Scope), TenantID: tenantID, DomainID: req.DomainID, Name: req.Name, Strategy: relay.SelectionStrategy(req.Strategy), DirectOnly: req.DirectOnly}
	created, err := h.relaySvc.CreatePool(c.Context(), pool)
	if err != nil {
		return relayError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"pool": created})
}

// PostRelayProvider handles POST /admin/relay/providers. The
// credential (if any) travels only in this one request body and is
// encrypted before it is ever persisted — it is never echoed back in
// the response.
func (h *Handler) PostRelayProvider(c fiber.Ctx) error {
	if h.relaySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "relay service not available"})
	}
	var req struct {
		PoolID          uint   `json:"pool_id"`
		Scope           string `json:"scope"`
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
	c.Bind().JSON(&req)
	p := relay.Provider{
		PoolID: req.PoolID, Scope: relay.Scope(req.Scope), Name: req.Name, Host: req.Host, Port: req.Port,
		Username: req.Username, ConnSecurity: relay.ConnSecurity(req.ConnSecurity), TLSValidation: relay.TLSValidation(req.TLSValidation),
		Priority: req.Priority, Weight: req.Weight, Active: req.Active, RateLimitPerMin: req.RateLimitPerMin,
	}
	created, err := h.relaySvc.CreateProvider(c.Context(), p, req.Password)
	req.Password = "" // discard local copy immediately after use
	if err != nil {
		return relayError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"provider": relay.Redact(*created)})
}

// GetRelayPoolProviders handles GET /admin/relay/pools/:id/providers — redacted list.
func (h *Handler) GetRelayPoolProviders(c fiber.Ctx) error {
	if h.relaySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "relay service not available"})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid pool id"})
	}
	providers, err := h.relaySvc.ListProvidersRedacted(c.Context(), uint(idVal))
	if err != nil {
		return relayError(c, err)
	}
	return c.JSON(fiber.Map{"providers": providers})
}

// PostRelayProviderTest handles POST /admin/relay/providers/:id/test —
// operator-triggered connection test. Never sends mail.
func (h *Handler) PostRelayProviderTest(c fiber.Ctx) error {
	if h.relaySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "relay service not available"})
	}
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid provider id"})
	}
	result, err := h.relaySvc.TestConnection(c.Context(), uint(idVal))
	if err != nil {
		return relayError(c, err)
	}
	return c.JSON(fiber.Map{"result": result})
}

// PostRelayRoutingRule handles POST /admin/relay/routing-rules.
func (h *Handler) PostRelayRoutingRule(c fiber.Ctx) error {
	if h.relaySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "relay service not available"})
	}
	tenantID, _ := auth.RequireTenantID(c)
	var req struct {
		DomainID        uint   `json:"domain_id"`
		SenderPattern   string `json:"sender_pattern"`
		RecipientDomain string `json:"recipient_domain"`
		Classification  string `json:"classification"`
		PoolID          uint   `json:"pool_id"`
		Priority        int    `json:"priority"`
	}
	c.Bind().JSON(&req)
	rule, err := h.relaySvc.CreateRoutingRule(c.Context(), relay.RoutingRule{
		TenantID: tenantID, DomainID: req.DomainID, SenderPattern: req.SenderPattern,
		RecipientDomain: req.RecipientDomain,
		Classification:  req.Classification, PoolID: req.PoolID, Priority: req.Priority,
	}, relayActor(c))
	if err != nil {
		return relayError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"rule": rule})
}

// PostRelayEmergencyOverride handles POST /admin/relay/emergency-override.
func (h *Handler) PostRelayEmergencyOverride(c fiber.Ctx) error {
	if h.relaySvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "relay service not available"})
	}
	tenantID, _ := auth.RequireTenantID(c)
	actorID, _ := c.Locals("user_id").(uint)
	var req struct {
		PoolID       uint   `json:"pool_id"`
		Reason       string `json:"reason"`
		ExpiresInMin int    `json:"expires_in_minutes"`
	}
	c.Bind().JSON(&req)
	if req.ExpiresInMin <= 0 {
		req.ExpiresInMin = 60
	}
	expiresAt := time.Now().UTC().Add(time.Duration(req.ExpiresInMin) * time.Minute)
	override, err := h.relaySvc.SetEmergencyOverride(c.Context(), tenantID, req.PoolID, actorID, req.Reason, expiresAt)
	if err != nil {
		return relayError(c, err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"override": override})
}
