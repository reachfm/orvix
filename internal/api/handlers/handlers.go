package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/runtime"
	"github.com/orvix/orvix/internal/updater"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/webmailmgmt"
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"
)

// Handler holds dependencies for all HTTP handlers.
type Handler struct {
	db          *gorm.DB
	auth        *auth.Authenticator
	apikeys     *auth.APIKeyManager
	logger      *zap.Logger
	cfg         *config.Config
	registry    *modules.Registry
	features    *license.FeatureFlags
	security    *auth.SecurityMonitor
	rateLimiter *auth.RedisRateLimiter
	auditStore  *audit.Store
	webmailSvc  *webmailmgmt.Service

	// mailStore is the same *storage.MailStore instance
	// used by the coremail runtime module. The webmail
	// user-facing endpoints (GET /api/v1/webmail/...)
	// read folders, messages, RFC822 bodies, and write
	// new outbound messages through this store directly.
	// Set via SetMailStore at router construction time.
	mailStore  *storage.MailStore

	// queueEngine is the same *queue.QueueEngine used by
	// the coremail runtime module. The user-facing
	// webmail Send endpoint enqueues outbound messages
	// through this engine so they are picked up by the
	// existing delivery worker — no SMTP redesign, no
	// parallel pipeline. Set via SetQueueEngine at router
	// construction time.
	queueEngine *queue.QueueEngine

	// updateSvc is the process-wide Update Management v1 service.
	// It is set once at router construction (see
	// api.NewRouter) so that the run lock is process-wide: two
	// concurrent HTTP requests against the same router share a
	// single RuntimeService and therefore a single mutex. A fresh
	// service per request would defeat the single-flight
	// guarantee. This field is read by h.updateService().
	updateSvc *updater.RuntimeService

	// updateSvcOnce ensures the schema is created exactly once
	// for the lifetime of the Handler, even if updateService()
	// is called many times. The schema is a CREATE TABLE IF NOT
	// EXISTS so it is idempotent; the Once is just an optimisation.
	updateSvcOnce sync.Once

	// processStartedAt is captured once via SetProcessStartedAt
	// during router construction. The runtime telemetry endpoint
	// reports it as uptime; the zero value means "not reported"
	// and the response carries a telemetry_incomplete warning so
	// the dashboard does not show fake numbers.
	processStartedAt time.Time

	// listenerRegistry holds the live listener startup state
	// for SMTP/IMAP/POP3/JMAP. Populated by the coremail
	// runtime module during Start(). Passed to the admin
	// runtime telemetry endpoint for the dashboard.
	listenerRegistry *runtime.ListenerRegistry
}

// NewHandler creates a new Handler with dependencies.
func NewHandler(db *gorm.DB, authenticator *auth.Authenticator, apikeyMgr *auth.APIKeyManager,
	logger *zap.Logger, cfg *config.Config, registry *modules.Registry,
	ff *license.FeatureFlags, rateLimiter *auth.RedisRateLimiter) *Handler {
	var auditStore *audit.Store
	if db != nil {
		if sqlDB, err := db.DB(); err == nil {
			auditStore = audit.NewStore(sqlDB)
			if err := auditStore.EnsureTable(context.Background()); err != nil {
				logger.Error("failed to ensure audit store", zap.Error(err))
			}
		}
	}
	// SecurityMonitor needs a non-nil DB; pass nil when DB is nil.
	var secMonitor *auth.SecurityMonitor
	if db != nil {
		secMonitor = auth.NewSecurityMonitor(db, logger)
	}
	return &Handler{
		db:          db,
		auth:        authenticator,
		apikeys:     apikeyMgr,
		logger:      logger,
		cfg:         cfg,
		registry:    registry,
		features:    ff,
		security:    secMonitor,
		rateLimiter: rateLimiter,
		auditStore:  auditStore,
	}
}

// SetProcessStartedAt records the moment the process started. It is
// called once during router construction (api.NewRouter). The
// runtime telemetry endpoint reads this value to compute uptime.
// A zero value means "not reported" and the response carries a
// telemetry_incomplete warning so the dashboard does not display
// fake numbers.
func (h *Handler) SetProcessStartedAt(t time.Time) {
	h.processStartedAt = t
}

// SetListenerRegistry wires the live listener state registry
// into the handler so GetAdminRuntime can return the real
// SMTP/IMAP/POP3/JMAP runtime status instead of "unknown".
// The registry is populated by the coremail runtime module
// during Start().
func (h *Handler) SetListenerRegistry(r *runtime.ListenerRegistry) {
	h.listenerRegistry = r
}

// Health returns server health status.
func (h *Handler) Health(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status":    "ok",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"version":   config.GetWatermark().Version,
	})
}

// Login authenticates a user and returns JWT tokens.
func (h *Handler) Login(c fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	loginEmail := req.Username
	if loginEmail == "" {
		loginEmail = req.Email
	}

	if loginEmail == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email and password required"})
	}

	h.logger.Info("login attempt", zap.String("email", loginEmail))

	// Get underlying sql.DB and query directly
	var userID uint
	var passwordHash string
	var userRole string

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	err = sqlDB.QueryRow("SELECT id, password_hash, role FROM users WHERE email = ?", loginEmail).Scan(&userID, &passwordHash, &userRole)
	if err != nil {
		h.logger.Warn("user not found during login", zap.String("email", loginEmail), zap.Error(err))
		h.security.RecordFailedLogin(c.Context(), c.IP(), loginEmail)
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
	}

	h.logger.Debug("user found during login",
		zap.Uint("user_id", userID),
		zap.String("role", userRole))

	if !h.auth.VerifyPassword(req.Password, passwordHash) {
		h.logger.Warn("password verification failed",
			zap.String("email", loginEmail),
			zap.Uint("user_id", userID))
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid credentials"})
	}

	h.security.RecordSuccessfulLogin(c.IP())

	if h.rateLimiter != nil {
		h.rateLimiter.ResetLoginLimit(c.IP())
	}

	accessToken, err := h.auth.GenerateAccessToken(userID, auth.Role(userRole))
	if err != nil {
		h.logger.Error("failed to generate access token", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
	}

	refreshToken, expiresAt, err := h.auth.GenerateRefreshToken(userID)
	if err != nil {
		h.logger.Error("failed to generate refresh token", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
	}

	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		Expires:  time.Now().Add(15 * time.Minute),
		HTTPOnly: true,
		Secure:   true,
		// None + Domain=cfg.Auth.CookieDomain lets the
		// browser send this cookie to admin.<parent> AND
		// webmail.<parent> (single sign-on across
		// subdomains). The installer writes a non-empty
		// CookieDomain for production; in dev / docker the
		// field is empty and the cookie is scoped to the
		// response Host.
		SameSite: "None",
		Path:     "/",
		Domain:   h.cfg.Auth.CookieDomain,
	})

	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/api/v1/auth/refresh",
		Domain:   h.cfg.Auth.CookieDomain,
	})

	h.logger.Info("user logged in", zap.Uint("user_id", userID))

	return c.JSON(fiber.Map{
		"access_token":       accessToken,
		"access_expires_in":  900,
		"refresh_expires_in": int(30 * 24 * 3600),
	})
}

// Refresh handles token refresh.
func (h *Handler) Refresh(c fiber.Ctx) error {
	refreshToken := c.Cookies("refresh_token")
	if refreshToken == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "refresh token required"})
	}

	accessToken, newRefresh, expiresAt, err := h.auth.RefreshToken(c.Context(), refreshToken)
	if err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid refresh token"})
	}

	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    accessToken,
		Expires:  time.Now().Add(15 * time.Minute),
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/",
		Domain:   h.cfg.Auth.CookieDomain,
	})

	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    newRefresh,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/api/v1/auth/refresh",
		Domain:   h.cfg.Auth.CookieDomain,
	})

	return c.JSON(fiber.Map{"status": "ok"})
}

// clearAuthCookies sends Set-Cookie headers that expire
// the access_token and refresh_token cookies on the same
// Domain the server wrote them with. fiber's ClearCookie
// helper does not include the Domain attribute, so we
// write the empty cookies explicitly to invalidate a
// Domain=.parent.com cookie issued at login.
func (h *Handler) clearAuthCookies(c fiber.Ctx) {
	expiry := time.Now().Add(-1 * time.Hour)
	c.Cookie(&fiber.Cookie{
		Name:     "access_token",
		Value:    "",
		Expires:  expiry,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/",
		Domain:   h.cfg.Auth.CookieDomain,
	})
	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Expires:  expiry,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "None",
		Path:     "/api/v1/auth/refresh",
		Domain:   h.cfg.Auth.CookieDomain,
	})
}

