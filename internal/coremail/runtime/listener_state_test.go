package runtime

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/config"
	orvixruntime "github.com/orvix/orvix/internal/runtime"
	"go.uber.org/zap"
)

// listenerStateHarness builds a coremail module bound to a real
// listener registry, with the listener ports allocated against free
// loopback ports. Tests can call Start() and then read the registry
// Snapshot to observe the actual state produced by the runtime.
type listenerStateHarness struct {
	t         *testing.T
	mod       *Module
	reg       *orvixruntime.ListenerRegistry
	dir       string
	smtpPort  int
	imapPort  int
	pop3Port  int
	subPort   int
	smtpsPort int
	imapsPort int
	pop3sPort int
	jmapPort  int
}

func newListenerStateHarness(t *testing.T) *listenerStateHarness {
	t.Helper()
	dir := t.TempDir()
	sqlDB := testRuntimeDB(t, dir)
	t.Cleanup(func() { sqlDB.Close() })

	reg := orvixruntime.NewListenerRegistry()
	mod := New(zap.NewNop())
	mod.SetListenerRegistry(reg)
	mod.db = sqlDB
	h := &listenerStateHarness{
		t:         t,
		mod:       mod,
		reg:       reg,
		dir:       dir,
		smtpPort:  freePort(t),
		imapPort:  freePort(t),
		pop3Port:  freePort(t),
		subPort:   freePort(t),
		smtpsPort: freePort(t),
		imapsPort: freePort(t),
		pop3sPort: freePort(t),
		jmapPort:  freePort(t),
	}
	t.Cleanup(func() { mod.Stop() })
	return h
}

func (h *listenerStateHarness) cfg() *config.Config {
	cfg := config.Defaults()
	cfg.CoreMail.Enabled = true
	cfg.CoreMail.Hostname = "test.orvix.local"
	cfg.CoreMail.MailStorePath = filepath.Join(h.dir, "msgs")
	cfg.CoreMail.QueueWorkers = 1
	cfg.CoreMail.SMTPHost = "127.0.0.1"
	cfg.CoreMail.SMTPPort = h.smtpPort
	cfg.CoreMail.IMAPHost = "127.0.0.1"
	cfg.CoreMail.IMAPPort = h.imapPort
	cfg.CoreMail.POP3Host = "127.0.0.1"
	cfg.CoreMail.POP3Port = h.pop3Port
	cfg.CoreMail.JMAPHost = "127.0.0.1"
	cfg.CoreMail.JMAPPort = h.jmapPort
	cfg.CoreMail.SubmissionHost = "127.0.0.1"
	cfg.CoreMail.SubmissionPort = h.subPort
	cfg.CoreMail.SMTPsHost = "127.0.0.1"
	cfg.CoreMail.SMTPsPort = h.smtpsPort
	cfg.CoreMail.IMAPsHost = "127.0.0.1"
	cfg.CoreMail.IMAPsPort = h.imapsPort
	cfg.CoreMail.POP3sHost = "127.0.0.1"
	cfg.CoreMail.POP3sPort = h.pop3sPort
	return cfg
}

