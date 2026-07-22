package metrics

import (
	"testing"
)

func TestRegisterIdempotent(t *testing.T) {
	// Multiple calls should not panic
	svc := NewService()

	// First call
	svc.Register()

	// Second call — should not panic
	svc.Register()

	// Third call — should not panic
	svc.Register()

	// Verify handler exists
	h := svc.Handler()
	if h == nil {
		t.Error("Handler() returned nil")
	}
}

func TestNewService(t *testing.T) {
	svc := NewService()
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	if svc.registry == nil {
		t.Error("registry is nil")
	}
}
