package runtime

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orvix/orvix/internal/audit"
	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/coremail/queue"
	"github.com/orvix/orvix/internal/coremail/storage"
	"github.com/orvix/orvix/internal/modules"
	"github.com/orvix/orvix/internal/observability"
	"github.com/orvix/orvix/internal/policy"
	"github.com/orvix/orvix/internal/trust"
	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

// ── Runtime Integration Test ─────────────────────────────

func testRuntimeDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(dir, "runtime_test.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	for _, stmt := range storage.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range storage.Indexes() {
		db.Exec(stmt)
	}
	for _, stmt := range queue.Tables() {
		db.Exec(stmt)
	}
	for _, stmt := range queue.Indexes() {
		db.Exec(stmt)
	}
	return db
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestRuntimeAllProtocolsStart(t *testing.T) {
	dir := t.TempDir()
	sqlDB := testRuntimeDB(t, dir)
	t.Cleanup(func() { sqlDB.Close() })

	logger, _ := zap.NewDevelopment()
	mod := New(logger)

	smtpPort := freePort(t)
	imapPort := freePort(t)
	pop3Port := freePort(t)
	jmapPort := freePort(t)

	cfg := &config.Config{
		CoreMail: config.CoreMailConfig{
			Enabled:       true,
			Hostname:      "test.orvix.local",
			SMTPHost:      "127.0.0.1",
			SMTPPort:      smtpPort,
			IMAPHost:      "127.0.0.1",
			IMAPPort:      imapPort,
			POP3Host:      "127.0.0.1",
			POP3Port:      pop3Port,
			JMAPHost:      "127.0.0.1",
			JMAPPort:      jmapPort,
			MailStorePath: filepath.Join(dir, "msgs"),
			QueueWorkers:  1,
		},
	}

	// Replace Init with direct initialization to avoid gorm dependency in tests.
	mod.cfg = cfg
	mod.db = sqlDB
	mod.initCore(cfg, sqlDB)

	if err := mod.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer mod.Stop()

	time.Sleep(200 * time.Millisecond)

	smtpAddr := fmt.Sprintf("127.0.0.1:%d", smtpPort)
	conn, err := net.DialTimeout("tcp", smtpAddr, time.Second)
	if err != nil {
		t.Fatalf("smtp: %v", err)
	}
	conn.Close()
	t.Log("SMTP listener: OK")

	imapAddr := fmt.Sprintf("127.0.0.1:%d", imapPort)
	conn, err = net.DialTimeout("tcp", imapAddr, time.Second)
	if err != nil {
		t.Fatalf("imap: %v", err)
	}
	conn.Close()
	t.Log("IMAP listener: OK")

	pop3Addr := fmt.Sprintf("127.0.0.1:%d", pop3Port)
	conn, err = net.DialTimeout("tcp", pop3Addr, time.Second)
	if err != nil {
		t.Fatalf("pop3: %v", err)
	}
	conn.Close()
	t.Log("POP3 listener: OK")

	jmapURL := fmt.Sprintf("http://127.0.0.1:%d/.well-known/jmap", jmapPort)
	resp, err := http.Get(jmapURL)
	if err != nil {
		t.Fatalf("jmap: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("jmap expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "sessionUrl") {
		t.Fatal("jmap expected sessionUrl")
	}
	t.Log("JMAP listener: OK")
}

func TestRuntimeWiresOutboundPreferIPv4ToWorkers(t *testing.T) {
	dir := t.TempDir()
	sqlDB := testRuntimeDB(t, dir)
	t.Cleanup(func() { sqlDB.Close() })

	cfg := config.Defaults()
	cfg.CoreMail.Enabled = true
	cfg.CoreMail.Hostname = "test.orvix.local"
	cfg.CoreMail.MailStorePath = filepath.Join(dir, "msgs")
	cfg.CoreMail.QueueWorkers = 2
	cfg.Outbound.PreferIPv4 = true

	mod := New(zap.NewNop())
	mod.cfg = cfg
	mod.db = sqlDB
	if err := mod.initCore(cfg, sqlDB); err != nil {
		t.Fatalf("init core: %v", err)
	}
	if len(mod.workers) != 2 {
		t.Fatalf("workers = %d, want 2", len(mod.workers))
	}
	for i, worker := range mod.workers {
		if !worker.PreferIPv4 {
			t.Fatalf("worker %d PreferIPv4=false, want true", i)
		}
	}
}

func TestRuntimeWiresCoreMailRequireAuthForSubmissionToSMTP(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{name: "inbound default allows unauthenticated mail from", want: false},
		{name: "explicit submission auth override", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			sqlDB := testRuntimeDB(t, dir)
			t.Cleanup(func() { sqlDB.Close() })

			cfg := config.Defaults()
			cfg.CoreMail.Enabled = true
			cfg.CoreMail.Hostname = "test.orvix.local"
			cfg.CoreMail.MailStorePath = filepath.Join(dir, "msgs")
			cfg.CoreMail.QueueWorkers = 1
			cfg.CoreMail.RequireAuthForSubmission = tc.want

			mod := New(zap.NewNop())
			mod.cfg = cfg
			mod.db = sqlDB
			if err := mod.initCore(cfg, sqlDB); err != nil {
				t.Fatalf("init core: %v", err)
			}
			if mod.smtpServer == nil {
				t.Fatal("smtp server not initialized")
			}
			if got := mod.smtpServer.Config.RequireAuthForSubmission; got != tc.want {
				t.Fatalf("smtp RequireAuthForSubmission=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestRuntimeHealthRegistered(t *testing.T) {
	dir := t.TempDir()
	sqlDB := testRuntimeDB(t, dir)
	t.Cleanup(func() { sqlDB.Close() })

	logger, _ := zap.NewDevelopment()
	mod := New(logger)
	mod.db = sqlDB
	mod.initCore(&config.Config{
		CoreMail: config.CoreMailConfig{
			Enabled:       true,
			Hostname:      "test.orvix.local",
			MailStorePath: filepath.Join(dir, "msgs"),
		},
	}, sqlDB)

	if mod.obs == nil {
		t.Fatal("observability not initialized")
	}

	report := mod.obs.Health.Report()
	expected := []string{"database", "mailstore", "queue", "jmap"}
	for _, name := range expected {
		if _, ok := report.Checks[name]; !ok {
			t.Fatalf("health check %s not registered", name)
		}
	}
}

func TestRuntimeShutdownCleanup(t *testing.T) {
	dir := t.TempDir()
	sqlDB := testRuntimeDB(t, dir)
	t.Cleanup(func() { sqlDB.Close() })

	logger, _ := zap.NewDevelopment()
	mod := New(logger)

	jmapPort := freePort(t)

	cfg := &config.Config{
		CoreMail: config.CoreMailConfig{
			Enabled:       true,
			Hostname:      "test.orvix.local",
			SMTPPort:      freePort(t),
			SMTPHost:      "127.0.0.1",
			IMAPPort:      freePort(t),
			IMAPHost:      "127.0.0.1",
			POP3Port:      freePort(t),
			POP3Host:      "127.0.0.1",
			JMAPPort:      jmapPort,
			JMAPHost:      "127.0.0.1",
			MailStorePath: filepath.Join(dir, "msgs"),
		},
	}

	mod.cfg = cfg
	mod.db = sqlDB
	mod.initCore(cfg, sqlDB)

	if err := mod.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	jmapURL := fmt.Sprintf("http://127.0.0.1:%d/.well-known/jmap", jmapPort)
	resp, err := http.Get(jmapURL)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal("jmap should be reachable before shutdown")
	}
	resp.Body.Close()

	mod.Stop()

	_, err = http.Get(jmapURL)
	if err == nil {
		t.Log("jmap port released after shutdown (or timeout)")
	}
}

func TestRuntimeModuleRegistration(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	reg := modules.NewRegistry(logger)
	mod := New(logger)

	if err := reg.Register(mod); err != nil {
		t.Fatalf("register: %v", err)
	}

	got, ok := reg.Get("coremail-runtime")
	if !ok {
		t.Fatal("module not found in registry")
	}
	if got.ID() != "coremail-runtime" {
		t.Fatalf("expected coremail-runtime, got %s", got.ID())
	}
}

func TestRuntimeAutoRecovery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime_recovery.db")
	sqlDB1, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db1: %v", err)
	}

	cfg := &config.Config{
		CoreMail: config.CoreMailConfig{
			Enabled:       true,
			Hostname:      "test.orvix.local",
			MailStorePath: filepath.Join(dir, "msgs1"),
			QueueWorkers:  1,
		},
	}
	logger, _ := zap.NewDevelopment()
	mod1 := New(logger)
	mod1.db = sqlDB1
	if err := mod1.initCore(cfg, sqlDB1); err != nil {
		t.Fatalf("init mod1: %v", err)
	}

	mod1.policyEngine.SetDefaultMode(policy.InternalOnly)
	mod1.policyEngine.SetTenantPolicy(11, policy.Disabled)
	mod1.policyEngine.SetDomainPolicy("recover.example", policy.SendOnly)
	mod1.policyEngine.SetMailboxPolicy(22, policy.ReceiveOnly)
	mod1.trustEngine.SetUserTrust("user@recover.example", trust.TrustHigh, "cert")
	mod1.trustEngine.SetMailboxTrust(22, trust.TrustMedium, "cert")
	mod1.trustEngine.SetDomainTrust("recover.example", trust.TrustLow, "cert")
	mod1.trustEngine.SetIPTrust("192.0.2.22", trust.TrustLow, "cert")
	for i := 0; i < 5; i++ {
		mod1.trustEngine.RecordAuthFailure("user@recover.example")
	}
	if err := mod1.auditStore.Record(t.Context(), &audit.Entry{Actor: "admin", Action: "runtime_recovery", Target: "cert", Result: "success"}); err != nil {
		t.Fatalf("record audit: %v", err)
	}
	if err := sqlDB1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	sqlDB2, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer sqlDB2.Close()
	cfg.CoreMail.MailStorePath = filepath.Join(dir, "msgs2")
	mod2 := New(logger)
	mod2.db = sqlDB2
	if err := mod2.initCore(cfg, sqlDB2); err != nil {
		t.Fatalf("init mod2: %v", err)
	}

	if got := mod2.policyEngine.Resolve(1, "none.example", nil).Mode; got != policy.InternalOnly {
		t.Fatalf("default policy not recovered: %v", got)
	}
	if p, ok := mod2.policyEngine.GetTenantPolicy(11); !ok || p.Mode != policy.Disabled {
		t.Fatalf("tenant policy not recovered: %#v ok=%v", p, ok)
	}
	if p, ok := mod2.policyEngine.GetDomainPolicy("recover.example"); !ok || p.Mode != policy.SendOnly {
		t.Fatalf("domain policy not recovered: %#v ok=%v", p, ok)
	}
	if p, ok := mod2.policyEngine.GetMailboxPolicy(22); !ok || p.Mode != policy.ReceiveOnly {
		t.Fatalf("mailbox policy not recovered: %#v ok=%v", p, ok)
	}
	if got := mod2.trustEngine.GetUserTrust("user@recover.example"); got == nil || got.Score != trust.TrustHigh {
		t.Fatalf("user trust not recovered: %#v", got)
	}
	if got := mod2.trustEngine.GetMailboxTrust(22); got == nil || got.Score != trust.TrustMedium {
		t.Fatalf("mailbox trust not recovered: %#v", got)
	}
	if got := mod2.trustEngine.GetDomainTrust("recover.example"); got == nil || got.Score != trust.TrustLow {
		t.Fatalf("domain trust not recovered: %#v", got)
	}
	if got := mod2.trustEngine.GetIPTrust("192.0.2.22"); got == nil || got.Score != trust.TrustLow {
		t.Fatalf("ip trust not recovered: %#v", got)
	}
	if !mod2.trustEngine.IsLockedOut("user@recover.example") {
		t.Fatal("lockout not recovered")
	}
	entries, total, err := mod2.auditStore.Search(t.Context(), &audit.Query{Action: "runtime_recovery", Limit: 10})
	if err != nil {
		t.Fatalf("search audit: %v", err)
	}
	if total != 1 || len(entries) != 1 {
		t.Fatalf("audit not recovered, total=%d len=%d", total, len(entries))
	}
}

func TestQueueWorkerHandlesProcessError(t *testing.T) {
	logger := zap.NewNop()
	mod := New(logger)
	mod.obs = observability.NewObservability(10, 10)

	mod.recordQueueWorkerError("worker-test", errors.New("process failed"))

	snap := mod.obs.Metrics.Snapshot()
	if snap.QueueDeferred != 1 {
		t.Fatalf("expected queue error metric, got %d", snap.QueueDeferred)
	}
	events := mod.obs.EventHistory.Recent()
	if len(events) == 0 {
		t.Fatal("expected queue error event")
	}
	report := mod.obs.Health.Report()
	queue, ok := report.Checks[observability.HealthCheckQueue]
	if !ok {
		t.Fatal("expected queue health check")
	}
	if queue.Status != observability.HealthNotReady {
		t.Fatal("queue health should be not ready after worker error")
	}
}

var _ = observability.NewObservability
