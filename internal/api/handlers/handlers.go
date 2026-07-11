package handlers

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/orvix/orvix/internal/antivirus"
	"github.com/orvix/orvix/internal/api/handlers/settings"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/push"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/dnsops"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/models"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/ruler"
	"github.com/orvix/orvix/internal/runtime"
	settingsbridge "github.com/orvix/orvix/internal/settings/bridge"
	"github.com/orvix/orvix/internal/tlsmgmt"
	"github.com/orvix/orvix/internal/trustmgmt"
	"github.com/orvix/orvix/internal/updater"
	"github.com/orvix/orvix/internal/webmailmgmt"
	"go.uber.org/zap"
	"golang.org/x/crypto/argon2"
	"gorm.io/gorm"
)

// Handler holds dependencies for all HTTP handlers.
type Handler struct {
	db          *gorm.DB
	dialect     *dbdialect.Info
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
	mailStore *storage.MailStore

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

	// pushNotifier handles browser push notification dispatching.
	pushNotifier *push.PushNotifier

	// listenerRegistry holds the live listener startup state
	// for SMTP/IMAP/POP3/JMAP. Populated by the coremail
	// runtime module during Start(). Passed to the admin
	// runtime telemetry endpoint for the dashboard.
	listenerRegistry *runtime.ListenerRegistry

	// trustService is the trust / lockout management service.
	// Set once at router construction. nil trustService means
	// the login protection endpoints return 503.
	trustService *trustmgmt.Service

	// trustPersistence tracks whether the trust engine's LoadFromDB
	// succeeded. When false, lockouts are tracked in-memory only and
	// the admin UI shows a degraded persistence warning.
	trustPersistence      string // "db" or "in_memory"
	trustPersistenceOK    bool
	trustPersistenceError string // sanitized, never contains raw DB internals

	// dnsOps is the DNS / DKIM operations service. Set once at
	// router construction (api.NewRouter). The admin DNS Ops
	// handlers (plan / verify / provider / DKIM keygen) read
	// from this service; tests can pass a custom service with
	// an in-memory Resolver so DNS lookups do not require
	// internet. nil dnsOps means the handlers return 503 — the
	// admin UI can still render but every action fails closed
	// rather than fabricating data.
	dnsOps *dnsops.Service

	// settingsStore is the DB-backed admin settings persistence
	// layer (see internal/api/handlers/settings). Set once at
	// router construction. PATCH /api/v1/admin/settings writes
	// to it; GET /api/v1/admin/settings merges its entries with
	// the config defaults to build the response.
	settingsStore *settings.Store

	// licenseValidator is the structured license validator
	// from internal/license. The admin GET /api/v1/license
	// endpoint returns its Status() report, which separates
	// public_key_missing / license_missing / expired / valid.
	// Wired in api.NewRouter; nil only in tests that pre-date
	// the completion work.
	licenseValidator *license.Validator

	// tlsService is the admin-facing TLS / certificate manager
	// (internal/tlsmgmt). Set once via SetTLSService. nil
	// disables the SSL admin endpoints with a 503 rather
	// than fabricating cert metadata.
	tlsService *tlsmgmt.Service

	// antivirusService is the wired-in ClamAV engine
	// (internal/antivirus). Optional: nil disables the
	// runtime_enforced assertion in /admin/security/antivirus.
	antivirusService *antivirus.Engine

	// rulerService is the wired-in rule engine
	// (internal/ruler). Optional: nil disables the
	// runtime_enforced assertion in /admin/security/{routing,rules}.
	rulerService *ruler.Engine

	// observability is wired by the runtime so the
	// admin endpoints can surface per-policy counters
	// (rejected / quarantined / tagged / fail_open /
	// fail_closed) without re-reading the metrics
	// package.
	observability *observability.Observability

	// settingsBridge is the boot-time loader that
	// applies persisted protocol settings to the
	// live cfg. The admin /settings/protocol/:protocol
	// endpoint reads through this handle.
	settingsBridge *settingsbridge.Bridge
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
	var dial *dbdialect.Info
	if cfg != nil {
		dial = dbdialect.FromDriver(cfg.Database.Driver)
	}
	return &Handler{
		db:          db,
		dialect:     dial,
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

// SetDNSOpsService wires the DNS / DKIM operations service into
// the handler. The admin DNS Ops endpoints read from this
// service. The service is constructed in api.NewRouter so the
// same Resolver / providers are shared with the rest of the admin
// endpoints. Passing nil leaves the service unavailable — the
// admin handlers will return 503 rather than fabricating data.
func (h *Handler) SetDNSOpsService(s *dnsops.Service) {
	h.dnsOps = s
}

// SetSettingsStore wires the admin settings persistence layer.
// Without it, PATCH /api/v1/admin/settings continues to return
// the "not_implemented" stub from the previous release; with it,
// patches are validated, persisted to admin_settings, and audited.
// The store is created in api.NewRouter.
func (h *Handler) SetSettingsStore(s *settings.Store) {
	h.settingsStore = s
}

// SetLicenseValidator wires the structured license validator.
// Without it, GET /api/v1/license falls back to a generic
// "license validator not wired" offline status. The validator
// is shared with the license-gated feature checks in the
// coremail / DNS / provisioning code paths.
func (h *Handler) SetLicenseValidator(v *license.Validator) {
	h.licenseValidator = v
}

// SetTLSService wires the admin TLS / certificate manager.
// Optional (nil means the SSL endpoints return 503 instead
// of fabricating metadata).
func (h *Handler) SetTLSService(s *tlsmgmt.Service) {
	h.tlsService = s
}

// SetAntivirusService wires the runtime antivirus engine
// into the admin handler so /admin/security/antivirus can
// report the engine's own snapshot (runtime_enforced,
// last_error, per-policy counters) instead of a
// reachability-only probe.
func (h *Handler) SetAntivirusService(s *antivirus.Engine) {
	h.antivirusService = s
}

// SetRulerService wires the runtime rule engine into the
// admin handler. /admin/security/{routing,rules} read
// the per-engine runtime_enforced flag from this handle.
func (h *Handler) SetRulerService(s *ruler.Engine) {
	h.rulerService = s
}

// SetObservability wires the runtime observability
// pipeline into the admin handler. The antivirus and
// settings endpoints use it to surface per-policy
// counters without re-reading the metrics package.
func (h *Handler) SetObservability(o *observability.Observability) {
	h.observability = o
}

// SetSettingsBridge wires the boot-time settings
// loader so the /settings/protocol/:protocol endpoint
// can surface applied vs pending-restart keys.
func (h *Handler) SetSettingsBridge(b *settingsbridge.Bridge) {
	h.settingsBridge = b
}

// loginDomain extracts the domain part from an email address.
// Returns "" for invalid addresses.
func loginDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) < 2 || parts[1] == "" {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(parts[1]))
}

func (h *Handler) SetTrustService(s *trustmgmt.Service) {
	h.trustService = s
}

// SetTrustPersistence sets the trust engine persistence state after
// LoadFromDB. Call this from the router after wiring the trust service.
func (h *Handler) SetTrustPersistence(ok bool, errMsg string) {
	if ok {
		h.trustPersistence = "db"
		h.trustPersistenceOK = true
		h.trustPersistenceError = ""
	} else {
		h.trustPersistence = "in_memory"
		h.trustPersistenceOK = false
		h.trustPersistenceError = errMsg
	}
}

