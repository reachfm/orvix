import StatusBadge from "../../components/StatusBadge";
import type { PlatformDomain } from "../contract";
import { domainStatusLabel, domainStatusTone, mailAccessModeLabel } from "../formatters";

export default function DomainTable({ domains, onSelect }: { domains: PlatformDomain[]; onSelect: (d: PlatformDomain) => void }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-sm" aria-label="Platform domains">
          <thead>
            <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
              <th className="p-3">Domain</th>
              <th className="p-3">Status</th>
              <th className="p-3">Mailboxes</th>
              <th className="p-3">Aliases</th>
              <th className="p-3">DKIM</th>
              <th className="p-3">Mail access</th>
              <th className="p-3">Updated</th>
            </tr>
          </thead>
          <tbody>
            {domains.map((d) => (
              <tr key={d.id} className="border-b border-[var(--bg-subtle)] hover:bg-[var(--bg-elevated)] cursor-pointer" onClick={() => onSelect(d)}>
                <td className="p-3 text-[var(--text-primary)] font-medium">{d.name}</td>
                <td className="p-3">
                  <StatusBadge tone={domainStatusTone(d.status)} label={`Status ${domainStatusLabel(d.status)}`}>
                    {domainStatusLabel(d.status)}
                  </StatusBadge>
                </td>
                <td className="p-3 text-[var(--text-secondary)]">{d.mailbox_count}</td>
                <td className="p-3 text-[var(--text-secondary)]">{d.alias_count}</td>
                <td className="p-3 text-[var(--text-secondary)]">
                  {d.dkim_enabled ? `enabled${d.dkim_selector ? ` (${d.dkim_selector})` : ""}` : "not enabled"}
                </td>
                <td className="p-3 text-[var(--text-secondary)]">{mailAccessModeLabel(d.mail_access_mode)}</td>
                <td className="p-3 text-[var(--text-secondary)]">{new Date(d.updated_at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
