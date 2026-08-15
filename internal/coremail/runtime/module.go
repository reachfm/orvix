package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	domainpkg "github.com/orvix/orvix/internal/admin/domain"
	"github.com/orvix/orvix/internal/antivirus"
	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/antispam"
	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/coremail/imap"
	"github.com/orvix/orvix/internal/coremail/jmap"
	"github.com/orvix/orvix/internal/coremail/mailpolicy"
	"github.com/orvix/orvix/internal/coremail/pop3"
	"github.com/orvix/orvix/internal/coremail/push"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/rules"
	"github.com/orvix/orvix/internal/coremail/smtp"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/dbdialect"
	"github.com/orvix/orvix/internal/licensing"
	"github.com/orvix/orvix/internal/licensingauthority"
	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/platform/cluster"
	"github.com/orvix/orvix/internal/platform/deliverability"
	"github.com/orvix/orvix/internal/platform/kernel"
	"github.com/orvix/orvix/internal/platform/relay"
	"github.com/orvix/orvix/internal/platform/security"
	"github.com/orvix/orvix/internal/policy"
	"github.com/orvix/orvix/internal/ruler"
	orvixruntime "github.com/orvix/orvix/internal/runtime"
	"github.com/orvix/orvix/internal/trust"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Module wires the native CoreMail engine into the production module registry.
type Module struct {
	logger *zap.Logger
	cfg    *config.Config
	db     *sql.DB

	engine       *coremail.Engine
	store        *storage.MailStore
	queue        *queue.QueueEngine
	obs          *observability.Observability
	policyEngine *policy.Engine
	trustEngine  *trust.Engine
	auditStore   *audit.Store
	// mailPolicy is the canonical mailbox-level mail-access policy
	// (MAILBOX-ACCESS-MODE-PHASE1), wired into every real delivery
	// path. nil when coremail is disabled.
	mailPolicy       *mailpolicy.Policy
	licenseSvc       *licensing.Service
	authorityService *licensingauthority.AuthorityService
	// avEngine is the wired antivirus scanner. Non-nil
	// means the SMTP receiver is calling it on every
	// accepted message — the admin endpoint reports
	// runtime_enforced via avEngine.RuntimeEnforced().
	avEngine *antivirus.Engine
	// rulerEngine owns both acceptance and incoming
	// message rule engines; the SMTP command handler
	// and the SMTP receiver each call into it via the
	// smtp.RuleEvaluator interface. Non-nil here means
	// the rule tables are LIVE (not just stored).
	rulerEngine *ruler.Engine

	smtpServer        *smtp.Server
	submissionServer  *smtp.Server
	submissionHandler *smtp.CommandHandler
	smtpsServer       *smtp.Server
	imapServer        *imap.Server
	imapsServer       *imap.Server
	pop3Server        *pop3.Server
	pop3sServer       *pop3.Server
	jmapServer        *jmap.Server
	workers           []*delivery.DeliveryWorker
	// clusterSvc is the Milestone 10 node registry/placement/lease
	// service. nil if schema init failed (never blocks startup).
	clusterSvc *cluster.Service
	// securitySvc is the Milestone 12 normalized security-event
	// recorder. nil if schema init failed (never blocks startup).
	securitySvc *security.Service

	// pushNotifier is the Web Push (RFC 8030 / RFC 8291) dispatcher.
	// It is constructed in initCore from cfg.CoreMail.VAPIDPublicKey
	// / VAPIDPrivateKey / VAPIDSubject. When both keys are present
	// the notifier is enabled; when either key is missing, a
	// disabled notifier (with nil repo is fine; IsEnabled returns
	// false) is still attached so worker.PushNotifier != nil but
	// NotifyMailboxMessage is a no-op. The /api/v1/webmail/push/*
	// endpoints read h.pushNotifier.IsEnabled() to decide whether
	// to serve 503 or the real status.
	pushNotifier *push.PushNotifier

	// tlsLoadErr is non-nil when the SMTP TLS cert/key were configured
	// but failed to load. The runtime does NOT abort initCore on this
	// failure — instead the submission listener is skipped and the
	// listener registry reports the specific reason so the operator can
	// fix it without taking the whole mail server down.
	tlsLoadErr error

	// listenerReg records live listener startup state for the
	// admin runtime telemetry endpoint. Populated by startServer.
	listenerReg *orvixruntime.ListenerRegistry

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func New(logger *zap.Logger) *Module {
	return &Module{logger: logger}
}

func (m *Module) ID() string { return "coremail-runtime" }

func (m *Module) Version() string { return "1.0.0" }

func (m *Module) Requires() []string { return nil }

// SetListenerRegistry wires the shared listener state registry
// into the module so startServer can record bind success/failure
// for the admin runtime telemetry endpoint. Must be called before
// Start().
func (m *Module) SetListenerRegistry(r *orvixruntime.ListenerRegistry) {
	m.listenerReg = r
}

func (m *Module) Init(cfg *config.Config, db *gorm.DB) error {
	m.cfg = cfg
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("coremail db: %w", err)
	}
	m.db = sqlDB
	return m.initCore(cfg, sqlDB)
}

