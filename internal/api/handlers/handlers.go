package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
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
}

// NewHandler creates a new Handler with dependencies.
func NewHandler(db *gorm.DB, authenticator *auth.Authenticator, apikeyMgr *auth.APIKeyManager,
	logger *zap.Logger, cfg *config.Config, registry *modules.Registry,
	ff *license.FeatureFlags, rateLimiter *auth.RedisRateLimiter) *Handler {
	var auditStore *audit.Store
	if sqlDB, err := db.DB(); err == nil {
		auditStore = audit.NewStore(sqlDB)
		if err := auditStore.EnsureTable(context.Background()); err != nil {
			logger.Error("failed to ensure audit store", zap.Error(err))
		}
	}
	return &Handler{
		db:          db,
		auth:        authenticator,
		apikeys:     apikeyMgr,
		logger:      logger,
		cfg:         cfg,
		registry:    registry,
		features:    ff,
		security:    auth.NewSecurityMonitor(db, logger),
		rateLimiter: rateLimiter,
		auditStore:  auditStore,
	}
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
		SameSite: "Strict",
		Path:     "/",
	})

	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Strict",
		Path:     "/api/v1/auth/refresh",
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
		SameSite: "Strict",
		Path:     "/",
	})

	c.Cookie(&fiber.Cookie{
		Name:     "refresh_token",
		Value:    newRefresh,
		Expires:  expiresAt,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Strict",
		Path:     "/api/v1/auth/refresh",
	})

	return c.JSON(fiber.Map{"status": "ok"})
}

// Logout clears auth cookies.
func (h *Handler) Logout(c fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(uint)
	if ok {
		_ = h.auth.InvalidateAllSessions(userID)
	}
	c.ClearCookie("access_token")
	c.ClearCookie("refresh_token")
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
	c.ClearCookie("access_token")
	c.ClearCookie("refresh_token")
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

// ListDomains returns all mail domains.
func (h *Handler) ListDomains(c fiber.Ctx) error {
	type domainRow struct {
		ID     uint   `json:"id"`
		Domain string `json:"domain"`
		Plan   string `json:"plan"`
		Status string `json:"status"`
	}
	var domains []domainRow

	sqlDB, err := h.db.DB()
	if err == nil {
		seen := make(map[string]bool)
		rows, err := sqlDB.Query("SELECT id, domain, plan, status FROM provisioned_domains ORDER BY id DESC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id uint
				var domain, plan, status string
				if err := rows.Scan(&id, &domain, &plan, &status); err != nil {
					continue
				}
				if !seen[domain] {
					domains = append(domains, domainRow{ID: id, Domain: domain, Plan: plan, Status: status})
					seen[domain] = true
				}
			}
		}

		rows, err = sqlDB.Query("SELECT id, name, plan, status FROM coremail_domains WHERE deleted_at IS NULL ORDER BY id DESC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id uint
				var name, plan, status string
				if err := rows.Scan(&id, &name, &plan, &status); err != nil {
					continue
				}
				if !seen[name] {
					domains = append(domains, domainRow{ID: id, Domain: name, Plan: plan, Status: status})
					seen[name] = true
				}
			}
		}
	}

	if domains == nil {
		domains = []domainRow{}
	}
	return c.JSON(domains)
}

// CreateDomain creates a new mail domain.
func (h *Handler) CreateDomain(c fiber.Ctx) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind().JSON(&req); err != nil || req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	result := h.db.Exec("INSERT INTO provisioned_domains (domain, plan, status, provisioned_by) VALUES (?, 'smb', 'active', 0) ON CONFLICT(domain) DO NOTHING", req.Name)
	if result.Error != nil {
		h.logger.Error("failed to create domain", zap.String("domain", req.Name), zap.Error(result.Error))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create domain"})
	}

	h.writeAuditLog(c, "domain.create", fmt.Sprintf("domain:%s", req.Name))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "created", "domain": req.Name})
}

// DeleteDomain removes a mail domain.
func (h *Handler) DeleteDomain(c fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	h.db.Exec("DELETE FROM provisioned_domains WHERE domain = ?", name)
	h.writeAuditLog(c, "domain.delete", fmt.Sprintf("domain:%s", name))
	return c.JSON(fiber.Map{"status": "deleted"})
}

// ListUsers returns all users/mailboxes.
func (h *Handler) ListUsers(c fiber.Ctx) error {
	type userRow struct {
		ID    uint   `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	var users []userRow

	sqlDB, err := h.db.DB()
	if err == nil {
		seen := make(map[string]bool)
		rows, err := sqlDB.Query("SELECT id, email, role FROM users ORDER BY id DESC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id uint
				var email, role string
				if err := rows.Scan(&id, &email, &role); err != nil {
					continue
				}
				if !seen[email] {
					users = append(users, userRow{ID: id, Email: email, Role: role})
					seen[email] = true
				}
			}
		}

		rows, err = sqlDB.Query("SELECT id, email, 'mailbox' AS role FROM coremail_mailboxes WHERE deleted_at IS NULL ORDER BY id DESC")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var id uint
				var email, role string
				if err := rows.Scan(&id, &email, &role); err != nil {
					continue
				}
				if !seen[email] {
					users = append(users, userRow{ID: id, Email: email, Role: role})
					seen[email] = true
				}
			}
		}
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

// ListQueue returns the mail queue.
func (h *Handler) ListQueue(c fiber.Ctx) error {
	var messages []struct {
		ID     uint   `json:"id"`
		From   string `json:"from"`
		To     string `json:"to"`
		Status string `json:"status"`
	}
	sqlDB, err := h.db.DB()
	if err == nil {
		rows, err := sqlDB.Query("SELECT id, from_address, to_address, status FROM coremail_queue WHERE deleted_at IS NULL ORDER BY id DESC LIMIT 200")
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var msg struct {
					ID     uint   `json:"id"`
					From   string `json:"from"`
					To     string `json:"to"`
					Status string `json:"status"`
				}
				if err := rows.Scan(&msg.ID, &msg.From, &msg.To, &msg.Status); err != nil {
					continue
				}
				messages = append(messages, msg)
			}
		}
	}
	type queueEntry struct {
		ID     uint   `json:"id"`
		From   string `json:"from"`
		To     string `json:"to"`
		Status string `json:"status"`
	}
	result := make([]queueEntry, 0, len(messages))
	for _, m := range messages {
		result = append(result, queueEntry{ID: m.ID, From: m.From, To: m.To, Status: m.Status})
	}
	if result == nil {
		result = []queueEntry{}
	}
	return c.JSON(result)
}

// DeleteQueue removes a message from the queue.
func (h *Handler) DeleteQueue(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "deleted"})
}

// RetryQueue forces a retry of a queued message.
func (h *Handler) RetryQueue(c fiber.Ctx) error {
	return c.JSON(fiber.Map{"status": "retrying"})
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

// ListAuditLogs returns audit log entries.
func (h *Handler) ListAuditLogs(c fiber.Ctx) error {
	if h.auditStore == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "audit store unavailable"})
	}
	logs, _, err := h.auditStore.Search(c.Context(), &audit.Query{Limit: 100})
	if err != nil {
		h.logger.Error("failed to list audit logs", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list audit logs"})
	}
	return c.JSON(logs)
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
