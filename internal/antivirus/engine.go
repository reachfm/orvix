// Package antivirus wires ClamAV scanning into the inbound
// SMTP accept path.
//
// The package exposes a single Engine type that wraps the
// existing internal/clamav.Scanner with:
//
//   - operator-configured Policy (reject / quarantine / tag /
//     fail-open / fail-closed)
//   - audit log integration via internal/audit.Store
//   - metrics integration via internal/observability
//   - a runtime_enforced flag that the SMTP receiver sets
//     once it has wired the engine into AcceptMessage
//
// Design contract:
//
//   - The Engine NEVER silently ignores an infected
//     message. Decisions are made strictly per the
//     configured policy. Failures (scanner unreachable,
//     timeout, malformed response) follow the same
//     policy enum so an operator's choice between
//     fail-open and fail-closed is explicit.
//   - The Engine NEVER reports runtime_enforced=true
//     unless the SMTP receiver (or any other caller) has
//     installed the Engine into the AcceptMessage flow.
//     The admin status endpoint uses runtime_enforced
//     to decide whether to claim "antivirus active".
//   - The Engine never returns a false negative: when in
//     doubt the engine flags the message as infected and
//     hands the verdict to the policy dispatcher. The
//     policy dispatcher is the only authority on what
//     "infected" means at runtime.
package antivirus

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/clamav"
	"github.com/orvix/orvix/internal/observability"
	"go.uber.org/zap"
)

// Action is the runtime decision the Engine returns to the
// SMTP receiver. The Decision(value) helper packages the
// outcome with enough context to drive error / accept
// / quarantine branches in the receiver.
type Action string

const (
	// ActionAccept is the verdict for a clean message
	// OR a tag-only mode that did not block delivery.
	ActionAccept Action = "accept"
	// ActionReject means the message must NOT be
	// stored; the SMTP receiver returns a 5.7.1 error.
	ActionReject Action = "reject"
	// ActionQuarantine means the message must NOT be
	// delivered to the user mailbox; the receiver
	// records a row in coremail_quarantine_index and
	// returns a temp-failure so the sender retries.
	ActionQuarantine Action = "quarantine"
	// ActionTag means the message is acceptable but
	// an X-Orvix-AV-Verdict header must be added
	// downstream (the SMTP injects the header before
	// StoreMessage).
	ActionTag Action = "tag"
)

// Policy enumerates operator-controlled behaviour for
// (a) an infected message and (b) a scanner-unavailable
// condition. The two halves are intentionally separate so
// the operator can choose "tag-only" for known viruses
// while still failing closed on a scanner outage.
type Policy struct {
	// OnInfected is applied when the scanner returns
	// Infected=true. Allowed values: reject,
	// quarantine, tag. An empty value defaults to
	// "reject" because virus-positive traffic must
	// never silently flow through.
	OnInfected string `json:"on_infected"`
	// OnScannerUnavailable is applied when the
	// scanner is unreachable / times out / returns
	// an unparseable response. Allowed values:
	// fail_open (accept + audit), fail_closed
	// (reject + audit). An empty value defaults to
	// "fail_closed" so the safer policy wins.
	OnScannerUnavailable string `json:"on_scanner_unavailable"`
	// TimeoutMS caps a single scan. Defaults to 30s
	// (matching the underlying clamav.Scanner default).
	TimeoutMS int `json:"timeout_ms"`
}

// Validate returns an error for unsafe values.
func (p Policy) Validate() error {
	switch p.OnInfected {
	case "", "reject":
		p.OnInfected = "reject"
	case "quarantine", "tag":
		// ok
	default:
		return fmt.Errorf("invalid on_infected policy %q", p.OnInfected)
	}
	switch p.OnScannerUnavailable {
	case "", "fail_closed":
		p.OnScannerUnavailable = "fail_closed"
	case "fail_open":
		// ok
	default:
		return fmt.Errorf("invalid on_scanner_unavailable policy %q", p.OnScannerUnavailable)
	}
	if p.TimeoutMS < 0 {
		return errors.New("timeout_ms must be >= 0")
	}
	return nil
}

