package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/backup"
	"github.com/orvix/orvix/internal/compliance"
	"github.com/orvix/orvix/internal/coremail"
	"github.com/orvix/orvix/internal/dnsverify"
	"github.com/orvix/orvix/internal/domainregistry"
	"github.com/orvix/orvix/internal/licensing"
	"github.com/orvix/orvix/internal/licensingauthority"
	"github.com/orvix/orvix/internal/lifecycle"
	"github.com/orvix/orvix/internal/mailboxmgmt"
	"github.com/orvix/orvix/internal/messagetrace"
	"github.com/orvix/orvix/internal/migration"
	"github.com/orvix/orvix/internal/monitoring"
	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/policymgmt"
	"github.com/orvix/orvix/internal/queuemgmt"
	"github.com/orvix/orvix/internal/runtimecontrol"
	"github.com/orvix/orvix/internal/tlsmgmt"
	"github.com/orvix/orvix/internal/trustmgmt"
)

type Server struct {
	Engine            *coremail.Engine
	Observability     *observability.Observability
	Sessions          *SessionStore
	RuntimeControl    *runtimecontrol.RuntimeControl
	DomainRegistry    *domainregistry.Service
	MailboxService    *mailboxmgmt.Service
	QueueService      *queuemgmt.Service
	DNSVerify         *dnsverify.Service
	MessageTrace      *messagetrace.Service
	TrustMgmt         *trustmgmt.Service
	PolicyMgmt        *policymgmt.Service
	BackupService     *backup.Service
	TLSMgmt           *tlsmgmt.Service
	ComplianceService *compliance.Service
	MonitoringService *monitoring.Service
	LifecycleService  *lifecycle.Service
	MigrationService   *migration.Service
	LicensingService   *licensing.Service
	AuthorityService   *licensingauthority.AuthorityService
	AuditStore         *audit.Store
	AllowedOrigins    []string
	mux               *http.ServeMux
	srv               *http.Server
}

func NewServer(engine *coremail.Engine) *Server {
	s := &Server{
		Engine:   engine,
		Sessions: NewSessionStore(),
		mux:      http.NewServeMux(),
	}
	if engine != nil && engine.DB != nil {
		store := audit.NewStore(engine.DB)
		_ = store.EnsureTable(context.Background())
		s.AuditStore = store
	}
	s.registerRoutes()
	return s
}

func (s *Server) SetObservability(obs *observability.Observability) {
	s.Observability = obs
}

func (s *Server) SetRuntimeControl(rc *runtimecontrol.RuntimeControl) {
	s.RuntimeControl = rc
}

func (s *Server) SetDomainRegistry(dr *domainregistry.Service) {
	s.DomainRegistry = dr
}

func (s *Server) SetMailboxService(ms *mailboxmgmt.Service) {
	s.MailboxService = ms
}

func (s *Server) SetQueueService(qs *queuemgmt.Service) {
	s.QueueService = qs
}

func (s *Server) SetDNSVerify(dv *dnsverify.Service) {
	s.DNSVerify = dv
}

func (s *Server) SetMessageTrace(mt *messagetrace.Service) {
	s.MessageTrace = mt
}

func (s *Server) SetAuthorityService(as *licensingauthority.AuthorityService) {
	s.AuthorityService = as
}

func (s *Server) SetLicensingService(ls *licensing.Service) {
	s.LicensingService = ls

	// Wire enforcement into domain registry and mailbox management.
	if ls != nil {
		enf := licensing.NewEnforcementService(ls,
			func(ctx context.Context) (int64, error) {
				if s.DomainRegistry == nil { return 0, nil }
				domains, err := s.DomainRegistry.ListDomains(ctx)
				if err != nil { return 0, err }
				return int64(len(domains)), nil
			},
			func(ctx context.Context) (int64, error) {
				if s.MailboxService == nil { return 0, nil }
				mbs, err := s.MailboxService.ListMailboxes(ctx, nil)
				if err != nil { return 0, err }
				return int64(len(mbs)), nil
			},
		)
		if s.DomainRegistry != nil {
			s.DomainRegistry.SetLimitChecker(enf)
		}
		if s.MailboxService != nil {
			s.MailboxService.SetLimitChecker(enf)
		}
	}
}

func (s *Server) SetMigrationService(ms *migration.Service) {
	s.MigrationService = ms
}

func (s *Server) SetLifecycleService(ls *lifecycle.Service) {
	s.LifecycleService = ls
}

func (s *Server) SetMonitoringService(ms *monitoring.Service) {
	s.MonitoringService = ms
}

func (s *Server) SetComplianceService(cs *compliance.Service) {
	s.ComplianceService = cs
}

func (s *Server) SetTLSMgmt(tm *tlsmgmt.Service) {
	s.TLSMgmt = tm
}

func (s *Server) SetBackupService(bs *backup.Service) {
	s.BackupService = bs
}

func (s *Server) SetTrustMgmt(tm *trustmgmt.Service) {
	s.TrustMgmt = tm
}

func (s *Server) SetPolicyMgmt(pm *policymgmt.Service) {
	s.PolicyMgmt = pm
}

func (s *Server) SetAuditStore(store *audit.Store) {
	s.AuditStore = store
}

func (s *Server) SetAllowedOrigins(origins []string) {
	s.AllowedOrigins = append([]string(nil), origins...)
}

