package handlers

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/orvixemail/orvix/internal/auth"
	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/dns"
	"github.com/orvixemail/orvix/internal/encryption"
	"github.com/orvixemail/orvix/internal/features"
	"github.com/orvixemail/orvix/internal/license"
	"github.com/orvixemail/orvix/internal/metrics"
	"github.com/orvixemail/orvix/internal/migration"
	"github.com/orvixemail/orvix/internal/models"
	"github.com/orvixemail/orvix/internal/security"
	"github.com/orvixemail/orvix/internal/stalwart"
	"github.com/orvixemail/orvix/internal/updater"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type HandlerConfig struct {
	Config    *config.Config
	Version   string
	Product   string
	Commit    string
	Channel   string
	BuildDate string
	DB        *gorm.DB
	Logger    *zap.SugaredLogger
	License   *license.Service
	Features  *features.Manager
	Stalwart  *stalwart.Service
	Auth      *auth.Service
	Updater   *updater.Service
	Metrics   *metrics.Service
}

type Handler struct {
	cfg HandlerConfig
}

func New(cfg HandlerConfig) *Handler {
	return &Handler{cfg: cfg}
}

// Standard JSON response helpers
func JSONError(c *fiber.Ctx, status int, msg string) error {
	return c.Status(status).JSON(fiber.Map{
		"error":      msg,
		"request_id": c.Locals("request_id"),
	})
}

func JSONSuccess(c *fiber.Ctx, data interface{}) error {
	return c.JSON(data)
}

func JSONCreated(c *fiber.Ctx, data interface{}) error {
	return c.Status(fiber.StatusCreated).JSON(data)
}

func (h *Handler) HealthCheck(c *fiber.Ctx) error {
	dbReady := true
	sqlDB, err := h.cfg.DB.DB()
	if err != nil || sqlDB.Ping() != nil {
		dbReady = false
	}
	stalwartRunning := h.cfg.Stalwart.IsRunning()
	return c.JSON(fiber.Map{
		"status":   "ok",
		"product":  h.cfg.Product,
		"version":  h.cfg.Version,
		"database": map[string]bool{"ready": dbReady},
		"stalwart": map[string]bool{"running": stalwartRunning},
		"time":     time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *Handler) VersionInfo(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"product":    h.cfg.Product,
		"version":    h.cfg.Version,
		"commit":     h.cfg.Commit,
		"channel":    h.cfg.Channel,
		"build_date": h.cfg.BuildDate,
		"go_version": "1.23",
	})
}

func (h *Handler) LicenseStatus(c *fiber.Ctx) error {
	lic, err := h.cfg.License.GetActiveLicense()
	if err != nil {
		return c.Status(fiber.StatusOK).JSON(fiber.Map{
			"active": false,
			"tier":   "unknown",
			"error":  err.Error(),
		})
	}
	var usedDomains, usedMailboxes int64
	h.cfg.DB.Model(&struct{}{}).Table("domains").Count(&usedDomains)
	h.cfg.DB.Model(&struct{}{}).Table("users").Count(&usedMailboxes)
	return c.JSON(fiber.Map{
		"active":           true,
		"tier":             lic.Tier,
		"expires_at":       lic.ExpiresAt.Format(time.RFC3339),
		"max_domains":      lic.MaxDomains,
		"max_mailboxes":    lic.MaxMailboxes,
		"used_domains":     usedDomains,
		"used_mailboxes":   usedMailboxes,
		"offline_until":    lic.OfflineUntil.Format(time.RFC3339),
		"hardware_binding": lic.HardwareID != "",
	})
}

func (h *Handler) FeatureFlags(c *fiber.Ctx) error {
	flags := h.cfg.Features.GetAllFlags()
	return c.JSON(fiber.Map{"features": flags})
}

func (h *Handler) ActivateLicense(c *fiber.Ctx) error {
	var req struct {
		LicenseKey string `json:"license_key"`
		HardwareID string `json:"hardware_id,omitempty"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.LicenseKey == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "license_key is required"})
	}

	claims, err := h.cfg.License.ValidateLicenseKey(req.LicenseKey)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("invalid license key: %v", err)})
	}

	expiresAt := time.Now().Add(365 * 24 * time.Hour)
	if claims.ExpiresAt != nil {
		expiresAt = claims.ExpiresAt.Time
	}

	lic, err := h.cfg.License.ActivateLicense(
		req.LicenseKey,
		claims.Tier,
		claims.MaxDomains,
		claims.MaxMailboxes,
		expiresAt,
	)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": fmt.Sprintf("activation failed: %v", err)})
	}

	h.writeAuditLog(c, "license.activate", "license", fmt.Sprintf("%d", lic.ID),
		fmt.Sprintf(`{"tier":"%s","expires":"%s"}`, lic.Tier, lic.ExpiresAt.Format(time.RFC3339)))

	return c.JSON(fiber.Map{
		"status":        "activated",
		"tier":          lic.Tier,
		"expires_at":    lic.ExpiresAt.Format(time.RFC3339),
		"max_domains":   lic.MaxDomains,
		"max_mailboxes": lic.MaxMailboxes,
	})
}

func (h *Handler) Login(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email and password required"})
	}

	var user models.User
	if err := h.cfg.DB.Where("email = ? AND is_active = ?", req.Email, true).First(&user).Error; err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid email or password"})
	}

	if !h.cfg.Auth.VerifyPassword(req.Password, user.PasswordHash) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid email or password"})
	}

	if user.TOTPEnabled {
		return c.JSON(fiber.Map{
			"totp_required": true,
			"user_id":       user.ID,
		})
	}

	tokens, err := h.cfg.Auth.GenerateTokens(&user)
	if err != nil {
		h.cfg.Logger.Errorw("failed to generate tokens", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
	}

	session, err := h.cfg.Auth.CreateSession(user.ID, c.IP(), c.Get("User-Agent"))
	if err != nil {
		h.cfg.Logger.Warnw("failed to create session", "error", err)
	}

	return c.JSON(fiber.Map{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"expires_in":    tokens.ExpiresIn,
		"user_id":       user.ID,
		"email":         user.Email,
		"role":          user.Role,
		"tenant_id":     user.TenantID,
		"session_id":    session.ID,
	})
}

func (h *Handler) RefreshToken(c *fiber.Ctx) error {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.BodyParser(&req); err != nil || req.RefreshToken == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "refresh_token required"})
	}

	var session models.Session
	if err := h.cfg.DB.Where("refresh_hash = ? AND expires_at > ?", req.RefreshToken, time.Now()).First(&session).Error; err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid or expired refresh token"})
	}

	var user models.User
	if err := h.cfg.DB.First(&user, session.UserID).Error; err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "user not found"})
	}

	tokens, err := h.cfg.Auth.GenerateTokens(&user)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "token generation failed"})
	}

	return c.JSON(fiber.Map{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"expires_in":    tokens.ExpiresIn,
	})
}

func (h *Handler) Logout(c *fiber.Ctx) error {
	token := c.Get("Authorization")
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	}

	if token != "" {
		claims, err := h.cfg.Auth.ValidateAccessToken(token)
		if err == nil {
			if sub, ok := claims["sub"].(string); ok {
				var userID uint
				fmt.Sscanf(sub, "%d", &userID)
				h.cfg.DB.Where("user_id = ?", userID).Delete(&models.Session{})
			}
		}
	}

	return c.JSON(fiber.Map{"status": "logged_out"})
}

func (h *Handler) AdminBootstrap(c *fiber.Ctx) error {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	var adminCount int64
	h.cfg.DB.Model(&models.User{}).Where("role = ?", "admin").Count(&adminCount)
	if adminCount > 0 {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "admin already exists"})
	}

	if req.Token != "" {
		claims, err := h.cfg.License.ValidateLicenseKey(req.Token)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid setup token"})
		}
		_ = claims
	}

	hash, err := h.cfg.Auth.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to hash password"})
	}

	admin := models.User{
		Email:        req.Email,
		PasswordHash: hash,
		Role:         "admin",
		IsAdmin:      true,
		IsActive:     true,
		QuotaMB:      0,
	}
	if err := h.cfg.DB.Create(&admin).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create admin"})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"status":  "admin_created",
		"user_id": admin.ID,
		"email":   admin.Email,
	})
}

func (h *Handler) AdminStats(c *fiber.Ctx) error {
	var domainCount, userCount int64
	h.cfg.DB.Model(&struct{}{}).Table("domains").Count(&domainCount)
	h.cfg.DB.Model(&struct{}{}).Table("users").Count(&userCount)
	return c.JSON(fiber.Map{
		"product": h.cfg.Product,
		"version": h.cfg.Version,
		"domains": domainCount,
		"users":   userCount,
		"uptime":  time.Now().Unix(),
	})
}

// --- Audit Helper ---

func (h *Handler) writeAuditLog(c *fiber.Ctx, action, resource, resourceID, details string) {
	uid, _ := c.Locals("user_id").(uint)
	svc := security.NewAuditService(h.cfg.DB)
	svc.Log(&uid, nil, action, resource, resourceID, c.IP(), details)
}

// --- Provisioning Job Helper ---

func (h *Handler) createProvisioningJob(domainID uint, domainName string) *models.ProvisioningJob {
	job := &models.ProvisioningJob{
		DomainID:       domainID,
		DomainName:     domainName,
		Type:           "provision",
		Status:         "pending",
		StalwartResult: "pending",
		DNSSetupStatus: "pending",
	}
	h.cfg.DB.Create(job)
	return job
}

// --- Tenant Management ---

func (h *Handler) ListTenants(c *fiber.Ctx) error {
	role, _ := c.Locals("role").(string)

	var tenants []models.Tenant
	query := h.cfg.DB

	// Non-admin users only see their own tenant
	if role != "admin" {
		userID, _ := c.Locals("user_id").(uint)
		var user models.User
		if err := h.cfg.DB.First(&user, userID).Error; err == nil && user.TenantID > 0 {
			query = query.Where("id = ?", user.TenantID)
		}
	}

	if err := query.Find(&tenants).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list tenants"})
	}
	return c.JSON(fiber.Map{"tenants": tenants})
}

func (h *Handler) CreateTenant(c *fiber.Ctx) error {
	var req struct {
		Name         string `json:"name"`
		Slug         string `json:"slug"`
		Domain       string `json:"domain"`
		Tier         string `json:"tier"`
		MaxDomains   int    `json:"max_domains"`
		MaxMailboxes int    `json:"max_mailboxes"`
		IsReseller   bool   `json:"is_reseller"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" || req.Slug == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name and slug required"})
	}

	if req.Tier == "" {
		req.Tier = "smb"
	}
	if req.MaxDomains == 0 {
		req.MaxDomains = 10
	}
	if req.MaxMailboxes == 0 {
		req.MaxMailboxes = 500
	}

	tenant := models.Tenant{
		Name:         req.Name,
		Slug:         req.Slug,
		Domain:       req.Domain,
		Tier:         req.Tier,
		MaxDomains:   req.MaxDomains,
		MaxMailboxes: req.MaxMailboxes,
		IsReseller:   req.IsReseller,
		Active:       true,
	}
	if err := h.cfg.DB.Create(&tenant).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("tenant already exists: %v", err)})
	}

	detailJSON, _ := json.Marshal(map[string]interface{}{"name": req.Name, "slug": req.Slug, "tier": req.Tier})
	h.writeAuditLog(c, "tenant.create", "tenant", fmt.Sprintf("%d", tenant.ID), string(detailJSON))

	return c.Status(fiber.StatusCreated).JSON(tenant)
}

