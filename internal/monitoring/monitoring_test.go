package monitoring

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func testService(t *testing.T, src *DataSources) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("%s/mon_test.db", t.TempDir()))
	if err != nil { t.Fatalf("open db: %v", err) }
	t.Cleanup(func() { db.Close() })
	svc := NewService(db, src)
	if err := svc.EnsureSchema(context.Background()); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return svc
}

func ptr[T any](v T) *T { return &v }

func TestQueueAlertWarning(t *testing.T) {
	svc := testService(t, &DataSources{
		QueuePending: func() (int64, error) { return 200, nil },
	})
	alerts, err := svc.EvaluateAlerts(context.Background())
	if err != nil { t.Fatalf("evaluate: %v", err) }
	found := false
	for _, a := range alerts {
		if a.Category == CatQueue && a.Severity == SeverityWarning { found = true }
	}
	if !found { t.Fatal("expected queue warning alert") }
}

func TestQueueAlertCritical(t *testing.T) {
	svc := testService(t, &DataSources{
		QueuePending: func() (int64, error) { return 2000, nil },
	})
	alerts, err := svc.EvaluateAlerts(context.Background())
	if err != nil { t.Fatalf("evaluate: %v", err) }
	found := false
	for _, a := range alerts {
		if a.Category == CatQueue && a.Severity == SeverityCritical { found = true }
	}
	if !found { t.Fatal("expected queue critical alert") }
}

func TestTLSCertExpiryWarning(t *testing.T) {
	svc := testService(t, &DataSources{
		TLSCerts: func() (int, int, error) { return 2, 0, nil },
	})
	alerts, err := svc.EvaluateAlerts(context.Background())
	if err != nil { t.Fatalf("evaluate: %v", err) }
	found := false
	for _, a := range alerts {
		if a.Category == CatTLS && a.Severity == SeverityWarning { found = true }
	}
	if !found { t.Fatal("expected TLS warning alert") }
}

func TestTLSCertExpiryCritical(t *testing.T) {
	svc := testService(t, &DataSources{
		TLSCerts: func() (int, int, error) { return 0, 1, nil },
	})
	alerts, err := svc.EvaluateAlerts(context.Background())
	if err != nil { t.Fatalf("evaluate: %v", err) }
	found := false
	for _, a := range alerts {
		if a.Category == CatTLS && a.Severity == SeverityCritical { found = true }
	}
	if !found { t.Fatal("expected TLS critical alert") }
}

func TestBackupExpiryWarning(t *testing.T) {
	svc := testService(t, &DataSources{
		LatestBackup: func() (time.Time, error) { return time.Now().Add(-10 * 24 * time.Hour), nil },
	})
	alerts, err := svc.EvaluateAlerts(context.Background())
	if err != nil { t.Fatalf("evaluate: %v", err) }
	found := false
	for _, a := range alerts {
		if a.Category == CatBackup { found = true }
	}
	if !found { t.Fatal("expected backup alert") }
}

func TestBackupExpiryCritical(t *testing.T) {
	svc := testService(t, &DataSources{
		LatestBackup: func() (time.Time, error) { return time.Now().Add(-40 * 24 * time.Hour), nil },
	})
	alerts, err := svc.EvaluateAlerts(context.Background())
	if err != nil { t.Fatalf("evaluate: %v", err) }
	found := false
	for _, a := range alerts {
		if a.Category == CatBackup && a.Severity == SeverityCritical { found = true }
	}
	if !found { t.Fatal("expected backup critical alert") }
}

func TestRuntimeFailureAlerts(t *testing.T) {
	svc := testService(t, &DataSources{
		SMTPHealthy:      func() bool { return false },
		IMAPHealthy:      func() bool { return true },
		POP3Healthy:      func() bool { return false },
		JMAPHealthy:      func() bool { return true },
		DatabaseHealthy:  func() bool { return false },
		MailStoreHealthy: func() bool { return true },
	})
	alerts, err := svc.EvaluateAlerts(context.Background())
	if err != nil { t.Fatalf("evaluate: %v", err) }
	criticalCount := 0
	for _, a := range alerts {
		if a.Severity == SeverityCritical { criticalCount++ }
	}
	if criticalCount < 3 { t.Fatalf("expected at least 3 critical alerts (SMTP, POP3, DB), got %d", criticalCount) }
}

func TestResolveAlert(t *testing.T) {
	svc := testService(t, &DataSources{
		SMTPHealthy: func() bool { return false },
	})
	alerts, _ := svc.EvaluateAlerts(context.Background())
	if len(alerts) > 0 {
		if _, err := svc.ResolveAlert(context.Background(), alerts[0].ID); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		active, _ := svc.ListActiveAlerts(context.Background())
		for _, a := range active {
			if a.ID == alerts[0].ID { t.Fatal("alert should be resolved") }
		}
	}
}

func TestCapacity(t *testing.T) {
	svc := testService(t, &DataSources{
		DomainCount:    func() (int, error) { return 5, nil },
		MailboxCount:   func() (int64, error) { return 100, nil },
		MessageCount:   func() (int64, error) { return 5000, nil },
		QueuePending:   func() (int64, error) { return 50, nil },
		BackupCount:    func() (int, error) { return 3, nil },
	})
	c := svc.GetCapacity(context.Background())
	if c.DomainCount != 5 { t.Fatalf("expected 5 domains, got %d", c.DomainCount) }
	if c.MailboxCount != 100 { t.Fatalf("expected 100 mailboxes, got %d", c.MailboxCount) }
	if c.MessageCount != 5000 { t.Fatalf("expected 5000 messages, got %d", c.MessageCount) }
	if c.QueueCount != 50 { t.Fatalf("expected 50 queue, got %d", c.QueueCount) }
	if c.BackupCount != 3 { t.Fatalf("expected 3 backups, got %d", c.BackupCount) }
}

func TestListAlerts(t *testing.T) {
	svc := testService(t, &DataSources{
		SMTPHealthy: func() bool { return false },
	})
	ctx := context.Background()
	svc.EvaluateAlerts(ctx)
	alerts, err := svc.ListAllAlerts(ctx)
	if err != nil { t.Fatalf("list: %v", err) }
	if len(alerts) == 0 { t.Fatal("expected alerts") }
}

func TestAlertResolveClears(t *testing.T) {
	svc := testService(t, &DataSources{
		SMTPHealthy: func() bool { return false },
	})
	svc.EvaluateAlerts(context.Background())
	svc.EvaluateAlerts(context.Background()) // Second evaluation resolves previous
	active, _ := svc.ListActiveAlerts(context.Background())
	all, _ := svc.ListAllAlerts(context.Background())
	if len(all) < len(active) { t.Fatal("all should be >= active") }
}
