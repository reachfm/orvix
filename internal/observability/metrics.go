package observability

import (
	"sync/atomic"
)

// MetricsCollector provides concurrency-safe metric counters.
type MetricsCollector struct {
	m Metrics
}

// NewMetricsCollector creates a new metrics collector.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{}
}

// Snapshot returns a consistent point-in-time copy of all metrics.
func (c *MetricsCollector) Snapshot() Metrics {
	return Metrics{
		SMTPAccepted: atomic.LoadInt64(&c.m.SMTPAccepted),
		SMTPRejected: atomic.LoadInt64(&c.m.SMTPRejected),
		SMTPSessions: atomic.LoadInt64(&c.m.SMTPSessions),

		AuthSuccess: atomic.LoadInt64(&c.m.AuthSuccess),
		AuthFailure: atomic.LoadInt64(&c.m.AuthFailure),

		TLSUpgrades: atomic.LoadInt64(&c.m.TLSUpgrades),

		SPFPass:      atomic.LoadInt64(&c.m.SPFPass),
		SPFFail:      atomic.LoadInt64(&c.m.SPFFail),
		SPFNone:      atomic.LoadInt64(&c.m.SPFNone),
		SPFTempError: atomic.LoadInt64(&c.m.SPFTempError),

		DKIMSigned:  atomic.LoadInt64(&c.m.DKIMSigned),
		DKIMSkipped: atomic.LoadInt64(&c.m.DKIMSkipped),
		DKIMFailed:  atomic.LoadInt64(&c.m.DKIMFailed),

		DMARCPass: atomic.LoadInt64(&c.m.DMARCPass),
		DMARCFail: atomic.LoadInt64(&c.m.DMARCFail),
		DMARCNone: atomic.LoadInt64(&c.m.DMARCNone),
		DMARCTempError: atomic.LoadInt64(&c.m.DMARCTempError),

		SpamAccepted:   atomic.LoadInt64(&c.m.SpamAccepted),
		SpamSuspicious: atomic.LoadInt64(&c.m.SpamSuspicious),
		SpamRejected:   atomic.LoadInt64(&c.m.SpamRejected),

		QueueDelivered:     atomic.LoadInt64(&c.m.QueueDelivered),
		QueueDeferred:      atomic.LoadInt64(&c.m.QueueDeferred),
		QueueBounced:       atomic.LoadInt64(&c.m.QueueBounced),
		QueueDeadLetter:     atomic.LoadInt64(&c.m.QueueDeadLetter),
		DeliveryLatencyTotal: atomic.LoadInt64(&c.m.DeliveryLatencyTotal),
		DeliveryCount:       atomic.LoadInt64(&c.m.DeliveryCount),

		IMAPSessionsCreated: atomic.LoadInt64(&c.m.IMAPSessionsCreated),
		IMAPLoginSuccess:    atomic.LoadInt64(&c.m.IMAPLoginSuccess),
		IMAPLoginFailure:    atomic.LoadInt64(&c.m.IMAPLoginFailure),
		IMAPMailboxSelected: atomic.LoadInt64(&c.m.IMAPMailboxSelected),

		POP3Sessions:          atomic.LoadInt64(&c.m.POP3Sessions),
		POP3LoginSuccess:      atomic.LoadInt64(&c.m.POP3LoginSuccess),
		POP3LoginFailure:      atomic.LoadInt64(&c.m.POP3LoginFailure),
		POP3MessagesRetrieved: atomic.LoadInt64(&c.m.POP3MessagesRetrieved),
		POP3MessagesDeleted:   atomic.LoadInt64(&c.m.POP3MessagesDeleted),

		TrustLockouts:        atomic.LoadInt64(&c.m.TrustLockouts),
		TrustRateLimits:      atomic.LoadInt64(&c.m.TrustRateLimits),
		TrustAbuseDetections: atomic.LoadInt64(&c.m.TrustAbuseDetections),

		PolicyAllowed:  atomic.LoadInt64(&c.m.PolicyAllowed),
		PolicyBlocked:  atomic.LoadInt64(&c.m.PolicyBlocked),
		PolicyOverrides: atomic.LoadInt64(&c.m.PolicyOverrides),

		AntivirusScanned:        atomic.LoadInt64(&c.m.AntivirusScanned),
		AntivirusInfected:       atomic.LoadInt64(&c.m.AntivirusInfected),
		AntivirusRejected:       atomic.LoadInt64(&c.m.AntivirusRejected),
		AntivirusQuarantined:    atomic.LoadInt64(&c.m.AntivirusQuarantined),
		AntivirusTagged:         atomic.LoadInt64(&c.m.AntivirusTagged),
		AntivirusScannerErrors:  atomic.LoadInt64(&c.m.AntivirusScannerErrors),
		AntivirusFailOpen:       atomic.LoadInt64(&c.m.AntivirusFailOpen),
		AntivirusFailClosed:     atomic.LoadInt64(&c.m.AntivirusFailClosed),

		BackupTargetUploadsAttempt: atomic.LoadInt64(&c.m.BackupTargetUploadsAttempt),
		BackupTargetUploadsSuccess: atomic.LoadInt64(&c.m.BackupTargetUploadsSuccess),
		BackupTargetUploadsFailure: atomic.LoadInt64(&c.m.BackupTargetUploadsFailure),

		JMAPRequests:      atomic.LoadInt64(&c.m.JMAPRequests),
		JMAPAuthSuccess:   atomic.LoadInt64(&c.m.JMAPAuthSuccess),
		JMAPAuthFailure:   atomic.LoadInt64(&c.m.JMAPAuthFailure),
		JMAPMethodSuccess: atomic.LoadInt64(&c.m.JMAPMethodSuccess),
		JMAPMethodFailure: atomic.LoadInt64(&c.m.JMAPMethodFailure),
		JMAPErrors:          atomic.LoadInt64(&c.m.JMAPErrors),
		JMAPMailboxQueries:  atomic.LoadInt64(&c.m.JMAPMailboxQueries),
		JMAPMailboxChanges:  atomic.LoadInt64(&c.m.JMAPMailboxChanges),
		JMAPEmailChanges:    atomic.LoadInt64(&c.m.JMAPEmailChanges),
		JMAPEmailSet:        atomic.LoadInt64(&c.m.JMAPEmailSet),
		JMAPEmailUpdated:    atomic.LoadInt64(&c.m.JMAPEmailUpdated),
		JMAPEmailDestroyed:  atomic.LoadInt64(&c.m.JMAPEmailDestroyed),
		JMAPSubmissions:      atomic.LoadInt64(&c.m.JMAPSubmissions),
		JMAPSubmissionQueued: atomic.LoadInt64(&c.m.JMAPSubmissionQueued),
		JMAPSubmissionFailed: atomic.LoadInt64(&c.m.JMAPSubmissionFailed),
	}
}

