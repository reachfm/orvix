// Package dr implements the coordination and DR-readiness layer of
// Feature 16 (Milestone 13). It does NOT reimplement backup/restore
// mechanics — internal/backup.Service already provides encrypted
// backups, integrity manifests, staged restore with preflight
// (RestorePreview), verification (ValidateBackup/ValidateArchive),
// activation, and automatic rollback on failure
// (rollbackRestoreLocked), all production-grade and reused here
// unchanged. What this package adds:
//
//   - Durable, cluster-aware mutual exclusion (via
//     internal/platform/cluster's fenced leases) around backup/restore
//     operations — internal/backup.Service's own sync.Mutex only
//     prevents concurrent operations WITHIN one process; it does not
//     survive a crash or coordinate across a future multi-node
//     deployment.
//   - Restore-drill history and RPO/RTO evidence for DR readiness
//     reporting.
package dr

import "time"

const (
	// BackupLeaseResource and RestoreLeaseResource are the fenced-lease
	// resource keys used to serialize backup/restore operations across
	// the whole cluster (or, in a single-node deployment, across
	// concurrent requests to the same process — a durable lease is
	// strictly stronger than the in-process mutex, not a replacement
	// for correctness, but a fix for the "crash mid-operation forgets
	// the lock" and "second node doesn't know about the first node's
	// operation" gaps).
	BackupLeaseResource  = "dr:backup-operation"
	RestoreLeaseResource = "dr:restore-operation"
)

// DrillOutcome is whether a restore drill (a real staged restore
// exercised against isolated test data, then rolled back / discarded
// — never activated against live data) succeeded.
type DrillOutcome string

const (
	DrillSucceeded DrillOutcome = "succeeded"
	DrillFailed    DrillOutcome = "failed"
)

// Drill is one recorded restore-drill execution — the evidence base
// for RPO/RTO reporting and "when did we last actually prove a
// restore works" readiness questions.
type Drill struct {
	ID            uint         `json:"id"`
	BackupID      string       `json:"backup_id"`
	Outcome       DrillOutcome `json:"outcome"`
	DurationMS    int64        `json:"duration_ms"`
	FailureReason string       `json:"failure_reason,omitempty"`
	ActorID       uint         `json:"actor_id"`
	StartedAt     time.Time    `json:"started_at"`
}

// Readiness summarizes DR posture from real evidence: the most recent
// verified backup (RPO proxy — how much data could be lost) and the
// most recent successful drill (RTO proxy — proven restore capability
// and how long it took).
type Readiness struct {
	LastVerifiedBackupAt  *time.Time     `json:"last_verified_backup_at,omitempty"`
	RPOGap                *time.Duration `json:"rpo_gap,omitempty"` // time since last verified backup
	LastSuccessfulDrillAt *time.Time     `json:"last_successful_drill_at,omitempty"`
	LastDrillDurationMS   int64          `json:"last_drill_duration_ms,omitempty"` // RTO proxy
	MissingBackupAlert    bool           `json:"missing_backup_alert"`             // no verified backup within the alert threshold
}
