package backup

import "time"

type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
	StatusVerifying  Status = "verifying"
	StatusVerified   Status = "verified"
)

type Backup struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Status      Status     `json:"status"`
	SizeBytes   int64      `json:"sizeBytes"`
	SHA256      string     `json:"sha256"`
	CreatedAt   time.Time  `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

type BackupManifest struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	CreatedAt       time.Time  `json:"createdAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
	SizeBytes       int64      `json:"sizeBytes"`
	SHA256          string     `json:"sha256"`
	DomainCount     int        `json:"domainCount"`
	MailboxCount    int        `json:"mailboxCount"`
	PolicyCount     int        `json:"policyCount"`
	MessageCount    int64      `json:"messageCount"`
	AttachmentCount int64      `json:"attachmentCount"`
}

type VerifyResult struct {
	Valid     bool     `json:"valid"`
	Errors    []string `json:"errors,omitempty"`
	SizeBytes int64    `json:"sizeBytes"`
	SHA256    string   `json:"sha256"`
}

type RestorePreview struct {
	DomainCount     int   `json:"domainCount"`
	MailboxCount    int   `json:"mailboxCount"`
	PolicyCount     int   `json:"policyCount"`
	MessageCount    int64 `json:"messageCount"`
	AttachmentCount int64 `json:"attachmentCount"`
	SizeBytes       int64 `json:"sizeBytes"`
}

type Frequency string

const (
	FrequencyManual Frequency = "manual"
	FrequencyDaily  Frequency = "daily"
	FrequencyWeekly Frequency = "weekly"
)

type ScheduleConfig struct {
	Enabled        bool       `json:"enabled"`
	Frequency      Frequency  `json:"frequency"`
	RetentionCount int        `json:"retentionCount"`
	LastRunAt      *time.Time `json:"lastRunAt,omitempty"`
	NextRunAt      *time.Time `json:"nextRunAt,omitempty"`
	UpdatedAt      time.Time  `json:"updatedAt"`
}

type BackupMetrics struct {
	TotalBackups     int    `json:"totalBackups"`
	TotalSizeBytes   int64  `json:"totalSizeBytes"`
	NewestBackupAt   string `json:"newestBackupAt,omitempty"`
	OldestBackupAt   string `json:"oldestBackupAt,omitempty"`
	LastSuccessfulAt string `json:"lastSuccessfulAt,omitempty"`
	NextScheduledAt  string `json:"nextScheduledAt,omitempty"`
}

type BackupHealth struct {
	SchedulerEnabled   bool  `json:"schedulerEnabled"`
	RetentionEnabled   bool  `json:"retentionEnabled"`
	DirectoryExists    bool  `json:"directoryExists"`
	Writable           bool  `json:"writable"`
	AvailableDiskBytes int64 `json:"availableDiskBytes"`
}

var tables = []string{
	`CREATE TABLE IF NOT EXISTS backup_registry (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		size_bytes INTEGER NOT NULL DEFAULT 0,
		sha256 TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL,
		completed_at DATETIME
	)`,
	`CREATE TABLE IF NOT EXISTS backup_schedule_config (
		id INTEGER PRIMARY KEY DEFAULT 1,
		enabled INTEGER NOT NULL DEFAULT 0,
		frequency TEXT NOT NULL DEFAULT 'manual',
		retention_count INTEGER NOT NULL DEFAULT 7,
		last_run_at DATETIME,
		next_run_at DATETIME,
		updated_at DATETIME NOT NULL
	)`,
}
