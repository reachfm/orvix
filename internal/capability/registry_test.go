package capability

import (
	"testing"

	"github.com/orvix/orvix/internal/config"
	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type testModule struct {
	id string
}

func (m *testModule) ID() string                                 { return m.id }
func (m *testModule) Version() string                            { return "1.0.0" }
func (m *testModule) Requires() []string                         { return nil }
func (m *testModule) Init(cfg *config.Config, db *gorm.DB) error { return nil }
func (m *testModule) Start() error                               { return nil }
func (m *testModule) Stop() error                                { return nil }
func (m *testModule) Migrate() error                             { return nil }

func TestFromRuntime_ModulesAvailable(t *testing.T) {
	logger := zap.NewNop()
	reg := modules.NewRegistry(logger)
	_ = reg.Register(&testModule{id: "coremail-runtime"})
	_ = reg.Register(&testModule{id: "dns"})
	entries := FromRuntime(reg, true, false)
	if len(entries) == 0 {
		t.Fatal("expected capability entries")
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e.ID] = true
		if e.ID == "mailbox_management" && e.Availability != Available {
			t.Fatalf("mailbox_management should be available when coremail-runtime is registered, got %s", e.Availability)
		}
		if e.ID == "update_management" && e.Availability != Available {
			t.Fatalf("update_management should be available when hasUpdater=true, got %s", e.Availability)
		}
		if e.ID == "disaster_recovery" && e.Availability != Unavailable {
			t.Fatalf("disaster_recovery should be unavailable when hasDR=false, got %s", e.Availability)
		}
	}
	if !found["mailbox_management"] {
		t.Fatal("expected mailbox_management capability")
	}
}

func TestFromRuntime_ModuleMissing(t *testing.T) {
	reg := modules.NewRegistry(zap.NewNop())
	entries := FromRuntime(reg, false, false)
	for _, e := range entries {
		switch e.ID {
		case "mailbox_management", "dns_automation", "firewall":
			if e.Availability != Unavailable {
				t.Fatalf("%s should be unavailable when module missing, got %s", e.ID, e.Availability)
			}
		case "platform_audit", "incident_management", "support_access":
			if e.Availability != Available {
				t.Fatalf("%s should be available regardless of modules, got %s", e.ID, e.Availability)
			}
		}
	}
}

func TestRegistry_DynamicAvailability(t *testing.T) {
	r := New()
	r.Register(Entry{ID: "feat1", Name: "Feature 1", Availability: Available})
	if e, _ := r.Get("feat1"); e.Availability != Available {
		t.Fatal("expected available")
	}
	r.SetAvailability("feat1", Unavailable, "dependency missing")
	if e, _ := r.Get("feat1"); e.Availability != Unavailable {
		t.Fatal("expected unavailable after SetAvailability")
	}
}

func TestRegistry_SortedSnapshot(t *testing.T) {
	r := New()
	r.Register(Entry{ID: "z", Name: "Z"})
	r.Register(Entry{ID: "a", Name: "A"})
	r.Register(Entry{ID: "m", Name: "M"})
	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(snap))
	}
	if snap[0].ID != "a" || snap[1].ID != "m" || snap[2].ID != "z" {
		t.Fatalf("expected sorted order a,m,z got %s,%s,%s", snap[0].ID, snap[1].ID, snap[2].ID)
	}
}