func (s *Server) registerRoutes() {
	// Public routes (no auth required).
	s.mux.HandleFunc("/admin/login", s.handleLogin)
	s.mux.HandleFunc("/admin/logout", s.handleLogout)
	s.mux.HandleFunc("/admin/session", s.handleSession)

	// Protected routes — each requires session + specific permission + audit.
	protected := func(perm Permission, action AuditAction, h http.HandlerFunc) http.Handler {
		return s.RequireSession(
			s.AuditMiddleware(action)(
				s.RequirePermission(perm)(
					http.HandlerFunc(h),
				),
			),
		)
	}

	s.mux.Handle("/admin/health", protected(PermHealthRead, AuditHealthViewed, s.handleHealth))
	s.mux.Handle("/admin/audit", protected(PermAuditRead, AuditHealthViewed, s.handleAudit))
	s.mux.Handle("/admin/metrics", protected(PermMetricsRead, AuditHealthViewed, s.handleMetrics))
	s.mux.Handle("/admin/diagnostics", protected(PermSystemRead, AuditHealthViewed, s.handleDiagnostics))
	s.mux.Handle("/admin/runtime", protected(PermRuntimeRead, AuditRuntimeViewed, s.handleRuntime))
	s.mux.Handle("/admin/runtime/reload", protected(PermRuntimeControl, AuditRuntimeReload, s.handleRuntimeReload))

	// Settings requires special handling for GET vs POST with different perms.
	// Domains routes — GET list, POST create, GET by ID, PUT update, DELETE.
	s.mux.Handle("/admin/domains",
		s.RequireSession(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "GET":
					s.RequirePermission(PermDomainsRead)(http.HandlerFunc(s.handleDomainList)).ServeHTTP(w, r)
				case "POST":
					s.RequirePermission(PermDomainsWrite)(s.AuditMiddleware(AuditDomainCreated)(http.HandlerFunc(s.handleDomainCreate))).ServeHTTP(w, r)
				default:
					jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
				}
			}),
		),
	)
	s.mux.Handle("/admin/domains/",
		s.RequireSession(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case "GET":
					s.RequirePermission(PermDomainsRead)(http.HandlerFunc(s.handleDomainGet)).ServeHTTP(w, r)
				case "PUT":
					s.RequirePermission(PermDomainsWrite)(s.AuditMiddleware(AuditDomainUpdated)(http.HandlerFunc(s.handleDomainUpdate))).ServeHTTP(w, r)
				case "DELETE":
					s.RequirePermission(PermDomainsWrite)(s.AuditMiddleware(AuditDomainDeleted)(http.HandlerFunc(s.handleDomainDelete))).ServeHTTP(w, r)
				default:
					jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
				}
			}),
		),
	)

	// DNS report route.
	s.mux.Handle("/admin/domains/dns/", s.RequireSession(
		s.RequirePermission(PermDomainsRead)(
			s.AuditMiddleware(AuditDNSReportViewed)(
				http.HandlerFunc(s.handleDomainDNS),
			),
		),
	))

	// Mailbox routes — methods dispatched by path.
	s.mux.Handle("/admin/mailboxes", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				s.RequirePermission(PermMailboxesRead)(http.HandlerFunc(s.handleMailboxList)).ServeHTTP(rw, r)
			case "POST":
				s.RequirePermission(PermMailboxesWrite)(s.AuditMiddleware(AuditMailboxCreated)(http.HandlerFunc(s.handleMailboxCreate))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))
	s.mux.Handle("/admin/mailboxes/", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				s.RequirePermission(PermMailboxesRead)(http.HandlerFunc(s.handleMailboxGet)).ServeHTTP(rw, r)
			case "PUT":
				s.RequirePermission(PermMailboxesWrite)(s.AuditMiddleware(AuditMailboxUpdated)(http.HandlerFunc(s.handleMailboxUpdate))).ServeHTTP(rw, r)
			case "DELETE":
				s.RequirePermission(PermMailboxesWrite)(s.AuditMiddleware(AuditMailboxDeleted)(http.HandlerFunc(s.handleMailboxDelete))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))
	// Sub-resource routes for mailbox actions (protected via mailboxProtected).
	s.mux.Handle("/admin/mailboxes/reset-password/", mailboxProtected(s, AuditMailboxPasswordReset, s.handleMailboxResetPassword))
	s.mux.Handle("/admin/mailboxes/suspend/", mailboxProtected(s, AuditMailboxSuspended, s.handleMailboxSuspend))
	s.mux.Handle("/admin/mailboxes/activate/", mailboxProtected(s, AuditMailboxActivated, s.handleMailboxActivate))

	// Queue routes.
	s.mux.Handle("/admin/queue/summary", s.RequireSession(
		s.RequirePermission(PermQueueRead)(
			s.AuditMiddleware(AuditQueueViewed)(
				http.HandlerFunc(s.handleQueueSummary),
			),
		),
	))
	s.mux.Handle("/admin/queue/entries", s.RequireSession(
		s.RequirePermission(PermQueueRead)(
			s.AuditMiddleware(AuditQueueViewed)(
				http.HandlerFunc(s.handleQueueEntries),
			),
		),
	))
	s.mux.Handle("/admin/queue/entries/", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			switch {
			case strings.Contains(path, "/attempts"):
				s.RequirePermission(PermQueueRead)(http.HandlerFunc(s.handleQueueAttempts)).ServeHTTP(rw, r)
			case strings.Contains(path, "/retry"):
				s.RequirePermission(PermQueueWrite)(s.AuditMiddleware(AuditQueueRetry)(http.HandlerFunc(s.handleQueueRetry))).ServeHTTP(rw, r)
			case strings.Contains(path, "/cancel"):
				s.RequirePermission(PermQueueWrite)(s.AuditMiddleware(AuditQueueCancel)(http.HandlerFunc(s.handleQueueCancel))).ServeHTTP(rw, r)
			default:
				s.RequirePermission(PermQueueRead)(http.HandlerFunc(s.handleQueueEntry)).ServeHTTP(rw, r)
			}
		}),
	))

	// Message Trace routes.
	s.mux.Handle("/admin/message-trace", s.RequireSession(
		s.RequirePermission(PermAuditRead)(
			s.AuditMiddleware(AuditMessageTraceSearch)(
				http.HandlerFunc(s.handleMessageTraceSearch),
			),
		),
	))
	s.mux.Handle("/admin/message-trace/", s.RequireSession(
		s.RequirePermission(PermAuditRead)(
			s.AuditMiddleware(AuditMessageTraceViewed)(
				http.HandlerFunc(s.handleMessageTraceDetail),
			),
		),
	))

	// Trust routes.
	s.mux.Handle("/admin/trust/summary", s.RequireSession(
		s.RequirePermission(PermTrustRead)(
			http.HandlerFunc(s.handleTrustSummary),
		),
	))
	s.mux.Handle("/admin/trust/lockouts", s.RequireSession(
		s.RequirePermission(PermTrustRead)(
			http.HandlerFunc(s.handleTrustLockouts),
		),
	))
	s.mux.Handle("/admin/trust/lockouts/clear/", s.RequireSession(
		s.RequirePermission(PermTrustWrite)(
			s.AuditMiddleware(AuditTrustLockoutCleared)(
				http.HandlerFunc(s.handleTrustClearLockout),
			),
		),
	))

	// Policy routes.
	s.mux.Handle("/admin/policies", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				s.RequirePermission(PermPoliciesRead)(http.HandlerFunc(s.handlePolicyGet)).ServeHTTP(rw, r)
			case "POST":
				s.RequirePermission(PermPoliciesWrite)(s.AuditMiddleware(AuditPolicyCreated)(http.HandlerFunc(s.handlePolicyCreate))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))
	s.mux.Handle("/admin/policies/", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "PUT":
				s.RequirePermission(PermPoliciesWrite)(s.AuditMiddleware(AuditPolicyUpdated)(http.HandlerFunc(s.handlePolicyUpdate))).ServeHTTP(rw, r)
			case "DELETE":
				s.RequirePermission(PermPoliciesWrite)(s.AuditMiddleware(AuditPolicyDeleted)(http.HandlerFunc(s.handlePolicyDelete))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))

	// Backup routes.
	s.mux.Handle("/admin/backups", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				s.RequirePermission(PermBackupRead)(s.AuditMiddleware(AuditBackupViewed)(http.HandlerFunc(s.handleBackupList))).ServeHTTP(rw, r)
			case "POST":
				s.RequirePermission(PermBackupWrite)(s.AuditMiddleware(AuditBackupCreated)(http.HandlerFunc(s.handleBackupCreate))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))
	s.mux.Handle("/admin/backups/", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			switch {
			case strings.HasSuffix(path, "/verify"):
				s.RequirePermission(PermBackupRead)(s.AuditMiddleware(AuditBackupVerified)(http.HandlerFunc(s.handleBackupVerify))).ServeHTTP(rw, r)
			case strings.HasSuffix(path, "/preview"):
				s.RequirePermission(PermBackupRead)(s.AuditMiddleware(AuditBackupViewed)(http.HandlerFunc(s.handleBackupPreview))).ServeHTTP(rw, r)
			case strings.HasSuffix(path, "/restore"):
				s.RequirePermission(PermBackupWrite)(s.AuditMiddleware(AuditBackupRestored)(http.HandlerFunc(s.handleBackupRestore))).ServeHTTP(rw, r)
			case r.Method == "GET":
				s.RequirePermission(PermBackupRead)(s.AuditMiddleware(AuditBackupViewed)(http.HandlerFunc(s.handleBackupGet))).ServeHTTP(rw, r)
			case r.Method == "DELETE":
				s.RequirePermission(PermBackupWrite)(s.AuditMiddleware(AuditBackupDeleted)(http.HandlerFunc(s.handleBackupDelete))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))

	// TLS routes.
	tlsRead := func(perm Permission, action AuditAction, h http.HandlerFunc) http.Handler {
		return s.RequireSession(s.AuditMiddleware(action)(s.RequirePermission(perm)(http.HandlerFunc(h))))
	}
	s.mux.Handle("/admin/tls/certificates", tlsRead(PermSettingsRead, AuditCertificateViewed, s.handleTLSCertificates))
	s.mux.Handle("/admin/tls/certificates/validate/", tlsRead(PermSettingsRead, AuditCertificateValidated, s.handleTLSCertificateValidate))
	s.mux.Handle("/admin/tls/runtime", tlsRead(PermSettingsRead, AuditCertificateViewed, s.handleTLSRuntime))
	s.mux.Handle("/admin/tls/reload", tlsRead(PermSettingsWrite, AuditCertificateReload, s.handleTLSReload))

	// Compliance routes.
	compRead := func(perm Permission, action AuditAction, h http.HandlerFunc) http.Handler {
		return s.RequireSession(s.AuditMiddleware(action)(s.RequirePermission(perm)(http.HandlerFunc(h))))
	}
	s.mux.Handle("/admin/compliance/policies", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				s.RequirePermission(PermComplianceRead)(http.HandlerFunc(s.handleCompliancePolicyList)).ServeHTTP(rw, r)
			case "POST":
				s.RequirePermission(PermComplianceWrite)(s.AuditMiddleware(AuditPolicyCreated)(http.HandlerFunc(s.handleCompliancePolicyCreate))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))
	s.mux.Handle("/admin/compliance/policies/", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "PUT":
				s.RequirePermission(PermComplianceWrite)(s.AuditMiddleware(AuditPolicyUpdated)(http.HandlerFunc(s.handleCompliancePolicyUpdate))).ServeHTTP(rw, r)
			case "DELETE":
				s.RequirePermission(PermComplianceWrite)(s.AuditMiddleware(AuditPolicyDeleted)(http.HandlerFunc(s.handleCompliancePolicyDelete))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))
	s.mux.Handle("/admin/quarantine", compRead(PermComplianceRead, AuditMessageQuarantined, s.handleQuarantineList))
	s.mux.Handle("/admin/quarantine/release/", compRead(PermComplianceWrite, AuditMessageReleased, s.handleQuarantineRelease))
	s.mux.Handle("/admin/quarantine/delete/", compRead(PermComplianceWrite, AuditMessageDeleted, s.handleQuarantineDelete))
	s.mux.Handle("/admin/abuse", compRead(PermComplianceRead, AuditAbuseViewed, s.handleAbuseList))

	// Monitoring routes.
	monRead := func(perm Permission, action AuditAction, h http.HandlerFunc) http.Handler {
		return s.RequireSession(s.AuditMiddleware(action)(s.RequirePermission(perm)(http.HandlerFunc(h))))
	}
	s.mux.Handle("/admin/monitoring/alerts", monRead(PermMonitoringRead, AuditAlertViewed, s.handleMonitoringAlerts))
	s.mux.Handle("/admin/monitoring/alerts/resolve/", monRead(PermMonitoringWrite, AuditAlertResolved, s.handleMonitoringAlertResolve))
	s.mux.Handle("/admin/monitoring/capacity", monRead(PermMonitoringRead, AuditCapacityViewed, s.handleMonitoringCapacity))

	// Lifecycle routes.
	lcRead := func(perm Permission, action AuditAction, h http.HandlerFunc) http.Handler {
		return s.RequireSession(s.AuditMiddleware(action)(s.RequirePermission(perm)(http.HandlerFunc(h))))
	}
	s.mux.Handle("/admin/lifecycle/version", lcRead(PermLifecycleRead, AuditUpgradeStarted, s.handleLifecycleVersion))
	s.mux.Handle("/admin/lifecycle/history", lcRead(PermLifecycleRead, AuditUpgradeStarted, s.handleLifecycleHistory))
	s.mux.Handle("/admin/lifecycle/preflight", lcRead(PermLifecycleRead, AuditPreflightExecuted, s.handleLifecyclePreflight))
	s.mux.Handle("/admin/lifecycle/upgrade", lcRead(PermLifecycleWrite, AuditUpgradeStarted, s.handleLifecycleUpgrade))
	s.mux.Handle("/admin/lifecycle/rollback", lcRead(PermLifecycleWrite, AuditRollbackStarted, s.handleLifecycleRollback))

	// Migration routes.
	s.mux.Handle("/admin/migrations", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case "GET":
				s.RequirePermission(PermMigrationRead)(http.HandlerFunc(s.handleMigrationList)).ServeHTTP(rw, r)
			case "POST":
				s.RequirePermission(PermMigrationWrite)(s.AuditMiddleware(AuditMigrationStarted)(http.HandlerFunc(s.handleMigrationCreate))).ServeHTTP(rw, r)
			default:
				jsonError(rw, "method not allowed", http.StatusMethodNotAllowed)
			}
		}),
	))
	// Licensing route (read-only status).
	s.mux.Handle("/admin/licensing/status", protected(PermSettingsRead, AuditSettingsViewed, s.handleLicensingStatus))
	s.mux.Handle("/admin/licensing/install", protected(PermSettingsWrite, AuditLicenseInstalled, s.handleLicensingInstall))
	s.mux.Handle("/admin/licensing/validate", protected(PermSettingsRead, AuditLicenseValidated, s.handleLicensingValidate))
	s.mux.Handle("/admin/licensing/refresh", protected(PermSettingsWrite, AuditLicenseRefreshed, s.handleLicensingRefresh))

	s.mux.Handle("/admin/migrations/", s.RequireSession(
		http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			if strings.HasSuffix(path, "/cancel") {
				s.RequirePermission(PermMigrationWrite)(s.AuditMiddleware(AuditMigrationCancelled)(http.HandlerFunc(s.handleMigrationCancel))).ServeHTTP(rw, r)
			} else {
				s.RequirePermission(PermMigrationRead)(http.HandlerFunc(s.handleMigrationGet)).ServeHTTP(rw, r)
			}
		}),
	))

	s.mux.Handle("/admin/settings",
		s.RequireSession(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					s.RequirePermission(PermSettingsWrite)(
						s.AuditMiddleware(AuditSettingsUpdated)(
							http.HandlerFunc(s.handleSettingsUpdate),
						),
					).ServeHTTP(w, r)
					return
				}
				s.RequirePermission(PermSettingsRead)(
					s.AuditMiddleware(AuditSettingsViewed)(
						http.HandlerFunc(s.handleSettingsGet),
					),
				).ServeHTTP(w, r)
			}),
		),
	)
}

