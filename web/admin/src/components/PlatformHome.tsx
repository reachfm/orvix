import { useQuery } from "@tanstack/react-query";
import { Building, HardDrive, Shield, Zap, HeartPulse, ServerCog, Monitor, FileText, Send, Settings, ShieldAlert, Loader2, AlertCircle } from "lucide-react";
import { api } from "../api";

// PlatformHome is the Platform Administration landing page for
// portal="platform" identities (Platform Super Admin). It fetches the
// real platform-wide dashboard (/platform/dashboard, platformMW-gated —
// aggregates totals across every tenant) and links to every implemented
// platform-owned page (each backed by a router.go route gated with
// platformMW, confirmed at implementation time — see
// docs/deployment/platform-console-capability-matrix.md). It must never
// call a tenant-owned endpoint (e.g. /enterprise/*, /users, /customer/*)
// during bootstrap or render.
type PlatformNavTarget = "organizations" | "enterprise" | "mail-operations" | "reliability" | "platform-security" | "firewall" | "modules" | "platform-configuration" | "license" | "health";

interface PlatformDashboardData {
  tenants?: { total?: number; active?: number };
  mailboxes?: { total?: number };
  domains?: { total?: number };
  queue?: { pending?: number; failed?: number };
}

export default function PlatformHome({
  email,
  onNavigate,
}: {
  email: string;
  onNavigate: (tab: PlatformNavTarget) => void;
}) {
  const dashQ = useQuery<PlatformDashboardData>({ queryKey: ["platform-dashboard"], queryFn: api.getPlatformDashboard });

  const cards: { id: PlatformNavTarget; label: string; description: string; icon: typeof Building }[] = [
    { id: "organizations", label: "Organizations", description: "All tenant organizations on this platform", icon: Building },
    { id: "enterprise", label: "Summary", description: "Platform-wide totals and metrics across every tenant", icon: Monitor },
    { id: "mail-operations", label: "Mail Operations", description: "Message queue: search, retry, bounce, cancel", icon: Send },
    { id: "reliability", label: "Reliability", description: "Backups, restores, updates, monitoring, storage, cluster", icon: HardDrive },
    { id: "platform-security", label: "Security", description: "Audit log, SSL/ACME, antivirus, guardian, self-heal, log rules", icon: ShieldAlert },
    { id: "firewall", label: "Firewall", description: "Mail firewall rules and activity", icon: Shield },
    { id: "platform-configuration", label: "Configuration", description: "Runtime settings and feature flags", icon: Settings },
    { id: "modules", label: "Modules", description: "Installed platform modules", icon: Zap },
    { id: "license", label: "License", description: "License tier, expiration, and validation status", icon: FileText },
    { id: "health", label: "Health", description: "System and runtime health", icon: HeartPulse },
  ];

  return (
    <div>
      <div className="flex items-center gap-3 mb-2">
        <ServerCog size={22} className="text-[var(--accent)]" />
        <h2 className="text-2xl font-semibold text-[var(--text-primary)]">Platform Administration</h2>
      </div>
      <p className="text-sm text-[var(--text-secondary)] mb-6">
        Signed in as <span className="text-[var(--text-primary)]">{email || "platform super admin"}</span> — platform-wide
        administration. This identity has no owning tenant and does not have access to any single
        organization's customer portal data.
      </p>

      {dashQ.isLoading ? (
        <div className="flex items-center gap-2 text-[var(--text-secondary)] mb-8"><Loader2 size={16} className="animate-spin" /> Loading platform totals…</div>
      ) : dashQ.error ? (
        <div className="flex items-center gap-2 text-[var(--danger)] text-sm mb-8"><AlertCircle size={16} /> Failed to load platform dashboard: {(dashQ.error as Error).message}</div>
      ) : dashQ.data ? (
        <div className="grid grid-cols-4 gap-4 mb-8">
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
            <p className="text-xs text-[var(--text-secondary)] mb-1">Tenants</p>
            <p className="text-2xl font-bold text-[var(--text-primary)]">{dashQ.data.tenants?.total ?? "—"}</p>
            <p className="text-xs text-[var(--text-muted)] mt-1">{dashQ.data.tenants?.active ?? 0} active</p>
          </div>
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
            <p className="text-xs text-[var(--text-secondary)] mb-1">Mailboxes</p>
            <p className="text-2xl font-bold text-[var(--text-primary)]">{dashQ.data.mailboxes?.total ?? "—"}</p>
          </div>
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
            <p className="text-xs text-[var(--text-secondary)] mb-1">Domains</p>
            <p className="text-2xl font-bold text-[var(--text-primary)]">{dashQ.data.domains?.total ?? "—"}</p>
          </div>
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
            <p className="text-xs text-[var(--text-secondary)] mb-1">Queue</p>
            <p className={`text-2xl font-bold ${(dashQ.data.queue?.failed ?? 0) > 0 ? "text-[var(--danger)]" : "text-[var(--text-primary)]"}`}>{dashQ.data.queue?.pending ?? 0}</p>
            <p className="text-xs text-[var(--text-muted)] mt-1">{dashQ.data.queue?.failed ?? 0} failed</p>
          </div>
        </div>
      ) : null}

      <div className="grid grid-cols-3 gap-4">
        {cards.map((c) => {
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
    </div>
  );
}
