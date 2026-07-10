package monitoring

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/orvix/orvix/internal/dbdialect"
)

// DataSources provides access to all subsystems for monitoring.
//
// All callbacks are optional (may be nil). When nil, the corresponding
// collector is skipped silently. Callbacks MUST NOT panic and MUST NOT
// leak secret material; they are called from the request goroutine and
// any value they return is reflected verbatim in the Health response.
type DataSources struct {
	// Existing fields (kept for backward compatibility with origin/main).
	DB               *sql.DB
	QueuePending     func() (int64, error)
	QueueDeadLetter  func() (int64, error) // NEW
	TLSCerts         func() (expiring30, expiring7 int, err error)
	LatestBackup     func() (time.Time, error)
	DomainCount      func() (int, error)
	MailboxCount     func() (int64, error)
	MessageCount     func() (int64, error)
	AttachmentCount  func() (int64, error)
	StorageBytes     func() (int64, error)
	SMTPHealthy      func() bool
	IMAPHealthy      func() bool
	POP3Healthy      func() bool
	JMAPHealthy      func() bool
	DatabaseHealthy  func() bool
	MailStoreHealthy func() bool
	BackupCount      func() (int, error)

	// NEW fields for Monitoring v1.
	BackupDir            string  // absolute path to the backup dir (used for writability + disk-usage label)
	BackupDirWritable    func() bool  // explicit writability check; if nil, the service computes one
	DatabaseSize         func() (int64, error) // explicit DB size; if nil, computed from the live DB
	ServiceStartedAt     time.Time // process start time, used for uptime
	APIPing              func() error // self-ping for admin API health; nil = unknown
	DiskPathLabels       map[string]string // map absolute path -> safe label (e.g. cfg.Backup.Dir -> "backup")
	MemoryUsage          func() (usedBytes, totalBytes int64) // explicit memory; if nil, computed from runtime.MemStats
	CPULoad              func() (load1, load5, load15 float64, err error) // explicit load; if nil, computed on POSIX only
	DNSHealthy           func() bool // DNS resolver health; nil = unknown

	// Alert thresholds — if nil, sensible defaults are used.
	Thresholds *AlertThresholds
}

// Service provides monitoring and alerting.
type Service struct {
	db      *sql.DB
	dialect *dbdialect.Info
	src     *DataSources

	// startTime is captured at NewService so the uptime is meaningful
	// even when the caller forgets to set DataSources.ServiceStartedAt.
	startTime time.Time

	// alertMu serializes EvaluateAlerts / saveAlert / resolveAll so two
	// concurrent evaluations cannot interleave alert rows.
	alertMu sync.Mutex

	// dispatcher fans newly-raised alerts out to the configured
	// delivery providers (in-app, webhook, …). Optional; when nil,
	// alerts are still persisted and listed, they are just not pushed
	// to external channels.
	dispatcher *Dispatcher
}

// SetDispatcher attaches an alert delivery dispatcher. Safe to call
// with nil to disable external delivery.
func (s *Service) SetDispatcher(d *Dispatcher) {
	s.dispatcher = d
}

// Dispatcher returns the configured delivery dispatcher (may be nil).
func (s *Service) Dispatcher() *Dispatcher {
	return s.dispatcher
}

// NewService creates a monitoring service.
func NewService(db *sql.DB, src *DataSources) *Service {
	if src == nil {
		src = &DataSources{}
	}
	dialect, err := dbdialect.Detect(db)
	if err != nil {
		dialect = dbdialect.FromDriver("sqlite")
	}
	return &Service{
		db:        db,
		dialect:   dialect,
		src:       src,
		startTime: time.Now().UTC(),
	}
}