func (s *Server) Handler() http.Handler {
	return LoggingMiddleware(CORSMiddlewareWithOrigins(s.mux, s.AllowedOrigins))
}

func (s *Server) ListenAndServe(addr string) error {
	s.srv = &http.Server{
		Addr:    addr,
		Handler: s.Handler(),
	}
	return s.srv.ListenAndServe()
}

func (s *Server) Stop() {
	if s.srv != nil {
		s.srv.Close()
	}
}

// ── Health Endpoint ────────────────────────────────────────

type subsystemHealth struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	statuses := []subsystemHealth{
		{Name: "smtp", Status: s.getSMTPStatus()},
		{Name: "imap", Status: s.getIMAPStatus()},
		{Name: "pop3", Status: s.getPOP3Status()},
		{Name: "jmap", Status: s.getJMAPStatus()},
		{Name: "queue", Status: s.getQueueStatus()},
		{Name: "database", Status: s.getDatabaseStatus()},
		{Name: "mailstore", Status: s.getMailStoreStatus()},
		{Name: "trust", Status: s.getTrustStatus()},
		{Name: "policy", Status: s.getPolicyStatus()},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"checks": statuses,
	})
}

func (s *Server) getHealthFor(name string) string {
	if s.Observability == nil || s.Observability.Health == nil {
		return "unknown"
	}
	report := s.Observability.Health.Report()
	if report == nil || report.Checks == nil {
		return "unknown"
	}
	if check, ok := report.Checks[name]; ok {
		return check.Status.String()
	}
	return "unknown"
}

// ── Audit Endpoint ─────────────────────────────────────────

