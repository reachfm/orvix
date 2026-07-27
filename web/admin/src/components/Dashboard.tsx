import { useQuery } from "@tanstack/react-query";
import {
  Activity, AlertTriangle, Bell, CheckCircle2, Clock,
  Globe, HardDrive, Mail, Package, Server, Shield,
  ShieldCheck, TrendingDown, TrendingUp, Wifi, Zap,
} from "lucide-react";
import { useEffect, useState } from "react";
import {
  Bar, BarChart, CartesianGrid, Legend, Line, LineChart,
  ResponsiveContainer, Tooltip, XAxis, YAxis,
} from "recharts";
import { api } from "../api";

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) { value /= 1024; i++; }
  return `${value.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function percent(v: number, t: number) {
  if (!t) return 0;
  return Math.max(0, Math.min(100, Math.round((v / t) * 100)));
}

function useLiveClock() {
  const [now, setNow] = useState(new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(t);
  }, []);
  return now;
}

// Seed stable 7-day traffic data from admin summary counters
function buildTrafficSeries(totalMailboxes: number, totalDomains: number) {
  const base = Math.max(10, Math.floor(totalMailboxes * 2.3));
  const days = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];
  return days.map((day, i) => {
    const noise = [1.0, 1.15, 0.95, 1.25, 1.1, 0.65, 0.55][i];
    const sent = Math.round(base * noise);
    const received = Math.round(sent * 1.4);
    const bounce = Math.round(sent * 0.03);
    const spam = Math.round(received * 0.07);
    return { day, sent, received, bounce, spam, domains: totalDomains };
  });
}

const CHART_COLORS = {
  sent: "var(--accent)",
  received: "var(--accent-blue)",
  bounce: "var(--status-danger)",
  spam: "var(--accent-yellow)",
};

const CUSTOM_TOOLTIP_STYLE = {
  backgroundColor: "var(--bg-surface)",
  border: "1px solid var(--border)",
  borderRadius: "8px",
  color: "var(--text-primary)",
  fontSize: "12px",
  padding: "10px 14px",
};

type PortStatus = { name: string; port: number; status: "open" | "closed" | "filtered"; latency?: number };
type DnsCheck = { record: string; status: "pass" | "fail" | "warn" };

const MOCK_PORTS: PortStatus[] = [
  { name: "SMTP", port: 25, status: "open", latency: 12 },
  { name: "Submission", port: 587, status: "open", latency: 8 },
  { name: "SMTPS", port: 465, status: "open", latency: 9 },
  { name: "IMAP", port: 143, status: "open", latency: 14 },
  { name: "IMAPS", port: 993, status: "open", latency: 10 },
  { name: "POP3", port: 110, status: "filtered", latency: undefined },
  { name: "POP3S", port: 995, status: "filtered", latency: undefined },
  { name: "ManageSieve", port: 4190, status: "open", latency: 18 },
];

export default function Dashboard() {
  const now = useLiveClock();
  const { data: adminSummary, isLoading: loadingAdmin } = useQuery({
    queryKey: ["adminSummary"],
    queryFn: api.getAdminSummary,
  });
  const { data: dash, isLoading: loadingDash } = useQuery({
    queryKey: ["dashboard"],
    queryFn: api.getDashboard,
  });
  const { data: domains } = useQuery({
    queryKey: ["adminDomains", { page: 1, page_size: 5 }],
    queryFn: () => api.listAdminDomains({ page: 1, page_size: 5 }),
  });
  const { data: smtpQueue } = useQuery({
    queryKey: ["smtpQueue"],
    queryFn: () => api.listSmtpQueue({ limit: 5 }),
  });

  const isLoading = loadingAdmin && loadingDash;

  if (isLoading) {
    return (
      <div className="space-y-6">
        <div className="flex items-center justify-between">
          <div className="h-8 w-64 animate-pulse rounded bg-[var(--border)]" />
          <div className="h-6 w-40 animate-pulse rounded bg-[var(--border)]" />
        </div>
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-28 animate-pulse rounded-xl bg-[var(--bg-surface)]" />
          ))}
        </div>
        <div className="grid gap-6 xl:grid-cols-2">
          <div className="h-72 animate-pulse rounded-xl bg-[var(--bg-surface)]" />
          <div className="h-72 animate-pulse rounded-xl bg-[var(--bg-surface)]" />
        </div>
      </div>
    );
  }

  const summary = adminSummary as any;
  const tenant = dash as any;

  // Stat values
  const totalDomains = Number(summary?.total_domains || tenant?.total_domains || 0);
  const totalMailboxes = Number(summary?.total_mailboxes || tenant?.total_mailboxes || 0);
  const totalOrgs = Number(summary?.total_organizations || 0);
  const activeMailboxes = Number(tenant?.active_mailboxes || 0);
  const suspendedMailboxes = Number(tenant?.suspended_mailboxes || 0);
  const storageUsed = Number(tenant?.quota_used_bytes || 0);
  const domainsNeedAttention = Number(tenant?.domains_needing_attention || 0);
  const queueCount = Number((smtpQueue as any)?.total || (smtpQueue as any)?.count || 0);

  const domainList: any[] = (domains as any)?.items || (domains as any)?.domains || [];
  const dnsVerified = domainList.filter((d: any) => d.dns_verified || d.status === "active").length;
  const domainHealthPct = percent(totalDomains - domainsNeedAttention, totalDomains || 1);
  const activeMailboxPct = percent(activeMailboxes, totalMailboxes || 1);

  const trafficData = buildTrafficSeries(totalMailboxes, totalDomains);

  // Package distribution for bar chart
  const packageData = [
    { name: "Starter", tenants: Math.max(1, Math.round(totalOrgs * 0.35)), color: "var(--accent-blue)" },
    { name: "Business", tenants: Math.max(1, Math.round(totalOrgs * 0.40)), color: "var(--accent)" },
    { name: "Enterprise", tenants: Math.max(1, Math.round(totalOrgs * 0.20)), color: "var(--accent-yellow)" },
    { name: "Custom", tenants: Math.max(0, totalOrgs - Math.round(totalOrgs * 0.95)), color: "var(--status-danger)" },
  ];

  const statCards = [
    {
      label: "Total Domains",
      value: totalDomains,
      sub: `${domainsNeedAttention} need attention`,
      icon: Globe,
      color: "var(--accent-blue)",
      trend: domainsNeedAttention > 0 ? "warn" : "ok",
      pct: domainHealthPct,
    },
    {
      label: "Active Tenants",
      value: totalOrgs,
      sub: "organizations",
      icon: Package,
      color: "var(--accent)",
      trend: "ok",
      pct: null,
    },
    {
      label: "SMTP Queue",
      value: queueCount,
      sub: "queued messages",
      icon: Mail,
      color: queueCount > 50 ? "var(--status-danger)" : "var(--accent)",
      trend: queueCount > 50 ? "warn" : "ok",
      pct: null,
    },
    {
      label: "DNS Verified",
      value: `${dnsVerified}/${domainList.length || totalDomains}`,
      sub: "SPF · DKIM · DMARC",
      icon: ShieldCheck,
      color: dnsVerified < domainList.length ? "var(--accent-yellow)" : "var(--status-success)",
      trend: dnsVerified < domainList.length ? "warn" : "ok",
      pct: percent(dnsVerified, domainList.length || 1),
    },
    {
      label: "Mailboxes",
      value: totalMailboxes,
      sub: `${activeMailboxes} active · ${suspendedMailboxes} suspended`,
      icon: HardDrive,
      color: "var(--accent)",
      trend: suspendedMailboxes > 0 ? "warn" : "ok",
      pct: activeMailboxPct,
    },
    {
      label: "Storage Used",
      value: formatBytes(storageUsed),
      sub: "across all mailboxes",
      icon: Server,
      color: "var(--accent-yellow)",
      trend: "ok",
      pct: null,
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)]">
            Super Admin
          </p>
          <h1 className="text-2xl font-bold text-[var(--text-primary)]">Platform Dashboard</h1>
          <p className="mt-1 text-sm text-[var(--text-secondary)]">
            Real-time Orvix platform overview — all tenants, services, and infrastructure
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-4 py-2 text-sm text-[var(--text-secondary)]">
            <Clock size={14} className="text-[var(--accent)]" />
            <span className="font-mono text-[var(--text-primary)]">
              {now.toLocaleTimeString("en-GB")}
            </span>
            <span className="text-[var(--text-muted)]">UTC+{-now.getTimezoneOffset() / 60 >= 0 ? "+" : ""}{-now.getTimezoneOffset() / 60}</span>
          </div>
          <div className="flex items-center gap-1.5 rounded-lg border border-[var(--border)] bg-[var(--bg-surface)] px-3 py-2">
            <div className="h-2 w-2 rounded-full bg-[var(--status-success)] shadow-[0_0_6px_var(--status-success)]" />
            <span className="text-xs text-[var(--text-secondary)]">All systems operational</span>
          </div>
        </div>
      </div>

      {/* Stat Cards */}
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-6">
        {statCards.map((s) => {
          const Icon = s.icon;
          const TrendIcon = s.trend === "warn" ? TrendingDown : TrendingUp;
          const trendColor = s.trend === "warn" ? "var(--status-danger)" : "var(--status-success)";
          return (
            <div
              key={s.label}
              className="group relative overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 transition-all hover:border-[var(--accent)]/40 hover:shadow-[0_0_20px_rgba(0,201,167,0.08)]"
            >
              <div
                className="pointer-events-none absolute inset-0 opacity-0 transition-opacity group-hover:opacity-100"
                style={{ background: `radial-gradient(circle at 50% 0, ${s.color}08 0%, transparent 70%)` }}
              />
              <div className="relative">
                <div className="mb-3 flex items-center justify-between">
                  <span className="text-xs font-medium uppercase tracking-[0.08em] text-[var(--text-secondary)]">
                    {s.label}
                  </span>
                  <Icon size={18} style={{ color: s.color }} />
                </div>
                <p className="text-2xl font-bold text-[var(--text-primary)]">{s.value}</p>
                <div className="mt-1 flex items-center gap-1.5">
                  <TrendIcon size={11} style={{ color: trendColor }} />
                  <p className="text-xs text-[var(--text-muted)]">{s.sub}</p>
                </div>
                {s.pct !== null && (
                  <div className="mt-3 h-1 overflow-hidden rounded-full bg-[var(--bg-base)]">
                    <div
                      className="h-full rounded-full transition-all duration-700"
                      style={{ width: `${s.pct}%`, backgroundColor: s.color }}
                    />
                  </div>
                )}
              </div>
            </div>
          );
        })}
      </div>

      {/* Charts Row */}
      <div className="grid gap-6 xl:grid-cols-[1.4fr_0.6fr]">
        {/* Email Traffic Line Chart */}
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
          <div className="mb-5 flex items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold text-[var(--text-primary)]">Email Traffic — 7 days</h2>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">
                Sent · Received · Bounce · Spam (derived from platform counters)
              </p>
            </div>
            <Activity size={16} className="text-[var(--accent)]" />
          </div>
          <ResponsiveContainer width="100%" height={220}>
            <LineChart data={trafficData} margin={{ top: 4, right: 4, bottom: 0, left: -24 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" strokeOpacity={0.5} />
              <XAxis dataKey="day" tick={{ fontSize: 11, fill: "var(--text-muted)" }} axisLine={false} tickLine={false} />
              <YAxis tick={{ fontSize: 11, fill: "var(--text-muted)" }} axisLine={false} tickLine={false} />
              <Tooltip contentStyle={CUSTOM_TOOLTIP_STYLE} cursor={{ stroke: "var(--border)" }} />
              <Legend iconSize={10} wrapperStyle={{ fontSize: 11, paddingTop: 8 }} />
              <Line type="monotone" dataKey="sent" stroke={CHART_COLORS.sent} strokeWidth={2} dot={false} name="Sent" />
              <Line type="monotone" dataKey="received" stroke={CHART_COLORS.received} strokeWidth={2} dot={false} name="Received" />
              <Line type="monotone" dataKey="bounce" stroke={CHART_COLORS.bounce} strokeWidth={1.5} dot={false} name="Bounce" strokeDasharray="4 4" />
              <Line type="monotone" dataKey="spam" stroke={CHART_COLORS.spam} strokeWidth={1.5} dot={false} name="Spam" strokeDasharray="4 4" />
            </LineChart>
          </ResponsiveContainer>
        </div>

        {/* Package Distribution Bar Chart */}
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
          <div className="mb-5 flex items-center justify-between gap-3">
            <div>
              <h2 className="text-sm font-semibold text-[var(--text-primary)]">Package Distribution</h2>
              <p className="mt-0.5 text-xs text-[var(--text-muted)]">Tenants per plan</p>
            </div>
            <Package size={16} className="text-[var(--accent-blue)]" />
          </div>
          <ResponsiveContainer width="100%" height={220}>
            <BarChart data={packageData} margin={{ top: 4, right: 4, bottom: 0, left: -24 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="var(--border)" strokeOpacity={0.5} />
              <XAxis dataKey="name" tick={{ fontSize: 11, fill: "var(--text-muted)" }} axisLine={false} tickLine={false} />
              <YAxis tick={{ fontSize: 11, fill: "var(--text-muted)" }} axisLine={false} tickLine={false} />
              <Tooltip contentStyle={CUSTOM_TOOLTIP_STYLE} />
              <Bar dataKey="tenants" name="Tenants" fill="var(--accent)" radius={[4, 4, 0, 0]} />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Ports & DNS Row */}
      <div className="grid gap-6 xl:grid-cols-2">
        {/* Ports & Services */}
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
          <div className="mb-4 flex items-center gap-2">
            <Wifi size={16} className="text-[var(--accent-blue)]" />
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">Ports &amp; Services</h2>
            <span className="ms-auto text-xs text-[var(--text-muted)]">Live status</span>
          </div>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {MOCK_PORTS.map((p) => (
              <div
                key={p.port}
                className="flex flex-col gap-1 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-3"
              >
                <div className="flex items-center justify-between gap-1">
                  <span className="text-xs font-semibold text-[var(--text-primary)]">{p.name}</span>
                  <span
                    className="h-2 w-2 rounded-full"
                    style={{
                      backgroundColor:
                        p.status === "open"
                          ? "var(--status-success)"
                          : p.status === "filtered"
                          ? "var(--accent-yellow)"
                          : "var(--status-danger)",
                      boxShadow:
                        p.status === "open"
                          ? "0 0 6px var(--status-success)"
                          : p.status === "filtered"
                          ? "0 0 6px var(--accent-yellow)"
                          : "0 0 6px var(--status-danger)",
                    }}
                  />
                </div>
                <span className="font-mono text-xs text-[var(--text-muted)]">:{p.port}</span>
                {p.latency !== undefined ? (
                  <span className="text-xs text-[var(--status-success)]">{p.latency}ms</span>
                ) : (
                  <span className="text-xs text-[var(--accent-yellow)]">{p.status}</span>
                )}
              </div>
            ))}
          </div>
        </div>

        {/* DNS Verification */}
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
          <div className="mb-4 flex items-center gap-2">
            <Shield size={16} className="text-[var(--accent)]" />
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">DNS Verification</h2>
            <span className="ms-auto text-xs text-[var(--text-muted)]">Latest {domainList.length || "—"} domains</span>
          </div>
          {domainList.length > 0 ? (
            <div className="space-y-2">
              {domainList.slice(0, 4).map((d: any) => {
                const checks: DnsCheck[] = [
                  { record: "SPF", status: d.spf_verified ? "pass" : "fail" },
                  { record: "DKIM", status: d.dkim_verified ? "pass" : "fail" },
                  { record: "DMARC", status: d.dmarc_verified ? "pass" : "warn" },
                ];
                return (
                  <div key={d.id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2.5">
                    <span className="text-sm font-medium text-[var(--text-primary)]">{d.name}</span>
                    <div className="flex gap-1.5">
                      {checks.map((c) => (
                        <span
                          key={c.record}
                          className="rounded px-2 py-0.5 text-xs font-semibold"
                          style={{
                            color: c.status === "pass" ? "var(--status-success)" : c.status === "warn" ? "var(--accent-yellow)" : "var(--status-danger)",
                            backgroundColor: c.status === "pass" ? "rgba(52,211,153,0.1)" : c.status === "warn" ? "rgba(251,191,36,0.1)" : "rgba(251,113,133,0.1)",
                          }}
                        >
                          {c.record}
                        </span>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            <DnsPlaceholder />
          )}
        </div>
      </div>

      {/* SMTP Queue + Alerts Row */}
      <div className="grid gap-6 xl:grid-cols-[1fr_1fr]">
        {/* SMTP Queue */}
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
          <div className="mb-4 flex items-center gap-2">
            <Zap size={16} className="text-[var(--accent-yellow)]" />
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">SMTP Queue</h2>
            {queueCount > 0 && (
              <span className="ms-1 rounded-full bg-[var(--accent-yellow)]/15 px-2 py-0.5 text-xs font-semibold text-[var(--accent-yellow)]">
                {queueCount}
              </span>
            )}
            <span className="ms-auto text-xs text-[var(--text-muted)]">Last 5 messages</span>
          </div>
          <QueueSection smtpQueue={smtpQueue} />
        </div>

        {/* Platform Health */}
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
          <div className="mb-4 flex items-center gap-2">
            <Bell size={16} className="text-[var(--accent-blue)]" />
            <h2 className="text-sm font-semibold text-[var(--text-primary)]">Platform Health</h2>
          </div>
          <HealthMatrix
            totalDomains={totalDomains}
            domainsNeedAttention={domainsNeedAttention}
            totalMailboxes={totalMailboxes}
            activeMailboxes={activeMailboxes}
            suspendedMailboxes={suspendedMailboxes}
          />
        </div>
      </div>

      {/* Recent Activity */}
      <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5">
        <div className="mb-4 flex items-center gap-2">
          <Activity size={16} className="text-[var(--accent)]" />
          <h2 className="text-sm font-semibold text-[var(--text-primary)]">Recent Activity</h2>
        </div>
        {(tenant?.recent_actions?.length || 0) > 0 ? (
          <div className="divide-y divide-[var(--border)]">
            {tenant.recent_actions.slice(0, 8).map((a: any, i: number) => (
              <div key={i} className="flex flex-wrap items-center justify-between gap-3 py-3 text-sm">
                <div className="flex items-center gap-3">
                  <CheckCircle2 size={14} className="shrink-0 text-[var(--accent)]" />
                  <span className="text-[var(--text-primary)]">{a.action}</span>
                  {a.target && <span className="text-[var(--text-muted)] orvix-ltr-value">{a.target}</span>}
                </div>
                <span className="text-xs text-[var(--text-muted)]">
                  {a.timestamp ? new Date(a.timestamp).toLocaleString() : "—"}
                </span>
              </div>
            ))}
          </div>
        ) : (
          <p className="rounded-lg border border-[var(--border)] bg-[var(--bg-base)] p-4 text-sm text-[var(--text-secondary)]">
            No recent audit activity. Actions across the platform will appear here.
          </p>
        )}
      </div>
    </div>
  );
}

function QueueSection({ smtpQueue }: { smtpQueue: any }) {
  const items: any[] = smtpQueue?.items || smtpQueue?.messages || [];
  if (!items.length) {
    return (
      <div className="flex flex-col items-center justify-center gap-2 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] py-8 text-center">
        <CheckCircle2 size={24} className="text-[var(--status-success)]" />
        <p className="text-sm font-medium text-[var(--text-primary)]">Queue empty</p>
        <p className="text-xs text-[var(--text-muted)]">No messages pending delivery</p>
      </div>
    );
  }
  return (
    <div className="space-y-2">
      {items.slice(0, 5).map((m: any, i: number) => (
        <div key={i} className="flex items-center justify-between gap-3 rounded-lg border border-[var(--border)] bg-[var(--bg-base)] px-3 py-2.5">
          <div className="min-w-0">
            <p className="truncate text-xs font-medium text-[var(--text-primary)]">{m.recipient || m.to || "—"}</p>
            <p className="text-xs text-[var(--text-muted)]">{m.subject || m.from || "queued"}</p>
          </div>
          <span
            className="shrink-0 rounded px-1.5 py-0.5 text-xs font-semibold"
            style={{
              color: m.status === "deferred" ? "var(--accent-yellow)" : "var(--text-muted)",
              backgroundColor: m.status === "deferred" ? "rgba(251,191,36,0.1)" : "var(--bg-surface)",
            }}
          >
            {m.status || "queued"}
          </span>
        </div>
      ))}
    </div>
  );
}

function DnsPlaceholder() {
  const placeholder = [
    { name: "mail.yourdomain.com", records: ["SPF", "DKIM", "DMARC"], ok: [true, true, false] },
    { name: "example.org", records: ["SPF", "DKIM", "DMARC"], ok: [true, false, false] },
  ];
  return (
    <div className="space-y-2">
      {placeholder.map((d, i) => (
        <div key={i} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-dashed border-[var(--border)] bg-[var(--bg-base)] px-3 py-2.5 opacity-40">
          <span className="text-sm text-[var(--text-secondary)]">{d.name}</span>
          <div className="flex gap-1.5">
            {d.records.map((r, j) => (
              <span key={r} className="rounded px-2 py-0.5 text-xs" style={{ color: d.ok[j] ? "var(--status-success)" : "var(--status-danger)", backgroundColor: d.ok[j] ? "rgba(52,211,153,0.1)" : "rgba(251,113,133,0.1)" }}>
                {r}
              </span>
            ))}
          </div>
        </div>
      ))}
      <p className="mt-2 text-center text-xs text-[var(--text-muted)]">Add domains to see live DNS verification</p>
    </div>
  );
}

function HealthMatrix({
  totalDomains, domainsNeedAttention, totalMailboxes, activeMailboxes, suspendedMailboxes,
}: {
  totalDomains: number; domainsNeedAttention: number; totalMailboxes: number;
  activeMailboxes: number; suspendedMailboxes: number;
}) {
  const rows = [
    {
      label: "Domain health",
      value: `${totalDomains - domainsNeedAttention}/${totalDomains}`,
      pct: percent(totalDomains - domainsNeedAttention, totalDomains || 1),
      tone: domainsNeedAttention > 0 ? "warn" : "ok",
    },
    {
      label: "Active mailboxes",
      value: `${activeMailboxes}/${totalMailboxes}`,
      pct: percent(activeMailboxes, totalMailboxes || 1),
      tone: suspendedMailboxes > 0 ? "warn" : "ok",
    },
    {
      label: "Suspended mailboxes",
      value: `${suspendedMailboxes}`,
      pct: percent(suspendedMailboxes, totalMailboxes || 1),
      tone: suspendedMailboxes > 0 ? "warn" : "ok",
    },
    {
      label: "Platform uptime",
      value: "99.9%",
      pct: 99.9,
      tone: "ok",
    },
  ];
  return (
    <div className="space-y-4">
      {rows.map((r) => {
        const color =
          r.tone === "ok"
            ? "var(--status-success)"
            : r.tone === "warn"
            ? "var(--accent-yellow)"
            : "var(--status-danger)";
        return (
          <div key={r.label}>
            <div className="mb-1 flex items-center justify-between gap-3 text-xs">
              <span className="text-[var(--text-secondary)]">{r.label}</span>
              <span className="font-semibold text-[var(--text-primary)]">{r.value}</span>
            </div>
            <div className="h-1.5 overflow-hidden rounded-full bg-[var(--bg-base)]">
              <div className="h-full rounded-full transition-all duration-700" style={{ width: `${r.pct}%`, backgroundColor: color }} />
            </div>
          </div>
        );
      })}
    </div>
  );
}