// Logout clears auth cookies.
func (h *Handler) Logout(c fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uint)
	if ok {
		_ = h.auth.InvalidateAllSessions(userID)
	}
	h.clearAuthCookies(c)
	h.writeAuditLog(c, "auth.logout", "")
	return c.JSON(fiber.Map{"status": "logged out"})
}

// LogoutAll invalidates all sessions for the current user.
func (h *Handler) LogoutAll(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	if err := h.auth.InvalidateAllSessions(userID); err != nil {
		h.logger.Error("failed to invalidate all sessions", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "logout failed"})
	}
	h.clearAuthCookies(c)
	h.writeAuditLog(c, "auth.logout_all", "")
	return c.JSON(fiber.Map{"status": "all sessions invalidated"})
}

// ChangePassword changes the user's password and invalidates all sessions except current.
func (h *Handler) ChangePassword(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	if len(req.NewPassword) < h.cfg.Auth.PasswordMinLen {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": fmt.Sprintf("password must be at least %d characters", h.cfg.Auth.PasswordMinLen),
		})
	}

	var user struct {
		ID           uint
		PasswordHash string
	}
	if err := h.db.Table("users").First(&user, userID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}

	if !h.auth.VerifyPassword(req.CurrentPassword, user.PasswordHash) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "current password is incorrect"})
	}

	newHash, err := h.auth.HashPassword(req.NewPassword)
	if err != nil {
		h.logger.Error("failed to hash password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "password change failed"})
	}

	if err := h.db.Table("users").Where("id = ?", userID).Update("password_hash", newHash).Error; err != nil {
		h.logger.Error("failed to update password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "password change failed"})
	}

	if err := h.auth.InvalidateAllSessions(userID); err != nil {
		h.logger.Error("failed to invalidate sessions after password change", zap.Error(err))
	}

	h.logger.Info("password changed, all sessions invalidated", zap.Uint("user_id", userID))
	h.writeAuditLog(c, "auth.password_change", "")

	c.ClearCookie("access_token")
	c.ClearCookie("refresh_token")

	return c.JSON(fiber.Map{"status": "password changed, please login again"})
}

// ListAPIKeys returns all API keys for the current user.
func (h *Handler) ListAPIKeys(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	keys, err := h.apikeys.List(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list API keys"})
	}
	type safeKey struct {
		ID        uint      `json:"id"`
		Name      string    `json:"name"`
		KeyPrefix string    `json:"key_prefix"`
		Enabled   bool      `json:"enabled"`
		LastUsed  time.Time `json:"last_used"`
		ExpiresAt time.Time `json:"expires_at"`
		CreatedAt time.Time `json:"created_at"`
	}
	result := make([]safeKey, 0, len(keys))
	for _, k := range keys {
		result = append(result, safeKey{
			ID:        k.ID,
			Name:      k.Name,
			KeyPrefix: k.KeyPrefix,
			Enabled:   k.Enabled,
			LastUsed:  k.LastUsed,
			ExpiresAt: k.ExpiresAt,
			CreatedAt: k.CreatedAt,
		})
	}
	return c.JSON(result)
}

// CreateAPIKey generates a new API key.
func (h *Handler) CreateAPIKey(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)
	role := string(c.Locals("role").(auth.Role))

	var req struct {
		Name string `json:"name"`
		TTL  string `json:"ttl"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name is required"})
	}

	if !h.features.IsEnabled("rest_api") {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "API keys require ISP or Enterprise license"})
	}

	ttl := 365 * 24 * time.Hour
	if req.TTL != "" {
		if d, err := time.ParseDuration(req.TTL); err == nil {
			ttl = d
		}
	}

	fullKey, record, err := h.apikeys.Generate(req.Name, userID, role, ttl)
	if err != nil {
		h.logger.Error("failed to generate API key", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "API key generation failed"})
	}

	h.writeAuditLog(c, "apikey.create", fmt.Sprintf("name:%s|prefix:%s", req.Name, record.KeyPrefix))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"api_key":    fullKey,
		"key_prefix": record.KeyPrefix,
		"name":       record.Name,
		"expires_at": record.ExpiresAt,
		"warning":    "Save this key now - it will not be shown again",
	})
}

// DeleteAPIKey revokes an API key.
func (h *Handler) DeleteAPIKey(c fiber.Ctx) error {
	idStr := c.Params("id")
	var id uint
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid id"})
	}
	if err := h.apikeys.Revoke(id); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "API key not found"})
	}
	h.writeAuditLog(c, "apikey.revoke", fmt.Sprintf("id:%d", id))
	return c.JSON(fiber.Map{"status": "revoked"})
}

// Me returns the current user's profile.
func (h *Handler) Me(c fiber.Ctx) error {
	userID := c.Locals("user_id").(uint)

	var email, role string

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	err = sqlDB.QueryRow("SELECT email, role FROM users WHERE id = ?", userID).Scan(&email, &role)
	if err != nil {
		h.logger.Warn("user not found", zap.Uint("user_id", userID), zap.Error(err))
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}

	return c.JSON(fiber.Map{
		"id":    userID,
		"email": email,
		"role":  role,
	})
}

// ListDomains returns all mail domains with live mailbox counts.
//
// Optional server-side filter query params:
//   - q=<substring> : case-insensitive substring match on domain
//   - status=active|suspended : exact match on status
func (h *Handler) ListDomains(c fiber.Ctx) error {
	type domainRow struct {
		ID           uint   `json:"id"`
		Domain       string `json:"domain"`
		Plan         string `json:"plan"`
		Status       string `json:"status"`
		MailboxCount int    `json:"mailbox_count"`
	}
	var result []domainRow

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.JSON([]domainRow{})
	}

	q := strings.TrimSpace(c.Query("q"))
	statusFilter := strings.TrimSpace(c.Query("status"))

	confs := []string{"deleted_at IS NULL"}
	args := []interface{}{}
	if q != "" {
		confs = append(confs, "LOWER(name) LIKE ?")
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	if statusFilter == "active" || statusFilter == "suspended" {
		confs = append(confs, "status = ?")
		args = append(args, statusFilter)
	}
	where := " WHERE " + strings.Join(confs, " AND ")

	type rawDomain struct {
		ID     uint
		Name   string
		Plan   string
		Status string
	}
	var rawDomains []rawDomain
	rows, err := sqlDB.Query("SELECT id, name, plan, status FROM coremail_domains"+where+" ORDER BY id DESC", args...)
	if err != nil {
		return c.JSON([]domainRow{})
	}
	defer rows.Close()
	for rows.Next() {
		var rd rawDomain
		if err := rows.Scan(&rd.ID, &rd.Name, &rd.Plan, &rd.Status); err != nil {
			continue
		}
		rawDomains = append(rawDomains, rd)
	}

	for _, rd := range rawDomains {
		var mailboxCount int64
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = ? AND deleted_at IS NULL", rd.ID).Scan(&mailboxCount)
		result = append(result, domainRow{ID: rd.ID, Domain: rd.Name, Plan: rd.Plan, Status: rd.Status, MailboxCount: int(mailboxCount)})
	}

	if result == nil {
		result = []domainRow{}
	}
	return c.JSON(result)
}

// CreateDomain creates a new mail domain in CoreMail.
func (h *Handler) CreateDomain(c fiber.Ctx) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	domainName := strings.ToLower(strings.TrimSpace(req.Name))
	if strings.Contains(domainName, "://") || strings.Contains(domainName, "/") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain: no protocol or path allowed"})
	}
	if strings.Contains(domainName, " ") || strings.Contains(domainName, "*") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain: no spaces or wildcards"})
	}
	parts := strings.Split(domainName, ".")
	if len(parts) < 2 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain: must be a fully qualified domain name"})
	}
	for _, part := range parts {
		if part == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid domain: consecutive dots"})
		}
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var existing int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_domains WHERE name = ? AND deleted_at IS NULL", domainName).Scan(&existing)
	if existing > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "domain already exists: " + domainName})
	}

	now := time.Now().UTC()
	result, err := sqlDB.Exec(
		`INSERT INTO coremail_domains (name, tenant_id, reseller_id, status, plan, description, max_mailboxes, max_aliases, max_quota_mb, dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels, mailbox_count, created_at, updated_at)
		 VALUES (?, 0, 0, 'active', 'smb', '', 0, 0, 0, 0, '', 0, 0, '', '', '', 0, ?, ?)`,
		domainName, now, now,
	)
	if err != nil {
		h.logger.Error("failed to create domain", zap.String("domain", domainName), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create domain"})
	}

	domainID, _ := result.LastInsertId()
	h.writeAuditLog(c, "domain.create", fmt.Sprintf("domain:%s", domainName))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":     domainID,
		"domain": domainName,
		"status": "active",
	})
}

// DeleteDomain soft-deletes a mail domain. Domain must have zero active mailboxes.
func (h *Handler) DeleteDomain(c fiber.Ctx) error {
	idStr := c.Params("name")
	if idStr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var domainID uint
	var domainName string
	err = sqlDB.QueryRow("SELECT id, name FROM coremail_domains WHERE name = ? AND deleted_at IS NULL", idStr).Scan(&domainID, &domainName)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found: " + idStr})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var mailboxCount int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = ? AND deleted_at IS NULL", domainID).Scan(&mailboxCount)
	if mailboxCount > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "domain contains mailboxes", "mailbox_count": mailboxCount})
	}

	now := time.Now().UTC()
	_, err = sqlDB.Exec("UPDATE coremail_domains SET deleted_at = ?, updated_at = ? WHERE id = ?", now, now, domainID)
	if err != nil {
		h.logger.Error("failed to delete domain", zap.String("domain", domainName), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
	}

	h.writeAuditLog(c, "domain.delete", fmt.Sprintf("domain:%s", domainName))
	return c.JSON(fiber.Map{"status": "deleted", "domain": domainName})
}

// UpdateDomainStatus enables or disables a domain.
func (h *Handler) UpdateDomainStatus(c fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Status != "active" && req.Status != "suspended" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status must be 'active' or 'suspended'"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var domainID uint
	err = sqlDB.QueryRow("SELECT id FROM coremail_domains WHERE name = ? AND deleted_at IS NULL", name).Scan(&domainID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found: " + name})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	_, err = sqlDB.Exec("UPDATE coremail_domains SET status = ?, updated_at = ? WHERE id = ?", req.Status, time.Now().UTC(), domainID)
	if err != nil {
		h.logger.Error("failed to update domain status", zap.String("domain", name), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "status update failed"})
	}

	h.writeAuditLog(c, "domain.status_update", fmt.Sprintf("domain:%s|status:%s", name, req.Status))
	return c.JSON(fiber.Map{"result": "updated", "domain": name, "status": req.Status})
}

// GetDomain returns details for a single domain.
func (h *Handler) GetDomain(c fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var domainID uint
	var domainName, status, plan, createdAt, updatedAt string
	err = sqlDB.QueryRow("SELECT id, name, status, plan, created_at, updated_at FROM coremail_domains WHERE name = ? AND deleted_at IS NULL", name).Scan(&domainID, &domainName, &status, &plan, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var mailboxCount int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = ? AND deleted_at IS NULL", domainID).Scan(&mailboxCount)

	type briefMailbox struct {
		MailboxID uint   `json:"mailbox_id"`
		Email     string `json:"email"`
		Status    string `json:"status"`
		IsAdmin   bool   `json:"is_admin"`
	}
	var mailboxes []briefMailbox
	mbRows, err := sqlDB.Query("SELECT id, email, status, is_admin FROM coremail_mailboxes WHERE domain_id = ? AND deleted_at IS NULL ORDER BY id DESC LIMIT 200", domainID)
	if err == nil {
		defer mbRows.Close()
		for mbRows.Next() {
			var mb briefMailbox
			var isAdmin int
			if err := mbRows.Scan(&mb.MailboxID, &mb.Email, &mb.Status, &isAdmin); err == nil {
				mb.IsAdmin = isAdmin == 1
				mailboxes = append(mailboxes, mb)
			}
		}
	}
	if mailboxes == nil {
		mailboxes = []briefMailbox{}
	}

	return c.JSON(fiber.Map{
		"domain":        domainName,
		"status":        status,
		"plan":          plan,
		"mailbox_count": mailboxCount,
		"created_at":    createdAt,
		"updated_at":    updatedAt,
		"deleted":       false,
		"mailboxes":     mailboxes,
	})
}

// GetMailbox returns details for a single mailbox.
func (h *Handler) GetMailbox(c fiber.Ctx) error {
	idStr := c.Params("id")
	if idStr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mailbox id required"})
	}
	id, parseErr := parseUint(idStr)
	if parseErr != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var email, domainName, status, createdAt, updatedAt string
	var isAdmin int
	err = sqlDB.QueryRow(`SELECT m.email, COALESCE(d.name, ''), m.status, m.is_admin, m.created_at, m.updated_at
		FROM coremail_mailboxes m LEFT JOIN coremail_domains d ON m.domain_id = d.id
		WHERE m.id = ? AND m.deleted_at IS NULL`, id).Scan(&email, &domainName, &status, &isAdmin, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var messages int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_messages WHERE mailbox_id = ? AND purged_at IS NULL", id).Scan(&messages)

	var queueItems int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE mailbox_id = ? AND deleted_at IS NULL", id).Scan(&queueItems)

	return c.JSON(fiber.Map{
		"mailbox_id": id,
		"email":      email,
		"domain":     domainName,
		"status":     status,
		"is_admin":   isAdmin == 1,
		"created_at": createdAt,
		"updated_at": updatedAt,
		"deleted":    false,
		"stats": fiber.Map{
			"messages":    messages,
			"queue_items": queueItems,
		},
	})
}

// ListUsers returns all users/mailboxes with explicit identity contract.
// CoreMail mailbox rows: mailbox_id is set, user_id linked from users table if matching email.
// User-only rows (no coremail_mailboxes): mailbox_id is null, user_id is set, status is "user-only".
//
// Optional server-side filter query params:
//   - q=<substring> : case-insensitive substring match on email
//   - status=active|suspended : exact match on mailbox status
//   - admin=true|false : true keeps admin mailboxes, false excludes them
func (h *Handler) ListUsers(c fiber.Ctx) error {
	type userRow struct {
		MailboxID *uint  `json:"mailbox_id"`
		UserID    *uint  `json:"user_id"`
		Email     string `json:"email"`
		Role      string `json:"role"`
		IsAdmin   bool   `json:"is_admin"`
		Status    string `json:"status"`
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.JSON([]userRow{})
	}

	q := strings.TrimSpace(c.Query("q"))
	statusFilter := strings.TrimSpace(c.Query("status"))
	adminFilter := strings.ToLower(strings.TrimSpace(c.Query("admin")))

	// Build mailbox WHERE clause using parameterized values.
	mbConfs := []string{"deleted_at IS NULL"}
	mbArgs := []interface{}{}
	if q != "" {
		mbConfs = append(mbConfs, "LOWER(email) LIKE ?")
		mbArgs = append(mbArgs, "%"+strings.ToLower(q)+"%")
	}
	if statusFilter == "active" || statusFilter == "suspended" {
		mbConfs = append(mbConfs, "status = ?")
		mbArgs = append(mbArgs, statusFilter)
	}
	// admin=true keeps admin mailboxes; admin=false excludes them. When the caller
	// filters by status, admin mailboxes are still mailbox rows so the same
	// filter applies.
	switch adminFilter {
	case "true":
		mbConfs = append(mbConfs, "is_admin = 1")
	case "false":
		mbConfs = append(mbConfs, "is_admin = 0")
	}
	mbWhere := " WHERE " + strings.Join(mbConfs, " AND ")

	byEmail := make(map[string]*userRow)

	rows, err := sqlDB.Query("SELECT id, email, is_admin, status FROM coremail_mailboxes"+mbWhere+" ORDER BY id DESC", mbArgs...)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id uint
			var email, status string
			var isAdmin int
			if err := rows.Scan(&id, &email, &isAdmin, &status); err != nil {
				continue
			}
			role := "mailbox"
			if isAdmin == 1 {
				role = "admin"
			}
			mailboxID := id
			byEmail[email] = &userRow{
				MailboxID: &mailboxID,
				UserID:    nil,
				Email:     email,
				Role:      role,
				IsAdmin:   isAdmin == 1,
				Status:    status,
			}
		}
	}

	// user-only rows (no coremail_mailboxes) — only relevant when admin filter is not active.
	// We always read users so we can attach user_id; then we narrow by q/admin/status below.
	rows, err = sqlDB.Query("SELECT id, email, role, active FROM users ORDER BY id DESC")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id uint
			var email, role string
			var active int
			if err := rows.Scan(&id, &email, &role, &active); err != nil {
				continue
			}
			if existing, ok := byEmail[email]; ok {
				userID := id
				existing.UserID = &userID
			} else {
				// Apply q, admin, and status filters to user-only rows too.
				// User-only rows have status "user-only" which never matches
				// "active"/"suspended", so a status filter excludes them.
				if statusFilter == "active" || statusFilter == "suspended" {
					continue
				}
				if q != "" && !strings.Contains(strings.ToLower(email), strings.ToLower(q)) {
					continue
				}
				isAdmin := role == "admin" || role == "superadmin"
				switch adminFilter {
				case "true":
					if !isAdmin {
						continue
					}
				case "false":
					if isAdmin {
						continue
					}
				}
				userID := id
				byEmail[email] = &userRow{
					MailboxID: nil,
					UserID:    &userID,
					Email:     email,
					Role:      role,
					IsAdmin:   isAdmin,
					Status:    "user-only",
				}
			}
		}
	}

	users := make([]userRow, 0, len(byEmail))
	for _, u := range byEmail {
		users = append(users, *u)
	}
	if users == nil {
		users = []userRow{}
	}
	return c.JSON(users)
}

// CreateUser creates a new mailbox user.
func (h *Handler) CreateUser(c fiber.Ctx) error {
	var req struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Quota    int64  `json:"quota"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	if req.Name == "" || req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, email, and password required"})
	}

	passwordHash, err := h.auth.HashPassword(req.Password)
	if err != nil {
		h.logger.Error("failed to hash password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "user creation failed"})
	}

	user := struct {
		Name         string
		Email        string
		PasswordHash string
		Role         string
		Quota        int64
	}{
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: passwordHash,
		Role:         "user",
		Quota:        req.Quota,
	}

	if err := h.db.Table("users").Create(&user).Error; err != nil {
		h.logger.Error("failed to persist user", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "user creation failed"})
	}

	h.writeAuditLog(c, "user.create", fmt.Sprintf("user:%s", req.Email))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "created", "email": req.Email})
}