type auditEntryResponse struct {
	Actor     string `json:"actor"`
	Role      string `json:"role"`
	Action    string `json:"action"`
	Result    string `json:"result"`
	IP        string `json:"ip"`
	UserAgent string `json:"userAgent"`
	Timestamp int64  `json:"timestamp"`
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	if s.Observability == nil || s.Observability.EventHistory == nil {
		jsonOK(w, map[string]interface{}{"entries": []auditEntryResponse{}, "total": 0})
		return
	}

	all := s.Observability.EventHistory.Recent()

	actorFilter := r.URL.Query().Get("actor")
	actionFilter := r.URL.Query().Get("action")
	resultFilter := r.URL.Query().Get("result")

	var filtered []auditEntryResponse
	for _, e := range all {
		if actorFilter != "" && e.Fields["actor"] != actorFilter {
			continue
		}
		if actionFilter != "" && string(e.Type) != "admin_"+actionFilter {
			continue
		}
		if resultFilter != "" && e.Fields["result"] != resultFilter {
			continue
		}
		entry := auditEntryResponse{
			Actor:     e.Fields["actor"],
			Role:      e.Fields["role"],
			Action:    strings.TrimPrefix(string(e.Type), "admin_"),
			Result:    e.Fields["result"],
			IP:        e.Fields["ip"],
			UserAgent: e.Fields["userAgent"],
			Timestamp: e.Timestamp,
		}
		filtered = append(filtered, entry)
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	jsonOK(w, map[string]interface{}{
		"entries": filtered,
		"total":   len(filtered),
	})
}

// ── Metrics Endpoint ───────────────────────────────────────

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.Observability == nil || s.Observability.Metrics == nil {
		jsonOK(w, map[string]interface{}{"metrics": struct{}{}})
		return
	}

	snap := s.Observability.Metrics.Snapshot()
	jsonOK(w, map[string]interface{}{
		"metrics": snap,
	})
}

// ── Diagnostics Endpoint ───────────────────────────────────

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	if s.Observability == nil || s.Observability.Snapshot == nil {
		jsonOK(w, map[string]interface{}{"status": "unavailable"})
		return
	}

	snap := s.Observability.Snapshot.Snapshot()
	jsonOK(w, map[string]interface{}{
		"timestamp":       time.Now().Unix(),
		"health":          snap.Health,
		"recent_failures": snap.RecentFailures,
		"queue_summary":   snap.QueueSummary,
	})
}

// ── Runtime Endpoint ────────────────────────────────────────

func (s *Server) handleRuntime(w http.ResponseWriter, r *http.Request) {
	if s.RuntimeControl == nil {
		jsonError(w, "runtime control not available", http.StatusServiceUnavailable)
		return
	}
	snap := s.RuntimeControl.Snapshot()
	jsonOK(w, snap)
}

// ── Runtime Reload Endpoint ─────────────────────────────────

func (s *Server) handleRuntimeReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.RuntimeControl == nil {
		jsonError(w, "runtime control not available", http.StatusServiceUnavailable)
		return
	}
	result := s.RuntimeControl.Reload()
	if !result.Success {
		jsonError(w, result.Message, http.StatusInternalServerError)
		return
	}
	jsonOK(w, result)
}

// ── Backup Handlers ────────────────────────────────────────

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	if s.BackupService == nil {
		jsonError(w, "backup not available", 503)
		return
	}
	list, err := s.BackupService.ListBackups(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if list == nil {
		list = []backup.Backup{}
	}
	jsonOK(w, map[string]interface{}{"backups": list})
}

func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	if s.BackupService == nil {
		jsonError(w, "backup not available", 503)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	b, err := s.BackupService.CreateBackup(r.Context(), req.Name)
	if err != nil {
		if s.Observability != nil {
			s.Observability.Metrics.IncBackupsFailed()
		}
		jsonError(w, err.Error(), 500)
		return
	}
	if s.Observability != nil {
		s.Observability.Metrics.IncBackupsCreated()
		s.Observability.Metrics.AddBackupBytes(b.SizeBytes)
	}
	jsonOK(w, b)
}

func (s *Server) handleBackupGet(w http.ResponseWriter, r *http.Request) {
	if s.BackupService == nil {
		jsonError(w, "backup not available", 503)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/backups/")
	id = strings.TrimSuffix(id, "/")
	for _, suffix := range []string{"/verify", "/preview", "/restore"} {
		id = strings.TrimSuffix(id, suffix)
	}
	if id == "" {
		jsonError(w, "id required", 400)
		return
	}
	b, err := s.BackupService.GetBackup(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), 404)
		return
	}
	jsonOK(w, b)
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	if s.BackupService == nil {
		jsonError(w, "backup not available", 503)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/backups/")
	id = strings.TrimSuffix(id, "/")
	if err := s.BackupService.DeleteBackup(r.Context(), id); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleBackupVerify(w http.ResponseWriter, r *http.Request) {
	if s.BackupService == nil {
		jsonError(w, "backup not available", 503)
		return
	}
	id := extractBackupID(r.URL.Path)
	result, err := s.BackupService.VerifyBackup(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if s.Observability != nil {
		s.Observability.Metrics.IncBackupsVerified()
	}
	jsonOK(w, result)
}

func (s *Server) handleBackupPreview(w http.ResponseWriter, r *http.Request) {
	if s.BackupService == nil {
		jsonError(w, "backup not available", 503)
		return
	}
	id := extractBackupID(r.URL.Path)
	preview, err := s.BackupService.RestorePreview(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), 404)
		return
	}
	jsonOK(w, preview)
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if s.BackupService == nil {
		jsonError(w, "backup not available", 503)
		return
	}
	id := extractBackupID(r.URL.Path)
	result := s.BackupService.RestoreBackup(r.Context(), id)
	if !result.Success {
		if s.Observability != nil {
			s.Observability.EventHistory.Record("backup_restore_failed", map[string]string{"id": id, "error": result.Message})
			s.Observability.Metrics.IncBackupsFailed()
		}
		jsonError(w, result.Message, 500)
		return
	}
	if s.Observability != nil {
		s.Observability.EventHistory.Record("backup_restored", map[string]string{"id": id})
		s.Observability.Metrics.IncBackupsRestored()
	}
	jsonOK(w, result)
}

func extractBackupID(path string) string {
	// Remove /admin/backups/ prefix, then strip /verify, /preview, /restore
	id := strings.TrimPrefix(path, "/admin/backups/")
	for _, suffix := range []string{"/verify", "/preview", "/restore"} {
		id = strings.TrimSuffix(id, suffix)
	}
	id = strings.TrimSuffix(id, "/")
	return id
}

// ── TLS Handlers ─────────────────────────────────────────

