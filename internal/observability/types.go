package observability

// ── Event Types ──────────────────────────────────────────────

type EventType string

const (
	// SMTP receive.
	EventSMTPAccepted EventType = "smtp.accepted"
	EventSMTPRejected EventType = "smtp.rejected"

	// SMTP auth.
	EventSMTPAuthSuccess EventType = "smtp.auth.success"
	EventSMTPAuthFailure EventType = "smtp.auth.failure"

	// STARTTLS / TLS.
	EventSTARTTLSSuccess EventType = "smtp.starttls.success"
	EventSTARTTLSFailure EventType = "smtp.starttls.failure"
	EventTLSFailure      EventType = "smtp.tls.failure"

	// Auth results.
	EventSPFPass     EventType = "spf.pass"
	EventSPFFail     EventType = "spf.fail"
	EventSPFNone     EventType = "spf.none"
	EventSPFTempError EventType = "spf.temperror"

	EventDKIMSignSuccess EventType = "dkim.sign.success"
	EventDKIMSignSkipped EventType = "dkim.sign.skipped"
	EventDKIMSignFailure EventType = "dkim.sign.failure"

	EventDMARCPass EventType = "dmarc.pass"
	EventDMARCFail EventType = "dmarc.fail"
	EventDMARCNone EventType = "dmarc.none"
	EventDMARCTempError EventType = "dmarc.temperror"

	// Spam.
	EventSpamAccepted    EventType = "spam.verdict.accept"
	EventSpamSuspicious  EventType = "spam.verdict.suspicious"
	EventSpamRejected    EventType = "spam.verdict.reject"

	// Queue delivery.
	EventQueueLeased      EventType = "queue.leased"
	EventQueueDelivered   EventType = "queue.delivered"
	EventQueueDeferred    EventType = "queue.deferred"
	EventQueueBounced     EventType = "queue.bounced"
	EventQueueDeadLetter  EventType = "queue.deadletter"

	// IMAP.
	EventIMAPSessionCreated EventType = "imap.session.created"

	// JMAP.
	EventJMAPSessionCreated EventType = "jmap.session.created"
	EventJMAPAuthSuccess    EventType = "jmap.auth.success"
	EventJMAPAuthFailure    EventType = "jmap.auth.failure"
	EventJMAPRequest        EventType = "jmap.request"
	EventJMAPMethodSuccess  EventType = "jmap.method.success"
	EventJMAPMethodFailure  EventType = "jmap.method.failure"
	EventJMAPError          EventType = "jmap.error"

	// Trust.
	EventTrustScoreChanged EventType = "trust.score.changed"
	EventTrustLockout      EventType = "trust.lockout"
	EventTrustRateLimit    EventType = "trust.rate_limit"
	EventTrustAbuseDetected EventType = "trust.abuse.detected"

	// Policy.
	EventPolicyAllowed  EventType = "policy.allowed"
	EventPolicyBlocked  EventType = "policy.blocked"
	EventPolicyOverride EventType = "policy.override"

	// Rules engine runner — emitted by the SMTP receiver
	// after the rules engine has evaluated one inbound
	// message. The Outcome field separates pass-through
	// (no rule matched), action (forward / vacation /
	// move / flag fired), skip (matched but suppressed —
	// loop marker, rate limit, Auto-Submitted, etc.) and
	// error (runner panic / DB failure / move failure).
	EventRulesRunnerPass    EventType = "rules.runner.passthrough"
	EventRulesRunnerAction  EventType = "rules.runner.action"
	EventRulesRunnerSkip    EventType = "rules.runner.skip"
	EventRulesRunnerError   EventType = "rules.runner.error"
	EventIMAPSessionClosed  EventType = "imap.session.closed"
	EventIMAPLoginSuccess   EventType = "imap.login.success"
	EventIMAPLoginFailure   EventType = "imap.login.failure"
	EventIMAPMailboxSelected EventType = "imap.mailbox.selected"
)

// LogEvent is a structured log entry.
type LogEvent struct {
	Type      EventType
	Fields    map[string]string
	Timestamp int64 // unix nanos
}

// ── Metrics ──────────────────────────────────────────────────