// setPreferredSessionCookie sets the opaque HttpOnly session cookie.
func (h *Handler) setPreferredSessionCookie(c fiber.Ctx, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     "__Host-orvix_session",
		Value:    token,
		Expires:  time.Now().Add(30 * time.Minute),
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Lax",
		Path:     "/",
	})
}

// issueLoginSession creates an opaque session and sets the session
// cookie. The role and email passed here are the server-derived
// values from the users table at login time — they get persisted
// alongside the SHA-256 hash of the session token so the auth
// middleware can restore the real role on every request without
// trusting the client. If the underlying store refuses the write
// (DB unavailable, schema drift, etc.) the error is returned and
// the caller MUST fail the request with a 5xx — silently logging
// the error and continuing would let a non-admin user hold a
// session whose role we cannot restore, which would either lock
// them out at the role gate or, worse, allow a role downgrade if
// the middleware ever had to fall back to a default.
func (h *Handler) issueLoginSession(c fiber.Ctx, userID uint, role auth.Role, email string) error {
	if userID == 0 {
		return fmt.Errorf("issue login session: userID is required")
	}
	if role == "" {
		return fmt.Errorf("issue login session: role is required")
	}
	token, err := h.auth.GenerateOpaqueSession(userID, role, email)
	if err != nil {
		return err
	}
	h.setPreferredSessionCookie(c, token)
	return nil
}

// recordLoginFailure records a failed login attempt via the security monitor.
func (h *Handler) recordLoginFailure(c fiber.Ctx, email string) {
	h.security.RecordFailedLogin(c.Context(), c.IP(), email)
}

// recordLoginSuccess records a successful login and resets the rate limiter.
func (h *Handler) recordLoginSuccess(c fiber.Ctx) {
	h.security.RecordSuccessfulLogin(c.IP())
	if h.rateLimiter != nil {
		h.rateLimiter.ResetLoginLimit(c.IP())
	}
}

// LoginProtectionStatus returns the current state of login protection.
func (h *Handler) LoginProtectionStatus(c fiber.Ctx) error {
	status := fiber.Map{
		"enabled":         h.trustService != nil,
		"rate_limiter":    "active",
		"rate_limit_desc": "100 req/min per IP, 5 login attempts per 15 min per IP",
		"lockout_count":   0,
		"persistence":     h.trustPersistence,
		"persistence_ok":  h.trustPersistenceOK,
	}
	if h.trustPersistence == "" {
		status["persistence"] = "in_memory"
		status["persistence_ok"] = false
	}
	if h.trustPersistenceError != "" {
		status["persistence_error"] = h.trustPersistenceError
	}
	if h.trustService != nil {
		lockouts := h.trustService.ListLockouts(c.Context())
		status["lockout_count"] = len(lockouts)
	}
	return c.JSON(status)
}

// ListLockouts returns current active lockouts from the trust engine.
func (h *Handler) ListLockouts(c fiber.Ctx) error {
	if h.trustService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "trust engine not available"})
	}
	lockouts := h.trustService.ListLockouts(c.Context())
	return c.JSON(fiber.Map{"lockouts": lockouts})
}

// ClearLockout removes a specific lockout by key.
func (h *Handler) ClearLockout(c fiber.Ctx) error {
	if h.trustService == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "trust engine not available"})
	}
	key := c.Params("key")
	if key == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "lockout key is required"})
	}
	if err := h.trustService.ClearLockout(c.Context(), key); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": err.Error()})
	}
	h.writeAuditLog(c, "lockout.clear", key)
	return c.JSON(fiber.Map{"result": "cleared", "key": key})
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
	var mfaEnabled bool

	sqlDB, err := h.db.DB()
	if err != nil {
		h.logger.Error("failed to get underlying DB", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal error"})
	}

	err = sqlDB.QueryRow("SELECT id, password_hash, role, COALESCE(mfa_enabled, "+h.dialect.FalseLiteral()+") FROM users WHERE email = "+h.dialect.Placeholder(1), loginEmail).Scan(&userID, &passwordHash, &userRole, &mfaEnabled)
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

	// MFA enforcement: if MFA is enabled, do NOT issue access/refresh tokens.
	// Instead return an MFA challenge token that can only be exchanged at
	// the /auth/mfa/verify endpoint.
	if mfaEnabled {
		challengeToken, err := h.auth.GenerateMFAChallengeToken(userID)
		if err != nil {
			h.logger.Error("failed to generate MFA challenge token", zap.Error(err))
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
		}
		h.logger.Info("MFA challenge issued", zap.Uint("user_id", userID))
		return c.JSON(fiber.Map{
			"mfa_required":   true,
			"mfa_challenge":  challengeToken,
			"mfa_expires_in": 300,
		})
	}

	// Issue opaque session cookie alongside JWT for transition.
	// Cookie issuance is the source of truth for browser auth; if
	// the store refuses the write we refuse the login rather than
	// return success without a usable session.
	if err := h.issueLoginSession(c, userID, auth.Role(userRole), loginEmail); err != nil {
		h.logger.Error("failed to issue login session", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "authentication failed"})
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

// revokeCurrentAccessToken revokes the access token presented on the request
// (cookie or Bearer header) so it is rejected immediately, closing the H-9 gap
// where a stateless JWT stayed valid until expiry after logout. Best-effort:
// never blocks logout if the revocation store errors.
func (h *Handler) revokeCurrentAccessToken(c fiber.Ctx) {
	token := c.Cookies("access_token")
	if token == "" {
		authHeader := c.Get("Authorization")
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			token = authHeader[7:]
		}
	}
	if token == "" {
		return
	}
	if err := h.auth.RevokeAccessToken(token); err != nil {
		h.logger.Warn("failed to revoke access token on logout", zap.Error(err))
	}
}

// Logout clears auth cookies and revokes the presented access token.
func (h *Handler) Logout(c fiber.Ctx) error {
	h.revokeCurrentAccessToken(c)
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
	h.revokeCurrentAccessToken(c)
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

	err = sqlDB.QueryRow("SELECT email, role FROM users WHERE id = "+h.dialect.Placeholder(1), userID).Scan(&email, &role)
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
		confs = append(confs, "LOWER(name) LIKE "+h.dialect.Placeholder(len(args)+1))
		args = append(args, "%"+strings.ToLower(q)+"%")
	}
	if statusFilter == "active" || statusFilter == "suspended" {
		confs = append(confs, "status = "+h.dialect.Placeholder(len(args)+1))
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
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", rd.ID).Scan(&mailboxCount)
		result = append(result, domainRow{ID: rd.ID, Domain: rd.Name, Plan: rd.Plan, Status: rd.Status, MailboxCount: int(mailboxCount)})
	}

	if result == nil {
		result = []domainRow{}
	}
	return c.JSON(result)
}

