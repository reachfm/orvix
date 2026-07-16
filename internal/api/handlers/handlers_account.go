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

	// Look up the session to get its JTI for JWT revocation.
	var session models.Session
	if err := h.db.Where("id = ? AND user_id = ?", uint(id), userID).First(&session).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "session not found"})
	}

	// Delete the opaque session row.
	h.db.Where("id = ? AND user_id = ?", uint(id), userID).Delete(&models.Session{})

	// Revoke the associated JWT via its JTI.
	if session.JTI != "" {
		h.db.Exec("INSERT OR IGNORE INTO revoked_tokens (jti, expires_at) VALUES (?, ?)", session.JTI, session.ExpiresAt)
	}

	// Also revoke the caller's current cookie token if present.
	if accessToken := c.Cookies("access_token"); accessToken != "" {
		h.auth.RevokeAccessToken(accessToken)
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
	tenantID, _ := c.Locals("tenant_id").(uint)
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
	sr := models.SupportRequest{
		ReferenceID: refID,
		TenantID:    tenantID,
		UserID:      userID,
		UserEmail:   email,
		Category:    req.Category,
		Subject:     req.Subject,
		Message:     req.Message,
		Status:      "received",
	}
	if h.mailSender != nil {
		body := fmt.Sprintf("Support Request #%s\nCategory: %s\nSubject: %s\nUser: %s (ID: %d)\nTenant: %d\n\n%s",
			refID, req.Category, req.Subject, email, userID, tenantID, req.Message)
		if err := h.mailSender.Send("noreply@orvix.email", "Orvix Support: "+req.Subject, body); err != nil {
			sr.DeliveryStatus = "failed"
			sr.DeliveryError = err.Error()
		} else {
			sr.DeliveryStatus = "sent"
		}
	} else {
		sr.DeliveryStatus = "queued"
	}

	if err := h.db.Create(&sr).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to save support request"})
	}

	h.writeAuditLog(c, "support.request.create", fmt.Sprintf("category:%s ref:%s", req.Category, refID))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"reference_id":    refID,
		"status":          sr.Status,
		"delivery_status": sr.DeliveryStatus,
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
	validLocales := map[string]bool{"en": true, "ar": true, "fr": true, "es": true, "de": true, "pt": true, "zh": true, "ja": true, "ko": true, "ru": true}
	if req.Theme != nil && !validThemes[*req.Theme] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "theme must be light, dark, or system"})
	}
	if req.Locale != nil && !validLocales[*req.Locale] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "unsupported locale"})
	}
	if req.Timezone != nil && !isValidTimezone(*req.Timezone) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid timezone"})
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
		// Return defaults without persisting — no side effects on GET.
		return c.JSON(fiber.Map{
			"domain_verification": true,
			"quota_warning":       true,
			"quota_reached":       true,
			"billing_status":      true,
			"invitation":          true,
			"session_activity":    true,
			"channel_email":       true,
		})
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
		pref = models.UserNotificationPreference{UserID: userID} // defaults via GORM
		h.db.Create(&pref)
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

var validTimezones = map[string]bool{
	"UTC": true, "GMT": true,
	"Africa/Cairo": true, "Africa/Johannesburg": true, "Africa/Lagos": true, "Africa/Nairobi": true,
	"America/Chicago": true, "America/Denver": true, "America/Los_Angeles": true,
	"America/New_York": true, "America/Phoenix": true, "America/Sao_Paulo": true,
	"America/Toronto": true, "America/Vancouver": true,
	"Asia/Baghdad": true, "Asia/Bangkok": true, "Asia/Calcutta": true, "Asia/Dhaka": true,
	"Asia/Dubai": true, "Asia/Hong_Kong": true, "Asia/Jakarta": true, "Asia/Jerusalem": true,
	"Asia/Karachi": true, "Asia/Kolkata": true, "Asia/Manila": true, "Asia/Seoul": true,
	"Asia/Shanghai": true, "Asia/Singapore": true, "Asia/Tokyo": true,
	"Australia/Adelaide": true, "Australia/Brisbane": true, "Australia/Melbourne": true,
	"Australia/Perth": true, "Australia/Sydney": true,
	"Europe/Amsterdam": true, "Europe/Athens": true, "Europe/Berlin": true, "Europe/Brussels": true,
	"Europe/Bucharest": true, "Europe/Dublin": true, "Europe/Helsinki": true, "Europe/Istanbul": true,
	"Europe/Kyiv": true, "Europe/Lisbon": true, "Europe/London": true, "Europe/Madrid": true,
	"Europe/Moscow": true, "Europe/Oslo": true, "Europe/Paris": true, "Europe/Prague": true,
	"Europe/Rome": true, "Europe/Stockholm": true, "Europe/Vienna": true, "Europe/Warsaw": true,
	"Europe/Zurich":    true,
	"Pacific/Auckland": true, "Pacific/Fiji": true, "Pacific/Guam": true, "Pacific/Honolulu": true,
	"US/Alaska": true, "US/Arizona": true, "US/Central": true, "US/Eastern": true,
	"US/Hawaii": true, "US/Mountain": true, "US/Pacific": true,
}

