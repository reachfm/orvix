import { useQuery } from "@tanstack/react-query";
import { api } from "../api";

interface FirewallRule {
  id: number;
  name: string;
  condition: string;
  action: string;
  priority: number;
  enabled: boolean;
}

interface FirewallLog {
  id: number;
  ip: string;
  domain: string;
  sender: string;
  recipient: string;
  created_at: string;
}

export default function Firewall() {
  const rulesQuery = useQuery<FirewallRule[]>({ queryKey: ["firewall-rules"], queryFn: api.listFirewallRules });
  const logsQuery = useQuery<FirewallLog[]>({ queryKey: ["firewall-logs"], queryFn: api.listFirewallLogs });

  const rules = rulesQuery.data || [];
  const logs = logsQuery.data || [];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Mail Firewall</h2>

      <div className="grid grid-cols-2 gap-4 mb-6">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <p className="text-xs text-[#8B92A8] mb-1">Recent Log Entries</p>
          <p className="text-2xl font-bold text-[#E8EAF0]">{logsQuery.isLoading ? "…" : logs.length}</p>
        </div>
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <p className="text-xs text-[#8B92A8] mb-1">Active Rules</p>
          <p className="text-2xl font-bold text-[#34D399]">{rulesQuery.isLoading ? "…" : rules.filter((r) => r.enabled).length}</p>
        </div>
      </div>

      <h3 className="text-sm font-semibold text-[#E8EAF0] mb-3">Rules</h3>
      {rulesQuery.isLoading ? (
        <p className="text-[#8B92A8] mb-6">Loading rules...</p>
      ) : rulesQuery.error ? (
        <p className="text-[#F87171] mb-6">Failed to load rules: {(rulesQuery.error as Error).message}</p>
      ) : rules.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center text-[#8B92A8] mb-6">
          No firewall rules configured.
        </div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden mb-6">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-4 text-[#8B92A8] font-medium">Rule</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Condition</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Action</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Priority</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((r) => (
                <tr key={r.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-4 text-[#E8EAF0]">{r.name}</td>
                  <td className="p-4 text-[#8B92A8]">{r.condition}</td>
                  <td className="p-4">
                    <span className={`px-2 py-1 text-xs rounded-full ${
                      r.action === "block" ? "bg-[#F87171]/10 text-[#F87171]" :
                      r.action === "throttle" ? "bg-[#FBBF24]/10 text-[#FBBF24]" :
                      "bg-[#34D399]/10 text-[#34D399]"
                    }`}>{r.action}</span>
                  </td>
                  <td className="p-4 text-[#8B92A8]">{r.priority}</td>
                  <td className="p-4">
                    <span className={`px-2 py-1 text-xs rounded-full ${r.enabled ? "bg-[#34D399]/10 text-[#34D399]" : "bg-[#8B92A8]/10 text-[#8B92A8]"}`}>
                      {r.enabled ? "enabled" : "disabled"}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <h3 className="text-sm font-semibold text-[#E8EAF0] mb-3">Recent Activity</h3>
      {logsQuery.isLoading ? (
        <p className="text-[#8B92A8]">Loading logs...</p>
      ) : logsQuery.error ? (
        <p className="text-[#F87171]">Failed to load logs: {(logsQuery.error as Error).message}</p>
      ) : logs.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center text-[#8B92A8]">
          No firewall activity recorded yet.
        </div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-4 text-[#8B92A8] font-medium">IP</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Domain</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Sender</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Recipient</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Time</th>
              </tr>
            </thead>
            <tbody>
              {logs.map((l) => (
                <tr key={l.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-4 text-[#E8EAF0]">{l.ip}</td>
                  <td className="p-4 text-[#8B92A8]">{l.domain}</td>
                  <td className="p-4 text-[#8B92A8]">{l.sender || "-"}</td>
                  <td className="p-4 text-[#8B92A8]">{l.recipient || "-"}</td>
                  <td className="p-4 text-[#8B92A8]">{new Date(l.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