func (s *Server) handleTLSCertificates(w http.ResponseWriter, r *http.Request) {
	if s.TLSMgmt == nil {
		jsonError(w, "TLS management not available", 503)
		return
	}
	// Refresh certs on each request to pick up changes.
	certs, err := s.TLSMgmt.CheckExpiration(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if certs == nil {
		certs = []tlsmgmt.TLSCertificate{}
	}
	jsonOK(w, map[string]interface{}{"certificates": certs})
}

func (s *Server) handleTLSCertificateValidate(w http.ResponseWriter, r *http.Request) {
	if s.TLSMgmt == nil {
		jsonError(w, "TLS management not available", 503)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/tls/certificates/validate/")
	id = strings.TrimSuffix(id, "/")
	result, err := s.TLSMgmt.ValidateCertificate(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, result)
}

func (s *Server) handleTLSRuntime(w http.ResponseWriter, r *http.Request) {
	if s.TLSMgmt == nil {
		jsonError(w, "TLS management not available", 503)
		return
	}
	statuses := s.TLSMgmt.GetRuntimeTLSStatus(r.Context())
	jsonOK(w, map[string]interface{}{"services": statuses})
}

func (s *Server) handleTLSReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.TLSMgmt == nil {
		jsonError(w, "TLS management not available", 503)
		return
	}
	result := s.TLSMgmt.ReloadCertificates(r.Context())
	if s.Observability != nil {
		if result.Success {
			s.Observability.Metrics.IncTLSReloads()
		} else {
			s.Observability.Metrics.IncTLSReloadFailures()
		}
	}
	if !result.Success {
		jsonError(w, result.Message, 500)
		return
	}
	jsonOK(w, result)
}

// ── Compliance Handlers ─────────────────────────────────

func withCompliance(s *Server, w http.ResponseWriter, fn func() error) bool {
	if s.ComplianceService == nil {
		jsonError(w, "compliance not available", 503)
		return false
	}
	if err := fn(); err != nil {
		jsonError(w, err.Error(), 500)
		return false
	}
	return true
}

func (s *Server) handleCompliancePolicyList(w http.ResponseWriter, r *http.Request) {
	if !withCompliance(s, w, func() error { return nil }) {
		return
	}
	policies, err := s.ComplianceService.ListPolicies(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if policies == nil {
		policies = []compliance.Policy{}
	}
	jsonOK(w, map[string]interface{}{"policies": policies})
}

func (s *Server) handleCompliancePolicyCreate(w http.ResponseWriter, r *http.Request) {
	if s.ComplianceService == nil {
		jsonError(w, "compliance not available", 503)
		return
	}
	var p compliance.Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		jsonError(w, "invalid request", 400)
		return
	}
	if err := s.ComplianceService.CreatePolicy(r.Context(), &p); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	jsonOK(w, p)
}

func (s *Server) handleCompliancePolicyUpdate(w http.ResponseWriter, r *http.Request) {
	if s.ComplianceService == nil {
		jsonError(w, "compliance not available", 503)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/compliance/policies/")
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}
	var p compliance.Policy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		jsonError(w, "invalid request", 400)
		return
	}
	if err := s.ComplianceService.UpdatePolicy(r.Context(), id, &p); err != nil {
		jsonError(w, err.Error(), 400)
		return
	}
	jsonOK(w, map[string]string{"status": "updated"})
}

