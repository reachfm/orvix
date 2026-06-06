package updater

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewUpdateManager(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	um := NewUpdateManager("https://updates.orvix.email", "stable", logger)
	if um == nil {
		t.Fatal("NewUpdateManager returned nil")
	}
	if um.channel != "stable" {
		t.Fatalf("expected channel 'stable', got %s", um.channel)
	}
}

func TestUpdateInfo(t *testing.T) {
	info := &UpdateInfo{
		ModuleID: "mail-firewall", CurrentVer: "1.0.0",
		LatestVer: "1.1.0", Changelog: "Bug fixes",
		Critical: false,
	}
	if info.ModuleID != "mail-firewall" {
		t.Fatalf("unexpected module: %s", info.ModuleID)
	}
	if info.LatestVer != "1.1.0" {
		t.Fatalf("unexpected latest version: %s", info.LatestVer)
	}
}

func TestChangelogEntry(t *testing.T) {
	entry := ChangelogEntry{
		ModuleID: "orvix-core", Version: "1.0.0",
		Changes: "Initial release",
	}
	if entry.ModuleID != "orvix-core" {
		t.Fatalf("unexpected module: %s", entry.ModuleID)
	}
}

func TestNewChangelogManager(t *testing.T) {
	cm := NewChangelogManager(nil)
	if cm == nil {
		t.Fatal("NewChangelogManager returned nil")
	}
}

func TestNewRollbackManager(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	rm := NewRollbackManager(nil, "/var/backups/orvix", logger)
	if rm == nil {
		t.Fatal("NewRollbackManager returned nil")
	}
	path, err := rm.CreateBackup("auto-heal", "1.0.0")
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty backup path")
	}
}

func TestModule(t *testing.T) {
	m := &Module{}
	if m.ID() != "auto-update" {
		t.Fatalf("expected ID 'auto-update', got %s", m.ID())
	}
	if m.Version() != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %s", m.Version())
	}
}