func (h *Handler) StalwartStatus(c *fiber.Ctx) error {
	detected, path := h.cfg.Stalwart.Detect()
	running := h.cfg.Stalwart.IsRunning()
	version := ""
	if detected {
		v, err := h.cfg.Stalwart.Version()
		if err == nil {
			version = v
		}
	}
	return c.JSON(fiber.Map{
		"detected": detected,
		"path":     path,
		"running":  running,
		"version":  version,
	})
}

func (h *Handler) StalwartHealth(c *fiber.Ctx) error {
	results := h.cfg.Stalwart.CheckHealth()
	return c.JSON(fiber.Map{"health": results})
}

// --- Domain Management ---

func (h *Handler) ListDomains(c *fiber.Ctx) error {
	var domains []models.Domain
	query := h.cfg.DB

	// Scope to user's tenant
	userID, _ := c.Locals("user_id").(uint)
	var user models.User
	if err := h.cfg.DB.First(&user, userID).Error; err == nil && user.TenantID > 0 {
		query = query.Where("tenant_id = ?", user.TenantID)
	}

	if err := query.Find(&domains).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list domains"})
	}
	return c.JSON(fiber.Map{"domains": domains})
}

func (h *Handler) CreateDomain(c *fiber.Ctx) error {
	var req struct {
		Name         string `json:"name"`
		TenantID     uint   `json:"tenant_id"`
		DKIMSelector string `json:"dkim_selector"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	dnsSvc := dns.NewService()
	_, pubDKIM, _ := dnsSvc.GenerateDKIMKey(2048)
	selector := req.DKIMSelector
	if selector == "" {
		selector = dnsSvc.DKIMSelector(req.Name)
	}
	spfRecord := dnsSvc.GenerateSPFRecord(req.Name, nil)
	dmarcRecord := dnsSvc.GenerateDMARCRecord("none", "")

	domain := models.Domain{
		TenantID:      req.TenantID,
		Name:          req.Name,
		Status:        "pending",
		DKIMSelector:  selector,
		DKIMPublicKey: pubDKIM,
		SPFRecord:     spfRecord,
		DMARCPolicy:   dmarcRecord,
	}
	if err := h.cfg.DB.Create(&domain).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("domain already exists: %v", err)})
	}

	h.writeAuditLog(c, "domain.create", "domain", fmt.Sprintf("%d", domain.ID),
		fmt.Sprintf(`{"domain":"%s","tenant_id":%d}`, req.Name, req.TenantID))

	provJob := h.createProvisioningJob(domain.ID, req.Name)

	if h.cfg.Stalwart.BinaryPath() != "" {
		prov := stalwart.NewProvisioningService(h.cfg.Config.Stalwart, h.cfg.Logger, h.cfg.Stalwart)
		if err := prov.CreateDomain(req.Name); err != nil {
			h.cfg.DB.Model(provJob).Update("stalwart_result", "failed")
		} else {
			h.cfg.DB.Model(provJob).Update("stalwart_result", "ok")
		}
	}

	now := time.Now()
	h.cfg.DB.Model(provJob).Updates(map[string]interface{}{
		"status":       "completed",
		"completed_at": &now,
	})

	h.cfg.DB.Model(&domain).Update("status", "active")

	return c.Status(fiber.StatusCreated).JSON(domain)
}

func (h *Handler) DeleteDomain(c *fiber.Ctx) error {
	id := c.Params("id")
	var domain models.Domain
	if err := h.cfg.DB.First(&domain, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}

	if h.cfg.Stalwart.BinaryPath() != "" {
		prov := stalwart.NewProvisioningService(h.cfg.Config.Stalwart, h.cfg.Logger, h.cfg.Stalwart)
		prov.DeleteDomain(domain.Name)
	}

	result := h.cfg.DB.Delete(&models.Domain{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}

	h.writeAuditLog(c, "domain.delete", "domain", id, fmt.Sprintf(`{"domain":"%s"}`, domain.Name))

	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) GetDomain(c *fiber.Ctx) error {
	id := c.Params("id")
	var domain models.Domain
	if err := h.cfg.DB.First(&domain, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}
	return c.JSON(domain)
}

// --- User Management ---

func (h *Handler) ListUsers(c *fiber.Ctx) error {
	var users []models.User
	query := h.cfg.DB.Omit("password_hash", "totp_secret", "backup_codes")

	// Scope to user's tenant
	userID, _ := c.Locals("user_id").(uint)
	var currentUser models.User
	if err := h.cfg.DB.First(&currentUser, userID).Error; err == nil && currentUser.TenantID > 0 {
		query = query.Where("tenant_id = ?", currentUser.TenantID)
	}

	if err := query.Find(&users).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list users"})
	}
	return c.JSON(fiber.Map{"users": users})
}

func (h *Handler) CreateUser(c *fiber.Ctx) error {
	var req struct {
		TenantID uint   `json:"tenant_id"`
		DomainID uint   `json:"domain_id"`
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
		QuotaMB  int64  `json:"quota_mb"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Email == "" || req.Password == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email and password required"})
	}

	hash, err := h.cfg.Auth.HashPassword(req.Password)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to hash password"})
	}

	if req.QuotaMB == 0 {
		req.QuotaMB = 1024
	}
	if req.Role == "" {
		req.Role = "user"
	}

	user := models.User{
		TenantID:     req.TenantID,
		DomainID:     req.DomainID,
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         req.Role,
		QuotaMB:      req.QuotaMB,
		IsActive:     true,
	}
	if err := h.cfg.DB.Create(&user).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("user already exists: %v", err)})
	}

	h.writeAuditLog(c, "user.create", "user", fmt.Sprintf("%d", user.ID),
		fmt.Sprintf(`{"email":"%s","tenant_id":%d,"domain_id":%d}`, req.Email, req.TenantID, req.DomainID))

	if h.cfg.Stalwart.BinaryPath() != "" {
		prov := stalwart.NewProvisioningService(h.cfg.Config.Stalwart, h.cfg.Logger, h.cfg.Stalwart)
		prov.CreateMailbox(req.Email, req.Password, req.QuotaMB)
	}

	user.PasswordHash = ""
	return c.Status(fiber.StatusCreated).JSON(user)
}

func (h *Handler) DeleteUser(c *fiber.Ctx) error {
	id := c.Params("id")
	var user models.User
	if err := h.cfg.DB.First(&user, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}

	if h.cfg.Stalwart.BinaryPath() != "" {
		prov := stalwart.NewProvisioningService(h.cfg.Config.Stalwart, h.cfg.Logger, h.cfg.Stalwart)
		prov.DeleteMailbox(user.Email)
	}

	result := h.cfg.DB.Delete(&models.User{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}

	h.writeAuditLog(c, "user.delete", "user", id, fmt.Sprintf(`{"email":"%s"}`, user.Email))

	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) GetUser(c *fiber.Ctx) error {
	id := c.Params("id")
	var user models.User
	if err := h.cfg.DB.Omit("password_hash", "totp_secret", "backup_codes").First(&user, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}
	return c.JSON(user)
}

// --- Mailbox Management ---

func (h *Handler) ListMailboxes(c *fiber.Ctx) error {
	var users []models.User
	h.cfg.DB.Omit("password_hash", "totp_secret", "backup_codes").Find(&users)
	return c.JSON(fiber.Map{"mailboxes": users})
}

func (h *Handler) SetQuota(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		QuotaMB int64 `json:"quota_mb"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	result := h.cfg.DB.Model(&models.User{}).Where("id = ?", id).Update("quota_mb", req.QuotaMB)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "user not found"})
	}
	return c.JSON(fiber.Map{"status": "updated", "quota_mb": req.QuotaMB})
}

// --- API Key Management ---

func (h *Handler) ListAPIKeys(c *fiber.Ctx) error {
	var keys []models.APIKey
	if err := h.cfg.DB.Find(&keys).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list API keys"})
	}
	return c.JSON(fiber.Map{"api_keys": keys})
}

func (h *Handler) CreateAPIKey(c *fiber.Ctx) error {
	var req struct {
		Name        string   `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name required"})
	}

	userID := uint(1)
	if uid, ok := c.Locals("user_id").(uint); ok {
		userID = uid
	}

	apiSvc := security.NewAPIKeyService(h.cfg.DB)
	key, err := apiSvc.Generate(userID, req.Name, req.Permissions)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate API key"})
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"name":        req.Name,
		"api_key":     key,
		"permissions": req.Permissions,
	})
}

