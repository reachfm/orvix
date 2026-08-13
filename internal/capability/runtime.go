package capability

import (
	"github.com/orvix/orvix/internal/modules"
)

// Status represents the full availability/health state of a capability.
type Status string

const (
	StatusSupported   Status = "supported"
	StatusEnabled     Status = "enabled"
	StatusDisabled    Status = "disabled"
	StatusHealthy     Status = "healthy"
	StatusDegraded    Status = "degraded"
	StatusUnavailable Status = "unavailable"
)

// Capability describes one runtime capability with its full state.
type Capability struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Supported    bool     `json:"supported"`
	Enabled      bool     `json:"enabled"`
	Healthy      bool     `json:"healthy"`
	Availability Status   `json:"availability"`
	Reason       string   `json:"reason,omitempty"`
	Version      string   `json:"version,omitempty"`
	Dependencies []string `json:"depends_on,omitempty"`
}

// Service derives capabilities from the actual runtime.
type Service struct {
	registry  *modules.Registry
	healthSrc HealthSource
}

// HealthSource provides health status for runtime components.
type HealthSource interface {
	HealthStatus(name string) (healthy bool, reason string)
}

// NewService returns a capability Service bound to the given registry.
func NewService(registry *modules.Registry, healthSrc HealthSource) *Service {
	return &Service{registry: registry, healthSrc: healthSrc}
}

// Capabilities derives the full capability set from the actual runtime.
func (s *Service) Capabilities() []Capability {
	coreMail := s.registry.HasModule("coremail-runtime")
	dns := s.registry.HasModule("dns")
	firewall := s.registry.HasModule("firewall")
	antivirus := s.registry.HasModule("antivirus")
	autoheal := s.registry.HasModule("autoheal")
	migration := s.registry.HasModule("migration-tool")
	provision := s.registry.HasModule("provisioner")
	guardian := s.registry.HasModule("guardian")
	compose := s.registry.HasModule("compose")
	calendar := s.registry.HasModule("calendar")
	collaboration := s.registry.HasModule("collaboration")
	compliance := s.registry.HasModule("compliance")
	intelligence := s.registry.HasModule("intelligence")

	caps := []Capability{
		{ID: "mailbox_management", Name: "Mailbox Management", Supported: coreMail, Enabled: coreMail, Dependencies: []string{"coremail-runtime"}},
		{ID: "domain_management", Name: "Domain Management", Supported: coreMail, Enabled: coreMail, Dependencies: []string{"coremail-runtime"}},
		{ID: "user_management", Name: "User Management", Supported: coreMail, Enabled: coreMail, Dependencies: []string{"coremail-runtime"}},
		{ID: "dns_automation", Name: "DNS Automation", Supported: dns, Enabled: dns, Dependencies: []string{"dns"}},
		{ID: "firewall", Name: "Firewall", Supported: firewall, Enabled: firewall, Dependencies: []string{"firewall"}},
		{ID: "antivirus", Name: "Antivirus", Supported: antivirus, Enabled: antivirus, Dependencies: []string{"antivirus"}},
		{ID: "autoheal", Name: "Auto Healing", Supported: autoheal, Enabled: autoheal, Dependencies: []string{"autoheal"}},
		{ID: "migration_tool", Name: "Migration Tool", Supported: migration, Enabled: migration, Dependencies: []string{"migration-tool"}},
		{ID: "provisioning", Name: "Bulk Provisioning", Supported: provision, Enabled: provision, Dependencies: []string{"provisioner"}},
		{ID: "guardian", Name: "Guardian Email Analysis", Supported: guardian, Enabled: guardian, Dependencies: []string{"guardian"}},
		{ID: "compose", Name: "Email Composition", Supported: compose, Enabled: compose, Dependencies: []string{"compose"}},
		{ID: "calendar", Name: "Calendar", Supported: calendar, Enabled: calendar, Dependencies: []string{"calendar"}},
		{ID: "collaboration", Name: "Collaboration", Supported: collaboration, Enabled: collaboration, Dependencies: []string{"collaboration"}},
		{ID: "compliance", Name: "Compliance", Supported: compliance, Enabled: compliance, Dependencies: []string{"compliance"}},
		{ID: "intelligence", Name: "Intelligence", Supported: intelligence, Enabled: intelligence, Dependencies: []string{"intelligence"}},
		{ID: "platform_audit", Name: "Platform Audit", Supported: true, Enabled: true},
		{ID: "incident_management", Name: "Incident Management", Supported: true, Enabled: true},
		{ID: "support_access", Name: "Support Access", Supported: true, Enabled: true},
	}

	for i := range caps {
		caps[i].Healthy = s.healthy(caps[i].ID, caps[i].Enabled)
		caps[i].Availability = computeAvailability(caps[i].Supported, caps[i].Enabled, caps[i].Healthy)
	}
	return caps
}

// healthy reports whether a capability that is enabled is currently healthy.
func (s *Service) healthy(id string, enabled bool) bool {
	if !enabled {
		return false
	}
	if s.healthSrc == nil {
		return true
	}
	h, reason := s.healthSrc.HealthStatus(id)
	if !h && reason != "" {
		// Could surface reason via logging, but never expose internals.
		_ = reason
	}
	return h
}

func computeAvailability(supported, enabled, healthy bool) Status {
	if !supported {
		return StatusUnavailable
	}
	if !enabled {
		return StatusDisabled
	}
	if healthy {
		return StatusHealthy
	}
	return StatusDegraded
}