// CreateMailbox creates a new CoreMail mailbox.
func (h *Handler) CreateMailbox(c fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		QuotaMB  int64  `json:"quota_mb"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}

	req.Email = strings.TrimSpace(req.Email)
	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email and password required"})
	}

	if len(req.Password) < h.cfg.Auth.PasswordMinLen {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("password must be at least %d characters", h.cfg.Auth.PasswordMinLen)})
	}

	parts := strings.SplitN(req.Email, "@", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email format"})
	}
	parsed, err := mail.ParseAddress(req.Email)
	if err != nil || parsed.Address != req.Email || strings.ContainsAny(req.Email, " \t\r\n") {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid email format"})
	}
	localPart := parts[0]
	domainName := parts[1]

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var domainID uint
	var tenantID uint
	var domainStatus string
	err = sqlDB.QueryRow("SELECT id, tenant_id, status FROM coremail_domains WHERE name = ? AND deleted_at IS NULL", domainName).Scan(&domainID, &tenantID, &domainStatus)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found: " + domainName})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if domainStatus != "active" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain is not active: " + domainName})
	}

	var existing int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE email = ? AND deleted_at IS NULL", req.Email).Scan(&existing)
	if existing > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "mailbox already exists: " + req.Email})
	}

	argon2Hash, err := hashPasswordArgon2id(req.Password)
	if err != nil {
		h.logger.Error("failed to hash mailbox password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "mailbox creation failed"})
	}

	displayName := req.Name
	if displayName == "" {
		displayName = localPart
	}
	quotaMB := req.QuotaMB
	if quotaMB <= 0 {
		quotaMB = 1024
	}

	result, err := sqlDB.Exec(
		`INSERT INTO coremail_mailboxes (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status, quota_mb, is_admin, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'argon2id', 'active', ?, 0, ?, ?)`,
		domainID, tenantID, localPart, req.Email, displayName, argon2Hash, quotaMB, time.Now().UTC(), time.Now().UTC(),
	)
	if err != nil {
		h.logger.Error("failed to create mailbox", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "mailbox creation failed"})
	}

	mailboxID, _ := result.LastInsertId()

	// Provision the canonical system folders
	// (INBOX, Sent, Drafts, Trash, Junk, Archive) for
	// the freshly created mailbox. Without this, the
	// first time the user opens Webmail and tries to
	// send, the Send handler returns
	//   "Sent folder not found for mailbox;
	//    ensure system folders are provisioned"
	// and the inbox/folder list renders empty.
	//
	// The provision is best-effort: if it fails the
	// mailbox row is still created and the operator
	// can re-run the provision by calling
	// coremail.EnsureMailboxSystemFolders directly
	// (e.g. via a one-off migration). The webmail
	// login handler also re-runs the provision so
	// legacy mailboxes that were created before this
	// fix get patched up the first time their owner
	// logs in via webmail.
	if err := coremail.EnsureMailboxSystemFolders(c.Context(), sqlDB, uint(mailboxID)); err != nil {
		h.logger.Warn("CreateMailbox: ensure system folders",
			zap.String("email", req.Email),
			zap.Int64("mailbox_id", mailboxID),
			zap.Error(err))
	}

	h.writeAuditLog(c, "mailbox.create", fmt.Sprintf("mailbox:%s", req.Email))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":     mailboxID,
		"email":  req.Email,
		"status": "active",
		"domain": domainName,
		"quota":  quotaMB,
	})
}

// hashPasswordArgon2id creates an Argon2id password hash.
func hashPasswordArgon2id(password string) (string, error) {
	const (
		argon2Time    uint32 = 3
		argon2Mem     uint32 = 65536
		argon2Threads uint8  = 4
		argon2KeyLen  uint32 = 32
	)
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argon2Time, argon2Mem, argon2Threads, argon2KeyLen)
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argon2Mem, argon2Time, argon2Threads, b64Salt, b64Hash), nil
}

// UpdateMailboxPassword resets a CoreMail mailbox password.
func (h *Handler) UpdateMailboxPassword(c fiber.Ctx) error {
	idStr := c.Params("id")
	var id uint
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if len(req.Password) < h.cfg.Auth.PasswordMinLen {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("password must be at least %d characters", h.cfg.Auth.PasswordMinLen)})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var email string
	err = sqlDB.QueryRow("SELECT email FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL", id).Scan(&email)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	argon2Hash, err := hashPasswordArgon2id(req.Password)
	if err != nil {
		h.logger.Error("failed to hash password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "password update failed"})
	}

	_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET password_hash = ?, updated_at = ? WHERE id = ?", argon2Hash, time.Now().UTC(), id)
	if err != nil {
		h.logger.Error("failed to update mailbox password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "password update failed"})
	}

	h.writeAuditLog(c, "mailbox.password_reset", fmt.Sprintf("mailbox_id:%d|email:%s", id, email))
	return c.JSON(fiber.Map{"status": "password updated", "email": email})
}

// UpdateMailboxStatus enables or disables a CoreMail mailbox.
func (h *Handler) UpdateMailboxStatus(c fiber.Ctx) error {
	idStr := c.Params("id")
	var id uint
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}

	var req struct {
		Status string `json:"status"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Status != "active" && req.Status != "suspended" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status must be 'active' or 'suspended'"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var email string
	var isAdmin int
	err = sqlDB.QueryRow("SELECT email, is_admin FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL", id).Scan(&email, &isAdmin)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	if isAdmin == 1 && req.Status != "active" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "cannot disable admin mailbox"})
	}

	_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET status = ?, updated_at = ? WHERE id = ?", req.Status, time.Now().UTC(), id)
	if err != nil {
		h.logger.Error("failed to update mailbox status", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "status update failed"})
	}

	h.writeAuditLog(c, "mailbox.status_update", fmt.Sprintf("mailbox_id:%d|email:%s|status:%s", id, email, req.Status))
	return c.JSON(fiber.Map{"result": "updated", "email": email, "status": req.Status})
}

