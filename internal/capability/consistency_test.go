package capability

import (
	"testing"

	"github.com/orvix/orvix/internal/modules"
	"go.uber.org/zap"
)

// TestCapabilityRouterConsistency verifies that the capabilities reported
// by the service match the actual registered modules.
func TestCapabilityRouterConsistency(t *testing.T) {
	reg := modules.NewRegistry(zap.NewNop())
	_ = reg.Register(&testModule{id: "coremail-runtime"})
	_ = reg.Register(&testModule{id: "dns"})

	svc := NewService(reg, nil)
	caps := svc.Capabilities()

	// Build a set of enabled capabilities
	enabled := map[string]bool{}
	for _, c := range caps {
		if c.Enabled {
			enabled[c.ID] = true
		}
	}

	// coremail-runtime enables mailbox/domain/user management
	for _, id := range []string{"mailbox_management", "domain_management", "user_management"} {
		if !enabled[id] {
			t.Fatalf("%s should be enabled when coremail-runtime is registered", id)
		}
	}

	// dns enables dns_automation
	if !enabled["dns_automation"] {
		t.Fatal("dns_automation should be enabled when dns is registered")
	}

	// firewall should NOT be enabled
	if enabled["firewall"] {
		t.Fatal("firewall should not be enabled when firewall module is missing")
	}
}

// TestCapabilityHealthDegradation verifies that a degraded health source
// correctly marks enabled capabilities as degraded.
func TestCapabilityHealthDegradation(t *testing.T) {
	reg := modules.NewRegistry(zap.NewNop())
	_ = reg.Register(&testModule{id: "coremail-runtime"})

	svc := NewService(reg, &fakeHealth{healthy: false})
	caps := svc.Capabilities()

	for _, c := range caps {
		if c.ID == "mailbox_management" {
			if c.Healthy {
				t.Fatal("mailbox_management should be unhealthy")
			}
			if c.Availability != StatusDegraded {
				t.Fatalf("expected degraded, got %s", c.Availability)
			}
		}
	}
}