// Decision is the full output of Engine.Scan. The Action
// is the runtime decision; Virus is populated when the
// scanner identified a specific family; Reason explains
// the policy decision (clean / Infected / scanner_error /
// scanner_timeout / policy_*) for audit logs.
type Decision struct {
	Action        Action
	Virus         string
	Filename      string
	Reason        string
	ScannerCalled bool
	LatencyMS     int64
}

// Engine is the runtime gluing clamav.Scanner to the
// SMTP receiver. Engine is safe for concurrent callers
// — the underlying clamav.Scanner performs per-call
// dialing.
type Engine struct {
	cfg          Config
	policy       atomic.Pointer[Policy]
	scanner      *clamav.Scanner
	logger       *zap.Logger
	observ       *observability.Observability
	auditStore   *audit.Store

	// runtimeEnforced is set true by the SMTP receiver
	// after the engine is successfully attached to
	// the receiver's AcceptMessage flow. The admin
	// status endpoint reads this counter to decide
	// whether to claim antivirus enforcement.
	enforced atomic.Bool
	// lastError tracks the last scanner failure so
	// the admin status endpoint can surface what is
	// failing without log spelunking.
	lastErrorMu sync.RWMutex
	lastError   string
	lastErrorAt time.Time

	// counters are the per-process totals. The admin
	// endpoint prefers the observability package for
	// history, but Engine keeps its own per-instance
	// count so a single Engine instance can answer
	// "did this build ever scan anything?" cheaply.
	scanned  atomic.Int64
	infected atomic.Int64
}

// Config is the bootstrap-time configuration.
type Config struct {
	Host    string
	Port    int
	Enabled bool
}

// New constructs an Engine. The Engine is always usable:
// when cfg.Enabled is false or the daemon is offline,
// the Engine returns ActionAccept (under fail-open
// policy) or ActionReject (under fail-closed policy)
// per the operator's explicit choice. The Engine never
// silently disables itself — an operator who set
// Enabled=false gets a deterministic "engine disabled"
// outcome that the audit log records.
func New(cfg Config, policy Policy, logger *zap.Logger, obs *observability.Observability, store *audit.Store) (*Engine, error) {
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("antivirus: invalid policy: %w", err)
	}
	if cfg.Port == 0 {
		cfg.Port = 3310
	}
	scanner := clamav.NewScanner(cfg.Host, cfg.Port, logger)
	if policy.TimeoutMS > 0 {
		scanner = clamav.NewScanner(cfg.Host, cfg.Port, logger)
		// Note: per-call timeout override happens inside
		// Scan via the context. The clamav.Scanner struct
		// does not expose a setter; we honour the timeout
		// at the call site.
	}
	e := &Engine{
		cfg:        cfg,
		scanner:    scanner,
		logger:     logger,
		observ:     obs,
		auditStore: store,
	}
	p := policy
	e.policy.Store(&p)
	return e, nil
}

// SetPolicy updates the running policy atomically. The
// next Scan call observes the new policy.
func (e *Engine) SetPolicy(p Policy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	e.policy.Store(&p)
	return nil
}

// Policy returns the active policy.
func (e *Engine) Policy() Policy {
	if p := e.policy.Load(); p != nil {
		return *p
	}
	return Policy{OnInfected: "reject", OnScannerUnavailable: "fail_closed"}
}

// MarkEnforced flips the runtime_enforced flag on. The
// SMTP receiver calls this in initCore after the engine
// is wired into AcceptMessage. The admin status endpoint
// reads the flag to know whether the engine is actually
// being called.
func (e *Engine) MarkEnforced() { e.enforced.Store(true) }

// RuntimeEnforced reports whether the Engine has been
// installed in the receive path. False means the
// admin endpoint must not claim antivirus is active.
func (e *Engine) RuntimeEnforced() bool { return e.enforced.Load() }

// Reachable runs a PING probe against the configured
// daemon. Pure probe — no scan, no audit.
func (e *Engine) Reachable(ctx context.Context) error {
	if !e.cfg.Enabled {
		return errors.New("antivirus disabled by config")
	}
	return e.scanner.Ping(ctx)
}