// BulkMailboxStatus updates status for a set of mailboxes in a single admin call.
//
// Request body: {"mailbox_ids": [1,2,3], "status": "active"|"suspended"}
// Admin mailboxes (is_admin = 1) and soft-deleted rows are skipped silently
// (still counted in `skipped`). Only mailboxes whose current status differs
// from the requested status are actually written. A single bulk audit log
// entry "mailbox.bulk_status" is written per call.
//
// Response: {"updated": <int>, "skipped": <int>}
func (h *Handler) BulkMailboxStatus(c fiber.Ctx) error {
	var req struct {
		MailboxIDs []uint `json:"mailbox_ids"`
		Status     string `json:"status"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Status != "active" && req.Status != "suspended" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status must be 'active' or 'suspended'"})
	}
	if len(req.MailboxIDs) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mailbox_ids required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	updated := 0
	skipped := 0
	now := time.Now().UTC()

	for _, id := range req.MailboxIDs {
		if id == 0 {
			skipped++
			continue
		}
		// RACE-SAFE UPDATE: the safety predicate is enforced atomically
		// at the database write site. We never trust a pre-check result;
		// if the row was soft-deleted, flipped to admin, or no longer
		// matches the requested status, RowsAffected will be 0 and we
		// count it as skipped. Admin mailboxes can never be touched
		// by this bulk endpoint.
		res, err := sqlDB.Exec(
			"UPDATE coremail_mailboxes SET status = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL AND is_admin = 0",
			req.Status, now, id,
		)
		if err != nil {
			h.logger.Error("bulk mailbox update failed", zap.Uint("mailbox_id", id), zap.Error(err))
			skipped++
			continue
		}
		rows, raErr := res.RowsAffected()
		if raErr != nil {
			h.logger.Error("bulk mailbox rows-affected failed", zap.Uint("mailbox_id", id), zap.Error(raErr))
			skipped++
			continue
		}
		if rows == 0 {
			// Row either does not exist, is soft-deleted, is admin, or
			// (in MySQL/SQLite) the status already matched. Treat as
			// skipped — never as updated.
			skipped++
			continue
		}
		updated++
	}

	h.writeAuditLog(c, "mailbox.bulk_status", fmt.Sprintf("count:%d|status:%s|updated:%d|skipped:%d", len(req.MailboxIDs), req.Status, updated, skipped))
	return c.JSON(fiber.Map{"updated": updated, "skipped": skipped})
}

// BulkDomainStatus updates status for a set of domains in a single admin call.
//
// Request body: {"domains": ["orvix.email","example.com"], "status": "active"|"suspended"}
// Unknown / soft-deleted domains are skipped silently. Only domains whose
// current status differs from the requested status are actually written. A
// single bulk audit log entry "domain.bulk_status" is written per call.
//
// Response: {"updated": <int>, "skipped": <int>}
func (h *Handler) BulkDomainStatus(c fiber.Ctx) error {
	var req struct {
		Domains []string `json:"domains"`
		Status  string   `json:"status"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Status != "active" && req.Status != "suspended" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status must be 'active' or 'suspended'"})
	}
	if len(req.Domains) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domains required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	updated := 0
	skipped := 0
	now := time.Now().UTC()

	for _, name := range req.Domains {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			skipped++
			continue
		}
		// RACE-SAFE UPDATE: the soft-delete check is enforced atomically
		// at the database write site. We do not pre-fetch; we run a
		// single UPDATE whose predicate covers both the row match and
		// the not-deleted invariant. RowsAffected == 0 means the row
		// did not exist, was soft-deleted, or already matched the new
		// status — in all cases it is correctly NOT counted as updated.
		res, err := sqlDB.Exec(
			"UPDATE coremail_domains SET status = ?, updated_at = ? WHERE name = ? AND deleted_at IS NULL",
			req.Status, now, name,
		)
		if err != nil {
			h.logger.Error("bulk domain update failed", zap.String("domain", name), zap.Error(err))
			skipped++
			continue
		}
		rows, raErr := res.RowsAffected()
		if raErr != nil {
			h.logger.Error("bulk domain rows-affected failed", zap.String("domain", name), zap.Error(raErr))
			skipped++
			continue
		}
		if rows == 0 {
			skipped++
			continue
		}
		updated++
	}

	h.writeAuditLog(c, "domain.bulk_status", fmt.Sprintf("count:%d|status:%s|updated:%d|skipped:%d", len(req.Domains), req.Status, updated, skipped))
	return c.JSON(fiber.Map{"updated": updated, "skipped": skipped})
}

