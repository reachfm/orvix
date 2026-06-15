package api

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/metrics"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/webmailmgmt"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Router struct {
	app          *fiber.App
	auth         *auth.Authenticator
	csrf         *auth.CSRFManager
	apikeys      *auth.APIKeyManager
	redisLimiter *auth.RedisRateLimiter
	logger       *zap.Logger
	cfg          *config.Config
	h            *handlers.Handler
}

func NewRouter(cfg *config.Config, authenticator *auth.Authenticator, logger *zap.Logger,
	db *gorm.DB, registry *modules.Registry,
	ff *license.FeatureFlags, redisClient *redis.Client) *Router {
	app := fiber.New(fiber.Config{
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
		BodyLimit:    cfg.Server.BodyLimit,
		AppName:      "Orvix",
	})

	apikeyMgr := auth.NewAPIKeyManager(db, logger)
	var rateLimiter *auth.RedisRateLimiter
	if redisClient != nil {
		rateLimiter = auth.NewRedisRateLimiter(redisClient, logger)
	}

	router := &Router{
		app:          app,
		auth:         authenticator,
		csrf:         auth.NewCSRFManager(db, logger, false),
		apikeys:      apikeyMgr,
		redisLimiter: rateLimiter,
		logger:       logger,
		cfg:          cfg,
		h:            handlers.NewHandler(db, authenticator, apikeyMgr, logger, cfg, registry, ff, rateLimiter),
	}

	// Wire webmail management service.
	if sqlDB, err := db.DB(); err == nil {
		eng := coremail.NewEngine(coremail.EngineConfig{DB: sqlDB, AuthCfg: coremail.DefaultAuthConfig()})
		ws := webmailmgmt.NewService(eng, sqlDB)
		router.h.SetWebmailService(ws)
	} else {
		logger.Warn("webmail service not available: failed to get sql.DB", zap.Error(err))
	}

	router.setupMiddleware()
	router.setupRoutes()
	router.setupAdminUI()

	return router
}

func (r *Router) App() *fiber.App { return r.app }

func (r *Router) setupMiddleware() {
	r.app.Use(recover.New())
	origins := r.cfg.Server.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"http://localhost:3000", "http://localhost:3001"}
	}
	r.app.Use(cors.New(cors.Config{
		AllowOrigins:     origins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-CSRF-Token"},
		AllowCredentials: true,
	}))
	r.app.Use(securityHeaders())
	if r.redisLimiter != nil {
		r.app.Use(r.redisLimiter.Middleware())
	} else {
		r.app.Use(limiter.New(limiter.Config{Max: 100, Expiration: 60 * 1000}))
	}
	if r.cfg.Metrics.Enabled {
		r.app.Get(r.cfg.Metrics.Path, metrics.Handler())
	}
}

