import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { CheckCircle, XCircle, AlertTriangle, Plus, Trash2, RefreshCw, Copy } from "lucide-react";
import { api, ApiError, domainErrorMessage } from "../api";

interface EnterpriseDomain {
  id: number;
  name: string;
  status: string;
  plan?: string;
  mailbox_count?: number;
  dkim_enabled?: boolean;
  dkim_selector?: string;
}

interface ConfirmDelete {
  domain: EnterpriseDomain;
}

function StatusBadge({ status }: { status: string }) {
  const isActive = status === "active";
  const isSuspended = status === "suspended";
  return (
    <span className={`inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full ${
      isActive ? "bg-[#34D399]/10 text-[#34D399]" :
      isSuspended ? "bg-[#F87171]/10 text-[#F87171]" :
      "bg-[#FBBF24]/10 text-[#FBBF24]"
    }`}>
      {isActive ? <CheckCircle size={10} /> : isSuspended ? <XCircle size={10} /> : <AlertTriangle size={10} />}
      {status || "unknown"}
    </span>
  );
}

/** Resolve a typed error code (or fall back to the server message) for display. */
function errorMessage(err: unknown, fallback: string): string {
  if (err instanceof ApiError) {
    return domainErrorMessage(err.code, err.message);
  }
  return (err as Error)?.message || fallback;
}

