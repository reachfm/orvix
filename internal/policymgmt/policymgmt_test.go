package policymgmt

import (
	"context"
	"testing"

	"github.com/orvix/orvix/internal/policy"
)

func TestSetDomainPolicy(t *testing.T) {
	eng := policy.NewEngine()
	svc := NewService(eng)
	ctx := context.Background()

	err := svc.SetDomainPolicy(ctx, "example.com", "internal_only")
	if err != nil {
		t.Fatalf("set domain policy: %v", err)
	}

	entry, err := svc.GetDomainPolicy(ctx, "example.com")
	if err != nil {
		t.Fatalf("get domain policy: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil policy entry")
	}
	if entry.Mode != "internal_only" {
		t.Fatalf("expected internal_only, got %s", entry.Mode)
	}
}

func TestInvalidModeRejected(t *testing.T) {
	eng := policy.NewEngine()
	svc := NewService(eng)
	ctx := context.Background()

	err := svc.SetDomainPolicy(ctx, "example.com", "invalid_mode")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

func TestDeleteDomainPolicy(t *testing.T) {
	eng := policy.NewEngine()
	svc := NewService(eng)
	ctx := context.Background()

	svc.SetDomainPolicy(ctx, "example.com", "disabled")
	err := svc.DeleteDomainPolicy(ctx, "example.com")
	if err != nil {
		t.Fatalf("delete domain policy: %v", err)
	}
}

func TestSetDefaultMode(t *testing.T) {
	eng := policy.NewEngine()
	svc := NewService(eng)
	ctx := context.Background()

	err := svc.SetDefaultMode(ctx, "disabled")
	if err != nil {
		t.Fatalf("set default mode: %v", err)
	}
}

func TestParseMode(t *testing.T) {
	valid := []string{"allow_all", "internal_only", "external_only", "send_only", "receive_only", "disabled"}
	for _, m := range valid {
		if parseMode(m) == nil {
			t.Errorf("expected valid mode: %s", m)
		}
	}
	if parseMode("invalid") != nil {
		t.Fatal("expected nil for invalid mode")
	}
}