func (r *Router) setupRoutes() {
	api := r.app.Group("/api/v1")
	api.Get("/health", r.h.Health)

	loginGroup := api.Group("/auth")
	if r.redisLimiter != nil {
		loginGroup.Post("/login", r.redisLimiter.LoginMiddleware(), r.h.Login)
	} else {
		loginGroup.Post("/login", limiter.New(limiter.Config{Max: 5, Expiration: 15 * 60 * 1000}), r.h.Login)
	}
	loginGroup.Post("/refresh", r.h.Refresh)
	r.app.Post("/admin/login", r.h.Login)

	protected := api.Group("", r.apikeys.Middleware(), r.auth.Middleware())
	protected.Get("/me", r.h.Me)

	protected.Get("/csrf-token", func(c fiber.Ctx) error {
		userID, _ := c.Locals("user_id").(uint)
		token, err := r.csrf.GenerateToken(c, userID)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": "csrf token generation failed"})
		}
		return c.JSON(fiber.Map{"csrf_token": token})
	})

	authCSRF := protected.Group("", r.csrf.Middleware())
	authCSRF.Post("/auth/logout", r.h.Logout)
	authCSRF.Post("/auth/logout-all", r.h.LogoutAll)
	authCSRF.Post("/auth/change-password", r.h.ChangePassword)

	admin := protected.Group("", auth.RequireAnyRole(auth.RoleAdmin, auth.RoleSuperAdmin))
	admin.Get("/domains", r.h.ListDomains)
	admin.Get("/users", r.h.ListUsers)
	admin.Get("/mailboxes", r.h.ListUsers)
	// CSV exports (admin-only, GET — no CSRF required). Registered before
	// the parameterized :id / :name routes so the literal /export segment
	// wins over /mailboxes/:id and /domains/:name.
	admin.Get("/mailboxes/export", r.h.ExportMailboxesCSV)
	admin.Get("/domains/export", r.h.ExportDomainsCSV)
	admin.Get("/domains/:name/audit", r.h.GetDomainAudit)
	admin.Get("/domains/:name", r.h.GetDomain)
	admin.Get("/mailboxes/:id/audit", r.h.GetMailboxAudit)
	admin.Get("/mailboxes/:id", r.h.GetMailbox)
	admin.Get("/queue", r.h.ListQueue)
	admin.Get("/backups", r.h.ListBackups)
	admin.Get("/backups/schedule", r.h.GetBackupSchedule)
	admin.Get("/backups/metrics", r.h.GetBackupMetrics)
	admin.Get("/backups/health", r.h.GetBackupHealth)
	admin.Get("/backups/:id/download", r.h.DownloadBackup)
	admin.Get("/firewall/rules", r.h.ListFirewallRules)
	admin.Get("/firewall/logs", r.h.ListFirewallLogs)
	admin.Get("/modules", r.h.ListModules)
	admin.Get("/license", r.h.GetLicense)
	admin.Get("/audit/logs", r.h.ListAuditLogs)
	admin.Get("/feature-flags", r.h.ListFeatureFlags)
	admin.Get("/api-keys", r.h.ListAPIKeys)
	admin.Get("/admin/summary", r.h.AdminSummary)
	// Monitoring v1: read-only health + alert endpoints (admin role).
	admin.Get("/monitoring/health", r.h.GetMonitoringHealth)
	admin.Get("/monitoring/alerts", r.h.GetMonitoringAlerts)
	admin.Get("/monitoring/capacity", r.h.GetMonitoringCapacity)

	// Auto-Heal
	admin.Get("/heal/history", r.h.ListHealHistory)
	admin.Post("/heal/check/:name", r.h.RunHealCheck)

	// Guardian
	admin.Post("/guardian/analyze", r.h.AnalyzeEmail)
	admin.Get("/guardian/logs", r.h.ListGuardianLogs)

	// Smart Compose AI
	admin.Post("/compose/complete", r.h.ComposeComplete)
	admin.Post("/compose/stream", r.h.ComposeStream)

	// DNS Automation
	admin.Post("/dns/check/:domain", r.h.DNSCheck)
	admin.Post("/dns/wizard/:domain", r.h.DNSWizard)

	// Migration
	admin.Post("/migration/test", r.h.MigrationTest)
	admin.Post("/migration/start", r.h.MigrationStart)
	admin.Get("/migration/jobs", r.h.ListMigrationJobs)

	// Webmail Management
	admin.Get("/webmail/accounts", r.h.ListWebmailAccounts)
	admin.Get("/webmail/sessions", r.h.ListWebmailSessions)
	admin.Get("/webmail/activity/:mailboxId", r.h.GetWebmailLoginActivity)
	admin.Get("/webmail/storage/:mailboxId", r.h.GetWebmailStorageMetrics)

	// Provision API
	admin.Post("/provision/domain", r.h.ProvisionDomain)

	// Calendar
	admin.Get("/calendar/events", r.h.ListEvents)
	admin.Post("/calendar/events", r.h.CreateEvent)
	admin.Put("/calendar/events/:id", r.h.UpdateEvent)
	admin.Delete("/calendar/events/:id", r.h.DeleteEvent)

	// Contacts
	admin.Get("/contacts", r.h.ListContacts)
	admin.Post("/contacts", r.h.CreateContact)
	admin.Put("/contacts/:id", r.h.UpdateContact)
	admin.Delete("/contacts/:id", r.h.DeleteContact)

	// Tasks
	admin.Get("/tasks", r.h.ListTasks)
	admin.Post("/tasks", r.h.CreateTask)
	admin.Put("/tasks/:id", r.h.UpdateTask)
	admin.Patch("/tasks/:id/complete", r.h.CompleteTask)
	admin.Delete("/tasks/:id", r.h.DeleteTask)

	// Auto-Update
	admin.Get("/updates/check", r.h.CheckUpdates)
	admin.Get("/updates/changelog", r.h.GetChangelog)
	admin.Post("/updates/apply/:module", r.h.ApplyUpdate)

	// Email Intelligence
	admin.Get("/intelligence/stats", r.h.GetEmailStats)
	admin.Get("/intelligence/delivery", r.h.GetDeliveryReports)

	// Compliance & Legal Hold
	admin.Get("/compliance/legal-holds", r.h.ListLegalHolds)
	admin.Post("/compliance/legal-holds", r.h.CreateLegalHold)
	admin.Put("/compliance/legal-holds/:id", r.h.UpdateLegalHold)
	admin.Get("/compliance/policies", r.h.ListRetentionPolicies)
	admin.Post("/compliance/policies", r.h.CreateRetentionPolicy)

	// Collaboration
	admin.Get("/collaboration/mailboxes", r.h.ListSharedMailboxes)
	admin.Post("/collaboration/mailboxes", r.h.CreateSharedMailbox)

	men := admin.Group("", r.csrf.Middleware())
	men.Post("/domains", r.h.CreateDomain)
	men.Patch("/domains/:name/status", r.h.UpdateDomainStatus)
	men.Delete("/domains/:name", r.h.DeleteDomain)
	men.Post("/users", r.h.CreateUser)
	men.Post("/mailboxes", r.h.CreateMailbox)
	men.Patch("/mailboxes/:id/password", r.h.UpdateMailboxPassword)
	men.Patch("/mailboxes/:id/status", r.h.UpdateMailboxStatus)
	// Bulk status operations (CSRF-protected).
	men.Post("/mailboxes/bulk/status", r.h.BulkMailboxStatus)
	men.Post("/domains/bulk/status", r.h.BulkDomainStatus)
	men.Delete("/mailboxes/:id", r.h.DeleteMailbox)
	men.Delete("/users/:id", r.h.DeleteUser)
	men.Delete("/queue/:id", r.h.DeleteQueue)
	men.Post("/queue/:id/retry", r.h.RetryQueue)
	men.Post("/backups", r.h.CreateBackup)
	men.Post("/backups/schedule", r.h.SetBackupSchedule)
	men.Post("/backups/retention", r.h.RunBackupRetention)
	men.Delete("/backups/:id", r.h.DeleteBackup)
	// Monitoring v1: resolve an alert (CSRF-protected, admin role).
	men.Post("/monitoring/alerts/:id/resolve", r.h.PostMonitoringAlertResolve)
	men.Post("/firewall/rules", r.h.CreateFirewallRule)
	men.Post("/license/validate", r.h.ValidateLicense)
	men.Put("/feature-flags/:id", r.h.UpdateFeatureFlag)
	men.Post("/api-keys", r.h.CreateAPIKey)
	men.Delete("/api-keys/:id", r.h.DeleteAPIKey)
	men.Put("/compliance/legal-holds/:id", r.h.UpdateLegalHold)
	men.Delete("/compliance/legal-holds/:id", r.h.DeleteLegalHold)
	men.Post("/compliance/policies", r.h.CreateRetentionPolicy)
	men.Put("/compliance/policies/:id", r.h.UpdateRetentionPolicy)
	men.Delete("/compliance/policies/:id", r.h.DeleteRetentionPolicy)

	// Webmail Management — CSRF-protected write routes
	men.Post("/webmail/sessions/:id/revoke", r.h.RevokeWebmailSession)
	men.Post("/webmail/sessions/revoke-all", r.h.RevokeAllWebmailSessions)
	men.Post("/webmail/controls/force-logout/:mailboxId", r.h.ForceLogoutWebmail)
	men.Post("/webmail/controls/unlock/:mailboxId", r.h.UnlockWebmailMailbox)
	men.Post("/webmail/controls/reset-preferences/:mailboxId", r.h.ResetWebmailPreferences)
	men.Post("/webmail/controls/clear-counters/:mailboxId", r.h.ClearFailedLoginCounters)
}