func (c *MetricsCollector) IncSMTPAccepted()     { atomic.AddInt64(&c.m.SMTPAccepted, 1) }
func (c *MetricsCollector) IncSMTPRejected()     { atomic.AddInt64(&c.m.SMTPRejected, 1) }
func (c *MetricsCollector) IncSMTPSessions()     { atomic.AddInt64(&c.m.SMTPSessions, 1) }
func (c *MetricsCollector) IncAuthSuccess()      { atomic.AddInt64(&c.m.AuthSuccess, 1) }
func (c *MetricsCollector) IncAuthFailure()      { atomic.AddInt64(&c.m.AuthFailure, 1) }
func (c *MetricsCollector) IncTLSUpgrade()       { atomic.AddInt64(&c.m.TLSUpgrades, 1) }
func (c *MetricsCollector) IncSPFPass()          { atomic.AddInt64(&c.m.SPFPass, 1) }
func (c *MetricsCollector) IncSPFFail()          { atomic.AddInt64(&c.m.SPFFail, 1) }
func (c *MetricsCollector) IncSPFNone()          { atomic.AddInt64(&c.m.SPFNone, 1) }
func (c *MetricsCollector) IncSPFTempError()     { atomic.AddInt64(&c.m.SPFTempError, 1) }
func (c *MetricsCollector) IncDKIMSigned()       { atomic.AddInt64(&c.m.DKIMSigned, 1) }
func (c *MetricsCollector) IncDKIMSkipped()      { atomic.AddInt64(&c.m.DKIMSkipped, 1) }
func (c *MetricsCollector) IncDKIMFailed()       { atomic.AddInt64(&c.m.DKIMFailed, 1) }
func (c *MetricsCollector) IncDMARCPass()        { atomic.AddInt64(&c.m.DMARCPass, 1) }
func (c *MetricsCollector) IncDMARCFail()        { atomic.AddInt64(&c.m.DMARCFail, 1) }
func (c *MetricsCollector) IncDMARCNone()        { atomic.AddInt64(&c.m.DMARCNone, 1) }
func (c *MetricsCollector) IncDMARCTempError()   { atomic.AddInt64(&c.m.DMARCTempError, 1) }
func (c *MetricsCollector) IncSpamAccepted()     { atomic.AddInt64(&c.m.SpamAccepted, 1) }
func (c *MetricsCollector) IncSpamSuspicious()   { atomic.AddInt64(&c.m.SpamSuspicious, 1) }
func (c *MetricsCollector) IncSpamRejected()     { atomic.AddInt64(&c.m.SpamRejected, 1) }
func (c *MetricsCollector) IncQueueDelivered()   { atomic.AddInt64(&c.m.QueueDelivered, 1) }
func (c *MetricsCollector) IncQueueDeferred()    { atomic.AddInt64(&c.m.QueueDeferred, 1) }
func (c *MetricsCollector) IncQueueBounced()     { atomic.AddInt64(&c.m.QueueBounced, 1) }
func (c *MetricsCollector) IncQueueDeadLetter()  { atomic.AddInt64(&c.m.QueueDeadLetter, 1) }
func (c *MetricsCollector) AddDeliveryLatency(ms int64) {
	atomic.AddInt64(&c.m.DeliveryLatencyTotal, ms)
	atomic.AddInt64(&c.m.DeliveryCount, 1)
}

