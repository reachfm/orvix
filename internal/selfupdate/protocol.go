package selfupdate

import "errors"

// Operation is a closed set of actions the updater daemon will perform.
// There is deliberately no "RunCommand"/"Exec" operation and no operation
// takes a raw string that could become a shell command, URL, or path — see
// docs/adr/0001-admin-console-self-update.md.
type Operation string

const (
	OpStatus                   Operation = "status"
	OpCheckRelease             Operation = "check_release"
	OpPreflight                Operation = "preflight"
	OpStartInstall             Operation = "start_install"
	OpGetJob                   Operation = "get_job"
	OpCancelBeforeIrreversible Operation = "cancel_before_irreversible"
	OpStartRollback            Operation = "start_rollback"
	OpListHistory              Operation = "list_history"
	OpListSnapshots            Operation = "list_snapshots"
)

// allowedOperations is the single source of truth for which operations the
// server will ever dispatch. Request.Validate rejects anything not in this
// set, including a syntactically well-formed but unrecognized operation
// string — there is no default/fallthrough case that executes anything.
var allowedOperations = map[Operation]bool{
	OpStatus:                   true,
	OpCheckRelease:             true,
	OpPreflight:                true,
	OpStartInstall:             true,
	OpGetJob:                   true,
	OpCancelBeforeIrreversible: true,
	OpStartRollback:            true,
	OpListHistory:              true,
	OpListSnapshots:            true,
}

// ProtocolVersion is bumped on any wire-incompatible change. The server
// rejects a request whose ProtocolVersion it does not recognize rather than
// guessing at a compatible interpretation.
const ProtocolVersion = 1

// MaxRequestBytes bounds a single IPC message. The API process and the
// updater run on the same host and exchange small structured JSON — there
// is never a legitimate reason for a multi-megabyte request, so an
// oversized frame is rejected before it is even fully read.
const MaxRequestBytes = 64 * 1024

// Request is the only shape of message the updater daemon will ever accept
// from the API process. Every field is validated; unknown JSON fields are
// rejected by the decoder (see server.go), not silently ignored.
type Request struct {
	ProtocolVersion int       `json:"protocol_version"`
	Op              Operation `json:"op"`

	// IdempotencyKey is required for StartInstall/StartRollback. A repeat
	// of the same key while that job is active or already completed
	// returns the existing job instead of starting a new one.
	IdempotencyKey string `json:"idempotency_key,omitempty"`

	// RequestedVersion must pass ValidateVersionString. It is never
	// interpolated into a shell string, file path, or URL — it is only
	// ever compared against verified release metadata the daemon itself
	// resolved from the official GitHub API.
	RequestedVersion string `json:"requested_version,omitempty"`

	// Channel is restricted to a fixed allow-list (see ValidateChannel).
	Channel string `json:"channel,omitempty"`

	JobID string `json:"job_id,omitempty"`

	// InitiatedBy identifies the admin for audit purposes only; it is
	// never used to build a path or command.
	InitiatedBy string `json:"initiated_by,omitempty"`
}

// Response is returned for every request, success or failure. Error is a
// sanitized, operator-safe message — never a raw error, stack trace, or
// environment dump (see the threat model's "secret leakage in logs" row).
type Response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`

	Job       *Job               `json:"job,omitempty"`
	Jobs      []Job              `json:"jobs,omitempty"`
	Snapshots []RollbackSnapshot `json:"snapshots,omitempty"`
	Releases  []ReleaseInfo      `json:"releases,omitempty"`
}

var (
	ErrUnknownOperation        = errors.New("selfupdate: unknown or disallowed operation")
	ErrUnsupportedProtoVersion = errors.New("selfupdate: unsupported protocol version")
	ErrMissingIdempotencyKey   = errors.New("selfupdate: idempotency_key is required for this operation")
)

var validChannels = map[string]bool{
	"stable":     true,
	"prerelease": true,
}

// ValidateChannel rejects anything outside the fixed channel allow-list —
// a channel string is never used to build a URL, so this exists purely to
// keep the value meaningful, not as an injection defense.
func ValidateChannel(c string) error {
	if !validChannels[c] {
		return errors.New("selfupdate: channel must be one of: stable, prerelease")
	}
	return nil
}

// Validate checks a Request against the fixed protocol contract before the
// server dispatches it to any handler. This is the single choke point that
// enforces "no free-form operation, no free-form version string" for every
// code path that reaches the updater daemon.
func (r *Request) Validate() error {
	if r.ProtocolVersion != ProtocolVersion {
		return ErrUnsupportedProtoVersion
	}
	if !allowedOperations[r.Op] {
		return ErrUnknownOperation
	}
	switch r.Op {
	case OpStartInstall, OpStartRollback:
		if r.IdempotencyKey == "" {
			return ErrMissingIdempotencyKey
		}
		if len(r.IdempotencyKey) > 128 {
			return errors.New("selfupdate: idempotency_key too long")
		}
	}
	switch r.Op {
	case OpStartInstall, OpCheckRelease:
		if r.RequestedVersion != "" {
			if err := ValidateVersionString(r.RequestedVersion); err != nil {
				return err
			}
		}
		if r.Channel != "" {
			if err := ValidateChannel(r.Channel); err != nil {
				return err
			}
		}
	}
	switch r.Op {
	case OpGetJob, OpStartRollback, OpCancelBeforeIrreversible:
		if r.Op == OpGetJob && r.JobID == "" {
			return errors.New("selfupdate: job_id is required")
		}
	}
	if len(r.InitiatedBy) > 256 {
		return errors.New("selfupdate: initiated_by too long")
	}
	return nil
}
