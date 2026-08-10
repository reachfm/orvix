package capability

import (
	"github.com/orvix/orvix/internal/modules"
)

// FromRuntime builds a capability snapshot from the actual registered
// modules and runtime flags. It never claims a capability is available
// when its runtime dependency is missing.
func FromRuntime(registry *modules.Registry, hasUpdater bool, hasDR bool) []Entry {
	r := New()
	coreMail := registry.HasModule("coremail-runtime")
	dns := registry.HasModule("dns")
	firewall := registry.HasModule("firewall")
	antivirus := registry.HasModule("antivirus")
	autoheal := registry.HasModule("autoheal")
	migration := registry.HasModule("migration-tool")
	provision := registry.HasModule("provisioner")

	r.Register(Entry{ID: "mailbox_management", Name: "Mailbox Management", Availability: avail(coreMail), Permission: "tenant_admin", DependsOn: []string{"coremail-runtime"}})
	r.Register(Entry{ID: "domain_management", Name: "Domain Management", Availability: avail(coreMail), Permission: "tenant_admin", DependsOn: []string{"coremail-runtime"}})
	r.Register(Entry{ID: "dns_automation", Name: "DNS Automation", Availability: avail(dns), Permission: "tenant_admin", DependsOn: []string{"dns"}})
	r.Register(Entry{ID: "firewall", Name: "Firewall", Availability: avail(firewall), Permission: "tenant_admin", DependsOn: []string{"firewall"}})
	r.Register(Entry{ID: "antivirus", Name: "Antivirus", Availability: avail(antivirus), Permission: "tenant_admin", DependsOn: []string{"antivirus"}})
	r.Register(Entry{ID: "autoheal", Name: "Auto Healing", Availability: avail(autoheal), Permission: "platform_admin", DependsOn: []string{"autoheal"}})
	r.Register(Entry{ID: "migration_tool", Name: "Migration Tool", Availability: avail(migration), Permission: "tenant_admin", DependsOn: []string{"migration-tool"}})
	r.Register(Entry{ID: "provisioning", Name: "Bulk Provisioning", Availability: avail(provision), Permission: "tenant_admin", DependsOn: []string{"provisioner"}})
	r.Register(Entry{ID: "disaster_recovery", Name: "Disaster Recovery", Availability: avail(hasDR), Permission: "platform_admin", DependsOn: []string{"backup-restore"}})
	r.Register(Entry{ID: "update_management", Name: "Update Management", Availability: avail(hasUpdater), Permission: "platform_admin", DependsOn: []string{"updater"}})
	r.Register(Entry{ID: "platform_audit", Name: "Platform Audit", Availability: Available, Permission: "platform_admin"})
	r.Register(Entry{ID: "incident_management", Name: "Incident Management", Availability: Available, Permission: "platform_admin"})
	r.Register(Entry{ID: "support_access", Name: "Support Access", Availability: Available, Permission: "platform_admin"})
	return r.Snapshot()
}

func avail(ok bool) Availability {
	if ok {
		return Available
	}
	return Unavailable
}