// CreateDomain creates a new mail domain in CoreMail.
//
// ADMIN-CONSOLE-FINAL-POLISH: the request shape now includes the
// enterprise provisioning knobs that the admin UI exposes in the
// "Create domain" modal:
//   - name (required)            — fully-qualified domain
//   - status                     — 'active' or 'suspended' (defaults to 'active')
//   - plan                       — 'smb' / 'enterprise' / 'education' / 'free' (defaults to 'smb')
//   - description                — operator note, freeform
//   - max_mailboxes              — 0 means unlimited
//   - max_aliases                — 0 means unlimited
//   - max_quota_mb               — per-mailbox quota in MB (0 means unlimited)
//   - dkim_enabled               — boolean (0 / 1)
//   - dkim_selector              — string (defaults to a safe fallback)
//   - dmarc_enabled              — boolean
//   - mtasts_enabled             — boolean
//   - catchall_address           — string (empty disables catch-all)
//   - abuse_contact              — string
//
// Unknown fields are rejected (the entire PATCH is rolled back)
// so the frontend can never silently drop a field. RBAC, CSRF,
// audit, and "no raw DB errors" guarantees from the previous
// release are preserved; see TestAdminDomainCreateAdvancedFields
// in handlers/admin_domain_advanced_test.go.
func (h *Handler) CreateDomain(c fiber.Ctx) error {
	var req struct {
		Name            string `json:"name"`
		Status          string `json:"status"`
		Plan            string `json:"plan"`
		Description     string `json:"description"`
		MaxMailboxes    *int64 `json:"max_mailboxes"`
		MaxAliases      *int64 `json:"max_aliases"`
		MaxQuotaMB      *int64 `json:"max_quota_mb"`
		DKIMEnabled     *bool  `json:"dkim_enabled"`
		DKIMSelector    string `json:"dkim_selector"`
		DMARCEnabled    *bool  `json:"dmarc_enabled"`
		MTASTSEnabled   *bool  `json:"mtasts_enabled"`
		CatchallAddress string `json:"catchall_address"`
		AbuseContact    string `json:"abuse_contact"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body: " + err.Error()})
	}
	if req.Name == "" {
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

	// Normalise the admin-supplied fields. Status defaults to
	// active; plan defaults to smb; description is plain text.
	status := strings.ToLower(strings.TrimSpace(req.Status))
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "suspended" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "status must be 'active' or 'suspended'"})
	}
	plan := strings.ToLower(strings.TrimSpace(req.Plan))
	switch plan {
	case "", "smb", "enterprise", "education", "free":
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "plan must be one of smb|enterprise|education|free"})
	}
	if plan == "" {
		plan = "smb"
	}
	description := strings.TrimSpace(req.Description)
	if len(description) > 512 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "description too long (max 512 chars)"})
	}

	// Bounds: never let the UI push negative limits, never trust
	// the client to fill in missing pointers.
	maxMailboxes := int64(0)
	if req.MaxMailboxes != nil {
		if *req.MaxMailboxes < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "max_mailboxes cannot be negative"})
		}
		maxMailboxes = *req.MaxMailboxes
	}
	maxAliases := int64(0)
	if req.MaxAliases != nil {
		if *req.MaxAliases < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "max_aliases cannot be negative"})
		}
		maxAliases = *req.MaxAliases
	}
	maxQuotaMB := int64(0)
	if req.MaxQuotaMB != nil {
		if *req.MaxQuotaMB < 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "max_quota_mb cannot be negative"})
		}
		maxQuotaMB = *req.MaxQuotaMB
	}

	dkimEnabled := 0
	if req.DKIMEnabled != nil && *req.DKIMEnabled {
		dkimEnabled = 1
	}
	dkimSelector := strings.TrimSpace(req.DKIMSelector)
	if dkimSelector != "" {
		// DKIM selector is a DNS label; only allow letters, digits,
		// dashes, underscores, dots. Reject spaces and slashes.
		for _, ch := range dkimSelector {
			if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
				continue
			}
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "dkim_selector contains invalid characters"})
		}
		if len(dkimSelector) > 64 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "dkim_selector too long (max 64 chars)"})
		}
	} else if dkimEnabled == 1 {
		// Generate a safe default selector if the operator enabled DKIM
		// without picking one. Keeps downstream DNS verification happy.
		dkimSelector = "default"
	}
	dmarcEnabled := 0
	if req.DMARCEnabled != nil && *req.DMARCEnabled {
		dmarcEnabled = 1
	}
	mtastsEnabled := 0
	if req.MTASTSEnabled != nil && *req.MTASTSEnabled {
		mtastsEnabled = 1
	}
	catchallAddress := strings.TrimSpace(req.CatchallAddress)
	if catchallAddress != "" {
		// Catch-all must be an address on the same domain.
		if !strings.HasSuffix(strings.ToLower(catchallAddress), "@"+domainName) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "catchall_address must be on the same domain"})
		}
		if _, err := mail.ParseAddress(catchallAddress); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "catchall_address is not a valid email address"})
		}
	}
	abuseContact := strings.TrimSpace(req.AbuseContact)
	if abuseContact != "" {
		if _, err := mail.ParseAddress(abuseContact); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "abuse_contact is not a valid email address"})
		}
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var existing int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_domains WHERE name = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", domainName).Scan(&existing)
	if existing > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "domain already exists: " + domainName})
	}

	now := time.Now().UTC()
	// The domain belongs to the creating caller's own tenant. This was
	// previously hardcoded to 0, which silently orphaned every
	// API-created domain from tenant scoping (see callerOwnsTenant) —
	// harmless before tenant checks existed, but it must be the real
	// tenant now that GetDomain/UpdateDomainStatus/DeleteDomain enforce
	// ownership.
	callerTenantID := h.scopedTenantID(c)
	result, err := sqlDB.Exec(
		"INSERT INTO coremail_domains (name, tenant_id, reseller_id, status, plan, description, max_mailboxes, max_aliases, max_quota_mb, dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled, catchall_address, abuse_contact, labels, mailbox_count, created_at, updated_at)"+
			" VALUES ("+h.dialect.Placeholder(1)+", "+h.dialect.Placeholder(2)+", 0, "+h.dialect.Placeholder(3)+", "+h.dialect.Placeholder(4)+", "+h.dialect.Placeholder(5)+", "+h.dialect.Placeholder(6)+", "+h.dialect.Placeholder(7)+", "+h.dialect.Placeholder(8)+", "+h.dialect.Placeholder(9)+", "+h.dialect.Placeholder(10)+", "+h.dialect.Placeholder(11)+", "+h.dialect.Placeholder(12)+", "+h.dialect.Placeholder(13)+", "+h.dialect.Placeholder(14)+", '', 0, "+h.dialect.Placeholder(15)+", "+h.dialect.Placeholder(16)+")",
		domainName, callerTenantID, status, plan, description, maxMailboxes, maxAliases, maxQuotaMB,
		dkimEnabled, dkimSelector, dmarcEnabled, mtastsEnabled, catchallAddress, abuseContact,
		now, now,
	)
	if err != nil {
		h.logger.Error("failed to create domain", zap.String("domain", domainName), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "failed to create domain"})
	}

	domainID, _ := result.LastInsertId()
	h.writeAuditLog(c, "domain.create", fmt.Sprintf(
		"domain:%s|status:%s|plan:%s|mailboxes:%d|aliases:%d|quota_mb:%d|dkim:%d|dmarc:%d|mtasts:%d",
		domainName, status, plan, maxMailboxes, maxAliases, maxQuotaMB,
		dkimEnabled, dmarcEnabled, mtastsEnabled,
	))
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":               domainID,
		"domain":           domainName,
		"status":           status,
		"plan":             plan,
		"description":      description,
		"max_mailboxes":    maxMailboxes,
		"max_aliases":      maxAliases,
		"max_quota_mb":     maxQuotaMB,
		"dkim_enabled":     dkimEnabled == 1,
		"dkim_selector":    dkimSelector,
		"dmarc_enabled":    dmarcEnabled == 1,
		"mtasts_enabled":   mtastsEnabled == 1,
		"catchall_address": catchallAddress,
		"abuse_contact":    abuseContact,
		"created_at":       now,
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
	var tenantID int64
	err = sqlDB.QueryRow("SELECT id, name, tenant_id FROM coremail_domains WHERE name = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", idStr).Scan(&domainID, &domainName, &tenantID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found: " + idStr})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found: " + idStr})
	}

	var mailboxCount int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", domainID).Scan(&mailboxCount)
	if mailboxCount > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "domain contains mailboxes", "mailbox_count": mailboxCount})
	}

	now := time.Now().UTC()
	_, err = sqlDB.Exec("UPDATE coremail_domains SET deleted_at = "+h.dialect.Placeholder(1)+", updated_at = "+h.dialect.Placeholder(2)+" WHERE id = "+h.dialect.Placeholder(3)+" AND tenant_id = "+h.dialect.Placeholder(4), now, now, domainID, tenantID)
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
	var tenantID int64
	err = sqlDB.QueryRow("SELECT id, tenant_id FROM coremail_domains WHERE name = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", name).Scan(&domainID, &tenantID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found: " + name})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found: " + name})
	}

	_, err = sqlDB.Exec("UPDATE coremail_domains SET status = "+h.dialect.Placeholder(1)+", updated_at = "+h.dialect.Placeholder(2)+" WHERE id = "+h.dialect.Placeholder(3)+" AND tenant_id = "+h.dialect.Placeholder(4), req.Status, time.Now().UTC(), domainID, tenantID)
	if err != nil {
		h.logger.Error("failed to update domain status", zap.String("domain", name), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "status update failed"})
	}

	h.writeAuditLog(c, "domain.status_update", fmt.Sprintf("domain:%s|status:%s", name, req.Status))
	return c.JSON(fiber.Map{"result": "updated", "domain": name, "status": req.Status})
}

// GetDomain returns details for a single domain.
//
// ADMIN-CONSOLE-FINAL-POLISH: returns the full provisioning
// shape (plan, status, description, mailboxes/aliases/quota
// limits, DKIM/DMARC/MTA-STS flags + DKIM selector, catch-all
// and abuse contact) so the "Domain detail" drawer can show
// every persistent property without a follow-up round trip.
func (h *Handler) GetDomain(c fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var (
		domainID                                    uint
		domainName, status, plan                    string
		description                                 string
		maxMailboxes, maxAliases, maxQuotaMB        int64
		dkimEnabled, dmarcEnabled, mtastsEnabled    int
		dkimSelector, catchallAddress, abuseContact string
		createdAt, updatedAt                        string
		tenantID                                    int64
	)
	err = sqlDB.QueryRow(
		"SELECT id, name, status, plan, description,"+
			" max_mailboxes, max_aliases, max_quota_mb,"+
			" dkim_enabled, dkim_selector, dmarc_enabled, mtasts_enabled,"+
			" catchall_address, abuse_contact,"+
			" created_at, updated_at, tenant_id"+
			" FROM coremail_domains"+
			" WHERE name = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL",
		name,
	).Scan(
		&domainID, &domainName, &status, &plan, &description,
		&maxMailboxes, &maxAliases, &maxQuotaMB,
		&dkimEnabled, &dkimSelector, &dmarcEnabled, &mtastsEnabled,
		&catchallAddress, &abuseContact,
		&createdAt, &updatedAt, &tenantID,
	)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}

	var mailboxCount int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", domainID).Scan(&mailboxCount)

	type briefMailbox struct {
		MailboxID uint   `json:"mailbox_id"`
		Email     string `json:"email"`
		Status    string `json:"status"`
		IsAdmin   bool   `json:"is_admin"`
	}
	var mailboxes []briefMailbox
	mbRows, err := sqlDB.Query("SELECT id, email, status, is_admin FROM coremail_mailboxes WHERE domain_id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL ORDER BY id DESC LIMIT 200", domainID)
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
		"id":               domainID,
		"domain":           domainName,
		"status":           status,
		"plan":             plan,
		"description":      description,
		"max_mailboxes":    maxMailboxes,
		"max_aliases":      maxAliases,
		"max_quota_mb":     maxQuotaMB,
		"mailbox_count":    mailboxCount,
		"dkim_enabled":     dkimEnabled == 1,
		"dkim_selector":    dkimSelector,
		"dmarc_enabled":    dmarcEnabled == 1,
		"mtasts_enabled":   mtastsEnabled == 1,
		"catchall_address": catchallAddress,
		"abuse_contact":    abuseContact,
		"created_at":       createdAt,
		"updated_at":       updatedAt,
		"deleted":          false,
		"mailboxes":        mailboxes,
	})
}

// PatchDomain updates the mutable fields of an existing domain.
//
// ADMIN-CONSOLE-FINAL-POLISH: this is the editable surface the
// Domain Detail drawer's "Edit limits" modal targets. Only
// fields present in the request body are updated; unknown
// fields are rejected (an unknown key aborts the patch so the
// frontend can never silently drop a value).
//
// Wired through CSRF, RBAC, audit, and the same hard-reject
// semantics as PATCH /admin/settings.
func (h *Handler) PatchDomain(c fiber.Ctx) error {
	name := c.Params("name")
	if name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "domain name required"})
	}

	var req map[string]json.RawMessage
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid JSON body"})
	}

	allowed := map[string]struct{}{
		"plan":             {},
		"description":      {},
		"max_mailboxes":    {},
		"max_aliases":      {},
		"max_quota_mb":     {},
		"dkim_enabled":     {},
		"dkim_selector":    {},
		"dmarc_enabled":    {},
		"mtasts_enabled":   {},
		"catchall_address": {},
		"abuse_contact":    {},
	}
	rejected := []string{}
	for k := range req {
		if _, ok := allowed[k]; !ok {
			rejected = append(rejected, k)
		}
	}
	if len(rejected) > 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error":    "patch contained unknown fields; nothing applied",
			"rejected": rejected,
		})
	}

	type update struct {
		set  []string
		args []interface{}
	}
	var u update
	for k, raw := range req {
		switch k {
		case "plan":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": k + ": invalid string"})
			}
			v = strings.ToLower(strings.TrimSpace(v))
			switch v {
			case "smb", "enterprise", "education", "free":
			default:
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "plan must be one of smb|enterprise|education|free"})
			}
			u.set = append(u.set, "plan = "+h.dialect.Placeholder(len(u.args)+1))
			u.args = append(u.args, v)
		case "description":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": k + ": invalid string"})
			}
			v = strings.TrimSpace(v)
			if len(v) > 512 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "description too long (max 512 chars)"})
			}
			u.set = append(u.set, "description = "+h.dialect.Placeholder(len(u.args)+1))
			u.args = append(u.args, v)
		case "max_mailboxes", "max_aliases", "max_quota_mb":
			var n int64
			if err := json.Unmarshal(raw, &n); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": k + ": invalid integer"})
			}
			if n < 0 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": k + " cannot be negative"})
			}
			u.set = append(u.set, k+" = "+h.dialect.Placeholder(len(u.args)+1))
			u.args = append(u.args, n)
		case "dkim_enabled", "dmarc_enabled", "mtasts_enabled":
			var b bool
			if err := json.Unmarshal(raw, &b); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": k + ": invalid bool"})
			}
			val := 0
			if b {
				val = 1
			}
			u.set = append(u.set, k+" = "+h.dialect.Placeholder(len(u.args)+1))
			u.args = append(u.args, val)
		case "dkim_selector":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": k + ": invalid string"})
			}
			v = strings.TrimSpace(v)
			if v != "" {
				for _, ch := range v {
					if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_' || ch == '.' {
						continue
					}
					return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "dkim_selector contains invalid characters"})
				}
				if len(v) > 64 {
					return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "dkim_selector too long (max 64 chars)"})
				}
			}
			u.set = append(u.set, "dkim_selector = "+h.dialect.Placeholder(len(u.args)+1))
			u.args = append(u.args, v)
		case "catchall_address":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": k + ": invalid string"})
			}
			v = strings.TrimSpace(v)
			if v != "" {
				if !strings.HasSuffix(strings.ToLower(v), "@"+strings.ToLower(name)) {
					return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "catchall_address must be on the same domain"})
				}
				if _, err := mail.ParseAddress(v); err != nil {
					return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "catchall_address is not a valid email address"})
				}
			}
			u.set = append(u.set, "catchall_address = "+h.dialect.Placeholder(len(u.args)+1))
			u.args = append(u.args, v)
		case "abuse_contact":
			var v string
			if err := json.Unmarshal(raw, &v); err != nil {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": k + ": invalid string"})
			}
			v = strings.TrimSpace(v)
			if v != "" {
				if _, err := mail.ParseAddress(v); err != nil {
					return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "abuse_contact is not a valid email address"})
				}
			}
			u.set = append(u.set, "abuse_contact = "+h.dialect.Placeholder(len(u.args)+1))
			u.args = append(u.args, v)
		}
	}

	if len(u.set) == 0 {
		return c.JSON(fiber.Map{"applied": []string{}, "domain": name})
	}
	u.set = append(u.set, "updated_at = "+h.dialect.Placeholder(len(u.args)+1))
	u.args = append(u.args, time.Now().UTC())
	u.args = append(u.args, name)

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	res, err := sqlDB.Exec(
		"UPDATE coremail_domains SET "+strings.Join(u.set, ", ")+
			" WHERE name = "+h.dialect.Placeholder(len(u.args))+" AND deleted_at IS NULL",
		u.args...,
	)
	if err != nil {
		h.logger.Error("failed to update domain", zap.String("domain", name), zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "update failed"})
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "domain not found"})
	}

	applied := make([]string, 0, len(req))
	for k := range req {
		applied = append(applied, k)
	}
	h.writeAuditLog(c, "domain.patch", fmt.Sprintf("domain:%s|applied:%d", name, len(applied)))
	return c.JSON(fiber.Map{"applied": applied, "domain": name})
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
	var isAdmin, allowSMTP, allowIMAP, allowPOP3, allowJMAP, allowWebmail bool
	var tenantID int64
	err = sqlDB.QueryRow("SELECT m.email, COALESCE(d.name, ''), m.status, m.is_admin, m.created_at, m.updated_at,"+
		" COALESCE(m.allow_smtp,"+h.dialect.TrueLiteral()+"), COALESCE(m.allow_imap,"+h.dialect.TrueLiteral()+"), COALESCE(m.allow_pop3,"+h.dialect.TrueLiteral()+"), COALESCE(m.allow_jmap,"+h.dialect.TrueLiteral()+"), COALESCE(m.allow_webmail,"+h.dialect.TrueLiteral()+"),"+
		" m.tenant_id"+
		" FROM coremail_mailboxes m LEFT JOIN coremail_domains d ON m.domain_id = d.id"+
		" WHERE m.id = "+h.dialect.Placeholder(1)+" AND m.deleted_at IS NULL", id).Scan(&email, &domainName, &status, &isAdmin, &createdAt, &updatedAt,
		&allowSMTP, &allowIMAP, &allowPOP3, &allowJMAP, &allowWebmail, &tenantID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}

	var messages int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_messages WHERE mailbox_id = "+h.dialect.Placeholder(1)+" AND purged_at IS NULL", id).Scan(&messages)

	var queueItems int64
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE mailbox_id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).Scan(&queueItems)

	return c.JSON(fiber.Map{
		"mailbox_id":    id,
		"email":         email,
		"domain":        domainName,
		"status":        status,
		"is_admin":      isAdmin,
		"created_at":    createdAt,
		"updated_at":    updatedAt,
		"deleted":       false,
		"allow_smtp":    allowSMTP,
		"allow_imap":    allowIMAP,
		"allow_pop3":    allowPOP3,
		"allow_jmap":    allowJMAP,
		"allow_webmail": allowWebmail,
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
		mbConfs = append(mbConfs, "LOWER(email) LIKE "+h.dialect.Placeholder(len(mbArgs)+1))
		mbArgs = append(mbArgs, "%"+strings.ToLower(q)+"%")
	}
	if statusFilter == "active" || statusFilter == "suspended" {
		mbConfs = append(mbConfs, "status = "+h.dialect.Placeholder(len(mbArgs)+1))
		mbArgs = append(mbArgs, statusFilter)
	}
	// admin=true keeps admin mailboxes; admin=false excludes them. When the caller
	// filters by status, admin mailboxes are still mailbox rows so the same
	// filter applies.
	switch adminFilter {
	case "true":
		mbConfs = append(mbConfs, "is_admin = "+h.dialect.TrueLiteral())
	case "false":
		mbConfs = append(mbConfs, "is_admin = "+h.dialect.FalseLiteral())
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
//
// The optional `class_id` field assigns the mailbox to an
// existing account class (mailbox service class). When set,
// the class's `default_quota_mb` / `max_quota_mb` /
// `max_send_per_hour` / `max_recv_per_hour` are applied as
// the defaults — the operator's per-request values still
// win when supplied. The handler refuses to assign a
// class_id that doesn't belong to the same tenant.
func (h *Handler) CreateMailbox(c fiber.Ctx) error {
	var req struct {
		Email         string `json:"email"`
		Password      string `json:"password"`
		Name          string `json:"name"`
		QuotaMB       int64  `json:"quota_mb"`
		ClassID       int64  `json:"class_id"`
		SendLimitHour int64  `json:"send_limit_per_hour"`
		RecvLimitHour int64  `json:"recv_limit_per_hour"`
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
	err = sqlDB.QueryRow("SELECT id, tenant_id, status FROM coremail_domains WHERE name = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", domainName).Scan(&domainID, &tenantID, &domainStatus)
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
	sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE email = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", req.Email).Scan(&existing)
	if existing > 0 {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "mailbox already exists: " + req.Email})
	}

	// Resolve account class. The lookup returns 400 if the
	// class doesn't exist OR doesn't belong to the same
	// tenant. class_id == 0 leaves the mailbox unclassed.
	var (
		classDefaultQuota int64
		classMaxQuota     int64
		classSendPerHr    int64
		classRecvPerHr    int64
	)
	if req.ClassID > 0 {
		var (
			dq, mq, msh, mrh int
		)
		classErr := sqlDB.QueryRow(
			"SELECT default_quota_mb, max_quota_mb, max_send_per_hour, max_recv_per_hour"+
				" FROM coremail_account_classes"+
				" WHERE id = "+h.dialect.Placeholder(1)+" AND tenant_id = "+h.dialect.Placeholder(2)+" AND deleted_at IS NULL",
			req.ClassID, tenantID).Scan(&dq, &mq, &msh, &mrh)
		if classErr == sql.ErrNoRows {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("account class %d not found in tenant %d", req.ClassID, tenantID)})
		}
		if classErr != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "class lookup failed"})
		}
		classDefaultQuota = int64(dq)
		classMaxQuota = int64(mq)
		classSendPerHr = int64(msh)
		classRecvPerHr = int64(mrh)
		// Per-class override: cap the operator-supplied quota at the class max.
		if req.QuotaMB > classMaxQuota && classMaxQuota > 0 {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": fmt.Sprintf("quota_mb %d exceeds class max %d", req.QuotaMB, classMaxQuota)})
		}
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
		quotaMB = classDefaultQuota
	}
	if quotaMB <= 0 {
		quotaMB = 1024
	}
	sendPerHr := req.SendLimitHour
	if sendPerHr <= 0 {
		sendPerHr = classSendPerHr
	}
	recvPerHr := req.RecvLimitHour
	if recvPerHr <= 0 {
		recvPerHr = classRecvPerHr
	}

	result, err := sqlDB.Exec(
		"INSERT INTO coremail_mailboxes"+
			" (domain_id, tenant_id, local_part, email, name, password_hash, auth_scheme, status,"+
			" quota_mb, is_admin, send_limit_per_hour, recv_limit_per_hour, class_id, created_at, updated_at)"+
			" VALUES ("+h.dialect.Placeholder(1)+", "+h.dialect.Placeholder(2)+", "+h.dialect.Placeholder(3)+", "+h.dialect.Placeholder(4)+", "+h.dialect.Placeholder(5)+", "+h.dialect.Placeholder(6)+", 'argon2id', 'active', "+h.dialect.Placeholder(7)+", 0, "+h.dialect.Placeholder(8)+", "+h.dialect.Placeholder(9)+", "+h.dialect.Placeholder(10)+", "+h.dialect.Placeholder(11)+", "+h.dialect.Placeholder(12)+")",
		domainID, tenantID, localPart, req.Email, displayName, argon2Hash, quotaMB,
		sendPerHr, recvPerHr, req.ClassID, time.Now().UTC(), time.Now().UTC(),
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
	var tenantID int64
	err = sqlDB.QueryRow("SELECT email, tenant_id FROM coremail_mailboxes WHERE id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).Scan(&email, &tenantID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}

	argon2Hash, err := hashPasswordArgon2id(req.Password)
	if err != nil {
		h.logger.Error("failed to hash password", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "password update failed"})
	}

	_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET password_hash = "+h.dialect.Placeholder(1)+", updated_at = "+h.dialect.Placeholder(2)+" WHERE id = "+h.dialect.Placeholder(3)+" AND tenant_id = "+h.dialect.Placeholder(4), argon2Hash, time.Now().UTC(), id, tenantID)
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
	var tenantID int64
	err = sqlDB.QueryRow("SELECT email, is_admin, tenant_id FROM coremail_mailboxes WHERE id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).Scan(&email, &isAdmin, &tenantID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}

	if isAdmin == 1 && req.Status != "active" {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "cannot disable admin mailbox"})
	}

	_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET status = "+h.dialect.Placeholder(1)+", updated_at = "+h.dialect.Placeholder(2)+" WHERE id = "+h.dialect.Placeholder(3)+" AND tenant_id = "+h.dialect.Placeholder(4), req.Status, time.Now().UTC(), id, tenantID)
	if err != nil {
		h.logger.Error("failed to update mailbox status", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "status update failed"})
	}

	h.writeAuditLog(c, "mailbox.status_update", fmt.Sprintf("mailbox_id:%d|email:%s|status:%s", id, email, req.Status))
	return c.JSON(fiber.Map{"result": "updated", "email": email, "status": req.Status})
}

// UpdateMailboxQuota updates the storage quota for a CoreMail mailbox.
// Request: {"quota_mb": 2048}. Returns the updated quota.
func (h *Handler) UpdateMailboxQuota(c fiber.Ctx) error {
	idStr := c.Params("id")
	var id uint
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}

	var req struct {
		QuotaMB int64 `json:"quota_mb"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.QuotaMB < 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "quota_mb must be >= 0"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var email string
	var tenantID int64
	err = sqlDB.QueryRow("SELECT email, tenant_id FROM coremail_mailboxes WHERE id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).Scan(&email, &tenantID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}

	_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET quota_mb = "+h.dialect.Placeholder(1)+", updated_at = "+h.dialect.Placeholder(2)+" WHERE id = "+h.dialect.Placeholder(3)+" AND tenant_id = "+h.dialect.Placeholder(4), req.QuotaMB, time.Now().UTC(), id, tenantID)
	if err != nil {
		h.logger.Error("failed to update mailbox quota", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "quota update failed"})
	}

	h.writeAuditLog(c, "mailbox.quota_update", fmt.Sprintf("mailbox_id:%d|email:%s|quota_mb:%d", id, email, req.QuotaMB))
	return c.JSON(fiber.Map{"result": "updated", "email": email, "quota_mb": req.QuotaMB})
}

// UpdateMailboxProtocols updates per-protocol access flags for a mailbox.
func (h *Handler) UpdateMailboxProtocols(c fiber.Ctx) error {
	idStr := c.Params("id")
	var id uint
	fmt.Sscanf(idStr, "%d", &id)
	if id == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid mailbox id"})
	}

	var req struct {
		AllowSMTP    *bool `json:"allow_smtp"`
		AllowIMAP    *bool `json:"allow_imap"`
		AllowPOP3    *bool `json:"allow_pop3"`
		AllowJMAP    *bool `json:"allow_jmap"`
		AllowWebmail *bool `json:"allow_webmail"`
	}
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid request"})
	}
	if req.AllowSMTP == nil && req.AllowIMAP == nil && req.AllowPOP3 == nil && req.AllowJMAP == nil && req.AllowWebmail == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "at least one protocol flag required"})
	}

	sqlDB, err := h.db.DB()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	var email string
	var tenantID int64
	err = sqlDB.QueryRow("SELECT email, tenant_id FROM coremail_mailboxes WHERE id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).Scan(&email, &tenantID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}

	now := time.Now().UTC()
	var sets []string
	var args []any
	if req.AllowSMTP != nil {
		sets = append(sets, "allow_smtp = "+h.dialect.Placeholder(len(args)+1))
		args = append(args, boolToInt(*req.AllowSMTP))
	}
	if req.AllowIMAP != nil {
		sets = append(sets, "allow_imap = "+h.dialect.Placeholder(len(args)+1))
		args = append(args, boolToInt(*req.AllowIMAP))
	}
	if req.AllowPOP3 != nil {
		sets = append(sets, "allow_pop3 = "+h.dialect.Placeholder(len(args)+1))
		args = append(args, boolToInt(*req.AllowPOP3))
	}
	if req.AllowJMAP != nil {
		sets = append(sets, "allow_jmap = "+h.dialect.Placeholder(len(args)+1))
		args = append(args, boolToInt(*req.AllowJMAP))
	}
	if req.AllowWebmail != nil {
		sets = append(sets, "allow_webmail = "+h.dialect.Placeholder(len(args)+1))
		args = append(args, boolToInt(*req.AllowWebmail))
	}
	sets = append(sets, "updated_at = "+h.dialect.Placeholder(len(args)+1))
	args = append(args, now, id, tenantID)

	_, err = sqlDB.Exec(fmt.Sprintf("UPDATE coremail_mailboxes SET %s WHERE id = "+h.dialect.Placeholder(len(args)-1)+" AND tenant_id = "+h.dialect.Placeholder(len(args)), strings.Join(sets, ", ")), args...)
	if err != nil {
		h.logger.Error("failed to update mailbox protocols", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "protocol update failed"})
	}

	h.writeAuditLog(c, "mailbox.protocols_update", fmt.Sprintf("mailbox_id:%d|email:%s", id, email))
	// Return updated flags
	var smtp, imap, pop3, jmap, wm bool
	sqlDB.QueryRow("SELECT allow_smtp, allow_imap, allow_pop3, allow_jmap, allow_webmail FROM coremail_mailboxes WHERE id = "+h.dialect.Placeholder(1), id).Scan(&smtp, &imap, &pop3, &jmap, &wm)
	return c.JSON(fiber.Map{"result": "updated", "protocols": fiber.Map{
		"allow_smtp": smtp, "allow_imap": imap, "allow_pop3": pop3,
		"allow_jmap": jmap, "allow_webmail": wm,
	}})
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
	crossTenant := isSuperRole(c)
	callerTenant := h.scopedTenantID(c)

	for _, id := range req.MailboxIDs {
		if id == 0 {
			skipped++
			continue
		}
		// RACE-SAFE UPDATE: the safety predicate is enforced atomically
		// at the database write site. We never trust a pre-check result;
		// if the row was soft-deleted, flipped to admin, no longer
		// matches the requested status, or (unless the caller is a
		// super admin) belongs to a different tenant, RowsAffected will
		// be 0 and we count it as skipped. Admin mailboxes can never be
		// touched by this bulk endpoint.
		query := "UPDATE coremail_mailboxes SET status = " + h.dialect.Placeholder(1) + ", updated_at = " + h.dialect.Placeholder(2) + " WHERE id = " + h.dialect.Placeholder(3) + " AND deleted_at IS NULL AND is_admin = " + h.dialect.FalseLiteral()
		args := []any{req.Status, now, id}
		if !crossTenant {
			query += " AND tenant_id = " + h.dialect.Placeholder(len(args)+1)
			args = append(args, callerTenant)
		}
		res, err := sqlDB.Exec(query, args...)
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
	crossTenant := isSuperRole(c)
	callerTenant := h.scopedTenantID(c)

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
		// did not exist, was soft-deleted, already matched the new
		// status, or (unless the caller is a super admin) belongs to a
		// different tenant — in all cases it is correctly NOT counted
		// as updated.
		query := "UPDATE coremail_domains SET status = " + h.dialect.Placeholder(1) + ", updated_at = " + h.dialect.Placeholder(2) + " WHERE name = " + h.dialect.Placeholder(3) + " AND deleted_at IS NULL"
		args := []any{req.Status, now, name}
		if !crossTenant {
			query += " AND tenant_id = " + h.dialect.Placeholder(len(args)+1)
			args = append(args, callerTenant)
		}
		res, err := sqlDB.Exec(query, args...)
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
	var tenantID int64
	err = sqlDB.QueryRow("SELECT email, is_admin, tenant_id FROM coremail_mailboxes WHERE id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).Scan(&email, &isAdmin, &tenantID)
	if err == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}
	if !h.callerOwnsTenant(c, tenantID) {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "mailbox not found"})
	}

	if isAdmin == 1 {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "cannot delete admin mailbox"})
	}

	now := time.Now().UTC()
	_, err = sqlDB.Exec("UPDATE coremail_mailboxes SET deleted_at = "+h.dialect.Placeholder(1)+", updated_at = "+h.dialect.Placeholder(2)+" WHERE id = "+h.dialect.Placeholder(3)+" AND tenant_id = "+h.dialect.Placeholder(4), now, now, id, tenantID)
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
	res, execErr := sqlDB.Exec("UPDATE coremail_queue SET deleted_at = "+h.dialect.Placeholder(1)+", updated_at = "+h.dialect.Placeholder(2)+" WHERE id = "+h.dialect.Placeholder(3)+" AND deleted_at IS NULL", now, now, id)
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
	scanErr := sqlDB.QueryRow("SELECT attempt_count FROM coremail_queue WHERE id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).Scan(&currentAttempts)
	if scanErr == sql.ErrNoRows {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "queue item not found"})
	}
	if scanErr != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
	}

	now := time.Now().UTC()
	res, execErr := sqlDB.Exec(
		"UPDATE coremail_queue SET status = 'pending', lease_owner = '', lease_expires_at = NULL, next_attempt_at = "+h.dialect.Placeholder(1)+", updated_at = "+h.dialect.Placeholder(2)+" WHERE id = "+h.dialect.Placeholder(3)+" AND deleted_at IS NULL",
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

// AdminQueueSummary returns aggregated queue metrics for the admin dashboard.
// Admin-only by route registration. Read-only.
func (h *Handler) AdminQueueSummary(c fiber.Ctx) error {
	if h.queueEngine == nil || h.queueEngine.Repo == nil {
		return c.JSON(fiber.Map{"error": "queue engine not available", "metrics": nil})
	}
	metrics, err := h.queueEngine.Repo.Metrics(c.Context(), nil, nil)
	if err != nil {
		return c.JSON(fiber.Map{"error": "metrics unavailable", "metrics": nil})
	}
	return c.JSON(fiber.Map{"metrics": metrics})
}

// GetAdminQueueEntry returns a single queue entry by ID with safe
// diagnostic fields. Admin-only by route registration. Read-only.
func (h *Handler) GetAdminQueueEntry(c fiber.Ctx) error {
	idStr := c.Params("id")
	id, parseErr := parseUint(idStr)
	if parseErr != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid queue id"})
	}

	var entry *queue.QueueEntry

	// Prefer the queue repository when available (QUEUE-OPERATIONS-2E).
	// Fall back to raw SQL for backward compatibility.
	if h.queueEngine != nil && h.queueEngine.Repo != nil {
		e, err := h.queueEngine.Repo.Get(c.Context(), uint(id), nil)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
		}
		if e == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "queue entry not found"})
		}
		entry = e
	} else {
		// Raw SQL fallback — safe fields only, parameterized query.
		sqlDB, err := h.db.DB()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "database error"})
		}
		e := &queue.QueueEntry{}
		var direction, statusStr, deliveryMode string
		var tlsUsed int
		err = sqlDB.QueryRowContext(c.Context(),
			"SELECT "+queueSafeCols+" FROM coremail_queue WHERE id="+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).
			Scan(&e.ID, &e.MessageID, &e.FromAddress, &e.ToAddress, &e.RecipientDomain,
				&statusStr, &e.AttemptCount, &e.MaxAttempts, &e.NextAttemptAt, &e.LastAttemptAt,
				&e.LastError, &deliveryMode, &e.RemoteHost, &e.RemoteIP, &tlsUsed,
				&e.LastStatusCode, &e.LastEnhancedCode,
				&e.CreatedAt, &e.UpdatedAt)
		if err != nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "queue entry not found"})
		}
		e.Status = queue.QueueStatus(statusStr)
		e.Direction = queue.Direction(direction)
		e.DeliveryMode = queue.DeliveryMode(deliveryMode)
		e.TLSUsed = tlsUsed == 1
		entry = e
	}

	// Map to safe DTO — never return QueueEntry directly.
	dto := adminQueueEntryDTO{
		ID:             int64(entry.ID),
		Status:         string(entry.Status),
		From:           entry.FromAddress,
		To:             entry.ToAddress,
		Attempts:       entry.AttemptCount,
		MaxAttempts:    entry.MaxAttempts,
		DeliveryMode:   string(entry.DeliveryMode),
		LastStatusCode: entry.LastStatusCode,
		LastError:      sanitizeQueueDiagnostic(entry.LastError),
	}
	if entry.NextAttemptAt != nil {
		dto.NextAttemptAt = entry.NextAttemptAt.UTC().Format(time.RFC3339)
	}
	if entry.LastAttemptAt != nil {
		dto.LastAttemptAt = entry.LastAttemptAt.UTC().Format(time.RFC3339)
	}
	if !entry.CreatedAt.IsZero() {
		dto.CreatedAt = entry.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !entry.UpdatedAt.IsZero() {
		dto.UpdatedAt = entry.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if entry.RecipientDomain != "" {
		dto.RecipientDomain = entry.RecipientDomain
	}
	if entry.RemoteHost != "" {
		dto.RemoteHost = entry.RemoteHost
	}
	if entry.LastEnhancedCode != "" {
		dto.LastEnhancedCode = entry.LastEnhancedCode
	}
	return c.JSON(dto)
}

// adminQueueEntryDTO is the safe admin-facing queue entry view.
// It explicitly excludes internal/operational fields such as
// tenant_id, domain_id, mailbox_id, priority, lease fields,
// deleted_at, and any future raw message body.
type adminQueueEntryDTO struct {
	ID               int64  `json:"id"`
	Status           string `json:"status"`
	From             string `json:"from"`
	To               string `json:"to"`
	RecipientDomain  string `json:"recipient_domain,omitempty"`
	Attempts         int    `json:"attempts"`
	MaxAttempts      int    `json:"max_attempts,omitempty"`
	DeliveryMode     string `json:"delivery_mode,omitempty"`
	RemoteHost       string `json:"remote_host,omitempty"`
	LastStatusCode   int    `json:"last_status_code,omitempty"`
	LastEnhancedCode string `json:"last_enhanced_code,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	NextAttemptAt    string `json:"next_attempt_at,omitempty"`
	LastAttemptAt    string `json:"last_attempt_at,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

// sanitizeQueueDiagnostic sanitizes a queue diagnostic/error string
// for safe admin display. Cuts length, removes control characters,
// and collapses excessive whitespace.
func sanitizeQueueDiagnostic(s string) string {
	if s == "" {
		return ""
	}
	// Cap length.
	const maxLen = 500
	if len(s) > maxLen {
		s = s[:maxLen]
	}
	// Remove control characters except newline, tab, carriage return.
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\t' || c == '\r' {
			b.WriteByte(c)
		} else if c >= 32 && c <= 126 {
			b.WriteByte(c)
		} else if c > 126 {
			// Keep UTF-8 multi-byte characters (admin may need
			// international SMTP responses). We skip the byte and
			// let the encoding continue; this is a simple ASCII
			// pass-through filter for control chars only.
			b.WriteByte(c)
		}
		// Control characters (0-31 except whitespace) are dropped.
	}
	// Trim surrounding whitespace.
	return strings.TrimSpace(b.String())
}