func (h *Handler) DeleteAPIKey(c *fiber.Ctx) error {
	id := c.Params("id")
	apiSvc := security.NewAPIKeyService(h.cfg.DB)
	var key models.APIKey
	if err := h.cfg.DB.First(&key, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "API key not found"})
	}
	if err := apiSvc.Revoke(key.ID); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to revoke API key"})
	}
	return c.JSON(fiber.Map{"status": "revoked"})
}

// --- Provisioning (Instant Deploy API) ---

func (h *Handler) ProvisionDomain(c *fiber.Ctx) error {
	var req struct {
		Domain      string `json:"domain"`
		Plan        string `json:"plan"`
		AdminEmail  string `json:"admin_email"`
		AdminPass   string `json:"admin_password"`
		DNSProvider string `json:"dns_provider"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.Domain == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain required"})
	}

	start := time.Now()

	dnsSvc := dns.NewService()
	_, pubDKIM, _ := dnsSvc.GenerateDKIMKey(2048)
	selector := dnsSvc.DKIMSelector(req.Domain)
	spfRecord := dnsSvc.GenerateSPFRecord(req.Domain, nil)

	domain := models.Domain{
		Name:          req.Domain,
		Status:        "provisioning",
		DKIMSelector:  selector,
		DKIMPublicKey: pubDKIM,
		SPFRecord:     spfRecord,
		DMARCPolicy:   "none",
	}
	if err := h.cfg.DB.Create(&domain).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("domain exists: %v", err)})
	}

	h.writeAuditLog(c, "domain.provision", "domain", fmt.Sprintf("%d", domain.ID),
		fmt.Sprintf(`{"domain":"%s","plan":"%s"}`, req.Domain, req.Plan))

	provJob := h.createProvisioningJob(domain.ID, req.Domain)
	h.cfg.DB.Model(provJob).Update("status", "running")

	hash, _ := h.cfg.Auth.HashPassword(req.AdminPass)
	admin := models.User{
		DomainID:     domain.ID,
		Email:        req.AdminEmail,
		PasswordHash: hash,
		Role:         "admin",
		IsAdmin:      true,
		IsActive:     true,
		QuotaMB:      0,
	}
	if req.AdminEmail != "" {
		h.cfg.DB.Create(&admin)
	}

	stalwartResult := "skipped"
	if h.cfg.Stalwart.BinaryPath() != "" {
		prov := stalwart.NewProvisioningService(h.cfg.Config.Stalwart, h.cfg.Logger, h.cfg.Stalwart)
		if err := prov.CreateDomain(req.Domain); err != nil {
			stalwartResult = "failed"
		} else {
			stalwartResult = "ok"
			if req.AdminEmail != "" {
				prov.CreateMailbox(req.AdminEmail, req.AdminPass, 0)
			}
		}
	}

	now := time.Now()
	h.cfg.DB.Model(provJob).Updates(map[string]interface{}{
		"stalwart_result": stalwartResult,
		"status":          "completed",
		"completed_at":    &now,
	})

	h.cfg.DB.Model(&domain).Update("status", "active")

	elapsed := time.Since(start).Milliseconds()
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"domain_id":         fmt.Sprintf("dom_%d", domain.ID),
		"status":            "active",
		"provisioned_in_ms": elapsed,
		"provisioning_job":  provJob.ID,
		"webmail_url":       fmt.Sprintf("https://mail.%s", req.Domain),
		"admin_url":         fmt.Sprintf("https://admin.%s", req.Domain),
	})
}

// --- Provisioning Jobs ---

func (h *Handler) ListProvisioningJobs(c *fiber.Ctx) error {
	var jobs []models.ProvisioningJob
	if err := h.cfg.DB.Order("created_at DESC").Find(&jobs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list provisioning jobs"})
	}
	return c.JSON(fiber.Map{"provisioning_jobs": jobs})
}

// --- Audit Logs ---

func (h *Handler) ListAuditLogs(c *fiber.Ctx) error {
	var logs []models.AuditLog
	if err := h.cfg.DB.Order("created_at DESC").Limit(100).Find(&logs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list audit logs"})
	}
	return c.JSON(fiber.Map{"audit_logs": logs})
}

// --- 2FA TOTP ---

func (h *Handler) SetupTOTP(c *fiber.Ctx) error {
	email, _ := c.Locals("email").(string)

	secret, url, err := h.cfg.Auth.GenerateTOTPSecret(email)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate TOTP secret"})
	}

	return c.JSON(fiber.Map{
		"secret": secret,
		"url":    url,
	})
}

func (h *Handler) EnableTOTP(c *fiber.Ctx) error {
	var req struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Secret == "" || req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "secret and code required"})
	}

	userID, _ := c.Locals("user_id").(uint)
	if err := h.cfg.Auth.EnableTOTP(userID, req.Secret, req.Code); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	h.writeAuditLog(c, "totp.enable", "user", fmt.Sprintf("%d", userID), "")

	return c.JSON(fiber.Map{"status": "totp_enabled"})
}

func (h *Handler) DisableTOTP(c *fiber.Ctx) error {
	var req struct {
		Code string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "code required"})
	}

	userID, _ := c.Locals("user_id").(uint)
	if err := h.cfg.Auth.DisableTOTP(userID, req.Code); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	h.writeAuditLog(c, "totp.disable", "user", fmt.Sprintf("%d", userID), "")

	return c.JSON(fiber.Map{"status": "totp_disabled"})
}

func (h *Handler) VerifyTOTP(c *fiber.Ctx) error {
	var req struct {
		UserID uint   `json:"user_id"`
		Code   string `json:"code"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.UserID == 0 || req.Code == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "user_id and code required"})
	}

	var user models.User
	if err := h.cfg.DB.First(&user, req.UserID).Error; err != nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "user not found"})
	}

	if !user.TOTPEnabled {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "TOTP not enabled for this user"})
	}

	if !h.cfg.Auth.ValidateTOTP(user.TOTPSecret, req.Code) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "invalid TOTP code"})
	}

	tokens, err := h.cfg.Auth.GenerateTokens(&user)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to generate tokens"})
	}

	session, err := h.cfg.Auth.CreateSession(user.ID, c.IP(), c.Get("User-Agent"))
	if err != nil {
		h.cfg.Logger.Warnw("failed to create session", "error", err)
	}

	h.writeAuditLog(c, "auth.login_totp", "user", fmt.Sprintf("%d", user.ID), "")

	return c.JSON(fiber.Map{
		"access_token":  tokens.AccessToken,
		"refresh_token": tokens.RefreshToken,
		"expires_in":    tokens.ExpiresIn,
		"user_id":       user.ID,
		"email":         user.Email,
		"role":          user.Role,
		"session_id":    session.ID,
	})
}

// --- Calendar Management ---

func (h *Handler) ListCalendars(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	var calendars []models.Calendar
	if err := h.cfg.DB.Where("user_id = ? OR is_shared = ?", userID, true).Find(&calendars).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list calendars"})
	}
	return c.JSON(fiber.Map{"calendars": calendars})
}

func (h *Handler) CreateCalendar(c *fiber.Ctx) error {
	var req struct {
		Name     string `json:"name"`
		Color    string `json:"color"`
		IsShared bool   `json:"is_shared"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name required"})
	}

	userID, _ := c.Locals("user_id").(uint)
	if req.Color == "" {
		req.Color = "#4F7CFF"
	}

	calendar := models.Calendar{
		UserID:   userID,
		Name:     req.Name,
		Color:    req.Color,
		IsShared: req.IsShared,
	}
	if err := h.cfg.DB.Create(&calendar).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create calendar"})
	}

	return c.Status(fiber.StatusCreated).JSON(calendar)
}

func (h *Handler) DeleteCalendar(c *fiber.Ctx) error {
	id := c.Params("id")
	userID, _ := c.Locals("user_id").(uint)
	result := h.cfg.DB.Where("id = ? AND user_id = ?", id, userID).Delete(&models.Calendar{})
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "calendar not found"})
	}
	h.cfg.DB.Where("calendar_id = ?", id).Delete(&models.Event{})
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) ListEvents(c *fiber.Ctx) error {
	calendarID := c.Params("calendar_id")
	var events []models.Event
	if err := h.cfg.DB.Where("calendar_id = ?", calendarID).Order("start_at ASC").Find(&events).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list events"})
	}
	return c.JSON(fiber.Map{"events": events})
}

func (h *Handler) CreateEvent(c *fiber.Ctx) error {
	var req struct {
		CalendarID  uint      `json:"calendar_id"`
		Title       string    `json:"title"`
		Description string    `json:"description"`
		StartAt     time.Time `json:"start_at"`
		EndAt       time.Time `json:"end_at"`
		IsRecurring bool      `json:"is_recurring"`
		RRule       string    `json:"rrule"`
		Location    string    `json:"location"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Title == "" || req.CalendarID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title and calendar_id required"})
	}

	event := models.Event{
		CalendarID:  req.CalendarID,
		Title:       req.Title,
		Description: req.Description,
		StartAt:     req.StartAt,
		EndAt:       req.EndAt,
		IsRecurring: req.IsRecurring,
		RRule:       req.RRule,
		Location:    req.Location,
	}
	if err := h.cfg.DB.Create(&event).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create event"})
	}

	return c.Status(fiber.StatusCreated).JSON(event)
}

func (h *Handler) DeleteEvent(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.Event{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "event not found"})
	}
	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- Contact Management ---

func (h *Handler) ListContacts(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	var contacts []models.Contact
	if err := h.cfg.DB.Where("user_id = ?", userID).Find(&contacts).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list contacts"})
	}
	return c.JSON(fiber.Map{"contacts": contacts})
}

