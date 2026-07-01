// Package runtime provides read-only process telemetry for the
// Orvix admin dashboard.
//
// Security contract:
//
//   - All collectors are read-only. There is no public function that
//     mutates process or filesystem state.
//   - The collectors never read environment variables, secret files,
//     or private key material. They never shell out.
//   - The collectors never include absolute filesystem paths in the
//     returned structs. Disk labels are safe names (e.g. "system",
//     "data") chosen by the caller, or safe basenames when the caller
//     passes a known path.
//   - License posture is reduced to {mode, public_key_loaded, status}
//     — no private key, no license key hash, no expiry token.
//
// The package is intentionally small and dependency-free so it can be
// reused from the cmd/orvix bootstrap, the API handler, and the
// Monitoring v1 service.
package runtime

import (
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

// Telemetry is the read-only process + system snapshot returned by
// /api/v1/admin/runtime. It is safe to JSON-encode directly: every
// field is a primitive, an int64, a small struct, or a string with
// no embedded secret.
type Telemetry struct {
	Status        string             `json:"status"`        // "ok" | "degraded" | "unknown"
	Version       string             `json:"version"`       // build version
	Commit        string             `json:"commit"`        // build commit SHA, or "not reported"
	BuildTime     string             `json:"build_time"`    // build timestamp string, or "development"
	GoVersion     string             `json:"go_version"`    // runtime.Version()
	Arch          string             `json:"arch"`          // GOOS/GOARCH
	Hostname      string             `json:"hostname"`      // os.Hostname()
	UptimeSeconds int64              `json:"uptime_seconds"`
	StartedAt     string             `json:"started_at,omitempty"` // RFC3339
	Services      map[string]Service `json:"services"`
	Capacity      Capacity           `json:"capacity"`
	Queue         QueueCounts        `json:"queue"`
	License       LicensePosture     `json:"license"`
	Warnings      []Warning          `json:"warnings"`
}

// Service is a single per-subsystem entry. Port is included so the
// dashboard can show the listening port alongside the status without
// any client-side mapping table.
type Service struct {
	Status string `json:"status"` // "ok" | "warn" | "fail" | "unknown"
	State  string `json:"state,omitempty"`  // normalized listener state: active|skipped|degraded|failed|unknown
	Detail string `json:"detail"`
	Port   int    `json:"port,omitempty"`
}

// Capacity is the disk summary for the configured safe path. Only one
// entry is included at a time (the data path); the label is a safe
// string, never the absolute path.
type Capacity struct {
	Disk Disk `json:"disk"`
}

// Disk mirrors the safe fields from internal/monitoring.DiskUsage so
// the dashboard can render the same shape from either endpoint.
type Disk struct {
	Label       string `json:"label"`
	TotalBytes  int64  `json:"total_bytes"`
	UsedBytes   int64  `json:"used_bytes"`
	FreeBytes   int64  `json:"free_bytes"`
	UsedPercent int    `json:"used_percent"`
}

// QueueCounts is the live snapshot of the outbound queue.
type QueueCounts struct {
	Pending   int64 `json:"pending"`
	Deferred  int64 `json:"deferred"`
	Bounced   int64 `json:"bounced"`
	Delivered int64 `json:"delivered"`
}

// LicensePosture is the safe license surface. No private key, no key
// hash, no expiry token. The presence of public_key_loaded=true means
// the operator has provisioned a public key file on disk.
//
// PublicKeyState is a stable classification:
//
//	"missing"  — no configured path or file does not exist
//	"invalid"  — file exists but is not a valid PEM public key
//	"loaded"   — valid PEM public key parsed successfully
//
// ValidationState is a stable classification:
//
//	"offline"  — public key loaded, no online validation
//	"valid"    — a license row with active=true exists
//	"invalid"  — license validation was attempted and failed
//	"unknown"  — state cannot be determined
type LicensePosture struct {
	Mode             string `json:"mode"`               // "online" | "offline" | "missing"
	PublicKeyLoaded  bool   `json:"public_key_loaded"`
	PublicKeyState   string `json:"public_key_state"`   // "missing" | "invalid" | "loaded"
	ValidationState  string `json:"validation_state"`   // "offline" | "valid" | "invalid" | "unknown"
	Status           string `json:"status"`             // "ok" | "warn" | "missing"
	Tier             string `json:"tier,omitempty"`     // tier from the most recent license row, if any
	ExpiresAt        string `json:"expires_at,omitempty"`
}

// Warning is a single server-rolled-up warning. The Code is a stable
// identifier the dashboard can use to filter or suppress duplicates.
type Warning struct {
	Level   string `json:"level"`   // "info" | "warn" | "error"
	Code    string `json:"code"`    // "license_public_key_missing" | "queue_deferred" | ...
	Message string `json:"message"` // safe label, never a path or secret
}

// Inputs is the dependency bag Collect needs. Every field is
// optional; nil-safe defaults are returned for any nil entry. The
// only required field is StartedAt, which the caller sets to the
// process start time once at boot. When StartedAt is zero, the
// uptime is reported as 0 and the response carries a "telemetry
// incomplete" warning so the dashboard does not show fake numbers.
type Inputs struct {
	Version     string
	Commit      string
	BuildTime   string
	GoVersion   string
	Arch        string
	StartedAt   time.Time
	DataPath    string             // disk path to stat (label falls back to "data" if empty)
	HostnameFn  func() (string, error) // defaults to os.Hostname
	DiskFn      DiskUsageFn        // defaults to syscall-based statfs
	DBPing      func() error       // nil = unknown
	QueueCounts QueueCounts        // zero = unknown
	License     LicensePosture     // zero = unknown
	// ListenerSnapshot is the current state of protocol listeners,
	// populated by the coremail runtime module at startup. When
	// nil or empty, listeners are reported as "unknown" with the
	// stable "listener runtime state not reported" detail (the
	// pre-ADMIN-LISTENER-TRACKING-2C fallback). The snapshot is
	// read-only and populated once; the map is not retained.
	ListenerSnapshot map[ListenerKind]ListenerStatus
	// DEPRECATED: ports are now carried inside ListenerSnapshot.
	// Kept for backward compatibility during the transition — they
	// are only used when ListenerSnapshot is nil.
	SMHTTPPort int
	IMAPPort   int
	POP3Port   int
	JMAPPort   int
}

// DiskUsageFn is the per-platform disk-usage lookup. Tests can
// inject a deterministic function.
type DiskUsageFn func(path string) Disk

// NewTelemetry builds a Telemetry by applying the supplied inputs to
// the read-only collectors. The function is safe to call from any
// goroutine; no global state is mutated. The package maintains one
// atomic counter (buildID) used only to identify in-process builds
// for diagnostic logging.
func NewTelemetry(in Inputs) Telemetry {
	t := Telemetry{
		Status:    "ok",
		Version:   in.Version,
		Commit:    in.Commit,
		BuildTime: in.BuildTime,
		GoVersion: in.GoVersion,
		Arch:      in.Arch,
	}

	if t.Version == "" {
		t.Version = "unknown"
	}
	if t.Commit == "" {
		t.Commit = "not reported"
	}
	if t.BuildTime == "" {
		t.BuildTime = "not reported"
	}
	if t.GoVersion == "" {
		t.GoVersion = runtime.Version()
	}
	if t.Arch == "" {
		t.Arch = runtime.GOOS + "/" + runtime.GOARCH
	}

	// Hostname — never panic, never return an empty string in
	// the JSON (we always emit a value, even if it is "unknown").
	hnFn := in.HostnameFn
	if hnFn == nil {
		hnFn = os.Hostname
	}
	if h, err := hnFn(); err == nil && h != "" {
		t.Hostname = h
	} else {
		t.Hostname = "unknown"
	}

	// Uptime — only meaningful if StartedAt was set at boot.
	if !in.StartedAt.IsZero() {
		t.StartedAt = in.StartedAt.UTC().Format(time.RFC3339)
		up := time.Since(in.StartedAt)
		if up < 0 {
			up = 0
		}
		t.UptimeSeconds = int64(up.Seconds())
	} else {
		t.Status = "unknown"
	}

	// Disk — only the safe label; the absolute path is never
	// echoed back. The label falls back to "data" when DataPath
	// is empty so the dashboard never sees an unlabeled row.
	diskFn := in.DiskFn
	if diskFn == nil {
		diskFn = defaultDiskUsage
	}
	disk := diskFn(in.DataPath)
	disk.Label = safeDiskLabel(in.DataPath, disk.Label)
	t.Capacity.Disk = disk

	// Services — prefer the live listener snapshot when the
	// coremail runtime module has populated it (ADMIN-LISTENER-
	// TRACKING-2C). When the snapshot is nil or empty we fall
	// back to the pre-tracking "unknown" listener state.
	t.Services = map[string]Service{
		"api":        newAPIService(),
		"smtp":       listenerOrFallback(in.ListenerSnapshot, ListenerSMTP, in.SMHTTPPort, "SMTP"),
		"submission": listenerOrFallback(in.ListenerSnapshot, ListenerSubmission, 0, "Submission"),
		"smtps":      listenerOrFallback(in.ListenerSnapshot, ListenerSMTPS, 0, "SMTPS"),
		"imap":       listenerOrFallback(in.ListenerSnapshot, ListenerIMAP, in.IMAPPort, "IMAP"),
		"imaps":      listenerOrFallback(in.ListenerSnapshot, ListenerIMAPS, 0, "IMAPS"),
		"pop3":       listenerOrFallback(in.ListenerSnapshot, ListenerPOP3, in.POP3Port, "POP3"),
		"pop3s":      listenerOrFallback(in.ListenerSnapshot, ListenerPOP3S, 0, "POP3S"),
		"jmap":       listenerOrFallback(in.ListenerSnapshot, ListenerJMAP, in.JMAPPort, "JMAP"),
		"database":   newDatabaseService(in.DBPing),
		"queue":      newQueueService(in.QueueCounts),
	}

	// Queue counts — pass through; zero values are honest when
	// the caller did not configure the queue repo.
	t.Queue = in.QueueCounts

	// License posture — the caller assembles this from config +
	// DB; the package does not need direct license-package deps.
	t.License = in.License
	if t.License.Status == "" {
		t.License.Status = "unknown"
		if t.License.PublicKeyLoaded {
			t.License.Status = "ok"
		} else if t.License.Mode == "offline" {
			t.License.Status = "offline"
		}
	}
	if t.License.Mode == "" {
		t.License.Mode = "unknown"
	}

	// Roll up server-side warnings. The dashboard may layer its
	// own client-side warnings on top.
	t.Warnings = buildWarnings(in, t)

	if t.Status == "ok" && len(t.Warnings) > 0 {
		t.Status = "degraded"
	}
	return t
}

// buildWarnings produces a deduped set of warnings from the live
// state. The function never panics on nil input.
func buildWarnings(in Inputs, t Telemetry) []Warning {
	var out []Warning
	if in.StartedAt.IsZero() {
		out = append(out, Warning{Level: "warn", Code: "telemetry_incomplete", Message: "Telemetry incomplete: process start time not reported"})
	}
	if t.License.PublicKeyState == "missing" {
		out = append(out, Warning{Level: "warn", Code: "license_public_key_missing", Message: "License public key missing"})
	} else if t.License.PublicKeyState == "invalid" {
		out = append(out, Warning{Level: "warn", Code: "license_public_key_invalid", Message: "License public key invalid"})
	}
	if t.License.PublicKeyState == "loaded" && t.License.ValidationState != "valid" {
		out = append(out, Warning{Level: "warn", Code: "license_validation_offline", Message: "License validation offline"})
	}
	if t.License.Mode == "missing" || t.License.PublicKeyState == "missing" {
		out = append(out, Warning{Level: "warn", Code: "license_missing", Message: "License not configured"})
	}
	if in.QueueCounts.Deferred > 0 {
		out = append(out, Warning{Level: "warn", Code: "queue_deferred", Message: "Queue has deferred messages"})
	}
	if in.QueueCounts.Bounced > 0 {
		out = append(out, Warning{Level: "warn", Code: "queue_bounced", Message: "Queue has bounced messages"})
	}
	if t.Capacity.Disk.UsedPercent >= 85 {
		out = append(out, Warning{Level: "warn", Code: "disk_high", Message: "Disk usage high"})
	}
	// A listener whose port is non-zero is still "unknown" in
	// status (we have no runtime listener-state tracking today);
	// we do not emit a warning for the unknown state because it
	// would flood the dashboard. The dashboard may add its own
	// "Telemetry incomplete" notice from the telemetry_incomplete
	// warning above.
	return out
}

// safeDiskLabel picks a non-empty, non-absolute label for the disk
// row. Absolute paths must NEVER reach the response.
func safeDiskLabel(dataPath, hinted string) string {
	if hinted != "" {
		// Defensive: even if a caller passes a path-like
		// string, treat it as untrusted and override.
		if isAbsolute(hinted) {
			return "data"
		}
		return hinted
	}
	if dataPath == "" {
		return "data"
	}
	return "data"
}

// isAbsolute is a tiny replacement for filepath.IsAbsolute that
// avoids importing path/filepath (which would add platform-specific
// quirks to a small helper). We treat a leading slash or drive
// letter as absolute.
func isAbsolute(s string) bool {
	if s == "" {
		return false
	}
	if s[0] == '/' || s[0] == '\\' {
		return true
	}
	// Windows drive letter: "C:", "D:" ...
	if len(s) >= 2 && s[1] == ':' {
		c := s[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}

// newAPIService reports "ok" when this function is called at all —
// the only way an admin can call /api/v1/admin/runtime is for the
// admin API to be responsive. If the caller wires a custom ping
// later, they can override through Inputs (future extension).
func newAPIService() Service {
	return Service{Status: "ok", Detail: "admin API responding"}
}

// newListenerService builds a service entry whose status is
// honestly "unknown" because runtime listener start state is not
// tracked today. The detail string is stable so the dashboard can
// show a single line. The port comes from config (cfg.CoreMail).
func newListenerService(port int, name string) Service {
	return Service{
		Status: "unknown",
		Detail: "listener runtime state not reported",
		Port:   port,
	}
}

// newJMAPService is identical to newListenerService in this build.
// The two are split for clarity in case listener-state tracking is
// added later — JMAP runs on the admin port, so a future revision
// could wire a real health check here.
func newJMAPService(port int) Service {
	if port == 0 {
		return Service{Status: "unknown", Detail: "listener runtime state not reported"}
	}
	return Service{
		Status: "unknown",
		Detail: "listener runtime state not reported",
		Port:   port,
	}
}

// listenerOrFallback returns the service entry from the live
// listener snapshot when available, or the pre-tracking fallback
// (unknown + "listener runtime state not reported") when the
// snapshot is nil or the kind is missing from the snapshot.
func listenerOrFallback(snapshot map[ListenerKind]ListenerStatus, kind ListenerKind, port int, _ string) Service {
	if snapshot != nil {
		if s, ok := snapshot[kind]; ok {
			return Service{Status: s.Status, State: s.State, Detail: s.Detail, Port: s.Port}
		}
	}
	return newListenerService(port, string(kind))
}

// newDatabaseService reports ok / fail / unknown based on the
// supplied ping callback. A nil callback is treated as "unknown" so
// the dashboard does not falsely report the database as healthy.
func newDatabaseService(ping func() error) Service {
	if ping == nil {
		return Service{Status: "unknown", Detail: "database health check not configured"}
	}
	if err := ping(); err != nil {
		return Service{Status: "fail", Detail: "database ping failed"}
	}
	return Service{Status: "ok", Detail: "database ping ok"}
}

// newQueueService derives a queue service entry from the live
// counts. We never report "ok" for an empty queue; a missing repo
// is reported as "unknown" so the dashboard can distinguish a
// stopped delivery worker from a quiet one.
func newQueueService(q QueueCounts) Service {
	if q.Pending == 0 && q.Deferred == 0 && q.Bounced == 0 && q.Delivered == 0 {
		// The counts are all zero. That can mean either
		// "nothing has ever been queued" or "no queue repo
		// wired". We surface the latter as "unknown" so the
		// operator is not falsely reassured.
		return Service{Status: "unknown", Detail: "queue summary not reported"}
	}
	switch {
	case q.Bounced > 0:
		return Service{Status: "fail", Detail: "queue has bounced messages"}
	case q.Deferred > 0:
		return Service{Status: "warn", Detail: "queue has deferred messages"}
	default:
		return Service{Status: "ok", Detail: "queue summary loaded"}
	}
}

// defaultDiskUsage is the per-platform statfs shim. On Windows it
// returns zeroed fields (matching internal/monitoring's behavior);
// on Linux / macOS it reads statfs(2). The dashboard renders the
// zero case as "Not reported" via the existing formatter.
var defaultDiskUsage DiskUsageFn = platformDiskUsage

// buildID is a small atomic counter so callers that need a process
// fingerprint (logs only) can ask for one without us introducing a
// private global. It is intentionally not exposed in the response.
var buildID atomic.Uint64

// NextBuildID returns a monotonic in-process id. Useful for log
// correlation in tests.
func NextBuildID() uint64 {
	return buildID.Add(1)
}
