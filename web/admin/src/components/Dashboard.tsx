import { useQuery } from "@tanstack/react-query";
import { Activity, AlertTriangle, CheckCircle2, Globe, HardDrive, Mail, ShieldAlert } from "lucide-react";
import { api } from "../api";

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
}

function percent(value: number, total: number): number {
  if (!Number.isFinite(value) || !Number.isFinite(total) || total <= 0) return 0;
  return Math.max(0, Math.min(100, Math.round((value / total) * 100)));
}

export default function Dashboard() {
  const { data, isLoading, error } = useQuery({ queryKey: ["dashboard"], queryFn: api.getDashboard });

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="h-8 w-64 animate-pulse rounded bg-[var(--border)]" />
        <div className="surface-grid-stats">
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className="h-28 animate-pulse rounded-xl bg-[var(--bg-surface)]" />
          ))}
        </div>
      </div>
    );
  }
  if (error) {
    return (
      <div className="surface-card p-6">
        <p className="text-sm text-[var(--danger)]">Failed to load dashboard: {(error as Error).message}</p>
        <p className="mt-2 text-xs text-[var(--text-muted)]">Ensure the backend API is reachable and your session is valid.</p>
      </div>
    );
  }

  const dash = data as any;
  const totalDomains = Number(dash?.total_domains || 0);
  const healthyDomains = Number(dash?.healthy_domains || 0);
  const domainsNeedingAttention = Number(dash?.domains_needing_attention || 0);
  const totalMailboxes = Number(dash?.total_mailboxes || 0);
  const activeMailboxes = Number(dash?.active_mailboxes || 0);
  const suspendedMailboxes = Number(dash?.suspended_mailboxes || 0);
  const disabledMailboxes = Number(dash?.disabled_mailboxes || 0);
  const quotaUsedBytes = Number(dash?.quota_used_bytes || 0);
  const activeMailboxPct = percent(activeMailboxes, totalMailboxes);
  const domainHealthPct = percent(healthyDomains, totalDomains);
  const mailboxIssues = suspendedMailboxes + disabledMailboxes;

  const stats = [
    { label: "Domains", value: `${totalDomains}`, sub: `${healthyDomains} healthy`, icon: Globe, color: "text-[var(--accent-blue)]", pct: domainHealthPct },
    { label: "Mailboxes", value: `${totalMailboxes}`, sub: `${activeMailboxes} active`, icon: Mail, color: "text-[var(--accent)]", pct: activeMailboxPct },
    { label: "Storage used", value: formatBytes(quotaUsedBytes), sub: "from mailbox usage", icon: HardDrive, color: "text-[var(--warning)]", pct: null },
    { label: "Needs attention", value: `${domainsNeedingAttention + mailboxIssues}`, sub: `${domainsNeedingAttention} domains, ${mailboxIssues} mailboxes`, icon: AlertTriangle, color: "text-[var(--danger)]", pct: null },
  ];

  return (
    <div className="space-y-6">
      <section className="overflow-hidden rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] shadow-[var(--shadow-deep)]">
        <div className="border-b border-[var(--border)] p-5">
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div>
              <p className="mb-2 text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)]">Tenant command center</p>
              <h2 className="text-2xl font-semibold text-[var(--text-primary)]">Operational dashboard</h2>
              <p className="mt-2 max-w-3xl text-sm text-[var(--text-secondary)]">
                Live tenant posture from Orvix APIs. Unavailable telemetry is shown as unavailable rather than simulated.
              </p>
            </div>
            <div className="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-4 py-3">
              <div className="flex items-center gap-2 text-sm text-[var(--text-primary)]">
                <CheckCircle2 size={16} className="text-[var(--accent)]" />
                Data source: `/api/v1/enterprise/dashboard`
              </div>
              <p className="mt-1 text-xs text-[var(--text-muted)]">Tenant-scoped and authenticated</p>
            </div>
          </div>
        </div>

        <div className="grid gap-4 p-5 sm:grid-cols-2 xl:grid-cols-4">
          {stats.map((s) => {
            const Icon = s.icon;
            return (
              <div key={s.label} className="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-4">
                <div className="mb-3 flex items-center justify-between gap-3">
                  <span className="text-xs font-medium uppercase tracking-[0.08em] text-[var(--text-secondary)]">{s.label}</span>
                  <Icon size={19} className={s.color} />
                </div>
                <p className="text-2xl font-bold text-[var(--text-primary)]">{s.value}</p>
                <p className="mt-1 text-xs text-[var(--text-muted)]">{s.sub}</p>
                {s.pct !== null && (
                  <div className="mt-4 h-1.5 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
                    <div className="h-full rounded-full bg-[var(--accent)]" style={{ width: `${s.pct}%` }} />
                  </div>
                )}
              </div>
            );
          })}
        </div>
      </section>

      <div className="grid gap-6 xl:grid-cols-[1.2fr_0.8fr]">
        <section className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-5">
          <div className="mb-5 flex items-center justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold text-[var(--text-primary)]">Readiness matrix</h3>
              <p className="mt-1 text-xs text-[var(--text-muted)]">Derived from returned domain and mailbox counts.</p>
            </div>
            <ShieldAlert size={18} className={domainsNeedingAttention || mailboxIssues ? "text-[var(--warning)]" : "text-[var(--accent)]"} />
          </div>
          <div className="space-y-4">
            <HealthRow label="Domain health" value={`${healthyDomains}/${totalDomains}`} pct={domainHealthPct} tone={domainsNeedingAttention > 0 ? "warn" : "ok"} />
            <HealthRow label="Active mailbox ratio" value={`${activeMailboxes}/${totalMailboxes}`} pct={activeMailboxPct} tone={mailboxIssues > 0 ? "warn" : "ok"} />
            <HealthRow label="Suspended mailboxes" value={`${suspendedMailboxes}`} pct={percent(suspendedMailboxes, totalMailboxes)} tone={suspendedMailboxes > 0 ? "warn" : "ok"} />
            <HealthRow label="Disabled mailboxes" value={`${disabledMailboxes}`} pct={percent(disabledMailboxes, totalMailboxes)} tone={disabledMailboxes > 0 ? "danger" : "ok"} />
          </div>
        </section>

        <section className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-5">
          <h3 className="text-sm font-semibold text-[var(--text-primary)]">Telemetry availability</h3>
          <p className="mt-1 text-xs text-[var(--text-muted)]">This page does not fabricate delivery trend data.</p>
          <div className="mt-5 rounded-lg border border-dashed border-[var(--border-strong)] bg-[var(--bg-base)] p-4">
            <p className="text-sm text-[var(--text-primary)]">Delivery time-series unavailable</p>
            <p className="mt-2 text-xs leading-5 text-[var(--text-secondary)]">
              Historical send, receive, and delivery-rate charts require a backend time-series endpoint. Until one is available, Orvix shows current counters and audit activity only.
            </p>
          </div>
        </section>
      </div>

      <section className="rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] p-5">
        <div className="mb-4 flex items-center gap-2">
          <Activity size={18} className="text-[var(--accent)]" />
          <h3 className="text-sm font-semibold text-[var(--text-primary)]">Recent Activity</h3>
        </div>
        {dash?.recent_actions && dash.recent_actions.length > 0 ? (
          <div className="divide-y divide-[var(--border)]">
            {dash.recent_actions.slice(0, 8).map((a: any, i: number) => (
              <div key={i} className="flex flex-wrap items-center justify-between gap-3 py-3 text-sm">
                <div className="min-w-0">
                  <span className="text-[var(--text-primary)]">{a.action}</span>
                  {a.target && <span className="ms-2 text-[var(--text-muted)] orvix-ltr-value">{a.target}</span>}
                </div>
                <span className="text-xs text-[var(--text-muted)]">
                  {a.timestamp ? new Date(a.timestamp).toLocaleString() : "No timestamp"}
                </span>
              </div>
            ))}
          </div>
        ) : (
          <p className="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-4 text-sm text-[var(--text-secondary)]">
            No recent audit activity returned for this tenant.
          </p>
        )}
      </section>
    </div>
  );
}

function HealthRow({ label, value, pct, tone }: { label: string; value: string; pct: number; tone: "ok" | "warn" | "danger" }) {
  const color = tone === "danger" ? "bg-[var(--danger)]" : tone === "warn" ? "bg-[var(--warning)]" : "bg-[var(--accent)]";
  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-3 text-xs">
        <span className="text-[var(--text-secondary)]">{label}</span>
        <span className="font-medium text-[var(--text-primary)]">{value}</span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-[var(--bg-subtle)]">
        <div className={`h-full rounded-full ${color}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}
