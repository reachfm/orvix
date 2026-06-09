package monitoring

import "time"

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

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
)

type Alert struct {
	ID          uint      `json:"id"`
	Category    Category  `json:"category"`
	Severity    Severity  `json:"severity"`
	Title       string    `json:"title"`
	Message     string    `json:"message"`
	Source      string    `json:"source"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"createdAt"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
}

type Capacity struct {
	DomainCount     int   `json:"domainCount"`
	MailboxCount    int64 `json:"mailboxCount"`
	MessageCount    int64 `json:"messageCount"`
	AttachmentCount int64 `json:"attachmentCount"`
	QueueCount      int64 `json:"queueCount"`
	StorageBytes    int64 `json:"storageBytes"`
	DatabaseSize    int64 `json:"databaseSize"`
	BackupCount     int   `json:"backupCount"`
	BackupBytes     int64 `json:"backupBytes"`
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
