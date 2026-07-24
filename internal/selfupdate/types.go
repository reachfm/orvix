// Package selfupdate implements the security-critical core of the Admin
// Console self-update feature: release bundle verification, the job/phase
// model, and the structured protocol between the unprivileged orvix
// process and the privileged orvix-updater daemon.
//
// This package must never build a shell command string from any value it
// receives — see docs/adr/0001-admin-console-self-update.md for the full
// threat model this package exists to enforce.
package selfupdate

import "time"

// Phase is one step of an update or rollback job. The zero value is not a
// valid phase; every job must be explicitly created with PhaseQueued.
type Phase string

const (
	PhaseQueued           Phase = "queued"
	PhaseChecking         Phase = "checking"
	PhaseDownloading      Phase = "downloading"
	PhaseVerifying        Phase = "verifying"
	PhasePreflight        Phase = "preflight"
	PhaseBackingUp        Phase = "backing_up"
	PhaseStoppingService  Phase = "stopping_service"
	PhaseMigrating        Phase = "migrating"
	PhaseReplacingRuntime Phase = "replacing_runtime"
	PhaseRestarting       Phase = "restarting"
	PhaseHealthCheck      Phase = "health_check"
	PhaseCompleted        Phase = "completed"
	PhaseFailed           Phase = "failed"
	PhaseRollingBack      Phase = "rolling_back"
	PhaseRolledBack       Phase = "rolled_back"
)

// Terminal reports whether a phase is an end state — no further transition
// happens without a brand new job (or, for a failed install, an explicit
// rollback job).
func (p Phase) Terminal() bool {
	switch p {
	case PhaseCompleted, PhaseFailed, PhaseRolledBack:
		return true
	default:
		return false
	}
}

// JobKind distinguishes an install job from a rollback job. They share the
// same Job/event model but have different allowed phase sequences.
type JobKind string

const (
	JobKindInstall  JobKind = "install"
	JobKindRollback JobKind = "rollback"
)

// Job is the persistent record of one update or rollback attempt. It is the
// single source of truth the Admin Console UI polls, so that a browser
// refresh or an updater-daemon restart never loses progress.
type Job struct {
	ID               string    `json:"id"`
	Kind             JobKind   `json:"kind"`
	IdempotencyKey   string    `json:"idempotency_key"`
	RequestedVersion string    `json:"requested_version"`
	InitiatedBy      string    `json:"initiated_by"` // admin user email/id
	Phase            Phase     `json:"phase"`
	ProgressPercent  int       `json:"progress_percent"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	ArtifactSHA256   string `json:"artifact_sha256,omitempty"`
	ArtifactVersion  string `json:"artifact_version,omitempty"`
	ArtifactCommit   string `json:"artifact_commit,omitempty"`
	RollbackSnapshot string `json:"rollback_snapshot_id,omitempty"`

	FailureCode    string `json:"failure_code,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
	RollbackResult string `json:"rollback_result,omitempty"`
}

// Event is one immutable entry in a job's history. Jobs are append-only —
// existing events are never edited or deleted, only appended to.
type Event struct {
	JobID     string    `json:"job_id"`
	Seq       int       `json:"seq"`
	At        time.Time `json:"at"`
	Phase     Phase     `json:"phase"`
	Message   string    `json:"message"`
}

// ReleaseInfo describes one release as resolved from the official GitHub
// release channel, before verification. Nothing in this struct is trusted
// until VerifyBundle succeeds against the corresponding downloaded bytes.
type ReleaseInfo struct {
	Tag             string    `json:"tag"`
	Version         string    `json:"version"`
	Channel         string    `json:"channel"`
	PublishedAt     time.Time `json:"published_at"`
	Prerelease      bool      `json:"prerelease"`
	AssetName       string    `json:"asset_name"`
	ChecksumSidecar string    `json:"checksum_sidecar"`
	SignatureSidecar string   `json:"signature_sidecar"`
	ManifestAsset   string    `json:"manifest_asset"`
}