func (c *MetricsCollector) IncIMAPSessionCreated()  { atomic.AddInt64(&c.m.IMAPSessionsCreated, 1) }
func (c *MetricsCollector) IncIMAPLoginSuccess()    { atomic.AddInt64(&c.m.IMAPLoginSuccess, 1) }
func (c *MetricsCollector) IncIMAPLoginFailure()    { atomic.AddInt64(&c.m.IMAPLoginFailure, 1) }
func (c *MetricsCollector) IncIMAPMailboxSelected() { atomic.AddInt64(&c.m.IMAPMailboxSelected, 1) }

func (c *MetricsCollector) IncPOP3Session()          { atomic.AddInt64(&c.m.POP3Sessions, 1) }
func (c *MetricsCollector) IncPOP3LoginSuccess()     { atomic.AddInt64(&c.m.POP3LoginSuccess, 1) }
func (c *MetricsCollector) IncPOP3LoginFailure()     { atomic.AddInt64(&c.m.POP3LoginFailure, 1) }
func (c *MetricsCollector) IncPOP3MessageRetrieved() { atomic.AddInt64(&c.m.POP3MessagesRetrieved, 1) }
func (c *MetricsCollector) IncPOP3MessageDeleted()   { atomic.AddInt64(&c.m.POP3MessagesDeleted, 1) }

func (c *MetricsCollector) IncTrustLockout()         { atomic.AddInt64(&c.m.TrustLockouts, 1) }
func (c *MetricsCollector) IncTrustRateLimit()       { atomic.AddInt64(&c.m.TrustRateLimits, 1) }
func (c *MetricsCollector) IncTrustAbuseDetection()  { atomic.AddInt64(&c.m.TrustAbuseDetections, 1) }

