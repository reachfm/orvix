package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/collaboration"
	"github.com/orvix/orvix/internal/compliance"
	"github.com/orvix/orvix/internal/intelligence"
	"github.com/orvix/orvix/internal/stalwart"
	"github.com/orvix/orvix/internal/updater"
)

func (h *Handler) CheckUpdates(c fiber.Ctx) error {
	moduleID := c.Query("module", "orvix-core")
	for _, mod := range h.registry.All() {
		if m, ok := mod.(*updater.Module); ok {
			info, _ := m.Mgr().CheckForUpdates(c.Context(), moduleID, mod.Version())
			if info != nil {
				return c.JSON(info)
			}
			return c.JSON(fiber.Map{"status": "up_to_date", "module": moduleID, "version": mod.Version()})
		}
	}
	return c.JSON(fiber.Map{"status": "up_to_date"})
}

func (h *Handler) GetChangelog(c fiber.Ctx) error {
	moduleID := c.Query("module", "orvix-core")
	entries, err := updater.NewChangelogManager(h.db).GetChangelog(moduleID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(entries)
}

func (h *Handler) ApplyUpdate(c fiber.Ctx) error {
	moduleID := c.Params("module")
	h.writeAuditLog(c, "update.apply", "module:"+moduleID)
	return c.JSON(fiber.Map{"status": "update initiated", "module": moduleID})
}

func (h *Handler) GetEmailStats(c fiber.Ctx) error {
	var stats []intelligence.EmailAnalytics
	h.db.Order("date desc").Limit(30).Find(&stats)
	return c.JSON(stats)
}

func (h *Handler) GetDeliveryReports(c fiber.Ctx) error {
	var reports []intelligence.DeliveryReport
	h.db.Order("created_at desc").Limit(50).Find(&reports)
	return c.JSON(reports)
}

func (h *Handler) ListLegalHolds(c fiber.Ctx) error {
	var holds []compliance.LegalHold
	h.db.Order("created_at desc").Find(&holds)
	return c.JSON(holds)
}

func (h *Handler) CreateLegalHold(c fiber.Ctx) error {
	var req compliance.LegalHold
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	req.CreatedBy = c.Locals("user_id").(uint)
	h.db.Create(&req)
	h.writeAuditLog(c, "legal_hold.create", "target:"+req.TargetEmail)
	return c.Status(201).JSON(req)
}

func (h *Handler) UpdateLegalHold(c fiber.Ctx) error {
	h.db.Model(&compliance.LegalHold{}).Where("id = ?", c.Params("id")).Update("active", true)
	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteLegalHold(c fiber.Ctx) error {
	h.db.Where("id = ?", c.Params("id")).Delete(&compliance.LegalHold{})
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) ListRetentionPolicies(c fiber.Ctx) error {
	var policies []compliance.RetentionPolicy
	h.db.Find(&policies)
	return c.JSON(policies)
}

func (h *Handler) CreateRetentionPolicy(c fiber.Ctx) error {
	var req compliance.RetentionPolicy
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	h.db.Create(&req)
	return c.Status(201).JSON(req)
}

func (h *Handler) UpdateRetentionPolicy(c fiber.Ctx) error {
	var req compliance.RetentionPolicy
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	h.db.Model(&compliance.RetentionPolicy{}).Where("id = ?", c.Params("id")).Updates(&req)
	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteRetentionPolicy(c fiber.Ctx) error {
	h.db.Where("id = ?", c.Params("id")).Delete(&compliance.RetentionPolicy{})
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) ListSharedMailboxes(c fiber.Ctx) error {
	var mailboxes []collaboration.SharedMailbox
	h.db.Find(&mailboxes)
	return c.JSON(mailboxes)
}

func (h *Handler) CreateSharedMailbox(c fiber.Ctx) error {
	var req collaboration.SharedMailbox
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}
	h.db.Create(&req)
	_ = h.stalwart.CreatePrincipal(c.Context(), stalwart.Principal{
		Name: req.Email, Type: "group", Emails: []string{req.Email}, Enabled: true,
	})
	return c.Status(201).JSON(req)
}
