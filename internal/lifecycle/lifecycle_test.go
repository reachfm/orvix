package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	_ "modernc.org/sqlite"
)

type mockHealth struct {
	smtp bool
	imap bool
	pop3 bool
	jmap bool
	db   bool
	ms   bool
}

func (m *mockHealth) SMTPHealthy() bool      { return m.smtp }
func (m *mockHealth) IMAPHealthy() bool      { return m.imap }
func (m *mockHealth) POP3Healthy() bool      { return m.pop3 }
func (m *mockHealth) JMAPHealthy() bool      { return m.jmap }
func (m *mockHealth) DatabaseHealthy() bool  { return m.db }
func (m *mockHealth) MailStoreHealthy() bool { return m.ms }

type mockBC struct{}

func (m *mockBC) CreateBackup(ctx context.Context, name string) (interface{}, error) {
	return struct{ ID string }{ID: "safety-" + name}, nil
}

type mockBR struct{}

func (m *mockBR) CreateBackup(ctx context.Context, name string) (interface{}, error) {
	return struct{ ID string }{ID: name}, nil
}
func (m *mockBR) RestoreBackup(ctx context.Context, id string) interface{}      { return nil }
func (m *mockBR) GetBackup(ctx context.Context, id string) (interface{}, error) { return nil, nil }
func (m *mockBR) ListBackups(ctx context.Context) (interface{}, error)          { return nil, nil }

type mockReloader struct{ reloaded bool }

func (m *mockReloader) Reload() error { m.reloaded = true; return nil }

type mockLoader struct{ loaded bool }

func (m *mockLoader) LoadFromDB(ctx context.Context) error { m.loaded = true; return nil }

func testService(t *testing.T) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/life_test.db", t.TempDir()))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	svc := NewService(db)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return svc
}

func TestRecordVersion(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	v, err := svc.RecordVersion(ctx, "1.0.0", "admin", "Initial install")
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if v.Version != "1.0.0" {
		t.Fatalf("expected 1.0.0, got %s", v.Version)
	}
}

func TestCurrentVersion(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.RecordVersion(ctx, "1.0.0", "admin", "")
	svc.RecordVersion(ctx, "1.1.0", "admin", "")
	v, err := svc.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if v.Version != "1.1.0" {
		t.Fatalf("expected 1.1.0, got %s", v.Version)
	}
}

func TestVersionHistory(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.RecordVersion(ctx, "1.0.0", "admin", "")
	svc.RecordVersion(ctx, "1.1.0", "admin", "")
	history, err := svc.VersionHistory(ctx)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 records, got %d", len(history))
	}
}

func TestPreflightPass(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.SetHealthChecker(&mockHealth{smtp: true, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})
	svc.SetRuntimeReloader(&mockReloader{})

	result := svc.RunPreflight(ctx)
	if !result.Pass {
		t.Fatalf("expected pass, got failures: %v", result.Checks)
	}
}

func TestPreflightFailure(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.SetHealthChecker(&mockHealth{smtp: false, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})

	result := svc.RunPreflight(ctx)
	if result.Pass {
		t.Fatal("expected fail for unhealthy SMTP")
	}
}

func TestUpgradeSuccess(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.SetHealthChecker(&mockHealth{smtp: true, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})
	svc.SetPolicyLoader(&mockLoader{})
	svc.SetTrustLoader(&mockLoader{})
	svc.SetRuntimeReloader(&mockReloader{})

	result := svc.Upgrade(ctx, "1.0.0", "2.0.0")
	if result.Status != UpgradeCompleted {
		t.Fatalf("expected completed, got %s", result.Status)
	}

	v, _ := svc.CurrentVersion(ctx)
	if v.Version != "2.0.0" {
		t.Fatalf("expected 2.0.0, got %s", v.Version)
	}
}

func TestUpgradeRollbackOnPreflightFailure(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.SetHealthChecker(&mockHealth{smtp: false, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})
	svc.SetRuntimeReloader(&mockReloader{})

	result := svc.Upgrade(ctx, "1.0.0", "2.0.0")
	if result.Status != UpgradeFailed && result.Status != UpgradeRolledBack {
		t.Fatalf("expected failed or rolled_back, got %s", result.Status)
	}
}

func TestUpgradeHistory(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.SetHealthChecker(&mockHealth{smtp: true, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})
	svc.SetRuntimeReloader(&mockReloader{})

	svc.Upgrade(ctx, "1.0.0", "1.1.0")
	history, err := svc.UpgradeHistory(ctx)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("expected history")
	}
}

func TestRollback(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	svc.SetHealthChecker(&mockHealth{smtp: true, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})
	svc.SetPolicyLoader(&mockLoader{})
	svc.SetTrustLoader(&mockLoader{})
	svc.SetRuntimeReloader(&mockReloader{})

	svc.Upgrade(ctx, "1.0.0", "2.0.0")
	result := svc.Rollback(ctx)
	if result.Status != UpgradeRolledBack {
		t.Fatalf("expected rolled_back, got %s", result.Status)
	}
}

func TestRollbackNoHistory(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	result := svc.Rollback(ctx)
	if result.Status != UpgradeFailed {
		t.Fatalf("expected failed when no upgrade history, got %s", result.Status)
	}
}

func TestRuntimeReloadOnUpgrade(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	rl := &mockReloader{}
	svc.SetHealthChecker(&mockHealth{smtp: true, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})
	svc.SetRuntimeReloader(rl)
	svc.SetPolicyLoader(&mockLoader{})
	svc.SetTrustLoader(&mockLoader{})

	svc.Upgrade(ctx, "1.0.0", "2.0.0")
	if !rl.reloaded {
		t.Fatal("runtime was not reloaded")
	}
}

func TestPolicyReloadOnUpgrade(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	pl := &mockLoader{}
	svc.SetHealthChecker(&mockHealth{smtp: true, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})
	svc.SetPolicyLoader(pl)
	svc.SetRuntimeReloader(&mockReloader{})

	svc.Upgrade(ctx, "1.0.0", "2.0.0")
	if !pl.loaded {
		t.Fatal("policy was not reloaded")
	}
}

func TestTrustReloadOnUpgrade(t *testing.T) {
	svc := testService(t)
	ctx := context.Background()
	tl := &mockLoader{}
	svc.SetHealthChecker(&mockHealth{smtp: true, imap: true, pop3: true, jmap: true, db: true, ms: true})
	svc.SetBackupCreator(&mockBC{})
	svc.SetTrustLoader(tl)
	svc.SetRuntimeReloader(&mockReloader{})

	svc.Upgrade(ctx, "1.0.0", "2.0.0")
	if !tl.loaded {
		t.Fatal("trust was not reloaded")
	}
}
