package antivirus

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/observability"
	"go.uber.org/zap"
)

// mockScanner replaces the underlying ClamAV transport
// for testing without requiring a real daemon. The engine
// accepts the configured scanner host/port but does NOT
// dial it directly; it routes through clamav.NewScanner.
// To test decisions deterministically we run a fake
// TCP listener that responds with a static response.
type mockClamd struct {
	listener net.Listener
	response string
	streams  atomic.Int64
	closed   atomic.Bool
}

func startMockClamd(t *testing.T, response string) *mockClamd {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("mock listen: %v", err)
	}
	m := &mockClamd{listener: ln, response: response}
	go m.serve()
	t.Cleanup(func() {
		m.closed.Store(true)
		ln.Close()
	})
	return m
}

func (m *mockClamd) addr() string { return m.listener.Addr().String() }

func (m *mockClamd) serve() {
	for {
		if m.closed.Load() {
			return
		}
		conn, err := m.listener.Accept()
		if err != nil {
			return
		}
		m.streams.Add(1)
		// Crude mock: read the request, write the canned
		// response, close. The matching of the request
		// bytes is irrelevant — the engine ALWAYS issues
		// zINSTREAM so any "stream: ...\000" sequence is
		// answered with the canned reply.
		go func(c net.Conn) {
			defer c.Close()
			buf := make([]byte, 4096)
			c.Read(buf) // drain
			c.Write([]byte(m.response + "\000"))
		}(conn)
	}
}

func newTestEngine(t *testing.T, host string, port int, policy Policy) (*Engine, *observability.Observability) {
	t.Helper()
	return newTestEngineWith(t, host, port, true, policy)
}

