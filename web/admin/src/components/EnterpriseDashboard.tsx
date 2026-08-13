import { useQuery } from "@tanstack/react-query";
import { api } from "../api";

interface AdminSummary {
  domains: { total: number; active: number; suspended: number };
  mailboxes: { total: number; active: number; suspended: number; admin: number };
  queue: { total: number; pending: number; deferred: number; failed: number };
  audit: { recent: number };
  runtime: { status: string; version: string };
  recent_activity: { action: string; actor: string; target: string; result: string; timestamp: string }[];
  top_domains: { domain: string; mailbox_count: number }[];
}

function StatCard({ label, value, tone }: { label: string; value: number | string; tone?: "default" | "warn" | "danger" }) {
  const color = tone === "danger" ? "text-[var(--danger)]" : tone === "warn" ? "text-[var(--warning)]" : "text-[var(--text-primary)]";
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
      <p className="text-xs text-[var(--text-secondary)] mb-1">{label}</p>
      <p className={`text-2xl font-bold ${color}`}>{value}</p>
    </div>
  );
}

export default function EnterpriseDashboard() {
  const { data, isLoading, error } = useQuery<AdminSummary>({
    queryKey: ["admin-summary"],
    queryFn: api.getAdminSummary,
  });

  if (isLoading) return <p className="text-[var(--text-secondary)]">Loading...</p>;
  if (error) return <p className="text-[var(--danger)]">Failed to load enterprise dashboard: {(error as Error).message}</p>;
  if (!data) return <p className="text-[var(--text-secondary)]">No data available.</p>;

  return (
    <div>
      <h2 className="text-xl font-semibold text-[var(--text-primary)] mb-1">Platform Summary</h2>
      <p className="text-[var(--text-secondary)] mb-6">Platform-wide totals and metrics across every tenant.</p>

      <div className="grid grid-cols-4 gap-4 mb-6">
        <StatCard label="Domains" value={data.domains.total} />
        <StatCard label="Mailboxes" value={data.mailboxes.total} />
        <StatCard label="Queue Pending" value={data.queue.pending} tone={data.queue.pending > 0 ? "warn" : "default"} />
        <StatCard label="Queue Failed" value={data.queue.failed} tone={data.queue.failed > 0 ? "danger" : "default"} />
      </div>

      <div className="grid grid-cols-2 gap-4 mb-6">
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Domains</h3>
          <dl className="space-y-1 text-sm">
            <div className="flex justify-between"><dt className="text-[var(--text-secondary)]">Active</dt><dd className="text-[var(--success)]">{data.domains.active}</dd></div>
            <div className="flex justify-between"><dt className="text-[var(--text-secondary)]">Suspended</dt><dd className="text-[var(--danger)]">{data.domains.suspended}</dd></div>
          </dl>
        </div>
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Mailboxes</h3>
          <dl className="space-y-1 text-sm">
            <div className="flex justify-between"><dt className="text-[var(--text-secondary)]">Active</dt><dd className="text-[var(--success)]">{data.mailboxes.active}</dd></div>
            <div className="flex justify-between"><dt className="text-[var(--text-secondary)]">Suspended</dt><dd className="text-[var(--danger)]">{data.mailboxes.suspended}</dd></div>
            <div className="flex justify-between"><dt className="text-[var(--text-secondary)]">Admins</dt><dd className="text-[var(--text-primary)]">{data.mailboxes.admin}</dd></div>
          </dl>
        </div>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 mb-6">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Runtime</h3>
        <p className="text-sm text-[var(--text-secondary)]">
          Status: <span className={data.runtime.status === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}>{data.runtime.status}</span>
          {"  •  "}Version: <span className="text-[var(--text-primary)]">{data.runtime.version}</span>
        </p>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Top Domains</h3>
          {data.top_domains.length === 0 ? (
            <p className="text-sm text-[var(--text-secondary)]">No domains yet.</p>
          ) : (
            <ul className="space-y-1 text-sm">
              {data.top_domains.map((d) => (
                <li key={d.domain} className="flex justify-between">
                  <span className="text-[var(--text-primary)]">{d.domain}</span>
                  <span className="text-[var(--text-secondary)]">{d.mailbox_count} mailboxes</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Recent Activity</h3>
          {data.recent_activity.length === 0 ? (
            <p className="text-sm text-[var(--text-secondary)]">No recent activity.</p>
          ) : (
            <ul className="space-y-2 text-sm">
              {data.recent_activity.slice(0, 5).map((a, i) => (
                <li key={i} className="text-[var(--text-secondary)]">
                  <span className="text-[var(--text-primary)]">{a.actor}</span> {a.action} <span className="text-[var(--text-primary)]">{a.target}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
