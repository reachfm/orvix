import { useState } from "react";
import TenantContextBanner from "../tenant-context/components/TenantContextBanner";
import { useMailboxesForTenant } from "./queries";
import { useTenantContext, useActiveGrantForSelectedTenant } from "../tenant-context/queries";
import { MAILBOX_STATUSES, type PlatformMailbox } from "./contract";

function MailboxStatusBadge({ status }: { status: string }) {
  const tone =
    status === "active"
      ? "text-[var(--success)] bg-[var(--success)]/10"
      : status === "suspended"
        ? "text-[var(--warning)] bg-[var(--warning)]/10"
        : status === "deleted"
          ? "text-[var(--text-muted)] bg-[var(--bg-subtle)]"
          : "text-[var(--danger)] bg-[var(--danger)]/10";
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs ${tone}`}>
      <span aria-hidden="true">●</span>
      {status}
    </span>
  );
}

function MailboxTable({ mailboxes }: { mailboxes: PlatformMailbox[] }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
      <div className="max-h-[36rem] overflow-auto">
        <table className="w-full text-sm">
          <thead className="sticky top-0 bg-[var(--bg-surface)]">
            <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
              <th className="p-3">Email</th>
              <th className="p-3">Domain</th>
              <th className="p-3">Status</th>
              <th className="p-3">Admin</th>
              <th className="p-3">Created</th>
            </tr>
          </thead>
          <tbody>
            {mailboxes.map((m) => (
              <tr key={m.id} className="border-b border-[var(--bg-subtle)]">
                <td className="p-3 text-[var(--text-primary)]">{m.email}</td>
                <td className="p-3 text-[var(--text-secondary)]">{m.domain || "—"}</td>
                <td className="p-3"><MailboxStatusBadge status={m.status} /></td>
                <td className="p-3 text-[var(--text-secondary)]">{m.is_admin ? "yes" : "no"}</td>
                <td className="p-3 text-[var(--text-secondary)]">{m.created_at ? new Date(m.created_at).toLocaleString() : "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export default function MailboxesPage() {
  const { data: context } = useTenantContext();
  const grant = useActiveGrantForSelectedTenant();
  const { data: mailboxes, isLoading, isError, error } = useMailboxesForTenant();
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState("");

  const tenantId = context?.tenantId ?? null;

  const filtered = (mailboxes ?? []).filter((m) => {
    const q = query.trim().toLowerCase();
    if (q && !m.email.toLowerCase().includes(q)) return false;
    if (statusFilter && m.status !== statusFilter) return false;
    return true;
  });

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Platform Mailboxes</h2>
        <p className="text-sm text-[var(--text-secondary)]">
          Read-only platform view of a tenant's mailboxes through an active support-access grant. Detail and mutations are tenant-admin-only.
        </p>
      </div>

      <TenantContextBanner />

      {!tenantId ? (
        <div className="border border-[var(--border)] rounded-lg p-6 bg-[var(--bg-surface)]">
          <p className="text-sm font-medium text-[var(--text-primary)]">Tenant context required</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Select a tenant above to view its mailboxes. The backend only serves tenant mailbox inventory through an
            active support-access grant.
          </p>
        </div>
      ) : !grant ? (
        <div className="border border-[var(--warning)] rounded-lg p-6 bg-[var(--warning)]/5">
          <p className="text-sm font-medium text-[var(--warning)]">No active support-access grant</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">
            Tenant {tenantId} has no active grant for your account. Requests would be denied by the backend. Create and
            activate a grant on the Support Access page first.
          </p>
        </div>
      ) : isLoading ? (
        <p className="text-sm text-[var(--text-muted)]">Loading mailboxes…</p>
      ) : isError ? (
        <div className="border border-[var(--danger)] rounded-lg p-6 bg-[var(--danger)]/5">
          <p className="text-sm font-medium text-[var(--danger)]">Failed to load mailboxes</p>
          <p className="text-sm text-[var(--text-secondary)] mt-1">{error instanceof Error ? error.message : "Unknown error"}</p>
        </div>
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-3 text-sm">
            <label className="text-[var(--text-secondary)]">
              Search
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="email address"
                className="ml-2 px-3 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded"
              />
            </label>
            <label className="text-[var(--text-secondary)]">
              Status
              <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} className="ml-2 px-3 py-1.5 bg-[var(--bg-surface)] border border-[var(--border)] rounded">
                <option value="">All</option>
                {MAILBOX_STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
              </select>
            </label>
          </div>

          {!mailboxes || mailboxes.length === 0 ? (
            <p className="text-sm text-[var(--text-muted)]">No mailboxes returned for tenant {tenantId}.</p>
          ) : filtered.length === 0 ? (
            <p className="text-sm text-[var(--text-muted)]">No mailboxes match the current filters.</p>
          ) : (
            <MailboxTable mailboxes={filtered} />
          )}
        </>
      )}

      <div className="border border-[var(--border)] rounded-lg p-4 bg-[var(--bg-surface)]">
        <p className="text-xs font-medium text-[var(--text-secondary)]">Tenant-only operations (not available to Platform Super Admin)</p>
        <p className="text-sm text-[var(--text-muted)] mt-1">
          Mailbox detail, quota, password reset, status transitions, soft-delete/restore/purge, and bulk operations are
          served by tenant-family-role routes. Use the Tenant Admin console for these operations.
        </p>
      </div>
    </div>
  );
}
