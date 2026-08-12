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

func TestCapabilities_DerivedFromRuntime(t *testing.T) {
	reg := modules.NewRegistry(zap.NewNop())
	_ = reg.Register(&testModule{id: "coremail-runtime"})
	_ = reg.Register(&testModule{id: "dns"})
	svc := NewService(reg, nil)
	caps := svc.Capabilities()
	if len(caps) == 0 {
		t.Fatal("expected capabilities")
	}
	found := map[string]Capability{}
	for _, c := range caps {
		found[c.ID] = c
	}
	if !found["mailbox_management"].Enabled {
		t.Fatal("mailbox_management should be enabled when coremail-runtime is registered")
	}
	if !found["dns_automation"].Enabled {
		t.Fatal("dns_automation should be enabled when dns is registered")
	}
	if found["firewall"].Enabled {
		t.Fatal("firewall should be disabled when firewall module is missing")
	}
	if !found["platform_audit"].Enabled {
		t.Fatal("platform_audit should always be enabled")
	}
}

func TestCapabilities_HealthSource(t *testing.T) {
	reg := modules.NewRegistry(zap.NewNop())
	_ = reg.Register(&testModule{id: "coremail-runtime"})
	svc := NewService(reg, &fakeHealth{healthy: false})
	caps := svc.Capabilities()
	for _, c := range caps {
		if c.ID == "mailbox_management" {
			if c.Healthy {
				t.Fatal("mailbox_management should be unhealthy when health source reports unhealthy")
			}
			if c.Availability != StatusDegraded {
				t.Fatalf("expected degraded, got %s", c.Availability)
			}
		}
	}
}

func TestCapabilities_AvailabilityStates(t *testing.T) {
	tests := []struct {
		supported, enabled, healthy bool
		want                        Status
	}{
		{false, false, false, StatusUnavailable},
		{true, false, false, StatusDisabled},
		{true, true, false, StatusDegraded},
		{true, true, true, StatusHealthy},
	}
	for _, tt := range tests {
		got := computeAvailability(tt.supported, tt.enabled, tt.healthy)
		if got != tt.want {
			t.Fatalf("computeAvailability(%v,%v,%v)=%s want %s", tt.supported, tt.enabled, tt.healthy, got, tt.want)
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

type fakeHealth struct {
	healthy bool
}

func (f *fakeHealth) HealthStatus(name string) (bool, string) {
	return f.healthy, ""
}

var _ = config.Defaults
var _ = gorm.DB{}
