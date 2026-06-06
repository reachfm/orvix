package collaboration

import (
	"testing"
)

func TestModuleID(t *testing.T) {
	m := &Module{}
	if m.ID() != "collaboration" {
		t.Fatalf("expected ID 'collaboration', got %s", m.ID())
	}
	if m.Version() != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %s", m.Version())
	}
}

func TestSharedMailboxStruct(t *testing.T) {
	mb := SharedMailbox{
		TenantID: 1, Name: "Support Team",
		Email: "support@example.com", Description: "Customer support inbox",
	}
	if mb.Name != "Support Team" {
		t.Fatalf("unexpected name: %s", mb.Name)
	}
	if mb.Email != "support@example.com" {
		t.Fatalf("unexpected email: %s", mb.Email)
	}
}

func TestSharedCalendarStruct(t *testing.T) {
	cal := SharedCalendar{
		TenantID: 1, Name: "Team Calendar",
		Color: "#34D399", Description: "Company events",
	}
	if cal.Name != "Team Calendar" {
		t.Fatalf("unexpected name: %s", cal.Name)
	}
}