// queueSafeCols is the column list for the safe admin queue detail
// query. It excludes internal/operational fields.
const queueSafeCols = "id, message_id, from_address, to_address, recipient_domain, status, attempt_count, max_attempts, next_attempt_at, last_attempt_at, last_error, delivery_mode, remote_host, remote_ip, tls_used, last_status_code, last_enhanced_code, created_at, updated_at"

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

// GetLicense returns the current license status.
//
// The response shape is the structured StatusReport from
// internal/license.Validator.Status(). The previous release
// returned a flat map with "status: no license" /
// "tier: community", which conflated several distinct states
// (no public key, no license, expired, invalid). The new
// response separates:
//
//   - status_public_key_missing — public key not configured
//   - status_license_missing     — no license row in DB
//   - status_expired             — license row present, past expiry
//   - status_invalid             — license row present, signature bad
//   - status_valid               — license row present, signed, not expired
//   - status_offline             — runtime cannot reach license authority
//
// The endpoint NEVER returns the license key, the key hash, the
// public key, or any other secret.
func (h *Handler) GetLicense(c fiber.Ctx) error {
	if h.licenseValidator == nil {
		return c.JSON(license.StatusReport{
			Status: license.StatusOffline,
			Reason: "license validator not wired in this build",
		})
	}
	return c.JSON(h.licenseValidator.Status())
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
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE deleted_at IS NULL AND is_admin = " + h.dialect.TrueLiteral()).Scan(&mbAdmin)

		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL").Scan(&qTotal)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'pending'").Scan(&qPending)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'deferred'").Scan(&qDeferred)
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_queue WHERE deleted_at IS NULL AND status = 'dead_letter'").Scan(&qFailed)
	}

	since := time.Now().UTC().Add(-24 * time.Hour)
	if err == nil {
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_audit WHERE timestamp >= "+h.dialect.Placeholder(1), since).Scan(&auditRecent)
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
		sqlDB.QueryRow("SELECT COUNT(*) FROM coremail_mailboxes WHERE domain_id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", d.ID).Scan(&mbCount)
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
	var tenantID int64
	err = sqlDB.QueryRow("SELECT email, tenant_id FROM coremail_mailboxes WHERE id = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", id).Scan(&email, &tenantID)
	if err != nil {
		return c.JSON([]struct{}{})
	}
	if !h.callerOwnsTenant(c, tenantID) {
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
	sqlDB, err := h.db.DB()
	if err == nil {
		var tenantID int64
		lookupErr := sqlDB.QueryRow("SELECT tenant_id FROM coremail_domains WHERE name = "+h.dialect.Placeholder(1)+" AND deleted_at IS NULL", name).Scan(&tenantID)
		if lookupErr == nil && !h.callerOwnsTenant(c, tenantID) {
			return c.JSON([]struct{}{})
		}
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
		Version:          wm.Version,
		Commit:           config.GetBuildCommit(),
		BuildTime:        wm.BuildTime,
		GoVersion:        wm.GoVersion,
		Arch:             wm.Arch,
		StartedAt:        h.processStartedAt,
		DataPath:         h.dataPathForTelemetry(),
		DBPing:           h.dbPingErrorForTelemetry,
		QueueCounts:      h.queueCountsForTelemetry(c.Context()),
		License:          h.licensePostureForTelemetry(c.Context()),
		ListenerSnapshot: listenerSnapshot,
		SMHTTPPort:       smtpPort,
		IMAPPort:         imapPort,
		POP3Port:         pop3Port,
		JMAPPort:         jmapPort,
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
