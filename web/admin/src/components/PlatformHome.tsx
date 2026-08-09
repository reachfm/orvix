import { Building, HardDrive, Shield, Zap, HeartPulse, ServerCog, Monitor, FileText } from "lucide-react";

// PlatformHome is the Platform Administration landing page for
// portal="platform" identities (Platform Super Admin). It is a
// frontend-only landing view: it makes NO network calls of its own and
// links only to existing, verified platform-owned pages (each backed by
// a router.go route gated with platformMW, confirmed at implementation
// time — see docs/deployment/portal-separation-phase1.md). It must never
// call a tenant-owned endpoint (e.g. /enterprise/*, /users, /customer/*)
// during bootstrap or render.
export default function PlatformHome({
  email,
  onNavigate,
}: {
  email: string;
  onNavigate: (tab: "organizations" | "enterprise" | "backups" | "firewall" | "modules" | "license" | "health") => void;
}) {
  const cards: { id: "organizations" | "enterprise" | "backups" | "firewall" | "modules" | "license" | "health"; label: string; description: string; icon: typeof Building }[] = [
    { id: "organizations", label: "Organizations", description: "All tenant organizations on this platform", icon: Building },
    { id: "enterprise", label: "Summary", description: "Platform-wide totals and metrics across every tenant", icon: Monitor },
    { id: "backups", label: "Backups", description: "Platform backup status and restore jobs", icon: HardDrive },
    { id: "health", label: "Health", description: "System and runtime health", icon: HeartPulse },
    { id: "firewall", label: "Firewall", description: "Mail firewall rules and activity", icon: Shield },
    { id: "modules", label: "Modules", description: "Installed platform modules", icon: Zap },
    { id: "license", label: "License", description: "License tier, expiration, and validation status", icon: FileText },
  ];

  return (
    <div>
      <div className="flex items-center gap-3 mb-2">
        <ServerCog size={22} className="text-[#4F7CFF]" />
        <h2 className="text-2xl font-semibold text-[#E8EAF0]">Platform Administration</h2>
      </div>
      <p className="text-sm text-[#8B92A8] mb-8">
        Signed in as <span className="text-[#E8EAF0]">{email || "platform super admin"}</span> — platform-wide
        administration. This identity has no owning tenant and does not have access to any single
        organization's customer portal data.
      </p>

      <div className="grid grid-cols-3 gap-4">
        {cards.map((c) => {
          const Icon = c.icon;
          return (
            <button
              key={c.id}
              onClick={() => onNavigate(c.id)}
              className="text-left bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4 hover:border-[#4F7CFF] transition-colors"
            >
              <div className="flex items-center gap-3 mb-2">
                <Icon size={20} className="text-[#4F7CFF]" />
                <span className="text-sm font-medium text-[#E8EAF0]">{c.label}</span>
              </div>
              <p className="text-xs text-[#8B92A8]">{c.description}</p>
            </button>
          );
        })}
      </div>
    </div>
  );
}