func isValidTimezone(tz string) bool {
	return validTimezones[tz]
}

func (h *Handler) ListCustomerInvoices(c fiber.Ctx) error {
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	var invoices []models.Invoice
	q := h.db.Where("tenant_id = ?", tenantID).Order("issued_at DESC").Limit(50)
	if status := c.Query("status"); status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Find(&invoices).Error; err != nil {
		return c.JSON([]fiber.Map{})
	}
	type result struct {
		ID               uint   `json:"id"`
		InvoiceNumber    string `json:"invoice_number"`
		Currency         string `json:"currency"`
		Total            int64  `json:"total"`
		AmountPaid       int64  `json:"amount_paid"`
		AmountDue        int64  `json:"amount_due"`
		Status           string `json:"status"`
		PeriodStart      string `json:"period_start,omitempty"`
		PeriodEnd        string `json:"period_end,omitempty"`
		IssuedAt         string `json:"issued_at,omitempty"`
		DueAt            string `json:"due_at,omitempty"`
		PaidAt           string `json:"paid_at,omitempty"`
		HostedInvoiceURL string `json:"hosted_invoice_url,omitempty"`
		PDFURL           string `json:"pdf_url,omitempty"`
	}
	out := make([]result, 0, len(invoices))
	for _, inv := range invoices {
		r := result{
			ID:               inv.ID,
			InvoiceNumber:    inv.InvoiceNumber,
			Currency:         inv.Currency,
			Total:            inv.Total,
			AmountPaid:       inv.AmountPaid,
			AmountDue:        inv.AmountDue,
			Status:           inv.Status,
			HostedInvoiceURL: inv.HostedInvoiceURL,
			PDFURL:           inv.PDFURL,
		}
		if inv.PeriodStart != nil {
			r.PeriodStart = inv.PeriodStart.Format(time.RFC3339)
		}
		if inv.PeriodEnd != nil {
			r.PeriodEnd = inv.PeriodEnd.Format(time.RFC3339)
		}
		if inv.IssuedAt != nil {
			r.IssuedAt = inv.IssuedAt.Format(time.RFC3339)
		}
		if inv.DueAt != nil {
			r.DueAt = inv.DueAt.Format(time.RFC3339)
		}
		if inv.PaidAt != nil {
			r.PaidAt = inv.PaidAt.Format(time.RFC3339)
		}
		out = append(out, r)
	}
	return c.JSON(out)
}

func (h *Handler) GetCustomerInvoice(c fiber.Ctx) error {
	tenantID, err := auth.RequireTenantID(c)
	if err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant context required"})
	}
	idStr := c.Params("id")
	var id uint
	if _, err := fmt.Sscanf(idStr, "%d", &id); err != nil || id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid invoice id"})
	}
	var inv models.Invoice
	if err := h.db.Where("id = ? AND tenant_id = ?", id, tenantID).First(&inv).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "invoice not found"})
	}
	r := fiber.Map{
		"id":                 inv.ID,
		"invoice_number":     inv.InvoiceNumber,
		"currency":           inv.Currency,
		"subtotal":           inv.Subtotal,
		"tax":                inv.Tax,
		"total":              inv.Total,
		"amount_paid":        inv.AmountPaid,
		"amount_due":         inv.AmountDue,
		"status":             inv.Status,
		"hosted_invoice_url": inv.HostedInvoiceURL,
		"pdf_url":            inv.PDFURL,
	}
	if inv.PeriodStart != nil {
		r["period_start"] = inv.PeriodStart.Format(time.RFC3339)
	}
	if inv.PeriodEnd != nil {
		r["period_end"] = inv.PeriodEnd.Format(time.RFC3339)
	}
	if inv.IssuedAt != nil {
		r["issued_at"] = inv.IssuedAt.Format(time.RFC3339)
	}
	if inv.DueAt != nil {
		r["due_at"] = inv.DueAt.Format(time.RFC3339)
	}
	if inv.PaidAt != nil {
		r["paid_at"] = inv.PaidAt.Format(time.RFC3339)
	}
	return c.JSON(r)
}