func (c *MetricsCollector) IncJMAPRequest()      { atomic.AddInt64(&c.m.JMAPRequests, 1) }
func (c *MetricsCollector) IncJMAPAuthSuccess()   { atomic.AddInt64(&c.m.JMAPAuthSuccess, 1) }
func (c *MetricsCollector) IncJMAPAuthFailure()   { atomic.AddInt64(&c.m.JMAPAuthFailure, 1) }
func (c *MetricsCollector) IncJMAPMethodSuccess() { atomic.AddInt64(&c.m.JMAPMethodSuccess, 1) }
func (c *MetricsCollector) IncJMAPMethodFailure() { atomic.AddInt64(&c.m.JMAPMethodFailure, 1) }
func (c *MetricsCollector) IncJMAPError()           { atomic.AddInt64(&c.m.JMAPErrors, 1) }

// Backup metrics.
func (c *MetricsCollector) IncBackupsCreated()  { atomic.AddInt64(&c.m.BackupsCreated, 1) }
func (c *MetricsCollector) IncBackupsFailed()   { atomic.AddInt64(&c.m.BackupsFailed, 1) }
func (c *MetricsCollector) IncBackupsVerified() { atomic.AddInt64(&c.m.BackupsVerified, 1) }
func (c *MetricsCollector) IncBackupsRestored() { atomic.AddInt64(&c.m.BackupsRestored, 1) }
func (c *MetricsCollector) AddBackupBytes(n int64) { atomic.AddInt64(&c.m.BackupBytes, n) }

// Licensing metrics.
func (c *MetricsCollector) IncLicenseValid()              { atomic.AddInt64(&c.m.LicenseValid, 1) }
func (c *MetricsCollector) IncLicenseInvalid()            { atomic.AddInt64(&c.m.LicenseInvalid, 1) }
func (c *MetricsCollector) IncLicenseExpired()            { atomic.AddInt64(&c.m.LicenseExpired, 1) }
func (c *MetricsCollector) IncLicenseValidationErrors()   { atomic.AddInt64(&c.m.LicenseValidationErrors, 1) }
func (c *MetricsCollector) IncLicenseDomainChecks()       { atomic.AddInt64(&c.m.LicenseDomainChecks, 1) }
func (c *MetricsCollector) IncLicenseMailboxChecks()      { atomic.AddInt64(&c.m.LicenseMailboxChecks, 1) }
func (c *MetricsCollector) IncLicenseBlocks()             { atomic.AddInt64(&c.m.LicenseBlocks, 1) }

// TLS metrics.
func (c *MetricsCollector) IncTLSCertificates()    { atomic.AddInt64(&c.m.TLSCertificates, 1) }
func (c *MetricsCollector) IncTLSExpiredCerts()    { atomic.AddInt64(&c.m.TLSExpiredCerts, 1) }
func (c *MetricsCollector) IncTLSWarningCerts()    { atomic.AddInt64(&c.m.TLSWarningCerts, 1) }
func (c *MetricsCollector) IncTLSReloads()         { atomic.AddInt64(&c.m.TLSReloads, 1) }
func (c *MetricsCollector) IncTLSReloadFailures()  { atomic.AddInt64(&c.m.TLSReloadFailures, 1) }

