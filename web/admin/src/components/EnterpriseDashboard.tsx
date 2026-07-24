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
  const color = tone === "danger" ? "text-[#F87171]" : tone === "warn" ? "text-[#FBBF24]" : "text-[#E8EAF0]";
  return (
    <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
      <p className="text-xs text-[#8B92A8] mb-1">{label}</p>
      <p className={`text-2xl font-bold ${color}`}>{value}</p>
    </div>
  );
}

export default function EnterpriseDashboard() {
  const { data, isLoading, error } = useQuery<AdminSummary>({
    queryKey: ["admin-summary"],
    queryFn: api.getAdminSummary,
  });

  if (isLoading) return <p className="text-[#8B92A8]">Loading...</p>;
  if (error) return <p className="text-[#F87171]">Failed to load enterprise dashboard: {(error as Error).message}</p>;
  if (!data) return <p className="text-[#8B92A8]">No data available.</p>;

  return (
    <div>
      <h2 className="text-xl font-semibold text-[#E8EAF0] mb-1">Enterprise Dashboard</h2>
      <p className="text-[#8B92A8] mb-6">Customer administration overview and metrics.</p>

      <div className="grid grid-cols-4 gap-4 mb-6">
        <StatCard label="Domains" value={data.domains.total} />
        <StatCard label="Mailboxes" value={data.mailboxes.total} />
        <StatCard label="Queue Pending" value={data.queue.pending} tone={data.queue.pending > 0 ? "warn" : "default"} />
        <StatCard label="Queue Failed" value={data.queue.failed} tone={data.queue.failed > 0 ? "danger" : "default"} />
      </div>

      <div className="grid grid-cols-2 gap-4 mb-6">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[#E8EAF0] mb-3">Domains</h3>
          <dl className="space-y-1 text-sm">
            <div className="flex justify-between"><dt className="text-[#8B92A8]">Active</dt><dd className="text-[#34D399]">{data.domains.active}</dd></div>
            <div className="flex justify-between"><dt className="text-[#8B92A8]">Suspended</dt><dd className="text-[#F87171]">{data.domains.suspended}</dd></div>
          </dl>
        </div>
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[#E8EAF0] mb-3">Mailboxes</h3>
          <dl className="space-y-1 text-sm">
            <div className="flex justify-between"><dt className="text-[#8B92A8]">Active</dt><dd className="text-[#34D399]">{data.mailboxes.active}</dd></div>
            <div className="flex justify-between"><dt className="text-[#8B92A8]">Suspended</dt><dd className="text-[#F87171]">{data.mailboxes.suspended}</dd></div>
            <div className="flex justify-between"><dt className="text-[#8B92A8]">Admins</dt><dd className="text-[#E8EAF0]">{data.mailboxes.admin}</dd></div>
          </dl>
        </div>
      </div>

      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4 mb-6">
        <h3 className="text-sm font-semibold text-[#E8EAF0] mb-3">Runtime</h3>
        <p className="text-sm text-[#8B92A8]">
          Status: <span className={data.runtime.status === "ok" ? "text-[#34D399]" : "text-[#F87171]"}>{data.runtime.status}</span>
          {"  •  "}Version: <span className="text-[#E8EAF0]">{data.runtime.version}</span>
        </p>
      </div>

      <div className="grid grid-cols-2 gap-4">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[#E8EAF0] mb-3">Top Domains</h3>
          {data.top_domains.length === 0 ? (
            <p className="text-sm text-[#8B92A8]">No domains yet.</p>
          ) : (
            <ul className="space-y-1 text-sm">
              {data.top_domains.map((d) => (
                <li key={d.domain} className="flex justify-between">
                  <span className="text-[#E8EAF0]">{d.domain}</span>
                  <span className="text-[#8B92A8]">{d.mailbox_count} mailboxes</span>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[#E8EAF0] mb-3">Recent Activity</h3>
          {data.recent_activity.length === 0 ? (
            <p className="text-sm text-[#8B92A8]">No recent activity.</p>
          ) : (
            <ul className="space-y-2 text-sm">
              {data.recent_activity.slice(0, 5).map((a, i) => (
                <li key={i} className="text-[#8B92A8]">
                  <span className="text-[#E8EAF0]">{a.actor}</span> {a.action} <span className="text-[#E8EAF0]">{a.target}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </div>
  );
}