// Metrics holds all operational counters for the CoreMail engine.
type Metrics struct {
	// SMTP.
	SMTPAccepted int64 `json:"smtp_accepted"`
	SMTPRejected int64 `json:"smtp_rejected"`
	SMTPSessions int64 `json:"smtp_sessions"`

	// Auth.
	AuthSuccess int64 `json:"auth_success"`
	AuthFailure int64 `json:"auth_failure"`

	// TLS.
	TLSUpgrades int64 `json:"tls_upgrades"`

	// SPF.
	SPFPass      int64 `json:"spf_pass"`
	SPFFail      int64 `json:"spf_fail"`
	SPFNone      int64 `json:"spf_none"`
	SPFTempError int64 `json:"spf_temperror"`

	// DKIM.
	DKIMSigned  int64 `json:"dkim_signed"`
	DKIMSkipped int64 `json:"dkim_skipped"`
	DKIMFailed  int64 `json:"dkim_failed"`

	// DMARC.
	DMARCPass int64 `json:"dmarc_pass"`
	DMARCFail int64 `json:"dmarc_fail"`
	DMARCNone int64 `json:"dmarc_none"`
	DMARCTempError int64 `json:"dmarc_temperror"`

	// Spam verdicts.
	SpamAccepted   int64 `json:"spam_accepted"`
	SpamSuspicious int64 `json:"spam_suspicious"`
	SpamRejected   int64 `json:"spam_rejected"`

	// Queue delivery.
	QueueDelivered int64 `json:"queue_delivered"`
	QueueDeferred  int64 `json:"queue_deferred"`
	QueueBounced   int64 `json:"queue_bounced"`
	QueueDeadLetter int64 `json:"queue_deadletter"`

	// Delivery latency in milliseconds (cumulative for averaging).
	DeliveryLatencyTotal int64 `json:"delivery_latency_total_ms"`
	DeliveryCount        int64 `json:"delivery_count"`

	// IMAP.
	IMAPSessionsCreated int64 `json:"imap_sessions_created"`
	IMAPLoginSuccess    int64 `json:"imap_login_success"`
	IMAPLoginFailure    int64 `json:"imap_login_failure"`
	IMAPMailboxSelected int64 `json:"imap_mailbox_selected"`

	// POP3.
	POP3Sessions         int64 `json:"pop3_sessions"`
	POP3LoginSuccess     int64 `json:"pop3_login_success"`
	POP3LoginFailure     int64 `json:"pop3_login_failure"`
	POP3MessagesRetrieved int64 `json:"pop3_messages_retrieved"`
	POP3MessagesDeleted   int64 `json:"pop3_messages_deleted"`

	// Trust.
	TrustLockouts       int64 `json:"trust_lockouts"`
	TrustRateLimits     int64 `json:"trust_rate_limits"`
	TrustAbuseDetections int64 `json:"trust_abuse_detections"`

	// Policy.
	PolicyAllowed  int64 `json:"policy_allowed"`
	PolicyBlocked  int64 `json:"policy_blocked"`
	PolicyOverrides int64 `json:"policy_overrides"`

	// JMAP.
	JMAPRequests       int64 `json:"jmap_requests"`
	JMAPMailboxQueries int64 `json:"jmap_mailbox_queries"`
	JMAPMailboxChanges int64 `json:"jmap_mailbox_changes"`
	JMAPEmailChanges   int64 `json:"jmap_email_changes"`
	JMAPEmailSet       int64 `json:"jmap_email_set"`
	JMAPEmailUpdated   int64 `json:"jmap_email_updated"`
	JMAPEmailDestroyed int64 `json:"jmap_email_destroyed"`
	JMAPSubmissions      int64 `json:"jmap_submissions"`
	JMAPSubmissionQueued int64 `json:"jmap_submission_queued"`
	JMAPSubmissionFailed int64 `json:"jmap_submission_failed"`
	JMAPAuthSuccess   int64 `json:"jmap_auth_success"`
	JMAPAuthFailure   int64 `json:"jmap_auth_failure"`
	JMAPMethodSuccess int64 `json:"jmap_method_success"`
	JMAPMethodFailure int64 `json:"jmap_method_failure"`
	JMAPErrors        int64 `json:"jmap_errors"`

	// Backup.
	BackupsCreated  int64 `json:"backups_created"`
	BackupsFailed   int64 `json:"backups_failed"`
	BackupsVerified int64 `json:"backups_verified"`
	BackupsRestored int64 `json:"backups_restored"`
	BackupBytes     int64 `json:"backup_bytes"`

	// Licensing.
	LicenseValid            int64 `json:"license_valid"`
	LicenseInvalid          int64 `json:"license_invalid"`
	LicenseExpired          int64 `json:"license_expired"`
	LicenseValidationErrors int64 `json:"license_validation_errors"`
	LicenseDomainChecks     int64 `json:"license_domain_checks"`
	LicenseMailboxChecks    int64 `json:"license_mailbox_checks"`
	LicenseBlocks           int64 `json:"license_blocks"`

	// TLS.
	TLSCertificates      int64 `json:"tls_certificates"`
	TLSExpiredCerts      int64 `json:"tls_expired_certificates"`
	TLSWarningCerts      int64 `json:"tls_warning_certificates"`
	TLSReloads           int64 `json:"tls_reloads"`
	TLSReloadFailures    int64 `json:"tls_reload_failures"`
}

// ── Health ───────────────────────────────────────────────────

// HealthStatus represents the readiness of a subsystem.
type HealthStatus int

const (
	HealthUnknown HealthStatus = iota
	HealthReady
	HealthNotReady
	HealthDegraded
)

func (h HealthStatus) String() string {
	switch h {
	case HealthReady:
		return "ready"
	case HealthNotReady:
		return "not_ready"
	case HealthDegraded:
		return "degraded"
	default:
		return "unknown"
	}
}

// HealthReport contains the overall system health.
type HealthReport struct {
	Overall  HealthStatus           `json:"overall"`
	Checks   map[string]HealthCheck `json:"checks"`
}

// HealthCheck is the result of a single subsystem health check.
type HealthCheck struct {
	Status  HealthStatus `json:"status"`
	Message string       `json:"message,omitempty"`
}

// ── Diagnostic Snapshot ──────────────────────────────────────

// EventEntry is a single recorded event in the diagnostic history.
type EventEntry struct {
	Type      EventType         `json:"type"`
	Fields    map[string]string `json:"fields"`
	Timestamp int64             `json:"timestamp"`
}

// DiagnosticSnapshot is a point-in-time view of system state.
type DiagnosticSnapshot struct {
	Metrics           Metrics            `json:"metrics"`
	RecentEvents      []EventEntry       `json:"recent_events"`
	Health            *HealthReport      `json:"health"`
	QueueSummary      *QueueSummary      `json:"queue_summary,omitempty"`
	RecentFailures    []EventEntry       `json:"recent_failures"`
	RecentAuthFailures []EventEntry      `json:"recent_auth_failures"`
	RecentSpamRejects []EventEntry      `json:"recent_spam_rejects"`
}

// QueueSummary is a high-level view of queue state.
type QueueSummary struct {
	Pending    int `json:"pending"`
	Delivered  int `json:"delivered"`
	Deferred   int `json:"deferred"`
	Bounced    int `json:"bounced"`
	DeadLetter int `json:"dead_letter"`
}
