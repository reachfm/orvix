package handlers

import (
	"bytes"
	"strconv"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
)

func (h *Handler) ExportAuditLogs(c fiber.Ctx) error {
	format := audit.ExportFormat(c.Query("format", "json"))
	if format != audit.ExportJSON && format != audit.ExportCSV {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "format must be json or csv"})
	}
	q := parseAuditQuery(c)
	var buf bytes.Buffer
	if err := h.auditStore.ExportTo(c.Context(), q, format, &buf); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if format == audit.ExportCSV {
		c.Set("Content-Type", "text/csv")
		c.Set("Content-Disposition", "attachment; filename=audit.csv")
	} else {
		c.Set("Content-Type", "application/json")
	}
	return c.Send(buf.Bytes())
}

func (h *Handler) GetAuditEntry(c fiber.Ctx) error {
	id, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
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
