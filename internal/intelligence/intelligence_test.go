package intelligence

import (
	"testing"
	"time"
)

func TestModuleID(t *testing.T) {
	m := &Module{}
	if m.ID() != "email-intelligence" {
		t.Fatalf("expected ID 'email-intelligence', got %s", m.ID())
	}
	if m.Version() != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %s", m.Version())
	}
}

func TestEmailAnalyticsStruct(t *testing.T) {
	now := time.Now()
	a := EmailAnalytics{
		Date: now, Domain: "example.com",
		SentCount: 100, RecvCount: 250,
		BounceCount: 5, SpamCount: 10,
	}
	if a.SentCount != 100 {
		t.Fatalf("unexpected sent count: %d", a.SentCount)
	}
	if a.RecvCount != 250 {
		t.Fatalf("unexpected recv count: %d", a.RecvCount)
	}
	if a.Domain != "example.com" {
		t.Fatalf("unexpected domain: %s", a.Domain)
	}
}

func TestDeliveryReportStruct(t *testing.T) {
	r := DeliveryReport{
		MessageID: "msg-001", Recipient: "user@example.com",
		Status: "delivered", DurationMs: 1234,
	}
	if r.MessageID != "msg-001" {
		t.Fatalf("unexpected message ID: %s", r.MessageID)
	}
	if r.Status != "delivered" {
		t.Fatalf("unexpected status: %s", r.Status)
	}
	if r.DurationMs != 1234 {
		t.Fatalf("unexpected duration: %d", r.DurationMs)
	}
}

func TestModuleRequires(t *testing.T) {
	m := &Module{}
	req := m.Requires()
	if len(req) != 1 || req[0] != "core" {
		t.Fatalf("unexpected requires: %v", req)
	}
}