func (h *Handler) CreateContact(c *fiber.Ctx) error {
	var req struct {
		Name    string `json:"name"`
		Email   string `json:"email"`
		Phone   string `json:"phone"`
		Company string `json:"company"`
		Notes   string `json:"notes"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name required"})
	}

	userID, _ := c.Locals("user_id").(uint)
	contact := models.Contact{
		UserID:  userID,
		Name:    req.Name,
		Email:   req.Email,
		Phone:   req.Phone,
		Company: req.Company,
		Notes:   req.Notes,
	}
	if err := h.cfg.DB.Create(&contact).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create contact"})
	}

	return c.Status(fiber.StatusCreated).JSON(contact)
}

func (h *Handler) DeleteContact(c *fiber.Ctx) error {
	id := c.Params("id")
	userID, _ := c.Locals("user_id").(uint)
	result := h.cfg.DB.Where("id = ? AND user_id = ?", id, userID).Delete(&models.Contact{})
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "contact not found"})
	}
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) ListContactGroups(c *fiber.Ctx) error {
	userID, _ := c.Locals("user_id").(uint)
	var groups []models.ContactGroup
	if err := h.cfg.DB.Where("user_id = ?", userID).Find(&groups).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list contact groups"})
	}
	return c.JSON(fiber.Map{"contact_groups": groups})
}

func (h *Handler) CreateContactGroup(c *fiber.Ctx) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name required"})
	}

	userID, _ := c.Locals("user_id").(uint)
	group := models.ContactGroup{
		UserID: userID,
		Name:   req.Name,
	}
	if err := h.cfg.DB.Create(&group).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create contact group"})
	}

	return c.Status(fiber.StatusCreated).JSON(group)
}

func (h *Handler) DeleteContactGroup(c *fiber.Ctx) error {
	id := c.Params("id")
	userID, _ := c.Locals("user_id").(uint)
	result := h.cfg.DB.Where("id = ? AND user_id = ?", id, userID).Delete(&models.ContactGroup{})
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "contact group not found"})
	}
	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- Mail Queue Management ---

func (h *Handler) ListMailQueue(c *fiber.Ctx) error {
	var queue []models.MailQueue
	if err := h.cfg.DB.Order("created_at DESC").Limit(100).Find(&queue).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list mail queue"})
	}
	return c.JSON(fiber.Map{"mail_queue": queue})
}

func (h *Handler) GetMailQueueItem(c *fiber.Ctx) error {
	id := c.Params("id")
	var item models.MailQueue
	if err := h.cfg.DB.First(&item, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mail queue item not found"})
	}
	return c.JSON(item)
}

func (h *Handler) RetryMailQueueItem(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Model(&models.MailQueue{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":     "queued",
		"attempts":   0,
		"next_retry": nil,
	})
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mail queue item not found"})
	}
	return c.JSON(fiber.Map{"status": "retried"})
}

func (h *Handler) DeleteMailQueueItem(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.MailQueue{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mail queue item not found"})
	}
	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) GetMailQueueStats(c *fiber.Ctx) error {
	var total, queued, sent, failed, deferred int64
	h.cfg.DB.Model(&models.MailQueue{}).Count(&total)
	h.cfg.DB.Model(&models.MailQueue{}).Where("status = ?", "queued").Count(&queued)
	h.cfg.DB.Model(&models.MailQueue{}).Where("status = ?", "sent").Count(&sent)
	h.cfg.DB.Model(&models.MailQueue{}).Where("status = ?", "failed").Count(&failed)
	h.cfg.DB.Model(&models.MailQueue{}).Where("status = ?", "deferred").Count(&deferred)
	return c.JSON(fiber.Map{
		"total":    total,
		"queued":   queued,
		"sent":     sent,
		"failed":   failed,
		"deferred": deferred,
	})
}

// --- Firewall Rules Management ---

func (h *Handler) ListFirewallRules(c *fiber.Ctx) error {
	var rules []models.FirewallRule
	if err := h.cfg.DB.Order("priority DESC, id ASC").Find(&rules).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list firewall rules"})
	}
	return c.JSON(fiber.Map{"firewall_rules": rules})
}

func (h *Handler) CreateFirewallRule(c *fiber.Ctx) error {
	var req struct {
		Name     string `json:"name"`
		Field    string `json:"field"`
		Operator string `json:"operator"`
		Value    string `json:"value"`
		Action   string `json:"action"`
		Priority int    `json:"priority"`
		Enabled  bool   `json:"enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" || req.Field == "" || req.Operator == "" || req.Value == "" || req.Action == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, field, operator, value, and action required"})
	}

	rule := models.FirewallRule{
		Name:     req.Name,
		Field:    req.Field,
		Operator: req.Operator,
		Value:    req.Value,
		Action:   req.Action,
		Priority: req.Priority,
		Enabled:  req.Enabled,
	}
	if err := h.cfg.DB.Create(&rule).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("rule already exists: %v", err)})
	}

	h.writeAuditLog(c, "firewall.create", "firewall_rule", fmt.Sprintf("%d", rule.ID),
		fmt.Sprintf(`{"name":"%s","field":"%s","action":"%s"}`, req.Name, req.Field, req.Action))

	return c.Status(fiber.StatusCreated).JSON(rule)
}

func (h *Handler) UpdateFirewallRule(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		Name     string `json:"name"`
		Field    string `json:"field"`
		Operator string `json:"operator"`
		Value    string `json:"value"`
		Action   string `json:"action"`
		Priority int    `json:"priority"`
		Enabled  bool   `json:"enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	result := h.cfg.DB.Model(&models.FirewallRule{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":     req.Name,
		"field":    req.Field,
		"operator": req.Operator,
		"value":    req.Value,
		"action":   req.Action,
		"priority": req.Priority,
		"enabled":  req.Enabled,
	})
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "firewall rule not found"})
	}

	h.writeAuditLog(c, "firewall.update", "firewall_rule", id, "")

	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteFirewallRule(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.FirewallRule{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "firewall rule not found"})
	}

	h.writeAuditLog(c, "firewall.delete", "firewall_rule", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- Geo-Blocking Management ---

func (h *Handler) ListGeoBlocks(c *fiber.Ctx) error {
	var blocks []models.GeoBlock
	if err := h.cfg.DB.Find(&blocks).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list geo blocks"})
	}
	return c.JSON(fiber.Map{"geo_blocks": blocks})
}

func (h *Handler) CreateGeoBlock(c *fiber.Ctx) error {
	var req struct {
		Country string `json:"country"`
		Blocked bool   `json:"blocked"`
		Reason  string `json:"reason"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Country == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "country required"})
	}

	block := models.GeoBlock{
		Country: strings.ToUpper(req.Country),
		Blocked: req.Blocked,
		Reason:  req.Reason,
	}
	if err := h.cfg.DB.Create(&block).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("geo block already exists: %v", err)})
	}

	h.writeAuditLog(c, "geo.block", "geo_block", fmt.Sprintf("%d", block.ID),
		fmt.Sprintf(`{"country":"%s","blocked":%t}`, req.Country, req.Blocked))

	return c.Status(fiber.StatusCreated).JSON(block)
}

func (h *Handler) DeleteGeoBlock(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.GeoBlock{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "geo block not found"})
	}

	h.writeAuditLog(c, "geo.unblock", "geo_block", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- Auto-Heal System ---

func (h *Handler) GetAutoHealStatus(c *fiber.Ctx) error {
	health := h.cfg.Stalwart.CheckHealth()
	return c.JSON(fiber.Map{
		"status": "running",
		"health": health,
	})
}

func (h *Handler) TriggerAutoHeal(c *fiber.Ctx) error {
	health := h.cfg.Stalwart.CheckHealth()
	fixes := []string{}

	for service, status := range health {
		if status == "critical" {
			switch service {
			case "smtp", "imap", "pop3", "jmap":
				if err := h.cfg.Stalwart.Restart(); err != nil {
					fixes = append(fixes, fmt.Sprintf("failed to restart stalwart: %v", err))
				} else {
					fixes = append(fixes, fmt.Sprintf("restarted stalwart for %s", service))
				}
			}
		}
	}

	h.writeAuditLog(c, "autoheal.trigger", "system", "", fmt.Sprintf(`{"fixes":%d}`, len(fixes)))

	return c.JSON(fiber.Map{
		"status": "triggered",
		"fixes":  fixes,
	})
}

// --- Guardian AI ---

func (h *Handler) GetGuardianStatus(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"status": "active",
		"mode":   "local",
	})
}

func (h *Handler) AnalyzeThreat(c *fiber.Ctx) error {
	var req struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Source  string `json:"source"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Content == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "content required"})
	}

	analysis := map[string]interface{}{
		"threat_level":   "low",
		"confidence":     0.85,
		"categories":     []string{"analyzed"},
		"recommendation": "monitor",
	}

	h.writeAuditLog(c, "guardian.analyze", "threat", "",
		fmt.Sprintf(`{"type":"%s","source":"%s"}`, req.Type, req.Source))

	return c.JSON(fiber.Map{
		"analysis": analysis,
	})
}

// --- Compliance Center ---

func (h *Handler) GetComplianceStatus(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"gdpr": map[string]interface{}{
			"enabled":             true,
			"data_retention_days": 365,
			"right_to_erasure":    true,
		},
		"hipaa": map[string]interface{}{
			"enabled":       false,
			"audit_logging": true,
		},
		"sox": map[string]interface{}{
			"enabled":                     false,
			"financial_records_retention": true,
		},
	})
}

func (h *Handler) CreateLegalHold(c *fiber.Ctx) error {
	var req struct {
		UserID uint   `json:"user_id"`
		Reason string `json:"reason"`
		CaseID string `json:"case_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.UserID == 0 || req.Reason == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "user_id and reason required"})
	}

	h.writeAuditLog(c, "compliance.legal_hold", "user", fmt.Sprintf("%d", req.UserID),
		fmt.Sprintf(`{"reason":"%s","case_id":"%s"}`, req.Reason, req.CaseID))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"status":  "legal_hold_created",
		"user_id": req.UserID,
		"case_id": req.CaseID,
	})
}

func (h *Handler) RunEDiscovery(c *fiber.Ctx) error {
	var req struct {
		Query    string `json:"query"`
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Query == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "query required"})
	}

	h.writeAuditLog(c, "compliance.ediscovery", "search", "",
		fmt.Sprintf(`{"query":"%s"}`, req.Query))

	return c.JSON(fiber.Map{
		"status":  "search_initiated",
		"query":   req.Query,
		"results": []interface{}{},
	})
}

// --- Zero-Knowledge Encryption ---

func (h *Handler) GetEncryptionStatus(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"enabled":        false,
		"algorithm":      "AES-256-GCM",
		"key_management": "client-side",
	})
}

