package updater

import "time"

// Severity classifies an update-history row by outcome.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Channel is the release channel. The spec mandates stable only.
type Channel string

const (
	ChannelStable Channel = "stable"
)

// UpdateStatus is the response shape for GET /api/v1/update/status.
//
// Security contract:
//   - CurrentSHA / AvailableSHA are git commit SHAs (40 hex chars). They
//     are NOT private paths and are safe to render in the admin UI.
//   - No env values, no tokens, no file contents, no private absolute
//     paths are ever populated. The install dir is a short safe label.
//   - UpdateAvailable is a boolean; UpdateError is a safe message.
type UpdateStatus struct {
	CurrentVersion   string     `json:"currentVersion"`
	CurrentSHA       string     `json:"currentSha"`
	BuildTime        string     `json:"buildTime"`
	AvailableVersion string     `json:"availableVersion"`
	AvailableSHA     string     `json:"availableSha"`
	Channel          Channel    `json:"channel"`
	UpdateAvailable  bool       `json:"updateAvailable"`
	ReleaseNotes     string     `json:"releaseNotes"`
	UpdateError      string     `json:"updateError,omitempty"`
	CheckedAt        time.Time  `json:"checkedAt"`
	JobStatus        string     `json:"jobStatus"`
	JobStartedAt     *time.Time `json:"jobStartedAt,omitempty"`
	JobCompletedAt   *time.Time `json:"jobCompletedAt,omitempty"`
	JobActor         string     `json:"jobActor,omitempty"`
}

// ReleaseManifest is the public update-feed document. It is safe to
// deserialize from an HTTPS release feed and contains no executable
// instructions.
type ReleaseManifest struct {
	Version                 string   `json:"version"`
	GitSHA                  string   `json:"git_sha"`
	Channel                 Channel  `json:"channel"`
	ReleaseDate             string   `json:"release_date"`
	ReleaseNotes            []string `json:"release_notes"`
	MinimumSupportedVersion string   `json:"minimum_supported_version"`
}

// UpdateCheckResult is the response shape for GET/POST
// /api/v1/update/check. It intentionally uses snake_case fields for
// the release-manifest contract.
type UpdateCheckResult struct {
	CurrentVersion  string   `json:"current_version"`
	CurrentSHA      string   `json:"current_sha"`
	LatestVersion   string   `json:"latest_version"`
	LatestSHA       string   `json:"latest_sha"`
	UpdateAvailable bool     `json:"update_available"`
	Channel         Channel  `json:"channel"`
	ReleaseNotes    []string `json:"release_notes"`
	Message         string   `json:"message,omitempty"`
}

// UpdateHistoryRow is a single row in the update history table.
type UpdateHistoryRow struct {
	ID              int64      `json:"id"`
	StartedAt       time.Time  `json:"startedAt"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
	DurationSeconds int64      `json:"durationSeconds"`
	PreviousSHA     string     `json:"previousSha"`
	NewSHA          string     `json:"newSha"`
	FromVersion     string     `json:"fromVersion"`
	ToVersion       string     `json:"toVersion"`
	Status          string     `json:"status"` // "running" | "completed" | "failed"
	Severity        Severity   `json:"severity"`
	Actor           string     `json:"actor"`
	Notes           string     `json:"notes,omitempty"`
}

// PreflightResult is the response shape for the preflight check.
type PreflightResult struct {
	Pass    bool             `json:"pass"`
	Checks  []PreflightCheck `json:"checks"`
	Message string           `json:"message"`
}

// PreflightCheck is a single preflight item.
type PreflightCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "pass" | "warning" | "fail"
	Detail string `json:"detail"` // safe label, never a path/secret/env value
}

// UpdateErrorCode is a closed enumeration of safe error categories
// for the Update Management v1 surface. These are the ONLY strings
// that the API or audit may surface to operators; raw exec / os
// errors (which may contain absolute paths and argv) are kept on
// the server logger only.
type UpdateErrorCode string

const (
	// ErrCodeNone indicates the run completed without an error.
	// It is returned by (*UpdateError).Code() when the underlying
	// error is nil.
	ErrCodeNone UpdateErrorCode = "none"

	// ErrCodePreflightFailed indicates the preflight gate refused
	// the run (disk space, backup dir writability, script
	// allow-list, etc.). The audit row carries this code only;
	// no path or env value is recorded.
	ErrCodePreflightFailed UpdateErrorCode = "preflight_failed"

	// ErrCodeStartFailed indicates exec.Command.Start returned a
	// non-nil error. The underlying error is typically an
	// "exec: ...<abs path>..: file does not exist" or similar —
	// it is logged but never returned to the API or audit.
	ErrCodeStartFailed UpdateErrorCode = "start_failed"

	// ErrCodeScriptFailed indicates the script started successfully
	// but exited non-zero or the context was cancelled. The audit
	// row carries this code; the underlying error is logged.
	ErrCodeScriptFailed UpdateErrorCode = "script_failed"

	// ErrCodeTimeout indicates the parent context was cancelled
	// (deadline exceeded) while the script was running. The audit
	// row carries this code; the underlying error is logged.
	ErrCodeTimeout UpdateErrorCode = "timeout"

	// ErrCodeAlreadyRunning indicates that Run was called while
	// another job was already in flight. The audit row carries
	// this code; no underlying error is logged because the job
	// was never started.
	ErrCodeAlreadyRunning UpdateErrorCode = "already_running"
)

// UpdateError is the typed error returned by RuntimeService.Run.
// The Code field is the only field that may cross the API/audit
// boundary; the underlying error is held in Internal and is
// surfaced only via the server logger.
type UpdateError struct {
	Code     UpdateErrorCode
	Internal error
}

// Error implements the error interface. The string returned here
// is the safe code; the human-readable detail is intentionally
// omitted. Do NOT change this method to return Internal.Error() —
// doing so would re-introduce the absolute-path leak that the
// Run() refactor fixed.
func (e *UpdateError) Error() string {
	if e == nil {
		return string(ErrCodeNone)
	}
	return string(e.Code)
}

// Unwrap allows errors.Is / errors.As to inspect the underlying
// error in tests and server-side code. It is never marshaled into
// a response.
func (e *UpdateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Internal
}

// SafeMessage returns the generic, user-facing message used by
// the API response body when a Run fails. Operators see this
// text in the admin UI; the rich diagnostic detail is in the
// server logs and the audit row's Code field.
func (e *UpdateError) SafeMessage() string {
	if e == nil {
		return "update completed"
	}
	switch e.Code {
	case ErrCodePreflightFailed:
		return "update failed"
	case ErrCodeStartFailed:
		return "update failed to start"
	case ErrCodeScriptFailed:
		return "update failed"
	case ErrCodeTimeout:
		return "update failed"
	default:
		return "update failed"
	}
}

// NewUpdateError wraps an internal error with a safe code. If
// internal is already an *UpdateError, it is returned as-is (this
// keeps the original code when the error has been re-typed by an
// intermediate layer).
func NewUpdateError(code UpdateErrorCode, internal error) *UpdateError {
	if internal == nil {
		return &UpdateError{Code: code, Internal: nil}
	}
	if ue, ok := internal.(*UpdateError); ok {
		return ue
	}
	return &UpdateError{Code: code, Internal: internal}
}
