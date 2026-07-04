package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/orvix/orvix/internal/antivirus"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/api/handlers/settings"
	settingsbridge "github.com/orvix/orvix/internal/settings/bridge"
	"github.com/orvix/orvix/internal/auth"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/push"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/dnsops"
	"github.com/orvix/orvix/internal/dnsops/providers"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/metrics"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/observability"
	orvixruntime "github.com/orvix/orvix/internal/runtime"
	"github.com/orvix/orvix/internal/ruler"
	"github.com/orvix/orvix/internal/tlsmgmt"
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
	appCtx       context.Context
	cancel       context.CancelFunc
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

	ctx, cancel := context.WithCancel(context.Background())
	router := &Router{
		app:          app,
		auth:         authenticator,
		csrf:         auth.NewCSRFManager(db, logger, cfg.Server.TLSAuto),
		apikeys:      apikeyMgr,
		redisLimiter: rateLimiter,
		logger:       logger,
		cfg:          cfg,
		appCtx:       ctx,
		cancel:       cancel,
		h:            handlers.NewHandler(db, authenticator, apikeyMgr, logger, cfg, registry, ff, rateLimiter),
	}
	// Record the moment the router was constructed. The runtime
	// telemetry endpoint (/api/v1/admin/runtime) reads this to
	// compute uptime. Capturing it here (rather than at process
	// start) is close enough for an admin dashboard: the small
	// difference between process start and router construction
	// is dominated by module init and DB migrations, and the
	// endpoint never claims second-precision.
	router.h.SetProcessStartedAt(time.Now().UTC())

	// Wire the listener registry (created in main.go and
	// populated by the coremail runtime module during Start())
	// into the handler so GetAdminRuntime returns real listener
	// status instead of "unknown".
	//
	// We retrieve the registry from Handler via a provider
	// interface on the coremail module. If the module is not
	// registered (custom builds, tests), the registry remains
	// nil and the telemetry endpoint falls back to "unknown"
	// (the pre-ADMIN-LISTENER-TRACKING-2C behaviour).
	if mod, ok := registry.Get("coremail-runtime"); ok {
		if lrProvider, ok := mod.(interface {
			ListenerRegistry() *orvixruntime.ListenerRegistry
		}); ok {
			if lr := lrProvider.ListenerRegistry(); lr != nil {
				router.h.SetListenerRegistry(lr)
				logger.Info("listener registry wired for admin runtime telemetry")
			}
		}
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
		// Wire the Web Push (RFC 8030) notifier from the
		// same runtime module. The webmail
		// /api/v1/webmail/push/* endpoints
		// (subscribe / unsubscribe / status / test) read
		// from this notifier, and the delivery worker
		// fires notifications from it on local INBOX
		// delivery. When the runtime is disabled or has
		// not been initialized, the notifier stays nil
		// and the push endpoints return a clear 503
		// "push notifications not available" — the webmail
		// UI surfaces that as "disabled by config".
		if pnProvider, ok := mod.(interface {
			PushNotifier() *push.PushNotifier
		}); ok {
			if pn := pnProvider.PushNotifier(); pn != nil {
				router.h.SetPushNotifier(pn)
				if pn.IsEnabled() {
					logger.Info("push notifier wired for webmail push endpoints")
				} else {
					logger.Info("push notifier wired but disabled (VAPID keys not configured)")
				}
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

	// Wire DNS / DKIM operations service (DNS-DKIM-OPERATIONS-2F).
	// The Service is built with the NetResolver so live DNS
	// verification uses the operator's real resolver (no shell-
	// out to dig/nslookup). Cloudflare / Namecheap providers are
	// registered with the credentials from cfg.DNS — when the
	// env config is missing, the provider's Plan() returns
	// "not configured" and Apply() refuses. Tokens never reach
	// any handler or response.
	dnsResolver := dnsops.NewNetResolver()
	// Namecheap uses an HTTP client abstraction so tests can
	// use a fake client; production wires a real
	// NetNamecheapClient with the operator's credentials.
	namecheapClient := providers.NewNetNamecheapClient(
		cfg.DNS.NamecheapAPIUser,
		cfg.DNS.NamecheapAPIKey,
		cfg.DNS.NamecheapUsername,
		cfg.DNS.NamecheapClientIP,
		cfg.DNS.NamecheapSandbox,
	)
	dnsProviderList := []dnsops.Provider{
		providers.NewCloudflareProvider(providers.CloudflareConfig{
			APIToken: cfg.DNS.CloudflareAPIKey,
			ZoneID:   cfg.DNS.CloudflareZoneID,
		}, dnsResolver),
		providers.NewNamecheapProvider(providers.NamecheapConfig{
			APIUser:     cfg.DNS.NamecheapAPIUser,
			APIKey:      cfg.DNS.NamecheapAPIKey,
			Username:    cfg.DNS.NamecheapUsername,
			ClientIP:    cfg.DNS.NamecheapClientIP,
			Sandbox:     cfg.DNS.NamecheapSandbox,
			EnableApply: cfg.DNS.NamecheapEnableApply,
		}, namecheapClient),
	}
	dnsSvc := dnsops.NewService(dnsResolver, dnsProviderList...)
	router.h.SetDNSOpsService(dnsSvc)
	logger.Info("dns ops service wired",
		zap.Strings("providers", dnsSvc.Providers()),
		zap.Bool("namecheap_apply_enabled", cfg.DNS.NamecheapEnableApply))

	// Wire the admin settings persistence store. PATCH
	// /api/v1/admin/settings writes through this store; GET merges
	// its rows with the config defaults to build the response. The
	// store manages its own table (admin_settings) and indexes on
	// first use; we MUST call EnsureSchema() before the boot-time
	// settings bridge runs, otherwise the bridge's first Apply()
	// query against admin_settings fails with "no such table" on
	// a brand-new VPS. The previous "lazy CREATE TABLE on first
	// PATCH" approach left a scary journal warning on every fresh
	// install — the BLOCKER 7 fresh-boot regression.
	if sqlDB, err := db.DB(); err == nil {
		store := settings.NewStore(sqlDB)
		if err := store.EnsureSchema(router.appCtx); err != nil {
			logger.Warn("admin settings store: ensure schema failed", zap.Error(err))
		}
		router.h.SetSettingsStore(store)
		logger.Info("admin settings store wired")
	} else {
		logger.Warn("admin settings store unavailable: failed to get sql.DB", zap.Error(err))
	}

	// Boot-time bridge: load persisted protocol settings
	// from admin_settings into the live cfg. Restart-
	// required keys are recorded on the bridge's
	// pending list so the admin UI can show "needs
	// restart" honestly. The bridge reads the same
	// admin_settings table the PATCH endpoint writes,
	// so it is always consistent with operator intent.
	if sqlDB, sErr := db.DB(); sErr == nil {
		br := settingsbridge.New(router.cfg, sqlDB, logger)
		if sm, aErr := br.Apply(router.appCtx); aErr != nil {
			logger.Warn("settings bridge: initial apply failed", zap.Error(aErr))
		} else {
			logger.Info("settings bridge loaded",
				zap.Int("applied", sm.Applied),
				zap.Int("pending", sm.Pending))
		}
		router.h.SetSettingsBridge(br)
	} else {
		logger.Warn("settings bridge unavailable: failed to get sql.DB", zap.Error(sErr))
	}

	// Wire the admin TLS / certificate manager. The service
	// is optional — when nil the SSL admin endpoints return
	// 503 instead of fabricating cert metadata.
	if sqlDB, err := db.DB(); err == nil {
		tlsSvc := tlsmgmt.NewService(sqlDB, &tlsConfigAdapter{cfg: router.cfg})
		if err := tlsSvc.EnsureUploadedCertSchema(context.Background()); err != nil {
			logger.Warn("ensure uploaded cert schema failed", zap.Error(err))
		}
		router.h.SetTLSService(tlsSvc)
		logger.Info("admin TLS service wired")
	}

	// Wire the runtime's antivirus engine + rule engine
	// into the admin handler. Look them up via the module
	// registry — the runtime registers itself during Init.
	if mod, ok := registry.Get("coremail-runtime"); ok {
		if rmod, ok := mod.(interface {
			AntivirusEngine() *antivirus.Engine
			RuleEngine() *ruler.Engine
			Observability() *observability.Observability
		}); ok {
			if eng := rmod.AntivirusEngine(); eng != nil {
				router.h.SetAntivirusService(eng)
				logger.Info("admin antivirus service wired from runtime")
			}
			if eng := rmod.RuleEngine(); eng != nil {
				router.h.SetRulerService(eng)
				logger.Info("admin ruler service wired from runtime")
			}
			if obs := rmod.Observability(); obs != nil {
				router.h.SetObservability(obs)
				logger.Info("admin observability wired from runtime")
			}
		}
	}

	router.setupMiddleware()
	router.setupRoutes()
	router.setupAdminUI()

	return router
}

// SetQueueEngine wires a queue engine into the handler for test setups
// where the coremail runtime module is not available.
func (r *Router) SetQueueEngine(qe *queue.QueueEngine) {
	r.h.SetQueueEngine(qe)
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
	// PHASE-0 BLOCKER FIX: the general API rate limiter is NO LONGER applied
	// globally. The previous global `r.app.Use(...)` blocked the
	// admin SPA itself — `GET /admin` triggered the rate limiter
	// because every static asset (index.html, app.js, styles.css,
	// the 10 core modules, the 19 page modules) counts against
	// the per-IP budget. Loading the admin console therefore
	// consumed ~35 requests on first paint and the dashboard
	// crashed within seconds with a JSON 429:
	//
	//     {"error":"rate limit exceeded, try again later"}
	//
	// The fix scopes the limiter to the `/api/v1` group only.
	// Static SPA assets (admin + webmail) are exempt; API calls
	// retain their per-IP budget (Redis default: 100 / 60 s).
	// Login endpoints retain their tighter login limit (5 / 15 m)
	// via the dedicated `LoginMiddleware()` already mounted in
	// `setupRoutes()`. Security is unchanged — only the scope of
	// the limit changed.
	// The metrics endpoint stays reachable without rate-limit.
	if r.cfg.Metrics.Enabled {
		r.app.Get(r.cfg.Metrics.Path, metrics.Handler())
	}
}

// apiRateLimitMiddleware returns the general API rate limiter
// middleware for the /api/v1 group. It is built once in setupRoutes
// and mounted only on the API group, so SPA static routes are
// never counted against the per-IP budget. Login endpoints get the
// dedicated LoginMiddleware (5 attempts / 15 min per IP) and do
// NOT also pass through this handler, by mounting order.
func (r *Router) apiRateLimitMiddleware() fiber.Handler {
	if r.redisLimiter != nil {
		return r.redisLimiter.Middleware()
	}
	return limiter.New(limiter.Config{Max: 100, Expiration: 60 * 1000})
}

func (r *Router) setupRoutes() {
	// Public MTA-STS policy endpoint (DNS-AUTOMATION-2G).
	// Served at the canonical RFC 8461 path; no auth, no CSRF.
	// The handler returns the policy body for any host that
	// resolves to a provisioned Orvix domain (mta-sts.<domain>)
	// and 404 otherwise. Caddy is expected to route
	// mta-sts.<domain> at the Orvix backend; the existing
	// admin / webmail hostnames continue to work.
	r.app.Get("/.well-known/mta-sts.txt", r.h.GetPublicMTASTS)

	// All `/api/v1/*` requests pass through the general rate
	// limiter (100/min per IP by default, via Redis when wired).
	// Static SPA routes (`/admin/*`, `/webmail/*`, `/`, mta-sts)
	// are registered on `r.app` directly and DO NOT pass through
	// this handler — so loading the admin UI no longer eats the
	// per-IP API budget.
	api := r.app.Group("/api/v1", r.apiRateLimitMiddleware())
	api.Get("/health", r.h.Health)

	loginGroup := api.Group("/auth")
	if r.redisLimiter != nil {
		loginGroup.Post("/login", r.redisLimiter.LoginMiddleware(), r.h.Login)
	} else {
		loginGroup.Post("/login", limiter.New(limiter.Config{Max: 5, Expiration: 15 * 60 * 1000}), r.h.Login)
	}
	loginGroup.Post("/refresh", r.h.Refresh)

	// MFA login verification (public — no auth middleware).
	// Exchanges a password-based MFA challenge token + TOTP/recovery code
	// for real access/refresh tokens. Mounted on the public login group
	// so MFA-enabled users can complete login without being authenticated.
	loginGroup.Post("/mfa/verify", r.h.MFALoginVerify)
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
	// New in Webmail Enterprise 2: per-message source
	// download, single-message move, multi-message batch
	// operations. All behind the same protected group as
	// the other state-changing webmail endpoints, so the
	// auth middleware rejects missing/invalid cookies
	// with 401 before the handler runs.
	protected.Get("/webmail/messages/:id/source", r.h.WebmailMessageSource)
	protected.Post("/webmail/messages/:id/move", r.h.WebmailMoveMessage)
	protected.Post("/webmail/messages/batch", r.h.WebmailMessageBatch)
	// Attachment download / preview. The :id is parsed
	// with parseMessageID (digits only) and the
	// handler confirms the attachment's parent message
	// belongs to the caller's mailbox before opening
	// the file. Returns 404 to non-owners so the
	// response shape does not leak existence.
	protected.Get("/webmail/attachments/:id", r.h.WebmailAttachmentDownload)
	protected.Get("/webmail/attachments/:id/preview", r.h.WebmailAttachmentPreview)
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
	// Push notification subscription management.
	protected.Post("/webmail/push/subscribe", r.h.PushSubscribe)
	protected.Post("/webmail/push/unsubscribe", r.h.PushUnsubscribe)
	protected.Get("/webmail/push/status", r.h.PushStatus)
	protected.Post("/webmail/push/test", r.h.PushTest)

	// User settings — per-mailbox profile / appearance / compose /
	// mail behavior / notification preferences. Auth + mailbox
	// ownership enforced by resolveWebmailUserContext inside the
	// handlers; no id is taken from the request body.
	protected.Get("/webmail/settings", r.h.WebmailGetSettings)
	protected.Put("/webmail/settings", r.h.WebmailPutSettings)

	// Per-mailbox rules engine API. The handlers resolve
	// the caller's mailbox from the JWT identity via
	// resolveWebmailUserContext — there is no mailbox id
	// in the URL, so the caller can never read or write
	// another user's rules / vacation / forwarding row.
	// The repository WHERE mailbox_id = ? predicate is the
	// second line of defence against guessing rule ids.
	// All endpoints are mounted behind the auth middleware
	// so missing / invalid cookies get 401 before any
	// mailbox lookup runs.
	protected.Get("/webmail/rules", r.h.WebmailListRules)
	protected.Post("/webmail/rules", r.h.WebmailCreateRule)
	protected.Put("/webmail/rules/:id", r.h.WebmailUpdateRule)
	protected.Delete("/webmail/rules/:id", r.h.WebmailDeleteRule)
	protected.Get("/webmail/vacation", r.h.WebmailGetVacation)
	protected.Put("/webmail/vacation", r.h.WebmailPutVacation)
	protected.Get("/webmail/forwarding", r.h.WebmailGetForwarding)
	protected.Put("/webmail/forwarding", r.h.WebmailPutForwarding)

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
	// Admin Queue Operations (QUEUE-OPERATIONS-2E): summary,
	// single-entry detail, and safe retry/delete (already wired
	// in the CSRF-protected men group below). All admin-only.
	// Note: the explicit /admin/ path segment distinguishes these
	// admin-read endpoints from legacy /queue paths (list, retry,
	// delete) which are mounted without the segment for backward
	// compatibility.
	admin.Get("/admin/queue/summary", r.h.AdminQueueSummary)
	admin.Get("/admin/queue/messages", r.h.AdminQueueList)
	admin.Get("/admin/queue/messages/:id", r.h.AdminQueueDetail)
	admin.Get("/admin/queue/:id", r.h.GetAdminQueueEntry)
	admin.Get("/admin/backups", r.h.ListBackups)
	admin.Get("/admin/backups/schedule", r.h.GetBackupSchedule)
	admin.Get("/admin/backups/metrics", r.h.GetBackupMetrics)
	admin.Get("/admin/backups/health", r.h.GetBackupHealth)
	admin.Get("/admin/backups/:id/download", r.h.DownloadBackup)
	admin.Get("/admin/backups/:id", r.h.GetBackup)
	// Legacy /backups routes — return 410 Gone so the frontend
	// can safely discover the new path without accidentally
	// performing destructive operations on the old one.
	admin.Get("/backups", r.h.LegacyGone)
	admin.Get("/backups/schedule", r.h.LegacyGone)
	admin.Get("/backups/metrics", r.h.LegacyGone)
	admin.Get("/backups/health", r.h.LegacyGone)
	admin.Get("/backups/:id/download", r.h.LegacyGone)
	admin.Get("/firewall/rules", r.h.ListFirewallRules)
	admin.Get("/firewall/logs", r.h.ListFirewallLogs)
	admin.Get("/modules", r.h.ListModules)
	admin.Get("/license", r.h.GetLicense)
	admin.Get("/audit/logs", r.h.ListAuditLogs)
	// Admin Enterprise v2 — RBAC + account classes + groups +
	// lists + public folders + quarantine + ACL + log rules.
	admin.Get("/admin/account-classes", r.h.ListAccountClasses)
	admin.Get("/admin/domain-groups", r.h.ListDomainGroups)
	admin.Get("/admin/mailing-lists", r.h.ListMailingLists)
	admin.Get("/admin/public-folders", r.h.ListPublicFolders)
	admin.Get("/admin/admin-groups", r.h.ListAdminGroups)
	admin.Get("/admin/quarantine", r.h.ListQuarantine)
admin.Get("/admin/audit-logs", r.h.ListAdminAuditLogs)
admin.Get("/admin/acl-rules", r.h.ListACLRules)
admin.Get("/admin/log-rules", r.h.ListLogRules)
// Enterprise v3 — SSL, acceptance rules, incoming message
// rules, FTP backup targets, file system browser,
// migration sources, clustering, antivirus, settings
// protocol splits.
admin.Get("/admin/ssl/certificates", r.h.AdminSslListCertificates)
admin.Get("/admin/ssl/certificates/reload", r.h.AdminSslReloadCertificates)
admin.Get("/admin/ssl/expiry-warnings", r.h.AdminSslExpiryWarnings)
admin.Get("/admin/ssl/acme/status", r.h.AdminSslAcmeStatus)
admin.Get("/admin/acceptance-rules", r.h.ListAcceptanceRules)
admin.Get("/admin/incoming-msg-rules", r.h.ListIncomingMsgRules)
admin.Get("/admin/migration-sources", r.h.ListMigrationSources)
admin.Get("/admin/backup-targets", r.h.ListBackupTargets)
admin.Get("/admin/backup-targets/:id/test", r.h.TestBackupTarget)
admin.Get("/admin/migration-sources/:id/test", r.h.TestMigrationSource)
admin.Get("/admin/fs/browse", r.h.AdminFsBrowse)
admin.Get("/admin/fs/read", r.h.AdminFsRead)
admin.Get("/admin/cluster/status", r.h.AdminClusteringStatus)
admin.Get("/admin/security/antivirus", r.h.AdminAntivirusStatus)
// Per-protocol settings sub-pages. The :protocol path
// parameter is one of the IDs in the protocolDefs map.
admin.Get("/admin/settings/protocol/:protocol", r.h.ListProtocolSettings)
	admin.Get("/admin/mailing-lists/:id/members", r.h.ListMailingListMembers)
	admin.Get("/admin/admin-groups/:id/members", r.h.ListAdminGroupMembers)
	admin.Get("/feature-flags", r.h.ListFeatureFlags)
	admin.Get("/api-keys", r.h.ListAPIKeys)
	admin.Get("/admin/summary", r.h.AdminSummary)
	// Admin Runtime Telemetry (ADMIN-RUNTIME-TELEMETRY-2B):
	// read-only, admin-protected. No CSRF required (GET).
	admin.Get("/admin/runtime", r.h.GetAdminRuntime)
	// Monitoring v1: read-only health + alert endpoints (admin role).
	admin.Get("/monitoring/health", r.h.GetMonitoringHealth)
	admin.Get("/monitoring/alerts", r.h.GetMonitoringAlerts)
	admin.Get("/monitoring/capacity", r.h.GetMonitoringCapacity)
	admin.Get("/monitoring/snapshot", r.h.GetMonitoringSnapshot)
	admin.Get("/monitoring/alert-providers", r.h.GetMonitoringProviders)

	// Auto-Heal
	admin.Get("/heal/history", r.h.ListHealHistory)
	admin.Post("/heal/check/:name", r.h.RunHealCheck)

	// Guardian
	admin.Post("/guardian/analyze", r.h.AnalyzeEmail)
	admin.Get("/guardian/logs", r.h.ListGuardianLogs)

	// Smart Compose AI
	admin.Post("/compose/complete", r.h.ComposeComplete)
	admin.Post("/compose/stream", r.h.ComposeStream)

	// DNS Automation — legacy endpoints (kept for backward compat
	// with the pre-DNS-DKIM-OPERATIONS-2F UI). They now delegate
	// to the new dnsops service when wired; they return 503 when
	// the service is not available so the dashboard never sees a
	// "pending" placeholder.
	admin.Post("/dns/check/:domain", r.h.DNSCheck)
	admin.Post("/dns/wizard/:domain", r.h.DNSWizard)

	// Admin Settings (ENTERPRISE-SETTINGS-2H): read-only GET, write is CSRF-protected
	admin.Get("/admin/mfa/status", r.h.MFAStatusGet)
	admin.Get("/admin/settings", r.h.AdminSettingsGet)

	// DNS Operations (DNS-DKIM-OPERATIONS-2F): real DNS / DKIM
	// operations for the admin UI. All admin-only, all read-only
	// except for DKIM keygen (CSRF-protected below in `men`)
	// and provider apply (also CSRF-protected).
	admin.Get("/admin/dns/providers", r.h.GetAdminDNSProviders)
	admin.Get("/admin/dns/:domain/plan", r.h.GetAdminDNSPlan)
	admin.Post("/admin/dns/:domain/verify", r.h.PostAdminDNSVerify)
	admin.Get("/admin/dns/:domain/wizard", r.h.GetAdminDNSWizard)
	admin.Post("/admin/dns/:domain/provider/plan", r.h.PostAdminDNSProviderPlan)

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
	men.Post("/mailboxes/import", r.h.ImportMailboxesCSV)
	men.Post("/mailboxes/import/dry-run", r.h.ImportMailboxesDryRun)
	men.Post("/domains/bulk/status", r.h.BulkDomainStatus)
	men.Delete("/mailboxes/:id", r.h.DeleteMailbox)
	men.Delete("/users/:id", r.h.DeleteUser)
	men.Delete("/queue/:id", r.h.DeleteQueue)
	men.Post("/queue/:id/retry", r.h.RetryQueue)
	men.Post("/admin/queue/messages/:id/retry", r.h.AdminQueueRetryNow)
	men.Post("/admin/queue/messages/:id/bounce", r.h.AdminQueueBounce)
	men.Post("/admin/queue/messages/:id/cancel", r.h.AdminQueueCancel)
	men.Post("/admin/backups", r.h.CreateBackup)
	men.Post("/admin/backups/now", r.h.PostBackupNow)
	men.Post("/admin/backups/schedule", r.h.SetBackupSchedule)
	men.Post("/admin/backups/retention", r.h.RunBackupRetention)
	men.Post("/admin/backups/:id/validate", r.h.PostValidateBackup)
	men.Post("/admin/backups/:id/restore", r.h.PostRestoreBackup)
	men.Delete("/admin/backups/:id", r.h.DeleteBackup)
	// Legacy write routes return 410 Gone.
	men.Post("/backups", r.h.LegacyGone)
	men.Post("/backups/schedule", r.h.LegacyGone)
	men.Post("/backups/retention", r.h.LegacyGone)
	men.Delete("/backups/:id", r.h.LegacyGone)
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
	// DNS Operations (DNS-DKIM-OPERATIONS-2F): state-changing
	// routes behind CSRF middleware. DKIM keygen rotates the
	// server-side private key (irreversible — old signed mail
	// still verifies until DKIM TTL expires); provider apply
	// always returns a Failed result in this build because the
	// live API path is intentionally disabled.
	men.Post("/admin/dns/:domain/dkim", r.h.PostAdminDNSDKIM)
	men.Post("/admin/dns/:domain/provider/apply", r.h.PostAdminDNSProviderApply)

	// Admin MFA (CSRF-protected)
	men.Post("/admin/mfa/setup/begin", r.h.MFASetupBegin)
	men.Post("/admin/mfa/setup/verify", r.h.MFASetupVerify)
	men.Post("/admin/mfa/disable", r.h.MFADisable)

	// Admin Settings write (CSRF-protected)
	men.Patch("/admin/settings", r.h.AdminSettingsPatch)

	// Admin Enterprise v2 mutations (CSRF-protected, admin
	// role). Every mutation writes an entry to coremail_audit
	// (action="<resource>.<verb>", target=<identifier>,
	// result="ok"). Refusal paths return 4xx with a stable
	// error JSON; never fabricate success.
	men.Post("/admin/account-classes", r.h.CreateAccountClass)
	men.Patch("/admin/account-classes/:id", r.h.UpdateAccountClass)
	men.Delete("/admin/account-classes/:id", r.h.DeleteAccountClass)
	men.Post("/admin/domain-groups", r.h.CreateDomainGroup)
	men.Put("/admin/domain-groups/:id/members", r.h.UpdateDomainGroupMembers)
	men.Delete("/admin/domain-groups/:id", r.h.DeleteDomainGroup)
	men.Post("/admin/mailing-lists", r.h.CreateMailingList)
	men.Delete("/admin/mailing-lists/:id", r.h.DeleteMailingList)
	men.Post("/admin/mailing-lists/:id/members", r.h.AddMailingListMember)
	men.Delete("/admin/mailing-lists/:id/members/:memberId", r.h.RemoveMailingListMember)
	men.Post("/admin/public-folders", r.h.CreatePublicFolder)
	men.Delete("/admin/public-folders/:id", r.h.DeletePublicFolder)
	men.Post("/admin/admin-groups", r.h.CreateAdminGroup)
	men.Patch("/admin/admin-groups/:id", r.h.UpdateAdminGroup)
	men.Delete("/admin/admin-groups/:id", r.h.DeleteAdminGroup)
	men.Post("/admin/admin-groups/:id/members", r.h.AddAdminGroupMember)
	men.Delete("/admin/admin-groups/:id/members/:userId", r.h.RemoveAdminGroupMember)
	men.Post("/admin/quarantine/:id/resolve", r.h.ResolveQuarantine)
	men.Post("/admin/acl-rules", r.h.CreateACLRule)
	men.Delete("/admin/acl-rules/:id", r.h.DeleteACLRule)
men.Post("/admin/log-rules", r.h.CreateLogRule)
men.Delete("/admin/log-rules/:id", r.h.DeleteLogRule)
// Enterprise v3 — CSRF-protected mutations for the new
// sections. Each one is mounted inside `men` so the
// X-CSRF-Token check runs before the handler. All
// handlers in enterprise_admin_v3.go + ssl.go write to
// the audit table via h.appendAudit.
men.Post("/admin/ssl/certificates", r.h.AdminSslUploadCertificate)
men.Post("/admin/ssl/certificates/reload", r.h.AdminSslReloadCertificates)
men.Delete("/admin/ssl/certificates/:id", r.h.AdminSslDeleteCertificate)
men.Post("/admin/acceptance-rules", r.h.CreateAcceptanceRule)
men.Patch("/admin/acceptance-rules/:id", r.h.UpdateAcceptanceRule)
men.Post("/admin/acceptance-rules/test", r.h.TestAcceptanceRule)
men.Delete("/admin/acceptance-rules/:id", r.h.DeleteAcceptanceRule)
men.Post("/admin/incoming-msg-rules", r.h.CreateIncomingMsgRule)
men.Patch("/admin/incoming-msg-rules/:id", r.h.UpdateIncomingMsgRule)
men.Delete("/admin/incoming-msg-rules/:id", r.h.DeleteIncomingMsgRule)
men.Post("/admin/migration-sources", r.h.CreateMigrationSource)
men.Patch("/admin/migration-sources/:id", r.h.UpdateMigrationSource)
men.Delete("/admin/migration-sources/:id", r.h.DeleteMigrationSource)
men.Post("/admin/migration-sources/:id/test", r.h.TestMigrationSource)
men.Post("/admin/backup-targets", r.h.CreateBackupTarget)
men.Patch("/admin/backup-targets/:id", r.h.UpdateBackupTarget)
men.Delete("/admin/backup-targets/:id", r.h.DeleteBackupTarget)
men.Post("/admin/backup-targets/:id/test", r.h.TestBackupTarget)
men.Patch("/admin/settings/protocol/:protocol", r.h.PatchProtocolSettings)
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
	return "/var/backups/orvix/"
}