// EnsureSchema creates required tables.
func (s *Service) EnsureSchema(ctx context.Context) error {
	// PostgreSQL schema is created by models.MigrateAllPostgres.
	if s.dialect.IsPostgres() {
		return nil
	}
	for _, stmt := range schema {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// ── Health (NEW for Monitoring v1) ──────────────────────

// GetHealth returns the full Health payload for /monitoring/health.
//
// Security: Disk paths in the response are mapped to safe labels via
// DataSources.DiskPathLabels (or fall back to the basename). No env
// values, no file contents, no tokens, no private absolute paths.
func (s *Service) GetHealth(ctx context.Context) *Health {
	h := &Health{
		Status:        "ok",
		UptimeSeconds: int64(time.Since(s.uptimeFrom()).Seconds()),
		GeneratedAt:   time.Now().UTC(),
		Disk:          s.collectDisk(),
		DB:            s.collectDBHealth(ctx),
		Queue:         s.collectQueueHealth(),
		Backup:        s.collectBackupHealth(),
		API:           s.collectAPIHealth(),
		Capacity:      s.collectCapacityShim(ctx),
		OpenAlerts:    0,
	}

	t := s.thresholds()

	if h.DB.Status == "critical" || h.Queue.Status == "critical" || h.Backup.Status == "critical" || h.API.Status == "critical" {
		h.Status = "down"
	} else if h.DB.Status == "warning" || h.Queue.Status == "warning" || h.Backup.Status == "warning" || h.API.Status == "warning" {
		h.Status = "degraded"
	}
	for _, d := range h.Disk {
		if d.UsedPct >= t.DiskUsageCriticalPct {
			if h.Status == "ok" {
				h.Status = "degraded"
			}
		}
		if d.UsedPct >= t.DiskUsageCriticalPct+5 {
			h.Status = "down"
		}
		if d.UsedPct >= t.DiskUsageWarningPct {
			if h.Status == "ok" {
				h.Status = "degraded"
			}
		}
	}

	if active, err := s.ListActiveAlerts(ctx); err == nil {
		h.OpenAlerts = len(active)
	}
	return h
}

// GetSnapshot returns a comprehensive snapshot of all health indicators
// in a single JSON response. Includes service status, disk usage, queue
// depth, DB health, backup freshness, cert expiry, DNS readiness, and more.
func (s *Service) GetSnapshot(ctx context.Context) *MonitoringSnapshot {
	t := s.thresholds()
	snapshot := &MonitoringSnapshot{
		GeneratedAt:    time.Now().UTC(),
		ServiceStatus:  "ok",
		UptimeSeconds:  int64(time.Since(s.uptimeFrom()).Seconds()),
		Disk:           s.collectDisk(),
		DBHealth:       s.collectDBHealth(ctx),
		QueueHealth:    s.collectQueueHealth(),
		BackupHealth:   s.collectBackupHealth(),
		APIHealth:      s.collectAPIHealth(),
		CertExpiry:     CertExpiryStatus{Status: "ok"},
		DNSReadiness:   ComponentHealth{Status: "unknown", Message: "DNS readiness not configured"},
		Capacity:       s.collectCapacityShim(ctx),
		MemoryUsedBytes: 0,
		MemoryTotalBytes: 0,
	}

	used, total := s.MemoryBytes()
	snapshot.MemoryUsedBytes = used
	snapshot.MemoryTotalBytes = total

	// Cert expiry status.
	if s.src.TLSCerts != nil {
		exp30, exp7, err := s.src.TLSCerts()
		if err == nil {
			snapshot.CertExpiry.ExpiringWithin7 = exp7
			snapshot.CertExpiry.ExpiringWithin30 = exp30
			if exp7 > 0 {
				snapshot.CertExpiry.Status = "critical"
			} else if exp30 > 0 {
				snapshot.CertExpiry.Status = "warning"
			}
		}
	}

	// DNS readiness from configured source.
	if s.src.DNSHealthy != nil {
		if s.src.DNSHealthy() {
			snapshot.DNSReadiness = ComponentHealth{Status: "ok", Message: "DNS resolver responsive"}
		} else {
			snapshot.DNSReadiness = ComponentHealth{Status: "warning", Message: "DNS resolver not responding"}
		}
	}

	// Determine overall service status.
	if snapshot.DBHealth.Status == "critical" || snapshot.QueueHealth.Status == "critical" ||
		snapshot.BackupHealth.Status == "critical" || snapshot.APIHealth.Status == "critical" {
		snapshot.ServiceStatus = "down"
	} else if snapshot.DBHealth.Status == "warning" || snapshot.QueueHealth.Status == "warning" ||
		snapshot.BackupHealth.Status == "warning" || snapshot.APIHealth.Status == "warning" {
		snapshot.ServiceStatus = "degraded"
	}

	for _, d := range snapshot.Disk {
		if d.UsedPct >= t.DiskUsageCriticalPct {
			if snapshot.ServiceStatus == "ok" {
				snapshot.ServiceStatus = "degraded"
			}
		}
		if d.UsedPct >= t.DiskUsageWarningPct {
			if snapshot.ServiceStatus == "ok" {
				snapshot.ServiceStatus = "degraded"
			}
		}
	}

	if snapshot.CertExpiry.Status == "critical" {
		snapshot.ServiceStatus = "degraded"
	}

	if active, err := s.ListActiveAlerts(ctx); err == nil {
		snapshot.OpenAlerts = len(active)
	}

	return snapshot
}

func (s *Service) uptimeFrom() time.Time {
	if !s.src.ServiceStartedAt.IsZero() {
		return s.src.ServiceStartedAt
	}
	return s.startTime
}

func (s *Service) thresholds() AlertThresholds {
	if s.src != nil && s.src.Thresholds != nil {
		t := *s.src.Thresholds
		t.ApplyDefaults()
		return t
	}
	t := AlertThresholds{}
	t.ApplyDefaults()
	return t
}

// collectDisk returns disk usage for the configured safe labels.
//
// We never include the absolute path in the response. If the caller
// supplies a DataSources.DiskPathLabels map, we use the label from the
// map. Otherwise we use the basename of the path, which is still a
// safe label (e.g. "backups", "var-lib-orvix"). No env values, no
// tokens, no file contents.
func (s *Service) collectDisk() []DiskUsage {
	out := make([]DiskUsage, 0, 4)

	// Backup dir (always present when configured).
	if s.src.BackupDir != "" {
		out = append(out, s.diskForSafePath(s.src.BackupDir))
	}
	// Mailstore / data dir: derived from cfg if the caller adds it via
	// DiskPathLabels. We always render the configured set.
	for path, label := range s.src.DiskPathLabels {
		if path == s.src.BackupDir && label == "" {
			continue
		}
		out = append(out, s.diskForSafePathWithLabel(path, label))
	}
	return out
}

func (s *Service) diskForSafePath(path string) DiskUsage {
	label := filepath.Base(path)
	if label == "" || label == "." || label == "/" || label == string(filepath.Separator) {
		label = "system"
	}
	return s.diskForSafePathWithLabel(path, label)
}

func (s *Service) diskForSafePathWithLabel(path, label string) DiskUsage {
	if label == "" {
		label = filepath.Base(path)
	}
	du := DiskUsage{Label: label}
	du = statfsInto(path, du)
	return du
}

func (s *Service) collectDBHealth(ctx context.Context) ComponentHealth {
	if s.src.DatabaseHealthy != nil {
		if s.src.DatabaseHealthy() {
			return ComponentHealth{Status: "ok", Message: "database responsive"}
		}
		return ComponentHealth{Status: "critical", Message: "database health check failed"}
	}
	// Fallback: try a quick ping.
	if s.src.DB != nil {
		if err := s.src.DB.PingContext(ctx); err == nil {
			return ComponentHealth{Status: "ok", Message: "database responsive"}
		} else {
			return ComponentHealth{Status: "critical", Message: "database ping failed"}
		}
	}
	return ComponentHealth{Status: "unknown", Message: "no database source configured"}
}

func (s *Service) collectQueueHealth() ComponentHealth {
	t := s.thresholds()
	if s.src.QueueDeadLetter != nil {
		if n, err := s.src.QueueDeadLetter(); err == nil && n > 0 {
			return ComponentHealth{
				Status:  "critical",
				Message: fmt.Sprintf("%d dead-lettered messages", n),
			}
		}
	}
	if s.src.QueuePending != nil {
		if n, err := s.src.QueuePending(); err == nil {
			switch {
			case n > t.QueueDepthCritical:
				return ComponentHealth{
					Status:  "critical",
					Message: fmt.Sprintf("%d pending messages", n),
				}
			case n > t.QueueDepthWarning:
				return ComponentHealth{
					Status:  "warning",
					Message: fmt.Sprintf("%d pending messages", n),
				}
			}
		}
	}
	return ComponentHealth{Status: "ok", Message: "queue within limits"}
}

func (s *Service) collectBackupHealth() ComponentHealth {
	t := s.thresholds()
	if s.src.BackupDirWritable != nil {
		if !s.src.BackupDirWritable() {
			return ComponentHealth{Status: "critical", Message: "backup directory not writable"}
		}
	} else if s.src.BackupDir != "" {
		probe := filepath.Join(s.src.BackupDir, ".orvix-write-probe")
		f, err := os.Create(probe)
		if err != nil {
			return ComponentHealth{Status: "critical", Message: "backup directory not writable"}
		}
		_ = f.Close()
		_ = os.Remove(probe)
	}
	if s.src.LatestBackup != nil {
		latest, err := s.src.LatestBackup()
		if err == nil {
			hours := time.Since(latest).Hours()
			if hours > t.BackupAgeCriticalHours {
				return ComponentHealth{
					Status:  "critical",
					Message: fmt.Sprintf("no backup in %.0f hours", hours),
				}
			}
			if hours > t.BackupAgeWarningHours {
				return ComponentHealth{
					Status:  "warning",
					Message: fmt.Sprintf("last backup %.0f hours ago", hours),
				}
			}
		}
	}
	return ComponentHealth{Status: "ok", Message: "backup healthy"}
}

func (s *Service) collectAPIHealth() ComponentHealth {
	if s.src.APIPing != nil {
		if err := s.src.APIPing(); err != nil {
			return ComponentHealth{Status: "critical", Message: "admin API self-check failed"}
		}
		return ComponentHealth{Status: "ok", Message: "admin API responsive"}
	}
	// No self-ping configured: report unknown instead of fake-ok.
	return ComponentHealth{Status: "unknown", Message: "admin API self-ping not configured"}
}

// collectCapacityShim wraps GetCapacity so the Health response reuses the
// same shape. The existing GetCapacity remains the canonical entry for
// the legacy /monitoring/capacity endpoint.
func (s *Service) collectCapacityShim(ctx context.Context) Capacity {
	cptr := s.GetCapacity(ctx)
	c := *cptr
	// Merge in dead_letter if a source is configured.
	if s.src.QueueDeadLetter != nil {
		if n, err := s.src.QueueDeadLetter(); err == nil {
			c.QueueDeadLetter = n
		}
	}
	// Merge in DB size if the explicit source is configured.
	if s.src.DatabaseSize != nil {
		if n, err := s.src.DatabaseSize(); err == nil && n > 0 {
			c.DatabaseSize = n
		}
	}
	return c
}

// ── Memory / CPU (NEW) ─────────────────────────────────

// MemoryBytes returns process memory usage in bytes. Always safe to
// call. Returns (0, 0) on platforms where runtime stats are unavailable.
func (s *Service) MemoryBytes() (usedBytes, totalBytes int64) {
	if s.src.MemoryUsage != nil {
		used, total := s.src.MemoryUsage()
		if used > 0 {
			return used, total
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	usedBytes = int64(ms.Alloc)
	totalBytes = int64(ms.Sys)
	return usedBytes, totalBytes
}

// CPULoad returns the system load average. On platforms where it cannot
// be computed safely (Windows or restricted environments), it returns
// (0, 0, 0, err). Callers should treat err != nil as "unknown".
func (s *Service) CPULoad() (load1, load5, load15 float64, err error) {
	if s.src.CPULoad != nil {
		return s.src.CPULoad()
	}
	// Default: parse /proc/loadavg on Linux only. This is the only
	// platform where reading load is universally considered safe. On
	// any other OS we return an error so the handler can label the
	// field as "unknown" rather than fabricate a value.
	if runtime.GOOS != "linux" {
		return 0, 0, 0, errors.New("cpu load: not available on this platform")
	}
	data, rerr := os.ReadFile("/proc/loadavg")
	if rerr != nil {
		return 0, 0, 0, rerr
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0, 0, 0, errors.New("cpu load: malformed /proc/loadavg")
	}
	parse := func(s string) (float64, error) {
		return strconv.ParseFloat(s, 64)
	}
	if l1, e1 := parse(fields[0]); e1 == nil {
		load1 = l1
	}
	if l5, e2 := parse(fields[1]); e2 == nil {
		load5 = l5
	}
	if l15, e3 := parse(fields[2]); e3 == nil {
		load15 = l15
	}
	return load1, load5, load15, nil
}

// ── Alert Generation ────────────────────────────────────

// EvaluateAlerts re-evaluates all alert rules. The function is
// safe to call concurrently; an internal mutex serializes writes.
//
// Delivery contract: only newly-raised alerts (i.e. alerts whose stable
// identity key was NOT already present in the previously-active set)
// are dispatched through the configured providers. A repeated
// evaluation of the same still-active condition — which happens on
// every read endpoint that calls EvaluateAlerts — does not re-deliver
// to webhook/in-app channels. An alert that was resolved (manually or
// by the condition clearing) and then re-fires IS delivered again,
// because its key is no longer in the previously-active set.
func (s *Service) EvaluateAlerts(ctx context.Context) ([]Alert, error) {
	s.alertMu.Lock()
	defer s.alertMu.Unlock()

	t := s.thresholds()
	var alerts []Alert

	// Snapshot the previously-active alert identities BEFORE resolving.
	// Only alerts whose key is NOT in this set will be dispatched,
	// which prevents repeated evaluations of the same still-active
	// condition from spamming webhook/in-app delivery.
	previousKeys := s.activeAlertKeys(ctx)

	// Resolve previous alerts before re-evaluating.
	s.resolveAll(ctx)

	// Queue dead-letter (Monitoring v1 rule).
	if s.src.QueueDeadLetter != nil {
		if n, err := s.src.QueueDeadLetter(); err == nil && n > 0 {
			alerts = append(alerts, s.newAlert(CatQueue, SeverityCritical,
				"Queue dead-letter", fmt.Sprintf("%d messages in dead-letter", n)))
		}
	}

	// Queue pending (existing rule).
	if s.src.QueuePending != nil {
		if count, err := s.src.QueuePending(); err == nil {
			if count > t.QueueDepthCritical {
				alerts = append(alerts, s.newAlert(CatQueue, SeverityCritical, "Queue growth critical", fmt.Sprintf("%d pending messages", count)))
			} else if count > t.QueueDepthWarning {
				alerts = append(alerts, s.newAlert(CatQueue, SeverityWarning, "Queue growth warning", fmt.Sprintf("%d pending messages", count)))
			}
		}
	}

	// TLS expiry (existing rule).
	if s.src.TLSCerts != nil {
		if exp30, exp7, err := s.src.TLSCerts(); err == nil {
			if exp7 > 0 {
				alerts = append(alerts, s.newAlert(CatTLS, SeverityCritical, "TLS certificates expiring soon", fmt.Sprintf("%d certificates expire within %d days", exp7, t.CertExpiryCriticalDays)))
			}
			if exp30 > 0 {
				alerts = append(alerts, s.newAlert(CatTLS, SeverityWarning, "TLS certificates expiring", fmt.Sprintf("%d certificates expire within %d days", exp30, t.CertExpiryWarningDays)))
			}
		}
	}

	// Backup freshness (existing rule).
	if s.src.LatestBackup != nil {
		if latest, err := s.src.LatestBackup(); err == nil {
			hours := time.Since(latest).Hours()
			if hours > t.BackupAgeCriticalHours {
				alerts = append(alerts, s.newAlert(CatBackup, SeverityCritical, "No recent backup", fmt.Sprintf("Last backup was %.0f hours ago", hours)))
			} else if hours > t.BackupAgeWarningHours {
				alerts = append(alerts, s.newAlert(CatBackup, SeverityWarning, "Backup is aging", fmt.Sprintf("Last backup was %.0f hours ago", hours)))
			}
		}
	}

	// Backup dir not writable (Monitoring v1 rule).
	if s.src.BackupDirWritable != nil && !s.src.BackupDirWritable() {
		alerts = append(alerts, s.newAlert(CatBackup, SeverityCritical,
			"Backup directory not writable", "the backup directory is not writable"))
	} else if s.src.BackupDir != "" {
		probe := filepath.Join(s.src.BackupDir, ".orvix-write-probe")
		f, ferr := os.Create(probe)
		if ferr != nil {
			alerts = append(alerts, s.newAlert(CatBackup, SeverityCritical,
				"Backup directory not writable", "the backup directory is not writable"))
		} else {
			_ = f.Close()
			_ = os.Remove(probe)
		}
	}

	// Database health (Monitoring v1 rule, replaces the old SMTP one for DB).
	if s.src.DatabaseHealthy != nil && !s.src.DatabaseHealthy() {
		alerts = append(alerts, s.newAlert(CatDatabase, SeverityCritical,
			"Database unhealthy", "database health check failed"))
	}

	// Backup health (Monitoring v1 rule) — combines writability and
	// freshness. We do not duplicate the per-subsystem alerts above;
	// this rule fires only when the backup subsystem as a whole is in
	// a critical state.
	if s.collectBackupHealth().Status == "critical" {
		hasBackupAlert := false
		for _, a := range alerts {
			if a.Category == CatBackup && a.Active {
				hasBackupAlert = true
				break
			}
		}
		if !hasBackupAlert {
			alerts = append(alerts, s.newAlert(CatBackup, SeverityCritical,
				"Backup unhealthy", "backup subsystem in critical state"))
		}
	}

	// Disk usage (Monitoring v1 rule).
	for _, d := range s.collectDisk() {
		switch {
		case d.UsedPct >= t.DiskUsageCriticalPct:
			alerts = append(alerts, s.newAlert(CatStorage, SeverityCritical,
				"Disk usage critical", fmt.Sprintf("%s disk at %d%%", d.Label, d.UsedPct)))
		case d.UsedPct >= t.DiskUsageWarningPct:
			alerts = append(alerts, s.newAlert(CatStorage, SeverityWarning,
				"Disk usage high", fmt.Sprintf("%s disk at %d%%", d.Label, d.UsedPct)))
		}
	}

	// Runtime health (existing rules — kept for back-compat).
	if s.src.SMTPHealthy != nil && !s.src.SMTPHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "SMTP unhealthy", "SMTP service is not healthy"))
	}
	if s.src.IMAPHealthy != nil && !s.src.IMAPHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "IMAP unhealthy", "IMAP service is not healthy"))
	}
	if s.src.POP3Healthy != nil && !s.src.POP3Healthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "POP3 unhealthy", "POP3 service is not healthy"))
	}
	if s.src.JMAPHealthy != nil && !s.src.JMAPHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "JMAP unhealthy", "JMAP service is not healthy"))
	}
	if s.src.MailStoreHealthy != nil && !s.src.MailStoreHealthy() {
		alerts = append(alerts, s.newAlert(CatRuntime, SeverityCritical, "MailStore unhealthy", "MailStore health check failed"))
	}

	// Persist alerts.
	for i := range alerts {
		s.saveAlert(ctx, &alerts[i])
	}

	// Deliver only newly-raised alerts. Delivery is best-effort and
	// isolated: a failing webhook never aborts evaluation or crashes
	// monitoring. The previousKeys snapshot was captured BEFORE
	// resolveAll, so an alert that was active in the prior evaluation
	// and is still active now is treated as "not new" and is NOT
	// re-delivered. An alert that resolves and then re-fires IS
	// delivered, because its key is no longer in previousKeys.
	if s.dispatcher != nil {
		for i := range alerts {
			k := alertKey(alerts[i])
			if previousKeys[k] {
				continue
			}
			s.dispatcher.Dispatch(ctx, alerts[i])
		}
	}

	return s.ListActiveAlerts(ctx)
}