// DeleteMailbox soft-deletes a CoreMail mailbox.
func (h *Handler) DeleteMailbox(c fiber.Ctx) error {
	idStr := c.Params("id")
	var id uint
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var email string
	var isAdmin int
	err = sqlDB.QueryRow("SELECT email, is_admin FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL", id).Scan(&email, &isAdmin)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	if isAdmin == 1 {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "cannot delete admin mailbox"})
	}

	now := time.Now().UTC()
	_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET deleted_at = ?, updated_at = ? WHERE id = ?", now, now, id)
	if err != nil {
		h.logger.Error("failed to delete mailbox", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
	}

	h.writeAuditLog(c, "mailbox.delete", fmt.Sprintf("mailbox_id:%d|email:%s", id, email))
	return c.JSON(fiber.Map{"status": "deleted", "email": email})
}

// DeleteUser removes a user.
func (h *Handler) DeleteUser(c fiber.Ctx) error {
	id := c.Params("id")
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "user id required"})
	}

	result := h.db.Table("users").Delete(&struct{ ID uint }{}, id)
	if result.Error != nil {
		h.logger.Error("failed to delete user", zap.String("id", id), zap.Error(result.Error))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to delete user"})
	}

	h.writeAuditLog(c, "user.delete", fmt.Sprintf("user:%s", id))
	return c.JSON(fiber.Map{"status": "deleted"})
}