func (h *listenerStateHarness) initAndStart(t *testing.T) {
	t.Helper()
	cfg := h.cfg()
	h.mod.cfg = cfg
	if err := h.mod.initCore(cfg, h.mod.db); err != nil {
		t.Fatalf("init core: %v", err)
	}
	if err := h.mod.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func (h *listenerStateHarness) waitForState(t *testing.T, kind orvixruntime.ListenerKind, wantState string, deadline time.Duration) orvixruntime.ListenerStatus {
	t.Helper()
	end := time.Now().Add(deadline)
	var last orvixruntime.ListenerStatus
	for time.Now().Before(end) {
		s := h.reg.Snapshot()[kind]
		if s.State == wantState {
			return s
		}
		last = s
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener %s did not reach state=%s within %s; last=%+v", kind, wantState, deadline, last)
	return orvixruntime.ListenerStatus{}
}

func (h *listenerStateHarness) waitForStateNotUnknown(t *testing.T, kind orvixruntime.ListenerKind, deadline time.Duration) orvixruntime.ListenerStatus {
	t.Helper()
	end := time.Now().Add(deadline)
	var last orvixruntime.ListenerStatus
	for time.Now().Before(end) {
		s := h.reg.Snapshot()[kind]
		if s.State != orvixruntime.StateUnknown {
			return s
		}
		last = s
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("listener %s state remained unknown within %s; last=%+v", kind, deadline, last)
	return orvixruntime.ListenerStatus{}
}

// TestListenerStatePlainListenersActive proves SMTP, IMAP, POP3, and
// JMAP reach the normalized "active" state when the runtime boots
// with a normal test config. The registry state matches the actual
// bind result — no config-derived "active" without a real socket.
func TestListenerStatePlainListenersActive(t *testing.T) {
	h := newListenerStateHarness(t)
	h.initAndStart(t)

	for _, kind := range []orvixruntime.ListenerKind{
		orvixruntime.ListenerSMTP,
		orvixruntime.ListenerIMAP,
		orvixruntime.ListenerPOP3,
		orvixruntime.ListenerJMAP,
	} {
		s := h.waitForState(t, kind, orvixruntime.StateActive, 3*time.Second)
		// Real TCP connect confirms the registry is grounded in reality.
		addr := fmt.Sprintf("127.0.0.1:%d", s.Port)
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Fatalf("%s reported active but port %d is unreachable: %v", kind, s.Port, err)
		}
		_ = conn.Close()
	}
}

// TestListenerStateSecureListenersSkipWhenNoCert proves IMAPS/POP3S
// report the normalized "skipped" state when no TLS cert is
// configured, and the runtime does not claim they are active. SMTPS
// is also skipped (not yet implemented); the registry must honestly
// report that, not fake an active listener.
func TestListenerStateSecureListenersSkipWhenNoCert(t *testing.T) {
	h := newListenerStateHarness(t)
	h.initAndStart(t)

	for _, kind := range []orvixruntime.ListenerKind{
		orvixruntime.ListenerSMTPS,
		orvixruntime.ListenerIMAPS,
		orvixruntime.ListenerPOP3S,
	} {
		s := h.waitForStateNotUnknown(t, kind, 2*time.Second)
		if s.State == orvixruntime.StateActive {
			t.Fatalf("%s must not be active without cert/key; got %+v", kind, s)
		}
	}

	for _, port := range []int{h.imapsPort, h.pop3sPort} {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Errorf("port %d is bound even though IMAPS/POP3S cert is missing", port)
		}
	}
}

// TestListenerStateSecureListenerActiveWithCert proves IMAPS reaches
// the normalized "active" state when a valid cert+key are configured,
// and a real socket is bound on the configured port.
func TestListenerStateSecureListenerActiveWithCert(t *testing.T) {
	h := newListenerStateHarness(t)
	certPEM, keyPEM := generateRuntimeTestCert(t)
	certFile := filepath.Join(h.dir, "cert.pem")
	keyFile := filepath.Join(h.dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	cfg := h.cfg()
	cfg.CoreMail.IMAPsEnabled = true
	cfg.CoreMail.IMAPsHost = "127.0.0.1"
	cfg.CoreMail.IMAPsPort = h.imapsPort
	cfg.CoreMail.TLSCertFile = certFile
	cfg.CoreMail.TLSKeyFile = keyFile
	h.mod.cfg = cfg
	if err := h.mod.initCore(cfg, h.mod.db); err != nil {
		t.Fatalf("init core: %v", err)
	}
	if h.mod.imapsServer == nil {
		t.Fatal("imaps server must be initialized with valid cert")
	}
	if err := h.mod.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	s := h.waitForState(t, orvixruntime.ListenerIMAPS, orvixruntime.StateActive, 3*time.Second)
	if s.Port != h.imapsPort {
		t.Errorf("IMAPS active on port %d, want %d", s.Port, h.imapsPort)
	}
	// And the socket is actually bound.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port), time.Second)
	if err != nil {
		t.Fatalf("IMAPS reported active but port is unreachable: %v", err)
	}
	_ = conn.Close()
}

// TestListenerStatePortConflictFails proves that when a port is held
// by another process, the corresponding listener is reported as
// "failed" with a safe address-in-use detail — never active, and
// never silently skipped.
func TestListenerStatePortConflictFails(t *testing.T) {
	hold, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("hold listen: %v", err)
	}
	defer hold.Close()
	conflictPort := hold.Addr().(*net.TCPAddr).Port

	h := newListenerStateHarness(t)
	cfg := h.cfg()
	cfg.CoreMail.SMTPHost = "127.0.0.1"
	cfg.CoreMail.SMTPPort = conflictPort
	h.mod.cfg = cfg
	if err := h.mod.initCore(cfg, h.mod.db); err != nil {
		t.Fatalf("init core: %v", err)
	}
	if err := h.mod.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	s := h.waitForState(t, orvixruntime.ListenerSMTP, orvixruntime.StateFailed, 3*time.Second)
	if s.Status != "fail" {
		t.Errorf("legacy Status must be 'fail' on port conflict; got %q", s.Status)
	}
	if s.Detail != "bind failed: address already in use" {
		t.Errorf("port-conflict detail must be the safe address-in-use summary; got %q", s.Detail)
	}
	if s.Port != conflictPort {
		t.Errorf("failed listener must retain its port (%d); got %d", conflictPort, s.Port)
	}
}

// TestListenerStateReachesRuntimeTelemetry proves the registry's
// actual listener state is carried into the runtime telemetry Service
// entries, which is what the /admin/runtime endpoint renders.
func TestListenerStateReachesRuntimeTelemetry(t *testing.T) {
	h := newListenerStateHarness(t)
	h.initAndStart(t)
	_ = h.waitForState(t, orvixruntime.ListenerSMTP, orvixruntime.StateActive, 3*time.Second)

	snap := h.reg.Snapshot()
	tel := orvixruntime.NewTelemetry(orvixruntime.Inputs{
		Version:          "test",
		StartedAt:        time.Now().Add(-time.Minute),
		DBPing:           func() error { return nil },
		ListenerSnapshot: snap,
		SMHTTPPort:       h.smtpPort,
		IMAPPort:         h.imapPort,
		POP3Port:         h.pop3Port,
		JMAPPort:         h.jmapPort,
	})

	if tel.Services["smtp"].State != orvixruntime.StateActive {
		t.Errorf("telemetry smtp state = %q, want %q", tel.Services["smtp"].State, orvixruntime.StateActive)
	}
	if tel.Services["imap"].State != orvixruntime.StateActive {
		t.Errorf("telemetry imap state = %q, want %q", tel.Services["imap"].State, orvixruntime.StateActive)
	}
	if tel.Services["pop3"].State != orvixruntime.StateActive {
		t.Errorf("telemetry pop3 state = %q, want %q", tel.Services["pop3"].State, orvixruntime.StateActive)
	}
	if tel.Services["imaps"].State == orvixruntime.StateActive {
		t.Errorf("telemetry imaps must NOT be active without cert; got %q", tel.Services["imaps"].State)
	}
	if tel.Services["pop3s"].State == orvixruntime.StateActive {
		t.Errorf("telemetry pop3s must NOT be active without cert; got %q", tel.Services["pop3s"].State)
	}
}

// TestListenerStateDisabledByConfigSkipped proves that when the
// coremail subsystem itself is disabled, the registry reports every
// listener as "skipped" — never active. The admin endpoint must never
// claim a listener is up when it was never started.
func TestListenerStateDisabledByConfigSkipped(t *testing.T) {
	h := newListenerStateHarness(t)
	cfg := h.cfg()
	cfg.CoreMail.Enabled = false
	h.mod.cfg = cfg
	if err := h.mod.initCore(cfg, h.mod.db); err != nil {
		t.Fatalf("init core: %v", err)
	}
	if err := h.mod.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	snap := h.reg.Snapshot()
	for kind, st := range snap {
		if st.State == orvixruntime.StateActive {
			t.Errorf("coremail disabled but %s reports active: %+v", kind, st)
		}
	}
}