// initCore initializes the module from a *sql.DB (shared between Init and tests).
func (m *Module) initCore(cfg *config.Config, sqlDB *sql.DB) error {
	if !cfg.CoreMail.Enabled {
		if m.logger != nil {
			m.logger.Info("coremail runtime disabled")
		}
		return nil
	}

	if err := m.Migrate(); err != nil {
		return err
	}

	authCfg := coremail.AuthConfig{
		Argon2Time:    cfg.Auth.Argon2Time,
		Argon2Memory:  cfg.Auth.Argon2Memory,
		Argon2Threads: cfg.Auth.Argon2Threads,
		Argon2KeyLen:  32,
	}
	m.engine = coremail.NewEngine(coremail.EngineConfig{DB: sqlDB, AuthCfg: authCfg})

	// Canonical mailbox-level mail-access policy
	// (MAILBOX-ACCESS-MODE-PHASE1). One instance is wired into every
	// real delivery path below: SMTP inbound/outbound, the delivery
	// workers (relay + retry), the rules runner (forwarding/vacation),
	// and JMAP Submission/set. The sink emits safe observable events
	// via the runtime logger; it never receives message content.
	m.mailPolicy = mailpolicy.New(&mailpolicy.EngineStore{Engine: m.engine}, runtimePolicySink{logger: m.logger})

	var err error
	m.store, err = storage.NewMailStore(sqlDB, cfg.CoreMail.MailStorePath)
	if err != nil {
		return fmt.Errorf("coremail mailstore: %w", err)
	}
	queueEngine, queueErr := queue.NewQueueEngineChecked(sqlDB)
	if queueErr != nil {
		return fmt.Errorf("initialize queue repository: %w", queueErr)
	}
	m.queue = queueEngine
	m.obs = observability.NewObservability(1000, 5000)

	// Initialize licensing service (retained for SaaS quota enforcement).
	// Local product-license files are retired; no license file is loaded.
	m.licenseSvc = licensing.NewService("")

	// Initialize license authority service — no network calls, non-blocking.
	cachePath := cfg.CoreMail.LicenseAuthorityCachePath
	var authorityClient licensingauthority.LicenseAuthorityClient
	if cfg.CoreMail.LicenseAuthorityURL != "" {
		httpClient, err := licensingauthority.NewHTTPAuthorityClient(licensingauthority.HTTPAuthorityConfig{
			BaseURL:  cfg.CoreMail.LicenseAuthorityURL,
			Timeout:  cfg.CoreMail.LicenseAuthorityTimeout,
			TestMode: cfg.CoreMail.LicenseAuthorityTestMode,
		})
		if err != nil {
			if m.logger != nil {
				m.logger.Warn("authority HTTP client init failed, falling back to noop", zap.Error(err))
			}
			authorityClient = &licensingauthority.NoopAuthorityClient{}
		} else {
			authorityClient = httpClient
		}
	} else {
		authorityClient = &licensingauthority.NoopAuthorityClient{}
	}
	m.authorityService = licensingauthority.NewAuthorityService(
		authorityClient,
		cachePath,
	)

	policyRepo := policy.NewRepository(sqlDB)
	m.policyEngine = policy.NewEngine()
	m.policyEngine.SetRepository(policyRepo)
	if err := m.policyEngine.LoadFromDB(context.Background()); err != nil {
		return fmt.Errorf("policy recovery: %w", err)
	}
	trustRepo := trust.NewRepository(sqlDB)
	m.trustEngine = trust.NewEngineWithRepo(trustRepo)
	if err := m.trustEngine.LoadFromDB(context.Background()); err != nil {
		return fmt.Errorf("trust recovery: %w", err)
	}
	m.auditStore = audit.NewStore(sqlDB)
	if err := m.auditStore.EnsureTable(context.Background()); err != nil {
		return fmt.Errorf("audit migration: %w", err)
	}
	m.obs.Health.Ready(observability.HealthCheckDatabase)
	// Licensing health: local product licensing is retired.
	// Licensing service is retained for SaaS quota enforcement only.
	m.obs.Health.Ready("licensing")
	m.obs.Health.Ready(observability.HealthCheckMailStore)
	m.obs.Health.Ready(observability.HealthCheckQueue)

	identity := smtp.NewIdentityService(m.engine)
	smtpAuth := smtp.NewAuthenticator(identity)
	smtpCfg := smtp.DefaultConfig()
	smtpCfg.Hostname = cfg.CoreMail.Hostname
	smtpCfg.TLSCertFile = cfg.CoreMail.TLSCertFile
	smtpCfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
	// LoadTLSConfig is tolerant of "no cert configured" (returns nil, nil)
	// but a real cert-load failure (bad path, malformed PEM, etc.) is
	// treated as a soft warning rather than a fatal initCore error. This
	// keeps port 25 inbound alive even if the operator's submission TLS
	// setup is broken, and surfaces the specific reason via listener
	// telemetry so the admin dashboard shows "disabled: <reason>".
	tlsCfg, tlsLoadErr := smtp.LoadTLSConfig(smtpCfg)
	if tlsLoadErr != nil {
		m.tlsLoadErr = tlsLoadErr
		if m.logger != nil {
			m.logger.Warn("SMTP TLS certificate/key failed to load — submission listener disabled; inbound STARTTLS disabled until fixed",
				zap.String("reason", safeTLSLoadError(tlsLoadErr)),
			)
		}
	}
	receiver := smtp.NewReceiver(m.engine, m.store, m.queue, smtpCfg)
	receiver.AntiSpamEngine = antispam.NewEngine(nil)
	receiver.Observability = m.obs

	// Rules engine runner. Wired into the SMTP receiver so
	// every locally-delivered inbound message is fed
	// through the rules engine AFTER the durable StoreMessage
	// call. The receiver applies the runner's outputs
	// (move / flag / keep-copy) defensively — see
	// internal/coremail/smtp/rules_apply.go for the full
	// contract. The runner shares the same MailStore +
	// QueueEngine the receiver uses, so forward and
	// vacation replies go through the existing
	// queue / outbound path — no raw SMTP, no parallel
	// pipeline. The logger is m.logger so the runner's
	// own audit logs flow into the same zap pipeline as
	// the rest of the runtime.
	receiver.RulesRunner = rules.NewRunner(rules.Dependencies{
		MailStore:        m.store,
		QueueEngine:      m.queue,
		Vacation:         m.store.Vacation,
		Forwarding:       m.store.Forwarding,
		Logger:           m.logger,
		MailAccessPolicy: m.mailPolicy,
	})

	// ── Antivirus engine ───────────────────────────────────
	// Wire the antivirus engine unconditionally so the
	// runtime exposes the same shape as the admin
	// expects. cfg.ClamAV.Enabled == false makes the
	// engine accept-and-audit; cfg.ClamAV.Enabled == true
	// makes the engine dial the daemon. The runtime
	// ONLY flips MarkEnforced() when the wiring is
	// real (calls exist in this file), so the admin
	// status endpoint's runtime_enforced flag stays
	// honest even if the operator disables the
	// scanner at runtime via SetPolicy.
	policy := antivirus.Policy{
		OnInfected:           "reject",
		OnScannerUnavailable: "fail_closed",
		TimeoutMS:            30000,
	}
	switch strings.ToLower(cfg.ClamAV.Mode) {
	case "quarantine":
		policy.OnInfected = "quarantine"
	case "tag":
		policy.OnInfected = "tag"
	case "fail_open":
		policy.OnScannerUnavailable = "fail_open"
	}
	avEngine, avErr := antivirus.New(antivirus.Config{
		Enabled: cfg.ClamAV.Enabled,
		Host:    cfg.ClamAV.Host,
		Port:    cfg.ClamAV.Port,
	}, policy, m.logger, m.obs, m.auditStore)
	if avErr != nil {
		m.logger.Warn("antivirus: invalid policy, engine not wired",
			zap.Error(avErr))
	} else {
		m.avEngine = avEngine
		receiver.AntivirusEngine = avEngine
		receiver.DB = sqlDB
		avEngine.MarkEnforced()
		if cfg.ClamAV.Enabled {
			m.logger.Info("antivirus engine wired",
				zap.String("host", cfg.ClamAV.Host),
				zap.Int("port", cfg.ClamAV.Port),
				zap.String("on_infected", policy.OnInfected),
				zap.String("on_scanner_unavailable", policy.OnScannerUnavailable))
		} else {
			m.logger.Info("antivirus engine wired but disabled (cfg.ClamAV.Enabled=false) — runtime accepts every message")
		}
	}

	// ── Acceptance & incoming rule engines ────────────────
	// The runtime installs a single internal/ruler.Engine
	// that exposes both evaluators through the
	// smtp.RuleEvaluator interface. Each evaluator is
	// marked enforced ONLY when wired into the receive
	// path, which is unconditional here.
	rulerEngine := ruler.New(sqlDB, m.logger, m.obs)
	m.rulerEngine = rulerEngine
	rulerEngine.MarkEnforced()
	receiver.AcceptanceEngine = rulerEngine
	receiver.IncomingRuleEngine = rulerEngine

	// ── Inbound SMTP (port 25, MX) ─────────────────────────
	inboundCfg := smtp.InboundConfig()
	inboundCfg.Hostname = cfg.CoreMail.Hostname
	inboundCfg.TLSCertFile = cfg.CoreMail.TLSCertFile
	inboundCfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
	inboundCfg.SpamMode = smtpCfg.SpamMode
	inboundHandler := smtp.NewCommandHandler(inboundCfg, smtpAuth, smtp.NewSession("runtime-init", tlsCfg, inboundCfg))
	inboundHandler.SetAcceptanceEngine(m.rulerEngine)
	m.smtpServer = smtp.NewServer(inboundCfg, inboundHandler, receiver)
	m.smtpServer.TLSConfig = tlsCfg
	m.smtpServer.RecipientValidator = func(ctx context.Context, address string) (bool, error) {
		_, err := m.engine.Auth.ResolveAddress(ctx, address)
		return err == nil, err
	}
	m.smtpServer.SetLocalDomainChecker(identity.IsLocalDomain)
	m.smtpServer.SetMailAccessModeChecker(identity.MailAccessMode)
	m.smtpServer.SetMailAccessPolicy(m.smtpMailAccessPolicy)
	m.smtpServer.Observability = m.obs

	// ── Submission SMTP (port 587, STARTTLS) ───────────────
	// Submission requires a valid TLS cert/key pair. The listener is
	// only created when:
	//   * submission_enabled=true
	//   * TLS cert file is configured
	//   * TLS key file is configured
	//   * cert/key load successfully (no tlsLoadErr)
	// If any of these fail, the listener is NOT created — no plaintext
	// AUTH is exposed — and the listener registry records the exact
	// reason ("disabled by config" vs "TLS missing" vs "TLS invalid").
	if cfg.CoreMail.SubmissionEnabled {
		switch {
		case cfg.CoreMail.TLSCertFile == "" || cfg.CoreMail.TLSKeyFile == "":
			if m.logger != nil {
				m.logger.Warn("submission listener disabled: TLS certificate/key not configured")
			}
		case tlsLoadErr != nil:
			if m.logger != nil {
				m.logger.Warn("submission listener disabled: TLS certificate/key failed to load",
					zap.String("reason", safeTLSLoadError(tlsLoadErr)),
				)
			}
		default:
			subCfg := smtp.SubmissionConfig()
			subCfg.Hostname = cfg.CoreMail.Hostname
			subCfg.TLSCertFile = cfg.CoreMail.TLSCertFile
			subCfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
			subHandler := smtp.NewCommandHandler(subCfg, smtpAuth, smtp.NewSession("runtime-init", tlsCfg, subCfg))
			subHandler.SetAcceptanceEngine(m.rulerEngine)
			m.submissionHandler = subHandler
			m.submissionServer = smtp.NewServer(subCfg, subHandler, receiver)
			m.submissionServer.TLSConfig = tlsCfg
			m.submissionServer.SetLocalDomainChecker(identity.IsLocalDomain)
			m.submissionServer.SetMailAccessModeChecker(identity.MailAccessMode)
			m.submissionServer.SetMailAccessPolicy(m.smtpMailAccessPolicy)
			m.submissionServer.SenderValidator = identity.ResolveSender
			m.submissionServer.Observability = m.obs
		}
	}

	// ── SMTPS (port 465, implicit TLS) — config exists but not implemented.
	// The SMTPsEnabled flag defaults to false. When enabled, a warning is logged.
	if cfg.CoreMail.SMTPsEnabled {
		if m.logger != nil {
			m.logger.Warn("SMTPS (port 465 implicit TLS) is not yet implemented; listener will not start")
		}
	}

	imapCfg := imap.DefaultConfig()
	imapCfg.Hostname = cfg.CoreMail.Hostname
	imapCfg.TLSCertFile = cfg.CoreMail.TLSCertFile
	imapCfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
	imapCfg.RequireTLSForAuth = cfg.CoreMail.RequireTLSForAuth
	m.imapServer = imap.NewServer(imapCfg, m.store, &imapMailboxAuth{auth: m.engine.Auth})
	m.imapServer.Observability = m.obs

	pop3Cfg := pop3.DefaultConfig()
	pop3Cfg.Hostname = cfg.CoreMail.Hostname
	pop3Cfg.TLSCertFile = cfg.CoreMail.TLSCertFile
	pop3Cfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
	pop3Cfg.RequireTLSForAuth = cfg.CoreMail.RequireTLSForAuth
	m.pop3Server = pop3.NewServer(pop3Cfg, m.store, pop3.NewAuthenticator(&pop3MailboxAuth{auth: m.engine.Auth}))
	m.pop3Server.Observability = m.obs

	// ── IMAPS (port 993, implicit TLS) ──────────────────────
	if cfg.CoreMail.IMAPsEnabled {
		switch {
		case cfg.CoreMail.TLSCertFile == "" || cfg.CoreMail.TLSKeyFile == "":
			if m.logger != nil {
				m.logger.Warn("IMAPS listener disabled: TLS certificate/key not configured")
			}
		case tlsLoadErr != nil:
			if m.logger != nil {
				m.logger.Warn("IMAPS listener disabled: TLS certificate/key failed to load",
					zap.String("reason", safeTLSLoadError(tlsLoadErr)),
				)
			}
		default:
			imapsCfg := imap.DefaultConfig()
			imapsCfg.Hostname = cfg.CoreMail.Hostname
			imapsCfg.TLSCertFile = cfg.CoreMail.TLSCertFile
			imapsCfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
			imapsCfg.RequireTLSForAuth = cfg.CoreMail.RequireTLSForAuth
			m.imapsServer = imap.NewServer(imapsCfg, m.store, &imapMailboxAuth{auth: m.engine.Auth})
			m.imapsServer.Observability = m.obs
		}
	}

	// ── POP3S (port 995, implicit TLS) ─────────────────────
	if cfg.CoreMail.POP3sEnabled {
		switch {
		case cfg.CoreMail.TLSCertFile == "" || cfg.CoreMail.TLSKeyFile == "":
			if m.logger != nil {
				m.logger.Warn("POP3S listener disabled: TLS certificate/key not configured")
			}
		case tlsLoadErr != nil:
			if m.logger != nil {
				m.logger.Warn("POP3S listener disabled: TLS certificate/key failed to load",
					zap.String("reason", safeTLSLoadError(tlsLoadErr)),
				)
			}
		default:
			pop3sCfg := pop3.DefaultConfig()
			pop3sCfg.Hostname = cfg.CoreMail.Hostname
			pop3sCfg.TLSCertFile = cfg.CoreMail.TLSCertFile
			pop3sCfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
			pop3sCfg.RequireTLSForAuth = cfg.CoreMail.RequireTLSForAuth
			m.pop3sServer = pop3.NewServer(pop3sCfg, m.store, pop3.NewAuthenticator(&pop3MailboxAuth{auth: m.engine.Auth}))
			m.pop3sServer.Observability = m.obs
		}
	}

	// JMAP
	m.jmapServer = jmap.NewServer(m.engine)
	m.jmapServer.Hostname = cfg.CoreMail.Hostname
	m.jmapServer.Observability = m.obs
	// Wire the same delivery queue the SMTP receiver and the webmail
	// send path use, so Submission/set is a real enqueue — and the
	// canonical mail-access policy, so an internal-only mailbox can
	// never submit to an external recipient through JMAP.
	m.jmapServer.SetQueueEngine(m.queue)
	m.jmapServer.SetMailAccessPolicy(m.mailPolicy)
	m.obs.Health.Ready("jmap")

	workerCount := cfg.CoreMail.QueueWorkers
	if workerCount < 1 {
		workerCount = 1
	}
	transportCfg := delivery.DefaultTransportConfig()
	if cfg.Outbound.TLSPolicy != "" {
		parsed, err := delivery.ParseTLSPolicy(cfg.Outbound.TLSPolicy)
		if err != nil {
			return fmt.Errorf("outbound.tls_policy: %w", err)
		}
		transportCfg.TLSPolicy = parsed
	}
	// Outbound relay control plane (Milestone 7) — optional. A schema
	// init failure disables relay routing for this run; every worker
	// simply keeps its RelaySelector nil and delivers direct-to-MX
	// exactly as before this integration existed.
	//
	// F9: the runtime relay service is wired with the canonical
	// transactional outbox, so circuit transitions and other meaningful
	// relay state changes are published as operational events. It was
	// constructed with `relay.NewService(relayRepo, nil, nil)`, so
	// RecordAttemptResult skipped outbox enqueue entirely and a tripped
	// circuit produced no event for operators. The outbox is a single
	// canonical instance (same dialect detection as the webhook outbox),
	// and a failure to initialize it degrades relay routing explicitly
	// rather than running silently without events.
	relayDialect, rderr := dbdialect.Detect(sqlDB)
	if rderr != nil {
		if m.logger != nil {
			m.logger.Warn("relay database dialect detection failed; outbound relay disabled", zap.Error(rderr))
		}
	}
	relayRepo, relayRepoErr := relay.NewRepositoryChecked(sqlDB)
	var relayAdapter *relay.DeliveryAdapter
	if relayRepoErr != nil {
		if m.logger != nil {
			m.logger.Warn("relay repository init failed; outbound relay disabled", zap.Error(relayRepoErr))
		}
	} else if err := relayRepo.EnsureSchema(context.Background()); err != nil {
		if m.logger != nil {
			m.logger.Warn("relay control plane schema init failed; outbound relay disabled", zap.Error(err))
		}
	} else if relayDialect != nil {
		relayOutbox := kernel.NewOutboxRepository(relayDialect)
		if oerr := relayOutbox.EnsureSchema(context.Background(), sqlDB); oerr != nil {
			if m.logger != nil {
				m.logger.Warn("relay outbox init failed; outbound relay disabled", zap.Error(oerr))
			}
		} else {
			relayAdapter = relay.NewDeliveryAdapter(relay.NewService(relayRepo, nil, relayOutbox))
		}
	}
	// Sending-identity resolution for relay routing.
	//
	// Three defects are closed here:
	//
	//  1. The statement used a raw `?` placeholder. On PostgreSQL that is a
	//     syntax error, the error was discarded by `_ = row.Scan(...)`, and
	//     every outbound message therefore resolved to tenant 0 with the
	//     default mail-access mode — so tenant-scoped relay routing and the
	//     internal-only policy check were both inert on the dialect that
	//     ships in production. It now goes through dbdialect.
	//  2. The sending domain's row id was never resolved at all, so
	//     domain-scoped routing rules could not match. It is selected here and
	//     handed to the worker via DomainIDForRelay.
	//  3. (F3) Every identity-lookup failure collapsed to tenant 0 +
	//     "internal_external" — a permissive anonymous identity — and the
	//     query ran under unbounded context.Background(). Identity failures
	//     are now typed and the worker defers before any network I/O.
	//
	// senderIdentityQueryTimeout bounds a single identity lookup well below
	// the queue lease (300s), so a hung or locked database cannot pin a
	// worker goroutine indefinitely.
	const senderIdentityQueryTimeout = 5 * time.Second
	senderIdentityQuery := relayDialect.Rewrite(
		`SELECT m.tenant_id, m.domain_id, COALESCE(m.mail_access_mode,'inherit'), COALESCE(d.mail_access_mode,'')
		 FROM coremail_mailboxes m JOIN coremail_domains d ON d.id = m.domain_id
		 WHERE m.email = ?`)
	type senderIdentity struct {
		tenantID uint
		domainID uint
		mode     string
	}
	// senderIdentityError signals that a sender identity could not be
	// established for a reason other than "the address is genuinely not
	// a local mailbox" (which is sql.ErrNoRows). The delivery worker
	// treats it as fail-closed: defer, never fabricate an anonymous
	// permissive identity.
	var errSenderIdentityUnavailable = errors.New("sender identity unavailable")

	// resolveSenderIdentity FAILS CLOSED (F3): the previous form returned
	// {tenantID: 0, mode: "internal_external"} on EVERY error, so a
	// transient database failure silently disabled tenant-scoped relay
	// rules and the internal-only policy — and the query ran under
	// unbounded context.Background(). Distinctions:
	//   - row found: identity (tenant, domain, mode) as stored;
	//   - sql.ErrNoRows: the sender is genuinely not a local mailbox;
	//     the caller decides policy from the enqueue origin, never from
	//     a fabricated identity;
	//   - any other error (database unavailable, timeout, malformed
	//     row): a typed identity-unavailable error → the worker defers
	//     BEFORE any direct or relay network I/O.
	// The query runs under the delivery operation's context with a hard
	// timeout well below the queue lease, so a hung database cannot pin
	// a worker indefinitely.
	resolveSenderIdentity := func(ctx context.Context, entry *queue.QueueEntry) (senderIdentity, error) {
		qctx, cancel := context.WithTimeout(ctx, senderIdentityQueryTimeout)
		defer cancel()
		id := senderIdentity{}
		var mailboxMode, domainMode string
		row := sqlDB.QueryRowContext(qctx, senderIdentityQuery, entry.FromAddress)
		if err := row.Scan(&id.tenantID, &id.domainID, &mailboxMode, &domainMode); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Genuinely absent sender: not an infrastructure failure.
				// The caller uses the enqueue origin; we never fabricate a
				// tenant or a permissive mode from nothing.
				return senderIdentity{}, sql.ErrNoRows
			}
			if m.logger != nil {
				m.logger.Warn("relay sender identity lookup failed", zap.Error(err))
			}
			return senderIdentity{}, errSenderIdentityUnavailable
		}
		// The EFFECTIVE mode is resolved through the canonical policy
		// resolution function (mailbox mode wins; inherit falls back
		// to the domain; corrupt values fail closed to internal_only)
		// so relay routing sees exactly the mode the delivery paths
		// enforce.
		eff, _ := mailpolicy.ResolveEffectiveMode(mailboxMode, domainMode)
		id.mode = string(eff.Effective)
		return id, nil
	}
	tenantForRelay := func(ctx context.Context, entry *queue.QueueEntry) (uint, string, error) {
		id, err := resolveSenderIdentity(ctx, entry)
		if err != nil {
			return 0, "", err
		}
		return id.tenantID, id.mode, nil
	}
	domainForRelay := func(ctx context.Context, entry *queue.QueueEntry) (uint, error) {
		id, err := resolveSenderIdentity(ctx, entry)
		if err != nil {
			return 0, err
		}
		return id.domainID, nil
	}

	// Deliverability control plane (Milestone 9) — optional, same
	// fail-safe-to-disabled pattern as the relay control plane above.
	deliverabilityRepo := deliverability.NewRepository(sqlDB)
	var deliverabilityAdapter *deliverability.DeliveryAdapter
	if err := deliverabilityRepo.EnsureSchema(context.Background()); err != nil {
		if m.logger != nil {
			m.logger.Warn("deliverability schema init failed; suppression enforcement and reputation signals disabled", zap.Error(err))
		}
	} else {
		deliverabilityAdapter = deliverability.NewDeliveryAdapter(deliverability.NewService(deliverabilityRepo, audit.NewExtendedStore(sqlDB), nil, nil))
	}

	// Cluster control plane (Milestone 10) — a fresh/single-node
	// install self-enrolls as its own sole node on first boot and
	// simply heartbeats on every later boot; nothing about existing
	// single-node behavior changes if the multi-node APIs are never
	// used.
	clusterRepo := cluster.NewRepository(sqlDB)
	if err := clusterRepo.EnsureSchema(context.Background()); err != nil {
		if m.logger != nil {
			m.logger.Warn("cluster schema init failed; node registry disabled", zap.Error(err))
		}
	} else {
		m.clusterSvc = cluster.NewService(clusterRepo, audit.NewExtendedStore(sqlDB), nil, nil)
		selfID := cfg.CoreMail.Hostname
		if selfID == "" {
			selfID = "orvix-node"
		}
		alreadyEnrolled, _, err := m.clusterSvc.EnsureSelfNode(context.Background(), selfID, cluster.Node{
			Role: "all-in-one", Capabilities: []string{"smtp", "delivery_worker", "imap", "pop3", "jmap"},
		})
		if err != nil && m.logger != nil {
			m.logger.Warn("cluster self-enrollment failed", zap.Error(err))
		} else if !alreadyEnrolled && m.logger != nil {
			m.logger.Info("cluster node self-enrolled", zap.String("node_id", selfID))
		}
	}

	// Security event normalization (Milestone 12) — the recording
	// choke point for antivirus/antispam/ACL/auth events. Wired here
	// so it's available to future producers; individual subsystems
	// (internal/antivirus.Engine, internal/coremail/antispam.Engine,
	// the ACL enforcement path) emitting INTO it is a follow-up
	// integration slice, not fabricated here.
	securityRepo := security.NewRepository(sqlDB)
	if err := securityRepo.EnsureSchema(context.Background()); err != nil {
		if m.logger != nil {
			m.logger.Warn("security event schema init failed; normalized security events disabled", zap.Error(err))
		}
	} else {
		m.securitySvc = security.NewService(securityRepo, nil)
	}

	m.workers = make([]*delivery.DeliveryWorker, 0, workerCount)
	for i := 0; i < workerCount; i++ {
		worker := delivery.NewDeliveryWorker(
			m.queue,
			m.store,
			delivery.NewDNSResolver(),
			delivery.NewSMTPTransport(transportCfg),
			cfg.CoreMail.Hostname,
			fmt.Sprintf("coremail-worker-%d", i+1),
		)
		worker.Observability = m.obs
		worker.PreferIPv4 = cfg.Outbound.PreferIPv4
		// Canonical mail-access policy on the worker: every outbound
		// delivery (initial, relay, retry) is policy-checked before
		// any network I/O.
		worker.MailAccessPolicy = m.mailPolicy
		if relayAdapter != nil {
			worker.RelaySelector = relayAdapter
			worker.TenantIDForRelay = tenantForRelay
			worker.DomainIDForRelay = domainForRelay
			// Bookkeeping failures after an already-completed SMTP
			// transaction are surfaced, not swallowed: the mail was
			// delivered, so this must not retry, but an operator needs to
			// know the circuit breaker is not seeing real outcomes.
			worker.RelayBookkeepingFailed = func(ctx context.Context, providerID uint, success bool, err error) {
				if m.logger != nil {
					m.logger.Warn("relay attempt bookkeeping failed",
						zap.Uint("provider_id", providerID),
						zap.Bool("delivery_succeeded", success),
						zap.Error(err))
				}
			}
		}
		if deliverabilityAdapter != nil {
			worker.SuppressionChecker = deliverabilityAdapter
			worker.DeliverabilityRecorder = deliverabilityAdapter
			if worker.TenantIDForRelay == nil {
				worker.TenantIDForRelay = tenantForRelay
			}
		}
		m.workers = append(m.workers, worker)
	}

	// Wire Web Push (RFC 8030) notifier. The notifier is built
	// even when VAPID keys are missing — IsEnabled() simply
	// returns false in that case so NotifyMailboxMessage is a
	// no-op. The /api/v1/webmail/push/status endpoint reads
	// IsEnabled() to decide whether to expose the VAPID public
	// key + active subscription list, or to return a
	// "disabled" status. Either way, the worker never crashes
	// on a missing subscription row.
	//
	// The repository is wired against the same *sql.DB the rest
	// of the runtime uses. The push_subscriptions table is
	// created by storage.Migrate().
	vapid := push.VAPIDConfig{
		PublicKey:  cfg.CoreMail.VAPIDPublicKey,
		PrivateKey: cfg.CoreMail.VAPIDPrivateKey,
		Subject:    cfg.CoreMail.VAPIDSubject,
	}
	repo := push.NewSubscriptionSQLRepo(sqlDB)
	m.pushNotifier = push.NewPushNotifier(m.store, repo, vapid)
	for _, worker := range m.workers {
		worker.PushNotifier = m.pushNotifier
	}
	if m.logger != nil {
		if m.pushNotifier.IsEnabled() {
			m.logger.Info("web push notifier enabled",
				zap.String("vapid_subject", vapid.Subject),
				zap.Int("worker_count", workerCount),
			)
		} else {
			m.logger.Info("web push notifier disabled (VAPID keys not configured)")
		}
	}

	return nil
}

