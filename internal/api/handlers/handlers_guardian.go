package handlers

import (
	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/guardian"
	"github.com/orvix/orvix/internal/models"
)

// AnalyzeEmail performs Guardian AI threat analysis on an email.
func (h *Handler) AnalyzeEmail(c fiber.Ctx) error {
	var req struct {
		EmailID        string `json:"email_id"`
		SenderIP       string `json:"sender_ip"`
		SenderDomain   string `json:"sender_domain"`
		Subject        string `json:"subject"`
		Body           string `json:"body"`
		HasAttachments bool   `json:"has_attachments"`
		SPFResult      string `json:"spf_result"`
		DKIMResult     string `json:"dkim_result"`
		DMARCResult    string `json:"dmarc_result"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}

	for _, mod := range h.registry.All() {
		if mod.ID() == "guardian-agent" {
			if g, ok := mod.(interface{ Agent() *guardian.Agent }); ok {
				analysisReq := &guardian.AnalyzeRequest{
					EmailID:        req.EmailID,
					SenderIP:       req.SenderIP,
					SenderDomain:   req.SenderDomain,
					Subject:        req.Subject,
					Body:           req.Body,
					HasAttachments: req.HasAttachments,
					SPFResult:      req.SPFResult,
					DKIMResult:     req.DKIMResult,
					DMARCResult:    req.DMARCResult,
				}
				result, err := g.Agent().Analyze(c.Context(), analysisReq)
				if err != nil {
					return c.Status(500).JSON(fiber.Map{"error": "analysis failed"})
				}
				return c.JSON(result)
			}
		}
	}
	return c.Status(404).JSON(fiber.Map{"error": "guardian module not available"})
}

// ListGuardianLogs returns guardian analysis logs.
func (h *Handler) ListGuardianLogs(c fiber.Ctx) error {
	var logs []models.GuardianLog
	h.db.Order("created_at desc").Limit(100).Find(&logs)
	return c.JSON(logs)
}
