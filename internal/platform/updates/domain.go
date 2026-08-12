// Package updates implements Milestone 13's signed update-artifact
// verification and staged-update lifecycle. It performs real
// cryptographic verification (ed25519 over a JSON manifest, stdlib
// crypto only — no shelling out) and pure file/hash operations for
// staging. It deliberately does NOT apply an update itself: the API
// process must never restart or replace itself in-process (same
// constraint documented on internal/restorecoord for restore). Apply
// is exposed as a hand-off point for an external coordinator; until
// one exists, "trigger apply" only confirms the artifact is verified
// and staged and returns its status — it never executes anything from
// the artifact or its manifest.
package updates

import "time"

// State is a staged-update's position in its lifecycle. Transitions
// only ever move forward except RolledBack, which is reachable only
// from Applied.
type State string

const (
	StateDownloaded State = "downloaded"
	StateVerified   State = "verified"
	StateStaged     State = "staged"
	StateApplied    State = "applied"
	StateRolledBack State = "rolled_back"
	StateRejected   State = "rejected"
)

// Manifest describes one update artifact. It is the ONLY thing the
// signature covers — the raw artifact bytes are verified separately
// against Manifest.SHA256, so a signed manifest cannot be paired with
// a different (tampered) artifact and pass.
type Manifest struct {
	Version  string `json:"version"`
	Platform string `json:"platform"` // e.g. "linux"
	Arch     string `json:"arch"`     // e.g. "amd64"
	SHA256   string `json:"sha256"`   // hex-encoded sha256 of the artifact bytes
}

// Record is one staged/applied update's durable state, including the
// rollback metadata captured BEFORE apply (previous version/hash), per
// the requirement that rollback metadata exist ahead of any apply
// attempt, not be reconstructed after the fact.
type Record struct {
	ID            uint      `json:"id"`
	Version       string    `json:"version"`
	Platform      string    `json:"platform"`
	Arch          string    `json:"arch"`
	ArtifactHash  string    `json:"artifact_hash"`
	ArtifactPath  string    `json:"artifact_path,omitempty"`
	State         State     `json:"state"`
	PrevVersion   string    `json:"prev_version,omitempty"`
	PrevHash      string    `json:"prev_hash,omitempty"`
	FailureNote   string    `json:"failure_note,omitempty"`
	ActorID       uint      `json:"actor_id"`
	ApplyJobID    string    `json:"apply_job_id,omitempty"`
	RollbackJobID string    `json:"rollback_job_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
