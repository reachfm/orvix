package monitoring

import "time"

// Severity classifies an alert by impact.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Category classifies an alert by subsystem.
type Category string

const (
	CatRuntime  Category = "runtime"
	CatQueue    Category = "queue"
	CatTLS      Category = "tls"
	CatDNS      Category = "dns"
	CatBackup   Category = "backup"
	CatTrust    Category = "trust"
	CatPolicy   Category = "policy"
	CatStorage  Category = "storage"
	CatDatabase Category = "database"
	CatAPI      Category = "api"
)

// Alert is a single monitoring event.
//
// Security contract: the Message field MUST NOT contain file contents,
// environment values, secret tokens, or private filesystem paths. The
// field is rendered verbatim in the admin UI. Use safe labels only.
type Alert struct {
	ID         uint       `json:"id"`
	Category   Category   `json:"category"`
	Severity   Severity   `json:"severity"`
	Title      string     `json:"title"`
	Message    string     `json:"message"`
	Source     string     `json:"source"`
	Active     bool       `json:"active"`
	CreatedAt  time.Time  `json:"createdAt"`
	ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
}

// Capacity is a high-level set of system counters for the dashboard.
type Capacity struct {
	DomainCount     int   `json:"domainCount"`
	MailboxCount    int64 `json:"mailboxCount"`
	MessageCount    int64 `json:"messageCount"`
	AttachmentCount int64 `json:"attachmentCount"`
	QueueCount      int64 `json:"queueCount"`
	QueueDeadLetter int64 `json:"queueDeadLetter"`
	StorageBytes    int64 `json:"storageBytes"`
	DatabaseSize    int64 `json:"databaseSize"`
	BackupCount     int   `json:"backupCount"`
	BackupBytes     int64 `json:"backupBytes"`
}

// DiskUsage describes filesystem consumption for a single mount or directory.
//
// Security contract: MountPath is a SAFE LABEL (e.g. "backup", "database",
// "mailstore"), never the actual on-disk absolute path. The handler is
// responsible for collapsing the real path into one of the known labels
// before populating this struct.
type DiskUsage struct {
	Label      string `json:"label"`
	TotalBytes int64  `json:"totalBytes"`
	UsedBytes  int64  `json:"usedBytes"`
	FreeBytes  int64  `json:"freeBytes"`
	UsedPct    int    `json:"usedPct"`
}

// Health is the top-level response shape for /api/v1/monitoring/health.
//
// Security contract: Status, Uptime, DiskUsage, DBHealth, QueueHealth,
// BackupHealth, APIHealth are all safe fields. ServiceUptime is a
// duration, never an absolute timestamp. DiskUsage paths are safe
// labels. No env values, no tokens, no file contents, no private
// filesystem paths.
type Health struct {
	Status        string       `json:"status"`        // "ok" | "degraded" | "down"
	UptimeSeconds int64        `json:"uptimeSeconds"` // process uptime
	GeneratedAt   time.Time    `json:"generatedAt"`
	Disk          []DiskUsage  `json:"disk"`
	DB            ComponentHealth `json:"db"`
	Queue         ComponentHealth `json:"queue"`
	Backup        ComponentHealth `json:"backup"`
	API           ComponentHealth `json:"api"`
	Capacity      Capacity     `json:"capacity"`
	OpenAlerts    int          `json:"openAlerts"`
}

// ComponentHealth is a per-subsystem status.
type ComponentHealth struct {
	Status  string `json:"status"`  // "ok" | "warning" | "critical" | "unknown"
	Message string `json:"message"` // safe label, never a path/secret/env value
}

// AlertThresholds defines configurable alert trigger points.
// All fields use sensible defaults when set to zero.
type AlertThresholds struct {
	DiskUsageWarningPct    int     `json:"diskUsageWarningPct"`
	DiskUsageCriticalPct   int     `json:"diskUsageCriticalPct"`
	QueueDepthWarning      int64   `json:"queueDepthWarning"`
	QueueDepthCritical     int64   `json:"queueDepthCritical"`
	BackupAgeWarningHours  float64 `json:"backupAgeWarningHours"`
	BackupAgeCriticalHours float64 `json:"backupAgeCriticalHours"`
	CertExpiryWarningDays  int     `json:"certExpiryWarningDays"`
	CertExpiryCriticalDays int     `json:"certExpiryCriticalDays"`
}

// ApplyDefaults fills in zero values with sensible defaults.
func (t *AlertThresholds) ApplyDefaults() {
	if t.DiskUsageWarningPct <= 0 {
		t.DiskUsageWarningPct = 85
	}
	if t.DiskUsageCriticalPct <= 0 {
		t.DiskUsageCriticalPct = 95
	}
	if t.QueueDepthWarning <= 0 {
		t.QueueDepthWarning = 100
	}
	if t.QueueDepthCritical <= 0 {
		t.QueueDepthCritical = 500
	}
	if t.BackupAgeWarningHours <= 0 {
		t.BackupAgeWarningHours = 24
	}
	if t.BackupAgeCriticalHours <= 0 {
		t.BackupAgeCriticalHours = 72
	}
	if t.CertExpiryWarningDays <= 0 {
		t.CertExpiryWarningDays = 30
	}
	if t.CertExpiryCriticalDays <= 0 {
		t.CertExpiryCriticalDays = 7
	}
}

// MonitoringSnapshot provides a comprehensive snapshot of all health indicators
// in a single JSON response.
type MonitoringSnapshot struct {
	GeneratedAt    time.Time        `json:"generatedAt"`
	ServiceStatus  string           `json:"serviceStatus"`
	UptimeSeconds  int64            `json:"uptimeSeconds"`
	Disk           []DiskUsage      `json:"disk"`
	DBHealth       ComponentHealth  `json:"dbHealth"`
	QueueHealth    ComponentHealth  `json:"queueHealth"`
	BackupHealth   ComponentHealth  `json:"backupHealth"`
	APIHealth      ComponentHealth  `json:"apiHealth"`
	CertExpiry     CertExpiryStatus `json:"certExpiry"`
	DNSReadiness   ComponentHealth  `json:"dnsReadiness"`
	Capacity       Capacity         `json:"capacity"`
	OpenAlerts     int              `json:"openAlerts"`
	MemoryUsedBytes int64           `json:"memoryUsedBytes"`
	MemoryTotalBytes int64          `json:"memoryTotalBytes"`
}

// CertExpiryStatus reports TLS certificate expiry status.
type CertExpiryStatus struct {
	Status          string `json:"status"`
	ExpiringWithin7 int    `json:"expiringWithin7"`
	ExpiringWithin30 int   `json:"expiringWithin30"`
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS monitoring_alerts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		category TEXT NOT NULL DEFAULT '',
		severity TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL DEFAULT '',
		source TEXT NOT NULL DEFAULT '',
		active INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL,
		resolved_at DATETIME
	)`,
}

// Schema returns the SQL DDL statements the monitoring service needs.
// The handler calls this on every request to keep the schema in sync
// with the latest definition; CREATE TABLE IF NOT EXISTS is idempotent
// and cheap.
func Schema() []string {
	return schema
}
