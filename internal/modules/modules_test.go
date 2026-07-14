package modules

import (
	"testing"

	"github.com/orvix/orvix/internal/config"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type testModule struct {
	id       string
	version  string
	requires []string
}

func (m *testModule) ID() string                                 { return m.id }
func (m *testModule) Version() string                            { return m.version }
func (m *testModule) Requires() []string                         { return m.requires }
func (m *testModule) Init(cfg *config.Config, db *gorm.DB) error { return nil }
func (m *testModule) Start() error                               { return nil }
func (m *testModule) Stop() error                                { return nil }
func (m *testModule) Migrate() error                             { return nil }

func TestRegisterAndGet(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	reg := NewRegistry(logger)

	mod := &testModule{id: "test-module", version: "1.0.0"}
	err := reg.Register(mod)
	if err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	got, ok := reg.Get("test-module")
	if !ok {
		t.Fatal("module not found")
	}
	if got.ID() != "test-module" {
		t.Fatalf("expected ID 'test-module', got %q", got.ID())
	}
	if got.Version() != "1.0.0" {
		t.Fatalf("expected version '1.0.0', got %q", got.Version())
	}
}

func TestRegisterDuplicate(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	reg := NewRegistry(logger)

	reg.Register(&testModule{id: "dup-module", version: "1.0.0"})
	err := reg.Register(&testModule{id: "dup-module", version: "2.0.0"})
	if err == nil {
		t.Fatal("expected error when registering duplicate module")
	}
}

func TestAllModules(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	reg := NewRegistry(logger)

	reg.Register(&testModule{id: "mod-a", version: "1.0.0"})
	reg.Register(&testModule{id: "mod-b", version: "2.0.0"})
	reg.Register(&testModule{id: "mod-c", version: "3.0.0"})

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(all))
	}
}

func TestGetNonExistent(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	reg := NewRegistry(logger)

	_, ok := reg.Get("non-existent")
	if ok {
		t.Fatal("expected false for non-existent module")
	}
}

func TestRegistryVersion(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	reg := NewRegistry(logger)

	if reg.Version() != "1.0.0" {
		t.Fatalf("expected registry version 1.0.0, got %s", reg.Version())
	}
}

func TestTopologicalSort(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	reg := NewRegistry(logger)

	reg.Register(&testModule{id: "mod-c", version: "1.0.0", requires: []string{"mod-a"}})
	reg.Register(&testModule{id: "mod-a", version: "1.0.0", requires: []string{}})
	reg.Register(&testModule{id: "mod-b", version: "1.0.0", requires: []string{"mod-a"}})

	all := reg.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 modules, got %d", len(all))
	}
}

func TestStopAll(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	reg := NewRegistry(logger)

	reg.Register(&testModule{id: "stop-a", version: "1.0.0"})
	reg.Register(&testModule{id: "stop-b", version: "1.0.0"})

	err := reg.StopAll()
	if err != nil {
		t.Fatalf("StopAll failed: %v", err)
	}
}
