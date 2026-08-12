package handlers

import (
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/platform/cluster"
)

func (h *Handler) GetClusterNodes(c fiber.Ctx) error {
	if h.clusterSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "cluster service not available"})
	}
	nodes, err := h.clusterSvc.ListNodes(c.Context())
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"nodes": nodes})
}

func (h *Handler) PostClusterNodeCordon(c fiber.Ctx) error {
	if h.clusterSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "cluster service not available"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	var req struct {
		Reason string `json:"reason"`
	}
	c.Bind().JSON(&req)
	if err := h.clusterSvc.Cordon(c.Context(), c.Params("id"), req.Reason, actorID); err != nil {
		return clusterActionError(c, err)
	}
	return c.JSON(fiber.Map{"status": "cordoned"})
}

func (h *Handler) PostClusterNodeUncordon(c fiber.Ctx) error {
	if h.clusterSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "cluster service not available"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	if err := h.clusterSvc.Uncordon(c.Context(), c.Params("id"), actorID); err != nil {
		return clusterActionError(c, err)
	}
	return c.JSON(fiber.Map{"status": "uncordoned"})
}

func (h *Handler) PostClusterNodeDrain(c fiber.Ctx) error {
	if h.clusterSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "cluster service not available"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	var req struct {
		Reason          string `json:"reason"`
		ExpiresInMinute int    `json:"expires_in_minutes"`
	}
	c.Bind().JSON(&req)
	var until *time.Time
	if req.ExpiresInMinute > 0 {
		t := time.Now().UTC().Add(time.Duration(req.ExpiresInMinute) * time.Minute)
		until = &t
	}
	if err := h.clusterSvc.Drain(c.Context(), c.Params("id"), req.Reason, until, actorID); err != nil {
		return clusterActionError(c, err)
	}
	return c.JSON(fiber.Map{"status": "draining"})
}

func (h *Handler) PostClusterNodeResume(c fiber.Ctx) error {
	if h.clusterSvc == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "cluster service not available"})
	}
	actorID, _ := c.Locals("user_id").(uint)
	if err := h.clusterSvc.Resume(c.Context(), c.Params("id"), actorID); err != nil {
		return clusterActionError(c, err)
	}
	return c.JSON(fiber.Map{"status": "resumed"})
}

func clusterActionError(c fiber.Ctx, err error) error {
	switch err {
	case cluster.ErrNodeNotFound:
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	case cluster.ErrMaintenanceReasonRequired:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	case cluster.ErrVersionConflict:
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": err.Error()})
	default:
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}
}
