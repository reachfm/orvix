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
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/metrics"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/updater"
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
		// Trust the Caddy reverse proxy in front of us for
		// X-Forwarded-* headers. The TrustedProxies list is
		// populated by the installer with 127.0.0.1 / ::1
		// (the loopback address Caddy listens on). Without
		// this, c.IP() returns the loopback address for every
		// request and the rate limiter, audit log, and login
		// rate-limit gate see the wrong value.
		ProxyHeader: fiber.HeaderXForwardedFor,
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies:  cfg.Server.TrustedProxies,
			Loopback: true,
		},
	})

	apikeyMgr := auth.NewAPIKeyManager(db, logger)
	var rateLimiter *auth.RedisRateLimiter
	if redisClient != nil {
		rateLimiter = auth.NewRedisRateLimiter(redisClient, logger)
	}

	router := &Router{
		app:          app,
		auth:         authenticator,
		csrf:         auth.NewCSRFManager(db, logger, cfg.Server.TLSAuto),
		apikeys:      apikeyMgr,
		redisLimiter: rateLimiter,
		logger:       logger,
		cfg:          cfg,
		h:            handlers.NewHandler(db, authenticator, apikeyMgr, logger, cfg, registry, ff, rateLimiter),
	}

	// Propagate the cookie Domain to the CSRF manager. The
	// installer writes cfg.Auth.CookieDomain (".parent.com")
	// for production so the csrf_token cookie is sent to
	// admin.<parent> and webmail.<parent> alike. In dev /
	// docker the field is empty and the browser scopes the
	// cookie to the response Host.
	router.csrf.SetCookieDomain(cfg.Auth.CookieDomain)

	// Wire webmail management service.
	if sqlDB, err := db.DB(); err == nil {
		eng := coremail.NewEngine(coremail.EngineConfig{DB: sqlDB, AuthCfg: coremail.DefaultAuthConfig()})
		ws := webmailmgmt.NewService(eng, sqlDB)
		router.h.SetWebmailService(ws)
	} else {
		logger.Warn("webmail service not available: failed to get sql.DB", zap.Error(err))
	}

	// Wire MailStore from the coremail runtime module. The
	// runtime creates the MailStore during initCore; the
	// webmail user-facing endpoints (GET /api/v1/webmail/
	// ...) read messages and folders directly from this
	// store, not from /api/v1/queue or any admin-side
	// endpoint. If the runtime module is not registered
	// (test mode, custom builds) the webmail endpoints
	// return 503 instead of crashing.
	if mod, ok := registry.Get("coremail-runtime"); ok {
		if msProvider, ok := mod.(interface {
			MailStore() *storage.MailStore
		}); ok {
			if ms := msProvider.MailStore(); ms != nil {
				router.h.SetMailStore(ms)
				logger.Info("mailstore wired for webmail user endpoints")
			}
		}
		// Wire the delivery QueueEngine from the same
		// runtime module. The webmail user-facing Send
		// endpoint enqueues outbound messages through
		// this engine so they are picked up by the same
		// delivery worker the SMTP receiver uses — no
		// separate queue, no SMTP redesign.
		if qeProvider, ok := mod.(interface {
			QueueEngine() *queue.QueueEngine
		}); ok {
			if qe := qeProvider.QueueEngine(); qe != nil {
				router.h.SetQueueEngine(qe)
				logger.Info("queue engine wired for webmail send")
			}
		}
	}

	// Wire Update Management v1 service. The service holds the
	// process-wide single-flight mutex; sharing it across all
	// requests against this router is what enforces "one update
	// job at a time" even under concurrent load. The web process
	// NEVER exec's the update script directly; it drives the
	// root-owned systemd oneshot helper unit via
	// `systemctl start orvix-update.service`. The helper unit's
	// ExecStart is the only path that ever reaches exec.
	if sqlDB, err := db.DB(); err == nil {
		updSvc := updater.NewRuntimeService(sqlDB, updater.Config{
			WorkspaceRoot:    updateWorkspaceRoot(cfg),
			Channel:          updateChannel(cfg),
			BackupDir:        updateBackupDir(cfg),
			Logger:           logger,
			UpdateHelperUnit: updater.DefaultUpdateHelperUnit,
		}).WithCheckURL(cfg.Update.CheckURL)
		router.h.SetUpdateService(updSvc)
	} else {
		logger.Warn("update service not available: failed to get sql.DB", zap.Error(err))
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

	// Webmail authentication (public — no auth middleware).
	//
	// /api/v1/webmail/login is the form submission. The
	// session probe (/api/v1/webmail/session) is on the
	// protected group below so the auth middleware
	// rejects missing/invalid cookies with 401 before
	// the handler runs — the gate uses that 401 as the
	// "show the login form" signal.
	webmailLoginGroup := api.Group("/webmail")
	if r.redisLimiter != nil {
		webmailLoginGroup.Post("/login", r.redisLimiter.LoginMiddleware(), r.h.WebmailLogin)
	} else {
		webmailLoginGroup.Post("/login", limiter.New(limiter.Config{Max: 5, Expiration: 15 * 60 * 1000}), r.h.WebmailLogin)
	}

	protected := api.Group("", r.apikeys.Middleware(), r.auth.Middleware())
	protected.Get("/me", r.h.Me)

	// User-facing webmail endpoints. Mounted on the
	// protected group so the auth middleware rejects
	// unauthenticated requests with 401 BEFORE any
	// mailbox lookup runs. The handlers themselves
	// resolve the current user to their mailbox and
	// read from the live MailStore — there is no
	// fallback to /api/v1/queue or any admin-side
	// data path.
	//
	// /webmail/session is also on the protected group:
	// the auth gate uses the 401 from the auth
	// middleware as the "show the login form" signal,
	// and a 200 with authenticated:true as the "reveal
	// the SPA" signal.
	protected.Get("/webmail/session", r.h.WebmailSession)
	protected.Get("/webmail/me", r.h.WebmailMe)
	protected.Get("/webmail/folders", r.h.WebmailFolders)
	protected.Get("/webmail/messages", r.h.WebmailMessages)
	protected.Get("/webmail/messages/:id", r.h.WebmailMessage)
	protected.Patch("/webmail/messages/:id", r.h.WebmailUpdateMessage)
	protected.Post("/webmail/messages/:id/archive", r.h.WebmailArchive)
	protected.Post("/webmail/messages/:id/delete", r.h.WebmailDelete)
	protected.Post("/webmail/folders/:id/read-all", r.h.WebmailMarkFolderRead)
	protected.Post("/webmail/send", r.h.WebmailSend)
	// Drafts — minimal CRUD. Drafts are Message rows
	// with Draft=true in the Drafts system folder; no
	// separate draft table, no schema change.
	protected.Get("/webmail/drafts", r.h.WebmailListDrafts)
	protected.Post("/webmail/drafts", r.h.WebmailSaveDraft)
	protected.Get("/webmail/drafts/:id", r.h.WebmailGetDraft)
	protected.Put("/webmail/drafts/:id", r.h.WebmailSaveDraft)
	protected.Delete("/webmail/drafts/:id", r.h.WebmailDeleteDraft)

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
	// Webmail logout. Mounted inside authCSRF so a CSRF
	// token is required to clear the cookies — the
	// session is the same one the admin panel uses, so
	// this endpoint also kills the admin session if the
	// caller is the same browser.
	authCSRF.Post("/webmail/logout", r.h.WebmailLogout)

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

	// Auto-Update (legacy /updates/* routes — kept for backward compat)
	admin.Get("/updates/check", r.h.CheckUpdates)
	admin.Get("/updates/changelog", r.h.GetChangelog)
	admin.Post("/updates/apply/:module", r.h.ApplyUpdate)

	// Update Management v1: read-only endpoints (admin role).
	admin.Get("/update/status", r.h.GetUpdateStatus)
	admin.Get("/update/history", r.h.GetUpdateHistory)
	admin.Get("/update/preflight", r.h.GetUpdatePreflight)
	admin.Get("/update/check", r.h.GetUpdateCheck)

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
	// Update Management v1: trigger a check or a runtime update
	// (CSRF-protected, admin role). The actual script execution is
	// single-flight: a second concurrent call returns 409 Conflict.
	men.Post("/update/check", r.h.PostUpdateCheck)
	men.Post("/update/run", r.h.PostUpdateRun)
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
	// Serve webmail assets at /assets/* so the SPA, when
	// accessed from admin.<domain>/webmail, can request
	// /assets/webmail.js instead of /webmail/assets/... The
	// dedicated webmail.<domain> vhost rewrites /assets/*
	// to /webmail/assets/* at the Caddy layer; this route
	// ensures the Go backend also responds for direct
	// requests (admin hostname, localhost, dev mode).
	r.app.Get("/assets/*", func(c fiber.Ctx) error {
		requestPath := strings.TrimPrefix(c.Params("*"), "/")
		if requestPath == "" || strings.Contains(requestPath, "..") {
			return c.SendStatus(fiber.StatusBadRequest)
		}
		target := filepath.Join(webmailDir, "assets", requestPath)
		if info, err := os.Stat(target); err == nil && !info.IsDir() {
			return c.SendFile(target)
		}
		return c.SendStatus(fiber.StatusNotFound)
	})
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

// updateWorkspaceRoot returns the workspace root used to anchor
// the runtime update script. The updater detector prefers a live git
// checkout root, then the explicit config value, then /opt/orvix when
// the canonical runtime script exists there, then the process working
// directory. The result is never sent to clients.
func updateWorkspaceRoot(cfg *config.Config) string {
	configured := ""
	if cfg != nil && cfg.Update.WorkspaceRoot != "" {
		configured = cfg.Update.WorkspaceRoot
	}
	return updater.DetectWorkspaceRoot(configured)
}

// updateChannel returns the release channel from config. The spec
// mandates stable only; we expose a config knob for future-proofing
// but refuse non-stable values at the response boundary.
func updateChannel(cfg *config.Config) updater.Channel {
	if cfg == nil || cfg.Update.Channel == "" {
		return updater.ChannelStable
	}
	return updater.Channel(cfg.Update.Channel)
}

// updateBackupDir returns the operator-supplied backup directory,
// falling back to the legacy /var/lib/orvix/backups default. The
// result is the dir the preflight uses for the writability probe.
func updateBackupDir(cfg *config.Config) string {
	if cfg != nil && cfg.Backup.Dir != "" {
		return cfg.Backup.Dir
	}
	return "/var/lib/orvix/backups"
}
