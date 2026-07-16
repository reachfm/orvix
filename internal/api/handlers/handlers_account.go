package handlers

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/models"
)

func (h *Handler) ListAccountSessions(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}

	currentHash := ""
	if raw := c.Cookies("__Host-orvix_session"); raw != "" {
		currentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(raw)))
	}

	var sessions []models.Session
	if err := h.db.Where("user_id = ? AND expires_at > ?", userID, time.Now()).
		Order("created_at DESC").Limit(50).Find(&sessions).Error; err != nil {
		return c.JSON(fiber.Map{"sessions": []fiber.Map{}})
	}

	type sessionInfo struct {
		ID        uint   `json:"id"`
		CreatedAt string `json:"created_at"`
		IP        string `json:"ip"`
		UserAgent string `json:"user_agent"`
		Current   bool   `json:"current"`
	}
	result := make([]sessionInfo, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, sessionInfo{
			ID:        s.ID,
			CreatedAt: s.CreatedAt.Format(time.RFC3339),
			IP:        s.IP,
			UserAgent: s.UserAgent,
			Current:   currentHash != "" && s.TokenHash == currentHash,
		})
	}
	return c.JSON(fiber.Map{"sessions": result})
}

func (h *Handler) RevokeAccountSession(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}
	idStr := c.Params("id")
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil || id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid session id"})
	}

	result := h.db.Where("id = ? AND user_id = ?", uint(id), userID).Delete(&models.Session{})
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "session not found"})
	}
	h.writeAuditLog(c, "session.revoke", fmt.Sprintf("session_id:%d", id))
	return c.JSON(fiber.Map{"status": "revoked"})
}

func (h *Handler) GetAccountProfile(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	email, _ := c.Locals("email").(string)
	role, _ := c.Locals("role").(auth.Role)

	var displayName, locale, timezone string
	if userID > 0 {
		h.db.Raw("SELECT COALESCE(full_name,'') FROM users WHERE id = ?", userID).Scan(&displayName)
	}

	return c.JSON(fiber.Map{
		"user_id":      userID,
		"email":        email,
		"role":         string(role),
		"display_name": displayName,
		"locale":       locale,
		"timezone":     timezone,
	})
}

func (h *Handler) UpdateAccountProfile(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}

	var req struct {
		DisplayName *string `json:"display_name"`
		Locale      *string `json:"locale"`
		Timezone    *string `json:"timezone"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	updates := map[string]interface{}{}
	if req.DisplayName != nil {
		updates["full_name"] = *req.DisplayName
	}
	if req.Locale != nil {
		updates["locale"] = *req.Locale
	}
	if req.Timezone != nil {
		updates["timezone"] = *req.Timezone
	}
	if len(updates) == 0 {
		return c.JSON(fiber.Map{"status": "ok"})
	}

	if err := h.db.Table("users").Where("id = ?", userID).Updates(updates).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update profile"})
	}
	h.writeAuditLog(c, "profile.update", fmt.Sprintf("user:%d", userID))
	return c.JSON(fiber.Map{"status": "ok"})
}

func (h *Handler) SubmitSupportRequest(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	email, _ := c.Locals("email").(string)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}

	var req struct {
		Category string `json:"category"`
		Subject  string `json:"subject"`
		Message  string `json:"message"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Subject == "" || req.Category == "" || req.Message == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "category, subject, and message are required"})
	}

	refID := fmt.Sprintf("SR-%d-%d", userID, time.Now().Unix())
	if h.mailSender != nil {
		body := fmt.Sprintf("Support Request #%s\nCategory: %s\nSubject: %s\nUser: %s (ID: %d)\n\n%s",
			refID, req.Category, req.Subject, email, userID, req.Message)
		_ = h.mailSender.Send("noreply@orvix.email", "Orvix Support: "+req.Subject, body)
	}

	h.writeAuditLog(c, "support.request.create", fmt.Sprintf("category:%s ref:%s", req.Category, refID))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"reference_id": refID,
		"status":       "received",
	})
}
