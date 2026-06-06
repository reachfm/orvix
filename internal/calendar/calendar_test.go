package calendar

import (
	"testing"
	"time"
)

func TestNewModule(t *testing.T) {
	m := &Module{}
	if m.ID() != "calendar" {
		t.Fatalf("expected ID 'calendar', got %s", m.ID())
	}
	if m.Version() != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %s", m.Version())
	}
}

func TestEventStruct(t *testing.T) {
	now := time.Now()
	e := Event{
		UserID: 1, Title: "Meeting", Description: "Team standup",
		StartTime: now, EndTime: now.Add(1 * time.Hour),
		AllDay: false, Location: "Room 101", Color: "#4F7CFF",
	}
	if e.Title != "Meeting" {
		t.Fatalf("unexpected title: %s", e.Title)
	}
	if !e.EndTime.After(e.StartTime) {
		t.Fatal("end time should be after start time")
	}
	if e.Color != "#4F7CFF" {
		t.Fatalf("unexpected color: %s", e.Color)
	}
}

func TestContactStruct(t *testing.T) {
	c := Contact{
		UserID: 1, Name: "John Doe",
		Email: "john@example.com", Phone: "+1-555-0123",
		Company: "Acme Inc", Notes: "Met at conference",
	}
	if c.Name != "John Doe" {
		t.Fatalf("unexpected name: %s", c.Name)
	}
	if c.Email != "john@example.com" {
		t.Fatalf("unexpected email: %s", c.Email)
	}
}

func TestTaskStruct(t *testing.T) {
	dueDate := time.Now().Add(7 * 24 * time.Hour)
	task := Task{
		UserID: 1, Title: "Complete report",
		Description: "Finish Q3 financial report",
		DueDate: &dueDate, Completed: false, Priority: "high",
	}
	if task.Title != "Complete report" {
		t.Fatalf("unexpected title: %s", task.Title)
	}
	if task.Priority != "high" {
		t.Fatalf("unexpected priority: %s", task.Priority)
	}
	if task.Completed {
		t.Fatal("expected Completed=false")
	}
}

func TestModuleInit(t *testing.T) {
	m := &Module{}
	err := m.Migrate()
	if err != nil {
		t.Fatalf("Migrate should not fail: %v", err)
	}
}