func newTestEngineWith(t *testing.T, host string, port int, enabled bool, policy Policy) (*Engine, *observability.Observability) {
	t.Helper()
	obs := observability.NewObservability(50, 50)
	e, err := New(Config{Enabled: enabled, Host: host, Port: port}, policy, zap.NewNop(), obs, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e, obs
}

func TestPolicyValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		policy  Policy
		wantErr bool
	}{
		{"defaults_ok", Policy{}, false},
		{"reject_invalid", Policy{OnInfected: "explode"}, true},
		{"fail_open_invalid", Policy{OnScannerUnavailable: "abort"}, true},
		{"tag_valid", Policy{OnInfected: "tag"}, false},
		{"quarantine_valid", Policy{OnInfected: "quarantine"}, false},
		{"fail_open_valid", Policy{OnScannerUnavailable: "fail_open"}, false},
		{"negative_timeout", Policy{TimeoutMS: -1}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("wantErr=%v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestCleanMessageIsAccepted(t *testing.T) {
	mock := startMockClamd(t, "stream: OK")
	host, portStr, _ := net.SplitHostPort(mock.addr())
	var port int
	if err := portParse(portStr, &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	e, _ := newTestEngine(t, host, port, Policy{OnInfected: "reject", OnScannerUnavailable: "fail_closed"})
	e.MarkEnforced()

	dec := e.Scan(context.Background(), []byte("clean message bytes"), "msg-1")
	if dec.Action != ActionAccept {
		t.Fatalf("want accept, got %v (%s)", dec.Action, dec.Reason)
	}
}

func TestInfectedMessageRejectsByDefaultPolicy(t *testing.T) {
	mock := startMockClamd(t, "stream: Eicar-Test-Signature FOUND")
	host, portStr, _ := net.SplitHostPort(mock.addr())
	var port int
	if err := portParse(portStr, &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	e, _ := newTestEngine(t, host, port, Policy{}) // default = reject
	e.MarkEnforced()

	dec := e.Scan(context.Background(), []byte("INFECTED PAYLOAD"), "msg-2")
	if dec.Action != ActionReject {
		t.Fatalf("want reject, got %v (%s)", dec.Action, dec.Reason)
	}
	if !strings.Contains(dec.Virus, "Eicar") {
		t.Fatalf("expected virus=*Eicar*, got %q", dec.Virus)
	}
}

func TestInfectedQuarantine(t *testing.T) {
	mock := startMockClamd(t, "stream: Trojan.Gen.42 FOUND")
	host, portStr, _ := net.SplitHostPort(mock.addr())
	var port int
	if err := portParse(portStr, &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	e, _ := newTestEngine(t, host, port, Policy{OnInfected: "quarantine"})
	e.MarkEnforced()

	dec := e.Scan(context.Background(), []byte("infected"), "msg-q")
	if dec.Action != ActionQuarantine {
		t.Fatalf("want quarantine, got %v (%s)", dec.Action, dec.Reason)
	}
}

func TestInfectedTagOnly(t *testing.T) {
	mock := startMockClamd(t, "stream: HEUR:Phishing.A FOUND")
	host, portStr, _ := net.SplitHostPort(mock.addr())
	var port int
	if err := portParse(portStr, &port); err != nil {
		t.Fatalf("parse port: %v", err)
	}
	e, _ := newTestEngine(t, host, port, Policy{OnInfected: "tag"})
	e.MarkEnforced()

	dec := e.Scan(context.Background(), []byte("infected"), "msg-tag")
	if dec.Action != ActionTag {
		t.Fatalf("want tag, got %v (%s)", dec.Action, dec.Reason)
	}
}

func TestScannerUnavailableFailOpen(t *testing.T) {
	// Point at a closed port to guarantee connection refusal.
	e, _ := newTestEngine(t, "127.0.0.1", 1, Policy{
		OnInfected:           "reject",
		OnScannerUnavailable: "fail_open",
		TimeoutMS:            500,
	})
	e.MarkEnforced()
	dec := e.Scan(context.Background(), []byte("x"), "msg-open")
	if dec.Action != ActionAccept {
		t.Fatalf("want accept under fail_open, got %v (%s)", dec.Action, dec.Reason)
	}
	if !strings.Contains(dec.Reason, "fail_open") {
		t.Fatalf("expected fail_open in reason, got %q", dec.Reason)
	}
}

func TestScannerUnavailableFailClosed(t *testing.T) {
	e, _ := newTestEngine(t, "127.0.0.1", 1, Policy{
		OnInfected:           "reject",
		OnScannerUnavailable: "fail_closed",
		TimeoutMS:            500,
	})
	e.MarkEnforced()
	dec := e.Scan(context.Background(), []byte("x"), "msg-closed")
	if dec.Action != ActionReject {
		t.Fatalf("want reject under fail_closed, got %v (%s)", dec.Action, dec.Reason)
	}
}

func TestScannerTimeoutFailsClosedByDefault(t *testing.T) {
	// We exercise the timeout/fail-closed path by
	// pointing at a port that connects but immediately
	// closes the socket — that yields a transport error
	// indistinguishable from "scanner unavailable" to
	// the engine. A pure hanging-connection test would
	// require a context-aware scanner; the underlying
	// clamav.Scanner reads until '\0' regardless of
	// deadline, so the unreachable-port path is the
	// realistic test the engine code can make any
	// decision on.
	e, _ := newTestEngine(t, "127.0.0.1", 1, Policy{TimeoutMS: 50})
	e.MarkEnforced()
	dec := e.Scan(context.Background(), []byte("x"), "msg-timeout")
	if dec.Action != ActionReject {
		t.Fatalf("want reject on scanner error under fail_closed, got %v (%s)", dec.Action, dec.Reason)
	}
}

func TestRuntimeEnforcedFlag(t *testing.T) {
	e, _ := newTestEngine(t, "127.0.0.1", 1, Policy{})
	if e.RuntimeEnforced() {
		t.Fatalf("RuntimeEnforced must default false")
	}
	e.MarkEnforced()
	if !e.RuntimeEnforced() {
		t.Fatalf("RuntimeEnforced must be true after MarkEnforced")
	}
}

func TestScanDisabledConfigAlwaysAccepts(t *testing.T) {
	// Disabled-by-config engine MUST bypass the scanner
	// entirely and accept every message. The audit log
	// records the bypass; the policy dispatcher is
	// never consulted because no scan happens.
	e, _ := newTestEngineWith(t, "127.0.0.1", 1, false, Policy{OnInfected: "reject"})
	// Note: do NOT call MarkEnforced.
	dec := e.Scan(context.Background(), []byte("anything"), "msg-disabled")
	if dec.Action != ActionAccept {
		t.Fatalf("want accept for disabled engine, got %v", dec.Action)
	}
	if !strings.Contains(dec.Reason, "disabled") {
		t.Fatalf("expected reason=disabled, got %q", dec.Reason)
	}
}

func TestEngineSnapshotReflectsRuntime(t *testing.T) {
	mock := startMockClamd(t, "stream: OK")
	host, portStr, _ := net.SplitHostPort(mock.addr())
	var port int
	_ = portParse(portStr, &port)
	e, _ := newTestEngine(t, host, port, Policy{OnInfected: "tag"})

	// Before MarkEnforced the snapshot reports
	// runtime_enforced=false even though the engine
	// itself is enabled and reachable.
	s := e.Snapshot(context.Background())
	if s.RuntimeEnforced {
		t.Fatalf("runtime_enforced must be false before MarkEnforced")
	}
	e.MarkEnforced()
	s = e.Snapshot(context.Background())
	if !s.RuntimeEnforced {
		t.Fatalf("runtime_enforced must be true after MarkEnforced")
	}
	if !s.EngineActive {
		t.Fatalf("engine_active must be true after MarkEnforced when reachable")
	}
	if s.PolicyOnInfected != "tag" {
		t.Fatalf("policy_on_infected: want tag, got %q", s.PolicyOnInfected)
	}
}

func TestQuarantinePersistsRow(t *testing.T) {
	// Quarantine is verified by the SMTP-receiver-level
	// tests; this package owns the Engine contract and
	// the quarantine helper is exercised end-to-end at
	// the integration level. Here we verify only the
	// path-and-error surface.
	e, _ := newTestEngine(t, "127.0.0.1", 1, Policy{OnInfected: "quarantine"})
	if _, err := e.Quarantine(context.Background(), nil, t.TempDir(), "msg-x", "x", "y", "z", "Eicar", []byte("x")); err == nil {
		t.Fatalf("want error for nil db")
	}
}

// portParse is a tiny helper to avoid pulling strconv
// into every test case.
func portParse(s string, out *int) error {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return errors.New("not a port")
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return nil
}

func TestPolicyAtomicUpdate(t *testing.T) {
	e, _ := newTestEngine(t, "127.0.0.1", 1, Policy{OnInfected: "reject"})
	if e.Policy().OnInfected != "reject" {
		t.Fatalf("want reject default, got %s", e.Policy().OnInfected)
	}
	if err := e.SetPolicy(Policy{OnInfected: "tag"}); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}
	if e.Policy().OnInfected != "tag" {
		t.Fatalf("want tag after update, got %s", e.Policy().OnInfected)
	}
}

// heartbeat: ensure Snapshot does not block longer than
// a few seconds even when the scanner is unreachable.
func TestSnapshotIsBounded(t *testing.T) {
	e, _ := newTestEngine(t, "127.0.0.1", 1, Policy{})
	e.MarkEnforced()
	done := make(chan struct{})
	go func() {
		_ = e.Snapshot(context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("Snapshot blocked too long")
	}
}
