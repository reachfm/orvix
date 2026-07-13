package backup

import "time"

// ProductName is the canonical product name embedded in backup manifests.
const ProductName = "Orvix Enterprise Mail"

// BackupFormatVersion is the version of the backup.json schema.
const BackupFormatVersion = 1

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

// BackupManifest is the on-disk per-backup manifest (manifest.json).
// Legacy format preserved for backward compatibility; the canonical
// enterprise manifest for archives is BackupArchiveManifest (backup.json).
type BackupManifest struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	CreatedAt       time.Time         `json:"createdAt"`
	CompletedAt     *time.Time        `json:"completedAt,omitempty"`
	SizeBytes       int64             `json:"sizeBytes"`
	SHA256          string            `json:"sha256"`
	DomainCount     int               `json:"domainCount"`
	MailboxCount    int               `json:"mailboxCount"`
	PolicyCount     int               `json:"policyCount"`
	MessageCount    int64             `json:"messageCount"`
	AttachmentCount int64             `json:"attachmentCount"`
	Version         string            `json:"version,omitempty"`
	BuildCommit     string            `json:"buildCommit,omitempty"`
	Hostname        string            `json:"hostname,omitempty"`
	Files           map[string]string `json:"files,omitempty"`
	// DatabaseFormat describes the format of the "database.sqlite"
	// entry: "sqlite" (a real SQLite file produced by VACUUM INTO)
	// or "postgres-custom" (a pg_dump -Fc archive, despite the
	// filename — restore with `pg_restore`, not by copying the
	// file back as a SQLite database).
	DatabaseFormat string `json:"databaseFormat,omitempty"`
	Encrypted      bool   `json:"encrypted,omitempty"`
	Checksum       string `json:"checksum,omitempty"`
}

// BackupEncryptionConfig holds the configuration for backup encryption.
type BackupEncryptionConfig struct {
	Enabled  bool
	Password string
	KeyFile  string
}

// ManifestItem describes a single file in the backup archive.
type ManifestItem struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// BackupArchiveManifest is the enterprise manifest stored as backup.json
// inside the tar.gz archive. It is the source of truth for 2H.
// archive_sha256 is intentionally NOT included here to avoid a self-
// referential hash. Instead the final archive sha256 is stored in a
// sidecar file: backup-archive.tar.gz.sha256.
type BackupArchiveManifest struct {
	BackupID              string         `json:"backup_id"`
	CreatedAt             string         `json:"created_at"`
	Hostname              string         `json:"hostname"`
	Product               string         `json:"product"`
	Version               string         `json:"version"`
	BuildCommit           string         `json:"build_commit"`
	SchemaVersion         int            `json:"schema_version"`
	BackupFormatVersion   int            `json:"backup_format_version"`
	IncludedItems         []ManifestItem `json:"included_items"`
	DatabasePath          string         `json:"database_path"`
	ConfigPath            string         `json:"config_path"`
	Warnings              []string       `json:"warnings,omitempty"`
	ConfigSummaryRedacted bool           `json:"config_summary_redacted"`
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

// RestoreStageResult is returned by RestoreBackup.
// In Phase 2H the restore is staged (not applied live).
type RestoreStageResult struct {
	Status      string `json:"status"`
	Message     string `json:"message"`
	BackupID    string `json:"backup_id"`
	StagingPath string `json:"staging_path,omitempty"`
}

const (
	RestoreStatusStaged  = "staged"
	RestoreStatusFailed  = "failed"
	RestoreStagedMessage = "Validated and staged, restart/manual apply required"
)

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
	SchedulerEnabled      bool    `json:"schedulerEnabled"`
	RetentionEnabled      bool    `json:"retentionEnabled"`
	DirectoryExists       bool    `json:"directoryExists"`
	Writable              bool    `json:"writable"`
	AvailableDiskBytes    int64   `json:"availableDiskBytes"`
	LastBackupAgeHours    float64 `json:"lastBackupAgeHours"`
	LastBackupAgeWarning  bool    `json:"lastBackupAgeWarning"`
	LastBackupAgeCritical bool    `json:"lastBackupAgeCritical"`
	Status                string  `json:"status"`
	// Reason is a human-readable explanation of the Status, e.g.
	// "no backups yet — first run pending" or "no backups in 96h".
	// It is empty when Status is "ok".
	Reason string `json:"reason,omitempty"`
	// NoBackups is true when the system has never produced a backup
	// in this install. The previous release conflated this with
	// "critical", which produced misleading dashboard alerts on
	// fresh installs. With NoBackups, the operator sees a
	// distinct state and can dismiss it without worrying about
	// missing an actual incident.
	NoBackups bool `json:"no_backups,omitempty"`
}

// Status values returned by GetBackupHealth. Exposed as constants
// so the API handlers and tests can match on them without using
// string literals.
const (
	HealthStatusOK             = "ok"
	HealthStatusWarning        = "warning"
	HealthStatusCritical       = "critical"
	HealthStatusNoBackups      = "no_backups"
	HealthStatusDirMissing     = "directory_missing"
	HealthStatusDirNotWritable = "directory_not_writable"
	HealthStatusDisabled       = "scheduler_disabled"
)

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
		retention_count INTEGER NOT NULL DEFAULT 10,
		last_run_at DATETIME,
		next_run_at DATETIME,
		updated_at DATETIME NOT NULL
	)`,
}