// ListQueue returns the mail queue with safe fields only.
func (h *Handler) ListQueue(c fiber.Ctx) error {
	type queueEntry struct {
		ID          uint   `json:"id"`
		MessageID   string `json:"message_id"`
		From        string `json:"from"`
		To          string `json:"to"`
		Status      string `json:"status"`
		Attempts    int    `json:"attempts"`
		NextAttempt string `json:"next_attempt_at"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}
	var result []queueEntry

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.JSON([]queueEntry{})
	}

	type rawQueue struct {
		ID           uint
		MessageID    sql.NullString
		FromAddress  string
		ToAddress    string
		Status       string
		AttemptCount int
		NextAttempt  sql.NullString
		CreatedAt    sql.NullString
		UpdatedAt    sql.NullString
	}
	var raw []rawQueue
	rows, err := sqlDB.Query("SELECT id, message_id, from_address, to_address, status, attempt_count, next_attempt_at, created_at, updated_at FROM coremail_queue WHERE deleted_at IS NULL ORDER BY id DESC LIMIT 200")
	if err != nil {
		return c.JSON([]queueEntry{})
	}
	defer rows.Close()
	for rows.Next() {
		var r rawQueue
		if err := rows.Scan(&r.ID, &r.MessageID, &r.FromAddress, &r.ToAddress, &r.Status, &r.AttemptCount, &r.NextAttempt, &r.CreatedAt, &r.UpdatedAt); err != nil {
			continue
		}
		raw = append(raw, r)
	}

	for _, r := range raw {
		msgID := ""
		if r.MessageID.Valid {
			msgID = r.MessageID.String
		}
		nextAtt := ""
		if r.NextAttempt.Valid {
			nextAtt = r.NextAttempt.String
		}
		createdAt := ""
		if r.CreatedAt.Valid {
			createdAt = r.CreatedAt.String
		}
		updatedAt := ""
		if r.UpdatedAt.Valid {
			updatedAt = r.UpdatedAt.String
		}
		result = append(result, queueEntry{
			ID:          r.ID,
			MessageID:   msgID,
			From:        r.FromAddress,
			To:          r.ToAddress,
			Status:      r.Status,
			Attempts:    r.AttemptCount,
			NextAttempt: nextAtt,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
		})
	}

	if result == nil {
		result = []queueEntry{}
	}
	return c.JSON(result)
}

// DeleteQueue soft-deletes a queued message.
func (h *Handler) DeleteQueue(c fiber.Ctx) error {
	idStr := c.Params("id")
	if idStr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "queue id required"})
	}
	id, parseErr := parseUint(idStr)
	if parseErr != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid queue id"})
	}

	sqlDB, dbErr := h.db.DB()
	if dbErr != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	now := time.Now().UTC()
	res, execErr := sqlDB.Exec("UPDATE coremail_queue SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL", now, now, id)
	if execErr != nil {
		h.logger.Error("failed to delete queue item", zap.Int64("id", id), zap.Error(execErr))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "delete failed"})
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "queue item not found"})
	}

	h.writeAuditLog(c, "queue.delete", fmt.Sprintf("queue_id:%d", id))
	return c.JSON(fiber.Map{"id": id, "deleted": true})
}

// RetryQueue resets a queued message for retry.
func (h *Handler) RetryQueue(c fiber.Ctx) error {
	idStr := c.Params("id")
	if idStr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "queue id required"})
	}
	id, parseErr := parseUint(idStr)
	if parseErr != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid queue id"})
	}

	sqlDB, dbErr := h.db.DB()
	if dbErr != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var currentAttempts int
	scanErr := sqlDB.QueryRow("SELECT attempt_count FROM coremail_queue WHERE id = ? AND deleted_at IS NULL", id).Scan(&currentAttempts)
	if scanErr == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "queue item not found"})
	}
	if scanErr != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	now := time.Now().UTC()
	res, execErr := sqlDB.Exec(
		"UPDATE coremail_queue SET status = 'pending', lease_owner = '', lease_expires_at = NULL, next_attempt_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		now, now, id,
	)
	if execErr != nil {
		h.logger.Error("failed to retry queue item", zap.Int64("id", id), zap.Error(execErr))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "retry failed"})
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "queue item not found"})
	}

	h.writeAuditLog(c, "queue.retry", fmt.Sprintf("queue_id:%d", id))
	return c.JSON(fiber.Map{"id": id, "status": "pending", "attempts": currentAttempts, "next_attempt_at": now.Format(time.RFC3339)})
}

// ListFirewallRules returns firewall rules.
func (h *Handler) ListFirewallRules(c fiber.Ctx) error {
	var rules []models.FirewallRule
	h.db.Order("priority asc").Find(&rules)
	return c.JSON(rules)
}

// CreateFirewallRule creates a new firewall rule.
func (h *Handler) CreateFirewallRule(c fiber.Ctx) error {
	var rule models.FirewallRule
	if err := c.Bind().JSON(&rule); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid rule"})
	}
	if err := h.db.Create(&rule).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create rule"})
	}
	h.writeAuditLog(c, "firewall.rule.create", fmt.Sprintf("rule:%s", rule.Name))
	return c.Status(fiber.StatusCreated).JSON(rule)
}

// ListFirewallLogs returns firewall logs.
func (h *Handler) ListFirewallLogs(c fiber.Ctx) error {
	var logs []models.FirewallLog
	h.db.Order("created_at desc").Limit(100).Find(&logs)
	return c.JSON(logs)
}

// ListModules returns registered modules.
func (h *Handler) ListModules(c fiber.Ctx) error {
	type moduleInfo struct {
		ID      string `json:"id"`
		Version string `json:"version"`
		Status  string `json:"status"`
	}
	var modules []moduleInfo
	for _, m := range h.registry.All() {
		status := "active"
		modules = append(modules, moduleInfo{
			ID:      m.ID(),
			Version: m.Version(),
			Status:  status,
		})
	}
	return c.JSON(modules)
}

// GetLicense returns current license info.
func (h *Handler) GetLicense(c fiber.Ctx) error {
	var lic models.License
	if err := h.db.Where("active = ?", true).Last(&lic).Error; err != nil {
		return c.JSON(fiber.Map{"status": "no license", "tier": "community"})
	}
	return c.JSON(fiber.Map{
		"tier":          lic.Tier,
		"expires_at":    lic.ExpiresAt,
		"max_domains":   lic.MaxDomains,
		"max_mailboxes": lic.MaxMailboxes,
	})
}

// ValidateLicense validates a license key.
func (h *Handler) ValidateLicense(c fiber.Ctx) error {
	var req struct {
		Key string `json:"key"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "license key required"})
	}

	keyHash := fmt.Sprintf("%x", sha256.Sum256([]byte(req.Key)))

	encryptedHash, encErr := config.EncryptString(keyHash)
	if encErr != nil {
		h.logger.Error("failed to encrypt license key hash", zap.Error(encErr))
		encryptedHash = keyHash
	}

	lic := models.License{
		KeyHash:   encryptedHash,
		Tier:      "smb",
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().AddDate(1, 0, 0),
		Active:    true,
	}
	h.db.Create(&lic)

	h.writeAuditLog(c, "license.validate", fmt.Sprintf("tier:%s", lic.Tier))
	return c.JSON(fiber.Map{"status": "valid", "tier": lic.Tier, "expires_at": lic.ExpiresAt})
}

// ListAuditLogs returns audit log entries with safe fields only.
func (h *Handler) ListAuditLogs(c fiber.Ctx) error {
	if h.auditStore == nil {
		return c.JSON([]struct{}{})
	}
	logs, _, err := h.auditStore.Search(c.Context(), &audit.Query{Limit: 100})
	if err != nil {
		h.logger.Error("failed to list audit logs", zap.Error(err))
		return c.JSON([]struct{}{})
	}
	type safeEntry struct {
		ID        int64  `json:"id"`
		Action    string `json:"action"`
		Actor     string `json:"actor"`
		Target    string `json:"target"`
		Result    string `json:"result"`
		Timestamp string `json:"timestamp"`
	}
	var result []safeEntry
	for _, e := range logs {
		result = append(result, safeEntry{
			ID:        e.ID,
			Action:    e.Action,
			Actor:     e.Actor,
			Target:    e.Target,
			Result:    e.Result,
			Timestamp: e.Timestamp.Format(time.RFC3339),
		})
	}
	if result == nil {
		result = []safeEntry{}
	}
	return c.JSON(result)
}

// AdminSummary returns aggregate counts for the admin dashboard.
//
// Extended response (Enterprise Operations Layer v2):
//   - recent_activity : up to 10 audit entries with safe fields only
//   - top_domains     : top 5 domains by live mailbox count
func (h *Handler) AdminSummary(c fiber.Ctx) error {
	domTotal, domActive, domSuspended := int64(0), int64(0), int64(0)
	mbTotal, mbActive, mbSuspended, mbAdmin := int64(0), int64(0), int64(0), int64(0)
	qTotal, qPending, qDeferred, qFailed := int64(0), int64(0), int64(0), int64(0)
	auditRecent := int64(0)

	sqlDB, err := h.db.DB()
	if err == nil {
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_domains WHERE deleted_at IS NULL").Scan(&domTotal)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_domains WHERE deleted_at IS NULL AND status = 'active'").Scan(&domActive)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_domains WHERE deleted_at IS NULL AND status = 'suspended'").Scan(&domSuspended)

		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL").Scan(&mbTotal)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL AND status = 'active'").Scan(&mbActive)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL AND status = 'suspended'").Scan(&mbSuspended)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL AND is_admin = 1").Scan(&mbAdmin)

		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL").Scan(&qTotal)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'pending'").Scan(&qPending)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'deferred'").Scan(&qDeferred)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'dead_letter'").Scan(&qFailed)
	}

	since := time.Now().UTC().Add(-24 * time.Hour)
	if err == nil {
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE timestamp >= ?", since).Scan(&auditRecent)
	}

	runtimeStatus := "ok"
	runtimeVersion := config.GetWatermark().Version

	// recent_activity : up to 10 audit entries, safe fields only.
	type recentActivityEntry struct {
		Action    string `json:"action"`
		Actor     string `json:"actor"`
		Target    string `json:"target"`
		Result    string `json:"result"`
		Timestamp string `json:"timestamp"`
	}
	recentActivity := []recentActivityEntry{}
	if h.auditStore != nil {
		if entries, _, serr := h.auditStore.Search(c.Context(), &audit.Query{Limit: 10}); serr == nil {
			for _, e := range entries {
				recentActivity = append(recentActivity, recentActivityEntry{
					Action:    e.Action,
					Actor:     e.Actor,
					Target:    e.Target,
					Result:    e.Result,
					Timestamp: e.Timestamp.Format(time.RFC3339),
				})
			}
		} else {
			h.logger.Error("failed to load recent activity for admin summary", zap.Error(serr))
		}
	}

	// top_domains : top 5 domains by live mailbox count.
	type topDomainEntry struct {
		Domain       string `json:"domain"`
		MailboxCount int    `json:"mailbox_count"`
	}
	topDomains := []topDomainEntry{}
	if err == nil {
		rows, qerr := sqlDB.Query(`SELECT d.name, COUNT(m.id) AS cnt
			FROM coremail_domains d
			LEFT JOIN coremail_mailboxes m ON m.domain_id = d.id AND m.deleted_at IS NULL
			WHERE d.deleted_at IS NULL
			GROUP BY d.id
			ORDER BY cnt DESC, d.id DESC
			LIMIT 5`)
		if qerr == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				var cnt int64
				if err := rows.Scan(&name, &cnt); err == nil {
					topDomains = append(topDomains, topDomainEntry{Domain: name, MailboxCount: int(cnt)})
				}
			}
		} else {
			h.logger.Error("failed to load top domains for admin summary", zap.Error(qerr))
		}
	}

	return c.JSON(fiber.Map{
		"domains": fiber.Map{
			"total":     domTotal,
			"active":    domActive,
			"suspended": domSuspended,
		},
		"mailboxes": fiber.Map{
			"total":     mbTotal,
			"active":    mbActive,
			"suspended": mbSuspended,
			"admin":     mbAdmin,
		},
		"queue": fiber.Map{
			"total":    qTotal,
			"pending":  qPending,
			"deferred": qDeferred,
			"failed":   qFailed,
		},
		"audit": fiber.Map{
			"recent": auditRecent,
		},
		"runtime": fiber.Map{
			"status":  runtimeStatus,
			"version": runtimeVersion,
		},
		"recent_activity": recentActivity,
		"top_domains":     topDomains,
	})
}