// Antivirus metrics. Every increment bumps both the per-call
// counter AND is recorded by observability.EventHistory by the
// caller (the antivirus engine). The counters are kept here so
// the admin status endpoint can answer runtime questions
// without forcing the engine to maintain its own stats.
func (c *MetricsCollector) IncAntivirusScanned()        { atomic.AddInt64(&c.m.AntivirusScanned, 1) }
func (c *MetricsCollector) IncAntivirusInfected()      { atomic.AddInt64(&c.m.AntivirusInfected, 1) }
func (c *MetricsCollector) IncAntivirusRejected()      { atomic.AddInt64(&c.m.AntivirusRejected, 1) }
func (c *MetricsCollector) IncAntivirusQuarantined()   { atomic.AddInt64(&c.m.AntivirusQuarantined, 1) }
func (c *MetricsCollector) IncAntivirusTagged()        { atomic.AddInt64(&c.m.AntivirusTagged, 1) }
func (c *MetricsCollector) IncAntivirusScannerErrors() { atomic.AddInt64(&c.m.AntivirusScannerErrors, 1) }
func (c *MetricsCollector) IncAntivirusFailOpen()      { atomic.AddInt64(&c.m.AntivirusFailOpen, 1) }
func (c *MetricsCollector) IncAntivirusFailClosed()    { atomic.AddInt64(&c.m.AntivirusFailClosed, 1) }

// Backup target upload metrics.
func (c *MetricsCollector) IncBackupTargetUploadAttempts() { atomic.AddInt64(&c.m.BackupTargetUploadsAttempt, 1) }
func (c *MetricsCollector) IncBackupTargetUploadSuccess()  { atomic.AddInt64(&c.m.BackupTargetUploadsSuccess, 1) }
func (c *MetricsCollector) IncBackupTargetUploadFailures() { atomic.AddInt64(&c.m.BackupTargetUploadsFailure, 1) }
func (c *MetricsCollector) IncJMAPMailboxQuery()    { atomic.AddInt64(&c.m.JMAPMailboxQueries, 1) }
func (c *MetricsCollector) IncJMAPMailboxChanges()  { atomic.AddInt64(&c.m.JMAPMailboxChanges, 1) }
func (c *MetricsCollector) IncJMAPEmailChanges()    { atomic.AddInt64(&c.m.JMAPEmailChanges, 1) }
func (c *MetricsCollector) IncJMAPEmailSet()       { atomic.AddInt64(&c.m.JMAPEmailSet, 1) }
func (c *MetricsCollector) IncJMAPEmailUpdated()   { atomic.AddInt64(&c.m.JMAPEmailUpdated, 1) }
func (c *MetricsCollector) IncJMAPEmailDestroyed() { atomic.AddInt64(&c.m.JMAPEmailDestroyed, 1) }
func (c *MetricsCollector) IncJMAPSubmission()     { atomic.AddInt64(&c.m.JMAPSubmissions, 1) }
func (c *MetricsCollector) IncJMAPSubmissionQueued()  { atomic.AddInt64(&c.m.JMAPSubmissionQueued, 1) }
func (c *MetricsCollector) IncJMAPSubmissionFailed()  { atomic.AddInt64(&c.m.JMAPSubmissionFailed, 1) }

func (c *MetricsCollector) IncPolicyAllowed()  { atomic.AddInt64(&c.m.PolicyAllowed, 1) }
func (c *MetricsCollector) IncPolicyBlocked()  { atomic.AddInt64(&c.m.PolicyBlocked, 1) }
func (c *MetricsCollector) IncPolicyOverride() { atomic.AddInt64(&c.m.PolicyOverrides, 1) }

func (c *MetricsCollector) RecordSPFResult(result string) {
	switch result {
	case "pass":
		c.IncSPFPass()
	case "fail":
		c.IncSPFFail()
	case "none", "neutral", "softfail":
		c.IncSPFNone()
	case "temperror":
		c.IncSPFTempError()
	}
}

func (c *MetricsCollector) RecordDMARCResult(result string) {
	switch result {
	case "pass":
		c.IncDMARCPass()
	case "fail":
		c.IncDMARCFail()
	case "temperror":
		c.IncDMARCTempError()
	default:
		c.IncDMARCNone()
	}
}

func (c *MetricsCollector) RecordSpamVerdict(verdict string) {
	switch verdict {
	case "reject":
		c.IncSpamRejected()
	case "suspicious":
		c.IncSpamSuspicious()
	default:
		c.IncSpamAccepted()
	}
}