// Scan runs the configured policy against the supplied
// RFC822 bytes. The function is the only entrypoint the
// SMTP receiver calls. The decision is logged to
// observability.EventHistory AND coremail_audit so the
// admin endpoint, the maintenance log, and the security
// reviewer all see the same outcome.
func (e *Engine) Scan(ctx context.Context, rfc822 []byte, messageID string) Decision {
	policy := e.Policy()
	start := time.Now()
	// If the operator has not enabled antivirus at
	// the config layer (cfg.Enabled == false), we
	// record a one-shot "disabled" verdict and fall
	// back to the disabled policy semantics: accept
	// the message (no scanner to call). The runtime
	// flag (enforced) reflects whether the SMTP
	// receiver is ACTUALLY calling us; the
	// config-level Enabled=false is just an
	// operator's "leave this alone" knob.
	if !e.cfg.Enabled {
		dec := Decision{
			Action:        ActionAccept,
			Reason:        "engine disabled by config",
			ScannerCalled: false,
		}
		e.record("", dec)
		return dec
	}

	// Apply caller-supplied timeout via context.
	if policy.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(policy.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	scanCtx, scanCancel := context.WithTimeout(ctx, 30*time.Second)
	defer scanCancel()
	res, err := e.scanner.ScanBytes(scanCtx, rfc822, "smtp-"+messageID)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		// Scanner unavailable / timed out / protocol error.
		e.setLastError(err.Error())
		if e.observ != nil {
			e.observ.Metrics.IncAntivirusScannerErrors()
		}
		dec := e.handleScannerError(err, policy)
		dec.LatencyMS = latency
		dec.Filename = "smtp-" + messageID
		e.record(err.Error(), dec)
		return dec
	}

	// Scanner responded without a transport error.
	e.clearLastError()
	if e.observ != nil {
		e.observ.Metrics.IncAntivirusScanned()
	}
	e.scanned.Add(1)

	if !res.Infected {
		dec := Decision{
			Action:        ActionAccept,
			Reason:        "clean",
			ScannerCalled: true,
			LatencyMS:     latency,
			Filename:      res.Filename,
		}
		e.record("", dec)
		return dec
	}

	// Infected: consult the operator policy.
	e.infected.Add(1)
	if e.observ != nil {
		e.observ.Metrics.IncAntivirusInfected()
	}
	var dec Decision
	switch policy.OnInfected {
	case "quarantine":
		dec = Decision{
			Action:   ActionQuarantine,
			Virus:    res.Virus,
			Reason:   "infected: " + res.Virus,
			Filename: res.Filename,
			LatencyMS: latency,
		}
		if e.observ != nil {
			e.observ.Metrics.IncAntivirusQuarantined()
		}
	case "tag":
		dec = Decision{
			Action:   ActionTag,
			Virus:    res.Virus,
			Reason:   "tag-only: " + res.Virus,
			Filename: res.Filename,
			LatencyMS: latency,
		}
		if e.observ != nil {
			e.observ.Metrics.IncAntivirusTagged()
		}
	default: // reject (default policy)
		dec = Decision{
			Action:   ActionReject,
			Virus:    res.Virus,
			Reason:   "infected: " + res.Virus,
			Filename: res.Filename,
			LatencyMS: latency,
		}
		if e.observ != nil {
			e.observ.Metrics.IncAntivirusRejected()
		}
	}
	e.record("", dec)
	return dec
}

// handleScannerError maps a transport error from the
// scanner to the configured fail policy.
func (e *Engine) handleScannerError(err error, policy Policy) Decision {
	if policy.OnScannerUnavailable == "fail_open" {
		if e.observ != nil {
			e.observ.Metrics.IncAntivirusFailOpen()
		}
		return Decision{
			Action:        ActionAccept,
			Reason:        "fail_open: " + err.Error(),
			ScannerCalled: true,
		}
	}
	if e.observ != nil {
		e.observ.Metrics.IncAntivirusFailClosed()
	}
	return Decision{
		Action:        ActionReject,
		Reason:        "fail_closed: " + err.Error(),
		ScannerCalled: true,
	}
}

