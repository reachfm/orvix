package runtimecontrol

import (
	"testing"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/observability"
)

type testConfigProvider struct {
	cfg *config.Config
}

func (t *testConfigProvider) GetConfig() *config.Config { return t.cfg }
func (t *testConfigProvider) ReloadConfig() error        { return nil }

func testControl() *RuntimeControl {
	obs := observability.NewObservability(100, 100)
	cfg := &config.Config{
		CoreMail: config.CoreMailConfig{
			Enabled:        true,
			Hostname:       "mail.test.com",
			SMTPHost:       "0.0.0.0",
			SMTPPort:       25,
			IMAPHost:       "0.0.0.0",
			IMAPPort:       143,
			POP3Host:       "0.0.0.0",
			POP3Port:       110,
			JMAPHost:       "0.0.0.0",
			JMAPPort:       8080,
			QueueWorkers:   2,
			WorkerInterval: 5000000000, // 5s
		},
	}
	return NewRuntimeControl(obs, &testConfigProvider{cfg: cfg})
}

func TestRuntimeSnapshot(t *testing.T) {
	rc := testControl()
	snap := rc.Snapshot()
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if len(snap.Services) != 9 {
		t.Fatalf("expected 9 services, got %d", len(snap.Services))
	}
	if len(snap.Listeners) != 4 {
		t.Fatalf("expected 4 listeners, got %d", len(snap.Listeners))
	}
	// Verify SMTP listener.
	found := false
	for _, l := range snap.Listeners {
		if l.Protocol == "SMTP" && l.Port == 25 {
			found = true
		}
	}
	if !found {
		t.Fatal("expected SMTP listener on port 25")
	}
}

func TestSettingsRead(t *testing.T) {
	rc := testControl()
	s := rc.GetSettings()
	if s == nil {
		t.Fatal("expected non-nil settings")
	}
	if s.SMTP.Hostname != "mail.test.com" {
		t.Fatalf("expected hostname mail.test.com, got %s", s.SMTP.Hostname)
	}
	if s.Queue.WorkerCount != 2 {
		t.Fatalf("expected 2 workers, got %d", s.Queue.WorkerCount)
	}
}

func TestSettingsUpdateValid(t *testing.T) {
	rc := testControl()
	err := rc.UpdateSettings(&Settings{
		SMTP: SMTPSettings{MaxMessageSizeMB: 50, MaxRecipients: 200},
		Queue: QueueSettings{WorkerCount: 5},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSettingsInvalidRejected(t *testing.T) {
	rc := testControl()
	tests := []struct {
		name string
		s    *Settings
	}{
		{"max size too large", &Settings{SMTP: SMTPSettings{MaxMessageSizeMB: 200}}},
		{"recipients too large", &Settings{SMTP: SMTPSettings{MaxRecipients: 2000}}},
		{"invalid spam mode", &Settings{SMTP: SMTPSettings{SpamMode: "invalid"}}},
		{"workers too large", &Settings{Queue: QueueSettings{WorkerCount: 100}}},
		{"invalid policy mode", &Settings{Policy: PolicySettings{DefaultMode: "invalid"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := rc.UpdateSettings(tt.s); err == nil {
				t.Fatal("expected error for invalid settings")
			}
		})
	}
}

func TestReloadEndpoint(t *testing.T) {
	rc := testControl()
	result := rc.Reload()
	if !result.Success {
		t.Fatalf("expected reload success, got: %s", result.Message)
	}
}

func TestSettingsNil(t *testing.T) {
	rc := testControl()
	if err := rc.UpdateSettings(nil); err == nil {
		t.Fatal("expected error for nil settings")
	}
}

func TestHealthIntegration(t *testing.T) {
	rc := testControl()
	snap := rc.Snapshot()
	// Health should return "unknown" since no checks registered.
	for _, svc := range snap.Services {
		if svc.Healthy == "" {
			t.Fatalf("service %s has empty health", svc.Name)
		}
	}
}
