import StatusBadge from "../../components/StatusBadge";
import type { PlatformMailbox } from "../contract";
import { formatBytes, mailboxStatusLabel, mailboxStatusTone, usagePercent } from "../formatters";

export default function MailboxTable({ mailboxes, onSelect }: { mailboxes: PlatformMailbox[]; onSelect: (m: PlatformMailbox) => void }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-sm" aria-label="Platform mailboxes">
          <thead>
            <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
              <th className="p-3">Email</th>
              <th className="p-3">Name</th>
              <th className="p-3">Domain</th>
              <th className="p-3">Status</th>
              <th className="p-3">Quota</th>
              <th className="p-3">Used</th>
              <th className="p-3">Updated</th>
            </tr>
          </thead>
          <tbody>
            {mailboxes.map((m) => (
              <tr key={m.id} className="border-b border-[var(--bg-subtle)] hover:bg-[var(--bg-elevated)] cursor-pointer" onClick={() => onSelect(m)}>
                <td className="p-3 text-[var(--text-primary)] font-medium">{m.email}</td>
                <td className="p-3 text-[var(--text-secondary)]">{m.name || "—"}</td>
                <td className="p-3 text-[var(--text-secondary)]">{m.domain || "—"}</td>
                <td className="p-3">
                  <StatusBadge tone={mailboxStatusTone(m.status)} label={`Status ${mailboxStatusLabel(m.status)}`}>
                    {mailboxStatusLabel(m.status)}
                  </StatusBadge>
                </td>
                <td className="p-3 text-[var(--text-secondary)]">{m.quota_mb > 0 ? `${m.quota_mb} MB` : "unlimited"}</td>
                <td className="p-3 text-[var(--text-secondary)]">
                  {formatBytes(m.used_bytes)}
                  {usagePercent(m.used_bytes, m.quota_mb) !== null && (
                    <span className="ml-1 text-[var(--text-muted)]">({usagePercent(m.used_bytes, m.quota_mb)}%)</span>
                  )}
                </td>
                <td className="p-3 text-[var(--text-secondary)]">{new Date(m.updated_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
