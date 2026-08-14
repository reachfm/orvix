package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/dnsops"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/platform/mailcontrol"
)

var (
	errEmptyJSONBody    = errors.New("empty request body")
	errTrailingJSONData = errors.New("unexpected trailing data after JSON value")
)

// ── Platform domain & mailbox provisioning (Phase 1) ───────────────
//
// These handlers implement the Platform Super Admin provisioning
// surface:
//
//	POST /api/v1/platform/domains/:tenant_id        — domain creation
//	POST /api/v1/platform/mailboxes/:tenant_id      — mailbox creation
//	POST /api/v1/platform/mailboxes/:tenant_id/:id/access-mode
//
// Security contract shared by every mutation in this file:
//   - platformMW gate (platform_super_admin / super_admin + CSRF);
//   - canonical RBAC permission (domains.write / mailboxes.write);
//   - explicit target tenant from the path, never derived;
//   - strict JSON (unknown fields rejected) with a safe size limit;
//   - required Idempotency-Key with same-key replay / changed-body
//     conflict / in-flight concurrency protection;
//   - typed, redacted errors; no password, hash, key, token, path,
//     or raw SQL detail ever reaches a response.

// maxPlatformMutationBodyBytes bounds provisioning request bodies.
// Provisioning payloads are small structured objects; anything larger
// is a client bug or an abuse attempt and is rejected outright.
const maxPlatformMutationBodyBytes = 32 * 1024

// platformMutationBody returns the request body after enforcing the
// size limit. A nil/empty body is rejected.
func platformMutationBody(c fiber.Ctx) ([]byte, error) {
	body := c.Body()
	if len(body) == 0 {
		return nil, kernel.NewError(kernel.ErrCodeValidation, "empty request body")
	}
	if len(body) > maxPlatformMutationBodyBytes {
		return nil, kernel.NewError(kernel.ErrCodeValidation, "request body too large")
	}
	return body, nil
}

// ── Platform domain creation ───────────────────────────────────────

// CreatePlatformDomain handles
// POST /api/v1/platform/domains/:tenant_id. The entire transactional
// body runs inside the canonical admin domain provisioning service;
// this handler adds the platform gates (idempotency, strict JSON,
// tenant parsing) and generates the publishable DNS requirements via
// the existing dnsops service.
func (h *Handler) CreatePlatformDomain(c fiber.Ctx) error {
	svc, err := h.mailControl()
	if err != nil {
		return errorResponse(c, err)
	}
	tenantID, err := parseTenantParam(c)
	if err != nil {
		return errorResponse(c, err)
	}
	body, err := platformMutationBody(c)
	if err != nil {
		return errorResponse(c, err)
	}
	var req mailcontrol.PlatformCreateDomainRequest
	if err := bindStrictJSONBytes(body, &req); err != nil {
		return strictJSONError(c, err)
	}
	if strings.TrimSpace(req.Name) == "" {
		return errorResponse(c, kernel.ValidationError(map[string]string{"name": "domain name is required"}))
	}

	actorID := h.platformActorID(c)
	// The idempotency scope binds the key to the authenticated actor,
	// the target tenant, the HTTP method, and the canonical action.
	// The request-body hash (computed inside platformIdempotent over
	// the exact bytes) binds the key to the payload.
	scope := "platform.domain.create:POST:/platform/domains/" + strconv.FormatUint(uint64(tenantID), 10) + ":actor:" + strconv.FormatUint(uint64(actorID), 10)

	return h.platformIdempotent(c, scope, func() (int, any, any, error) {
		var dnsRequirements []mailcontrol.PlatformDNSRequirement
		if dnsSvc := h.dnsOpsService(); dnsSvc != nil {
			if inputs, inErr := h.dnsOpsInputsForDomain(c.Context(), strings.TrimSpace(req.Name)); inErr == nil {
				if plan, planErr := dnsSvc.Generate(inputs); planErr == nil {
					dnsRequirements = mapDNSPlanRequirements(plan)
				}
			}
		}

		result, err := svc.CreateDomain(c.Context(), req, tenantID, actorID, dnsRequirements)
		if err != nil {
			return 0, nil, nil, err
		}

		status := fiber.StatusOK
		if !result.Idempotent {
			status = fiber.StatusCreated
		}
		// The full result is both the replay body and the live body —
		// it contains only publishable data, so replaying it verbatim
		// is safe.
		return status, result, result, nil
	})
}

// mapDNSPlanRequirements projects the public dnsops plan records into
// the platform contract. Only public values travel (name/type/value/
// ttl/priority); the plan itself contains no private material.
func mapDNSPlanRequirements(plan *dnsops.Plan) []mailcontrol.PlatformDNSRequirement {
	if plan == nil {
		return nil
	}
	out := make([]mailcontrol.PlatformDNSRequirement, 0, len(plan.Records))
	for _, r := range plan.Records {
		out = append(out, mailcontrol.PlatformDNSRequirement{
			Name:     r.Name,
			Type:     string(r.Type),
			Value:    r.Value,
			TTL:      r.TTL,
			Priority: r.Priority,
			Required: r.Required,
			Purpose:  string(r.Purpose),
		})
	}
	return out
}

// bindStrictJSONBytes parses a raw body with DisallowUnknownFields
// and rejects trailing data, exactly like bindStrictJSON but for a
// pre-read body (so the size limit can be enforced first).
func bindStrictJSONBytes(body []byte, dst interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if err == io.EOF {
			return errEmptyJSONBody
		}
		return err
	}
	if dec.More() {
		return errTrailingJSONData
	}
	return nil
}
