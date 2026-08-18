package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	fiberrecover "github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/orvix/orvix/internal/abuse"
	dashboardsvc "github.com/orvix/orvix/internal/admin/dashboard"
	domainadminsvc "github.com/orvix/orvix/internal/admin/domain"
	mailboxadminsvc "github.com/orvix/orvix/internal/admin/mailbox"
	orgadminsvc "github.com/orvix/orvix/internal/admin/organization"
	platformpkg "github.com/orvix/orvix/internal/admin/platform"
	"github.com/orvix/orvix/internal/antivirus"
	"github.com/orvix/orvix/internal/api/handlers"
	"github.com/orvix/orvix/internal/api/handlers/settings"
	"github.com/orvix/orvix/internal/api/publicv1"
	auditpkg "github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/auth"
	authrbac "github.com/orvix/orvix/internal/auth/rbac"
	"github.com/orvix/orvix/internal/billing"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/coremail/dkim"
	"github.com/orvix/orvix/internal/coremail/mailpolicy"
	"github.com/orvix/orvix/internal/coremail/push"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	customerdomain "github.com/orvix/orvix/internal/customerdomain"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/dnsops"
	"github.com/orvix/orvix/internal/dnsops/providers"
	entrbac "github.com/orvix/orvix/internal/enterprise/rbac"
	"github.com/orvix/orvix/internal/license"
	"github.com/orvix/orvix/internal/metrics"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/observability"
	platformbilling "github.com/orvix/orvix/internal/platform/billing"
	"github.com/orvix/orvix/internal/platform/bulkprovision"
	"github.com/orvix/orvix/internal/platform/cluster"
	"github.com/orvix/orvix/internal/platform/deliverability"
	platformimporter "github.com/orvix/orvix/internal/platform/importer"
	platformjobs "github.com/orvix/orvix/internal/platform/jobs"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/platform/mailcontrol"
	"github.com/orvix/orvix/internal/platform/relay"
	"github.com/orvix/orvix/internal/platform/retention"
	"github.com/orvix/orvix/internal/ruler"
	orvixruntime "github.com/orvix/orvix/internal/runtime"
	settingsbridge "github.com/orvix/orvix/internal/settings/bridge"
	"github.com/orvix/orvix/internal/tlsmgmt"
	"github.com/orvix/orvix/internal/trust"
	"github.com/orvix/orvix/internal/trustmgmt"
	"github.com/orvix/orvix/internal/updater"
	"github.com/orvix/orvix/internal/webhooks"
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
	authLimiter  *auth.AuthLimiter
	logger       *zap.Logger
	cfg          *config.Config
	h            *handlers.Handler
	appCtx       context.Context
	cancel       context.CancelFunc
	db           *gorm.DB
	startOnce    sync.Once
	publicIdem   *kernel.IdempotencyStore
	platformIdem *kernel.IdempotencyStore

	bulkProvisionSvc *bulkprovision.Service
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
		//
		// The client-IP model (H-6): c.IP() is the ONLY client
		// address ever consumed by the authentication throttles,
		// the login flow, audit records and lockout keys. Fiber
		// resolves it as follows, and nothing in this codebase
		// reads a forwarding header directly:
		//
		//  1. If the immediate peer is NOT a trusted proxy
		//     (trusted = listed in TrustedProxies, a loopback
		//     address via Loopback:true, or a private/link-local
		//     address when those flags were enabled — none are),
		//     the X-Forwarded-For header is IGNORED and c.IP()
		//     is the socket peer. A random internet client
		//     therefore cannot spoof an IP.
		//  2. If the peer IS trusted, the X-Forwarded-For chain
		//     is walked right-to-left, skipping entries that are
		//     themselves trusted proxies, and the first valid
		//     untrusted address is returned. Malformed entries
		//     are skipped and every entry is validated with
		//     netip before it can be trusted.
		//  3. If every entry is trusted, the leftmost valid
		//     entry wins; with none, the socket peer wins.
		//
		// EnableIPValidation must stay true: without it, c.IP()
		// returns the RAW header verbatim for trusted peers,
		// i.e. any chain an upstream client can influence would
		// reach the limiter — including garbage that would land
		// in the shared "invalid" budget and arbitrary values
		// that would mint fresh per-IP budgets.
		ProxyHeader: fiber.HeaderXForwardedFor,
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Proxies:  cfg.Server.TrustedProxies,
			Loopback: true,
		},
		EnableIPValidation: true,
	})

	apikeyMgr := auth.NewAPIKeyManager(db, logger)
	var rateLimiter *auth.RedisRateLimiter
	if redisClient != nil {
		rateLimiter = auth.NewRedisRateLimiter(redisClient, logger)
	}
	// Multi-dimensional authentication limiter (H-6). The primary store is
	// Redis when one is configured (shared budget across nodes) and the
	// in-process store otherwise; the limiter itself degrades per-request to
	// an in-process fallback if the primary errors at runtime.
	var authLimiter *auth.AuthLimiter
	if redisClient != nil {
		authLimiter = auth.NewAuthLimiter(auth.NewRedisLimitStore(redisClient), auth.DefaultAuthLimitPolicy(), logger)
	} else {
		authLimiter = auth.NewAuthLimiter(nil, auth.DefaultAuthLimitPolicy(), logger)
	}

	ctx, cancel := context.WithCancel(context.Background())
	router := &Router{
		app:          app,
		auth:         authenticator,
		csrf:         auth.NewCSRFManager(db, logger, cfg.Server.TLSAuto),
		apikeys:      apikeyMgr,
		redisLimiter: rateLimiter,
		authLimiter:  authLimiter,
		logger:       logger,
		cfg:          cfg,
		appCtx:       ctx,
		cancel:       cancel,
		h:            handlers.NewHandler(db, authenticator, apikeyMgr, logger, cfg, registry, ff, rateLimiter),
		db:           db,
	}
	// Wire the multi-dimensional auth limiter (H-6) into the handler for
	// success-path resets. The middleware mounting it is built below in
	// authThrottle/authThrottleIP.
	router.h.SetAuthLimiter(authLimiter)
	if sqlDB, err := db.DB(); err == nil {
		dialect, detectErr := dbdialect.Detect(sqlDB)
		if detectErr != nil {
			dialect = dbdialect.FromDriver(cfg.Database.Driver)
		}
		router.publicIdem = kernel.NewIdempotencyStore(sqlDB, dialect)
		if err := router.publicIdem.EnsureSchema(ctx); err != nil {
			logger.Error("public API idempotency schema init failed", zap.Error(err))
			router.publicIdem = nil
		} else if _, err := router.publicIdem.PurgeBefore(ctx, time.Now().UTC().Add(-publicv1.IdempotencyRetention)); err != nil {
			logger.Warn("public API idempotency retention purge failed", zap.Error(err))
		}
		// Platform control-plane idempotency (relay create/update/
		// rotate/test). Same kernel store, distinct scope namespace.
		router.platformIdem = kernel.NewIdempotencyStore(sqlDB, dialect)
		if err := router.platformIdem.EnsureSchema(ctx); err != nil {
			logger.Error("platform idempotency schema init failed", zap.Error(err))
			router.platformIdem = nil
		}
		// Wire the platform control-plane idempotency store into the
		// handler (nil when the schema init failed — the relay
		// mutation handlers then fail closed with 503).
		router.h.SetPlatformIdempotencyStore(router.platformIdem)
	}

	// Cancel the background context when the Fiber app shuts
	// down, stopping the billing scheduler and any other
	// context-bound goroutines cleanly.
	app.Hooks().OnPreShutdown(func() error {
		cancel()
		return nil
	})
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
	var eng *coremail.Engine
	if sqlDB, err := db.DB(); err == nil {
		eng = coremail.NewEngine(coremail.EngineConfig{DB: sqlDB, AuthCfg: coremail.DefaultAuthConfig()})
		ws := webmailmgmt.NewService(eng, sqlDB)
		router.h.SetWebmailService(ws)

		// Wire customer domain administration service.
		domainRepo := coremail.NewDomainSQLRepo(sqlDB)
		dnsResolver := dnsops.NewNetResolver()
		// The canonical DNS expectations are read ONCE, here, from
		// config and handed to the inspector. Every downstream consumer
		// (live-record verification, the `expected` field in the API
		// response, the repair guidance, and the record file the console
		// downloads) reads them back out of the inspector, so no consumer
		// can drift away from the operator's configured policy.
		inspector := customerdomain.NewDNSInspector(dnsResolver).
			WithExpectations(customerdomain.CanonicalExpectations{
				SPFRecord:   cfg.CoreMail.ExpectedSPF,
				DMARCPolicy: cfg.CoreMail.ExpectedDMARCPolicy,
				DMARCRUA:    cfg.CoreMail.ExpectedDMARCRUA,
				SRVTarget:   cfg.CoreMail.AutodiscoverSRVTarget,
				SRVPort:     cfg.CoreMail.AutodiscoverSRVPort,
				SRVPriority: cfg.CoreMail.AutodiscoverSRVPriority,
				SRVWeight:   cfg.CoreMail.AutodiscoverSRVWeight,
			})
		verifRepo := customerdomain.NewVerificationRepo(sqlDB)
		if err := verifRepo.EnsureTable(context.Background()); err != nil {
			logger.Error("customer domain verification init failed, service disabled", zap.Error(err))
		} else {
			cds := customerdomain.NewService(sqlDB, domainRepo, inspector, verifRepo)
			router.h.SetCustomerDomainService(cds)
			logger.Info("customer domain administration service wired")
		}
	} else {
		logger.Warn("webmail service not available: failed to get sql.DB", zap.Error(err))
	}

	// Wire enterprise admin services (mailbox, org, domain, platform, dashboard).
	var platformSvc *platformpkg.PlatformService
	if sqlDB, err := db.DB(); err == nil {
		auditExtendedStore := auditpkg.NewExtendedStore(sqlDB)
		if err := auditExtendedStore.EnsureTable(context.Background()); err != nil {
			logger.Error("enterprise admin audit initialization failed; mutation services disabled", zap.Error(err))
		} else {
			rbacEval := entrbac.NewEvaluator(sqlDB)
			router.h.SetAuditExtendedStore(auditExtendedStore)

			adminMailboxRepo := mailboxadminsvc.NewAdminMailboxRepo(sqlDB)
			if eng == nil {
				eng = coremail.NewEngine(coremail.EngineConfig{DB: sqlDB, AuthCfg: coremail.DefaultAuthConfig()})
			}
			mailboxAdminSvc := mailboxadminsvc.NewService(adminMailboxRepo, eng.Auth, auditExtendedStore, rbacEval)
			router.h.SetMailboxAdminService(mailboxAdminSvc)
			orgRepo := orgadminsvc.NewOrganizationRepo(sqlDB)
			orgAdminSvc := orgadminsvc.NewService(orgRepo, auditExtendedStore, rbacEval)
			router.h.SetOrganizationAdminService(orgAdminSvc)

			domainAdminRepo := domainadminsvc.NewDomainAdminRepo(sqlDB)
			dkimRepo := dkim.NewSQLRepo(sqlDB)
			domainAdminSvc := domainadminsvc.NewService(domainAdminRepo, dkimRepo, auditExtendedStore, rbacEval)
			webhookDialect, dialectErr := dbdialect.Detect(sqlDB)
			if dialectErr != nil {
				webhookDialect = dbdialect.FromDriver("sqlite")
			}
			outboxRepo := kernel.NewOutboxRepository(webhookDialect)
			webhookRepo := webhooks.NewRepository(sqlDB)
			if err := outboxRepo.EnsureSchema(context.Background(), sqlDB); err != nil {
				logger.Error("webhook outbox initialization failed", zap.Error(err))
			} else if err := webhookRepo.EnsureSchema(context.Background()); err != nil {
				logger.Error("webhook schema initialization failed", zap.Error(err))
			} else {
				webhookSvc := webhooks.NewService(webhookRepo, nil).WithOutbox(outboxRepo)
				router.h.SetWebhookService(webhookSvc)
				domainAdminSvc.SetWebhookPublisher(webhooks.NewOutboxPublisher(outboxRepo))
				logger.Info("transactional webhook outbox wired")
			}
			router.h.SetDomainAdminService(domainAdminSvc)

			// Platform mail control: orchestrates the production admin
			// services for the platform_super_admin surface. Every
			// platform mail-control request requires an explicit target
			// tenant; the PSA never impersonates a tenant.
			mailControlRepo := mailcontrol.NewRepository(sqlDB)
			router.h.SetMailControlService(mailcontrol.NewService(mailControlRepo, mailcontrol.Ports{
				Domains: domainAdminSvc, Mailboxes: mailboxAdminSvc, Audit: auditExtendedStore, Outbox: outboxRepo,
			}))

			// Platform deliverability / suppression (Milestone 9 bounded
			// context): the production service already enforces
			// suppression in the real outbound path; these platform
			// routes expose safe management and metrics.
			deliverabilityRepo := deliverability.NewRepository(sqlDB)
			if err := deliverabilityRepo.EnsureSchema(context.Background()); err != nil {
				logger.Warn("platform deliverability schema init failed; suppression/metrics disabled", zap.Error(err))
			} else {
				router.h.SetDeliverabilityService(deliverability.NewService(deliverabilityRepo, auditExtendedStore, outboxRepo, nil))
			}

			bulkProvisionRepo := bulkprovision.NewRepository(sqlDB)
			if err := bulkProvisionRepo.EnsureSchema(context.Background()); err != nil {
				logger.Warn("bulk provisioning schema init failed; service disabled", zap.Error(err))
			} else {
				// Idempotency, outbox and audit were previously wired nil
				// here, silently disabling the service's own idempotent-
				// replay guard, its outbox evidence, and its audit trail
				// despite the service already supporting all three.
				bulkProvisionSvc := bulkprovision.NewService(bulkProvisionRepo, mailboxAdminSvc, domainAdminSvc, router.platformIdem, outboxRepo, auditExtendedStore, nil)
				router.bulkProvisionSvc = bulkProvisionSvc
				router.h.SetBulkProvisionService(bulkProvisionSvc)

				// Bulk mailbox uploads are staged through the SAME confined
				// staging primitive internal/platform/importer already
				// implements (confined paths, random server-generated IDs,
				// atomic fsync+rename writes, symlink rejection, hash
				// verification) — a dedicated subdirectory, not a second
				// staging subsystem.
				bulkStagingDir := cfg.Imports.StagingDir
				if bulkStagingDir == "" {
					bulkStagingDir = filepath.Join(os.TempDir(), "orvix-imports")
				}
				bulkStagingDir = filepath.Join(bulkStagingDir, "bulk-mailboxes")
				if err := os.MkdirAll(bulkStagingDir, 0o700); err != nil {
					logger.Warn("bulk mailbox staging directory could not be created; staging disabled", zap.Error(err))
				} else if bulkStaging, err := platformimporter.NewStagingService(bulkStagingDir); err != nil {
					logger.Warn("bulk mailbox staging initialization failed; staging disabled", zap.Error(err))
				} else {
					router.h.SetBulkMailboxStaging(bulkStaging)
				}
			}

			relayRepo, relayRepoErr := relay.NewRepositoryChecked(sqlDB)
			if relayRepoErr != nil {
				logger.Warn("relay control plane dialect detection failed; service disabled", zap.Error(relayRepoErr))
			} else if err := relayRepo.EnsureSchema(context.Background()); err != nil {
				logger.Warn("relay control plane schema init failed; service disabled", zap.Error(err))
			} else {
				relaySvc, relaySvcErr := relay.NewAdministrativeService(relayRepo, nil, outboxRepo, auditExtendedStore)
				if relaySvcErr != nil {
					logger.Warn("relay administrative evidence wiring failed; service disabled", zap.Error(relaySvcErr))
				} else {
					router.h.SetRelayService(relaySvc)
				}
			}

			dashboardSvc := dashboardsvc.NewDashboardService(sqlDB)
			router.h.SetDashboardService(dashboardSvc)

			platformSvc = platformpkg.NewPlatformService(sqlDB, auditExtendedStore, rbacEval, orgAdminSvc)
			router.h.SetPlatformAdminService(platformSvc)

			// Retention/legal-hold/compliance (Milestone 14) — the
			// purge target is the REAL mailbox soft-delete lifecycle
			// (Milestone 5), not a fake or disconnected table.
			retentionRepo := retention.NewRepository(sqlDB)
			if err := retentionRepo.EnsureSchema(context.Background()); err != nil {
				logger.Warn("retention schema init failed; service disabled", zap.Error(err))
			} else {
				purgeTarget := retention.NewMailboxPurgeAdapter(adminMailboxRepo)
				router.h.SetRetentionService(retention.NewService(retentionRepo, purgeTarget, auditExtendedStore, nil, nil))
			}

			// Manual credits/adjustments (Milestone 15).
			platformBillingRepo := platformbilling.NewRepository(sqlDB)
			if err := platformBillingRepo.EnsureSchema(context.Background()); err != nil {
				logger.Warn("platform billing schema init failed; service disabled", zap.Error(err))
			} else {
				router.h.SetPlatformBillingService(platformbilling.NewService(sqlDB, platformBillingRepo, auditExtendedStore, nil, nil))
			}

			logger.Info("enterprise admin services wired with transactional audit and RBAC")
		}

		if billingSvc, usageSvc, quotaSvc, webhookSvc, sched, enforcer, err := billing.Initialize(sqlDB); err != nil {
			logger.Error("billing initialization failed", zap.Error(err))
		} else {
			router.h.SetBillingService(billingSvc)
			router.h.SetBillingUsageService(usageSvc)
			router.h.SetBillingQuotaService(quotaSvc)
			router.h.SetSendEnforcer(enforcer)
			// PSA-created organizations need the same billing service the
			// tenant console uses so their subscription initialization is
			// consistent with self-signup.
			if billingSvc != nil && platformSvc != nil {
				platformSvc.SetBillingService(billingSvc)
			}
			logger.Info("billing services wired")

			abuseSvc := abuse.NewSignalService(sqlDB, abuse.NewRateLimitService(sqlDB))
			router.h.SetAbuseSignalService(abuseSvc)
			router.h.SetRateLimitService(abuse.NewRateLimitService(sqlDB))
			logger.Info("abuse services wired")

			if enforcer != nil {
				if mod, ok := registry.Get("coremail-runtime"); ok {
					if esc, ok := mod.(interface {
						SetSendEnforcerCallback(func(ctx context.Context, tenantID, mailboxID uint, count int) error, func(ctx context.Context, tenantID, mailboxID uint, count int, eventID string), func(ctx context.Context, tenantID, mailboxID uint, eventID string))
					}); ok {
						esc.SetSendEnforcerCallback(
							func(ctx context.Context, tenantID, mailboxID uint, count int) error {
								id := billing.SendIdentity{TenantID: tenantID, MailboxID: mailboxID}
								result := enforcer.AllowSend(ctx, id, count)
								if !result.Allowed {
									return fmt.Errorf("%s", result.Reason)
								}
								return nil
							},
							func(ctx context.Context, tenantID, mailboxID uint, count int, eventID string) {
								id := billing.SendIdentity{TenantID: tenantID, MailboxID: mailboxID}
								enforcer.RecordSend(ctx, id, eventID, count)
							},
							func(ctx context.Context, tenantID, mailboxID uint, eventID string) {
								id := billing.SendIdentity{TenantID: tenantID, MailboxID: mailboxID}
								enforcer.RecordBounce(ctx, id, eventID)
							},
						)
						logger.Info("SMTP send enforcement wired")
					}
				}
			}

			if sched != nil {
				router.h.SetBillingScheduler(sched)
				logger.Info("billing scheduler wired (call router.Start() to begin)")
			}
			if webhookSvc != nil {
				router.h.SetBillingWebhook(webhookSvc)
				router.h.SetInvoiceService(billing.NewInvoiceService(sqlDB))
				logger.Info("billing webhook service wired")
			}

			if provider, err := billing.NewPaymentProviderFromConfig(
				router.cfg.Payment.Provider,
				router.cfg.Payment.Secret,
				router.cfg.Payment.WebhookSecret,
				router.cfg.Payment.Enabled,
				time.Duration(router.cfg.Payment.WebhookToleranceSeconds)*time.Second,
			); err != nil {
				logger.Error("payment provider init failed", zap.Error(err))
			} else if provider != nil {
				router.h.SetPaymentProvider(provider)
				logger.Info("payment provider wired", zap.String("provider", router.cfg.Payment.Provider))
			} else {
				logger.Info("payment provider not configured — webhook returns 503")
			}
		}
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
		// Wire the canonical mailbox-level mail-access policy into the
		// webmail send path (MAILBOX-ACCESS-MODE-PHASE1). The policy
		// store is built over the same *sql.DB the runtime engine
		// uses; a schema failure degrades to "policy not wired" (the
		// pre-policy behavior) with an explicit log line.
		if sqlDB, err := db.DB(); err == nil {
			router.h.SetMailAccessPolicy(mailpolicy.New(mailpolicy.NewEngineStoreFromDB(sqlDB), nil))
			logger.Info("mail access policy wired for webmail send")
		} else {
			logger.Warn("mail access policy not wired: failed to get sql.DB", zap.Error(err))
		}
		// Wire the immutable delivery-attempt history repo
		// (Milestone 8) against the same table the delivery
		// workers already write to — not a separate history store.
		if sqlDB, err := db.DB(); err == nil {
			historyRepo, historyErr := delivery.NewAttemptHistorySQLRepoChecked(sqlDB)
			if historyErr != nil {
				logger.Warn("delivery history dialect detection failed; history API disabled", zap.Error(historyErr))
			} else {
				router.h.SetAttemptHistoryRepo(historyRepo)
			}
		}
		// Wire the cluster node registry service (Milestone 10) from
		// the same runtime module that self-enrolled at boot — the
		// admin API reads/commands the SAME node rows, not a second
		// registry.
		if clProvider, ok := mod.(interface {
			ClusterService() *cluster.Service
		}); ok {
			if cs := clProvider.ClusterService(); cs != nil {
				router.h.SetClusterService(cs)
				logger.Info("cluster service wired for admin API")
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

	// Wire the trust / login protection service.
	if sqlDB, err := db.DB(); err == nil {
		// Ensure the trust persistence tables exist before LoadFromDB using
		// dialect-correct DDL. On PostgreSQL these tables are owned by
		// models.MigrateAllPostgres; emitting SQLite-only DDL (AUTOINCREMENT/
		// DATETIME) there is a syntax error that used to be swallowed.
		trustDialect, derr := dbdialect.Detect(sqlDB)
		if derr != nil {
			trustDialect = dbdialect.FromDriver("sqlite")
		}
		for _, ddl := range trust.TablesForDialect(trustDialect) {
			if _, err := sqlDB.ExecContext(context.Background(), ddl); err != nil {
				logger.Warn("trust schema migration failed, falling back to in-memory", zap.Error(err))
			}
		}
		trustRepo := trust.NewRepository(sqlDB)
		trustEng := trust.NewEngineWithRepo(trustRepo)
		trustSvc := trustmgmt.NewService(trustEng)
		router.h.SetTrustService(trustSvc)
		if err := trustEng.LoadFromDB(context.Background()); err != nil {
			router.h.SetTrustPersistence(false, "Trust engine persistence load failed. Lockouts are tracked in-memory and reset on restart.")
			logger.Warn("trust engine load from DB failed, using in-memory only", zap.Error(err))
		} else {
			router.h.SetTrustPersistence(true, "")
		}
		logger.Info("trust service wired")
	}

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
		if das := router.h.DomainAdminService(); das != nil {
			das.SetTLSService(tlsSvc)
		}
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

	// Wire transactional mail sender for signup OTP, password resets, and
	// support requests. Prefers authenticated TLS submission when
	// transactional submission credentials are configured; falls back to
	// the legacy unauthenticated MX-port path otherwise (see
	// initTransactionalMailSender for why that path is relay-rejected for
	// external recipients).
	ms := initTransactionalMailSender(
		cfg.CoreMail.SMTPHost, cfg.CoreMail.SMTPPort,
		cfg.CoreMail.SubmissionHost, cfg.CoreMail.SubmissionPort,
		cfg.CoreMail.TransactionalSMTPUsername, cfg.CoreMail.TransactionalSMTPPassword,
		cfg.CoreMail.Hostname, logger,
	)
	router.h.SetMailSender(ms)

	if sqlDB, err := db.DB(); err == nil {
		jobRepo := platformjobs.NewJobRepository(sqlDB)
		jobRegistry := platformjobs.NewRegistry()
		if err := jobRepo.EnsureSchema(context.Background()); err != nil {
			logger.Error("automation jobs schema initialization failed", zap.Error(err))
		} else {
			jobService := platformjobs.NewServiceWithRegistry(jobRepo, jobRegistry, kernel.SystemClock{})

			// Wire the durable import service (Phase 4B): real repository,
			// a confined staging directory, the durable jobs service, and
			// concrete service adapters. The platform.import handler is
			// registered below so the worker can execute it.
			importRepo := platformimporter.NewRepository(sqlDB)
			if err := importRepo.EnsureSchema(context.Background()); err != nil {
				logger.Error("import schema initialization failed; import service disabled", zap.Error(err))
			} else {
				stagingDir := cfg.Imports.StagingDir
				if stagingDir == "" {
					stagingDir = filepath.Join(os.TempDir(), "orvix-imports")
				}
				if err := os.MkdirAll(stagingDir, 0o700); err != nil {
					logger.Error("import staging directory could not be created; import service disabled", zap.Error(err))
				} else {
					staging, err := platformimporter.NewStagingService(stagingDir)
					if err != nil {
						logger.Error("import staging initialization failed; import service disabled", zap.Error(err))
					} else if eng == nil {
						logger.Error("coremail engine unavailable; import service disabled")
					} else {
						importDialect, dialectErr := dbdialect.Detect(sqlDB)
						if dialectErr != nil {
							importDialect = dbdialect.FromDriver("sqlite")
						}
						adapters, err := platformimporter.NewProductionAdapters(platformimporter.ProductionAdapterDeps{
							OrgService:     router.h.OrganizationAdminService(),
							DomainService:  router.h.DomainAdminService(),
							MailboxService: router.h.MailboxAdminService(),
							DB:             sqlDB,
							Dialect:        importDialect,
						})
						if err != nil {
							logger.Error("import adapters initialization failed; import service disabled", zap.Error(err))
						} else {
							importSvc := platformimporter.NewService(importRepo, adapters, staging, jobService, kernel.SystemClock{})
							router.h.SetImportService(importSvc)
							logger.Info("durable import service wired")
						}
					}
				}
			}

			if router.bulkProvisionSvc != nil {
				if err := bulkprovision.RegisterImportJob(jobRegistry, router.bulkProvisionSvc); err != nil {
					logger.Error("bulk mailbox import job registration failed", zap.Error(err))
				}
			}

			if err := platformjobs.RegisterProductionHandlers(jobRegistry, router.h.CustomerDomainService(), router.h.WebhookService(), router.h.ImportService()); err != nil {
				logger.Error("automation jobs handler registration failed", zap.Error(err))
			} else {
				jobWorker := platformjobs.NewWorker(jobService, jobRegistry, "orvix-"+kernel.UUIDGenerator{}.NewID()).WithErrorHandler(func(err error) {
					logger.Error("automation jobs worker iteration failed", zap.Error(err))
				})
				router.h.SetAutomationJobs(jobService, jobWorker)
				logger.Info("durable automation jobs wired")
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

// SetTrustService wires the trust/lockout service into the router's handler.
func (r *Router) SetTrustService(s *trustmgmt.Service) {
	r.h.SetTrustService(s)
}

// SetTrustPersistence sets the trust engine persistence state.
func (r *Router) SetTrustPersistence(ok bool, errMsg string) {
	r.h.SetTrustPersistence(ok, errMsg)
}

// SetClusterService wires the cluster node registry service into the
// router's handler for test setups where the coremail runtime module
// (which normally self-enrolls and wires this) is not available —
// mirrors SetQueueEngine/SetTrustService above.
func (r *Router) SetClusterService(s *cluster.Service) {
	r.h.SetClusterService(s)
}

// SetMailSender overrides the router's transactional mail sender — mirrors
// SetQueueEngine/SetTrustService/SetClusterService above. Primarily for test
// setups that need a deterministic fake MailSender instead of the real
// SMTP-dialing sender initTransactionalMailSender wires by default.
func (r *Router) SetMailSender(m handlers.MailSender) {
	r.h.SetMailSender(m)
}

func (r *Router) App() *fiber.App { return r.app }

// Start begins background services (billing scheduler, etc).
// Called by the production entry point; test callers should NOT
// call Start to avoid background goroutines leaking.
// newUserRateLimiter builds a fiber limiter keyed by authenticated user id
// (falling back to client IP for unauthenticated callers), returning HTTP 429
// with a real integer-seconds Retry-After header and a stable JSON body when
// the per-window budget is exceeded. Extracted so the exact production limiter
// is exercised by tests (threshold, reset window, and user isolation).
func newUserRateLimiter(prefix string, max int, window time.Duration, retryAfterSeconds int, message string) fiber.Handler {
	retry := strconv.Itoa(retryAfterSeconds)
	return limiter.New(limiter.Config{
		Max:        max,
		Expiration: window,
		KeyGenerator: func(c fiber.Ctx) string {
			if uid, ok := c.Locals("user_id").(uint); ok && uid > 0 {
				return fmt.Sprintf("%s:%d", prefix, uid)
			}
			return prefix + ":" + c.IP()
		},
		LimitReached: func(c fiber.Ctx) error {
			c.Set("Retry-After", retry)
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"error":       message,
				"retry_after": retryAfterSeconds,
			})
		},
	})
}

func (r *Router) Start() {
	r.startOnce.Do(func() {
		r.h.StartBillingScheduler(r.appCtx, 15*time.Minute)
		r.h.StartWebhookWorker(r.appCtx, time.Second)
		r.h.StartAutomationWorker(r.appCtx)
		r.h.StartDeliverabilityScheduler(r.appCtx, 15*time.Minute)
	})
}

// Shutdown cancels the router's background context, stopping
// the billing scheduler and any other context-bound goroutines.
// Call during application shutdown or test cleanup.
func (r *Router) Shutdown() error {
	r.cancel()
	return r.app.Shutdown()
}

// allowedOrigins returns the trusted Origin allow-list for browser
// requests. It mirrors the CORS configuration in setupMiddleware:
// cfg.Server.AllowedOrigins, falling back to the same localhost
// defaults when unset.
func (r *Router) allowedOrigins() []string {
	origins := r.cfg.Server.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"http://localhost:3000", "http://localhost:3001"}
	}
	// A wildcard origin is incompatible with credentials (the CORS setup
	// replaces it with the same localhost fallback); failing to do the
	// same here would make the exact-match check reject every request
	// because the literal string "*" never equals a real Origin.
	if len(origins) == 1 && origins[0] == "*" {
		origins = []string{"http://localhost:3000", "http://localhost:3001"}
	}
	return origins
}

// authThrottle returns the multi-dimensional throttle applied to every
// endpoint that accepts credentials (login, signup, forgot-password).
//
// H-6: previously only /api/v1/auth/login and /api/v1/webmail/login were
// throttled, and each call site duplicated the Redis/in-memory choice. That
// left /admin/login, /auth/mfa/verify, /auth/signup and /auth/reset-password
// completely unlimited. Centralising it here means a new credential endpoint
// cannot silently ship without a limiter. The old budget was a single
// per-IP counter (5 / 15 min), which a distributed attacker trivially
// rotates around; the replacement enforces three dimensions together — per
// IP, per account, per (IP, account) pair — on Redis when configured and on
// an in-process store otherwise (see internal/auth/authlimit.go).
//
// The client address is c.IP(), resolved by the router's trusted-proxy
// model (see fiber.Config above); the account is read from the request body
// by CredentialAccountFromBody, which only ever reads identifier fields.
//
// Degradation: a primary-store error falls back to an in-process budget
// (loudly logged); only if BOTH stores fail is the request refused, so a
// Redis outage can neither open the endpoint nor brick every login.
func (r *Router) authThrottle() fiber.Handler {
	return auth.AuthLimitMiddleware(r.authLimiter, auth.CredentialAccountFromBody, r.logger)
}

// authThrottleIP is the same throttle without an account dimension, for
// credential endpoints whose request body carries no account identifier
// (MFA verification, password reset): only the client-IP budget applies,
// which still stops a single host hammering the endpoint.
func (r *Router) authThrottleIP() fiber.Handler {
	return auth.AuthLimitMiddleware(r.authLimiter, auth.NoAccount, r.logger)
}

// isAllowedOrigin reports whether a browser request's Origin header is
// trusted for the public CSRF bootstrap endpoint. It uses the existing
// trusted-origin/CORS policy: the Origin must be listed in
// cfg.Server.AllowedOrigins (with the same localhost defaults used by the
// CORS middleware when unset). An untrusted cross-origin page is rejected
// before a CSRF token or cookie is created. Same-origin GET bootstrap
// requests (the admin/webmail login SPA) carry no Origin header at all and
// are therefore allowed by the handler.
func (r *Router) isAllowedOrigin(origin string) bool {
	for _, o := range r.allowedOrigins() {
		if o == origin {
			return true
		}
	}
	return false
}

func (r *Router) setupMiddleware() {
	r.app.Use(fiberrecover.New())
	origins := r.cfg.Server.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"http://localhost:3000", "http://localhost:3001"}
	}
	// Reject wildcard origins when credentials are enabled
	// (CORS safety requirement). If the config has a wildcard
	// and credentials are true, enforce the wildcard rejection
	// at startup so the admin SPA cannot be loaded from an
	// attacker-controlled origin with cookies attached.
	allowOrigins := origins
	if len(allowOrigins) == 1 && allowOrigins[0] == "*" {
		allowOrigins = []string{"http://localhost:3000", "http://localhost:3001"}
		r.logger.Warn("CORS: wildcard origin replaced with localhost defaults because AllowCredentials=true")
	}
	r.app.Use(cors.New(cors.Config{
		AllowOrigins:     allowOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-CSRF-Token", "Idempotency-Key", "X-Request-ID"},
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
	// Credential endpoints retain their tighter multi-dimensional
	// budget (IP 20, account 5, combo 5 / 15 min — see
	// authThrottle/authThrottleIP above) and do not pass through
	// this handler. Security is unchanged — only the scope of
	// the limit changed.
}

// csrfBootstrapPath is the one route explicitly exempted from the general
// API budget below — see apiRateLimitMiddleware's doc comment for why.
const csrfBootstrapPath = "/api/v1/csrf-token"

// apiRateLimitMiddleware returns the general API rate limiter
// middleware for the /api/v1 group. It is built once in setupRoutes
// and mounted only on the API group, so SPA static routes are
// never counted against the per-IP budget. Credential endpoints get
// the dedicated multi-dimensional throttle and do NOT also pass
// through this handler, by mounting order.
//
// GET /api/v1/csrf-token is explicitly exempted here. Fiber v3's
// Group(prefix, middleware) registers that middleware as a
// prefix-matching USE route (see (*fiber.App).Group -> app.register
// with methodUse): it matches every request whose path starts with
// the prefix, regardless of which Router object the final route was
// added through. Simply registering /api/v1/csrf-token outside the
// `api` group variable (e.g. directly on r.app) does NOT exempt it,
// because its path still starts with "/api/v1" and this middleware is
// still mounted on that whole prefix. The only real way to exempt one
// path from a prefix-mounted middleware is an explicit check inside
// the middleware itself, as done here.
//
// This exemption exists because /api/v1/csrf-token previously shared
// this general 100-req/min-per-IP budget with EVERY other /api/v1
// call. A burst of ordinary API traffic (or one heavy admin/testing
// session) could exhaust it and then lock every client behind that IP
// out of ever bootstrapping a fresh CSRF token again until the window
// rolled over — breaking login and webmail for legitimate traffic, not
// abuse. Observed live 2026-08-14. csrf-token now relies solely on its
// own dedicated, isolated budget — see csrfBootstrapRateLimitMiddleware,
// mounted directly on that route.
func (r *Router) apiRateLimitMiddleware() fiber.Handler {
	var inner fiber.Handler
	if r.redisLimiter != nil {
		inner = r.redisLimiter.Middleware()
	} else {
		// NOTE: Expiration is a time.Duration. The previous literal
		// `60 * 1000` was 60,000 NANOSECONDS (0.06 ms), so the
		// no-Redis fallback window expired between consecutive
		// requests and the general API limiter never actually limited
		// anything — a silently-open API budget for single-node
		// deployments. A real 1-minute window is intended.
		inner = limiter.New(limiter.Config{Max: 100, Expiration: time.Minute})
	}
	return func(c fiber.Ctx) error {
		if c.Path() == csrfBootstrapPath {
			return c.Next()
		}
		return inner(c)
	}
}

// csrfBootstrapRateLimitMiddleware returns a DEDICATED, generous per-IP
// budget for GET /api/v1/csrf-token, isolated from both the general API
// budget (apiRateLimitMiddleware) and every credential-attempt budget
// (authThrottle/authThrottleIP). Mounted directly on the csrf-token
// route in place of the general limiter — see RedisRateLimiter.
// CSRFBootstrapMiddleware's doc comment for why sharing that budget was
// a real outage risk: a burst of ordinary API traffic could exhaust it
// and lock every client behind that IP out of ever obtaining a fresh
// CSRF token. Same fail-open-on-Redis-error behavior as the general
// limiter (never block login/webmail bootstrap on a Redis outage); the
// in-memory fallback uses the exact same newUserRateLimiter helper and
// budget as the Redis path for parity on a single-node deployment.
func (r *Router) csrfBootstrapRateLimitMiddleware() fiber.Handler {
	if r.redisLimiter != nil {
		return r.redisLimiter.CSRFBootstrapMiddleware()
	}
	return newUserRateLimiter("ratelimit:csrf", 60, time.Minute, 60, "too many token requests, try again later")
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

	// Mail client setup — Outlook Autodiscover (WEBMAIL-CLIENT-SETUP-1A).
	// Both the lowercase (`/autodiscover/autodiscover.xml`) and
	// uppercase (`/Autodiscover/Autodiscover.xml`) paths are
	// served; Outlook's hard-coded behaviour varies by build.
	// Each path supports GET and POST. No auth, no CSRF — the
	// caller is an email client that has not authenticated yet.
	// Domain validation goes through `coremail_domains`. See
	// internal/api/handlers/autodiscover.go for the full
	// security model and schema references.
	r.app.Get("/autodiscover/autodiscover.xml", r.h.OutlookAutodiscoverXML)
	r.app.Post("/autodiscover/autodiscover.xml", r.h.OutlookAutodiscoverXML)
	r.app.Get("/Autodiscover/Autodiscover.xml", r.h.OutlookAutodiscoverXMLUpper)
	r.app.Post("/Autodiscover/Autodiscover.xml", r.h.OutlookAutodiscoverXMLUpper)
	// Mozilla Thunderbird autoconfig — the canonical ISPDB path
	// (`/.well-known/autoconfig/mail/config-v1.1.xml`) plus the
	// secondary `/mail/config-v1.1.xml` fallback some clients
	// probe.
	r.app.Get("/.well-known/autoconfig/mail/config-v1.1.xml", r.h.MozillaAutoconfig)
	r.app.Get("/mail/config-v1.1.xml", r.h.MozillaAutoconfigFallback)

	// All `/api/v1/*` requests pass through the general rate
	// limiter (100/min per IP by default, via Redis when wired).
	// Static SPA routes (`/admin/*`, `/webmail/*`, `/`, mta-sts)
	// are registered on `r.app` directly and DO NOT pass through
	// this handler — so loading the admin UI no longer eats the
	// per-IP API budget.
	api := r.app.Group("/api/v1", r.apiRateLimitMiddleware())
	api.Get("/health", r.h.Health)
	api.Get("/billing/plans", r.h.ListBillingPlans)
	api.Post("/billing/webhook", r.h.ReceivePaymentWebhook)
	api.Post("/billing/complaint", r.h.ReceiveComplaintWebhook)
	api.Get("/status", r.h.GetPublicStatus)

	// Stable tenant-facing automation API. Authentication is API-key only;
	// it intentionally does not fall through to browser JWT sessions. Every
	// operation declares one explicit public scope and all resource IDs are
	// resolved inside the tenant bound to the validated key.
	publicAPI := api.Group("/public", publicv1.Correlation(), r.apikeys.PublicMiddleware())
	publicMutation := func(c fiber.Ctx) error {
		if r.publicIdem == nil {
			return publicv1.WriteError(c, fiber.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Idempotent mutations are unavailable.")
		}
		return publicv1.Idempotent(r.publicIdem)(c)
	}
	publicAPI.Get("/organization", publicv1.RequireScope(publicv1.ScopeOrganizationRead), r.h.PublicOrganization)
	publicAPI.Get("/usage", publicv1.RequireScope(publicv1.ScopeUsageRead), r.h.PublicUsage)
	publicAPI.Get("/domains", publicv1.RequireScope(publicv1.ScopeDomainsRead), r.h.PublicListDomains)
	publicAPI.Get("/domains/:id", publicv1.RequireScope(publicv1.ScopeDomainsRead), r.h.PublicGetDomain)
	publicAPI.Post("/domains", publicv1.RequireScope(publicv1.ScopeDomainsWrite), publicMutation, r.h.PublicCreateDomain)
	publicAPI.Patch("/domains/:id", publicv1.RequireScope(publicv1.ScopeDomainsWrite), publicMutation, r.h.PublicUpdateDomain)
	publicAPI.Post("/domains/:id/status", publicv1.RequireScope(publicv1.ScopeDomainsWrite), publicMutation, r.h.PublicSetDomainStatus)
	publicAPI.Delete("/domains/:id", publicv1.RequireScope(publicv1.ScopeDomainsWrite), publicMutation, r.h.PublicDeleteDomain)
	publicAPI.Get("/mailboxes", publicv1.RequireScope(publicv1.ScopeMailboxesRead), r.h.PublicListMailboxes)
	publicAPI.Get("/mailboxes/:id", publicv1.RequireScope(publicv1.ScopeMailboxesRead), r.h.PublicGetMailbox)
	publicAPI.Post("/mailboxes", publicv1.RequireScope(publicv1.ScopeMailboxesWrite), publicMutation, r.h.PublicCreateMailbox)
	publicAPI.Patch("/mailboxes/:id", publicv1.RequireScope(publicv1.ScopeMailboxesWrite), publicMutation, r.h.PublicUpdateMailbox)
	publicAPI.Post("/mailboxes/:id/status", publicv1.RequireScope(publicv1.ScopeMailboxesWrite), publicMutation, r.h.PublicSetMailboxStatus)
	publicAPI.Delete("/mailboxes/:id", publicv1.RequireScope(publicv1.ScopeMailboxesWrite), publicMutation, r.h.PublicDeleteMailbox)
	publicAPI.Get("/aliases", publicv1.RequireScope(publicv1.ScopeAliasesRead), r.h.PublicListAliases)
	publicAPI.Get("/aliases/:id", publicv1.RequireScope(publicv1.ScopeAliasesRead), r.h.PublicGetAlias)
	publicAPI.Post("/aliases", publicv1.RequireScope(publicv1.ScopeAliasesWrite), publicMutation, r.h.PublicCreateAlias)
	publicAPI.Patch("/aliases/:id", publicv1.RequireScope(publicv1.ScopeAliasesWrite), publicMutation, r.h.PublicUpdateAlias)
	publicAPI.Delete("/aliases/:id", publicv1.RequireScope(publicv1.ScopeAliasesWrite), publicMutation, r.h.PublicDeleteAlias)
	publicAPI.Get("/groups", publicv1.RequireScope(publicv1.ScopeGroupsRead), r.h.PublicListGroups)
	publicAPI.Get("/groups/:id", publicv1.RequireScope(publicv1.ScopeGroupsRead), r.h.PublicGetGroup)
	publicAPI.Post("/groups", publicv1.RequireScope(publicv1.ScopeGroupsWrite), publicMutation, r.h.PublicCreateGroup)
	publicAPI.Patch("/groups/:id", publicv1.RequireScope(publicv1.ScopeGroupsWrite), publicMutation, r.h.PublicUpdateGroup)
	publicAPI.Delete("/groups/:id", publicv1.RequireScope(publicv1.ScopeGroupsWrite), publicMutation, r.h.PublicDeleteGroup)
	publicAPI.Get("/groups/:id/members", publicv1.RequireScope(publicv1.ScopeGroupsRead), r.h.PublicListGroupMembers)
	publicAPI.Post("/groups/:id/members", publicv1.RequireScope(publicv1.ScopeGroupsWrite), publicMutation, r.h.PublicAddGroupMember)
	publicAPI.Delete("/groups/:id/members/:memberId", publicv1.RequireScope(publicv1.ScopeGroupsWrite), publicMutation, r.h.PublicDeleteGroupMember)

	// authThrottle is applied to every credential-accepting endpoint below.
	// Endpoints whose bodies carry an account identifier (login, signup,
	// forgot-password) get the full IP+account+combo budget; endpoints with
	// no account identifier in the body (MFA verification, password reset)
	// get the IP budget via authThrottleIP.
	loginGroup := api.Group("/auth")
	loginGroup.Post("/login", r.authThrottle(), r.h.Login)
	loginGroup.Post("/refresh", r.h.Refresh)

	// MFA login verification (public — no auth middleware).
	// Exchanges a password-based MFA challenge token + TOTP/recovery code
	// for real access/refresh tokens. Mounted on the public login group
	// so MFA-enabled users can complete login without being authenticated.
	//
	// H-6: this endpoint is now throttled. Without it, a stolen password plus
	// an unbounded number of TOTP guesses inside the challenge window defeats
	// MFA; the handler additionally caps attempts per individual challenge.
	loginGroup.Post("/mfa/verify", r.authThrottleIP(), r.h.MFALoginVerify)

	// Customer portal auth (public — no auth middleware).
	// H-6: signup is throttled to stop automated account/tenant creation.
	loginGroup.Post("/signup", r.authThrottle(), r.h.Signup)
	loginGroup.Post("/forgot-password", r.authThrottle(), r.h.ForgotPassword)
	loginGroup.Post("/reset-password", r.authThrottleIP(), r.h.ResetPassword)

	// Phase D/E: email-OTP-verified signup (two-step flow). All three
	// endpoints carry an email in the body, so the full IP+account+combo
	// throttle applies, same as /auth/signup.
	loginGroup.Post("/signup/start", r.authThrottle(), r.h.SignupStart)
	loginGroup.Post("/signup/resend", r.authThrottle(), r.h.SignupResend)
	loginGroup.Post("/signup/verify", r.authThrottle(), r.h.SignupVerify)

	// H-6: /admin/login is the admin SPA's form target. It was registered on
	// the ROOT app, outside the API group, and so carried no limiter at all —
	// unbounded password guessing against the highest-value accounts on the
	// platform.
	r.app.Post("/admin/login", r.authThrottle(), r.h.Login)

	// Webmail authentication (public — no auth middleware).
	//
	// /api/v1/webmail/login is the form submission. The
	// session probe (/api/v1/webmail/session) is on the
	// protected group below so the auth middleware
	// rejects missing/invalid cookies with 401 before
	// the handler runs — the gate uses that 401 as the
	// "show the login form" signal.
	webmailLoginGroup := api.Group("/webmail")
	webmailLoginGroup.Post("/login", r.authThrottle(), r.h.WebmailLogin)

	// Public CSRF bootstrap for the unauthenticated admin/webmail login
	// pages. The SPA calls GET /api/v1/csrf-token BEFORE the user has any
	// credentials, so this MUST be registered outside the JWT/API-key
	// protected group (otherwise the auth middleware returns 401 and the
	// login page can never obtain a double-submit CSRF token).
	//
	// Registered on r.app directly (full path), NOT via the `api` group,
	// for the same reason /admin/login is above: the `api` group carries
	// apiRateLimitMiddleware()'s general 100-req/min-per-IP budget shared
	// by every /api/v1 route. Every ordinary page load/reload — plus every
	// automatic 403-retry-with-fresh-token on a mutation — calls this
	// endpoint, so that shared budget meant a burst of ordinary API
	// traffic from one client IP could exhaust it and lock every user
	// behind that IP out of ever obtaining a CSRF token again until the
	// window rolled over: a full login/webmail outage caused by
	// legitimate traffic, observed live on 2026-08-14. This route now
	// gets its own dedicated, generous, isolated budget instead — see
	// csrfBootstrapRateLimitMiddleware / RedisRateLimiter.
	// CSRFBootstrapMiddleware.
	//
	// Security posture (unchanged from before this move):
	//   - Reuses the existing CSRFManager.GenerateToken (random token stored
	//     as a SHA-256 hash + double-submit cookie). CSRF validation is
	//     unchanged and never weakened.
	//   - A browser bootstrap request with a cross-origin Origin header is
	//     rejected (403) unless the Origin is in the trusted
	//     cfg.Server.AllowedOrigins list — and it is rejected BEFORE any
	//     token or cookie is created. Same-origin GET bootstrap requests
	//     (the login SPA) carry no Origin header at all and are allowed.
	//   - Cache-Control: no-store so a shared/forward cache can never serve a
	//     token to a different user.
	//   - The response body contains only the CSRF token; no user, tenant,
	//     or session data is ever included.
	r.app.Get("/api/v1/csrf-token", r.csrfBootstrapRateLimitMiddleware(), func(c fiber.Ctx) error {
		if origin := c.Get("Origin"); origin != "" && !r.isAllowedOrigin(origin) {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "origin not allowed",
			})
		}
		c.Set("Cache-Control", "no-store")
		userID, _ := c.Locals("user_id").(uint)
		token, err := r.csrf.GenerateToken(c, userID)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "csrf token generation failed"})
		}
		return c.JSON(fiber.Map{"csrf_token": token})
	})

	// TenantMiddleware resolves tenant_id from the authenticated user
	// row and stores it in c.Locals so handlers can scope mailbox/domain
	// lookups to the caller's own tenant (see Handler.callerOwnsTenant).
	// It must run after auth (which sets user_id) and before any handler.
	protected := api.Group("", r.apikeys.Middleware(), r.auth.Middleware(), auth.TenantMiddleware(r.db))
	protected.Get("/me", r.h.Me)

	// Account endpoints — own profile, sessions, preferences. Authenticated user only.
	protected.Get("/account/profile", r.h.GetAccountProfile)
	protected.Patch("/account/profile", r.h.UpdateAccountProfile)
	protected.Get("/account/preferences", r.h.GetAccountPreferences)
	protected.Patch("/account/preferences", r.h.UpdateAccountPreferences)
	protected.Get("/account/notification-preferences", r.h.GetNotificationPreferences)
	protected.Patch("/account/notification-preferences", r.h.UpdateNotificationPreferences)
	protected.Get("/account/sessions", r.h.ListAccountSessions)
	protected.Post("/account/sessions/:id/revoke", r.h.RevokeAccountSession)
	// Support requests: dedicated rate limit — 5 requests per 10 minutes per user.
	supportLimiter := newUserRateLimiter("support_req", 5, 10*time.Minute, 600, "too many support requests, please try again later")
	protected.Post("/account/support-requests", supportLimiter, r.h.SubmitSupportRequest)
	// MFA: dedicated per-user rate limit — 10 attempts per 15 minutes.
	mfaLimiter := newUserRateLimiter("mfa_req", 10, 15*time.Minute, 900, "too many MFA attempts, please try again later")
	protected.Get("/account/mfa/status", r.h.AccountMFAStatus)
	protected.Post("/account/mfa/setup", mfaLimiter, r.h.AccountMFASetup)
	protected.Post("/account/mfa/verify", mfaLimiter, r.h.AccountMFAVerify)
	protected.Post("/account/mfa/disable", mfaLimiter, r.h.AccountMFADisable)
	protected.Post("/account/mfa/recovery-codes/regenerate", mfaLimiter, r.h.AccountMFARegenerateRecoveryCodes)

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
	// H-1: every cookie-authenticated webmail MUTATION carries the canonical
	// CSRF middleware plus a strict JSON content-type guard, applied
	// PER-ROUTE. Effective middleware order is therefore
	// authentication -> portal/tenant context (from the `protected` group)
	// -> CSRF -> content type -> handler; the handlers still perform their
	// own mailbox-ownership authorization via resolveWebmailUserContext.
	//
	// These are deliberately NOT mounted with protected.Group("", ...):
	// an empty-prefix group installs its middleware for every route
	// registered on the parent group afterwards, which would silently apply
	// the webmail JSON-only rule to unrelated admin routes further down (and
	// did — it broke the multipart send route). Per-route registration keeps
	// the blast radius exactly equal to the route list below.
	//
	// Read-only GETs are left bare: they change no state, and the CSRF
	// middleware no-ops on safe methods anyway.
	//
	// webmailSendCT additionally permits multipart/form-data because
	// /webmail/send is the only webmail route that genuinely parses an
	// upload (webmailParseMultipartSend, for attachments). Every other
	// mutation is JSON-only, so a plain cross-site HTML <form> — which can
	// only emit urlencoded/multipart/text-plain — is rejected with 415
	// before the handler runs, independently of the CSRF check.
	webmailCSRF := r.csrf.Middleware()
	webmailJSONCT := auth.RequireJSONContentType()
	webmailSendCT := auth.RequireJSONContentType(auth.AllowMultipart())

	protected.Get("/webmail/session", r.h.WebmailSession)
	protected.Get("/webmail/me", r.h.WebmailMe)
	protected.Get("/webmail/folders", r.h.WebmailFolders)
	protected.Get("/webmail/messages", r.h.WebmailMessages)
	protected.Get("/webmail/messages/:id", r.h.WebmailMessage)
	protected.Patch("/webmail/messages/:id", webmailCSRF, webmailJSONCT, r.h.WebmailUpdateMessage)
	protected.Post("/webmail/messages/:id/archive", webmailCSRF, webmailJSONCT, r.h.WebmailArchive)
	protected.Post("/webmail/messages/:id/delete", webmailCSRF, webmailJSONCT, r.h.WebmailDelete)
	// New in Webmail Enterprise 2: per-message source
	// download, single-message move, multi-message batch
	// operations. All behind the same protected group as
	// the other state-changing webmail endpoints, so the
	// auth middleware rejects missing/invalid cookies
	// with 401 before the handler runs.
	protected.Get("/webmail/messages/:id/source", r.h.WebmailMessageSource)
	protected.Post("/webmail/messages/:id/move", webmailCSRF, webmailJSONCT, r.h.WebmailMoveMessage)
	protected.Post("/webmail/messages/batch", webmailCSRF, webmailJSONCT, r.h.WebmailMessageBatch)
	// Attachment download / preview. The :id is parsed
	// with parseMessageID (digits only) and the
	// handler confirms the attachment's parent message
	// belongs to the caller's mailbox before opening
	// the file. Returns 404 to non-owners so the
	// response shape does not leak existence.
	protected.Get("/webmail/attachments/:id", r.h.WebmailAttachmentDownload)
	protected.Get("/webmail/attachments/:id/preview", r.h.WebmailAttachmentPreview)
	protected.Post("/webmail/folders/:id/read-all", webmailCSRF, webmailJSONCT, r.h.WebmailMarkFolderRead)
	protected.Post("/webmail/send", webmailCSRF, webmailSendCT, r.h.WebmailSend)
	// Drafts — minimal CRUD. Drafts are Message rows
	// with Draft=true in the Drafts system folder; no
	// separate draft table, no schema change.
	protected.Get("/webmail/drafts", r.h.WebmailListDrafts)
	protected.Post("/webmail/drafts", webmailCSRF, webmailJSONCT, r.h.WebmailSaveDraft)
	protected.Get("/webmail/drafts/:id", r.h.WebmailGetDraft)
	protected.Put("/webmail/drafts/:id", webmailCSRF, webmailJSONCT, r.h.WebmailSaveDraft)
	protected.Delete("/webmail/drafts/:id", webmailCSRF, webmailJSONCT, r.h.WebmailDeleteDraft)
	// Push notification subscription management.
	protected.Post("/webmail/push/subscribe", webmailCSRF, webmailJSONCT, r.h.PushSubscribe)
	protected.Post("/webmail/push/unsubscribe", webmailCSRF, webmailJSONCT, r.h.PushUnsubscribe)
	protected.Get("/webmail/push/status", r.h.PushStatus)
	protected.Post("/webmail/push/test", webmailCSRF, webmailJSONCT, r.h.PushTest)

	// User settings — per-mailbox profile / appearance / compose /
	// mail behavior / notification preferences. Auth + mailbox
	// ownership enforced by resolveWebmailUserContext inside the
	// handlers; no id is taken from the request body.
	protected.Get("/webmail/settings", r.h.WebmailGetSettings)
	protected.Put("/webmail/settings", webmailCSRF, webmailJSONCT, r.h.WebmailPutSettings)

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
	protected.Post("/webmail/rules", webmailCSRF, webmailJSONCT, r.h.WebmailCreateRule)
	protected.Put("/webmail/rules/:id", webmailCSRF, webmailJSONCT, r.h.WebmailUpdateRule)
	protected.Delete("/webmail/rules/:id", webmailCSRF, webmailJSONCT, r.h.WebmailDeleteRule)
	protected.Get("/webmail/vacation", r.h.WebmailGetVacation)
	protected.Put("/webmail/vacation", webmailCSRF, webmailJSONCT, r.h.WebmailPutVacation)
	protected.Get("/webmail/forwarding", r.h.WebmailGetForwarding)
	protected.Put("/webmail/forwarding", webmailCSRF, webmailJSONCT, r.h.WebmailPutForwarding)

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
	// Webmail Change Password. Same CSRF + auth shape as
	// logout. The handler ignores any id in the body
	// and operates on the mailbox the JWT resolves to,
	// so cross-mailbox password changes are impossible.
	authCSRF.Post("/webmail/password/change", r.h.WebmailChangePassword)

	requireTenantContext := func(c fiber.Ctx) error {
		if _, err := auth.RequireTenantID(c); err != nil {
			return err
		}
		return c.Next()
	}

	// Customer domain administration. Mounted on the protected group
	// (auth + tenant middleware) without admin role restriction so
	// regular tenant users can view and verify their own domains.
	// The service layer enforces tenant isolation: every query is
	// scoped to the caller's tenant_id.
	protected.Get("/customer/domains", r.h.ListCustomerDomains)
	protected.Get("/customer/domains/:domain_id", r.h.GetCustomerDomain)
	protected.Get("/customer/domains/:domain_id/dns", r.h.GetCustomerDomainDNS)
	protected.Post("/customer/domains/:domain_id/verify", r.h.VerifyCustomerDomain)

	// Enterprise customer administration (tenant-scoped, CSRF-protected).
	// All operations are scoped to the caller's tenant_id.
	//
	// GET routes (enterpriseRead): role-gated to the canonical tenant
	// administration family (tenant_admin, tenant_operator,
	// tenant_support, tenant_readonly) with valid tenant context. RoleUser
	// (per-mailbox webmail end-user) and RoleBilling (unsupported billing
	// persona) are denied the entire console — reads of domains, mailboxes
	// and members are still Organization administration surface.
	//
	// POST/PATCH/DELETE routes: each capability group has its own exact
	// permission guard. A caller with domains.write cannot reach mailbox
	// mutations, and vice versa.
	//
	// Platform admin routes (admin group) remain separate and continue to
	// require RoleAdmin / RoleSuperAdmin.

	// Detect dialect once for requireTenantActive SQL generation.
	var activeDialect *dbdialect.Info
	if r.db != nil {
		if sqlDB, err := r.db.DB(); err == nil {
			if d, err := dbdialect.Detect(sqlDB); err == nil {
				activeDialect = d
			}
		}
	}
	if activeDialect == nil {
		activeDialect = dbdialect.FromDriver("sqlite")
	}

	requireTenantActive := func(c fiber.Ctx) error {
		if c.Method() == "GET" || c.Method() == "HEAD" || c.Method() == "OPTIONS" {
			return c.Next()
		}
		tenantID, err := auth.RequireTenantID(c)
		if err != nil {
			return err
		}
		if r.db == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "organization is suspended or inactive",
			})
		}
		sqlDB, err := r.db.DB()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "database not available",
			})
		}
		query := "SELECT 1 FROM tenants WHERE id = " + activeDialect.Placeholder(1) +
			" AND active = " + activeDialect.TrueLiteral() +
			" AND deleted_at IS NULL"
		var ok int
		if err := sqlDB.QueryRow(query, tenantID).Scan(&ok); err != nil || ok != 1 {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "organization is suspended or inactive",
			})
		}
		return c.Next()
	}

	// This route intentionally sits outside enterpriseRead: a platform
	// support actor has no permanent tenant membership, so enterpriseRead's
	// requireTenantContext would reject it before SupportAccess can establish
	// the grant-scoped, request-local tenant context. The support gate admits
	// the normal tenant role family unchanged and requires an active
	// read_only grant for platform actors.
	protected.Get("/enterprise/organizations/current", r.h.SupportAccessMiddlewareForScope("read_only"), r.csrf.Middleware(), r.h.GetCurrentOrganization)

	// The /enterprise/* group IS the customer Organization Admin console.
	// It is role-gated to the canonical tenant administration family
	// (tenant_admin, tenant_operator, tenant_support, tenant_readonly) —
	// the same family tenantCompatMW admits. RoleUser (per-mailbox webmail
	// end-user) and RoleBilling (unsupported billing-only persona) must
	// NOT reach any of these endpoints, even read-only ones: reads of
	// domains/mailboxes/members are still Organization administration
	// surface. Per-route authrbac.Require(...) gates below decide read vs
	// write within the family; this gate keeps non-administrative roles
	// entirely out of the console.
	enterpriseRead := protected.Group("/enterprise",
		auth.RequireAnyRole(auth.RoleTenantAdmin, auth.RoleTenantOperator, auth.RoleTenantSupport, auth.RoleTenantReadOnly),
		requireTenantContext,
		r.csrf.Middleware(),
		requireTenantActive,
	)

	// Per-capability write guards. IMPORTANT: these are plain middleware
	// handlers applied PER ROUTE, NOT empty-prefix Group("") objects —
	// Fiber v3 merges middleware across empty-prefix groups registered on
	// the same parent (documented in the /protected block below), which
	// would flatten every write gate onto every route of the console and
	// deny tenant_operator/tenant_support/tenant_readonly even read-only
	// access. The per-route slice pattern is the same one tenantCompatMW
	// uses; it must not drift back to Group("").
	canWriteDomains := authrbac.Require(authrbac.PermDomainsWrite)
	canWriteMailboxes := authrbac.Require(authrbac.PermMailboxesWrite)
	canWriteOrgs := authrbac.Require(authrbac.PermOrganizationsWrite)
	canWriteUsers := authrbac.Require(authrbac.PermUsersWrite)
	canWriteInvitations := authrbac.Require(authrbac.PermInvitationsWrite)
	canTransferOwnership := authrbac.Require(authrbac.PermOwnershipTransfer)
	canWriteAPIKeys := authrbac.Require(authrbac.PermAPIKeysWrite)
	canWriteBilling := authrbac.Require(authrbac.PermBillingWrite)
	canWriteAliases := authrbac.Require(authrbac.PermAliasesWrite)
	canWriteGroups := authrbac.Require(authrbac.PermGroupsWrite)
	canWriteImports := authrbac.Require(authrbac.PermImportsWrite)
	canExecuteImports := authrbac.Require(authrbac.PermImportsExecute)

	// ── Dashboard ──
	enterpriseRead.Get("/dashboard", r.h.CustomerDashboard)

	// ── Domains ──
	enterpriseRead.Get("/domains", r.h.ListAdminDomains)
	enterpriseRead.Get("/domains/:id", r.h.GetAdminDomain)
	enterpriseRead.Post("/domains", canWriteDomains, r.h.CreateAdminDomain)
	enterpriseRead.Patch("/domains/:id", canWriteDomains, r.h.UpdateAdminDomain)
	enterpriseRead.Post("/domains/:id/status", canWriteDomains, r.h.SetAdminDomainStatus)
	enterpriseRead.Delete("/domains/:id", canWriteDomains, r.h.DeleteAdminDomain)
	enterpriseRead.Get("/domains/:id/dkim", r.h.GetAdminDomainDKIM)
	enterpriseRead.Post("/domains/:id/dkim/generate", canWriteDomains, r.h.PostAdminDomainDKIMGenerate)
	enterpriseRead.Post("/domains/:id/dkim/rotate", canWriteDomains, r.h.PostAdminDomainDKIMRotate)
	enterpriseRead.Post("/domains/:id/dkim/revoke", canWriteDomains, r.h.PostAdminDomainDKIMRevoke)
	enterpriseRead.Get("/domains/:id/dkim/history", r.h.GetAdminDomainDKIMHistory)
	enterpriseRead.Get("/domains/:id/tls", r.h.GetAdminDomainTLSStatus)
	enterpriseRead.Get("/domains/:id/mail-access-mode", r.h.GetAdminDomainMailAccessMode)
	enterpriseRead.Post("/domains/:id/mail-access-mode", canWriteDomains, r.h.PostAdminDomainMailAccessMode)
	enterpriseRead.Post("/domains/:id/verify", r.h.VerifyEnterpriseDomain)
	enterpriseRead.Get("/domains/:id/dns", r.h.GetEnterpriseDomainDNS)
	enterpriseRead.Post("/domains/:id/dns/verify", canWriteDomains, r.h.VerifyEnterpriseDomainDNS)

	// ── Mailboxes ──
	enterpriseRead.Get("/mailboxes", r.h.ListAdminMailboxes)
	enterpriseRead.Get("/mailboxes/:id", r.h.GetAdminMailbox)
	enterpriseRead.Post("/mailboxes", canWriteMailboxes, r.h.CreateAdminMailbox)
	enterpriseRead.Patch("/mailboxes/:id", canWriteMailboxes, r.h.UpdateAdminMailbox)
	enterpriseRead.Post("/mailboxes/:id/status", canWriteMailboxes, r.h.SetAdminMailboxStatus)
	enterpriseRead.Post("/mailboxes/bulk/status", canWriteMailboxes, r.h.BulkSetAdminMailboxStatus)
	enterpriseRead.Post("/mailboxes/:id/reset-password", canWriteMailboxes, r.h.ResetAdminMailboxPassword)
	enterpriseRead.Delete("/mailboxes/:id", canWriteMailboxes, r.h.DeleteMailbox)
	enterpriseRead.Post("/mailboxes/:id/restore", canWriteMailboxes, r.h.PostAdminMailboxRestore)
	enterpriseRead.Delete("/mailboxes/:id/purge", canWriteMailboxes, r.h.DeleteAdminMailboxPurge)

	// ── Bulk mailbox provisioning (Milestone 6) ──
	enterpriseRead.Post("/domains/:id/mailboxes/bulk/validate", canWriteMailboxes, r.h.PostBulkProvisionValidate)
	enterpriseRead.Post("/domains/:id/mailboxes/bulk/jobs", canWriteMailboxes, r.h.PostBulkProvisionCreateJob)
	enterpriseRead.Get("/mailboxes/bulk/jobs/:jobId", r.h.GetBulkProvisionJob)
	enterpriseRead.Post("/mailboxes/bulk/jobs/:jobId/execute", canWriteMailboxes, r.h.PostBulkProvisionExecute)
	enterpriseRead.Post("/mailboxes/bulk/jobs/:jobId/cancel", canWriteMailboxes, r.h.PostBulkProvisionCancel)
	enterpriseRead.Post("/mailboxes/bulk/jobs/:jobId/retry", canWriteMailboxes, r.h.PostBulkProvisionRetry)

	// ── Outbound relay control plane (Milestone 7) ──
	enterpriseRead.Post("/relay/pools", canWriteDomains, r.h.PostRelayPool)
	enterpriseRead.Post("/relay/providers", canWriteDomains, r.h.PostRelayProvider)
	enterpriseRead.Get("/relay/pools/:id/providers", r.h.GetRelayPoolProviders)
	enterpriseRead.Post("/relay/providers/:id/test", canWriteDomains, r.h.PostRelayProviderTest)
	enterpriseRead.Post("/relay/routing-rules", canWriteDomains, r.h.PostRelayRoutingRule)
	enterpriseRead.Post("/relay/emergency-override", canWriteDomains, r.h.PostRelayEmergencyOverride)
	enterpriseRead.Delete("/relay/emergency-override/:id", canWriteDomains, r.h.DeleteRelayEmergencyOverride)

	// ── Cluster control plane (Milestone 10) ──
	enterpriseRead.Get("/cluster/nodes", r.h.GetClusterNodes)
	enterpriseRead.Post("/cluster/nodes/:id/cordon", canWriteDomains, r.h.PostClusterNodeCordon)
	enterpriseRead.Post("/cluster/nodes/:id/uncordon", canWriteDomains, r.h.PostClusterNodeUncordon)
	enterpriseRead.Post("/cluster/nodes/:id/drain", canWriteDomains, r.h.PostClusterNodeDrain)
	enterpriseRead.Post("/cluster/nodes/:id/resume", canWriteDomains, r.h.PostClusterNodeResume)

	// ── Organizations ──
	enterpriseRead.Get("/organizations/:id", r.h.GetOrganization)
	// Plan + live usage summary consumed by the domain provisioning wizard.
	// Read-only and scoped to the authenticated tenant — it uses the same
	// enterpriseRead group as every other tenant-scoped read, so RBAC is
	// unchanged and no new privilege is introduced.
	enterpriseRead.Get("/organizations/current/capacity", r.h.GetOrganizationCapacity)

	// ── Invitations ──
	enterpriseRead.Get("/invitations", r.h.ListInvitations)
	enterpriseRead.Post("/invitations", canWriteInvitations, r.h.CreateInvitation)
	enterpriseRead.Post("/invitations/:id/revoke", canWriteInvitations, r.h.RevokeInvitation)
	enterpriseRead.Post("/invitations/:id/resend", canWriteInvitations, r.h.ResendInvitation)

	// ── Members ──
	enterpriseRead.Get("/members", r.h.ListMembers)
	enterpriseRead.Patch("/members/:id/role", canWriteUsers, r.h.UpdateMemberRole)
	enterpriseRead.Delete("/members/:id", canWriteUsers, r.h.RemoveMember)

	// ── Ownership ──
	enterpriseRead.Post("/ownership/request", canTransferOwnership, r.h.RequestOwnershipTransfer)
	enterpriseRead.Post("/ownership/accept", canTransferOwnership, r.h.AcceptOwnershipTransfer)
	enterpriseRead.Post("/ownership/cancel", canTransferOwnership, r.h.CancelOwnershipTransfer)

	// ── Aliases ──
	enterpriseRead.Get("/aliases", r.h.ListAliases)
	enterpriseRead.Post("/aliases", canWriteAliases, r.h.CreateAlias)
	enterpriseRead.Delete("/aliases/:id", canWriteAliases, r.h.DeleteAlias)

	// ── Groups ──
	enterpriseRead.Get("/groups", r.h.ListGroups)
	enterpriseRead.Post("/groups", canWriteGroups, r.h.CreateGroup)
	enterpriseRead.Post("/groups/:id/members", canWriteGroups, r.h.AddGroupMember)
	enterpriseRead.Delete("/groups/:id/members/:memberId", canWriteGroups, r.h.RemoveGroupMember)
	enterpriseRead.Delete("/groups/:id", canWriteGroups, r.h.DeleteGroup)

	// ── Abuse ──
	enterpriseRead.Get("/abuse/send-limit", r.h.CheckSendLimit)
	enterpriseRead.Get("/abuse/signals", r.h.ListAbuseSignals)
	enterpriseRead.Post("/abuse/signals/:id/acknowledge", canWriteUsers, r.h.AcknowledgeAbuseSignal)
	enterpriseRead.Post("/abuse/signals/:id/resolve", canWriteUsers, r.h.ResolveAbuseSignal)

	// ── Account Status ──
	enterpriseRead.Get("/status", r.h.SuspensionStatus)
	enterpriseRead.Post("/deletion", canWriteOrgs, r.h.RequestDeletion)
	enterpriseRead.Post("/deletion/cancel", canWriteOrgs, r.h.CancelDeletion)

	// ── Billing ──
	enterpriseRead.Get("/billing/subscription", r.h.GetBillingSubscription)
	enterpriseRead.Get("/billing/state", r.h.GetBillingState)
	enterpriseRead.Get("/billing/usage", r.h.GetBillingUsage)
	enterpriseRead.Get("/billing/quota", r.h.CheckBillingQuota)
	enterpriseRead.Post("/billing/subscription", canWriteBilling, r.h.CreateBillingSubscription)
	enterpriseRead.Get("/billing/invoices", r.h.ListCustomerInvoices)
	enterpriseRead.Get("/billing/invoices/:id", r.h.GetCustomerInvoice)

	// ── API Keys ──
	enterpriseRead.Get("/api-keys", r.h.ListEnterpriseAPIKeys)
	enterpriseRead.Post("/api-keys", canWriteAPIKeys, r.h.CreateEnterpriseAPIKey)
	enterpriseRead.Post("/api-keys/:id/rotate", canWriteAPIKeys, r.h.RotateEnterpriseAPIKey)
	enterpriseRead.Delete("/api-keys/:id", canWriteAPIKeys, r.h.DeleteEnterpriseAPIKey)

	// ── Audit Logs ──
	enterpriseRead.Get("/audit/logs", r.h.ListEnterpriseAuditLogs)

	// ── Sessions ──
	enterpriseRead.Get("/sessions", r.h.ListAccountSessions)
	enterpriseRead.Post("/sessions/:id/revoke", canWriteUsers, r.h.RevokeAccountSession)

	// ── Imports ──
	enterpriseRead.Get("/imports", r.h.ListImports)
	enterpriseRead.Get("/imports/:id", r.h.GetImport)
	enterpriseRead.Get("/imports/:id/report", r.h.GetImportReport)
	enterpriseRead.Post("/imports", canWriteImports, r.h.CreateImport)
	enterpriseRead.Post("/imports/:id/validate", canWriteImports, r.h.ValidateImport)
	enterpriseRead.Post("/imports/:id/execute", canExecuteImports, r.h.ExecuteImport)
	enterpriseRead.Post("/imports/:id/resume", canExecuteImports, r.h.ResumeImport)
	enterpriseRead.Post("/imports/:id/cancel", canExecuteImports, r.h.CancelImport)
	enterpriseRead.Post("/imports/:id/compensate", canExecuteImports, r.h.CompensateImport)

	// CSRF is enforced on the entire admin group by default (deny-list,
	// not allow-list) rather than only on routes an author remembered to
	// nest under a separate CSRF sub-group — several state-changing
	// routes (migration start, domain provisioning, calendar/contacts/
	// tasks, compliance policies) were previously mounted directly here
	// and shipped with no CSRF check at all. csrf.Middleware() already
	// no-ops on GET/HEAD/OPTIONS and on API-key-authenticated requests,
	// so this adds no burden to the read-only routes or to the
	// provisioning API below.
	// PORTAL-SEPARATION-PHASE1: the deprecated auth.RoleAdmin was removed from
	// this gate. Legacy "admin" rows are remapped to either RolePlatformSuperAdmin
	// or RoleTenantAdmin at startup by normalizeAdminRoles (internal/models),
	// so no legitimate account is affected. The RoleAdmin permission map has
	// also been removed from internal/auth/rbac so any un-normalized "admin"
	// row can neither pass this gate nor satisfy any per-route authrbac.Require.
	// Tenant admins keep console access via RoleTenantAdmin; strictly platform-
	// only routes below (backups, firewall, modules, license, monitoring, etc.)
	// remain reachable only to platform super admins because their per-handler
	// permission checks fail for tenant roles that lack those permissions.
	// ── Per-route middleware stacks ────────────────────────────
	// Fiber v3 Group("") with empty prefix merges middleware across
	// groups registered on the same parent. To avoid cross-contamination
	// we apply middleware per-route via MW slice indices instead of
	// Group("") groups. The index approach must NOT drift — each group
	// has exactly N middleware elements; the route registration pattern
	// lists all N followed by the handler.
	//
	// platformMW: 2 elements — RequireAnyRole(PSA, SuperAdmin), CSRF.
	platformMW := []fiber.Handler{
		auth.RequireAnyRole(auth.RolePlatformSuperAdmin, auth.RoleSuperAdmin),
		r.csrf.Middleware(),
	}
	// tenantCompatMW: 3 elements — RequireAnyRole(full tenant family),
	// requireTenantContext, CSRF.
	// Admits the canonical tenant-role family so route-level RBAC
	// permissions (not the role gate) decide read vs write access.
	tenantCompatMW := []fiber.Handler{
		auth.RequireAnyRole(auth.RoleTenantAdmin, auth.RoleTenantOperator, auth.RoleTenantSupport, auth.RoleTenantReadOnly),
		requireTenantContext,
		r.csrf.Middleware(),
	}
	protected.Get("/domains", r.h.SupportAccessMiddlewareForScope("domain_view"), tenantCompatMW[2], r.h.ListDomains)
	protected.Get("/users", r.h.SupportAccessMiddlewareForScope("read_only"), tenantCompatMW[2], r.h.ListUsers)
	protected.Get("/mailboxes", r.h.SupportAccessMiddlewareForScope("mailbox_view"), tenantCompatMW[2], r.h.ListMailboxes)
	// CSV exports (admin-only, GET — no CSRF required). Registered before
	// the parameterized :id / :name routes so the literal /export segment
	// wins over /mailboxes/:id and /domains/:name.
	protected.Get("/mailboxes/export", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ExportMailboxesCSV)
	protected.Get("/domains/export", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ExportDomainsCSV)
	protected.Get("/domains/:name/audit", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetDomainAudit)
	protected.Get("/domains/:name", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetDomain)
	protected.Get("/mailboxes/:id/audit", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetMailboxAudit)
	protected.Get("/mailboxes/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetMailbox)
	protected.Get("/queue", platformMW[0], platformMW[1], r.h.ListQueue)
	// Admin Queue Operations (QUEUE-OPERATIONS-2E): summary,
	// single-entry detail, and safe retry/delete (already wired
	// in the CSRF-protected men group below). All admin-only.
	// Note: the explicit /admin/ path segment distinguishes these
	// admin-read endpoints from legacy /queue paths (list, retry,
	// delete) which are mounted without the segment for backward
	// compatibility.
	protected.Get("/admin/queue/summary", platformMW[0], platformMW[1], r.h.AdminQueueSummary)
	protected.Get("/admin/queue/messages", platformMW[0], platformMW[1], r.h.AdminQueueList)
	protected.Get("/admin/queue/messages/:id", platformMW[0], platformMW[1], r.h.AdminQueueDetail)
	// history/export MUST be registered before the /admin/queue/:id
	// wildcard below, or fiber matches "history"/"export" as :id.
	protected.Get("/admin/queue/history", platformMW[0], platformMW[1], r.h.AdminQueueHistory)
	protected.Get("/admin/queue/export", platformMW[0], platformMW[1], r.h.AdminQueueExport)
	protected.Get("/admin/queue/:id", platformMW[0], platformMW[1], r.h.GetAdminQueueEntry)
	protected.Get("/admin/backups", platformMW[0], platformMW[1], r.h.ListBackups)
	protected.Get("/admin/backups/schedule", platformMW[0], platformMW[1], r.h.GetBackupSchedule)
	protected.Get("/admin/backups/metrics", platformMW[0], platformMW[1], r.h.GetBackupMetrics)
	protected.Get("/admin/backups/health", platformMW[0], platformMW[1], r.h.GetBackupHealth)
	// Durable restore-job status (async restore lifecycle). Placed before the
	// /admin/backups/:id catch-all so "restore-jobs" is never treated as an id.
	protected.Get("/admin/backups/restore-jobs/:job_id", platformMW[0], platformMW[1], r.h.GetRestoreJobStatus)
	protected.Get("/admin/backups/:id/download", platformMW[0], platformMW[1], r.h.DownloadBackup)
	protected.Get("/admin/backups/:id", platformMW[0], platformMW[1], r.h.GetBackup)
	// Legacy /backups routes — return 410 Gone so the frontend
	// can safely discover the new path without accidentally
	// performing destructive operations on the old one.
	protected.Get("/backups", platformMW[0], platformMW[1], r.h.LegacyGone)
	protected.Get("/backups/schedule", platformMW[0], platformMW[1], r.h.LegacyGone)
	protected.Get("/backups/metrics", platformMW[0], platformMW[1], r.h.LegacyGone)
	protected.Get("/backups/health", platformMW[0], platformMW[1], r.h.LegacyGone)
	protected.Get("/backups/:id/download", platformMW[0], platformMW[1], r.h.LegacyGone)
	protected.Get("/firewall/rules", platformMW[0], platformMW[1], r.h.ListFirewallRules)
	protected.Get("/firewall/logs", platformMW[0], platformMW[1], r.h.ListFirewallLogs)
	protected.Get("/modules", platformMW[0], platformMW[1], r.h.ListModules)
	protected.Get("/license", platformMW[0], platformMW[1], r.h.GetLicense)
	protected.Get("/audit/logs", platformMW[0], platformMW[1], r.h.ListAuditLogs)
	protected.Get("/audit/logs/export", platformMW[0], platformMW[1], r.h.ExportAuditLogs)
	protected.Get("/audit/logs/:id", platformMW[0], platformMW[1], r.h.GetAuditEntry)
	// Admin Enterprise v2 — RBAC + account classes + groups +
	// lists + public folders + quarantine + ACL + log rules.
	protected.Get("/admin/account-classes", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListAccountClasses)
	protected.Get("/admin/domain-groups", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListDomainGroups)
	protected.Get("/admin/mailing-lists", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListMailingLists)
	protected.Get("/admin/public-folders", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListPublicFolders)
	protected.Get("/admin/admin-groups", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListAdminGroups)
	protected.Get("/admin/quarantine", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListQuarantine)
	protected.Get("/admin/audit-logs", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListAdminAuditLogs)
	protected.Get("/admin/acl-rules", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListACLRules)
	protected.Get("/admin/login-protection/status", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.LoginProtectionStatus)
	protected.Get("/admin/login-protection/lockouts", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListLockouts)
	protected.Get("/admin/admin-users", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListAdminUsers)
	protected.Get("/admin/admin-users/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetAdminUser)
	protected.Get("/admin/log-rules", platformMW[0], platformMW[1], r.h.ListLogRules)
	// Enterprise v3 — SSL, acceptance rules, incoming message
	// rules, FTP backup targets, file system browser,
	// migration sources, clustering, antivirus, settings
	// protocol splits.
	protected.Get("/admin/ssl/certificates", platformMW[0], platformMW[1], r.h.AdminSslListCertificates)
	protected.Get("/admin/ssl/certificates/reload", platformMW[0], platformMW[1], r.h.AdminSslReloadCertificates)
	protected.Get("/admin/ssl/expiry-warnings", platformMW[0], platformMW[1], r.h.AdminSslExpiryWarnings)
	protected.Get("/admin/ssl/acme/status", platformMW[0], platformMW[1], r.h.AdminSslAcmeStatus)
	protected.Get("/admin/acceptance-rules", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListAcceptanceRules)
	protected.Get("/admin/incoming-msg-rules", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListIncomingMsgRules)
	protected.Get("/admin/migration-sources", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListMigrationSources)
	protected.Get("/admin/backup-targets", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListBackupTargets)
	protected.Get("/admin/backup-targets/:id/test", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.TestBackupTarget)
	protected.Get("/admin/migration-sources/:id/test", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.TestMigrationSource)
	protected.Get("/admin/fs/browse", platformMW[0], platformMW[1], r.h.AdminFsBrowse)
	protected.Get("/admin/fs/read", platformMW[0], platformMW[1], r.h.AdminFsRead)
	protected.Get("/admin/cluster/status", platformMW[0], platformMW[1], r.h.AdminClusteringStatus)
	protected.Get("/admin/security/antivirus", platformMW[0], platformMW[1], r.h.AdminAntivirusStatus)
	// Per-protocol settings sub-pages. The :protocol path
	// parameter is one of the IDs in the protocolDefs map.
	protected.Get("/admin/settings/protocol/:protocol", platformMW[0], platformMW[1], r.h.ListProtocolSettings)
	protected.Get("/admin/mailing-lists/:id/members", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListMailingListMembers)
	protected.Get("/admin/admin-groups/:id/members", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListAdminGroupMembers)
	protected.Get("/feature-flags", platformMW[0], platformMW[1], r.h.ListFeatureFlags)
	protected.Get("/api-keys", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListAPIKeys)
	protected.Get("/admin/summary", platformMW[0], platformMW[1], r.h.AdminSummary)
	// Admin Runtime Telemetry (ADMIN-RUNTIME-TELEMETRY-2B):
	// read-only, admin-protected. No CSRF required (GET).
	protected.Get("/admin/runtime", platformMW[0], platformMW[1], r.h.GetAdminRuntime)
	// Monitoring v1: read-only health + alert endpoints (admin role).
	protected.Get("/monitoring/health", platformMW[0], platformMW[1], r.h.GetMonitoringHealth)
	protected.Get("/monitoring/alerts", platformMW[0], platformMW[1], r.h.GetMonitoringAlerts)
	protected.Get("/monitoring/capacity", platformMW[0], platformMW[1], r.h.GetMonitoringCapacity)
	protected.Get("/monitoring/snapshot", platformMW[0], platformMW[1], r.h.GetMonitoringSnapshot)
	protected.Get("/monitoring/alert-providers", platformMW[0], platformMW[1], r.h.GetMonitoringProviders)
	if r.cfg.Metrics.Enabled {
		// Metrics contain operational state and are never exposed on a public
		// unauthenticated route. Operators scrape this admin-authenticated path
		// through a trusted collector or the loopback API.
		protected.Get("/metrics", platformMW[0], platformMW[1], metrics.Handler())
	}
	// Admin alert-delivery audit (ORVIX-ADMIN-ENTERPRISE-PARITY):
	// read-only, reuses monitoring.Dispatcher.ListDeliveries so the
	// secret-free contract stays aligned with the rest of the alert
	// pipeline.
	protected.Get("/monitoring/alert-deliveries", platformMW[0], platformMW[1], r.h.ListAlertDeliveries)
	// Admin storage topology (ORVIX-ADMIN-ENTERPRISE-PARITY-G):
	// real on-disk usage for mail/attachments/backups. No replica or
	// shard controls; see docs/ORVIX_ENTERPRISE_PARITY_AUDIT.md.
	protected.Get("/admin/storage/volumes", platformMW[0], platformMW[1], r.h.ListStorageVolumes)
	// Tenants read (ORVIX-ADMIN-ENTERPRISE-PARITY-D): surface the
	// JWT-tenant row read-only so the admin "Branding" page knows
	// what to render. Branding writes are CSRF-protected below.
	protected.Get("/admin/tenants/current", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetAdminTenant)

	// Auto-Heal
	protected.Get("/heal/history", platformMW[0], platformMW[1], r.h.ListHealHistory)
	protected.Post("/heal/check/:name", platformMW[0], platformMW[1], r.h.RunHealCheck)

	// Guardian
	protected.Post("/guardian/analyze", platformMW[0], platformMW[1], r.h.AnalyzeEmail)
	protected.Get("/guardian/logs", platformMW[0], platformMW[1], r.h.ListGuardianLogs)

	// Smart Compose AI
	protected.Post("/compose/complete", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ComposeComplete)
	protected.Post("/compose/stream", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ComposeStream)

	// DNS Automation — legacy endpoints (kept for backward compat
	// with the pre-DNS-DKIM-OPERATIONS-2F UI). They now delegate
	// to the new dnsops service when wired; they return 503 when
	// the service is not available so the dashboard never sees a
	// "pending" placeholder.
	protected.Post("/dns/check/:domain", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DNSCheck)
	protected.Post("/dns/wizard/:domain", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DNSWizard)

	// Admin Settings (ENTERPRISE-SETTINGS-2H): read-only GET, write is CSRF-protected
	protected.Get("/admin/mfa/status", platformMW[0], platformMW[1], r.h.MFAStatusGet)
	protected.Get("/admin/settings", platformMW[0], platformMW[1], r.h.AdminSettingsGet)

	// DNS Operations (DNS-DKIM-OPERATIONS-2F): real DNS / DKIM
	// operations for the admin UI. All admin-only, all read-only
	// except for DKIM keygen (CSRF-protected below in `men`)
	// and provider apply (also CSRF-protected).
	protected.Get("/admin/dns/providers", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetAdminDNSProviders)
	protected.Get("/admin/dns/:domain/plan", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetAdminDNSPlan)
	protected.Post("/admin/dns/:domain/verify", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.PostAdminDNSVerify)
	protected.Get("/admin/dns/:domain/wizard", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetAdminDNSWizard)
	protected.Post("/admin/dns/:domain/provider/plan", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.PostAdminDNSProviderPlan)

	// Migration
	protected.Post("/migration/test", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.MigrationTest)
	protected.Post("/migration/start", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.MigrationStart)
	protected.Get("/migration/jobs", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListMigrationJobs)

	// Webmail Management
	protected.Get("/webmail/accounts", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListWebmailAccounts)
	protected.Get("/webmail/sessions", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListWebmailSessions)
	protected.Get("/webmail/activity/:mailboxId", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetWebmailLoginActivity)
	protected.Get("/webmail/storage/:mailboxId", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetWebmailStorageMetrics)

	// Provision API
	protected.Post("/provision/domain", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ProvisionDomain)

	// Calendar
	protected.Get("/calendar/events", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListEvents)
	protected.Post("/calendar/events", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateEvent)
	protected.Put("/calendar/events/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateEvent)
	protected.Delete("/calendar/events/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteEvent)

	// Contacts
	protected.Get("/contacts", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListContacts)
	protected.Post("/contacts", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateContact)
	protected.Put("/contacts/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateContact)
	protected.Delete("/contacts/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteContact)

	// Tasks
	protected.Get("/tasks", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListTasks)
	protected.Post("/tasks", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateTask)
	protected.Put("/tasks/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateTask)
	protected.Patch("/tasks/:id/complete", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CompleteTask)
	protected.Delete("/tasks/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteTask)

	// Auto-Update (legacy /updates/* routes — kept for backward compat)
	protected.Get("/updates/check", platformMW[0], platformMW[1], r.h.CheckUpdates)
	protected.Get("/updates/changelog", platformMW[0], platformMW[1], r.h.GetChangelog)
	protected.Post("/updates/apply/:module", platformMW[0], platformMW[1], r.h.ApplyUpdate)

	// Update Management v1: read-only endpoints (admin role).
	protected.Get("/update/status", platformMW[0], platformMW[1], r.h.GetUpdateStatus)
	protected.Get("/update/history", platformMW[0], platformMW[1], r.h.GetUpdateHistory)
	protected.Get("/update/preflight", platformMW[0], platformMW[1], r.h.GetUpdatePreflight)
	protected.Get("/update/check", platformMW[0], platformMW[1], r.h.GetUpdateCheck)

	// Email Intelligence
	protected.Get("/intelligence/stats", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetEmailStats)
	protected.Get("/intelligence/delivery", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.GetDeliveryReports)

	// Compliance & Legal Hold
	protected.Get("/compliance/legal-holds", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListLegalHolds)
	protected.Post("/compliance/legal-holds", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateLegalHold)
	protected.Put("/compliance/legal-holds/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateLegalHold)
	protected.Get("/compliance/policies", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListRetentionPolicies)
	protected.Post("/compliance/policies", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateRetentionPolicy)

	// Collaboration
	protected.Get("/collaboration/mailboxes", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ListSharedMailboxes)
	protected.Post("/collaboration/mailboxes", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateSharedMailbox)

	// men no longer adds its own CSRF middleware — admin (above) now
	// enforces it for the whole group. Kept as a separate alias so the
	// diff for existing routes below stays minimal.
	protected.Post("/domains", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateDomain)
	protected.Patch("/domains/:name", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.PatchDomain)
	protected.Patch("/domains/:name/status", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateDomainStatus)
	protected.Delete("/domains/:name", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteDomain)
	protected.Post("/users", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateUser)
	protected.Post("/mailboxes", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateMailbox)
	protected.Patch("/mailboxes/:id/password", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateMailboxPassword)
	protected.Patch("/mailboxes/:id/status", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateMailboxStatus)
	protected.Patch("/mailboxes/:id/quota", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateMailboxQuota)
	protected.Patch("/mailboxes/:id/protocols", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateMailboxProtocols)
	// Bulk status operations (CSRF-protected).
	protected.Post("/mailboxes/bulk/status", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.BulkMailboxStatus)
	protected.Post("/mailboxes/import", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ImportMailboxesCSV)
	protected.Post("/mailboxes/import/dry-run", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ImportMailboxesDryRun)
	protected.Post("/domains/bulk/status", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.BulkDomainStatus)
	protected.Delete("/mailboxes/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteMailbox)
	protected.Delete("/users/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteUser)
	protected.Delete("/queue/:id", platformMW[0], platformMW[1], r.h.DeleteQueue)
	protected.Post("/queue/:id/retry", platformMW[0], platformMW[1], r.h.RetryQueue)
	protected.Post("/admin/queue/messages/:id/retry", platformMW[0], platformMW[1], r.h.AdminQueueRetryNow)
	protected.Post("/admin/queue/messages/:id/bounce", platformMW[0], platformMW[1], r.h.AdminQueueBounce)
	protected.Post("/admin/queue/messages/:id/cancel", platformMW[0], platformMW[1], r.h.AdminQueueCancel)
	protected.Post("/admin/queue/messages/bulk-action", platformMW[0], platformMW[1], r.h.AdminQueueBulkAction)
	protected.Post("/admin/backups", platformMW[0], platformMW[1], r.h.CreateBackup)
	protected.Post("/admin/backups/now", platformMW[0], platformMW[1], r.h.PostBackupNow)
	protected.Post("/admin/backups/schedule", platformMW[0], platformMW[1], r.h.SetBackupSchedule)
	protected.Post("/admin/backups/retention", platformMW[0], platformMW[1], r.h.RunBackupRetention)
	protected.Post("/admin/backups/:id/validate", platformMW[0], platformMW[1], r.h.PostValidateBackup)
	protected.Post("/admin/backups/:id/restore", platformMW[0], platformMW[1], r.h.PostRestoreBackup)
	protected.Delete("/admin/backups/:id", platformMW[0], platformMW[1], r.h.DeleteBackup)
	// Legacy write routes return 410 Gone.
	protected.Post("/backups", platformMW[0], platformMW[1], r.h.LegacyGone)
	protected.Post("/backups/schedule", platformMW[0], platformMW[1], r.h.LegacyGone)
	protected.Post("/backups/retention", platformMW[0], platformMW[1], r.h.LegacyGone)
	protected.Delete("/backups/:id", platformMW[0], platformMW[1], r.h.LegacyGone)
	// SaaS Two-Console: Internal Ops (superadmin-only, read-only).
	protected.Get("/console/reports", platformMW[0], platformMW[1], r.h.AdminReports)
	// The /console/internal/* surface is the Orvix-internal
	// operations control plane. Customer admins must not be able
	// to read it even if they guess the URL — the contract is
	// server-side role enforcement, not client-side hiding. We
	// therefore mount these routes on a sub-group that requires
	// RolePlatformSuperAdmin or RoleSuperAdmin; the parent `admin` group still accepts
	// admin-or-superadmin for the customer-facing read paths.
	protected.Get("/console/internal/overview", platformMW[0], platformMW[1], r.h.InternalOverview)
	protected.Get("/console/internal/tenants", platformMW[0], platformMW[1], r.h.InternalTenants)
	protected.Get("/console/internal/domain-intelligence", platformMW[0], platformMW[1], r.h.InternalDomainIntelligence)
	protected.Get("/console/internal/security-ops", platformMW[0], platformMW[1], r.h.InternalSecurityOps)
	protected.Get("/console/internal/mail-flow-ops", platformMW[0], platformMW[1], r.h.InternalMailFlowOps)

	// Platform administration (cross-tenant, admin/superadmin only).
	// These routes operate on all tenants and require explicit
	// platform-level authorization — not just tenant membership.
	protected.Get("/platform/dashboard", platformMW[0], platformMW[1], r.h.PlatformDashboard)
	protected.Post("/platform/organizations", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformOrganizationsWrite), r.h.CreatePlatformOrganization)
	protected.Get("/platform/organizations", platformMW[0], platformMW[1], r.h.ListPlatformOrganizations)
	protected.Get("/platform/organizations/:id", platformMW[0], platformMW[1], r.h.GetPlatformOrganization)
	protected.Patch("/platform/organizations/:id", platformMW[0], platformMW[1], r.h.UpdateOrganization)
	protected.Post("/platform/organizations/:id/active", platformMW[0], platformMW[1], r.h.SetOrganizationActive)
	protected.Get("/platform/organizations/:id/detail", platformMW[0], platformMW[1], r.h.GetOrganizationDetail)
	// Phase G: safe organization deletion lifecycle — schedules deletion
	// (30-day retention, soft state transition) rather than deleting
	// anything immediately. Gated the same as every other platform/organizations
	// route (platform admin auth), plus its own dependency/confirmation
	// checks in the handler/service.
	protected.Post("/platform/organizations/:id/deletion", platformMW[0], platformMW[1], r.h.PlatformScheduleOrganizationDeletion)

	// ── Disaster Recovery (Milestone 13) — coordinated backup/restore
	// on top of the existing internal/backup mechanics and restorecoord
	// restart/rollback lifecycle. Platform-admin only, CSRF-protected
	// (platformMW carries CSRF for every route registered with it).
	protected.Get("/dr/readiness", platformMW[0], platformMW[1], r.h.GetDRReadiness)
	protected.Get("/dr/drills", platformMW[0], platformMW[1], r.h.GetDRDrills)
	protected.Post("/dr/drills", platformMW[0], platformMW[1], r.h.PostDRDrill)
	protected.Post("/dr/backup", platformMW[0], platformMW[1], r.h.PostDRCoordinatedBackup)
	protected.Post("/dr/backups/:id/restore", platformMW[0], platformMW[1], r.h.PostDRCoordinatedRestore)
	protected.Get("/dr/operations", platformMW[0], platformMW[1], r.h.GetDROperationHistory)
	protected.Get("/dr/operations/:job_id", platformMW[0], platformMW[1], r.h.GetDROperationStatus)

	// ── Retention / legal hold / purge (Milestone 14) ──────────────
	protected.Post("/retention/policies", platformMW[0], platformMW[1], r.h.PostRetentionPolicy)
	protected.Get("/retention/policies/effective", platformMW[0], platformMW[1], r.h.GetRetentionEffectivePolicy)
	protected.Post("/retention/legal-holds", platformMW[0], platformMW[1], r.h.PostRetentionLegalHold)
	protected.Get("/retention/legal-holds", platformMW[0], platformMW[1], r.h.GetRetentionLegalHolds)
	protected.Post("/retention/legal-holds/:id/release", platformMW[0], platformMW[1], r.h.PostRetentionLegalHoldRelease)
	protected.Post("/retention/purge/plan", platformMW[0], platformMW[1], r.h.PostRetentionPurgePlan)
	protected.Post("/retention/purge/execute", platformMW[0], platformMW[1], r.h.PostRetentionPurgeExecute)
	protected.Post("/retention/mailboxes/:id/recover", platformMW[0], platformMW[1], r.h.PostRetentionRecoverMailbox)
	protected.Get("/retention/custody", platformMW[0], platformMW[1], r.h.GetRetentionCustody)

	// ── Signed update-artifact verification + staged lifecycle
	// (Milestone 13). Independent of the legacy /update/* routes above
	// (version check/changelog/systemd-oneshot runtime update) — this
	// adds cryptographic verification and staging; apply/rollback only
	// hand off to an external coordinator, never restart in-process.
	protected.Post("/updates/artifacts", platformMW[0], platformMW[1], r.h.PostUpdateArtifact)
	protected.Get("/updates/artifacts/history", platformMW[0], platformMW[1], r.h.GetUpdateArtifactHistory)
	protected.Get("/updates/artifacts/:id", platformMW[0], platformMW[1], r.h.GetUpdateArtifactStatus)
	protected.Post("/updates/artifacts/:id/apply", platformMW[0], platformMW[1], r.h.PostUpdateArtifactApply)
	protected.Post("/updates/artifacts/:id/rollback", platformMW[0], platformMW[1], r.h.PostUpdateArtifactRollback)
	protected.Get("/updates/operations/:job_id", platformMW[0], platformMW[1], r.h.GetUpdateOperationStatus)

	// ── Incident management (Milestone 16, platform-only) ──────────
	protected.Post("/incidents", platformMW[0], platformMW[1], r.h.CreateIncident)
	protected.Get("/incidents", platformMW[0], platformMW[1], r.h.ListIncidents)
	protected.Get("/incidents/:id", platformMW[0], platformMW[1], r.h.GetIncident)
	protected.Patch("/incidents/:id", platformMW[0], platformMW[1], r.h.UpdateIncident)
	protected.Get("/incidents/:id/timeline", platformMW[0], platformMW[1], r.h.GetIncidentTimeline)

	// ── Support access (Milestone 16, platform-only) ───────────────
	protected.Post("/platform/support/grants", platformMW[0], platformMW[1], r.h.CreateSupportAccessGrant)
	protected.Get("/platform/support/grants", platformMW[0], platformMW[1], r.h.ListSupportAccessGrants)
	protected.Get("/platform/support/grants/:id", platformMW[0], platformMW[1], r.h.GetSupportAccessGrant)
	protected.Post("/platform/support/grants/:id/activate", platformMW[0], platformMW[1], r.h.ActivateSupportAccessGrant)
	protected.Post("/platform/support/grants/:id/revoke", platformMW[0], platformMW[1], r.h.RevokeSupportAccessGrant)

	// ── Support-access enforcement (Milestone 16) ─────────────────
	// These routes demonstrate the support-access middleware enforcing
	// tenant binding, scope, expiry, and revocation for a support
	// operator accessing tenant-scoped resources.
	// ── Runtime capability endpoint (Milestone 16, platform-only) ─
	protected.Get("/platform/capabilities", platformMW[0], platformMW[1], r.h.GetCapabilities)

	// ── Configuration truth model (Milestone 16, platform-only) ────
	protected.Get("/platform/config", platformMW[0], platformMW[1], r.h.ListConfigurationSettings)
	protected.Get("/platform/config/:key", platformMW[0], platformMW[1], r.h.GetConfigurationSetting)
	protected.Patch("/platform/config/:key", platformMW[0], platformMW[1], r.h.MutateConfigurationSetting)

	// ── Platform import endpoints (Milestone 16 Phase 4B) ───────
	protected.Get("/platform/imports", platformMW[0], platformMW[1], r.h.ListImports)
	protected.Get("/platform/imports/:id", platformMW[0], platformMW[1], r.h.GetImport)
	protected.Get("/platform/imports/:id/report", platformMW[0], platformMW[1], r.h.GetImportReport)
	protected.Post("/platform/imports", platformMW[0], platformMW[1], r.h.CreateImport)
	protected.Post("/platform/imports/:id/validate", platformMW[0], platformMW[1], r.h.ValidateImport)
	protected.Post("/platform/imports/:id/execute", platformMW[0], platformMW[1], r.h.ExecuteImport)
	protected.Post("/platform/imports/:id/resume", platformMW[0], platformMW[1], r.h.ResumeImport)
	protected.Post("/platform/imports/:id/cancel", platformMW[0], platformMW[1], r.h.CancelImport)
	protected.Post("/platform/imports/:id/compensate", platformMW[0], platformMW[1], r.h.CompensateImport)

	// ── Platform mail control (Mail-Control enablement) ─────────
	// platformMW-gated: only platform_super_admin may call these; every
	// route requires an explicit target tenant_id in the path. Tenant
	// Admin roles cannot reach them (platformMW[0] role gate), and the
	// PSA is never treated as a tenant admin. RBAC permissions reuse
	// the canonical domain/mailbox/alias/group permissions.
	protected.Get("/platform/domains/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsRead), r.h.ListPlatformDomains)
	protected.Get("/platform/domains/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsRead), r.h.GetPlatformDomain)
	protected.Post("/platform/domains/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsWrite), r.h.CreatePlatformDomain)
	protected.Post("/platform/domains/:tenant_id/:id/status", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsWrite), r.h.SetPlatformDomainStatus)
	protected.Post("/platform/domains/:tenant_id/:id/mail-access-mode", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsWrite), r.h.SetPlatformDomainMailAccessMode)
	// Read-only public DNS/DKIM snapshot for an existing domain, and
	// the canonical DKIM generate/rotate transaction exposed to the
	// Platform Super Admin surface (API-contract closure, concerns 2/3).
	// Registered as literal sub-paths of :id — Fiber's router always
	// prefers a literal segment match over a param match at the same
	// position, so these can never be shadowed by GetPlatformDomain's
	// bare ":id" route regardless of declaration order, but they are
	// still declared immediately alongside it for readability.
	protected.Get("/platform/domains/:tenant_id/:id/dns", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsRead), r.h.GetPlatformDomainDNS)
	// Read-only live public-DNS verification against the same
	// canonical requirements .../dns exposes. External DNS lookups
	// only — never mutates public DNS, DKIM, or the domain. Gated on
	// the same PermDomainsRead as the read route above (not
	// PermDomainsWrite): this route performs no write of any kind.
	protected.Post("/platform/domains/:tenant_id/:id/dns/verify", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsRead), r.h.VerifyPlatformDomainDNS)
	protected.Post("/platform/domains/:tenant_id/:id/dkim/generate", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsWrite), r.h.GeneratePlatformDomainDKIM)
	protected.Post("/platform/domains/:tenant_id/:id/dkim/rotate", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsWrite), r.h.RotatePlatformDomainDKIM)
	protected.Post("/platform/domains/:tenant_id/:id/dkim/revoke", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDomainsWrite), r.h.RevokePlatformDomainDKIM)
	// Platform domain lifecycle (Phase 8 production-acceptance
	// remediation): canonical, audited deactivation/soft-delete. See
	// platform_domain_lifecycle.go for the full contract.
	protected.Post("/platform/domains/:tenant_id/:id/deactivate", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformDomainsDeactivate), r.h.DeactivatePlatformDomain)
	// Canonical, audited, PERMANENT deleted_at-tombstone delete —
	// distinct authority from deactivate above. See
	// platform_domain_lifecycle.go for the full contract (deactivate-
	// then-delete gate, structured dependency blockers, active-DKIM
	// purge with history preserved).
	protected.Post("/platform/domains/:tenant_id/:id/delete", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformDomainsDelete), r.h.DeletePlatformDomain)

	// Platform user lifecycle (Phase 8 production-acceptance remediation):
	// canonical, audited, non-self deactivation of another platform-scoped
	// user account. See platform_user_lifecycle.go for the full contract.
	protected.Post("/platform/users/:id/deactivate", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformUsersWrite), r.h.DeactivatePlatformUser)

	// NOTE: GET /platform/mailboxes/bulk/template MUST be registered
	// before GET /platform/mailboxes/:tenant_id/:id below. Fiber v3
	// matches same-depth routes (both are 4 path segments) in
	// REGISTRATION ORDER, not static-over-param — registering the
	// param route first would silently shadow the bulk template route
	// (":tenant_id"="bulk", ":id"="template"), producing a bogus
	// "a valid tenant_id is required" 400 instead of the template.
	protected.Get("/platform/mailboxes/bulk/template", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesRead), r.h.GetPlatformBulkMailboxTemplate)
	protected.Get("/platform/mailboxes/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesRead), r.h.ListPlatformMailboxes)
	protected.Get("/platform/mailboxes/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesRead), r.h.GetPlatformMailbox)
	protected.Post("/platform/mailboxes/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.CreatePlatformMailbox)
	protected.Post("/platform/mailboxes/:tenant_id/:id/access-mode", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.SetPlatformMailboxAccessMode)
	protected.Post("/platform/mailboxes/:tenant_id/:id/status", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.SetPlatformMailboxStatus)
	protected.Post("/platform/mailboxes/:tenant_id/:id/quota", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.SetPlatformMailboxQuota)
	protected.Post("/platform/mailboxes/:tenant_id/:id/reset-password", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.ResetPlatformMailboxPassword)
	protected.Delete("/platform/mailboxes/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.DeletePlatformMailbox)
	// ── Audited, read-only mailbox support view (PermPlatformMailboxSupportView) ──
	// Platform-only: never inherited by any tenant role. The session
	// model (supportaccess.MailboxViewSession) binds one operator to
	// one mailbox in one tenant for a bounded window; the platform
	// operator's own auth cookie/session is never touched by any of
	// these routes, and the mailbox password is never read.
	protected.Post("/platform/mailboxes/:tenant_id/:id/support-view", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformMailboxSupportView), r.h.StartMailboxSupportView)
	protected.Get("/platform/mailboxes/:tenant_id/:id/support-view/:session_id/folders", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformMailboxSupportView), r.h.ListMailboxSupportFolders)
	protected.Get("/platform/mailboxes/:tenant_id/:id/support-view/:session_id/messages", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformMailboxSupportView), r.h.ListMailboxSupportMessages)
	protected.Get("/platform/mailboxes/:tenant_id/:id/support-view/:session_id/messages/:message_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformMailboxSupportView), r.h.GetMailboxSupportMessage)
	protected.Get("/platform/mailboxes/:tenant_id/:id/support-view/:session_id/messages/:message_id/attachments/:attachment_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformMailboxSupportView), r.h.GetMailboxSupportAttachment)
	protected.Post("/platform/mailboxes/:tenant_id/:id/support-view/:session_id/end", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermPlatformMailboxSupportView), r.h.EndMailboxSupportView)
	protected.Post("/platform/mailboxes/:tenant_id/bulk/status", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.BulkPlatformMailboxStatus)

	// ── Platform bulk mailbox provisioning (Stage 8) ─────────────
	// (GET .../bulk/template is registered earlier, above, to avoid
	// being shadowed by GET /platform/mailboxes/:tenant_id/:id.)
	protected.Post("/platform/mailboxes/bulk/:tenant_id/stage", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.PostPlatformBulkMailboxStage)
	protected.Post("/platform/mailboxes/bulk/:tenant_id/validate", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.PostPlatformBulkMailboxValidate)
	protected.Post("/platform/mailboxes/bulk/:tenant_id/jobs", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.PostPlatformBulkMailboxCreateJob)
	protected.Get("/platform/mailboxes/bulk/:tenant_id/jobs", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesRead), r.h.GetPlatformBulkMailboxJobs)
	protected.Get("/platform/mailboxes/bulk/:tenant_id/jobs/:jobId", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesRead), r.h.GetPlatformBulkMailboxJob)
	protected.Get("/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/rows", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesRead), r.h.GetPlatformBulkMailboxJobRows)
	protected.Post("/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/execute", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.PostPlatformBulkMailboxExecute)
	protected.Post("/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/cancel", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.PostPlatformBulkMailboxCancel)
	protected.Post("/platform/mailboxes/bulk/:tenant_id/jobs/:jobId/retry", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermMailboxesWrite), r.h.PostPlatformBulkMailboxRetry)

	protected.Get("/platform/aliases/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermAliasesRead), r.h.ListPlatformAliases)
	protected.Get("/platform/aliases/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermAliasesRead), r.h.GetPlatformAlias)
	protected.Post("/platform/aliases/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermAliasesWrite), r.h.CreatePlatformAlias)
	protected.Delete("/platform/aliases/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermAliasesWrite), r.h.DeletePlatformAlias)

	protected.Get("/platform/groups/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermGroupsRead), r.h.ListPlatformGroups)
	protected.Get("/platform/groups/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermGroupsRead), r.h.GetPlatformGroup)
	protected.Get("/platform/groups/:tenant_id/:id/members", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermGroupsRead), r.h.ListPlatformGroupMembers)
	protected.Post("/platform/groups/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermGroupsWrite), r.h.CreatePlatformGroup)
	protected.Delete("/platform/groups/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermGroupsWrite), r.h.DeletePlatformGroup)
	protected.Post("/platform/groups/:tenant_id/:id/members", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermGroupsWrite), r.h.AddPlatformGroupMember)
	protected.Delete("/platform/groups/:tenant_id/:id/members/:member_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermGroupsWrite), r.h.RemovePlatformGroupMember)

	// Platform suppression + deliverability (Milestone 9 bounded
	// context; the production service enforces suppression in the real
	// outbound path — these routes expose safe platform management and
	// metrics, all explicit-tenant). Gated by the canonical
	// platform-scoped suppressions.* / deliverability.read permissions.
	protected.Get("/platform/suppressions/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermSuppressionsRead), r.h.ListPlatformSuppressions)
	protected.Post("/platform/suppressions/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermSuppressionsWrite), r.h.AddPlatformSuppression)
	protected.Get("/platform/suppressions/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermSuppressionsRead), r.h.GetPlatformSuppression)
	protected.Get("/platform/suppressions/:tenant_id/:id/history", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermSuppressionsRead), r.h.GetPlatformSuppressionHistory)
	protected.Post("/platform/suppressions/:tenant_id/:id/release", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermSuppressionsWrite), r.h.ReleasePlatformSuppression)
	protected.Post("/platform/suppressions/:tenant_id/:id/reactivate", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermSuppressionsWrite), r.h.ReactivatePlatformSuppression)
	protected.Delete("/platform/suppressions/:tenant_id/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermSuppressionsWrite), r.h.DeletePlatformSuppression)
	protected.Delete("/platform/suppressions/:tenant_id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermSuppressionsWrite), r.h.RemovePlatformSuppression)
	protected.Get("/platform/deliverability/:tenant_id/metrics", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDeliverabilityRead), r.h.GetPlatformDeliverabilityMetrics)
	protected.Get("/platform/deliverability/:tenant_id/events", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDeliverabilityRead), r.h.ListPlatformDeliverabilityEvents)
	protected.Get("/platform/deliverability/:tenant_id/events/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermDeliverabilityRead), r.h.GetPlatformDeliverabilityEvent)

	// ── Platform relay administration (Mail-Control Phase B) ────────
	// Production relay endpoints (the same providers the outbound
	// delivery path routes through). Credentials are encrypted at
	// rest; every response is redacted; mutations are idempotent,
	// version-guarded, and typed-confirmation gated.
	protected.Get("/platform/relays", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysRead), r.h.ListPlatformRelays)
	protected.Get("/platform/relays/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysRead), r.h.GetPlatformRelay)
	protected.Post("/platform/relays", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysWrite), r.h.CreatePlatformRelay)
	protected.Patch("/platform/relays/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysWrite), r.h.UpdatePlatformRelay)
	protected.Post("/platform/relays/:id/enable", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysWrite), r.h.EnablePlatformRelay)
	protected.Post("/platform/relays/:id/disable", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysWrite), r.h.DisablePlatformRelay)
	protected.Post("/platform/relays/:id/rotate-credentials", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysWrite), r.h.RotatePlatformRelayCredentials)
	protected.Post("/platform/relays/:id/test", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysTest), r.h.TestPlatformRelay)
	protected.Delete("/platform/relays/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermRelaysWrite), r.h.DeletePlatformRelay)

	protected.Post("/webhooks/subscriptions", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysWrite), r.h.CreateWebhookSubscription)
	protected.Get("/webhooks/subscriptions", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysRead), r.h.ListWebhookSubscriptions)
	protected.Get("/webhooks/subscriptions/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysRead), r.h.GetWebhookSubscription)
	protected.Patch("/webhooks/subscriptions/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysWrite), r.h.UpdateWebhookSubscription)
	protected.Post("/webhooks/subscriptions/:id/disable", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysWrite), r.h.DisableWebhookSubscription)
	protected.Delete("/webhooks/subscriptions/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysWrite), r.h.DeleteWebhookSubscription)
	protected.Get("/webhooks/subscriptions/:id/history", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysRead), r.h.GetWebhookDeliveryHistory)
	protected.Post("/webhooks/subscriptions/:id/rotate-secret", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysWrite), r.h.RotateWebhookSecret)
	protected.Post("/webhooks/subscriptions/:id/reactivate", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysWrite), r.h.ReactivateWebhookSubscription)
	protected.Get("/webhooks/deliveries/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysRead), r.h.GetWebhookDelivery)
	protected.Post("/webhooks/deliveries/:id/replay", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysWrite), r.h.ReplayWebhookDelivery)
	protected.Post("/webhooks/deliveries/:id/retry", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermAPIKeysWrite), r.h.RetryWebhookDelivery)

	protected.Post("/automation/jobs", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermJobsWrite), r.h.SubmitTenantAutomationJob)
	protected.Get("/automation/jobs", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermJobsRead), r.h.ListTenantAutomationJobs)
	protected.Get("/automation/jobs/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermJobsRead), r.h.GetTenantAutomationJob)
	protected.Post("/automation/jobs/:id/cancel", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermJobsWrite), r.h.CancelTenantAutomationJob)
	protected.Post("/automation/jobs/:id/retry", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], authrbac.Require(authrbac.PermJobsWrite), r.h.RetryTenantAutomationJob)

	protected.Post("/platform/automation/jobs", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermJobsWrite), r.h.SubmitPlatformAutomationJob)
	protected.Get("/platform/automation/jobs", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermJobsRead), r.h.ListPlatformAutomationJobs)
	protected.Get("/platform/automation/jobs/:id", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermJobsRead), r.h.GetPlatformAutomationJob)
	protected.Post("/platform/automation/jobs/:id/cancel", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermJobsWrite), r.h.CancelPlatformAutomationJob)
	protected.Post("/platform/automation/jobs/:id/retry", platformMW[0], platformMW[1], authrbac.Require(authrbac.PermJobsWrite), r.h.RetryPlatformAutomationJob)

	// ── Platform billing balances/adjustments (Milestone 15) ───────
	protected.Get("/platform/billing/tenants/:tenant_id/overview", platformMW[0], platformMW[1], r.h.GetPlatformBillingOverview)
	protected.Get("/platform/billing/tenants/:tenant_id/balance", platformMW[0], platformMW[1], r.h.GetPlatformBillingBalance)
	protected.Post("/platform/billing/tenants/:tenant_id/adjustments", platformMW[0], platformMW[1], r.h.PostPlatformBillingAdjustment)
	protected.Get("/platform/billing/tenants/:tenant_id/adjustments", platformMW[0], platformMW[1], r.h.GetPlatformBillingAdjustments)
	protected.Get("/platform/billing/tenants/:tenant_id/reconciliation", platformMW[0], platformMW[1], r.h.GetPlatformBillingReconciliation)

	// Monitoring v1: resolve an alert (CSRF-protected, admin role).
	protected.Post("/monitoring/alerts/:id/resolve", platformMW[0], platformMW[1], r.h.PostMonitoringAlertResolve)
	// Tenants branding write (ORVIX-ADMIN-ENTERPRISE-PARITY-E):
	// CSRF-protected, admin role. logo_url must be a public
	// http(s) URL (safeExternalURL-validated in handler);
	// primary_color must match a #RRGGBB CSS hex.
	protected.Patch("/admin/tenants/:id/branding", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.PatchAdminTenantBranding)
	// Update Management v1: trigger a check or a runtime update
	// (CSRF-protected, admin role). The actual script execution is
	// single-flight: a second concurrent call returns 409 Conflict.
	protected.Post("/update/check", platformMW[0], platformMW[1], r.h.PostUpdateCheck)
	protected.Post("/update/run", platformMW[0], platformMW[1], r.h.PostUpdateRun)
	protected.Post("/firewall/rules", platformMW[0], platformMW[1], r.h.CreateFirewallRule)
	protected.Post("/license/validate", platformMW[0], platformMW[1], r.h.ValidateLicense)
	protected.Put("/feature-flags/:id", platformMW[0], platformMW[1], r.h.UpdateFeatureFlag)
	protected.Post("/api-keys", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateAPIKey)
	protected.Delete("/api-keys/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteAPIKey)
	protected.Delete("/compliance/legal-holds/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteLegalHold)
	protected.Put("/compliance/policies/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateRetentionPolicy)
	protected.Delete("/compliance/policies/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteRetentionPolicy)

	// Webmail Management — CSRF-protected write routes
	protected.Post("/webmail/sessions/:id/revoke", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.RevokeWebmailSession)
	protected.Post("/webmail/sessions/revoke-all", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.RevokeAllWebmailSessions)
	protected.Post("/webmail/controls/force-logout/:mailboxId", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ForceLogoutWebmail)
	protected.Post("/webmail/controls/unlock/:mailboxId", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UnlockWebmailMailbox)
	protected.Post("/webmail/controls/reset-preferences/:mailboxId", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ResetWebmailPreferences)
	protected.Post("/webmail/controls/clear-counters/:mailboxId", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ClearFailedLoginCounters)
	// DNS Operations (DNS-DKIM-OPERATIONS-2F): state-changing
	// routes behind CSRF middleware. DKIM keygen rotates the
	// server-side private key (irreversible — old signed mail
	// still verifies until DKIM TTL expires); provider apply
	// always returns a Failed result in this build because the
	// live API path is intentionally disabled.
	protected.Post("/admin/dns/:domain/dkim", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.PostAdminDNSDKIM)
	protected.Post("/admin/dns/:domain/provider/apply", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.PostAdminDNSProviderApply)

	// Admin MFA (CSRF-protected)
	protected.Post("/admin/mfa/setup/begin", platformMW[0], platformMW[1], r.h.MFASetupBegin)
	protected.Post("/admin/mfa/setup/verify", platformMW[0], platformMW[1], r.h.MFASetupVerify)
	protected.Post("/admin/mfa/disable", platformMW[0], platformMW[1], r.h.MFADisable)

	// Admin Settings write (CSRF-protected)
	protected.Patch("/admin/settings", platformMW[0], platformMW[1], r.h.AdminSettingsPatch)

	// Admin Enterprise v2 mutations (CSRF-protected, admin
	// role). Every mutation writes an entry to coremail_audit
	// (action="<resource>.<verb>", target=<identifier>,
	// result="ok"). Refusal paths return 4xx with a stable
	// error JSON; never fabricate success.
	protected.Post("/admin/account-classes", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateAccountClass)
	protected.Patch("/admin/account-classes/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateAccountClass)
	protected.Delete("/admin/account-classes/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteAccountClass)
	protected.Post("/admin/domain-groups", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateDomainGroup)
	protected.Put("/admin/domain-groups/:id/members", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateDomainGroupMembers)
	protected.Delete("/admin/domain-groups/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteDomainGroup)
	protected.Post("/admin/mailing-lists", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateMailingList)
	protected.Patch("/admin/mailing-lists/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.PatchMailingList)
	protected.Delete("/admin/mailing-lists/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteMailingList)
	protected.Post("/admin/mailing-lists/:id/members", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.AddMailingListMember)
	protected.Delete("/admin/mailing-lists/:id/members/:memberId", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.RemoveMailingListMember)
	protected.Post("/admin/public-folders", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreatePublicFolder)
	protected.Patch("/admin/public-folders/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.PatchPublicFolder)
	protected.Delete("/admin/public-folders/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeletePublicFolder)
	protected.Post("/admin/admin-groups", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateAdminGroup)
	protected.Patch("/admin/admin-groups/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateAdminGroup)
	protected.Delete("/admin/admin-groups/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteAdminGroup)
	protected.Post("/admin/admin-groups/:id/members", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.AddAdminGroupMember)
	protected.Delete("/admin/admin-groups/:id/members/:userId", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.RemoveAdminGroupMember)
	protected.Post("/admin/quarantine/:id/resolve", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ResolveQuarantine)
	protected.Post("/admin/acl-rules", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateACLRule)
	protected.Delete("/admin/acl-rules/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteACLRule)
	protected.Post("/admin/login-protection/lockouts/:key/clear", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.ClearLockout)
	protected.Post("/admin/admin-users", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateAdminUser)
	protected.Patch("/admin/admin-users/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateAdminUser)
	protected.Patch("/admin/admin-users/:id/password", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateAdminUserPassword)
	protected.Patch("/admin/admin-users/:id/status", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateAdminUserStatus)
	protected.Patch("/admin/admin-users/:id/groups", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateAdminUserGroups)
	protected.Delete("/admin/admin-users/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteAdminUser)
	protected.Post("/admin/log-rules", platformMW[0], platformMW[1], r.h.CreateLogRule)
	protected.Delete("/admin/log-rules/:id", platformMW[0], platformMW[1], r.h.DeleteLogRule)
	// Enterprise v3 — CSRF-protected mutations for the new
	// sections. Each one is mounted inside `men` so the
	// X-CSRF-Token check runs before the handler. All
	// handlers in enterprise_admin_v3.go + ssl.go write to
	// the audit table via h.appendAudit.
	protected.Post("/admin/ssl/certificates", platformMW[0], platformMW[1], r.h.AdminSslUploadCertificate)
	protected.Post("/admin/ssl/certificates/reload", platformMW[0], platformMW[1], r.h.AdminSslReloadCertificates)
	protected.Delete("/admin/ssl/certificates/:id", platformMW[0], platformMW[1], r.h.AdminSslDeleteCertificate)
	protected.Post("/admin/acceptance-rules", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateAcceptanceRule)
	protected.Patch("/admin/acceptance-rules/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateAcceptanceRule)
	protected.Post("/admin/acceptance-rules/test", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.TestAcceptanceRule)
	protected.Delete("/admin/acceptance-rules/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteAcceptanceRule)
	protected.Post("/admin/incoming-msg-rules", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateIncomingMsgRule)
	protected.Patch("/admin/incoming-msg-rules/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateIncomingMsgRule)
	protected.Delete("/admin/incoming-msg-rules/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteIncomingMsgRule)
	protected.Post("/admin/migration-sources", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateMigrationSource)
	protected.Patch("/admin/migration-sources/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateMigrationSource)
	protected.Delete("/admin/migration-sources/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteMigrationSource)
	protected.Post("/admin/migration-sources/:id/test", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.TestMigrationSource)
	protected.Post("/admin/backup-targets", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.CreateBackupTarget)
	protected.Patch("/admin/backup-targets/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.UpdateBackupTarget)
	protected.Delete("/admin/backup-targets/:id", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.DeleteBackupTarget)
	protected.Post("/admin/backup-targets/:id/test", tenantCompatMW[0], tenantCompatMW[1], tenantCompatMW[2], r.h.TestBackupTarget)
	protected.Patch("/admin/settings/protocol/:protocol", platformMW[0], platformMW[1], r.h.PatchProtocolSettings)
}

func (r *Router) setupAdminUI() {
	adminDir := r.cfg.Server.AdminUIDir
	if adminDir == "" {
		adminDir = "/usr/share/orvix/admin"
	}
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

	// Keep unknown API reads inside the API surface. Without this guard the
	// marketing SPA catch-all below would return HTML for a misspelled API URL.
	r.app.Get("/api/v1/*", func(c fiber.Ctx) error {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "api endpoint not found",
		})
	})

	marketingDir := r.cfg.Server.MarketingUIDir
	if marketingDir == "" {
		marketingDir = "/usr/share/orvix/marketing"
	}
	r.serveMarketingSPA(marketingDir)
}

func (r *Router) serveMarketingSPA(dir string) {
	indexPath := filepath.Join(dir, "index.html")
	notFoundPath := filepath.Join(dir, "404.html")

	r.app.Get("/", func(c fiber.Ctx) error {
		return c.SendFile(indexPath)
	})
	r.app.Get("/*", func(c fiber.Ctx) error {
		requestPath := strings.TrimPrefix(c.Params("*"), "/")
		clean := filepath.Clean(filepath.FromSlash(requestPath))
		if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
			return c.SendStatus(fiber.StatusBadRequest)
		}

		target := filepath.Join(dir, clean)
		if info, err := os.Stat(target); err == nil {
			if !info.IsDir() {
				return c.SendFile(target)
			}
			routeIndex := filepath.Join(target, "index.html")
			if routeInfo, routeErr := os.Stat(routeIndex); routeErr == nil && !routeInfo.IsDir() {
				return c.SendFile(routeIndex)
			}
		}

		return c.Status(fiber.StatusNotFound).SendFile(notFoundPath)
	})
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