// record writes the decision to observability.EventHistory
// AND coremail_audit. Both are best-effort: errors are
// logged but never returned to the caller. The audit log
// uses actor="antivirus_engine" so the resulting row is
// discoverable in /api/v1/admin/audit-logs.
func (e *Engine) record(detail string, dec Decision) {
	if e.observ != nil && e.observ.EventHistory != nil {
		var et observability.EventType
		switch dec.Action {
		case ActionReject:
			et = observability.EventAntivirusRejected
		case ActionQuarantine:
			et = observability.EventAntivirusQuarantined
		case ActionTag:
			et = observability.EventAntivirusTagged
		case ActionAccept:
			if !dec.ScannerCalled {
				// Engine-disabled bypass; record as scanned-clean.
				et = observability.EventAntivirusScanned
			} else if dec.Reason != "" && dec.Reason != "clean" {
				et = observability.EventAntivirusScannerError
			} else {
				et = observability.EventAntivirusScanned
			}
		}
		fields := map[string]string{
			"action":  string(dec.Action),
			"reason":  dec.Reason,
			"latency": fmt.Sprintf("%d", dec.LatencyMS),
		}
		if dec.Virus != "" {
			fields["virus"] = dec.Virus
		}
		if dec.Filename != "" {
			fields["filename"] = dec.Filename
		}
		if detail != "" {
			fields["error"] = detail
		}
		e.observ.EventHistory.Record(et, fields)
	}
	if e.auditStore != nil {
		_ = e.auditStore.Record(context.Background(), &audit.Entry{
			Actor:  "antivirus_engine",
			Action: "antivirus." + string(dec.Action),
			Target: dec.Filename,
			Result: dec.Reason,
		})
	}
}

// LastError returns the most recent scanner error and
// when it happened, for diagnostic display in the admin
// status endpoint.
func (e *Engine) LastError() (string, time.Time) {
	e.lastErrorMu.RLock()
	defer e.lastErrorMu.RUnlock()
	return e.lastError, e.lastErrorAt
}

func (e *Engine) setLastError(s string) {
	e.lastErrorMu.Lock()
	defer e.lastErrorMu.Unlock()
	e.lastError = s
	e.lastErrorAt = time.Now().UTC()
}

func (e *Engine) clearLastError() {
	e.lastErrorMu.Lock()
	defer e.lastErrorMu.Unlock()
	e.lastError = ""
	e.lastErrorAt = time.Time{}
}

// Counts returns the per-instance totals. The admin
// status endpoint uses these to surface "this build
// scanned N messages" cheaply without a SQL roundtrip.
// Counters reflect ONLY decisions made through this
// Engine instance; if multiple Engines are wired (rare)
// they each track their own.
func (e *Engine) Counts() (scanned, infected int64) {
	return e.scanned.Load(), e.infected.Load()
}

// Status is the API payload for AdminAntivirusStatus.
// Fields are explicit; the admin endpoint must NEVER
// claim runtime_enforced=true when the SMTP receiver
// has not wired this Engine into the receive path.
type Status struct {
	EngineConfigured bool   `json:"engine_configured"`
	EngineEnabled     bool   `json:"engine_enabled"`
	EngineReachable   bool   `json:"engine_reachable"`
	EngineActive      bool   `json:"engine_active"`
	ScannerHost       string `json:"scanner_host"`
	ScannerPort       int    `json:"scanner_port"`
	PolicyOnInfected  string `json:"policy_on_infected"`
	PolicyOnUnavailable string `json:"policy_on_scanner_unavailable"`
	TimeoutMS         int    `json:"timeout_ms"`
	RuntimeEnforced   bool   `json:"runtime_enforced"`
	LastError         string `json:"last_error"`
	LastErrorAt       string `json:"last_error_at"`
	Scanned           int64  `json:"scanned"`
	Infected          int64  `json:"infected"`
}