func (s *Server) handleCompliancePolicyDelete(w http.ResponseWriter, r *http.Request) {
	if s.ComplianceService == nil {
		jsonError(w, "compliance not available", 503)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/compliance/policies/")
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}
	if err := s.ComplianceService.DeletePolicy(r.Context(), id); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleQuarantineList(w http.ResponseWriter, r *http.Request) {
	if s.ComplianceService == nil {
		jsonError(w, "compliance not available", 503)
		return
	}
	msgs, err := s.ComplianceService.ListQuarantine(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if msgs == nil {
		msgs = []compliance.QuarantinedMessage{}
	}
	jsonOK(w, map[string]interface{}{"messages": msgs})
}

func (s *Server) handleQuarantineRelease(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.ComplianceService == nil {
		jsonError(w, "compliance not available", 503)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/quarantine/release/")
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}
	// Get releasing user from session.
	session := s.getSession(r)
	releasedBy := session.Username
	q, err := s.ComplianceService.ReleaseMessage(r.Context(), id, releasedBy)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, q)
}

func (s *Server) handleQuarantineDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.ComplianceService == nil {
		jsonError(w, "compliance not available", 503)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/quarantine/delete/")
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}
	if err := s.ComplianceService.DeleteQuarantine(r.Context(), id); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleAbuseList(w http.ResponseWriter, r *http.Request) {
	if s.ComplianceService == nil {
		jsonError(w, "compliance not available", 503)
		return
	}
	events, err := s.ComplianceService.ListAbuseEvents(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if events == nil {
		events = []compliance.AbuseEvent{}
	}
	jsonOK(w, map[string]interface{}{"events": events})
}

// ── Monitoring Handlers ─────────────────────────────────

func (s *Server) handleMonitoringAlerts(w http.ResponseWriter, r *http.Request) {
	if s.MonitoringService == nil {
		jsonError(w, "monitoring not available", 503)
		return
	}
	// Evaluate alerts on each request for freshness.
	s.MonitoringService.EvaluateAlerts(r.Context())
	alerts, err := s.MonitoringService.ListActiveAlerts(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if alerts == nil {
		alerts = []monitoring.Alert{}
	}
	jsonOK(w, map[string]interface{}{"alerts": alerts})
}

func (s *Server) handleMonitoringAlertResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.MonitoringService == nil {
		jsonError(w, "monitoring not available", 503)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/monitoring/alerts/resolve/")
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}
	if err := s.MonitoringService.ResolveAlert(r.Context(), id); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, map[string]string{"status": "resolved"})
}

func (s *Server) handleMonitoringCapacity(w http.ResponseWriter, r *http.Request) {
	if s.MonitoringService == nil {
		jsonError(w, "monitoring not available", 503)
		return
	}
	c := s.MonitoringService.GetCapacity(r.Context())
	jsonOK(w, c)
}

// ── Lifecycle Handlers ──────────────────────────────────

func (s *Server) handleLifecycleVersion(w http.ResponseWriter, r *http.Request) {
	if s.LifecycleService == nil {
		jsonError(w, "lifecycle not available", 503)
		return
	}
	v, err := s.LifecycleService.CurrentVersion(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	history, err := s.LifecycleService.VersionHistory(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if history == nil {
		history = []lifecycle.VersionRecord{}
	}
	jsonOK(w, map[string]interface{}{"current": v, "history": history})
}

func (s *Server) handleLifecycleHistory(w http.ResponseWriter, r *http.Request) {
	if s.LifecycleService == nil {
		jsonError(w, "lifecycle not available", 503)
		return
	}
	upgrades, err := s.LifecycleService.UpgradeHistory(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if upgrades == nil {
		upgrades = []lifecycle.UpgradeRecord{}
	}
	jsonOK(w, map[string]interface{}{"upgrades": upgrades})
}

func (s *Server) handleLifecyclePreflight(w http.ResponseWriter, r *http.Request) {
	if s.LifecycleService == nil {
		jsonError(w, "lifecycle not available", 503)
		return
	}
	result := s.LifecycleService.RunPreflight(r.Context())
	jsonOK(w, result)
}

func (s *Server) handleLifecycleUpgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.LifecycleService == nil {
		jsonError(w, "lifecycle not available", 503)
		return
	}
	var req struct {
		FromVersion string `json:"fromVersion"`
		ToVersion   string `json:"toVersion"`
	}
	json.NewDecoder(r.Body).Decode(&req)
	if req.ToVersion == "" {
		req.ToVersion = "latest"
	}
	result := s.LifecycleService.Upgrade(r.Context(), req.FromVersion, req.ToVersion)
	if s.Observability != nil {
		if result.Status == lifecycle.UpgradeCompleted {
			s.Observability.EventHistory.Record("upgrade_completed", map[string]string{"from": req.FromVersion, "to": req.ToVersion})
		} else {
			s.Observability.EventHistory.Record("upgrade_failed", map[string]string{"status": string(result.Status)})
		}
	}
	jsonOK(w, result)
}

func (s *Server) handleLifecycleRollback(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.LifecycleService == nil {
		jsonError(w, "lifecycle not available", 503)
		return
	}
	result := s.LifecycleService.Rollback(r.Context())
	jsonOK(w, result)
}

// ── Licensing Handlers ─────────────────────────────────

func (s *Server) handleLicensingStatus(w http.ResponseWriter, r *http.Request) {
	if s.LicensingService == nil {
		jsonOK(w, map[string]interface{}{
			"edition": "community", "valid": false,
			"machineId": licensing.GenerateMachineID(),
			"limits":    map[string]interface{}{"domains": 1, "mailboxes": 5, "storageGB": 1},
			"usage":     map[string]interface{}{},
			"graceState": "valid", "daysRemaining": -1, "features": []string{},
		})
		return
	}

	usage := s.LicensingService.StatusWithUsage(r.Context(),
		func(ctx context.Context) (int64, error) {
			if s.DomainRegistry == nil { return 0, nil }
			domains, err := s.DomainRegistry.ListDomains(ctx)
			if err != nil { return 0, err }
			return int64(len(domains)), nil
		},
		func(ctx context.Context) (int64, error) {
			if s.MailboxService == nil { return 0, nil }
			mbs, err := s.MailboxService.ListMailboxes(ctx, nil)
			if err != nil { return 0, err }
			return int64(len(mbs)), nil
		},
	)

	// Enrich with authority status.
	usage["authority"] = s.getAuthorityStatus()

	jsonOK(w, usage)
}

func (s *Server) getAuthorityStatus() map[string]interface{} {
	if s.AuthorityService == nil {
		return map[string]interface{}{
			"authorityState": "unknown",
			"cacheValid":     false,
			"offlineAllowed": true,
		}
	}
	as := s.AuthorityService.Status()
	return map[string]interface{}{
		"authorityState": string(as.AuthorityState),
		"licenseState":   string(as.LicenseState),
		"lastValidation": as.LastValidation,
		"nextValidation": as.NextValidation,
		"graceExpiresAt": as.GraceExpiresAt,
		"cacheValid":     as.CacheValid,
		"offlineAllowed": as.OfflineAllowed,
		"offlineSeconds": as.OfflineSeconds,
	}
}

func (s *Server) handleLicensingInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.LicensingService == nil {
		jsonError(w, "licensing service not available", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		LicenseJSON   string `json:"licenseJson"`
		ActivationKey string `json:"activationKey,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.LicenseJSON == "" {
		jsonError(w, "licenseJson is required", http.StatusBadRequest)
		return
	}

	status, err := s.LicensingService.InstallLicense(r.Context(), []byte(req.LicenseJSON))
	if err != nil {
		session := s.getSession(r)
		if session != nil {
			s.recordAudit(AuditLicenseInstallFailed, session.Username, string(session.Role), r, err.Error())
		}
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	jsonOK(w, map[string]interface{}{
		"status":  "installed",
		"edition": string(status.Edition),
		"valid":   status.Valid,
	})
}

func (s *Server) handleLicensingValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.LicensingService == nil {
		jsonError(w, "licensing service not available", http.StatusServiceUnavailable)
		return
	}

	status := s.LicensingService.LoadLicense(r.Context())
	jsonOK(w, map[string]interface{}{
		"valid":      status.Valid,
		"edition":    string(status.Edition),
		"machineId":  status.MachineID,
		"errors":     func() []string { if status.Validation != nil { return status.Validation.Errors }; return nil }(),
		"warnings":   func() []string { if status.Validation != nil { return status.Validation.Warnings }; return nil }(),
	})
}

func (s *Server) handleLicensingRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.AuthorityService == nil {
		jsonOK(w, map[string]interface{}{
			"status":         "no_authority",
			"authorityState": "unknown",
		})
		return
	}

	lic := s.LicensingService.GetLicense(r.Context())
	licenseID := ""
	edition := ""
	if lic != nil {
		licenseID = lic.LicenseID
		edition = string(lic.Edition)
	}

	machineID := licensing.GenerateMachineID()
	authStatus := s.AuthorityService.ValidateWithAuthority(r.Context(), licenseID, edition, machineID)

	jsonOK(w, map[string]interface{}{
		"status":         "refreshed",
		"authorityState": string(authStatus.AuthorityState),
		"licenseState":   string(authStatus.LicenseState),
		"offlineAllowed": authStatus.OfflineAllowed,
		"cacheValid":     authStatus.CacheValid,
	})
}

// ── Migration Handlers ──────────────────────────────────

func (s *Server) handleMigrationList(w http.ResponseWriter, r *http.Request) {
	if s.MigrationService == nil {
		jsonError(w, "migration not available", 503)
		return
	}
	jobs, err := s.MigrationService.ListJobs(r.Context())
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if jobs == nil {
		jobs = []migration.ImportJob{}
	}
	jsonOK(w, map[string]interface{}{"jobs": jobs})
}

func (s *Server) handleMigrationCreate(w http.ResponseWriter, r *http.Request) {
	if s.MigrationService == nil {
		jsonError(w, "migration not available", 503)
		return
	}
	var req struct {
		SourceType string `json:"sourceType"`
		SourceHost string `json:"sourceHost"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request", 400)
		return
	}
	j, err := s.MigrationService.CreateJob(r.Context(), migration.ImportSourceType(req.SourceType), req.SourceHost)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, j)
}

func (s *Server) handleMigrationGet(w http.ResponseWriter, r *http.Request) {
	if s.MigrationService == nil {
		jsonError(w, "migration not available", 503)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/migrations/")
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}
	j, err := s.MigrationService.GetJob(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	if j == nil {
		jsonError(w, "not found", 404)
		return
	}
	jsonOK(w, j)
}

func (s *Server) handleMigrationCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", 405)
		return
	}
	if s.MigrationService == nil {
		jsonError(w, "migration not available", 503)
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/cancel")
	id, err := parsePathID(path, "/admin/migrations/")
	if err != nil {
		jsonError(w, "invalid id", 400)
		return
	}
	if err := s.MigrationService.CancelJob(r.Context(), id); err != nil {
		jsonError(w, err.Error(), 500)
		return
	}
	jsonOK(w, map[string]string{"status": "cancelled"})
}

// ── Settings Endpoints ──────────────────────────────────────

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.RuntimeControl == nil {
		jsonError(w, "runtime control not available", http.StatusServiceUnavailable)
		return
	}
	settings := s.RuntimeControl.GetSettings()
	jsonOK(w, settings)
}

func (s *Server) handleSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	if s.RuntimeControl == nil {
		jsonError(w, "runtime control not available", http.StatusServiceUnavailable)
		return
	}
	var req runtimecontrol.Settings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.RuntimeControl.UpdateSettings(&req); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, map[string]string{"status": "updated"})
}

// ── Mailbox Handlers ─────────────────────────────────-------

func (s *Server) handleMailboxList(w http.ResponseWriter, r *http.Request) {
	if s.MailboxService == nil {
		jsonError(w, "mailbox service not available", http.StatusServiceUnavailable)
		return
	}
	var domainID *uint
	if didStr := r.URL.Query().Get("domainId"); didStr != "" {
		if did, err := parseUint64(didStr); err == nil {
			domainID = &did
		}
	}
	mboxes, err := s.MailboxService.ListMailboxes(r.Context(), domainID)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if mboxes == nil {
		mboxes = []mailboxmgmt.Mailbox{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"mailboxes": mboxes})
}

