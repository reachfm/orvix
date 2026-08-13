import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, domainErrorMessage } from "../api";

interface AdminMailbox {
  id: number;
  email: string;
  domain_id?: number;
  status: string;
  is_admin: boolean;
  created_at: string;
}

interface ConfirmState {
  type: "delete" | "status";
  id: number;
  email: string;
  newStatus?: "active" | "disabled";
}

/** Resolve a typed error code (or fall back to the server message) for display. */
function errorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    return domainErrorMessage(err.code, err.message);
  }
  return (err as Error)?.message || fallback;
}

export default function MailboxList() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [newEmail, setNewEmail] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirm, setConfirm] = useState<ConfirmState | null>(null);

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["enterprise-mailboxes"],
    queryFn: () => api.listMailboxes(),
  });

  const createMutation = useMutation({
    mutationFn: () => api.createMailbox({ email: newEmail, password: newPassword }),
    onSuccess: () => {
      setShowCreate(false);
      setNewEmail("");
      setNewPassword("");
      queryClient.invalidateQueries({ queryKey: ["enterprise-mailboxes"] });
    },
  });

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: number; status: "active" | "disabled" }) =>
      api.setMailboxStatus(id, status),
    onSuccess: () => {
      setConfirm(null);
      queryClient.invalidateQueries({ queryKey: ["enterprise-mailboxes"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteMailbox(id),
    onSuccess: () => {
      setConfirm(null);
      queryClient.invalidateQueries({ queryKey: ["enterprise-mailboxes"] });
    },
  });

  if (isLoading) return <div className="text-[var(--text-secondary)]">Loading mailboxes...</div>;
  if (error) {
    return (
      <div className="flex flex-col gap-3">
        <div className="text-[var(--danger)]">Failed to load mailboxes: {errorMessage(error, "Failed to load mailboxes.")}</div>
        <button onClick={() => refetch()} className="text-sm text-[var(--accent)] hover:underline text-left">Retry</button>
      </div>
    );
  }

  const mailboxes: AdminMailbox[] = (data as any)?.mailboxes ?? (Array.isArray(data) ? data : []);
  const filtered = mailboxes.filter((m) =>
    !search || m.email.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div>
      {confirm && (
        <div
          className="fixed inset-0 bg-black/60 flex items-center justify-center z-50"
          role="dialog"
          aria-modal="true"
          aria-labelledby="mailbox-confirm-title"
        >
          <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6 w-96 max-w-full">
            <h3 id="mailbox-confirm-title" className="text-lg font-semibold text-[var(--text-primary)] mb-2">
              {confirm.type === "delete" ? "Delete Mailbox" : "Change Status"}
            </h3>
            <p className="text-sm text-[var(--text-secondary)] mb-6">
              {confirm.type === "delete"
                ? `Permanently delete ${confirm.email}? All email data will be lost. This cannot be undone.`
                : `Set ${confirm.email} to ${confirm.newStatus}?`}
            </p>
            {(deleteMutation.isError || statusMutation.isError) && (
              <p className="text-[var(--danger)] text-xs mb-3" role="alert">
                {errorMessage(
                  deleteMutation.error || statusMutation.error,
                  "Operation failed."
                )}
              </p>
            )}
            <div className="flex gap-3 justify-end">
              <button onClick={() => setConfirm(null)}
                className="px-4 py-2 text-sm text-[var(--text-secondary)] hover:text-[var(--text-primary)] rounded-lg border border-[var(--border)]">
                Cancel
              </button>
              <button
                onClick={() => {
                  if (confirm.type === "delete") {
                    deleteMutation.mutate(confirm.id);
                  } else if (confirm.newStatus) {
                    statusMutation.mutate({ id: confirm.id, status: confirm.newStatus });
                  }
                }}
                disabled={deleteMutation.isPending || statusMutation.isPending}
                className={`px-4 py-2 text-sm rounded-lg disabled:opacity-50 ${
                  confirm.type === "delete"
                    ? "bg-[var(--danger)] text-white hover:bg-[var(--danger)]"
                    : "bg-[var(--accent)] text-white hover:bg-[var(--accent-hover)]"
                }`}>
                {deleteMutation.isPending || statusMutation.isPending ? "Processing..." : "Confirm"}
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="flex justify-between items-center mb-4">
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Mailboxes</h2>
        <button onClick={() => setShowCreate((v) => !v)}
          className="px-4 py-2 bg-[var(--accent)] text-white rounded-lg text-sm hover:bg-[var(--accent-hover)]">
          Create Mailbox
        </button>
      </div>

      {showCreate && (
        <form onSubmit={(e) => { e.preventDefault(); createMutation.mutate(); }}
          className="flex gap-2 mb-4 bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-3">
          <input type="email" required placeholder="email@domain.com" value={newEmail}
            onChange={(e) => setNewEmail(e.target.value)}
            className="flex-1 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          <input type="password" required placeholder="Password" value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            className="flex-1 px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          <button type="submit" disabled={createMutation.isPending}
            className="px-4 py-2 bg-[var(--accent)] text-white rounded-lg text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
            {createMutation.isPending ? "Creating..." : "Create"}
          </button>
        </form>
      )}
      {createMutation.isError && (
        <p className="text-[var(--danger)] text-sm mb-4" role="alert">
          Failed to create mailbox: {errorMessage(createMutation.error, "Failed to create mailbox.")}
        </p>
      )}

      <input type="text" placeholder="Search mailboxes..." value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="w-full px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded-lg text-[var(--text-primary)] text-sm mb-4" />

      {filtered.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)]">
          {mailboxes.length === 0 ? "No mailboxes found." : "No mailboxes match your search."}
        </div>
      ) : (
        <div className="overflow-x-auto bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-[var(--text-secondary)] text-left">
                <th className="py-3 px-4">Email</th>
                <th className="py-3 px-4">Status</th>
                <th className="py-3 px-4">Type</th>
                <th className="py-3 px-4">Created</th>
                <th className="py-3 px-4 text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((m) => (
                <tr key={m.id} className="border-b border-[var(--bg-elevated)] hover:bg-[var(--bg-elevated)]">
                  <td className="py-3 px-4 text-[var(--text-primary)] font-mono text-xs">{m.email}</td>
                  <td className="py-3 px-4">
                    <span className={`px-2 py-0.5 rounded text-xs ${
                      m.status === "active" ? "bg-[var(--success)]/20/50 text-[var(--success)]" : "bg-[var(--warning)]/20 text-[var(--warning)]"
                    }`}>{m.status}</span>
                  </td>
                  <td className="py-3 px-4 text-[var(--text-secondary)] text-xs">
                    {m.is_admin ? <span className="text-[var(--accent)]">admin</span> : "user"}
                  </td>
                  <td className="py-3 px-4 text-[var(--text-secondary)] text-xs">
                    {m.created_at ? new Date(m.created_at).toLocaleDateString() : "—"}
                  </td>
                  <td className="py-3 px-4 text-right">
                    <div className="flex items-center gap-2 justify-end">
                      {!m.is_admin && (
                        <button
                          onClick={() => setConfirm({
                            type: "status", id: m.id, email: m.email,
                            newStatus: m.status === "active" ? "disabled" : "active",
                          })}
                          className={`text-xs hover:underline ${
                            m.status === "active" ? "text-[var(--warning)]" : "text-[var(--success)]"
                          }`}>
                          {m.status === "active" ? "Disable" : "Enable"}
                        </button>
                      )}
                      <button
                        onClick={() => setConfirm({ type: "delete", id: m.id, email: m.email })}
                        className="text-xs text-[var(--danger)] hover:underline">
                        Delete
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
