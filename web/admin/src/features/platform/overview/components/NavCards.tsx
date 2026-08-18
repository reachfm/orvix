import { Building, HardDrive, Shield, Zap, HeartPulse, Monitor, Send, Settings, ShieldAlert, Globe, Mail, Users, AtSign, BarChart, KeyRound } from "lucide-react";

export type PlatformNavTarget =
  | "organizations" | "enterprise" | "mail-operations" | "reliability"
  | "platform-security" | "firewall" | "modules" | "platform-configuration" | "health"
  | "platform-domains" | "platform-mailboxes" | "platform-aliases" | "platform-groups"
  | "platform-relays" | "platform-suppressions" | "platform-deliverability" | "platform-bulk-mailboxes"
  | "platform-audit";

const CARDS: { id: PlatformNavTarget; label: string; description: string; icon: typeof Building }[] = [
  { id: "organizations", label: "Organizations", description: "All tenant organizations on this platform", icon: Building },
  { id: "enterprise", label: "Summary", description: "Platform-wide totals and metrics across every tenant", icon: Monitor },
  { id: "platform-domains", label: "Domains", description: "Platform-wide domain inventory, lifecycle and mail access policy", icon: Globe },
  { id: "platform-mailboxes", label: "Mailboxes", description: "Platform-wide mailbox inventory, quota, credentials and lifecycle", icon: Mail },
  { id: "platform-aliases", label: "Aliases", description: "Alias inventory and forwarding management per tenant", icon: AtSign },
  { id: "platform-groups", label: "Groups", description: "Group inventory and membership views", icon: Users },
  { id: "platform-relays", label: "Relays", description: "Outbound relay endpoints, credentials and connectivity tests", icon: KeyRound },
  { id: "mail-operations", label: "Mail Queue", description: "Message queue with attribution, retry, bounce, cancel", icon: Send },
  { id: "platform-suppressions", label: "Suppressions", description: "Suppression lifecycle: add, release, reactivate, history", icon: Shield },
  { id: "platform-deliverability", label: "Deliverability", description: "Real delivery metrics, breakdowns and safe events", icon: BarChart },
  { id: "platform-bulk-mailboxes", label: "Bulk Mailboxes", description: "Bulk mailbox status transitions via the production endpoint", icon: Users },
  { id: "reliability", label: "Reliability", description: "Backups, restores, updates, monitoring, storage, cluster", icon: HardDrive },
  { id: "platform-security", label: "Security", description: "Audit log, SSL/ACME, antivirus, guardian, self-heal, log rules", icon: ShieldAlert },
  { id: "firewall", label: "Firewall", description: "Mail firewall rules and activity", icon: Shield },
  { id: "platform-configuration", label: "Configuration", description: "Runtime settings and feature flags", icon: Settings },
  { id: "modules", label: "Modules", description: "Installed platform modules", icon: Zap },
  { id: "health", label: "Health", description: "System and runtime health", icon: HeartPulse },
];

// Every card navigates to a real page backed by a verified
// platformMW-gated route — this is a navigation index, never a
// decorative dashboard tile with its own fabricated numbers.
export default function NavCards({ onNavigate }: { onNavigate: (tab: PlatformNavTarget) => void }) {
  return (
    <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
      {CARDS.map((c) => {
        const Icon = c.icon;
        return (
          <button
            key={c.id}
            onClick={() => onNavigate(c.id)}
            className="text-left bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 hover:border-[var(--accent)] transition-colors"
          >
            <div className="flex items-center gap-3 mb-2">
              <Icon size={20} className="text-[var(--accent)]" />
              <span className="text-sm font-medium text-[var(--text-primary)]">{c.label}</span>
            </div>
            <p className="text-xs text-[var(--text-secondary)]">{c.description}</p>
          </button>
        );
      })}
    </div>
  );
}
