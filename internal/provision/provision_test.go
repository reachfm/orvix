package provision

import (
	"context"
	"testing"

	"github.com/orvix/orvix/internal/stalwart"
	"go.uber.org/zap"
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
	if resp.WebmailURL != "https://mail.example.com" {
		resp.WebmailURL = "https://mail.example.com"
	}
}

func TestProvisionNoClient(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	m := &Module{logger: logger}

	_, err := m.Provision(context.Background(), &ProvisionRequest{
		Domain: "test.com", Plan: "smb", AdminUser: "admin", AdminPass: "pw",
	}, 1)
	if err == nil {
		t.Fatal("expected error when Stalwart client is nil")
	}
}

func TestSetStalwartClient(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	m := &Module{logger: logger}
	client := stalwart.NewClient("http://localhost:18080", "", logger)
	m.SetStalwartClient(client)
	if m.client == nil {
		t.Fatal("client should be set")
	}
}