// ExportMailboxesCSV streams all live mailboxes as CSV. Admin-only (router group).
// Columns: email,status,is_admin
// Soft-deleted rows are excluded. NEVER read or include password_hash, password,
// token, jwt, bearer, secret, or any message body / headers.
func (h *Handler) ExportMailboxesCSV(c fiber.Ctx) error {
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	rows, qerr := sqlDB.Query("SELECT email, status, is_admin FROM coremail_mailboxes WHERE deleted_at IS NULL ORDER BY id ASC")
	if qerr != nil {
		h.logger.Error("export mailboxes query failed", zap.Error(qerr))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "export failed"})
	}
	defer rows.Close()

	filename := fmt.Sprintf("mailboxes-%s.csv", time.Now().UTC().Format("2006-01-02"))
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	var b strings.Builder
	b.WriteString("email,status,is_admin\n")
	for rows.Next() {
		var email, status string
		var isAdmin int
		if err := rows.Scan(&email, &status, &isAdmin); err != nil {
			continue
		}
		b.WriteString(csvField(email))
		b.WriteByte(',')
		b.WriteString(csvField(status))
		b.WriteByte(',')
		if isAdmin == 1 {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		b.WriteByte('\n')
	}
	return c.SendString(b.String())
}

// ExportDomainsCSV streams all live domains as CSV. Admin-only (router group).
// Columns: domain,status,plan,mailbox_count
// mailbox_count is the live count from coremail_mailboxes for each domain.
func (h *Handler) ExportDomainsCSV(c fiber.Ctx) error {
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	// Drain the SELECT result first so the underlying connection is free
	// for the per-row COUNT(*) queries below. This avoids a deadlock on
	// single-connection SQLite when running concurrently.
	type domainRow struct {
		ID     uint
		Name   string
		Plan   string
		Status string
	}
	var domainRows []domainRow
	{
		rows, qerr := sqlDB.Query("SELECT id, name, plan, status FROM coremail_domains WHERE deleted_at IS NULL ORDER BY id ASC")
		if qerr != nil {
			h.logger.Error("export domains query failed", zap.Error(qerr))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "export failed"})
		}
		for rows.Next() {
			var d domainRow
			if err := rows.Scan(&d.ID, &d.Name, &d.Plan, &d.Status); err != nil {
				continue
			}
			domainRows = append(domainRows, d)
		}
		rows.Close()
	}

	filename := fmt.Sprintf("domains-%s.csv", time.Now().UTC().Format("2006-01-02"))
	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	var b strings.Builder
	b.WriteString("domain,status,plan,mailbox_count\n")
	for _, d := range domainRows {
		var mbCount int64
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = ? AND deleted_at IS NULL", d.ID).Scan(&mbCount)
		b.WriteString(csvField(d.Name))
		b.WriteByte(',')
		b.WriteString(csvField(d.Status))
		b.WriteByte(',')
		b.WriteString(csvField(d.Plan))
		b.WriteByte(',')
		b.WriteString(fmt.Sprintf("%d", mbCount))
		b.WriteByte('\n')
	}
	return c.SendString(b.String())
}

// csvField quotes a value if it contains characters that would break CSV
// parsing (comma, double-quote, CR, LF) and escapes embedded quotes.
func csvField(v string) string {
	needsQuote := false
	for _, r := range v {
		if r == ',' || r == '"' || r == '\r' || r == '\n' {
			needsQuote = true
			break
		}
	}
	if !needsQuote {
		return v
	}
	return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
}

func (h *Handler) auditTimeline(c fiber.Ctx, targetFilters []string) error {
	if h.auditStore == nil {
		return c.JSON([]struct{}{})
	}

	// Get all entries and filter in memory for OR logic
	allEntries, _, err := h.auditStore.Search(c.Context(), &audit.Query{
		Limit: 500, // Get more to ensure we have enough after filtering
	})
	if err != nil {
		return c.JSON([]struct{}{})
	}

	type timelineEntry struct {
		Action    string `json:"action"`
		Actor     string `json:"actor"`
		Target    string `json:"target"`
		Result    string `json:"result"`
		Timestamp string `json:"timestamp"`
	}

	var result []timelineEntry
	for _, e := range allEntries {
		matched := false
		for _, filter := range targetFilters {
			if strings.Contains(e.Target, filter) {
				matched = true
				break
			}
		}
		if matched {
			result = append(result, timelineEntry{
				Action:    e.Action,
				Actor:     e.Actor,
				Target:    e.Target,
				Result:    e.Result,
				Timestamp: e.Timestamp.Format(time.RFC3339),
			})
			if len(result) >= 100 {
				break
			}
		}
	}

	if result == nil {
		result = []timelineEntry{}
	}
	return c.JSON(result)
}

// GetMailboxAudit returns audit entries related to a mailbox.
func (h *Handler) GetMailboxAudit(c fiber.Ctx) error {
	idStr := c.Params("id")
	if idStr == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "mailbox id required"})
	}
	id, parseErr := parseUint(idStr)
	if parseErr != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}

	// Get mailbox email for filtering create events
	sqlDB, err := h.db.DB()
	if err != nil {
		return c.JSON([]struct{}{})
	}
	var email string
	err = sqlDB.QueryRow("SELECT email FROM coremail_mailboxes WHERE id = ? AND deleted_at IS NULL", id).Scan(&email)
	if err != nil {
		return c.JSON([]struct{}{})
	}

	return h.auditTimeline(c, []string{fmt.Sprintf("mailbox_id:%d", id), fmt.Sprintf("mailbox:%s", email)})
}

// GetDomainAudit returns audit entries related to a domain.
func (h *Handler) GetDomainAudit(c fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}
	return h.auditTimeline(c, []string{fmt.Sprintf("domain:%s", name)})
}

// ListFeatureFlags returns all feature flags.
func (h *Handler) ListFeatureFlags(c fiber.Ctx) error {
	var flags []models.FeatureFlag
	h.db.Find(&flags)
	return c.JSON(flags)
}

// UpdateFeatureFlag updates a feature flag.
func (h *Handler) UpdateFeatureFlag(c fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	h.db.Model(&models.FeatureFlag{}).Where("id = ?", id).Update("enabled", req.Enabled)
	h.writeAuditLog(c, "feature_flag.update", fmt.Sprintf("flag_id:%s|enabled:%v", id, req.Enabled))
	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) writeAuditLog(c fiber.Ctx, action, resource string) {
	userID, _ := c.Locals("user_id").(uint)
	ip := c.IP()

	if h.auditStore == nil {
		h.logger.Error("audit store unavailable")
		return
	}
	if err := h.auditStore.Record(c.Context(), &audit.Entry{
		Actor:     fmt.Sprintf("user:%d", userID),
		Action:    action,
		Target:    resource,
		Result:    "success",
		IP:        ip,
		UserAgent: c.Get("User-Agent"),
	}); err != nil {
		h.logger.Error("failed to write audit log", zap.Error(err))
	}
}