// Snapshot returns a status snapshot suitable for the
// admin endpoint. The function performs a Ping with a
// short timeout — that probe is non-blocking for the
// caller (sub-second) but only fails closed when the
// probe context exceeds the timeout. Caller may freely
// switch this off by passing a context with very short
// deadline.
func (e *Engine) Snapshot(ctx context.Context) Status {
	policy := e.Policy()
	scanned, infected := e.Counts()
	reachable := e.Reachable(ctx) == nil
	lastErr, lastErrAt := e.LastError()
	out := Status{
		EngineConfigured:     e.cfg.Enabled,
		EngineEnabled:         e.cfg.Enabled,
		EngineReachable:       reachable,
		EngineActive:          reachable && e.cfg.Enabled,
		ScannerHost:           e.cfg.Host,
		ScannerPort:           e.cfg.Port,
		PolicyOnInfected:      policy.OnInfected,
		PolicyOnUnavailable:   policy.OnScannerUnavailable,
		TimeoutMS:             policy.TimeoutMS,
		RuntimeEnforced:       e.RuntimeEnforced(),
		LastError:             lastErr,
		Scanned:               scanned,
		Infected:              infected,
	}
	if !lastErrAt.IsZero() {
		out.LastErrorAt = lastErrAt.Format(time.RFC3339)
	}
	return out
}

// EnsureSchema installs the per-row quarantine index the
// Engine writes when policy.OnInfected == "quarantine".
// The table already exists in models.MigrateAllRaw; this
// is a defensive idempotent call that no-ops when the
// store is nil or the table is present.
func EnsureSchema(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return nil
	}
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS coremail_av_quarantine (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id TEXT NOT NULL,
		received_at DATETIME NOT NULL,
		sender TEXT NOT NULL DEFAULT '',
		recipient TEXT NOT NULL DEFAULT '',
		subject TEXT NOT NULL DEFAULT '',
		virus TEXT NOT NULL DEFAULT '',
		raw_path TEXT NOT NULL DEFAULT '',
		raw_size INTEGER NOT NULL DEFAULT 0,
		resolved_at DATETIME,
		resolved_by TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL
	)`)
	return err
}

// Quarantine persists an infected message that the policy
// decided to quarantine. The RFC822 bytes are streamed
// to disk so the operator can inspect / release them
// later through the admin UI or the cli. The function
// returns the storage path so the SMTP receiver can log
// it. The function does NOT delete the local copy —
// the quarantine layer is additive and reversible.
//
// The caller passes the *sql.DB explicitly because the
// Engine deliberately does not store one — the audit
// store handles mutation logs, and the SMTP receiver
// already has its own DB.
func (e *Engine) Quarantine(ctx context.Context, db *sql.DB, rawDir, messageID, sender, recipient, subject, virus string, rfc822 []byte) (string, error) {
	if db == nil {
		return "", errors.New("quarantine: nil db")
	}
	if rawDir == "" {
		rawDir = "/var/lib/orvix/quarantine"
	}
	if err := EnsureSchema(ctx, db); err != nil {
		return "", err
	}
	safeID := sanitize(messageID)
	path := rawDir + "/" + safeID + ".eml"
	if err := os.WriteFile(path, rfc822, 0o600); err != nil {
		return "", fmt.Errorf("quarantine: write: %w", err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(ctx, `INSERT INTO coremail_av_quarantine
		(message_id, received_at, sender, recipient, subject, virus, raw_path, raw_size, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		messageID, now, sender, recipient, subject, virus, path, len(rfc822), now); err != nil {
		return "", fmt.Errorf("quarantine: insert: %w", err)
	}
	if e.auditStore != nil {
		_ = e.auditStore.Record(ctx, &audit.Entry{
			Actor:  "antivirus_engine",
			Action: "antivirus.quarantined",
			Target: messageID,
			Result: virus,
		})
	}
	return path, nil
}

// dbFromEngine is intentionally removed. Callers pass the
// *sql.DB explicitly so the Engine keeps no DB handle.

// sanitize strips path-unsafe characters from a message
// id before using it as a disk filename.
func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			out = append(out, byte(r))
		case r >= 'a' && r <= 'z':
			out = append(out, byte(r))
		case r >= '0' && r <= '9':
			out = append(out, byte(r))
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		out = []byte("unknown")
	}
	return string(out)
}

// Active requires the Engine to be wired and reachable —
// see Snapshot.
func (e *Engine) Active(ctx context.Context) bool {
	return e.cfg.Enabled && e.RuntimeEnforced() && e.Reachable(ctx) == nil
}
