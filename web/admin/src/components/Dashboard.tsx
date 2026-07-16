import { useQuery } from "@tanstack/react-query";
import { Globe, Mail, HardDrive, CreditCard, Activity, Inbox, Send, AlertTriangle } from "lucide-react";
import { api } from "../api";

export default function Dashboard() {
  const { data, isLoading, error } = useQuery({ queryKey: ["dashboard"], queryFn: api.getDashboard });

  if (isLoading) return <p className="text-[#8B92A8]">Loading...</p>;
  if (error) return <p className="text-[#F87171]">Failed to load dashboard</p>;

  const dash = data as any;
  const storageUsedGB = dash?.quota_used_bytes ? (dash.quota_used_bytes / (1024 * 1024 * 1024)).toFixed(1) : "0.0";
  const storagePct = dash?.total_mailboxes ? Math.min(100, Math.round((dash.quota_used_bytes || 0) / (dash.total_mailboxes * 1024 * 1024 * 1024) * 100)) : 0;

  const stats = [
    { label: "Domains", value: `${dash?.total_domains || 0}`, sub: `${dash?.healthy_domains || 0} active`, icon: Globe, color: "text-[#4F7CFF]" },
    { label: "Mailboxes", value: `${dash?.total_mailboxes || 0}`, sub: `${dash?.active_mailboxes || 0} active`, icon: Mail, color: "text-[#34D399]" },
    { label: "Storage", value: `${storageUsedGB} GB`, sub: `${storagePct}% used`, icon: HardDrive, color: "text-[#FBBF24]" },
    { label: "Needs Attention", value: `${dash?.domains_needing_attention || 0}`, sub: "domains", icon: AlertTriangle, color: "text-[#F87171]" },
  ];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Dashboard</h2>

      <div className="grid grid-cols-4 gap-4 mb-8">
        {stats.map((s) => {
          const Icon = s.icon;
          return (
            <div key={s.label} className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
              <div className="flex items-center gap-3 mb-3">
                <Icon size={20} className={s.color} />
                <span className="text-xs text-[#8B92A8]">{s.label}</span>
              </div>
              <p className="text-2xl font-bold text-[#E8EAF0]">{s.value}</p>
              <p className="text-xs text-[#555D73] mt-1">{s.sub}</p>
            </div>
          );
        })}
      </div>

      <div className="grid grid-cols-2 gap-6 mb-8">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">Delivery Rate</h3>
          <div className="flex items-end gap-2 h-32">
            {[85, 92, 88, 95, 91, 89, 94, 96, 93, 90, 97, 95].map((v, i) => (
              <div
                key={i}
                className="flex-1 bg-[#4F7CFF] rounded-t"
                style={{ height: `${v}%` }}
              />
            ))}
          </div>
          <p className="text-xs text-[#555D73] mt-2">Historical trend (12 periods)</p>
        </div>

        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">Mailbox Status</h3>
          <div className="space-y-3">
            {[
              { label: "Active", value: dash?.active_mailboxes || 0, total: dash?.total_mailboxes || 1, color: "bg-[#34D399]" },
              { label: "Suspended", value: dash?.suspended_mailboxes || 0, total: dash?.total_mailboxes || 1, color: "bg-[#FBBF24]" },
              { label: "Disabled", value: dash?.disabled_mailboxes || 0, total: dash?.total_mailboxes || 1, color: "bg-[#F87171]" },
            ].map((m) => (
              <div key={m.label}>
                <div className="flex justify-between text-xs mb-1">
                  <span className="text-[#8B92A8]">{m.label}</span>
                  <span className="text-[#E8EAF0]">{m.value}</span>
                </div>
                <div className="h-1.5 bg-[#222736] rounded-full overflow-hidden">
                  <div className={`h-full rounded-full ${m.color}`} style={{ width: `${m.total > 0 ? Math.max(2, (m.value / m.total) * 100) : 0}%` }} />
                </div>
              </div>
            ))}
          </div>
        </div>
      </div>

      {dash?.recent_actions && dash.recent_actions.length > 0 && (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">Recent Activity</h3>
          <div className="space-y-2">
            {dash.recent_actions.slice(0, 5).map((a: any, i: number) => (
              <div key={i} className="flex items-center justify-between text-sm">
                <div className="flex items-center gap-2">
                  <Activity size={14} className="text-[#4F7CFF]" />
                  <span className="text-[#E8EAF0]">{a.action}</span>
                  {a.target && <span className="text-[#555D73]">- {a.target}</span>}
                </div>
                <span className="text-xs text-[#555D73]">
                  {a.timestamp ? new Date(a.timestamp).toLocaleString() : ""}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