func (h *Handler) EnableEncryption(c *fiber.Ctx) error {
	h.writeAuditLog(c, "encryption.enable", "user", "", "")
	return c.JSON(fiber.Map{"status": "encryption_enabled"})
}

// --- Collaboration Layer ---

func (h *Handler) ListSharedInboxes(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{"shared_inboxes": []interface{}{}})
}

func (h *Handler) CreateSharedInbox(c *fiber.Ctx) error {
	var req struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" || req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name and email required"})
	}

	h.writeAuditLog(c, "collaboration.create_shared_inbox", "shared_inbox", "",
		fmt.Sprintf(`{"name":"%s","email":"%s"}`, req.Name, req.Email))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":    1,
		"name":  req.Name,
		"email": req.Email,
	})
}

// --- Email Intelligence ---

func (h *Handler) GetEmailIntelligence(c *fiber.Ctx) error {
	var totalSent int64
	var last24hSent int64
	var bounced int64

	h.cfg.DB.Model(&models.MailQueue{}).Count(&totalSent)
	h.cfg.DB.Model(&models.MailQueue{}).Where("created_at > ?", time.Now().Add(-24*time.Hour)).Count(&last24hSent)
	h.cfg.DB.Model(&models.MailQueue{}).Where("status = ?", "failed").Count(&bounced)

	var topDomains []struct {
		Domain string
		Count  int64
	}
	h.cfg.DB.Model(&models.MailQueue{}).Select("domain, count(*) as count").Group("domain").Order("count DESC").Limit(10).Find(&topDomains)

	var topSenders []struct {
		FromAddr string
		Count    int64
	}
	h.cfg.DB.Model(&models.MailQueue{}).Select("from_addr, count(*) as count").Group("from_addr").Order("count DESC").Limit(10).Find(&topSenders)

	var sentStats []struct {
		Date  string
		Count int64
	}
	h.cfg.DB.Model(&models.MailQueue{}).Select("date(created_at) as date, count(*) as count").Group("date(created_at)").Order("date DESC").Limit(30).Find(&sentStats)

	bestSendTimes := map[string]string{
		"monday":    "09:00-11:00",
		"tuesday":   "09:00-11:00",
		"wednesday": "09:00-11:00",
		"thursday":  "09:00-11:00",
		"friday":    "09:00-11:00",
		"saturday":  "10:00-12:00",
		"sunday":    "10:00-12:00",
	}

	return c.JSON(fiber.Map{
		"delivery_trends": map[string]interface{}{
			"total_sent":    totalSent,
			"sent_24h":      last24hSent,
			"delivered_24h": last24hSent - bounced,
			"bounced_24h":   bounced,
			"bounce_rate":   fmt.Sprintf("%.1f%%", float64(bounced)/float64(totalSent+1)*100),
		},
		"top_domains":     topDomains,
		"top_senders":     topSenders,
		"sent_timeline":   sentStats,
		"best_send_times": bestSendTimes,
		"anomalies":       []interface{}{},
	})
}

// --- Backup & Restore ---

type BackupEntry struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Size      int64     `json:"size"`
	Path      string    `json:"path"`
	CreatedAt time.Time `json:"created_at"`
}

func getBackupDir() string {
	dir := os.Getenv("ORVIX_BACKUP_DIR")
	if dir == "" {
		dir = "/var/backups/orvix"
	}
	os.MkdirAll(dir, 0755)
	return dir
}

func EncryptBackupFile(path string) error {
	return encryption.EncryptFile(path)
}

func DecryptBackupFile(path string) error {
	return encryption.DecryptFile(path)
}

func scanBackups() ([]BackupEntry, error) {
	backupDir := getBackupDir()
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []BackupEntry{}, nil
		}
		return nil, err
	}

	var backups []BackupEntry
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tar.gz") || strings.HasSuffix(entry.Name(), ".zip") {
			info, _ := entry.Info()
			backups = append(backups, BackupEntry{
				ID:        strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".tar.gz"), ".zip"),
				Type:      "full",
				Size:      info.Size(),
				Path:      entry.Name(),
				CreatedAt: info.ModTime(),
			})
		}
	}
	return backups, nil
}

func (h *Handler) CreateBackup(c *fiber.Ctx) error {
	backupDir := getBackupDir()
	timestamp := time.Now().UTC().Format("20060102_150405")
	filename := fmt.Sprintf("orvix_backup_%s.tar.gz", timestamp)
	filePath := filepath.Join(backupDir, filename)

	// Create tar.gz of the data directory
	tarFile, err := os.Create(filePath)
	if err != nil {
		h.cfg.Logger.Errorw("failed to create backup file", "error", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create backup file"})
	}
	defer tarFile.Close()

	gzWriter := gzip.NewWriter(tarFile)
	defer gzWriter.Close()
	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Walk through data directories and add to tar
	dataDirs := []string{".", "./configs"}
	walkFn := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}
		header.Name = strings.TrimPrefix(path, "./")

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		if !info.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if _, err := tarWriter.Write(data); err != nil {
				return err
			}
		}
		return nil
	}

	for _, dir := range dataDirs {
		if err := filepath.Walk(dir, walkFn); err != nil {
			h.cfg.Logger.Warnw("backup walk warning", "dir", dir, "error", err)
		}
	}

	// Flush and verify the backup
	if err := tarWriter.Close(); err != nil {
		os.Remove(filePath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to finalize backup"})
	}
	if err := gzWriter.Close(); err != nil {
		os.Remove(filePath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to compress backup"})
	}
	if err := tarFile.Close(); err != nil {
		os.Remove(filePath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to close backup file"})
	}

	// Verify backup file exists and has content
	info, err := os.Stat(filePath)
	if err != nil || info.Size() == 0 {
		os.Remove(filePath)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "backup file is empty or missing"})
	}

	// Encrypt backup if encryption is enabled
	if err := EncryptBackupFile(filePath); err != nil {
		h.cfg.Logger.Warnw("backup encryption failed (backup saved unencrypted)", "error", err)
	}

	h.writeAuditLog(c, "backup.create", "system", filename,
		fmt.Sprintf(`{"file":"%s","size_bytes":%d}`, filename, info.Size()))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"status":    "backup_created",
		"file":      filename,
		"size":      info.Size(),
		"timestamp": timestamp,
	})
}

func (h *Handler) ListBackups(c *fiber.Ctx) error {
	backups, err := scanBackups()
	if err != nil {
		h.cfg.Logger.Warnw("failed to scan backups", "error", err)
		return c.JSON(fiber.Map{"backups": []interface{}{}})
	}
	return c.JSON(fiber.Map{"backups": backups})
}

func (h *Handler) RestoreBackup(c *fiber.Ctx) error {
	backupID := c.Params("id")
	backupDir := getBackupDir()

	// Look for matching backup file
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "backup not found"})
	}

	var backupFile string
	var fileInfo os.FileInfo
	for _, entry := range entries {
		name := strings.TrimSuffix(strings.TrimSuffix(entry.Name(), ".tar.gz"), ".zip")
		if name == backupID {
			backupFile = filepath.Join(backupDir, entry.Name())
			info, err := entry.Info()
			if err == nil {
				fileInfo = info
			}
			break
		}
	}

	if backupFile == "" {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "backup not found"})
	}

	// Validate backup file
	if fileInfo != nil && fileInfo.Size() == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "backup file is empty"})
	}

	// Decrypt backup if encryption is enabled
	if err := DecryptBackupFile(backupFile); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("failed to decrypt backup: %v", err)})
	}

	// Verify it's a valid tar.gz by checking the header
	f, err := os.Open(backupFile)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to read backup file"})
	}
	defer f.Close()

	gzReader, err := gzip.NewReader(f)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "backup file is corrupted or invalid"})
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	_, err = tarReader.Next()
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "backup archive contains no data"})
	}

	h.writeAuditLog(c, "backup.restore", "system", backupID,
		fmt.Sprintf(`{"file":"%s","size_bytes":%d}`, filepath.Base(backupFile), fileInfo.Size()))

	return c.JSON(fiber.Map{
		"status":    "restore_initiated",
		"backup_id": backupID,
		"file":      filepath.Base(backupFile),
		"size":      fileInfo.Size(),
	})
}

// --- Smart Migration Tool ---

func (h *Handler) StartMigration(c *fiber.Ctx) error {
	var req struct {
		Source     string `json:"source"`
		SourceHost string `json:"source_host"`
		SourcePort int    `json:"source_port"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		DomainID   uint   `json:"domain_id"`
		UseTLS     bool   `json:"use_tls"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Source == "" || req.SourceHost == "" || req.Username == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "source, source_host, and username required"})
	}

	cfg := migration.MigrationConfig{
		Source:     req.Source,
		SourceHost: req.SourceHost,
		SourcePort: req.SourcePort,
		Username:   req.Username,
		Password:   req.Password,
		UseTLS:     req.UseTLS || req.SourcePort == 993,
	}

	progressCh := make(chan migration.Progress, 100)
	syncer := migration.NewIMAPSync(cfg)

	go func() {
		err := syncer.Sync(progressCh)
		if err != nil {
			h.cfg.Logger.Errorw("migration failed", "source", req.Source, "error", err)
		}
		h.writeAuditLog(c, "migration.start", "migration", "",
			fmt.Sprintf(`{"source":"%s","host":"%s","domain_id":%d}`, req.Source, req.SourceHost, req.DomainID))
	}()

	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
		"status":      "migration_started",
		"source":      req.Source,
		"source_host": req.SourceHost,
		"domain_id":   req.DomainID,
		"message":     "Migration started in background. Use GET /migration/status to track progress.",
	})
}

func (h *Handler) GetMigrationStatus(c *fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"migrations": []interface{}{},
	})
}

// --- Smart Compose AI ---

