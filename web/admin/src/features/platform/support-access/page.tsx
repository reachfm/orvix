import { useState } from "react";
import { useGrants, useCreateGrant, useActivateGrant, useRevokeGrant } from "./queries";
import { SUPPORT_SCOPES, type AccessGrant } from "./contract";

function GrantStatusBadge({ status }: { status: string }) {
  const tone =
    status === "active"
      ? "text-[var(--success)] bg-[var(--success)]/10"
      : status === "revoked" || status === "expired"
        ? "text-[var(--danger)] bg-[var(--danger)]/10"
        : status === "approved"
          ? "text-[var(--warning)] bg-[var(--warning)]/10"
          : "text-[var(--text-secondary)] bg-[var(--bg-subtle)]";
  return <span className={`px-2 py-0.5 rounded text-xs ${tone}`}>{status}</span>;
}

function GrantRow({ grant }: { grant: AccessGrant }) {
  const activate = useActivateGrant(grant.id);
  const revoke = useRevokeGrant(grant.id);
  const [revokeReason, setRevokeReason] = useState("");
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  return (
    <tr className="border-b border-[var(--bg-subtle)]">
      <td className="p-3 text-[var(--text-primary)]">#{grant.id}</td>
      <td className="p-3 text-[var(--text-primary)]">{grant.target_tenant_name || `tenant:${grant.target_tenant_id}`}</td>
      <td className="p-3 text-[var(--text-secondary)]">{grant.permission_scope}</td>
      <td className="p-3"><GrantStatusBadge status={grant.status} /></td>
      <td className="p-3 text-[var(--text-secondary)]">{grant.reason}</td>
      <td className="p-3 text-[var(--text-secondary)]">{new Date(grant.expires_at).toLocaleString()}</td>
      <td className="p-3">
        <div className="flex flex-wrap items-center gap-2">
          {grant.status === "approved" && (
            <button onClick={() => { setFeedback(null); activate.mutate(undefined, { onSuccess: () => setFeedback({ kind: "ok", message: "Grant activated." }), onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Activation failed." }) }); }} disabled={activate.isPending} className="px-2 py-1 rounded text-xs bg-[var(--accent)] text-white disabled:opacity-50">Activate</button>
          )}
          {(grant.status === "approved" || grant.status === "active") && (
            <>
              <input value={revokeReason} onChange={(e) => setRevokeReason(e.target.value)} placeholder="revoke reason" className="px-2 py-1 text-xs bg-[var(--bg-base)] border border-[var(--border)] rounded" />
              <button onClick={() => { setFeedback(null); revoke.mutate(revokeReason.trim(), { onSuccess: () => setFeedback({ kind: "ok", message: "Grant revoked." }), onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Revoke failed." }) }); }} disabled={revoke.isPending} className="px-2 py-1 rounded text-xs bg-[var(--danger)]/10 text-[var(--danger)] disabled:opacity-50">Revoke</button>
            </>
          )}
        </div>
        {feedback && <span className={`block mt-1 text-xs ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</span>}
      </td>
    </tr>
  );
}

export default function SupportAccessPage() {
  const { data, isLoading } = useGrants();
  const create = useCreateGrant();
  const [ticketRef, setTicketRef] = useState("");
  const [reason, setReason] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [scope, setScope] = useState(SUPPORT_SCOPES[0]);
  const [expiresAt, setExpiresAt] = useState("");
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);

  const submit = () => {
    const tid = Number.parseInt(tenantId, 10);
    if (!Number.isFinite(tid) || tid <= 0) { setFeedback({ kind: "error", message: "A valid target tenant ID is required." }); return; }
    if (reason.trim() === "") { setFeedback({ kind: "error", message: "A reason is required." }); return; }
    if (!expiresAt) { setFeedback({ kind: "error", message: "An expiry is required." }); return; }
    setFeedback(null);
    create.mutate(
      { ticket_ref: ticketRef.trim(), reason: reason.trim(), target_tenant_id: tid, permission_scope: scope, expires_at: new Date(expiresAt).toISOString() },
      {
        onSuccess: () => { setTicketRef(""); setReason(""); setTenantId(""); setFeedback({ kind: "ok", message: "Grant created." }); },
        onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Create failed." }),
      },
    );
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Support Access</h2>
        <p className="text-sm text-[var(--text-secondary)]">Temporary, scoped, audited support-access grants.</p>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 space-y-3">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Request a grant</h3>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
          <input value={ticketRef} onChange={(e) => setTicketRef(e.target.value)} placeholder="Ticket reference" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <input value={tenantId} onChange={(e) => setTenantId(e.target.value)} placeholder="Target tenant ID" type="number" min={1} className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Reason (required)" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <select value={scope} onChange={(e) => setScope(e.target.value)} className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm">
            {SUPPORT_SCOPES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
          <input value={expiresAt} onChange={(e) => setExpiresAt(e.target.value)} type="datetime-local" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
        </div>
        <button onClick={submit} disabled={create.isPending} className="px-4 py-2 bg-[var(--accent)] text-white rounded text-sm disabled:opacity-50">
          {create.isPending ? "Creating…" : "Create Grant"}
        </button>
        {feedback && <p className={`text-sm ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</p>}
      </div>

      {isLoading ? (
        <p className="text-sm text-[var(--text-muted)]">Loading…</p>
      ) : !data || data.grants.length === 0 ? (
        <p className="text-sm text-[var(--text-muted)]">No support-access grants found.</p>
      ) : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">ID</th><th className="p-3">Tenant</th><th className="p-3">Scope</th><th className="p-3">Status</th><th className="p-3">Reason</th><th className="p-3">Expires</th><th className="p-3">Actions</th>
              </tr>
            </thead>
            <tbody>
              {data.grants.map((g) => <GrantRow key={g.id} grant={g} />)}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
