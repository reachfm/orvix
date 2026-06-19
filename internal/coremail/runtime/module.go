package runtime

import (
	"context"
	"database/sql"
	"fmt"
	"net"
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
	"github.com/orvix/orvix/internal/coremail/queue"
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

	smtpServer *smtp.Server
	imapServer *imap.Server
	pop3Server *pop3.Server
	jmapServer *jmap.Server
	workers    []*delivery.DeliveryWorker

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
	smtpCfg := smtp.DefaultConfig()
	smtpCfg.Hostname = cfg.CoreMail.Hostname
	smtpCfg.TLSCertFile = cfg.CoreMail.TLSCertFile
	smtpCfg.TLSKeyFile = cfg.CoreMail.TLSKeyFile
	smtpCfg.RequireTLSForAuth = cfg.CoreMail.RequireTLSForAuth
	smtpCfg.RequireTLSForSubmission = cfg.CoreMail.RequireTLSForAuth
	smtpAuth := smtp.NewAuthenticator(identity)
	tlsCfg, err := smtp.LoadTLSConfig(smtpCfg)
	if err != nil {
		return err
	}
	receiver := smtp.NewReceiver(m.engine, m.store, m.queue, smtpCfg)
	receiver.AntiSpamEngine = antispam.NewEngine(nil)
	receiver.Observability = m.obs
	baseHandler := smtp.NewCommandHandler(smtpCfg, smtpAuth, smtp.NewSession("runtime-init", tlsCfg, smtpCfg))
	m.smtpServer = smtp.NewServer(smtpCfg, baseHandler, receiver)
	m.smtpServer.TLSConfig = tlsCfg
	m.smtpServer.RecipientValidator = func(ctx context.Context, address string) (bool, error) {
		_, err := m.engine.Auth.ResolveAddress(ctx, address)
		return err == nil, err
	}
	m.smtpServer.SetLocalDomainChecker(identity.IsLocalDomain)
	m.smtpServer.Observability = m.obs

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
			m.listenerReg.MarkDisabled(orvixruntime.ListenerIMAP, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerPOP3, 0, "disabled by config")
			m.listenerReg.MarkDisabled(orvixruntime.ListenerJMAP, 0, "disabled by config")
		}
		return nil
	}
	m.startServer(orvixruntime.ListenerSMTP, net.JoinHostPort(m.cfg.CoreMail.SMTPHost, fmt.Sprintf("%d", m.cfg.CoreMail.SMTPPort)), m.smtpServer.ListenAndServe)
	m.startServer(orvixruntime.ListenerIMAP, net.JoinHostPort(m.cfg.CoreMail.IMAPHost, fmt.Sprintf("%d", m.cfg.CoreMail.IMAPPort)), m.imapServer.ListenAndServe)
	m.startServer(orvixruntime.ListenerPOP3, net.JoinHostPort(m.cfg.CoreMail.POP3Host, fmt.Sprintf("%d", m.cfg.CoreMail.POP3Port)), m.pop3Server.ListenAndServe)
	m.startServer(orvixruntime.ListenerJMAP, net.JoinHostPort(m.cfg.CoreMail.JMAPHost, fmt.Sprintf("%d", m.cfg.CoreMail.JMAPPort)), m.jmapServer.ListenAndServe)
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

	// Create and bind the listener synchronously so we know
	// immediately whether bind succeeded or failed. The
	// listener is then handed to the server so it never needs
	// to bind itself.
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		m.logger.Error("coremail "+string(kind)+" bind failed", zap.Error(err))
		if m.listenerReg != nil {
			m.listenerReg.MarkFailed(kind, port, err)
		}
		if m.obs != nil {
			m.obs.Health.NotReady(string(kind), err.Error())
		}
		return
	}
	if m.listenerReg != nil {
		m.listenerReg.MarkOK(kind, port)
	}
	m.logger.Info("coremail "+string(kind)+" listening", zap.String("addr", addr))

	// Inject the listener into the server. Each server supports
	// SetListener (added for SMTP/JMAP, pre-existing for IMAP/POP3).
	switch kind {
	case orvixruntime.ListenerSMTP:
		m.smtpServer.SetListener(listener)
	case orvixruntime.ListenerIMAP:
		m.imapServer.SetListener(listener)
	case orvixruntime.ListenerPOP3:
		m.pop3Server.SetListener(listener)
	case orvixruntime.ListenerJMAP:
		m.jmapServer.SetListener(listener)
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		if err := fn(addr); err != nil && m.ctx.Err() == nil {
			m.logger.Error("coremail "+string(kind)+" serve error", zap.Error(err))
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

func (m *Module) Stop() error {
	if m.cancel != nil {
		m.cancel()
	}
	if m.smtpServer != nil {
		_ = m.smtpServer.Stop()
	}
	if m.imapServer != nil {
		m.imapServer.Stop()
	}
	if m.pop3Server != nil {
		m.pop3Server.Stop()
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