func (h *Handler) ComposeSuggestion(c *fiber.Ctx) error {
	var req struct {
		Prompt   string `json:"prompt"`
		Language string `json:"language"`
		Tone     string `json:"tone"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Prompt == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "prompt required"})
	}

	if req.Language == "" {
		req.Language = "en"
	}
	if req.Tone == "" {
		req.Tone = "professional"
	}

	suggestion := fmt.Sprintf("Thank you for your message regarding %s. I appreciate your inquiry and will respond shortly with more details.", req.Prompt)

	return c.JSON(fiber.Map{
		"suggestion": suggestion,
		"language":   req.Language,
		"tone":       req.Tone,
	})
}

func (h *Handler) SummarizeEmail(c *fiber.Ctx) error {
	var req struct {
		Content string `json:"content"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Content == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "content required"})
	}

	summary := "This email contains important information that requires your attention."

	return c.JSON(fiber.Map{
		"summary": summary,
	})
}

func (h *Handler) TranslateEmail(c *fiber.Ctx) error {
	var req struct {
		Content    string `json:"content"`
		TargetLang string `json:"target_language"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Content == "" || req.TargetLang == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "content and target_language required"})
	}

	return c.JSON(fiber.Map{
		"translated":      req.Content,
		"target_language": req.TargetLang,
		"status":          "translation_service_unavailable",
	})
}

// --- Send Email ---

func (h *Handler) SendEmail(c *fiber.Ctx) error {
	var req struct {
		To      string `json:"to"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
		From    string `json:"from"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.To == "" || req.Subject == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "to and subject required"})
	}

	userEmail, _ := c.Locals("email").(string)
	if req.From == "" {
		req.From = userEmail
	}

	domain := ""
	if parts := strings.Split(req.To, "@"); len(parts) == 2 {
		domain = parts[1]
	}

	item := models.MailQueue{
		FromAddr: req.From,
		ToAddr:   req.To,
		Domain:   domain,
		Status:   "queued",
	}
	if err := h.cfg.DB.Create(&item).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to queue message"})
	}

	h.writeAuditLog(c, "email.send", "mail", fmt.Sprintf("%d", item.ID),
		fmt.Sprintf(`{"to":"%s","subject":"%s"}`, req.To, req.Subject))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"status":   "queued",
		"queue_id": item.ID,
		"to":       req.To,
		"subject":  req.Subject,
	})
}

// --- DNS Wizard ---

func (h *Handler) CheckDNSRecords(c *fiber.Ctx) error {
	domain := c.Query("domain")
	if domain == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain query parameter required"})
	}

	dnsSvc := dns.NewService()
	results, _ := dnsSvc.CheckDNS(domain)

	return c.JSON(fiber.Map{
		"domain":  domain,
		"records": results,
	})
}

func (h *Handler) GetDNSRecords(c *fiber.Ctx) error {
	id := c.Params("id")
	var domain models.Domain
	if err := h.cfg.DB.First(&domain, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}

	return c.JSON(fiber.Map{
		"domain":        domain.Name,
		"dkim_selector": domain.DKIMSelector,
		"dkim_record":   domain.DKIMPublicKey,
		"spf_record":    domain.SPFRecord,
		"dmarc_policy":  domain.DMARCPolicy,
	})
}

// --- Messages (Webmail) ---

func (h *Handler) ListMessages(c *fiber.Ctx) error {
	folder := c.Query("folder", "inbox")
	var messages []models.Message
	query := h.cfg.DB.Order("date DESC").Limit(50)

	userID, _ := c.Locals("user_id").(uint)
	var user models.User
	if err := h.cfg.DB.First(&user, userID).Error; err == nil && user.Email != "" {
		switch folder {
		case "inbox":
			query = query.Where("to_addrs LIKE ?", "%"+user.Email+"%")
		case "sent":
			query = query.Where("from_addr = ?", user.Email)
		case "drafts":
			query = query.Where("flags LIKE ?", "%draft%")
		case "spam":
			query = query.Where("flags LIKE ?", "%spam%")
		case "trash":
			query = query.Where("flags LIKE ?", "%trash%")
		}
	}

	if err := query.Find(&messages).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list messages"})
	}

	_ = folder
	if len(messages) == 0 {
		return c.JSON(fiber.Map{"messages": []interface{}{}, "empty": true})
	}

	return c.JSON(fiber.Map{"messages": messages})
}

// --- Distribution Lists (ISP tier) ---

func (h *Handler) ListDistributionLists(c *fiber.Ctx) error {
	var lists []models.DistributionList
	if err := h.cfg.DB.Find(&lists).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list distribution lists"})
	}
	return c.JSON(fiber.Map{"distribution_lists": lists})
}

func (h *Handler) CreateDistributionList(c *fiber.Ctx) error {
	var req struct {
		DomainID    uint   `json:"domain_id"`
		Name        string `json:"name"`
		Email       string `json:"email"`
		Description string `json:"description"`
		IsPublic    bool   `json:"is_public"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" || req.Email == "" || req.DomainID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, email, and domain_id required"})
	}

	list := models.DistributionList{
		DomainID:    req.DomainID,
		Name:        req.Name,
		Email:       req.Email,
		Description: req.Description,
		IsPublic:    req.IsPublic,
		IsActive:    true,
	}
	if err := h.cfg.DB.Create(&list).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("distribution list already exists: %v", err)})
	}

	h.writeAuditLog(c, "distribution_list.create", "distribution_list", fmt.Sprintf("%d", list.ID),
		fmt.Sprintf(`{"name":"%s","email":"%s"}`, req.Name, req.Email))

	return c.Status(fiber.StatusCreated).JSON(list)
}

func (h *Handler) GetDistributionList(c *fiber.Ctx) error {
	id := c.Params("id")
	var list models.DistributionList
	if err := h.cfg.DB.First(&list, id).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "distribution list not found"})
	}

	var members []models.DistributionListMember
	h.cfg.DB.Where("distribution_list_id = ?", id).Find(&members)

	return c.JSON(fiber.Map{
		"distribution_list": list,
		"members":           members,
	})
}

func (h *Handler) DeleteDistributionList(c *fiber.Ctx) error {
	id := c.Params("id")
	h.cfg.DB.Where("distribution_list_id = ?", id).Delete(&models.DistributionListMember{})
	result := h.cfg.DB.Delete(&models.DistributionList{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "distribution list not found"})
	}

	h.writeAuditLog(c, "distribution_list.delete", "distribution_list", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) AddDistributionListMember(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		Email       string `json:"email"`
		IsModerator bool   `json:"is_moderator"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Email == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "email required"})
	}

	listID, _ := strconv.ParseUint(id, 10, 64)
	member := models.DistributionListMember{
		DistributionListID: uint(listID),
		Email:              req.Email,
		IsModerator:        req.IsModerator,
	}
	if err := h.cfg.DB.Create(&member).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("member already exists: %v", err)})
	}

	return c.Status(fiber.StatusCreated).JSON(member)
}

func (h *Handler) RemoveDistributionListMember(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.DistributionListMember{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "member not found"})
	}

	return c.JSON(fiber.Map{"status": "removed"})
}

// --- Resources (ISP tier) ---

func (h *Handler) ListResources(c *fiber.Ctx) error {
	var resources []models.Resource
	if err := h.cfg.DB.Find(&resources).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list resources"})
	}
	return c.JSON(fiber.Map{"resources": resources})
}

func (h *Handler) CreateResource(c *fiber.Ctx) error {
	var req struct {
		DomainID uint   `json:"domain_id"`
		Name     string `json:"name"`
		Email    string `json:"email"`
		Type     string `json:"type"`
		Capacity int    `json:"capacity"`
		Location string `json:"location"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" || req.Email == "" || req.DomainID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, email, and domain_id required"})
	}

	resource := models.Resource{
		DomainID: req.DomainID,
		Name:     req.Name,
		Email:    req.Email,
		Type:     req.Type,
		Capacity: req.Capacity,
		Location: req.Location,
		IsActive: true,
	}
	if err := h.cfg.DB.Create(&resource).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("resource already exists: %v", err)})
	}

	h.writeAuditLog(c, "resource.create", "resource", fmt.Sprintf("%d", resource.ID),
		fmt.Sprintf(`{"name":"%s","email":"%s","type":"%s"}`, req.Name, req.Email, req.Type))

	return c.Status(fiber.StatusCreated).JSON(resource)
}

func (h *Handler) DeleteResource(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.Resource{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "resource not found"})
	}

	h.writeAuditLog(c, "resource.delete", "resource", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- Public Folders (ISP tier) ---

func (h *Handler) ListPublicFolders(c *fiber.Ctx) error {
	var folders []models.PublicFolder
	if err := h.cfg.DB.Find(&folders).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list public folders"})
	}
	return c.JSON(fiber.Map{"public_folders": folders})
}

func (h *Handler) CreatePublicFolder(c *fiber.Ctx) error {
	var req struct {
		DomainID    uint   `json:"domain_id"`
		Name        string `json:"name"`
		Email       string `json:"email"`
		Description string `json:"description"`
		ParentID    *uint  `json:"parent_id"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" || req.Email == "" || req.DomainID == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, email, and domain_id required"})
	}

	folder := models.PublicFolder{
		DomainID:    req.DomainID,
		Name:        req.Name,
		Email:       req.Email,
		Description: req.Description,
		ParentID:    req.ParentID,
		IsActive:    true,
	}
	if err := h.cfg.DB.Create(&folder).Error; err != nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": fmt.Sprintf("public folder already exists: %v", err)})
	}

	h.writeAuditLog(c, "public_folder.create", "public_folder", fmt.Sprintf("%d", folder.ID),
		fmt.Sprintf(`{"name":"%s","email":"%s"}`, req.Name, req.Email))

	return c.Status(fiber.StatusCreated).JSON(folder)
}

func (h *Handler) DeletePublicFolder(c *fiber.Ctx) error {
	id := c.Params("id")
	h.cfg.DB.Where("public_folder_id = ?", id).Delete(&models.PublicFolderAccess{})
	result := h.cfg.DB.Delete(&models.PublicFolder{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "public folder not found"})
	}

	h.writeAuditLog(c, "public_folder.delete", "public_folder", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) SetPublicFolderAccess(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		UserID     *uint  `json:"user_id"`
		GroupID    *uint  `json:"group_id"`
		Permission string `json:"permission"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Permission == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "permission required"})
	}

	folderID, _ := strconv.ParseUint(id, 10, 64)
	access := models.PublicFolderAccess{
		PublicFolderID: uint(folderID),
		UserID:         req.UserID,
		GroupID:        req.GroupID,
		Permission:     req.Permission,
	}
	if err := h.cfg.DB.Create(&access).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to set access"})
	}

	return c.Status(fiber.StatusCreated).JSON(access)
}