export default function Domains() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [newDomain, setNewDomain] = useState("");
  const [confirm, setConfirm] = useState<ConfirmDelete | null>(null);
  const [dkimBusy, setDkimBusy] = useState<number | null>(null);
  const [dkimMsg, setDkimMsg] = useState<{ id: number; text: string } | null>(null);

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: ["enterprise-domains"],
    queryFn: () => api.listDomainsEnterprise(),
  });

  const createMutation = useMutation({
    mutationFn: (name: string) => api.createDomainEnterprise({ name }),
    onSuccess: () => {
      setShowCreate(false);
      setNewDomain("");
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteDomainEnterprise(id),
    onSuccess: () => {
      setConfirm(null);
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const generateDKIM = useMutation({
    mutationFn: (d: EnterpriseDomain) => api.generateDomainDKIM(d.id, d.dkim_selector || "mail"),
    onSuccess: (_data, d) => {
      setDkimMsg({ id: d.id, text: "DKIM key generated. Publish the DNS TXT record below." });
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
    onError: (err, d) => setDkimMsg({ id: d.id, text: errorMessage(err, "DKIM generation failed.") }),
  });

  if (isLoading) return <p className="text-[#8B92A8]">Loading...</p>;
  if (error) {
    return (
      <div className="flex flex-col gap-3">
        <p className="text-[#F87171]">Failed to load domains: {errorMessage(error, "Failed to load domains.")}</p>
        <button onClick={() => refetch()} className="text-sm text-[#4F7CFF] hover:underline text-left">Retry</button>
      </div>
    );
  }

  const raw = (data as any)?.domains ?? (Array.isArray(data) ? data : []);
  const items: EnterpriseDomain[] = Array.isArray(raw) ? raw : [];
  const filtered = items.filter(
    (d) => !search || (d.name || "").toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div>
      {confirm && (
        <div
          className="fixed inset-0 bg-black/60 flex items-center justify-center z-50"
          role="dialog"
          aria-modal="true"
          aria-labelledby="delete-domain-title"
        >
          <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6 w-96 max-w-full">
            <h3 id="delete-domain-title" className="text-lg font-semibold text-[#E8EAF0] mb-2">
              Delete Domain
            </h3>
            <p className="text-sm text-[#8B92A8] mb-6">
              Permanently delete <span className="text-[#E8EAF0] font-mono">{confirm.domain.name}</span>?{" "}
              {(confirm.domain.mailbox_count ?? 0) > 0
                ? "This domain still has mailboxes — delete them first, then remove the domain."
                : "This cannot be undone."}
            </p>
            {deleteMutation.isError && (
              <p className="text-[#F87171] text-xs mb-3" role="alert">
                {errorMessage(deleteMutation.error, "Deletion failed.")}
              </p>
            )}
            <div className="flex gap-3 justify-end">
              <button onClick={() => setConfirm(null)}
                className="px-4 py-2 text-sm text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E]">
                Cancel
              </button>
              <button
                onClick={() => deleteMutation.mutate(confirm.domain.id)}
                disabled={deleteMutation.isPending}
                className="px-4 py-2 text-sm rounded-lg bg-[#F87171] text-white hover:bg-red-500 disabled:opacity-50">
                {deleteMutation.isPending ? "Deleting..." : "Delete Domain"}
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="flex justify-between items-center mb-6">
        <h2 className="text-2xl font-semibold text-[#E8EAF0]">Domain Management</h2>
        <button onClick={() => setShowCreate((v) => !v)}
          className="inline-flex items-center gap-2 px-4 py-2 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9]">
          <Plus size={16} /> Add Domain
        </button>
      </div>

      {showCreate && (
        <form
          onSubmit={(e) => { e.preventDefault(); if (newDomain.trim()) createMutation.mutate(newDomain.trim()); }}
          className="flex gap-2 mb-4 bg-[#13161C] border border-[#2A2F3E] rounded-lg p-3"
        >
          <input
            type="text"
            required
            placeholder="example.com"
            value={newDomain}
            onChange={(e) => setNewDomain(e.target.value)}
            aria-label="New domain name"
            className="flex-1 px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-sm font-mono"
          />
          <button type="submit" disabled={createMutation.isPending}
            className="px-4 py-2 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9] disabled:opacity-50">
            {createMutation.isPending ? "Adding..." : "Add"}
          </button>
        </form>
      )}
      {createMutation.isError && (
        <p className="text-[#F87171] text-sm mb-4" role="alert">
          {errorMessage(createMutation.error, "Failed to add domain.")}
        </p>
      )}

      <input type="text" placeholder="Search domains..." value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-sm mb-4" />

      {filtered.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center">
          <p className="text-[#8B92A8]">{items.length === 0 ? "No domains configured yet." : "No domains match your search."}</p>
        </div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-4 text-[#8B92A8] font-medium">Domain</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Plan</th>
                <th className="text-right p-4 text-[#8B92A8] font-medium">Mailboxes</th>
                <th className="text-right p-4 text-[#8B92A8] font-medium">DKIM</th>
                <th className="text-right p-4 text-[#8B92A8] font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((d) => (
                <tr key={d.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-4 text-[#E8EAF0] font-medium font-mono text-xs">{d.name}</td>
                  <td className="p-4"><StatusBadge status={d.status} /></td>
                  <td className="p-4 text-[#8B92A8] text-xs">{d.plan || "—"}</td>
                  <td className="p-4 text-right text-[#8B92A8] text-xs">{d.mailbox_count ?? "—"}</td>
                  <td className="p-4 text-right text-[#8B92A8] text-xs">
                    {d.dkim_enabled ? (
                      <button
                        onClick={() => generateDKIM.mutate(d)}
                        disabled={dkimBusy === d.id || generateDKIM.isPending}
                        className="text-[#34D399] hover:underline disabled:opacity-50 inline-flex items-center gap-1">
                        <RefreshCw size={12} className={generateDKIM.isPending ? "animate-spin" : ""} /> Rotate
                      </button>
                    ) : (
                      <button
                        onClick={() => generateDKIM.mutate(d)}
                        disabled={generateDKIM.isPending}
                        className="text-[#4F7CFF] hover:underline disabled:opacity-50">
                        Generate
                      </button>
                    )}
                  </td>
                  <td className="p-4 text-right">
                    <button
                      onClick={() => setConfirm({ domain: d })}
                      className="text-xs text-[#F87171] hover:underline inline-flex items-center gap-1">
                      <Trash2 size={12} /> Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {dkimMsg && (
            <div className="p-4 border-t border-[#2A2F3E] flex items-start gap-2 text-xs text-[#8B92A8]">
              <Copy size={12} className="mt-0.5" />
              <span>{dkimMsg.text}</span>
              <button onClick={() => setDkimMsg(null)} className="ml-auto text-[#8B92A8] hover:text-[#E8EAF0]" aria-label="Dismiss">×</button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
