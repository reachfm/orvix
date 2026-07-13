package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v3"
	platformSvc "github.com/orvix/orvix/internal/admin/platform"
)

func (h *Handler) ListPlatformOrganizations(c fiber.Ctx) error {
	var req struct {
		Search string `json:"search"`
		Limit  int    `json:"limit"`
		Offset int    `json:"offset"`
	}
	c.Bind().Query(&req)

	result, total, err := h.platformAdminSvc.ListOrganizationSummaries(c.Context(), req.Search, req.Limit, req.Offset)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
	if result == nil {
		result = []platformSvc.OrganizationSummary{}
	}
	return c.JSON(fiber.Map{"organizations": result, "total": total})
}

func (h *Handler) GetPlatformOrganization(c fiber.Ctx) error {
	idVal, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil || idVal == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid organization id"})
	}
	id := uint(idVal)

	detail, err := h.platformAdminSvc.GetOrganizationDetail(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "organization not found"})
	}
	return c.JSON(detail)
}
