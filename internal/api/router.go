package api

import (
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/adaptor"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/orvixemail/orvix/internal/adapters"
	"github.com/orvixemail/orvix/internal/api/handlers"
	admin_handlers "github.com/orvixemail/orvix/internal/api/handlers/admin"
	"github.com/orvixemail/orvix/internal/api/middleware"
	"github.com/orvixemail/orvix/internal/auth"
	"github.com/orvixemail/orvix/internal/config"
	"github.com/orvixemail/orvix/internal/features"
	"github.com/orvixemail/orvix/internal/license"
	"github.com/orvixemail/orvix/internal/metrics"
	"github.com/orvixemail/orvix/internal/security"
	"github.com/orvixemail/orvix/internal/stalwart"
	"github.com/orvixemail/orvix/internal/updater"
	"github.com/orvixemail/orvix/web"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type RouterConfig struct {
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

func serveFrontend(root fs.FS, embedPath string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		requestPath := c.Path()

		var appPrefix string
		switch {
		case strings.HasPrefix(requestPath, "/admin"):
			appPrefix = "/admin"
		case strings.HasPrefix(requestPath, "/mail"):
			appPrefix = "/mail"
		case strings.HasPrefix(requestPath, "/portal"):
			appPrefix = "/portal"
		default:
			appPrefix = ""
		}

		relPath := strings.TrimPrefix(requestPath, appPrefix)
		if relPath == "" || relPath == "/" {
			relPath = "/index.html"
		}

		// Build the full path within the embedded FS
		fullPath := embedPath + relPath

		// Try the actual file
		data, err := fs.ReadFile(root, fullPath)
		if err != nil {
			// Fallback to app index.html for SPA routing
			indexPath := embedPath + "/index.html"
			indexData, err := fs.ReadFile(root, indexPath)
			if err != nil {
				return c.Status(fiber.StatusNotFound).SendString("Not found")
			}
			c.Set("Content-Type", "text/html; charset=utf-8")
			c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
			return c.Send(indexData)
		}

		// Set content type and cache headers
		contentType := detectContentType(relPath)
		if contentType != "" {
			c.Set("Content-Type", contentType)
		}
		if strings.HasPrefix(relPath, "/assets/") {
			c.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			c.Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}

		return c.Send(data)
	}
}

func detectContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".json"):
		return "application/json"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(path, ".woff2"):
		return "font/woff2"
	case strings.HasSuffix(path, ".woff"):
		return "font/woff"
	case strings.HasSuffix(path, ".ico"):
		return "image/x-icon"
	default:
		return ""
	}
}

