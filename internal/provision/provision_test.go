package provision

import (
	"testing"

	"github.com/orvix/orvix/internal/config"
)

func TestNewModule(t *testing.T) {
	m := &Module{}
	if m.ID() != "provision-api" {
		t.Fatalf("expected ID 'provision-api', got %s", m.ID())
	}
	if m.Version() != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %s", m.Version())
	}
}

func TestProvisionRequest(t *testing.T) {
	req := &ProvisionRequest{
		Domain: "example.com", Plan: "smb",
		AdminUser: "admin", AdminPass: "secret123", QuotaMB: 1024,
	}
	if req.Domain != "example.com" {
		t.Fatalf("unexpected domain: %s", req.Domain)
	}
	if req.QuotaMB != 1024 {
		t.Fatalf("unexpected quota: %d", req.QuotaMB)
	}
}

func TestProvisionResponse(t *testing.T) {
	resp := &ProvisionResponse{
		Status: "active", Domain: "example.com",
		AdminEmail: "admin@example.com", ProvisionedMs: 1234,
	}
	if resp.Status != "active" {
		t.Fatalf("unexpected status: %s", resp.Status)
	}
}

func TestProvisionInitSetsLogger(t *testing.T) {
	cfg := config.Defaults()
	logger, _ := config.NewLogger(&config.LoggingConfig{Level: "debug", Format: "console", Output: "stdout"})
	cfg.SetLogger(logger)
	m := &Module{}
	if err := m.Init(cfg, nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	if m.ID() != "provision-api" {
		t.Fatalf("expected ID 'provision-api', got %s", m.ID())
	}
}