func (m *Module) Migrate() error {
	if m.db == nil {
		return nil
	}
	dialect, err := dbdialect.Detect(m.db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	for _, stmt := range append(storage.Tables(), storage.Indexes()...) {
		if _, err := m.db.Exec(stmt); err != nil {
			return fmt.Errorf("coremail storage migration: %w", err)
		}
	}
	for _, stmt := range append(queue.Tables(), queue.Indexes()...) {
		if _, err := m.db.Exec(stmt); err != nil {
			return fmt.Errorf("coremail queue migration: %w", err)
		}
	}
	for _, stmt := range append(policy.Tables(dialect), policy.Indexes()...) {
		if _, err := m.db.Exec(stmt); err != nil {
			return fmt.Errorf("coremail policy migration: %w", err)
		}
	}
	for _, stmt := range trust.TablesForDialect(dialect) {
		if _, err := m.db.Exec(stmt); err != nil {
			return fmt.Errorf("coremail trust migration: %w", err)
		}
	}
	if err := audit.NewStore(m.db).EnsureTable(context.Background()); err != nil {
		return fmt.Errorf("coremail audit migration: %w", err)
	}
	return nil
}

func (m *Module) Start() error {
	m.ctx, m.cancel = context.WithCancel(context.Background())
	if m.cfg == nil || !m.cfg.CoreMail.Enabled {
		// Record all listeners as disabled so the admin
		// dashboard shows "disabled" instead of "unknown".
		if m.listenerReg != nil {
			m.listenerReg.MarkDisabled(orvixruntime.ListenerSMTP, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerSubmission, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerSMTPS, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerIMAP, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerIMAPS, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerPOP3, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerPOP3S, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerJMAP, 0, "disabled by config")
		}
		return nil
	}
	// Note on listener-goroutine tracking (BLOCKER-2 fix):
	//
	// The listener goroutines launched by startServer are NOT
	// registered with m.wg. On Windows, net.Listener.Close()
	// does not always unblock a goroutine stuck in Accept()
	// immediately — the system can take seconds to wake the
	// goroutine even after the listener's socket handle is
	// closed. Waiting for those goroutines in Stop() would
	// therefore block every test cleanup for several seconds
	// when the suite is run repeatedly with -count > 5.
	//
	// The listener goroutines are still bounded: each one
	// runs ListenAndServe, which returns when the per-server
	// Stop() closes that server's listener. For the BLOCKER-2
	// review this means Module.Stop() returns as soon as the
	// workers have stopped (or hit the bounded wait) — the
	// listener goroutines become orphans that exit on their
	// own at the next OS scheduling point. The downside
	// (FD usage while orphaned) is acceptable for tests
	// because each test allocates fresh ports via freePort().
	m.startServer(orvixruntime.ListenerSMTP, net.JoinHostPort(m.cfg.CoreMail.SMTPHost, fmt.Sprintf("%d", m.cfg.CoreMail.SMTPPort)), m.smtpServer.ListenAndServe)
	if m.submissionServer != nil {
		m.startServer(orvixruntime.ListenerSubmission, net.JoinHostPort(m.cfg.CoreMail.SubmissionHost, fmt.Sprintf("%d", m.cfg.CoreMail.SubmissionPort)), m.submissionServer.ListenAndServe)
	}
	m.startServer(orvixruntime.ListenerIMAP, net.JoinHostPort(m.cfg.CoreMail.IMAPHost, fmt.Sprintf("%d", m.cfg.CoreMail.IMAPPort)), m.imapServer.ListenAndServe)
	m.startServer(orvixruntime.ListenerPOP3, net.JoinHostPort(m.cfg.CoreMail.POP3Host, fmt.Sprintf("%d", m.cfg.CoreMail.POP3Port)), m.pop3Server.ListenAndServe)
	m.startServer(orvixruntime.ListenerJMAP, net.JoinHostPort(m.cfg.CoreMail.JMAPHost, fmt.Sprintf("%d", m.cfg.CoreMail.JMAPPort)), m.jmapServer.ListenAndServe)
	if m.imapsServer != nil {
		m.startServer(orvixruntime.ListenerIMAPS, net.JoinHostPort(m.cfg.CoreMail.IMAPsHost, fmt.Sprintf("%d", m.cfg.CoreMail.IMAPsPort)), m.imapsServer.ListenAndServe)
	}
	if m.pop3sServer != nil {
		m.startServer(orvixruntime.ListenerPOP3S, net.JoinHostPort(m.cfg.CoreMail.POP3sHost, fmt.Sprintf("%d", m.cfg.CoreMail.POP3sPort)), m.pop3sServer.ListenAndServe)
	}
	// Telemetry: mark listeners that are config-disabled or not-yet-implemented.
	if m.listenerReg != nil {
		if m.submissionServer == nil && m.cfg.CoreMail.SubmissionEnabled {
			reason := m.submissionDisabledReason()
			m.listenerReg.MarkDisabled(orvixruntime.ListenerSubmission, m.cfg.CoreMail.SubmissionPort, reason)
		}
		if !m.cfg.CoreMail.SMTPsEnabled {
			m.listenerReg.MarkDisabled(orvixruntime.ListenerSMTPS, m.cfg.CoreMail.SMTPsPort, "SMTPS disabled by config")
		} else if m.smtpsServer == nil {
			m.listenerReg.MarkDisabled(orvixruntime.ListenerSMTPS, m.cfg.CoreMail.SMTPsPort, "SMTPS not yet implemented")
		}
		if !m.cfg.CoreMail.IMAPsEnabled {
			m.listenerReg.MarkDisabled(orvixruntime.ListenerIMAPS, m.cfg.CoreMail.IMAPsPort, "IMAPS disabled by config")
		} else if m.imapsServer == nil {
			reason := m.imapsDisabledReason()
			m.listenerReg.MarkDisabled(orvixruntime.ListenerIMAPS, m.cfg.CoreMail.IMAPsPort, reason)
		}
		if !m.cfg.CoreMail.POP3sEnabled {
			m.listenerReg.MarkDisabled(orvixruntime.ListenerPOP3S, m.cfg.CoreMail.POP3sPort, "POP3S disabled by config")
		} else if m.pop3sServer == nil {
			reason := m.pop3sDisabledReason()
			m.listenerReg.MarkDisabled(orvixruntime.ListenerPOP3S, m.cfg.CoreMail.POP3sPort, reason)
		}
	}
	for _, worker := range m.workers {
		w := worker
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			interval := m.cfg.CoreMail.WorkerInterval
			if interval <= 0 {
				interval = 5 * time.Second
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-m.ctx.Done():
					return
				default:
					if _, err := w.ProcessAll(m.ctx); err != nil {
						m.recordQueueWorkerError(w.WorkerID, err)
					}
				}
				select {
				case <-m.ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	m.logger.Info("coremail runtime started")
	return nil
}

func (m *Module) recordQueueWorkerError(workerID string, err error) {
	if err == nil {
		return
	}
	if m.logger != nil {
		m.logger.Error("coremail queue worker process failed", zap.String("worker", workerID), zap.Error(err))
	}
	if m.obs != nil {
		m.obs.Metrics.IncQueueDeferred()
		m.obs.EventHistory.Record(observability.EventQueueDeferred, map[string]string{
			"worker": workerID,
			"error":  err.Error(),
		})
		m.obs.Health.NotReady(observability.HealthCheckQueue, err.Error())
	}
}

func (m *Module) startServer(kind orvixruntime.ListenerKind, addr string, fn func(string) error) {
	// Extract port for the registry.
	_, portStr, _ := net.SplitHostPort(addr)
	port := 0
	if portStr != "" {
		fmt.Sscanf(portStr, "%d", &port)
	}

	// Record that this listener is starting so the admin surface does
	// not show "unknown" during the brief window before the bind
	// callback fires. The callback below overwrites this with the
	// actual bind result (active on success, failed on error).
	if m.listenerReg != nil {
		m.listenerReg.MarkStarting(kind, port)
	}

	// Register the listener callback so the server notifies us
	// after its real listener is created (preserving TLS paths).
	cb := func(addr2 string, err error) {
		if m.listenerReg == nil {
			return
		}
		if err != nil {
			m.listenerReg.MarkFailed(kind, port, err)
		} else {
			m.listenerReg.MarkOK(kind, port)
		}
	}
	switch kind {
	case orvixruntime.ListenerSMTP:
		m.smtpServer.SetListenerCallback(cb)
	case orvixruntime.ListenerSubmission:
		if m.submissionServer != nil {
			m.submissionServer.SetListenerCallback(cb)
		}
	case orvixruntime.ListenerSMTPS:
		if m.smtpsServer != nil {
			m.smtpsServer.SetListenerCallback(cb)
		}
	case orvixruntime.ListenerIMAP:
		m.imapServer.SetListenerCallback(cb)
	case orvixruntime.ListenerIMAPS:
		if m.imapsServer != nil {
			m.imapsServer.SetListenerCallback(cb)
		}
	case orvixruntime.ListenerPOP3:
		m.pop3Server.SetListenerCallback(cb)
	case orvixruntime.ListenerPOP3S:
		if m.pop3sServer != nil {
			m.pop3sServer.SetListenerCallback(cb)
		}
	case orvixruntime.ListenerJMAP:
		m.jmapServer.SetListenerCallback(cb)
	}

	// Listener goroutine is intentionally NOT registered with
	// m.wg. See the BLOCKER-2 note in Start(). Each per-server
	// Stop() closes its own listener, which causes that server's
	// ListenAndServe to return; Module.Stop() does not wait on
	// these goroutines because Windows keeps them parked in
	// Accept() for several seconds even after close, and the
	// cumulative delay across a -count=N run was hanging the
	// Go test runner. The goroutines are orphaned but harmless
	// (each one already holds a port the harness used freePort
	// to obtain, and the orphaned accept() does not block new
	// ports from binding).
	go func() {
		m.logger.Info("starting coremail "+string(kind), zap.String("addr", addr))
		if err := fn(addr); err != nil && m.ctx.Err() == nil {
			m.logger.Error("coremail "+string(kind)+" stopped", zap.Error(err))
			if m.obs != nil {
				m.obs.Health.NotReady(string(kind), err.Error())
			}
		}
	}()
}

func (m *Module) GetLicensingService() *licensing.Service {
	return m.licenseSvc
}

// ListenerRegistry returns the shared listener state registry
// used by the admin runtime telemetry endpoint. Returns nil when
// SetListenerRegistry was not called (tests, legacy builds).
func (m *Module) ListenerRegistry() *orvixruntime.ListenerRegistry {
	return m.listenerReg
}

func (m *Module) GetAuthorityService() *licensingauthority.AuthorityService {
	return m.authorityService
}

// MailStore returns the underlying MailStore owned by this
// module. The webmail user-facing endpoints read messages
// and folders directly from this store — they do not need to
// go through SMTP/IMAP/JMAP to render the inbox. Returns
// nil if the module has not been initialized yet (MailStore
// is created in initCore, which runs during InitAll).
func (m *Module) MailStore() *storage.MailStore {
	return m.store
}

// QueueEngine returns the delivery queue owned by this
// module. The user-facing webmail Send endpoint enqueues
// outbound messages through this queue — the same queue
// the SMTP receiver uses for inbound and the delivery
// worker drains for outbound. Returns nil if the module has
// not been initialized (cfg.CoreMail.Enabled == false) or
// the runtime was not booted.
func (m *Module) QueueEngine() *queue.QueueEngine {
	return m.queue
}

// ClusterService returns the Milestone 10 node registry service, or
// nil if cluster schema init failed or the runtime was not booted.
func (m *Module) ClusterService() *cluster.Service {
	return m.clusterSvc
}

// SecurityService returns the Milestone 12 normalized security-event
// service, or nil if schema init failed or the runtime was not
// booted.
func (m *Module) SecurityService() *security.Service {
	return m.securitySvc
}

// HealthStatus implements the admin API's optional moduleHealthReporter
// capability (internal/api/handlers.moduleHealthReporter): it derives
// this module's status from its OWN observability.HealthChecker
// report (populated throughout initCore via m.obs.Health.Ready/
// NotReady/Degraded on real subsystem checks — database, mailstore,
// queue, DNS resolver, DKIM config) rather than a hardcoded "active".
func (m *Module) HealthStatus() (status, message string) {
	if !m.cfg.CoreMail.Enabled {
		return "disabled", "coremail.enabled=false"
	}
	if m.obs == nil {
		return "unknown", "observability not initialized"
	}
	report := m.obs.Health.Report()
	switch report.Overall {
	case observability.HealthReady:
		return "active", ""
	case observability.HealthDegraded:
		return "degraded", degradedSummary(report)
	default:
		return "unavailable", degradedSummary(report)
	}
}

func degradedSummary(report *observability.HealthReport) string {
	for name, check := range report.Checks {
		if check.Status != observability.HealthReady {
			if check.Message != "" {
				return name + ": " + check.Message
			}
			return name + " not ready"
		}
	}
	return ""
}

// AntivirusEngine returns the antivirus engine wired into
// the SMTP receive path, or nil when the runtime has not
// initialized one. The admin handler uses this to read
// the engine's own snapshot for runtime_enforced, last
// error, and per-policy counters.
func (m *Module) AntivirusEngine() *antivirus.Engine {
	return m.avEngine
}

// RuleEngine returns the bundled acceptance + incoming
// rule engine. The admin endpoints use this to surface
// runtime_enforced status without re-reading the rule
// tables.
func (m *Module) RuleEngine() *ruler.Engine {
	return m.rulerEngine
}

// Observability returns the runtime observability pipeline
// so the admin handler can surface per-policy counters.
func (m *Module) Observability() *observability.Observability {
	return m.obs
}

// PushNotifier returns the Web Push (RFC 8030) dispatcher
// constructed from cfg.CoreMail.VAPIDPublicKey /
// VAPIDPrivateKey / VAPIDSubject. The router wires this
// into the user-facing webmail handler so
// /api/v1/webmail/push/* can subscribe / unsubscribe /
// status / test. Returns nil when the module has not been
// initialized. The notifier itself returns IsEnabled()=false
// when VAPID keys are missing, so callers should always
// check IsEnabled() before issuing push requests.
func (m *Module) PushNotifier() *push.PushNotifier {
	return m.pushNotifier
}

// RulesRunner returns the per-recipient rules engine runner
// that the SMTP receiver invokes after a message is durably
// stored in a recipient's mailbox. The router wires this
// into the user-facing webmail handlers (rules / vacation /
// forwarding API) so the same MailStore + QueueEngine the
// SMTP receiver uses is reachable from the API path. Returns
// nil when the runtime was not initialized
// (cfg.CoreMail.Enabled == false) or the receiver has not
// been built yet.
func (m *Module) RulesRunner() *rules.Runner {
	if m.smtpServer == nil {
		return nil
	}
	// Receiver lives inside the SMTP server. We do not have
	// a direct handle on it; the SMTP server does not expose
	// its receiver. The clean way to expose this is via a
	// dedicated accessor on the receiver side; until then we
	// fall back to constructing a fresh runner that shares
	// the runtime's MailStore + QueueEngine. The two runners
	// share no state, so the API runner's rule evaluations
	// never interfere with the SMTP-side runner's per-message
	// evaluation.
	return rules.NewRunner(rules.Dependencies{
		MailStore:        m.store,
		QueueEngine:      m.queue,
		Vacation:         m.store.Vacation,
		Forwarding:       m.store.Forwarding,
		Logger:           m.logger,
		MailAccessPolicy: m.mailPolicy,
	})
}

// smtpMailAccessPolicy adapts the canonical mail-access policy to the
// SMTP session shape used at RCPT TO. It is wired into both the
// inbound (port 25) and submission (port 587) servers.
//
//   - External recipient: the sender-side outbound policy applies. An
//     authenticated mailbox that is internal_only cannot send to an
//     external recipient; unauthenticated external relay is already
//     blocked by the relay-protection check that runs first.
//   - Local recipient: the recipient-side inbound policy applies. An
//     internal_only recipient may receive only from a trusted local
//     authenticated sender — never from a remote path with a forged
//     local MAIL FROM.
//
// A policy-evaluation failure returns a non-nil error so handleRCPT
// fails closed with a temporary failure instead of delivering.
func (m *Module) smtpMailAccessPolicy(ctx context.Context, session *smtp.Session, rcptAddr string, rcptIsLocal bool) (bool, string, error) {
	if m.mailPolicy == nil {
		// No policy wired: preserve pre-policy behavior (the legacy
		// domain checker remains installed on the servers).
		return true, "", nil
	}
	if !rcptIsLocal {
		if session.Authenticated && session.AuthIdentity != nil {
			decision := m.mailPolicy.CheckOutbound(ctx, "smtp_outbound", session.AuthIdentity.Username, []string{rcptAddr})
			switch {
			case decision.Allowed:
				return true, "", nil
			case decision.Unavailable:
				return false, "", errPolicyUnavailable
			case decision.Denied:
				return false, string(decision.Reason), nil
			}
		}
		// Unauthenticated external recipient: the relay-protection
		// check that runs before this hook already denies it.
		return true, "", nil
	}

	// Canonical domain operability guard (Phase 8 C3A), checked for
	// the RECIPIENT's local domain — never MAIL FROM, which a client
	// fully controls and could spoof to try to bypass this. Runs
	// before alias/group expansion, before mailAccessPolicy's
	// mailbox-level check, before DATA is ever accepted: RCPT is
	// evaluated per-recipient before the message body is read at all,
	// so refusing here means no spool write, no queue enqueue, and no
	// delivery record for this recipient ever happens.
	if allow, reason, err := m.checkRecipientDomainOperability(ctx, rcptAddr); err != nil {
		return false, "", err
	} else if !allow {
		return false, reason, nil
	}

	sender := mailpolicy.Sender{Authenticated: session.Authenticated}
	if session.AuthIdentity != nil {
		sender.MailboxEmail = session.AuthIdentity.Username
	}
	decision := m.mailPolicy.CheckInboundRecipient(ctx, "smtp_inbound", sender, rcptAddr)
	switch {
	case decision.Allowed:
		return true, "", nil
	case decision.Unavailable:
		return false, "", errPolicyUnavailable
	case decision.Denied:
		return false, string(decision.Reason), nil
	}
	return true, "", nil
}

// checkRecipientDomainOperability resolves the recipient address's
// local domain by name (global lookup — there is no caller tenant to
// scope an inbound SMTP recipient against; the domain's OWN tenant,
// from the resolved row, is what CheckOperabilityByIDTx checks) and
// applies the canonical C1 guard. An unknown domain preserves the
// existing anti-enumeration/open-relay behavior by allowing (the
// isLocalDomain check upstream of this hook is the actual existence
// gate; this function only refuses an EXISTING domain that is
// disabled/suspended/locked, and never distinguishes "doesn't exist"
// from "exists but not owned here" in its own right). A repository
// failure returns errPolicyUnavailable, which handleRCPT maps to a
// temporary (4.x) SMTP failure — never a permanent rejection and
// never silent acceptance.
func (m *Module) checkRecipientDomainOperability(ctx context.Context, rcptAddr string) (allow bool, reason string, err error) {
	if m.db == nil {
		return true, "", nil
	}
	at := strings.LastIndex(rcptAddr, "@")
	if at < 0 || at == len(rcptAddr)-1 {
		return true, "", nil
	}
	domainName := strings.ToLower(rcptAddr[at+1:])

	var domainID, tenantID uint
	var status string
	var deletedAt sql.NullTime
	qerr := m.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, status, deleted_at FROM coremail_domains WHERE name = ? AND deleted_at IS NULL`,
		domainName).Scan(&domainID, &tenantID, &status, &deletedAt)
	if qerr == sql.ErrNoRows {
		// No row -- either genuinely unknown or already soft-deleted;
		// preserve existing anti-enumeration behavior (allow; the
		// isLocalDomain gate upstream is the real existence check).
		return true, "", nil
	}
	if qerr != nil {
		return false, "", errPolicyUnavailable
	}

	if err := domainpkg.StatusError(domainpkg.DomainStatus(status)); err != nil {
		return false, "recipient domain unavailable", nil
	}
	return true, "", nil
}

// errPolicyUnavailable is returned by the SMTP policy adapter when the
// canonical policy cannot be evaluated; handleRCPT maps it to a
// temporary failure (fail closed before delivery).
var errPolicyUnavailable = errors.New("mail access policy unavailable")

// runtimePolicySink emits safe observable mail-policy events through
// the runtime logger. It never receives message bodies, passwords,
// tokens, or hashes.
type runtimePolicySink struct {
	logger *zap.Logger
}

func (s runtimePolicySink) PolicyDenied(_ context.Context, kind, sender, recipient string, reason mailpolicy.DeniedReason, detail string) {
	if s.logger != nil {
		s.logger.Warn("mail access policy denied",
			zap.String("path", kind),
			zap.String("sender", sender),
			zap.String("recipient", recipient),
			zap.String("reason", string(reason)),
			zap.String("detail", detail))
	}
}

func (s runtimePolicySink) PolicyCorrupt(_ context.Context, kind, detail string) {
	if s.logger != nil {
		s.logger.Error("mail access policy failed closed on corrupt data",
			zap.String("path", kind),
			zap.String("detail", detail))
	}
}

func (s runtimePolicySink) PolicyUnavailable(_ context.Context, kind, detail string) {
	if s.logger != nil {
		s.logger.Error("mail access policy unavailable; failing closed",
			zap.String("path", kind),
			zap.String("detail", detail))
	}
}

// moduleStopTimeout caps how long Module.Stop() is allowed to
// block on m.wg.Wait(). Without a cap, a stuck listener goroutine
// (e.g. the JMAP accept loop blocking past http.Server.Close on
// Windows) would cause every t.Cleanup(mod.Stop()) in the test
// harness to hang for the Go test runner's full timeout — that's
// exactly the BLOCKER-2 flake we just fixed. The cap is short
// (3s) so a SINGLE test cleanup never dominates the suite runtime;
// only genuinely stuck goroutines trip it. Healthy listeners
// drain via their own per-server Stop() in well under 1s.
const moduleStopTimeout = 3 * time.Second

// SetSendEnforcerCallback wires shared send enforcement into the SMTP
// submission pipeline. Called by the router after billing services are
// initialized. preFn checks AllowSend before DATA (returns error on rejection);
// postFn calls RecordSend after successful acceptance (no error, best effort).
// SetSendEnforcerCallback wires shared send enforcement into the SMTP
// submission pipeline. Called by the router after billing services are
// initialized. preFn checks AllowSend before DATA (returns error on rejection);
// postFn calls RecordSend after successful acceptance (no error, best effort).
func (m *Module) SetSendEnforcerCallback(preFn func(ctx context.Context, tenantID, mailboxID uint, count int) error, postFn func(ctx context.Context, tenantID, mailboxID uint, count int, eventID string), bounceFn func(ctx context.Context, tenantID, mailboxID uint, eventID string)) {
	if m.submissionHandler != nil {
		m.submissionHandler.SetSendEnforcer(preFn)
	}
	if m.submissionServer != nil {
		m.submissionServer.PostAcceptFn = postFn
	}
	for _, w := range m.workers {
		w.OnBounceFn = bounceFn
	}
}

func (m *Module) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}
	if m.smtpServer != nil {
		_ = m.smtpServer.Stop()
	}
	if m.submissionServer != nil {
		_ = m.submissionServer.Stop()
	}
	if m.smtpsServer != nil {
		_ = m.smtpsServer.Stop()
	}
	if m.imapServer != nil {
		m.imapServer.Stop()
	}
	if m.imapsServer != nil {
		m.imapsServer.Stop()
	}
	if m.pop3Server != nil {
		m.pop3Server.Stop()
	}
	if m.pop3sServer != nil {
		m.pop3sServer.Stop()
	}
	if m.jmapServer != nil {
		m.jmapServer.Stop()
	}
	// Wait for the listener / worker goroutines to exit, but do
	// not hang forever. If m.wg does not drain in
	// moduleStopTimeout we log a diagnostic and return so the
	// test harness's t.Cleanup(mod.Stop) never wedges the Go
	// test runner. The orphaned goroutines (which are now likely
	// blocked in net.Accept on Windows) are still tracked by m.wg
	// in case they ever do exit; the runtime test passes
	// deterministically either way.
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// all goroutines exited cleanly
	case <-time.After(moduleStopTimeout):
		if m.logger != nil {
			m.logger.Warn("coremail runtime Stop timed out waiting for goroutines; returning anyway to avoid test hangs",
				zap.Duration("deadline", moduleStopTimeout),
			)
		}
		// Force the module's worker goroutine to exit even if
		// its current ProcessAll call is wedged. The worker
		// loop respects m.ctx; an additional cancel here is
		// idempotent — the channel has already been closed —
		// but we still try, in case the goroutine leaked past
		// the first cancel.
		if m.cancel != nil {
			m.cancel()
		}
	}
	return nil
}

type mailboxAuth struct {
	auth *coremail.AuthService
}

func (a *mailboxAuth) Authenticate(username, password string) (uint, bool) {
	if a == nil || a.auth == nil {
		return 0, false
	}
	mbox, err := a.auth.AuthenticateMailbox(context.Background(), username, password)
	if err != nil || mbox == nil {
		return 0, false
	}
	return mbox.ID, true
}

type imapMailboxAuth struct{ auth *coremail.AuthService }
type pop3MailboxAuth struct{ auth *coremail.AuthService }

func (a *imapMailboxAuth) Authenticate(username, password string) (uint, bool) {
	if a == nil || a.auth == nil {
		return 0, false
	}
	mbox, err := a.auth.AuthenticateMailbox(context.Background(), username, password)
	if err != nil || mbox == nil {
		return 0, false
	}
	if !mbox.AllowIMAP {
		return 0, false
	}
	return mbox.ID, true
}

func (a *pop3MailboxAuth) Authenticate(username, password string) (uint, bool) {
	if a == nil || a.auth == nil {
		return 0, false
	}
	mbox, err := a.auth.AuthenticateMailbox(context.Background(), username, password)
	if err != nil || mbox == nil {
		return 0, false
	}
	if !mbox.AllowPOP3 {
		return 0, false
	}
	return mbox.ID, true
}

// submissionDisabledReason returns the specific reason why the
// submission listener was not started, in a format safe to surface
// in the admin dashboard. Order matters: the most actionable
// reason is preferred. The error path itself is never echoed raw
// — only a short stable summary, so the dashboard does not leak
// file paths or PEM contents.
func (m *Module) submissionDisabledReason() string {
	if m.cfg == nil || !m.cfg.CoreMail.SubmissionEnabled {
		return "submission disabled by config"
	}
	if m.cfg.CoreMail.TLSCertFile == "" || m.cfg.CoreMail.TLSKeyFile == "" {
		return "submission disabled: TLS certificate/key not configured"
	}
	if m.tlsLoadErr != nil {
		return "submission disabled: TLS certificate/key failed to load (" + safeTLSLoadError(m.tlsLoadErr) + ")"
	}
	return "submission disabled: not initialized"
}

// imapsDisabledReason returns the specific reason why the
// IMAPS listener was not started.
func (m *Module) imapsDisabledReason() string {
	if m.cfg == nil || !m.cfg.CoreMail.IMAPsEnabled {
		return "IMAPS disabled by config"
	}
	if m.cfg.CoreMail.TLSCertFile == "" || m.cfg.CoreMail.TLSKeyFile == "" {
		return "IMAPS disabled: TLS certificate/key not configured"
	}
	if m.tlsLoadErr != nil {
		return "IMAPS disabled: TLS certificate/key failed to load (" + safeTLSLoadError(m.tlsLoadErr) + ")"
	}
	return "IMAPS disabled: not initialized"
}

// pop3sDisabledReason returns the specific reason why the
// POP3S listener was not started.
func (m *Module) pop3sDisabledReason() string {
	if m.cfg == nil || !m.cfg.CoreMail.POP3sEnabled {
		return "POP3S disabled by config"
	}
	if m.cfg.CoreMail.TLSCertFile == "" || m.cfg.CoreMail.TLSKeyFile == "" {
		return "POP3S disabled: TLS certificate/key not configured"
	}
	if m.tlsLoadErr != nil {
		return "POP3S disabled: TLS certificate/key failed to load (" + safeTLSLoadError(m.tlsLoadErr) + ")"
	}
	return "POP3S disabled: not initialized"
}

// safeTLSLoadError converts a tls.LoadX509KeyPair error into a
// short, safe summary. The original error from the Go stdlib can
// contain the file path; we strip that to keep secrets out of the
// admin runtime telemetry endpoint.
func safeTLSLoadError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "no such file"):
		return "file not found"
	case strings.Contains(s, "permission denied"):
		return "permission denied"
	case strings.Contains(s, "tls: failed to find any PEM data"):
		return "missing or malformed PEM"
	case strings.Contains(s, "tls: failed to parse"):
		return "malformed certificate or key"
	case strings.Contains(s, "private key does not match"):
		return "cert/key mismatch"
	default:
		return "load failed"
	}
}