func NewRouter(cfg RouterConfig) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:      cfg.Product,
		ServerHeader: "OrvixEM",
	})

	app.Use(cors.New(cors.Config{
		AllowOrigins:     joinStrings(cfg.Config.Security.AllowedOrigins),
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowHeaders:     "Origin,Content-Type,Accept,Authorization,X-API-Key,X-CSRF-Token",
		AllowCredentials: joinStrings(cfg.Config.Security.AllowedOrigins) != "*",
	}))

	app.Use(handlers.SecureHeadersMiddleware(cfg.Config.Security))

	// Request ID middleware
	app.Use(func(c *fiber.Ctx) error {
		rid := c.Get("X-Request-ID")
		if rid == "" {
			rid = fmt.Sprintf("orvix-%d", time.Now().UnixNano())
		}
		c.Set("X-Request-ID", rid)
		c.Locals("request_id", rid)
		return c.Next()
	})

	app.Use(security.CSRFMiddleware())

	app.Use(security.RateLimitMiddleware(cfg.Config.Security.RateLimitPerIP, cfg.Config.Security.RateLimitWindow))

	// Metrics middleware
	app.Use(func(c *fiber.Ctx) error {
		start := time.Now()
		err := c.Next()
		duration := time.Since(start).Seconds()
		metrics.HTTPRequestsTotal.WithLabelValues(c.Method(), c.Path(), fmt.Sprintf("%d", c.Response().StatusCode())).Inc()
		metrics.HTTPRequestDuration.WithLabelValues(c.Method(), c.Path()).Observe(duration)
		return err
	})

	h := handlers.New(handlers.HandlerConfig{
		Config:    cfg.Config,
		Version:   cfg.Version,
		Product:   cfg.Product,
		Commit:    cfg.Commit,
		Channel:   cfg.Channel,
		BuildDate: cfg.BuildDate,
		DB:        cfg.DB,
		Logger:    cfg.Logger,
		License:   cfg.License,
		Features:  cfg.Features,
		Stalwart:  cfg.Stalwart,
		Auth:      cfg.Auth,
		Updater:   cfg.Updater,
		Metrics:   cfg.Metrics,
	})

	licenseGate := security.LicenseGateMiddleware(cfg.License, cfg.Features)

	// --- API routes (must be defined before frontend catch-all) ---
	app.Get("/health", h.HealthCheck)

	// Enhanced health check
	app.Get("/healthz", func(c *fiber.Ctx) error {
		dbReady := true
		sqlDB, err := cfg.DB.DB()
		if err != nil || sqlDB.Ping() != nil {
			dbReady = false
		}
		stalwartRunning := cfg.Stalwart.IsRunning()

		// Check frontend assets exist
		adminExists := false
		webmailExists := false
		portalExists := false
		if _, err := web.FrontendFS.ReadDir("admin/dist"); err == nil {
			adminExists = true
		}
		if _, err := web.FrontendFS.ReadDir("webmail/dist"); err == nil {
			webmailExists = true
		}
		if _, err := web.FrontendFS.ReadDir("portal/dist"); err == nil {
			portalExists = true
		}

		licenseActive := false
		if lic, err := cfg.License.GetActiveLicense(); err == nil && lic != nil {
			licenseActive = true
		}

		return c.JSON(fiber.Map{
			"status":   "ok",
			"product":  cfg.Product,
			"version":  cfg.Version,
			"database": dbReady,
			"stalwart": stalwartRunning,
			"license":  licenseActive,
			"frontend": map[string]bool{
				"admin":   adminExists,
				"webmail": webmailExists,
				"portal":  portalExists,
			},
		})
	})

	app.Get("/version", h.VersionInfo)

	apiGroup := app.Group("/api/v1")

	apiGroup.Get("/license/status", h.LicenseStatus)
	apiGroup.Post("/license/activate", h.ActivateLicense)
	apiGroup.Get("/features", h.FeatureFlags)

	apiGroup.Post("/auth/login", h.Login)
	apiGroup.Post("/auth/verify-totp", h.VerifyTOTP)
	apiGroup.Post("/auth/refresh", h.RefreshToken)
	apiGroup.Post("/auth/logout", h.Logout)

	apiGroup.Post("/admin/bootstrap", h.AdminBootstrap)

	authGroup := apiGroup.Group("")
	authGroup.Use(security.AuthRequiredMiddleware(cfg.Auth))

	authGroup.Get("/admin/stats", h.AdminStats)

	authGroup.Get("/admin/tenants", licenseGate("admin_console"), h.ListTenants)
	authGroup.Post("/admin/tenants", licenseGate("admin_console"), h.CreateTenant)

	authGroup.Get("/admin/domains", licenseGate("admin_console"), h.ListDomains)
	authGroup.Post("/admin/domains", licenseGate("admin_console"), h.CreateDomain)
	authGroup.Get("/admin/domains/:id", licenseGate("admin_console"), h.GetDomain)
	authGroup.Delete("/admin/domains/:id", licenseGate("admin_console"), h.DeleteDomain)

	authGroup.Get("/admin/provisioning-jobs", licenseGate("admin_console"), h.ListProvisioningJobs)
	authGroup.Get("/admin/audit-logs", licenseGate("admin_console"), h.ListAuditLogs)

	authGroup.Post("/totp/setup", h.SetupTOTP)
	authGroup.Post("/totp/enable", h.EnableTOTP)
	authGroup.Post("/totp/disable", h.DisableTOTP)

	authGroup.Get("/calendars", licenseGate("calendar_contacts"), h.ListCalendars)
	authGroup.Post("/calendars", licenseGate("calendar_contacts"), h.CreateCalendar)
	authGroup.Delete("/calendars/:id", licenseGate("calendar_contacts"), h.DeleteCalendar)
	authGroup.Get("/calendars/:calendar_id/events", licenseGate("calendar_contacts"), h.ListEvents)
	authGroup.Post("/events", licenseGate("calendar_contacts"), h.CreateEvent)
	authGroup.Delete("/events/:id", licenseGate("calendar_contacts"), h.DeleteEvent)

	authGroup.Get("/contacts", licenseGate("calendar_contacts"), h.ListContacts)
	authGroup.Post("/contacts", licenseGate("calendar_contacts"), h.CreateContact)
	authGroup.Delete("/contacts/:id", licenseGate("calendar_contacts"), h.DeleteContact)
	authGroup.Get("/contact-groups", licenseGate("calendar_contacts"), h.ListContactGroups)
	authGroup.Post("/contact-groups", licenseGate("calendar_contacts"), h.CreateContactGroup)
	authGroup.Delete("/contact-groups/:id", licenseGate("calendar_contacts"), h.DeleteContactGroup)

	authGroup.Get("/domains", licenseGate("admin_console"), h.ListDomains)
	authGroup.Post("/domains", licenseGate("admin_console"), h.CreateDomain)
	authGroup.Get("/domains/:id", licenseGate("admin_console"), h.GetDomain)
	authGroup.Delete("/domains/:id", licenseGate("admin_console"), h.DeleteDomain)

	authGroup.Get("/users", licenseGate("admin_console"), h.ListUsers)
	authGroup.Post("/users", licenseGate("admin_console"), h.CreateUser)
	authGroup.Get("/users/:id", licenseGate("admin_console"), h.GetUser)
	authGroup.Delete("/users/:id", licenseGate("admin_console"), h.DeleteUser)

	authGroup.Get("/mailboxes", licenseGate("rest_api"), h.ListMailboxes)
	authGroup.Put("/mailboxes/:id/quota", licenseGate("rest_api"), h.SetQuota)

	authGroup.Get("/mail-queue", licenseGate("admin_console"), h.ListMailQueue)
	authGroup.Get("/mail-queue/stats", licenseGate("admin_console"), h.GetMailQueueStats)
	authGroup.Get("/mail-queue/:id", licenseGate("admin_console"), h.GetMailQueueItem)
	authGroup.Post("/mail-queue/:id/retry", licenseGate("admin_console"), h.RetryMailQueueItem)
	authGroup.Delete("/mail-queue/:id", licenseGate("admin_console"), h.DeleteMailQueueItem)

	authGroup.Get("/firewall/rules", licenseGate("mail_firewall_basic"), h.ListFirewallRules)
	authGroup.Post("/firewall/rules", licenseGate("mail_firewall_basic"), h.CreateFirewallRule)
	authGroup.Put("/firewall/rules/:id", licenseGate("mail_firewall_basic"), h.UpdateFirewallRule)
	authGroup.Delete("/firewall/rules/:id", licenseGate("mail_firewall_basic"), h.DeleteFirewallRule)

	authGroup.Get("/firewall/geo", licenseGate("mail_firewall_basic"), h.ListGeoBlocks)
	authGroup.Post("/firewall/geo", licenseGate("mail_firewall_basic"), h.CreateGeoBlock)
	authGroup.Delete("/firewall/geo/:id", licenseGate("mail_firewall_basic"), h.DeleteGeoBlock)

	authGroup.Get("/spam/whitelist", licenseGate("anti_spam_basic"), h.ListSpamWhitelist)
	authGroup.Get("/spam/blacklist", licenseGate("anti_spam_basic"), h.ListSpamBlacklist)
	authGroup.Post("/spam/:list", licenseGate("anti_spam_basic"), h.AddSpamListEntry)
	authGroup.Delete("/spam/:list/:id", licenseGate("anti_spam_basic"), h.DeleteSpamListEntry)

	authGroup.Get("/autoheal/status", licenseGate("auto_heal"), h.GetAutoHealStatus)
	authGroup.Post("/autoheal/trigger", licenseGate("auto_heal"), h.TriggerAutoHeal)

	authGroup.Get("/guardian/status", licenseGate("guardian_ai"), h.GetGuardianStatus)
	authGroup.Post("/guardian/analyze", licenseGate("guardian_ai"), h.AnalyzeThreat)

	authGroup.Get("/compliance/status", licenseGate("compliance_center"), h.GetComplianceStatus)
	authGroup.Post("/compliance/legal-hold", licenseGate("legal_hold"), h.CreateLegalHold)
	authGroup.Post("/compliance/ediscovery", licenseGate("legal_hold"), h.RunEDiscovery)

	authGroup.Get("/encryption/status", licenseGate("zero_knowledge_encryption"), h.GetEncryptionStatus)
	authGroup.Post("/encryption/enable", licenseGate("zero_knowledge_encryption"), h.EnableEncryption)

	authGroup.Get("/collaboration/shared-inboxes", licenseGate("collaboration_layer"), h.ListSharedInboxes)
	authGroup.Post("/collaboration/shared-inboxes", licenseGate("collaboration_layer"), h.CreateSharedInbox)

	authGroup.Get("/intelligence", licenseGate("email_intelligence"), h.GetEmailIntelligence)

	authGroup.Get("/backups", licenseGate("backup_restore"), h.ListBackups)
	authGroup.Post("/backups", licenseGate("backup_restore"), h.CreateBackup)
	authGroup.Post("/backups/:id/restore", licenseGate("backup_restore"), h.RestoreBackup)

	authGroup.Post("/migration/start", licenseGate("migration_tool"), h.StartMigration)
	authGroup.Get("/migration/status", licenseGate("migration_tool"), h.GetMigrationStatus)

	authGroup.Post("/compose/suggest", licenseGate("smart_compose_basic"), h.ComposeSuggestion)
	authGroup.Post("/compose/summarize", licenseGate("smart_compose_basic"), h.SummarizeEmail)
	authGroup.Post("/compose/translate", licenseGate("smart_compose_advanced"), h.TranslateEmail)
	authGroup.Post("/compose/send", licenseGate("admin_console"), h.SendEmail)

	authGroup.Get("/dns/check", licenseGate("dns_wizard"), h.CheckDNSRecords)
	authGroup.Get("/dns/records/:id", licenseGate("dns_wizard"), h.GetDNSRecords)

	authGroup.Post("/search", licenseGate("admin_console"), h.SearchMessages)

	authGroup.Get("/logs/:type", licenseGate("admin_console"), h.ListLogs)

	authGroup.Get("/distribution-lists", licenseGate("distribution_lists"), h.ListDistributionLists)
	authGroup.Post("/distribution-lists", licenseGate("distribution_lists"), h.CreateDistributionList)
	authGroup.Get("/distribution-lists/:id", licenseGate("distribution_lists"), h.GetDistributionList)
	authGroup.Delete("/distribution-lists/:id", licenseGate("distribution_lists"), h.DeleteDistributionList)
	authGroup.Post("/distribution-lists/:id/members", licenseGate("distribution_lists"), h.AddDistributionListMember)
	authGroup.Delete("/distribution-list-members/:id", licenseGate("distribution_lists"), h.RemoveDistributionListMember)

	authGroup.Get("/resources", licenseGate("resource_booking"), h.ListResources)
	authGroup.Post("/resources", licenseGate("resource_booking"), h.CreateResource)
	authGroup.Delete("/resources/:id", licenseGate("resource_booking"), h.DeleteResource)

	authGroup.Get("/public-folders", licenseGate("public_folders"), h.ListPublicFolders)
	authGroup.Post("/public-folders", licenseGate("public_folders"), h.CreatePublicFolder)
	authGroup.Delete("/public-folders/:id", licenseGate("public_folders"), h.DeletePublicFolder)
	authGroup.Post("/public-folders/:id/access", licenseGate("public_folders"), h.SetPublicFolderAccess)

	authGroup.Get("/routing-rules", licenseGate("advanced_routing"), h.ListRoutingRules)
	authGroup.Post("/routing-rules", licenseGate("advanced_routing"), h.CreateRoutingRule)
	authGroup.Put("/routing-rules/:id", licenseGate("advanced_routing"), h.UpdateRoutingRule)
	authGroup.Delete("/routing-rules/:id", licenseGate("advanced_routing"), h.DeleteRoutingRule)

	authGroup.Get("/dlp/policies", licenseGate("dlp"), h.ListDLPPolicies)
	authGroup.Post("/dlp/policies", licenseGate("dlp"), h.CreateDLPPolicy)
	authGroup.Put("/dlp/policies/:id", licenseGate("dlp"), h.UpdateDLPPolicy)
	authGroup.Delete("/dlp/policies/:id", licenseGate("dlp"), h.DeleteDLPPolicy)
	authGroup.Get("/dlp/violations", licenseGate("dlp"), h.ListDLPViolations)

	authGroup.Get("/sla/dashboard", licenseGate("sla_monitoring"), h.GetSLADashboard)
	authGroup.Post("/sla/metrics", licenseGate("sla_monitoring"), h.RecordSLAMetric)

	authGroup.Get("/ldap/configs", licenseGate("ldap_sync"), h.ListLDAPConfigs)
	authGroup.Post("/ldap/configs", licenseGate("ldap_sync"), h.CreateLDAPConfig)
	authGroup.Put("/ldap/configs/:id", licenseGate("ldap_sync"), h.UpdateLDAPConfig)
	authGroup.Delete("/ldap/configs/:id", licenseGate("ldap_sync"), h.DeleteLDAPConfig)
	authGroup.Post("/ldap/configs/:id/sync", licenseGate("ldap_sync"), h.TriggerLDAPSync)

	authGroup.Get("/sso/configs", licenseGate("sso"), h.ListSSOConfigs)
	authGroup.Post("/sso/configs", licenseGate("sso"), h.CreateSSOConfig)
	authGroup.Put("/sso/configs/:id", licenseGate("sso"), h.UpdateSSOConfig)
	authGroup.Delete("/sso/configs/:id", licenseGate("sso"), h.DeleteSSOConfig)

	authGroup.Get("/messages", licenseGate("webmail"), h.ListMessages)

	authGroup.Get("/api-keys", licenseGate("rest_api"), h.ListAPIKeys)
	authGroup.Post("/api-keys", licenseGate("rest_api"), h.CreateAPIKey)
	authGroup.Delete("/api-keys/:id", licenseGate("rest_api"), h.DeleteAPIKey)

	authGroup.Post("/provision/domain", licenseGate("instant_deploy_api"), h.ProvisionDomain)

	authGroup.Get("/webhooks", licenseGate("admin_console"), h.ListWebhooks)
	authGroup.Post("/webhooks", licenseGate("admin_console"), h.CreateWebhook)
	authGroup.Delete("/webhooks/:id", licenseGate("admin_console"), h.DeleteWebhook)

	stalwartGroup := apiGroup.Group("/stalwart")
	stalwartGroup.Get("/status", h.StalwartStatus)
	stalwartGroup.Get("/health", h.StalwartHealth)

	// --- Admin Control Plane v2 routes (decoupled, RBAC-scoped) ---
	mailAdapter := adapters.NewLocalCoreMailAdapter(cfg.DB, cfg.Auth, cfg.Logger)
	adminCfg := admin_handlers.HandlerConfig{
		DB:          cfg.DB,
		Logger:      cfg.Logger,
		Auth:        cfg.Auth,
		MailAdapter: mailAdapter,
	}
	adm := admin_handlers.New(adminCfg)

	adminV1 := apiGroup.Group("/admin/v1")
	adminV1.Use(security.AuthRequiredMiddleware(cfg.Auth))

	superGroup := adminV1.Group("/super")
	superGroup.Use(middleware.RequireSuperAdmin())
	superGroup.Post("/tenants", adm.CreateTenant)
	superGroup.Get("/tenants/:id", adm.GetTenant)

	tenantGroup := adminV1.Group("/tenant")
	tenantGroup.Use(middleware.RequireTenantAdmin())
	tenantGroup.Post("/users", adm.CreateTenantUser)
	tenantGroup.Get("/users", adm.GetTenantUsers)

	app.Get("/metrics", adaptor.HTTPHandler(cfg.Metrics.Handler()))

	// --- Frontend routes (after API routes) ---
	app.Get("/admin*", serveFrontend(web.FrontendFS, "admin/dist"))
	app.Get("/mail*", serveFrontend(web.FrontendFS, "webmail/dist"))
	app.Get("/portal*", serveFrontend(web.FrontendFS, "portal/dist"))
	app.Get("/*", serveFrontend(web.FrontendFS, "admin/dist"))

	return app
}

func joinStrings(s []string) string {
	result := ""
	for i, v := range s {
		if i > 0 {
			result += ", "
		}
		result += v
	}
	return result
}
