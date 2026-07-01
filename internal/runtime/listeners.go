package runtime

import (
	"sync"
)

// ListenerKind is a stable identifier for a listener slot.
type ListenerKind string

const (
	ListenerSMTP        ListenerKind = "smtp"
	ListenerSubmission  ListenerKind = "smtp-submission"
	ListenerSMTPS       ListenerKind = "smtps"
	ListenerIMAP        ListenerKind = "imap"
	ListenerIMAPS       ListenerKind = "imaps"
	ListenerPOP3        ListenerKind = "pop3"
	ListenerPOP3S       ListenerKind = "pop3s"
	ListenerJMAP        ListenerKind = "jmap"
)

// ListenerStatus represents the runtime state of a single
// protocol listener. It is safe to JSON-encode.
//
// Two fields describe state:
//   - Status is the legacy vocabulary ("ok" | "fail" | "disabled" |
//     "unknown") kept for backward compatibility with existing admin
//     clients and tests.
//   - State is the normalized proof vocabulary ("active" | "skipped" |
//     "degraded" | "failed" | "unknown"). It reflects the ACTUAL bind
//     result recorded by the runtime, never a value derived from config
//     alone. A listener is only "active" when the runtime observed a
//     successful bind.
type ListenerStatus struct {
	Status string `json:"status"` // "ok" | "fail" | "disabled" | "unknown"
	State  string `json:"state"`  // "active" | "skipped" | "degraded" | "failed" | "unknown"
	Detail string `json:"detail"` // safe human-readable detail, never a secret or path
	Port   int    `json:"port,omitempty"`
}

// Listener state constants (the normalized proof vocabulary).
const (
	StateActive   = "active"
	StateSkipped  = "skipped"
	StateDegraded = "degraded"
	StateFailed   = "failed"
	StateUnknown  = "unknown"
)

// ListenerRegistry is a thread-safe store for listener runtime
// state. It is populated at process startup by the coremail
// runtime module and read by the admin telemetry endpoint.
// Methods are safe to call from any goroutine; zero value is
// ready to use (all listeners default to unknown).
type ListenerRegistry struct {
	mu  sync.RWMutex
	all map[ListenerKind]ListenerStatus
}

// NewListenerRegistry creates a ready-to-use listener registry.
func NewListenerRegistry() *ListenerRegistry {
	return &ListenerRegistry{all: make(map[ListenerKind]ListenerStatus)}
}

func (r *ListenerRegistry) init() {
	if r.all == nil {
		r.all = make(map[ListenerKind]ListenerStatus)
	}
}

// MarkStarting records that a listener is being started. The
// detail is "starting" so the dashboard does not show unknown
// during the brief window between goroutine launch and bind.
func (r *ListenerRegistry) MarkStarting(kind ListenerKind, port int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	r.all[kind] = ListenerStatus{
		Status: "unknown",
		State:  StateUnknown,
		Detail: "listener starting",
		Port:   port,
	}
}

// MarkOK records that a listener bound and started successfully.
func (r *ListenerRegistry) MarkOK(kind ListenerKind, port int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	r.all[kind] = ListenerStatus{
		Status: "ok",
		State:  StateActive,
		Detail: "listening",
		Port:   port,
	}
}

// MarkFailed records that a listener failed to bind or start.
// The detail is a safe error summary; the original error is
// never exposed raw.
func (r *ListenerRegistry) MarkFailed(kind ListenerKind, port int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	detail := safeListenError(err)
	r.all[kind] = ListenerStatus{
		Status: "fail",
		State:  StateFailed,
		Detail: detail,
		Port:   port,
	}
}

// MarkDisabled records that a listener is disabled by config.
// A config-disabled or safely-skipped listener is reported as
// "skipped": it is intentionally not listening, and the runtime
// never claims it is active.
func (r *ListenerRegistry) MarkDisabled(kind ListenerKind, port int, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	detail := reason
	if detail == "" {
		detail = "disabled by config"
	}
	r.all[kind] = ListenerStatus{
		Status: "disabled",
		State:  StateSkipped,
		Detail: detail,
		Port:   port,
	}
}

// MarkDegraded records that a listener is partially functional: it is
// bound and accepting connections but an important capability is
// impaired (for example, STARTTLS is unavailable because the TLS
// certificate failed to load). The listener is reachable, so Status
// stays "ok" for backward compatibility, but State is "degraded" so the
// admin surface can flag the impairment honestly.
func (r *ListenerRegistry) MarkDegraded(kind ListenerKind, port int, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	detail := reason
	if detail == "" {
		detail = "listener degraded"
	}
	r.all[kind] = ListenerStatus{
		Status: "ok",
		State:  StateDegraded,
		Detail: detail,
		Port:   port,
	}
}

// Snapshot returns a copy of every registered listener state.
// Listeners that have never been recorded appear as unknown.
// The returned map is safe to read without holding the lock.
func (r *ListenerRegistry) Snapshot() map[ListenerKind]ListenerStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[ListenerKind]ListenerStatus, len(r.all))
	for k, v := range r.all {
		out[k] = v
	}
	// Ensure all four slots have at least an unknown entry.
	for _, k := range allKinds {
		if _, ok := out[k]; !ok {
			out[k] = ListenerStatus{Status: "unknown", State: StateUnknown, Detail: "listener runtime state not reported"}
		}
	}
	return out
}

var allKinds = []ListenerKind{ListenerSMTP, ListenerSubmission, ListenerSMTPS, ListenerIMAP, ListenerIMAPS, ListenerPOP3, ListenerPOP3S, ListenerJMAP}

// safeListenError converts a listener bind error into a safe
// detail string. The original error is never exposed verbatim
// because it may contain paths, addresses, or other sensitive
// context.
func safeListenError(err error) string {
	if err == nil {
		return "listener failed"
	}
	s := err.Error()
	// Common safe patterns.
	if isAddrInUse(s) {
		return "bind failed: address already in use"
	}
	if isPermDenied(s) {
		return "bind failed: permission denied"
	}
	if isTLSError(s) {
		return "listener failed to start"
	}
	// Fallback — never expose the raw error.
	return "listener failed to start"
}

func isAddrInUse(s string) bool {
	// Common across platforms.
	phrases := []string{
		"address already in use",
		"EADDRINUSE",
		"bind: address already in use",
		// Windows: WSAEADDRINUSE surfaces as "Only one usage of each
		// socket address (protocol/network address/port) is normally
		// permitted." when a second bind on the same port is attempted.
		"only one usage of each socket address",
	}
	for _, p := range phrases {
		if containsFold(s, p) {
			return true
		}
	}
	return false
}

func isPermDenied(s string) bool {
	phrases := []string{"permission denied", "EACCES", "access denied"}
	for _, p := range phrases {
		if containsFold(s, p) {
			return true
		}
	}
	return false
}

func isTLSError(s string) bool {
	phrases := []string{"tls", "certificate", "handshake failure"}
	for _, p := range phrases {
		if containsFold(s, p) {
			return true
		}
	}
	return false
}

func containsFold(s, sub string) bool {
	if len(s) < len(sub) {
		return false
	}
	// Simple case-insensitive contains without importing strings.
	for i := 0; i <= len(s)-len(sub); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			sc := s[i+j]
			tc := sub[j]
			if sc >= 'A' && sc <= 'Z' {
				sc += 32
			}
			if tc >= 'A' && tc <= 'Z' {
				tc += 32
			}
			if sc != tc {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
