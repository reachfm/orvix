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

	var u models.User
	if userID > 0 {
		h.db.First(&u, userID)
	}

	return c.JSON(fiber.Map{
		"user_id":      userID,
		"email":        email,
		"role":         string(role),
		"display_name": u.DisplayName,
		"locale":       u.Locale,
		"timezone":     u.Timezone,
		"theme":        u.Theme,
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

	refID := fmt.Sprintf("SR-%d-%d-%d", userID, time.Now().UnixNano()/1000, time.Now().Nanosecond()%1000)
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

func (h *Handler) GetAccountPreferences(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}
	var u models.User
	h.db.First(&u, userID)
	return c.JSON(fiber.Map{
		"theme":    u.Theme,
		"locale":   u.Locale,
		"timezone": u.Timezone,
	})
}

func (h *Handler) UpdateAccountPreferences(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}
	var req struct {
		Theme    *string `json:"theme"`
		Locale   *string `json:"locale"`
		Timezone *string `json:"timezone"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	validThemes := map[string]bool{"light": true, "dark": true, "system": true}
	if req.Theme != nil && !validThemes[*req.Theme] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "theme must be light, dark, or system"})
	}
	if req.Locale != nil && len(*req.Locale) > 10 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "locale too long"})
	}
	if req.Timezone != nil && len(*req.Timezone) > 64 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "timezone too long"})
	}

	updates := map[string]interface{}{}
	if req.Theme != nil {
		updates["theme"] = *req.Theme
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
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update preferences"})
	}
	h.writeAuditLog(c, "preferences.update", fmt.Sprintf("user:%d", userID))
	return c.JSON(fiber.Map{"status": "ok"})
}

func (h *Handler) GetNotificationPreferences(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}
	var pref models.UserNotificationPreference
	result := h.db.Where("user_id = ?", userID).First(&pref)
	if result.Error != nil {
		pref = models.UserNotificationPreference{
			UserID:             userID,
			DomainVerification: true,
			QuotaWarning:       true,
			QuotaReached:       true,
			BillingStatus:      true,
			Invitation:         true,
			SessionActivity:    true,
			ChannelEmail:       true,
		}
		h.db.Create(&pref)
	}
	return c.JSON(fiber.Map{
		"domain_verification": pref.DomainVerification,
		"quota_warning":       pref.QuotaWarning,
		"quota_reached":       pref.QuotaReached,
		"billing_status":      pref.BillingStatus,
		"invitation":          pref.Invitation,
		"session_activity":    pref.SessionActivity,
		"channel_email":       pref.ChannelEmail,
	})
}

func (h *Handler) UpdateNotificationPreferences(c fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	if userID == 0 {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "authentication required"})
	}
	var req struct {
		DomainVerification *bool `json:"domain_verification"`
		QuotaWarning       *bool `json:"quota_warning"`
		QuotaReached       *bool `json:"quota_reached"`
		BillingStatus      *bool `json:"billing_status"`
		Invitation         *bool `json:"invitation"`
		SessionActivity    *bool `json:"session_activity"`
		ChannelEmail       *bool `json:"channel_email"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	var pref models.UserNotificationPreference
	if err := h.db.Where("user_id = ?", userID).First(&pref).Error; err != nil {
		pref = models.UserNotificationPreference{UserID: userID}
	}
	if req.DomainVerification != nil {
		pref.DomainVerification = *req.DomainVerification
	}
	if req.QuotaWarning != nil {
		pref.QuotaWarning = *req.QuotaWarning
	}
	if req.QuotaReached != nil {
		pref.QuotaReached = *req.QuotaReached
	}
	if req.BillingStatus != nil {
		pref.BillingStatus = *req.BillingStatus
	}
	if req.Invitation != nil {
		pref.Invitation = *req.Invitation
	}
	if req.SessionActivity != nil {
		pref.SessionActivity = *req.SessionActivity
	}
	if req.ChannelEmail != nil {
		pref.ChannelEmail = *req.ChannelEmail
	}
	if err := h.db.Save(&pref).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to update notification preferences"})
	}
	h.writeAuditLog(c, "notification_prefs.update", fmt.Sprintf("user:%d", userID))
	return c.JSON(fiber.Map{"status": "ok"})
}