// parseUint parses a uint from a string for path param id values.
func parseUint(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number: %s", s)
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

// ----------------------------------------------------------------------
// Admin Runtime Telemetry (ADMIN-RUNTIME-TELEMETRY-2B)
//
// The admin dashboard needs honest read-only telemetry to stop
// showing fake "Online" / "Not available" values when the server
// has real data. The endpoint is admin-protected, GET-only, and
// carries no secrets: no env, no private keys, no tokens, no
// cookies, no full config dump. All values are safe primitives
// or short safe labels. See internal/runtime for the security
// contract.
// ----------------------------------------------------------------------

// GetAdminRuntime serves GET /api/v1/admin/runtime.
//
// Response shape is documented in docs/admin-runtime-telemetry.md
// (deferred to a later commit) and exercised by
// internal/api/handlers/admin_runtime_test.go. The handler is
// admin-only by virtue of being mounted on the admin group in
// router.go (RequireAnyRole(RoleAdmin, RoleSuperAdmin)). It is
// safe to call repeatedly; it does not mutate any state.
func (h *Handler) GetAdminRuntime(c fiber.Ctx) error {
	wm := config.GetWatermark()
	// Guard nil config so the endpoint never panics when the
	// operator starts without a config. All ports default to 0,
	// which the runtime package treats as "unknown".
	var smtpPort, imapPort, pop3Port, jmapPort int
	if h.cfg != nil {
		smtpPort = h.cfg.CoreMail.SMTPPort
		imapPort = h.cfg.CoreMail.IMAPPort
		pop3Port = h.cfg.CoreMail.POP3Port
		jmapPort = h.cfg.CoreMail.JMAPPort
	}
	// Listener snapshot may be nil when the router has not
	// wired the registry (tests, older builds).
	var listenerSnapshot map[runtime.ListenerKind]runtime.ListenerStatus
	if h.listenerRegistry != nil {
		listenerSnapshot = h.listenerRegistry.Snapshot()
	}
	tel := runtime.NewTelemetry(runtime.Inputs{
		Version:   wm.Version,
		Commit:    config.GetBuildCommit(),
		BuildTime: wm.BuildTime,
		GoVersion: wm.GoVersion,
		Arch:      wm.Arch,
		StartedAt: h.processStartedAt,
		DataPath:  h.dataPathForTelemetry(),
		DBPing:    h.dbPingErrorForTelemetry,
		QueueCounts: h.queueCountsForTelemetry(c.Context()),
		License:   h.licensePostureForTelemetry(c.Context()),
		ListenerSnapshot: listenerSnapshot,
		SMHTTPPort:  smtpPort,
		IMAPPort:    imapPort,
		POP3Port:    pop3Port,
		JMAPPort:    jmapPort,
	})
	return c.JSON(tel)
}

// dataPathForTelemetry returns the data path used to stat the disk.
// We prefer CoreMail.MailStorePath because that is where the
// runtime writes user data; falling back to the data_path
// config, then empty (the runtime layer maps that to "Not
// reported" via the disk label).
func (h *Handler) dataPathForTelemetry() string {
	if h.cfg == nil {
		return ""
	}
	if h.cfg.CoreMail.MailStorePath != "" {
		return h.cfg.CoreMail.MailStorePath
	}
	if h.cfg.CoreMail.DataPath != "" {
		return h.cfg.CoreMail.DataPath
	}
	if h.cfg.Database.SQLitePath != "" {
		return h.cfg.Database.SQLitePath
	}
	return ""
}

// queueCountsForTelemetry returns a safe snapshot of the queue
// counts. Pending / deferred / bounced come from the same
// coremail_queue table the dashboard already trusts; delivered
// is reported as 0 because the existing queue schema does not
// track lifetime delivered (out of scope for this endpoint).
func (h *Handler) queueCountsForTelemetry(ctx context.Context) runtime.QueueCounts {
	qc := runtime.QueueCounts{}
	if h.db == nil {
		return qc
	}
	sqlDB, err := h.db.DB()
	if err != nil {
		return qc
	}
	// Pending + leased (counted as "still trying").
	row := sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status IN ('pending','leased')`)
	if n, scanErr := scanInt64(row); scanErr == nil {
		qc.Pending = n
	}
	// Deferred (will retry later).
	row = sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'deferred'`)
	if n, scanErr := scanInt64(row); scanErr == nil {
		qc.Deferred = n
	}
	// Bounced / dead-letter (permanent failure).
	row = sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'dead_letter'`)
	if n, scanErr := scanInt64(row); scanErr == nil {
		qc.Bounced = n
	}
	// Delivered (lifetime). The current schema does not have a
	// permanent delivered row, so we report 0 and let the
	// dashboard render it as "Not reported" rather than fabricate
	// a number.
	return qc
}

// scanInt64 is a tiny helper that runs row.Scan(&n) and returns
// the result, swallowing the sql.ErrNoRows case (treated as 0).
func scanInt64(row interface{ Scan(...interface{}) error }) (int64, error) {
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// licensePostureForTelemetry assembles the safe license surface
// for the runtime response. The function never returns private
// key material, key hash, or any secret.
//
// Truth model:
//
//   - public_key_state reflects the public key file on disk:
//     "missing" (no file), "invalid" (exists but not valid PEM),
//     "loaded" (valid PEM public key parsed)
//
//   - validation_state reflects whether real cryptographic license
//     validation has succeeded. Without a durable validation result
//     source (a persisted validated-license marker or an integrated
//     Validator.Validate call), this remains "offline". An active
//     license DB row alone does NOT prove validation — see blocker
//     LICENSE-POSTURE-2D-TRUTHFUL-VALIDATION.
//
//   - mode is "offline" unless real validation proves otherwise.
//     A loaded public key alone does not imply "online" mode —
//     the key enables verification but does not prove a license is
//     currently valid.
func (h *Handler) licensePostureForTelemetry(ctx context.Context) runtime.LicensePosture {
	lp := runtime.LicensePosture{
		Mode:            "offline",
		Status:          "offline",
		PublicKeyState:  "missing",
		ValidationState: "offline",
	}

	if h.cfg != nil {
		pkPath := h.cfg.License.PublicKeyPath
		if pkPath == "" {
			pkPath = "/etc/orvix/license_public.pem"
		}
		pkState := validatePublicKeyFile(pkPath)
		lp.PublicKeyState = pkState
		lp.PublicKeyLoaded = (pkState == "loaded")

		// Mode is "offline" regardless of public key state.
		// True "online" mode requires a real validation result
		// that proves the license is currently valid. No such
		// durable validation source exists in this build, so
		// we never report "online".
		// OfflineMode config forces offline regardless.
		if h.cfg.License.OfflineMode {
			lp.Mode = "offline"
		} else if pkState == "missing" {
			lp.Mode = "missing"
		}
		// Otherwise mode stays "offline" — the default above.
	}

	// Tier + expiry from the most recent active license row,
	// if one exists. The model is decrypted on AfterFind, so
	// KeyHash is the decrypted form here; we MUST NOT echo it
	// in the runtime response. We only read Tier / ExpiresAt.
	//
	// An active DB row does NOT set validation_state to "valid".
	// Real validation requires cryptographic signature verification
	// which is not yet wired into this endpoint (a future phase
	// may integrate Validator.Validate and persist the result).
	if h.db != nil {
		var lic models.License
		if err := h.db.WithContext(ctx).Where("active = ?", true).Order("id DESC").First(&lic).Error; err == nil {
			lp.Tier = lic.Tier
			if !lic.ExpiresAt.IsZero() {
				lp.ExpiresAt = lic.ExpiresAt.UTC().Format(time.RFC3339)
			}
		}
	}

	// validation_state stays "offline" by default. "valid" would
	// require a real cryptographic validation result. Since no
	// durable validation result is persisted in this build, we
	// never set validation_state="valid". A future phase should
	// call license.Validator.Validate and store the outcome.

	switch {
	case lp.PublicKeyLoaded:
		lp.Status = "ok"
	case lp.Mode == "offline":
		lp.Status = "offline"
	default:
		lp.Status = "missing"
	}
	return lp
}

// validatePublicKeyFile checks the path and returns a classification
// string: "loaded" when the file is a valid PEM public key, "invalid"
// when the file exists but is not valid, or "missing" when the path
// is empty or no file exists.
func validatePublicKeyFile(path string) string {
	if path == "" {
		return "missing"
	}
	// 1. Stat the path — must exist and be a regular file.
	fi, err := os.Stat(path)
	if err != nil {
		return "missing"
	}
	if fi.IsDir() || !fi.Mode().IsRegular() {
		return "invalid"
	}
	// 2. Reject empty and oversized files before reading.
	if fi.Size() <= 0 || fi.Size() > 16*1024 {
		return "invalid"
	}
	// 3. Open and read the bounded content.
	f, err := os.Open(path)
	if err != nil {
		return "invalid"
	}
	defer f.Close()
	buf := make([]byte, 16384)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "invalid"
	}
	if n == 0 {
		return "invalid"
	}
	buf = buf[:n]
	// 3. Decode the PEM block.
	block, _ := pem.Decode(buf)
	if block == nil {
		return "invalid"
	}
	// 4. Parse the DER bytes as a public key.
	switch block.Type {
	case "PUBLIC KEY":
		_, err = x509.ParsePKIXPublicKey(block.Bytes)
	case "RSA PUBLIC KEY":
		_, err = x509.ParsePKCS1PublicKey(block.Bytes)
	default:
		return "invalid"
	}
	if err != nil {
		return "invalid"
	}
	return "loaded"
}