// alertKey is the stable identity of an alert used to decide whether
// the alert is "newly raised" or "still active from a prior evaluation".
// It intentionally excludes the message field (which can change as
// counts drift) and uses category + severity + title, matching the
// dashboard's grouping and the operator's mental model of "this is the
// same alert I already saw".
func alertKey(a Alert) string {
	return string(a.Category) + "|" + string(a.Severity) + "|" + a.Title
}

// activeAlertKeys returns the set of alertKey values for every alert
// currently in the active state. It is captured at the top of
// EvaluateAlerts (before resolveAll) so the dispatch decision can be
// made against the "previously active" set.
func (s *Service) activeAlertKeys(ctx context.Context) map[string]bool {
	keys := make(map[string]bool)
	if s.db == nil {
		return keys
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT category, severity, title FROM monitoring_alerts WHERE active=1`)
	if err != nil {
		return keys
	}
	defer rows.Close()
	for rows.Next() {
		var cat, sev, title string
		if err := rows.Scan(&cat, &sev, &title); err != nil {
			continue
		}
		keys[cat+"|"+sev+"|"+title] = true
	}
	return keys
}

func (s *Service) newAlert(cat Category, sev Severity, title, msg string) Alert {
	return Alert{
		Category: cat, Severity: sev, Title: title, Message: msg,
		Source: string(cat), Active: true, CreatedAt: time.Now().UTC(),
	}
}

func (s *Service) saveAlert(ctx context.Context, a *Alert) {
	if s.db == nil {
		return
	}
	s.db.ExecContext(ctx,
		`INSERT INTO monitoring_alerts (category, severity, title, message, source, active, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(a.Category), string(a.Severity), a.Title, a.Message, a.Source, a.Active, a.CreatedAt)
}

func (s *Service) resolveAll(ctx context.Context) {
	if s.db == nil {
		return
	}
	s.db.ExecContext(ctx, "UPDATE monitoring_alerts SET active=0, resolved_at="+s.dialect.NowExpr()+" WHERE active=1")
}

// alertMu serializes EvaluateAlerts / saveAlert / resolveAll so two
// concurrent evaluations cannot interleave alert rows. The handlers in
// the Monitoring v1 surface are not concurrent in the same process, but
// the lock is cheap and makes the unit tests deterministic.

// ── Alert CRUD ──────────────────────────────────────────

// ListActiveAlerts returns the active (unresolved) alerts.
func (s *Service) ListActiveAlerts(ctx context.Context) ([]Alert, error) {
	return s.listAlerts(ctx, true)
}

// ListAllAlerts returns the full alert history.
func (s *Service) ListAllAlerts(ctx context.Context) ([]Alert, error) {
	return s.listAlerts(ctx, false)
}

func (s *Service) listAlerts(ctx context.Context, activeOnly bool) ([]Alert, error) {
	if s.db == nil {
		return nil, nil
	}
	query := "SELECT id, category, severity, title, message, source, active, created_at, resolved_at FROM monitoring_alerts"
	if activeOnly {
		query += " WHERE active=1"
	}
	query += " ORDER BY created_at DESC LIMIT 200"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		var resolvedAt sql.NullTime
		if err := rows.Scan(&a.ID, &a.Category, &a.Severity, &a.Title, &a.Message, &a.Source, &a.Active, &a.CreatedAt, &resolvedAt); err != nil {
			return nil, err
		}
		if resolvedAt.Valid {
			a.ResolvedAt = &resolvedAt.Time
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

// ResolveAlert marks the given alert id as resolved. Returns the
// number of rows affected so the handler can produce a 404 on zero.
func (s *Service) ResolveAlert(ctx context.Context, id uint) (int64, error) {
	if s.db == nil {
		return 0, errors.New("monitoring: no database configured")
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE monitoring_alerts SET active=0, resolved_at="+s.dialect.NowExpr()+" WHERE id="+s.dialect.Placeholder(1)+" AND active=1", id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── Capacity ────────────────────────────────────────────

// GetCapacity returns the dashboard capacity snapshot. Kept for
// backward compatibility with origin/main.
func (s *Service) GetCapacity(ctx context.Context) *Capacity {
	c := &Capacity{}
	if s.src.DomainCount != nil {
		c.DomainCount, _ = s.src.DomainCount()
	}
	if s.src.MailboxCount != nil {
		c.MailboxCount, _ = s.src.MailboxCount()
	}
	if s.src.MessageCount != nil {
		c.MessageCount, _ = s.src.MessageCount()
	}
	if s.src.AttachmentCount != nil {
		c.AttachmentCount, _ = s.src.AttachmentCount()
	}
	if s.src.StorageBytes != nil {
		c.StorageBytes, _ = s.src.StorageBytes()
	}
	if s.src.BackupCount != nil {
		c.BackupCount, _ = s.src.BackupCount()
	}

	if s.src.DatabaseSize != nil {
		if n, err := s.src.DatabaseSize(); err == nil && n > 0 {
			c.DatabaseSize = n
		}
	} else if s.db != nil {
		var size int64
		s.db.QueryRowContext(ctx, "SELECT IFNULL(SUM(pgsize), 0) FROM (SELECT page_count * page_size as pgsize FROM pragma_page_count(), pragma_page_size())").Scan(&size)
		c.DatabaseSize = size
	}

	if s.src.QueuePending != nil {
		count, _ := s.src.QueuePending()
		c.QueueCount = count
	}
	if s.src.QueueDeadLetter != nil {
		if n, err := s.src.QueueDeadLetter(); err == nil {
			c.QueueDeadLetter = n
		}
	}

	return c
}
