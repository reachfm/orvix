import type { OrganizationSummary } from "../contract";

export default function OrganizationsTable({
  organizations,
  onSelect,
}: {
  organizations: OrganizationSummary[];
  onSelect: (id: number) => void;
}) {
  return (
    <div className="overflow-x-auto bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-[var(--border)] text-[var(--text-secondary)] text-left">
            <th className="py-2 px-3">Name</th>
            <th className="py-2 px-3">Domain</th>
            <th className="py-2 px-3">Plan</th>
            <th className="py-2 px-3">Status</th>
            <th className="py-2 px-3">Mailboxes</th>
            <th className="py-2 px-3">Domains</th>
            <th className="py-2 px-3">Created</th>
          </tr>
        </thead>
        <tbody>
          {organizations.map((o) => (
            <tr
              key={o.id}
              className="border-b border-[var(--bg-elevated)] hover:bg-[var(--bg-elevated)] cursor-pointer"
              onClick={() => onSelect(o.id)}
            >
              <td className="py-2 px-3 text-[var(--text-primary)]">{o.name}</td>
              <td className="py-2 px-3 text-[var(--text-secondary)]">{o.domain}</td>
              <td className="py-2 px-3 text-[var(--text-secondary)]">{o.plan}</td>
              <td className="py-2 px-3">
                <span className={`px-2 py-0.5 rounded text-xs ${o.active ? "bg-[var(--success)]/20 text-[var(--success)]" : "bg-[var(--danger)]/20 text-[var(--danger)]"}`}>
                  {o.active ? "active" : "suspended"}
                </span>
              </td>
              <td className="py-2 px-3 text-[var(--text-secondary)]">{o.mailbox_count}</td>
              <td className="py-2 px-3 text-[var(--text-secondary)]">{o.domain_count}</td>
              <td className="py-2 px-3 text-[var(--text-secondary)]">{new Date(o.created_at).toLocaleDateString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
