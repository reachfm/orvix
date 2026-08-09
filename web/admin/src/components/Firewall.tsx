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
      <h2 className="text-2xl font-semibold mb-6 text-[var(--text-primary)]">Mail Firewall</h2>

      <div className="grid grid-cols-2 gap-4 mb-6">
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <p className="text-xs text-[var(--text-secondary)] mb-1">Recent Log Entries</p>
          <p className="text-2xl font-bold text-[var(--text-primary)]">{logsQuery.isLoading ? "…" : logs.length}</p>
        </div>
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <p className="text-xs text-[var(--text-secondary)] mb-1">Active Rules</p>
          <p className="text-2xl font-bold text-[var(--success)]">{rulesQuery.isLoading ? "…" : rules.filter((r) => r.enabled).length}</p>
        </div>
      </div>

      <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Rules</h3>
      {rulesQuery.isLoading ? (
        <p className="text-[var(--text-secondary)] mb-6">Loading rules...</p>
      ) : rulesQuery.error ? (
        <p className="text-[var(--danger)] mb-6">Failed to load rules: {(rulesQuery.error as Error).message}</p>
      ) : rules.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] mb-6">
          No firewall rules configured.
        </div>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden mb-6">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)]">
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Rule</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Condition</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Action</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Priority</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {rules.map((r) => (
                <tr key={r.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                  <td className="p-4 text-[var(--text-primary)]">{r.name}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{r.condition}</td>
                  <td className="p-4">
                    <span className={`px-2 py-1 text-xs rounded-full ${
                      r.action === "block" ? "bg-[var(--danger)]/10 text-[var(--danger)]" :
                      r.action === "throttle" ? "bg-[var(--warning)]/10 text-[var(--warning)]" :
                      "bg-[var(--success)]/10 text-[var(--success)]"
                    }`}>{r.action}</span>
                  </td>
                  <td className="p-4 text-[var(--text-secondary)]">{r.priority}</td>
                  <td className="p-4">
                    <span className={`px-2 py-1 text-xs rounded-full ${r.enabled ? "bg-[var(--success)]/10 text-[var(--success)]" : "bg-[var(--text-secondary)]/10 text-[var(--text-secondary)]"}`}>
                      {r.enabled ? "enabled" : "disabled"}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Recent Activity</h3>
      {logsQuery.isLoading ? (
        <p className="text-[var(--text-secondary)]">Loading logs...</p>
      ) : logsQuery.error ? (
        <p className="text-[var(--danger)]">Failed to load logs: {(logsQuery.error as Error).message}</p>
      ) : logs.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)]">
          No firewall activity recorded yet.
        </div>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)]">
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">IP</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Domain</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Sender</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Recipient</th>
                <th className="text-left p-4 text-[var(--text-secondary)] font-medium">Time</th>
              </tr>
            </thead>
            <tbody>
              {logs.map((l) => (
                <tr key={l.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)]">
                  <td className="p-4 text-[var(--text-primary)]">{l.ip}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{l.domain}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{l.sender || "-"}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{l.recipient || "-"}</td>
                  <td className="p-4 text-[var(--text-secondary)]">{new Date(l.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