func (r *Router) setupAdminUI() {
	adminDir := r.cfg.Server.AdminUIDir
	if adminDir == "" {
		adminDir = "/usr/share/orvix/admin"
	}
	r.app.Get("/", func(c fiber.Ctx) error {
		return c.Redirect().To("/admin")
	})
	r.serveSPA("/admin", adminDir)

	webmailDir := r.cfg.Server.WebmailUIDir
	if webmailDir == "" {
		webmailDir = "/usr/share/orvix/webmail"
	}
	r.serveSPA("/webmail", webmailDir)
}

func (r *Router) serveSPA(prefix, dir string) {
	indexPath := filepath.Join(dir, "index.html")
	r.app.Get(prefix, func(c fiber.Ctx) error {
		return c.SendFile(indexPath)
	})
	r.app.Get(prefix+"/*", func(c fiber.Ctx) error {
		requestPath := strings.TrimPrefix(c.Params("*"), "/")
		if requestPath == "" {
			return c.SendFile(indexPath)
		}
		clean := filepath.Clean(filepath.FromSlash(requestPath))
		if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		target := filepath.Join(dir, clean)
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			return c.SendFile(target)
		}
		return c.SendFile(indexPath)
	})
}

func securityHeaders() fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Set("X-Content-Type-Options", "nosniff")
		c.Set("X-Frame-Options", "DENY")
		c.Set("X-XSS-Protection", "1; mode=block")
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: https:; font-src 'self'; connect-src 'self' https:; frame-src 'none'; object-src 'none'; base-uri 'self'; form-action 'self'")
		if c.Protocol() == "https" {
			c.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		return c.Next()
	}
}
