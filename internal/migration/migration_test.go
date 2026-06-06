package migration

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewIMAPSync(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	s := NewIMAPSync(logger)
	if s == nil {
		t.Fatal("NewIMAPSync returned nil")
	}
}

func TestSupportedProviders(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	s := NewIMAPSync(logger)
	providers := s.SupportedProviders()
	if len(providers) == 0 {
		t.Fatal("expected at least 1 provider")
	}
}

func TestMigrationSource(t *testing.T) {
	src := &MigrationSource{
		Host: "imap.example.com", Port: 993,
		Username: "user", Password: "pass", UseTLS: true,
		Provider: "generic-imap",
	}
	if src.Host != "imap.example.com" {
		t.Fatalf("unexpected host: %s", src.Host)
	}
	if src.Port != 993 {
		t.Fatalf("unexpected port: %d", src.Port)
	}
}

func TestSyncProgress(t *testing.T) {
	p := SyncProgress{
		TotalMessages: 1000, SyncedMessages: 500,
		CurrentFolder: "INBOX", Status: "syncing",
	}
	if p.TotalMessages != 1000 {
		t.Fatalf("unexpected total: %d", p.TotalMessages)
	}
	if p.Status != "syncing" {
		t.Fatalf("unexpected status: %s", p.Status)
	}
}

func TestMigrationJobFields(t *testing.T) {
	job := MigrationJob{
		SourceHost: "imap.old.com", SourcePort: 993,
		SourceUser: "olduser", Provider: "zimbra",
		TargetUser: "newuser@orvix.email", Status: "pending",
	}
	if job.SourceHost != "imap.old.com" {
		t.Fatalf("unexpected source host: %s", job.SourceHost)
	}
	if job.Status != "pending" {
		t.Fatalf("unexpected status: %s", job.Status)
	}
}
