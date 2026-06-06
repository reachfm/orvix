package autoheal

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestNewMonitor(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	m := NewMonitor(logger)
	if m == nil {
		t.Fatal("NewMonitor returned nil")
	}
}

func TestAddAndRunChecks(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	m := NewMonitor(logger)

	m.AddCheck(HealthCheck{
		Name:     "test-check",
		Interval: 100 * time.Millisecond,
		Severity: "low",
		Check: func(ctx context.Context) (string, error) {
			return "", nil
		},
	})

	if len(m.checks) != 1 {
		t.Fatalf("expected 1 check, got %d", len(m.checks))
	}
	if m.checks[0].Name != "test-check" {
		t.Fatalf("expected check name 'test-check', got %s", m.checks[0].Name)
	}
}

func TestHealHistoryStruct(t *testing.T) {
	now := time.Now()
	h := HealHistory{
		CheckName:  "database",
		Severity:   "high",
		Issue:      "connection lost",
		FixApplied: "reconnected",
		Success:    true,
		CreatedAt:  now,
	}
	if h.CheckName != "database" {
		t.Fatalf("unexpected CheckName: %s", h.CheckName)
	}
	if !h.Success {
		t.Fatal("expected Success=true")
	}
}

func TestHealthCheckStruct(t *testing.T) {
	hc := HealthCheck{
		Name:     "disk",
		Interval: 60 * time.Second,
		Severity: "medium",
	}
	if hc.Name != "disk" {
		t.Fatalf("unexpected name: %s", hc.Name)
	}
	if hc.Interval != 60*time.Second {
		t.Fatalf("unexpected interval: %v", hc.Interval)
	}
}

func TestMonitorStartStop(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	m := NewMonitor(logger)

	m.Start(context.Background())
	m.Stop()
}
