package dns

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestNewManager(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	m := NewManager(logger)
	if m == nil {
		t.Fatal("NewManager returned nil")
	}
}

func TestRegisterProvider(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	m := NewManager(logger)

	p := NewCloudflareProvider("test-key", logger)
	m.RegisterProvider(p)

	if len(m.providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(m.providers))
	}
}

func TestCloudflareProviderName(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewCloudflareProvider("test-key", logger)
	if p.Name() != "cloudflare" {
		t.Fatalf("expected 'cloudflare', got %s", p.Name())
	}
}

func TestCloudflareProviderCalls(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	p := NewCloudflareProvider("test-key", logger)

	if err := p.CreateMXRecord(context.Background(), "example.com", "mail.example.com", 10); err != nil {
		t.Fatalf("CreateMXRecord failed: %v", err)
	}
	if err := p.CreateTXTRecord(context.Background(), "example.com", "@", "v=spf1 mx ~all"); err != nil {
		t.Fatalf("CreateTXTRecord failed: %v", err)
	}
	if err := p.CreateDKIMRecord(context.Background(), "example.com", "orvix", "v=DKIM1; p=testkey"); err != nil {
		t.Fatalf("CreateDKIMRecord failed: %v", err)
	}
}

func TestWizardResult(t *testing.T) {
	r := &WizardResult{
		SPFStatus: "configured", DKIMStatus: "configured",
		DMARCStatus: "configured", MXStatus: "configured",
	}
	if r.SPFStatus != "configured" {
		t.Fatalf("unexpected SPF status: %s", r.SPFStatus)
	}
}

func TestRunWizardNoProvider(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	m := NewManager(logger)

	_, err := m.RunWizard(context.Background(), "nonexistent", "example.com")
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}