func (s *Server) handleMailboxGet(w http.ResponseWriter, r *http.Request) {
	if s.MailboxService == nil {
		jsonError(w, "mailbox service not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/mailboxes/")
	if err != nil {
		jsonError(w, "invalid mailbox id", http.StatusBadRequest)
		return
	}
	mb, err := s.MailboxService.GetMailbox(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if mb == nil {
		jsonError(w, "mailbox not found", http.StatusNotFound)
		return
	}
	jsonOK(w, mb)
}

func (s *Server) handleMailboxCreate(w http.ResponseWriter, r *http.Request) {
	if s.MailboxService == nil {
		jsonError(w, "mailbox service not available", http.StatusServiceUnavailable)
		return
	}
	var req mailboxmgmt.CreateMailboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	mb, err := s.MailboxService.CreateMailbox(r.Context(), &req)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, mb)
}

func (s *Server) handleMailboxUpdate(w http.ResponseWriter, r *http.Request) {
	if s.MailboxService == nil {
		jsonError(w, "mailbox service not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/mailboxes/")
	if err != nil {
		jsonError(w, "invalid mailbox id", http.StatusBadRequest)
		return
	}
	var req mailboxmgmt.UpdateMailboxRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	mb, err := s.MailboxService.UpdateMailbox(r.Context(), id, &req)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, mb)
}

func (s *Server) handleMailboxDelete(w http.ResponseWriter, r *http.Request) {
	if s.MailboxService == nil {
		jsonError(w, "mailbox service not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/mailboxes/")
	if err != nil {
		jsonError(w, "invalid mailbox id", http.StatusBadRequest)
		return
	}
	if err := s.MailboxService.DeleteMailbox(r.Context(), id); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func (s *Server) handleMailboxResetPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.MailboxService == nil {
		jsonError(w, "mailbox service not available", http.StatusServiceUnavailable)
		return
	}
	// Path: /admin/mailboxes/reset-password/{id}
	id, err := parsePathID(r.URL.Path, "/admin/mailboxes/reset-password/")
	if err != nil {
		jsonError(w, "invalid mailbox id", http.StatusBadRequest)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if err := s.MailboxService.ResetPassword(r.Context(), id, req.Password); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, map[string]string{"status": "password_updated"})
}

func (s *Server) handleMailboxSuspend(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.MailboxService == nil {
		jsonError(w, "mailbox service not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/mailboxes/suspend/")
	if err != nil {
		jsonError(w, "invalid mailbox id", http.StatusBadRequest)
		return
	}
	mb, err := s.MailboxService.SuspendMailbox(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, mb)
}

func (s *Server) handleMailboxActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.MailboxService == nil {
		jsonError(w, "mailbox service not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/mailboxes/activate/")
	if err != nil {
		jsonError(w, "invalid mailbox id", http.StatusBadRequest)
		return
	}
	mb, err := s.MailboxService.ActivateMailbox(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, mb)
}

// update the `json.NewEncoder` — this should use jsonOK.
// The sub-resource routes need session+permission protection. We wrap them:
func mailboxProtected(s *Server, action AuditAction, h http.HandlerFunc) http.Handler {
	return s.RequireSession(s.AuditMiddleware(action)(s.RequirePermission(PermMailboxesWrite)(http.HandlerFunc(h))))
}

// ── Queue Handlers ─────────────────────────────────────────

func (s *Server) handleQueueSummary(w http.ResponseWriter, r *http.Request) {
	if s.QueueService == nil {
		jsonError(w, "queue service not available", http.StatusServiceUnavailable)
		return
	}
	summary, err := s.QueueService.GetSummary(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, summary)
}

func (s *Server) handleQueueEntries(w http.ResponseWriter, r *http.Request) {
	if s.QueueService == nil {
		jsonError(w, "queue service not available", http.StatusServiceUnavailable)
		return
	}
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	resp, err := s.QueueService.ListEntries(r.Context(), status, limit, offset)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, resp)
}

func (s *Server) handleQueueEntry(w http.ResponseWriter, r *http.Request) {
	if s.QueueService == nil {
		jsonError(w, "queue service not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/queue/entries/")
	if err != nil {
		jsonError(w, "invalid entry id", http.StatusBadRequest)
		return
	}
	entry, err := s.QueueService.GetEntry(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if entry == nil {
		jsonError(w, "entry not found", http.StatusNotFound)
		return
	}
	jsonOK(w, entry)
}

func (s *Server) handleQueueAttempts(w http.ResponseWriter, r *http.Request) {
	if s.QueueService == nil {
		jsonError(w, "queue service not available", http.StatusServiceUnavailable)
		return
	}
	// Path: /admin/queue/entries/{id}/attempts
	path := strings.TrimSuffix(r.URL.Path, "/attempts")
	id, err := parsePathID(path, "/admin/queue/entries/")
	if err != nil {
		jsonError(w, "invalid entry id", http.StatusBadRequest)
		return
	}
	attempts, err := s.QueueService.ListAttempts(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if attempts == nil {
		attempts = []queuemgmt.Attempt{}
	}
	jsonOK(w, map[string]interface{}{"attempts": attempts})
}

func (s *Server) handleQueueRetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.QueueService == nil {
		jsonError(w, "queue service not available", http.StatusServiceUnavailable)
		return
	}
	// Path: /admin/queue/entries/{id}/retry
	path := strings.TrimSuffix(r.URL.Path, "/retry")
	id, err := parsePathID(path, "/admin/queue/entries/")
	if err != nil {
		jsonError(w, "invalid entry id", http.StatusBadRequest)
		return
	}
	if err := s.QueueService.RetryEntry(r.Context(), id); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, map[string]string{"status": "retried"})
}

func (s *Server) handleQueueCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.QueueService == nil {
		jsonError(w, "queue service not available", http.StatusServiceUnavailable)
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/cancel")
	id, err := parsePathID(path, "/admin/queue/entries/")
	if err != nil {
		jsonError(w, "invalid entry id", http.StatusBadRequest)
		return
	}
	if err := s.QueueService.CancelEntry(r.Context(), id); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, map[string]string{"status": "cancelled"})
}

// ── Message Trace Handlers ─────────────────────────────────

func (s *Server) handleMessageTraceSearch(w http.ResponseWriter, r *http.Request) {
	if s.MessageTrace == nil {
		jsonError(w, "message trace not available", http.StatusServiceUnavailable)
		return
	}
	messageID := r.URL.Query().Get("messageId")
	sender := r.URL.Query().Get("sender")
	recipient := r.URL.Query().Get("recipient")
	domain := r.URL.Query().Get("domain")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	resp, err := s.MessageTrace.Search(r.Context(), messageID, sender, recipient, domain, limit, offset)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	jsonOK(w, resp)
}

func (s *Server) handleMessageTraceDetail(w http.ResponseWriter, r *http.Request) {
	if s.MessageTrace == nil {
		jsonError(w, "message trace not available", http.StatusServiceUnavailable)
		return
	}
	// Path: /admin/message-trace/{id} or /admin/message-trace/{id}/timeline
	path := r.URL.Path
	isTimeline := strings.HasSuffix(path, "/timeline")
	if isTimeline {
		path = strings.TrimSuffix(path, "/timeline")
	}

	id, err := parsePathID(path, "/admin/message-trace/")
	if err != nil {
		jsonError(w, "invalid trace id", http.StatusBadRequest)
		return
	}

	detail, err := s.MessageTrace.GetTrace(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if detail == nil {
		jsonError(w, "trace not found", http.StatusNotFound)
		return
	}

	if isTimeline {
		jsonOK(w, map[string]interface{}{"timeline": detail.Timeline})
		return
	}
	jsonOK(w, detail)
}

// ── Trust Handlers ─────────────────────────────────────────

func (s *Server) handleTrustSummary(w http.ResponseWriter, r *http.Request) {
	if s.TrustMgmt == nil {
		jsonError(w, "trust service not available", http.StatusServiceUnavailable)
		return
	}
	sum := s.TrustMgmt.Summary(r.Context())
	jsonOK(w, sum)
}

func (s *Server) handleTrustLockouts(w http.ResponseWriter, r *http.Request) {
	if s.TrustMgmt == nil {
		jsonError(w, "trust service not available", http.StatusServiceUnavailable)
		return
	}
	lockouts := s.TrustMgmt.ListLockouts(r.Context())
	jsonOK(w, map[string]interface{}{"lockouts": lockouts})
}

func (s *Server) handleTrustClearLockout(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		jsonError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.TrustMgmt == nil {
		jsonError(w, "trust service not available", http.StatusServiceUnavailable)
		return
	}
	// Path: /admin/trust/lockouts/clear/{key}
	key := strings.TrimPrefix(r.URL.Path, "/admin/trust/lockouts/clear/")
	key = strings.TrimSuffix(key, "/")
	if key == "" {
		jsonError(w, "key required", http.StatusBadRequest)
		return
	}
	if err := s.TrustMgmt.ClearLockout(r.Context(), key); err != nil {
		jsonError(w, err.Error(), http.StatusNotFound)
		return
	}
	jsonOK(w, map[string]string{"status": "cleared"})
}

// ── Policy Handlers ─────────────────────────────────────────

func (s *Server) handlePolicyGet(w http.ResponseWriter, r *http.Request) {
	// If path has an ID, look up specific policy.
	if id := strings.TrimPrefix(r.URL.Path, "/admin/policies/"); id != "" && id != r.URL.Path {
		parts := strings.SplitN(id, ":", 2)
		if len(parts) == 2 && parts[0] == "domain" {
			entry, err := s.PolicyMgmt.GetDomainPolicy(r.Context(), parts[1])
			if err != nil {
				jsonError(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if entry == nil {
				jsonError(w, "not found", http.StatusNotFound)
				return
			}
			jsonOK(w, entry)
			return
		}
		jsonError(w, "invalid policy id", http.StatusBadRequest)
		return
	}
	// List all — currently only returns configurable policies.
	entries := s.PolicyMgmt.List(r.Context())
	if entries == nil {
		entries = []policymgmt.PolicyEntry{}
	}
	jsonOK(w, map[string]interface{}{"policies": entries})
}

func (s *Server) handlePolicyCreate(w http.ResponseWriter, r *http.Request) {
	if s.PolicyMgmt == nil {
		jsonError(w, "policy service not available", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Scope  string `json:"scope"`
		Target string `json:"target"`
		Mode   string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if req.Scope == "domain" {
		if err := s.PolicyMgmt.SetDomainPolicy(r.Context(), req.Target, req.Mode); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "created"})
		return
	}
	if req.Scope == "default" {
		if err := s.PolicyMgmt.SetDefaultMode(r.Context(), req.Mode); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "created"})
		return
	}
	jsonError(w, "unsupported scope", http.StatusBadRequest)
}

func (s *Server) handlePolicyUpdate(w http.ResponseWriter, r *http.Request) {
	if s.PolicyMgmt == nil {
		jsonError(w, "policy service not available", http.StatusServiceUnavailable)
		return
	}
	// Path: /admin/policies/{id}
	id := strings.TrimPrefix(r.URL.Path, "/admin/policies/")
	id = strings.TrimSuffix(id, "/")
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		jsonError(w, "invalid policy id", http.StatusBadRequest)
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request", http.StatusBadRequest)
		return
	}
	if parts[0] == "domain" {
		if err := s.PolicyMgmt.SetDomainPolicy(r.Context(), parts[1], req.Mode); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "updated"})
		return
	}
	jsonError(w, "unsupported policy type", http.StatusBadRequest)
}

