package backup

import "time"

type Status string

const (
	StatusPending     Status = "pending"
	StatusInProgress  Status = "in_progress"
	StatusCompleted   Status = "completed"
	StatusFailed      Status = "failed"
	StatusVerifying   Status = "verifying"
	StatusVerified    Status = "verified"
)

type Backup struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Status      Status    `json:"status"`
	SizeBytes   int64     `json:"sizeBytes"`
	SHA256      string    `json:"sha256"`
	CreatedAt   time.Time `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

type BackupManifest struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"createdAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	SizeBytes   int64     `json:"sizeBytes"`
	SHA256      string    `json:"sha256"`
	DomainCount int       `json:"domainCount"`
	MailboxCount int      `json:"mailboxCount"`
	PolicyCount int       `json:"policyCount"`
	MessageCount int64    `json:"messageCount"`
	AttachmentCount int64 `json:"attachmentCount"`
}

type VerifyResult struct {
	Valid       bool     `json:"valid"`
	Errors      []string `json:"errors,omitempty"`
	SizeBytes   int64    `json:"sizeBytes"`
	SHA256      string   `json:"sha256"`
}

type RestorePreview struct {
	DomainCount     int    `json:"domainCount"`
	MailboxCount    int    `json:"mailboxCount"`
	PolicyCount     int    `json:"policyCount"`
	MessageCount    int64  `json:"messageCount"`
	AttachmentCount int64  `json:"attachmentCount"`
	SizeBytes       int64  `json:"sizeBytes"`
}

type RestoreResult struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
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
}