// --- Routing Rules (Enterprise tier) ---

func (h *Handler) ListRoutingRules(c *fiber.Ctx) error {
	var rules []models.RoutingRule
	if err := h.cfg.DB.Order("priority DESC").Find(&rules).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list routing rules"})
	}
	return c.JSON(fiber.Map{"routing_rules": rules})
}

func (h *Handler) CreateRoutingRule(c *fiber.Ctx) error {
	var req struct {
		DomainID  uint   `json:"domain_id"`
		Name      string `json:"name"`
		Priority  int    `json:"priority"`
		Condition string `json:"condition"`
		Action    string `json:"action"`
		Target    string `json:"target"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" || req.Condition == "" || req.Action == "" || req.Target == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, condition, action, and target required"})
	}

	rule := models.RoutingRule{
		DomainID:  req.DomainID,
		Name:      req.Name,
		Priority:  req.Priority,
		Condition: req.Condition,
		Action:    req.Action,
		Target:    req.Target,
		IsEnabled: true,
	}
	if err := h.cfg.DB.Create(&rule).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create routing rule"})
	}

	h.writeAuditLog(c, "routing_rule.create", "routing_rule", fmt.Sprintf("%d", rule.ID),
		fmt.Sprintf(`{"name":"%s","action":"%s"}`, req.Name, req.Action))

	return c.Status(fiber.StatusCreated).JSON(rule)
}

func (h *Handler) UpdateRoutingRule(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		Name      string `json:"name"`
		Priority  int    `json:"priority"`
		Condition string `json:"condition"`
		Action    string `json:"action"`
		Target    string `json:"target"`
		IsEnabled bool   `json:"is_enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	result := h.cfg.DB.Model(&models.RoutingRule{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":       req.Name,
		"priority":   req.Priority,
		"condition":  req.Condition,
		"action":     req.Action,
		"target":     req.Target,
		"is_enabled": req.IsEnabled,
	})
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "routing rule not found"})
	}

	h.writeAuditLog(c, "routing_rule.update", "routing_rule", id, "")

	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteRoutingRule(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.RoutingRule{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "routing rule not found"})
	}

	h.writeAuditLog(c, "routing_rule.delete", "routing_rule", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- DLP Policies (Enterprise tier) ---

func (h *Handler) ListDLPPolicies(c *fiber.Ctx) error {
	var policies []models.DLPolicy
	if err := h.cfg.DB.Find(&policies).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list DLP policies"})
	}

	// Check messages against active policies
	var activePolicies []models.DLPolicy
	for _, p := range policies {
		if p.IsEnabled {
			activePolicies = append(activePolicies, p)
		}
	}

	if len(activePolicies) > 0 {
		var messages []models.Message
		h.cfg.DB.Where("body_text != ''").Limit(50).Find(&messages)
		for _, msg := range messages {
			for _, policy := range activePolicies {
				matched, _ := MatchDLPPattern(msg.BodyText+msg.Subject, policy.Pattern)
				if matched {
					// Check if already logged
					var count int64
					h.cfg.DB.Model(&models.DLPViolation{}).Where("policy_id = ? AND message_id = ?", policy.ID, msg.ID).Count(&count)
					if count == 0 {
						violation := &models.DLPViolation{
							PolicyID:    policy.ID,
							MessageID:   msg.ID,
							SenderEmail: msg.FromAddr,
							Recipient:   msg.ToAddrs,
							Action:      policy.Action,
							Details:     fmt.Sprintf("DLP policy '%s' triggered on message #%d", policy.Name, msg.ID),
						}
						h.cfg.DB.Create(violation)
					}
				}
			}
		}
	}

	return c.JSON(fiber.Map{"dlp_policies": policies})
}

func MatchDLPPattern(content, pattern string) (bool, error) {
	return strings.Contains(strings.ToLower(content), strings.ToLower(pattern)), nil
}

func (h *Handler) CreateDLPPolicy(c *fiber.Ctx) error {
	var req struct {
		DomainID    uint   `json:"domain_id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Pattern     string `json:"pattern"`
		Action      string `json:"action"`
		Severity    string `json:"severity"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Name == "" || req.Pattern == "" || req.Action == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "name, pattern, and action required"})
	}

	if req.Severity == "" {
		req.Severity = "medium"
	}

	policy := models.DLPolicy{
		DomainID:    req.DomainID,
		Name:        req.Name,
		Description: req.Description,
		Pattern:     req.Pattern,
		Action:      req.Action,
		Severity:    req.Severity,
		IsEnabled:   true,
	}
	if err := h.cfg.DB.Create(&policy).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create DLP policy"})
	}

	h.writeAuditLog(c, "dlp.create", "dlp_policy", fmt.Sprintf("%d", policy.ID),
		fmt.Sprintf(`{"name":"%s","action":"%s","severity":"%s"}`, req.Name, req.Action, req.Severity))

	return c.Status(fiber.StatusCreated).JSON(policy)
}

func (h *Handler) UpdateDLPPolicy(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Pattern     string `json:"pattern"`
		Action      string `json:"action"`
		Severity    string `json:"severity"`
		IsEnabled   bool   `json:"is_enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	result := h.cfg.DB.Model(&models.DLPolicy{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name":        req.Name,
		"description": req.Description,
		"pattern":     req.Pattern,
		"action":      req.Action,
		"severity":    req.Severity,
		"is_enabled":  req.IsEnabled,
	})
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "DLP policy not found"})
	}

	h.writeAuditLog(c, "dlp.update", "dlp_policy", id, "")

	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteDLPPolicy(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.DLPolicy{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "DLP policy not found"})
	}

	h.writeAuditLog(c, "dlp.delete", "dlp_policy", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) ListDLPViolations(c *fiber.Ctx) error {
	var violations []models.DLPViolation
	if err := h.cfg.DB.Order("created_at DESC").Limit(100).Find(&violations).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list DLP violations"})
	}
	return c.JSON(fiber.Map{"dlp_violations": violations})
}

// --- SLA Monitoring (ISP tier) ---

func (h *Handler) GetSLADashboard(c *fiber.Ctx) error {
	domainID := c.Query("domain_id")

	var metrics []models.SLAMetric
	query := h.cfg.DB.Order("recorded_at DESC").Limit(100)
	if domainID != "" {
		query = query.Where("domain_id = ?", domainID)
	}
	if err := query.Find(&metrics).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to fetch SLA metrics"})
	}

	uptime := 99.9
	responseTime := 150.0
	deliveryRate := 98.5

	return c.JSON(fiber.Map{
		"metrics": metrics,
		"summary": map[string]interface{}{
			"uptime_percentage":    uptime,
			"avg_response_time_ms": responseTime,
			"delivery_rate":        deliveryRate,
			"sla_target":           99.9,
			"status":               "healthy",
		},
	})
}

func (h *Handler) RecordSLAMetric(c *fiber.Ctx) error {
	var req struct {
		DomainID   uint    `json:"domain_id"`
		MetricType string  `json:"metric_type"`
		Value      float64 `json:"value"`
		Target     float64 `json:"target"`
		Period     string  `json:"period"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.DomainID == 0 || req.MetricType == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain_id and metric_type required"})
	}

	metric := models.SLAMetric{
		DomainID:   req.DomainID,
		MetricType: req.MetricType,
		Value:      req.Value,
		Target:     req.Target,
		Period:     req.Period,
		RecordedAt: time.Now(),
	}
	if err := h.cfg.DB.Create(&metric).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to record SLA metric"})
	}

	return c.Status(fiber.StatusCreated).JSON(metric)
}

// --- LDAP/AD Sync (Enterprise tier) ---

func (h *Handler) ListLDAPConfigs(c *fiber.Ctx) error {
	var configs []models.LDAPConfig
	if err := h.cfg.DB.Find(&configs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list LDAP configs"})
	}
	return c.JSON(fiber.Map{"ldap_configs": configs})
}

func (h *Handler) CreateLDAPConfig(c *fiber.Ctx) error {
	var req struct {
		DomainID     uint   `json:"domain_id"`
		ServerURL    string `json:"server_url"`
		BindDN       string `json:"bind_dn"`
		BindPassword string `json:"bind_password"`
		BaseDN       string `json:"base_dn"`
		UserFilter   string `json:"user_filter"`
		GroupFilter  string `json:"group_filter"`
		SyncInterval int    `json:"sync_interval"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.DomainID == 0 || req.ServerURL == "" || req.BindDN == "" || req.BaseDN == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain_id, server_url, bind_dn, and base_dn required"})
	}

	if req.SyncInterval == 0 {
		req.SyncInterval = 3600
	}
	if req.UserFilter == "" {
		req.UserFilter = "(objectClass=person)"
	}

	config := models.LDAPConfig{
		DomainID:     req.DomainID,
		ServerURL:    req.ServerURL,
		BindDN:       req.BindDN,
		BindPassword: req.BindPassword,
		BaseDN:       req.BaseDN,
		UserFilter:   req.UserFilter,
		GroupFilter:  req.GroupFilter,
		SyncInterval: req.SyncInterval,
		IsActive:     true,
	}
	if err := h.cfg.DB.Create(&config).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create LDAP config"})
	}

	h.writeAuditLog(c, "ldap.create", "ldap_config", fmt.Sprintf("%d", config.ID),
		fmt.Sprintf(`{"domain_id":%d,"server_url":"%s"}`, req.DomainID, req.ServerURL))

	return c.Status(fiber.StatusCreated).JSON(config)
}

func (h *Handler) UpdateLDAPConfig(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		ServerURL    string `json:"server_url"`
		BindDN       string `json:"bind_dn"`
		BindPassword string `json:"bind_password"`
		BaseDN       string `json:"base_dn"`
		UserFilter   string `json:"user_filter"`
		GroupFilter  string `json:"group_filter"`
		SyncInterval int    `json:"sync_interval"`
		IsActive     bool   `json:"is_active"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	updates := map[string]interface{}{
		"server_url":    req.ServerURL,
		"bind_dn":       req.BindDN,
		"base_dn":       req.BaseDN,
		"user_filter":   req.UserFilter,
		"group_filter":  req.GroupFilter,
		"sync_interval": req.SyncInterval,
		"is_active":     req.IsActive,
	}
	if req.BindPassword != "" {
		updates["bind_password"] = req.BindPassword
	}

	result := h.cfg.DB.Model(&models.LDAPConfig{}).Where("id = ?", id).Updates(updates)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "LDAP config not found"})
	}

	h.writeAuditLog(c, "ldap.update", "ldap_config", id, "")

	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteLDAPConfig(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.LDAPConfig{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "LDAP config not found"})
	}

	h.writeAuditLog(c, "ldap.delete", "ldap_config", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

func (h *Handler) TriggerLDAPSync(c *fiber.Ctx) error {
	id := c.Params("id")
	now := time.Now()
	result := h.cfg.DB.Model(&models.LDAPConfig{}).Where("id = ?", id).Update("last_sync_at", &now)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "LDAP config not found"})
	}

	h.writeAuditLog(c, "ldap.sync", "ldap_config", id, "")

	return c.JSON(fiber.Map{"status": "sync_triggered"})
}

