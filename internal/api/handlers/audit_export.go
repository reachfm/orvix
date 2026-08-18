package handlers

import (
	"bytes"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/platform/kernel"
)

// ExportAuditLogs streams the filtered Platform Audit log as JSON or CSV.
// It reads the SAME canonical store as GET /audit/logs and
// GET /audit/logs/:id (orvix_audit via the extended store), so an export
// can never disagree with the list or the detail view. The legacy
// coremail_audit exporter remains only as a fallback for deployments where
// the extended store was not initialized.
func (h *Handler) ExportAuditLogs(c fiber.Ctx) error {
	format := audit.ExportFormat(c.Query("format", "json"))
	if format != audit.ExportJSON && format != audit.ExportCSV {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "format must be json or csv"})
	}
	q := parseAuditQuery(c)
	var buf bytes.Buffer

	if h.auditExtended != nil {
		eq := &audit.ExtendedQuery{
			Action: q.Action, Result: q.Result, Target: q.Target,
			Limit: q.Limit, Offset: q.Offset,
		}
		if q.TenantID != 0 {
			tid := q.TenantID
			eq.TenantID = &tid
		}
		if actorID, err := strconv.ParseUint(q.Actor, 10, 64); err == nil && actorID > 0 {
			aid := uint(actorID)
			eq.ActorID = &aid
		} else if q.Actor != "" {
			// Non-numeric actor: the extended store has no LIKE column for
			// actor; fall back to the legacy exporter for this filter so
			// the export stays correct rather than silently dropping rows.
			if err := h.auditStore.ExportTo(c.Context(), q, format, &buf); err != nil {
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
			}
			return h.sendAuditExport(c, format, buf.Bytes())
		}
		if err := h.auditExtended.ExportTo(c.Context(), eq, format, &buf); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return h.sendAuditExport(c, format, buf.Bytes())
	}

	if err := h.auditStore.ExportTo(c.Context(), q, format, &buf); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return h.sendAuditExport(c, format, buf.Bytes())
}

func (h *Handler) sendAuditExport(c fiber.Ctx, format audit.ExportFormat, data []byte) error {
	if format == audit.ExportCSV {
		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", "attachment; filename=audit.csv")
	} else {
		c.Set("Content-Type", "application/json")
	}
	return c.Send(data)
}

func (h *Handler) GetAuditEntry(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id", "code": string(kernel.ErrCodeValidation)})
	}
	// Prefer the extended store so the detail contract matches the
	// list contract (actor_id/actor_role/tenant_id/ip/user_agent/…).
	if h.auditExtended != nil {
		entry, err := h.auditExtended.GetEntry(c.Context(), id)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to load audit entry"})
		}
		if entry == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "entry not found"})
		}
		return c.JSON(entry)
	}
	entry, err := h.auditStore.GetEntry(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "entry not found"})
	}
	return c.JSON(entry)
}

func parseAuditQuery(c fiber.Ctx) *audit.Query {
	q := &audit.Query{}
	if v := c.Query("tenant_id"); v != "" {
		if tid, err := strconv.ParseUint(v, 10, 64); err == nil {
			q.TenantID = uint(tid)
		}
	}
	q.Actor = c.Query("actor")
	q.Action = c.Query("action")
	q.Target = c.Query("target")
	q.Result = c.Query("result")
	if v := c.Query("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil {
			q.Limit = l
		}
	}
	return q
}
