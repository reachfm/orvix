package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/coremail/antispam"
	"github.com/orvix/orvix/internal/coremail/delivery"
	"github.com/orvix/orvix/internal/coremail/imap"
	"github.com/orvix/orvix/internal/coremail/jmap"
	"github.com/orvix/orvix/internal/coremail/pop3"
	"github.com/orvix/orvix/internal/coremail/push"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/rules"
	"github.com/orvix/orvix/internal/coremail/smtp"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/licensing"
	"github.com/orvix/orvix/internal/licensingauthority"
	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/policy"
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

	engine           *coremail.Engine
	store            *storage.MailStore
	queue            *queue.QueueEngine
	obs              *observability.Observability
	policyEngine     *policy.Engine
	trustEngine      *trust.Engine
	auditStore       *audit.Store
	licenseSvc       *licensing.Service
	authorityService *licensingauthority.AuthorityService

	smtpServer      *smtp.Server
	submissionServer *smtp.Server
	smtpsServer     *smtp.Server
	imapServer      *imap.Server
	imapsServer     *imap.Server
	pop3Server      *pop3.Server
	pop3sServer     *pop3.Server
	jmapServer      *jmap.Server
	workers    []*delivery.DeliveryWorker

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

	var err error
	m.store, err = storage.NewMailStore(sqlDB, cfg.CoreMail.MailStorePath)
	if err != nil {
		return fmt.Errorf("coremail mailstore: %w", err)
	}
	m.queue = queue.NewQueueEngine(sqlDB)
	m.obs = observability.NewObservability(1000, 5000)

	// Initialize licensing.
	licensePath := cfg.CoreMail.LicenseFilePath
	m.licenseSvc = licensing.NewService(licensePath)
	if licensePath != "" {
		m.licenseSvc.LoadLicense(context.Background())
		if m.licenseSvc.IsValid() {
			m.obs.Metrics.IncLicenseValid()
		} else {
			m.obs.Metrics.IncLicenseInvalid()
		}
	}

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
	// Licensing health depends on license status.
	if m.licenseSvc != nil && m.licenseSvc.IsValid() {
		m.obs.Health.Ready("licensing")
	} else {
		m.obs.Health.Ready("licensing")
	}
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
		MailStore:   m.store,
		QueueEngine: m.queue,
		Vacation:    m.store.Vacation,
		Forwarding:  m.store.Forwarding,
		Logger:      m.logger,
	})

	// ── Inbound SMTP (port 25, MX) ─────────────────────────
	inboundCfg := smtp.InboundConfig()
	inboundCfg.Hostname = cfg.CoreMail.Hostname
	inboundCfg.TLSCertFile = cfg.CoreMail.TLSCertFile
	inboundCfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
	inboundCfg.SpamMode = smtpCfg.SpamMode
	inboundHandler := smtp.NewCommandHandler(inboundCfg, smtpAuth, smtp.NewSession("runtime-init", tlsCfg, inboundCfg))
	m.smtpServer = smtp.NewServer(inboundCfg, inboundHandler, receiver)
	m.smtpServer.TLSConfig = tlsCfg
	m.smtpServer.RecipientValidator = func(ctx context.Context, address string) (bool, error) {
		_, err := m.engine.Auth.ResolveAddress(ctx, address)
		return err == nil, err
	}
	m.smtpServer.SetLocalDomainChecker(identity.IsLocalDomain)
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
			m.submissionServer = smtp.NewServer(subCfg, subHandler, receiver)
			m.submissionServer.TLSConfig = tlsCfg
			m.submissionServer.SetLocalDomainChecker(identity.IsLocalDomain)
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
	m.imapServer = imap.NewServer(imapCfg, m.store, &mailboxAuth{auth: m.engine.Auth})
	m.imapServer.Observability = m.obs

	pop3Cfg := pop3.DefaultConfig()
	pop3Cfg.Hostname = cfg.CoreMail.Hostname
	pop3Cfg.TLSCertFile = cfg.CoreMail.TLSCertFile
	pop3Cfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
	pop3Cfg.RequireTLSForAuth = cfg.CoreMail.RequireTLSForAuth
	m.pop3Server = pop3.NewServer(pop3Cfg, m.store, pop3.NewAuthenticator(&mailboxAuth{auth: m.engine.Auth}))
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
			m.imapsServer = imap.NewServer(imapsCfg, m.store, &mailboxAuth{auth: m.engine.Auth})
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
			m.pop3sServer = pop3.NewServer(pop3sCfg, m.store, pop3.NewAuthenticator(&mailboxAuth{auth: m.engine.Auth}))
			m.pop3sServer.Observability = m.obs
		}
	}

	// JMAP
	m.jmapServer = jmap.NewServer(m.engine)
	m.jmapServer.Hostname = cfg.CoreMail.Hostname
	m.jmapServer.Observability = m.obs
	m.obs.Health.Ready("jmap")

	workerCount := cfg.CoreMail.QueueWorkers
	if workerCount < 1 {
		workerCount = 1
	}
	m.workers = make([]*delivery.DeliveryWorker, 0, workerCount)
	for i := 0; i < workerCount; i++ {
		worker := delivery.NewDeliveryWorker(
			m.queue,
			m.store,
			delivery.NewDNSResolver(),
			delivery.NewSMTPTransport(delivery.DefaultTransportConfig()),
			cfg.CoreMail.Hostname,
			fmt.Sprintf("coremail-worker-%d", i+1),
		)
		worker.Observability = m.obs
		worker.PreferIPv4 = cfg.Outbound.PreferIPv4
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
	for _, stmt := range append(policy.Tables(), policy.Indexes()...) {
		if _, err := m.db.Exec(stmt); err != nil {
			return fmt.Errorf("coremail policy migration: %w", err)
		}
	}
	for _, stmt := range trust.Tables() {
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

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
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
		MailStore:   m.store,
		QueueEngine: m.queue,
		Vacation:    m.store.Vacation,
		Forwarding:  m.store.Forwarding,
		Logger:      m.logger,
	})
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
	m.wg.Wait()
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