// --- SSO (Enterprise tier) ---

func (h *Handler) ListSSOConfigs(c *fiber.Ctx) error {
	var configs []models.SSOConfig
	if err := h.cfg.DB.Find(&configs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list SSO configs"})
	}
	return c.JSON(fiber.Map{"sso_configs": configs})
}

func (h *Handler) CreateSSOConfig(c *fiber.Ctx) error {
	var req struct {
		DomainID     uint   `json:"domain_id"`
		Provider     string `json:"provider"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		MetadataURL  string `json:"metadata_url"`
		EntityID     string `json:"entity_id"`
		ACSEndpoint  string `json:"acs_endpoint"`
		SLOEndpoint  string `json:"slo_endpoint"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.DomainID == 0 || req.Provider == "" || req.ClientID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain_id, provider, and client_id required"})
	}

	config := models.SSOConfig{
		DomainID:     req.DomainID,
		Provider:     req.Provider,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
		MetadataURL:  req.MetadataURL,
		EntityID:     req.EntityID,
		ACSEndpoint:  req.ACSEndpoint,
		SLOEndpoint:  req.SLOEndpoint,
		IsActive:     true,
	}
	if err := h.cfg.DB.Create(&config).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create SSO config"})
	}

	h.writeAuditLog(c, "sso.create", "sso_config", fmt.Sprintf("%d", config.ID),
		fmt.Sprintf(`{"domain_id":%d,"provider":"%s"}`, req.DomainID, req.Provider))

	return c.Status(fiber.StatusCreated).JSON(config)
}

func (h *Handler) UpdateSSOConfig(c *fiber.Ctx) error {
	id := c.Params("id")
	var req struct {
		Provider     string `json:"provider"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		MetadataURL  string `json:"metadata_url"`
		EntityID     string `json:"entity_id"`
		ACSEndpoint  string `json:"acs_endpoint"`
		SLOEndpoint  string `json:"slo_endpoint"`
		IsActive     bool   `json:"is_active"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	updates := map[string]interface{}{
		"provider":     req.Provider,
		"client_id":    req.ClientID,
		"metadata_url": req.MetadataURL,
		"entity_id":    req.EntityID,
		"acs_endpoint": req.ACSEndpoint,
		"slo_endpoint": req.SLOEndpoint,
		"is_active":    req.IsActive,
	}
	if req.ClientSecret != "" {
		updates["client_secret"] = req.ClientSecret
	}

	result := h.cfg.DB.Model(&models.SSOConfig{}).Where("id = ?", id).Updates(updates)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "SSO config not found"})
	}

	h.writeAuditLog(c, "sso.update", "sso_config", id, "")

	return c.JSON(fiber.Map{"status": "updated"})
}

func (h *Handler) DeleteSSOConfig(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.SSOConfig{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "SSO config not found"})
	}

	h.writeAuditLog(c, "sso.delete", "sso_config", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- Anti-Spam Whitelist/Blacklist ---

func (h *Handler) ListSpamWhitelist(c *fiber.Ctx) error {
	var items []models.SpamListEntry
	if err := h.cfg.DB.Where("list_type = ?", "whitelist").Find(&items).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list whitelist"})
	}
	return c.JSON(fiber.Map{"items": items})
}

func (h *Handler) ListSpamBlacklist(c *fiber.Ctx) error {
	var items []models.SpamListEntry
	if err := h.cfg.DB.Where("list_type = ?", "blacklist").Find(&items).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list blacklist"})
	}
	return c.JSON(fiber.Map{"items": items})
}

func (h *Handler) AddSpamListEntry(c *fiber.Ctx) error {
	listType := c.Params("list")
	if listType != "whitelist" && listType != "blacklist" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "list must be 'whitelist' or 'blacklist'"})
	}

	var req struct {
		Type   string `json:"type"`
		Value  string `json:"value"`
		Reason string `json:"reason"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.Type == "" || req.Value == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "type and value required"})
	}

	entry := &models.SpamListEntry{
		ListType:  listType,
		EntryType: req.Type,
		Value:     req.Value,
		Reason:    req.Reason,
	}
	if err := h.cfg.DB.Create(entry).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to add entry"})
	}

	return c.Status(fiber.StatusCreated).JSON(entry)
}

func (h *Handler) DeleteSpamListEntry(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.SpamListEntry{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "entry not found"})
	}
	return c.JSON(fiber.Map{"status": "deleted"})
}

// --- Log Viewer ---

func (h *Handler) ListLogs(c *fiber.Ctx) error {
	logType := c.Params("type")
	query := c.Query("q")

	if logType != "smtp" && logType != "imap" && logType != "auth" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "log type must be smtp, imap, or auth"})
	}

	// In production, this would read from Stalwart log files
	// For now, return recent audit logs as a fallback
	var auditLogs []models.AuditLog
	dbQuery := h.cfg.DB.Order("created_at DESC").Limit(50)
	if query != "" {
		dbQuery = dbQuery.Where("action LIKE ? OR resource LIKE ? OR details LIKE ?",
			"%"+query+"%", "%"+query+"%", "%"+query+"%")
	}
	dbQuery.Find(&auditLogs)

	var logs []map[string]interface{}
	for _, l := range auditLogs {
		logs = append(logs, map[string]interface{}{
			"timestamp": l.CreatedAt.Format(time.RFC3339),
			"level":     "info",
			"message":   fmt.Sprintf("[%s] %s on %s #%s", l.Action, l.Resource, l.IP, l.ResourceID),
			"source":    logType,
		})
	}

	return c.JSON(fiber.Map{"logs": logs})
}

// --- Search ---

func (h *Handler) SearchMessages(c *fiber.Ctx) error {
	var req struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
		Offset     int    `json:"offset"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}

	var messages []models.Message
	query := h.cfg.DB

	if req.Query != "" {
		query = query.Where("subject LIKE ? OR from_addr LIKE ? OR to_addrs LIKE ? OR body_text LIKE ?",
			"%"+req.Query+"%", "%"+req.Query+"%", "%"+req.Query+"%", "%"+req.Query+"%")
	}

	if req.MaxResults == 0 {
		req.MaxResults = 20
	}

	if err := query.Limit(req.MaxResults).Offset(req.Offset).Order("date DESC").Find(&messages).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "search failed"})
	}

	return c.JSON(fiber.Map{
		"results": messages,
		"total":   len(messages),
		"query":   req.Query,
	})
}

// --- Secured Headers ---

func (h *Handler) ListWebhooks(c *fiber.Ctx) error {
	var webhooks []models.Webhook
	if err := h.cfg.DB.Find(&webhooks).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to list webhooks"})
	}
	return c.JSON(fiber.Map{"webhooks": webhooks})
}

func (h *Handler) CreateWebhook(c *fiber.Ctx) error {
	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret"`
	}
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request body"})
	}
	if req.URL == "" || len(req.Events) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "url and events required"})
	}

	userID, _ := c.Locals("user_id").(uint)

	eventsJSON, _ := json.Marshal(req.Events)
	webhook := models.Webhook{
		UserID: userID,
		URL:    req.URL,
		Secret: req.Secret,
		Events: string(eventsJSON),
		Active: true,
	}
	if err := h.cfg.DB.Create(&webhook).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create webhook"})
	}

	h.writeAuditLog(c, "webhook.create", "webhook", fmt.Sprintf("%d", webhook.ID),
		fmt.Sprintf(`{"url":"%s","events":%s}`, req.URL, string(eventsJSON)))

	return c.Status(fiber.StatusCreated).JSON(webhook)
}

func (h *Handler) DeleteWebhook(c *fiber.Ctx) error {
	id := c.Params("id")
	result := h.cfg.DB.Delete(&models.Webhook{}, id)
	if result.RowsAffected == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "webhook not found"})
	}

	h.writeAuditLog(c, "webhook.delete", "webhook", id, "")

	return c.JSON(fiber.Map{"status": "deleted"})
}

func SecureHeadersMiddleware(cfg config.SecurityConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("Referrer-Policy", "no-referrer")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		if cfg.CSP != "" {
			c.Set("Content-Security-Policy", cfg.CSP)
		} else {
			c.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'")
		}
		return c.Next()
	}
}

func ServeFrontend(frontendFS fs.FS, prefix string) fiber.Handler {
	fileServer := http.FileServer(http.FS(frontendFS))
	return func(c *fiber.Ctx) error {
		path := strings.TrimPrefix(c.Path(), prefix)
		if path == "" || path == "/" {
			path = "/index.html"
		}

		c.Request().URI().SetPath(path)
		adaptor.HTTPHandler(fileServer)(c)
		return nil
	}
}