func (s *Server) handlePolicyDelete(w http.ResponseWriter, r *http.Request) {
	if s.PolicyMgmt == nil {
		jsonError(w, "policy service not available", http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/admin/policies/")
	id = strings.TrimSuffix(id, "/")
	parts := strings.SplitN(id, ":", 2)
	if len(parts) != 2 {
		jsonError(w, "invalid policy id", http.StatusBadRequest)
		return
	}
	if parts[0] == "domain" {
		if err := s.PolicyMgmt.DeleteDomainPolicy(r.Context(), parts[1]); err != nil {
			jsonError(w, err.Error(), http.StatusBadRequest)
			return
		}
		jsonOK(w, map[string]string{"status": "deleted"})
		return
	}
	jsonError(w, "unsupported policy type", http.StatusBadRequest)
}

// ── DNS Handlers ───────────────────────────────────────────

func (s *Server) handleDomainDNS(w http.ResponseWriter, r *http.Request) {
	if s.DNSVerify == nil {
		jsonError(w, "DNS verification not available", http.StatusServiceUnavailable)
		return
	}
	// Path: /admin/domains/dns/{id}
	id, err := parsePathID(r.URL.Path, "/admin/domains/dns/")
	if err != nil {
		jsonError(w, "invalid domain id", http.StatusBadRequest)
		return
	}
	if s.DomainRegistry == nil {
		jsonError(w, "domain registry not available", http.StatusServiceUnavailable)
		return
	}
	domain, err := s.DomainRegistry.GetDomain(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if domain == nil || domain.Name == "" {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	report := s.DNSVerify.GenerateReport(domain.Name)
	jsonOK(w, report)
}

// ── Domain Handlers ─────────────────────────────────────────

func (s *Server) handleDomainList(w http.ResponseWriter, r *http.Request) {
	if s.DomainRegistry == nil {
		jsonError(w, "domain registry not available", http.StatusServiceUnavailable)
		return
	}
	domains, err := s.DomainRegistry.ListDomains(r.Context())
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if domains == nil {
		domains = []domainregistry.Domain{}
	}
	jsonOK(w, map[string]interface{}{"domains": domains})
}

func (s *Server) handleDomainGet(w http.ResponseWriter, r *http.Request) {
	if s.DomainRegistry == nil {
		jsonError(w, "domain registry not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/domains/")
	if err != nil {
		jsonError(w, "invalid domain id", http.StatusBadRequest)
		return
	}
	d, err := s.DomainRegistry.GetDomain(r.Context(), id)
	if err != nil {
		jsonError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if d == nil {
		jsonError(w, "domain not found", http.StatusNotFound)
		return
	}
	jsonOK(w, d)
}

func (s *Server) handleDomainCreate(w http.ResponseWriter, r *http.Request) {
	if s.DomainRegistry == nil {
		jsonError(w, "domain registry not available", http.StatusServiceUnavailable)
		return
	}
	var req domainregistry.CreateDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	d, err := s.DomainRegistry.CreateDomain(r.Context(), &req)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, d)
}

func (s *Server) handleDomainUpdate(w http.ResponseWriter, r *http.Request) {
	if s.DomainRegistry == nil {
		jsonError(w, "domain registry not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/domains/")
	if err != nil {
		jsonError(w, "invalid domain id", http.StatusBadRequest)
		return
	}
	var req domainregistry.UpdateDomainRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	d, err := s.DomainRegistry.UpdateDomain(r.Context(), id, &req)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, d)
}

func (s *Server) handleDomainDelete(w http.ResponseWriter, r *http.Request) {
	if s.DomainRegistry == nil {
		jsonError(w, "domain registry not available", http.StatusServiceUnavailable)
		return
	}
	id, err := parsePathID(r.URL.Path, "/admin/domains/")
	if err != nil {
		jsonError(w, "invalid domain id", http.StatusBadRequest)
		return
	}
	if err := s.DomainRegistry.DeleteDomain(r.Context(), id); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	jsonOK(w, map[string]string{"status": "deleted"})
}

func parseUint64(s string) (uint, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	return uint(n), err
}

func parsePathID(path, prefix string) (uint, error) {
	idStr := strings.TrimPrefix(path, prefix)
	idStr = strings.TrimSuffix(idStr, "/")
	if idStr == "" {
		return 0, fmt.Errorf("empty id")
	}
	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid id: %s", idStr)
	}
	return uint(id), nil
}

func (s *Server) getSMTPStatus() string      { return s.getHealthFor("smtp_receive") }
func (s *Server) getIMAPStatus() string      { return s.getHealthFor("imap") }
func (s *Server) getPOP3Status() string      { return s.getHealthFor("pop3") }
func (s *Server) getJMAPStatus() string      { return s.getHealthFor("jmap") }
func (s *Server) getQueueStatus() string     { return s.getHealthFor("queue") }
func (s *Server) getDatabaseStatus() string  { return s.getHealthFor("database") }
func (s *Server) getMailStoreStatus() string { return s.getHealthFor("mailstore") }
func (s *Server) getTrustStatus() string     { return s.getHealthFor("trust") }
func (s *Server) getPolicyStatus() string    { return s.getHealthFor("policy") }
