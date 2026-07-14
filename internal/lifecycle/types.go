package lifecycle

import "time"

type VersionRecord struct {
	ID          uint      `json:"id"`
	Version     string    `json:"version"`
	InstalledAt time.Time `json:"installedAt"`
	InstalledBy string    `json:"installedBy"`
	Notes       string    `json:"notes"`
}

type UpgradeStatus string

const (
	UpgradePending    UpgradeStatus = "pending"
	UpgradeRunning    UpgradeStatus = "running"
	UpgradeCompleted  UpgradeStatus = "completed"
	UpgradeFailed     UpgradeStatus = "failed"
	UpgradeRolledBack UpgradeStatus = "rolled_back"
)

type UpgradeRecord struct {
	ID          uint          `json:"id"`
	FromVersion string        `json:"fromVersion"`
	ToVersion   string        `json:"toVersion"`
	Status      UpgradeStatus `json:"status"`
	StartedAt   time.Time     `json:"startedAt"`
	CompletedAt *time.Time    `json:"completedAt,omitempty"`
}

type PreflightResult struct {
	Pass   bool             `json:"pass"`
	Checks []PreflightCheck `json:"checks"`
}

type PreflightCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "pass", "warning", "fail"
	Detail string `json:"detail,omitempty"`
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS coremail_versions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version TEXT NOT NULL DEFAULT '',
		installed_at DATETIME NOT NULL,
		installed_by TEXT NOT NULL DEFAULT '',
		notes TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE TABLE IF NOT EXISTS upgrade_history (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_version TEXT NOT NULL DEFAULT '',
		to_version TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'pending',
		started_at DATETIME NOT NULL,
		completed_at DATETIME
	)`,
}
