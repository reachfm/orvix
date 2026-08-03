import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { CheckCircle, XCircle, AlertTriangle, Plus, Trash2, RefreshCw } from "lucide-react";
import { api, domainErrorMessage } from "../api";

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

interface ConfirmRotate {
  domain: EnterpriseDomain;
}

interface DKIMResult {
  selector: string;
  public_dns_txt: string;
  dns_record_name: string;
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

function errorMessage(err: unknown, fallback: string): string {
  if (err && typeof err === "object" && "code" in (err as any) && typeof (err as any).code === "string") {
    return domainErrorMessage((err as any).code, (err as any).message || fallback);
  }
  return (err as Error)?.message || fallback;
}

function DKIMInstructions({ result }: { result: DKIMResult }) {
  return (
    <div className="bg-[#0C0E12] border border-[#2A2F3E] rounded-lg p-3 mt-2 text-xs font-mono text-[#8B92A8] space-y-2">
      <p className="text-[#34D399] text-sm font-medium">DKIM key rotated successfully. Update the DNS TXT record below. DKIM verification will fail until the new record propagates.</p>
      <div className="flex items-start gap-1">
        <span className="text-[#4F7CFF] shrink-0">Name:</span>
        <span className="text-[#E8EAF0] break-all select-all">{result.dns_record_name}</span>
      </div>
      <div className="flex items-start gap-1">
        <span className="text-[#4F7CFF] shrink-0">Value:</span>
        <span className="text-[#E8EAF0] break-all select-all">{result.public_dns_txt}</span>
      </div>
      <div className="flex items-start gap-1">
        <span className="text-[#4F7CFF] shrink-0">Selector:</span>
        <span className="text-[#E8EAF0]">{result.selector}</span>
      </div>
    </div>
  );
}

export default function Domains() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [newDomain, setNewDomain] = useState("");
  const [confirmDelete, setConfirmDelete] = useState<ConfirmDelete | null>(null);
  const [confirmRotate, setConfirmRotate] = useState<ConfirmRotate | null>(null);
  const [dkimR, setDkimR] = useState<Record<number, DKIMResult | null>>({});
  const [dkimErr, setDkimErr] = useState<Record<number, string | null>>({});

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
      setConfirmDelete(null);
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
  });

  const generateDKIM = useMutation({
    mutationFn: (d: EnterpriseDomain) => api.generateDomainDKIM(d.id, d.dkim_selector || "mail"),
    onSuccess: (_data, d) => {
      setDkimErr((p) => ({ ...p, [d.id]: null }));
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
    onError: (err, d) => {
      setDkimErr((p) => ({ ...p, [d.id]: errorMessage(err, "DKIM generation failed.") }));
    },
  });

  const rotateDKIM = useMutation({
    mutationFn: (d: EnterpriseDomain) => api.rotateDomainDKIM(d.id, d.dkim_selector || "mail"),
    onSuccess: (data, d) => {
      setConfirmRotate(null);
      setDkimErr((p) => ({ ...p, [d.id]: null }));
      const result = (data as any)?.dkim as DKIMResult | undefined;
      if (result) {
        setDkimR((p) => ({ ...p, [d.id]: result }));
      }
      queryClient.invalidateQueries({ queryKey: ["enterprise-domains"] });
    },
    onError: (err, d) => {
      setDkimErr((p) => ({ ...p, [d.id]: errorMessage(err, "DKIM rotation failed.") }));
    },
  });

  const dismissDkim = (id: number) => {
    setDkimR((p) => ({ ...p, [id]: null }));
    setDkimErr((p) => ({ ...p, [id]: null }));
  };

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
      {/* Delete confirmation */}
      {confirmDelete && (
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
              Permanently delete <span className="text-[#E8EAF0] font-mono">{confirmDelete.domain.name}</span>?{" "}
              {(confirmDelete.domain.mailbox_count ?? 0) > 0
                ? "This domain still has mailboxes — delete them first, then remove the domain."
                : "This cannot be undone."}
            </p>
            {deleteMutation.isError && (
              <p className="text-[#F87171] text-xs mb-3" role="alert">
                {errorMessage(deleteMutation.error, "Deletion failed.")}
              </p>
            )}
            <div className="flex gap-3 justify-end">
              <button onClick={() => setConfirmDelete(null)}
                className="px-4 py-2 text-sm text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E]">
                Cancel
              </button>
              <button
                onClick={() => { if (!deleteMutation.isPending) deleteMutation.mutate(confirmDelete.domain.id); }}
                disabled={deleteMutation.isPending}
                className="px-4 py-2 text-sm rounded-lg bg-[#F87171] text-white hover:bg-red-500 disabled:opacity-50">
                {deleteMutation.isPending ? "Deleting..." : "Delete Domain"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Rotate confirmation */}
      {confirmRotate && (
        <div
          className="fixed inset-0 bg-black/60 flex items-center justify-center z-50"
          role="dialog"
          aria-modal="true"
          aria-labelledby="rotate-domain-title"
        >
          <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6 w-96 max-w-full">
            <h3 id="rotate-domain-title" className="text-lg font-semibold text-[#E8EAF0] mb-2">
              Rotate DKIM Key
            </h3>
            <p className="text-sm text-[#8B92A8] mb-1">
              Rotate the DKIM key for <span className="text-[#E8EAF0] font-mono">{confirmRotate.domain.name}</span>?
            </p>
            <p className="text-sm text-[#FBBF24] mb-4">
              After rotation, you must update the DNS TXT record with the new key. DKIM verification will fail until the new record propagates.
            </p>
            {rotateDKIM.isPending && (
              <p className="text-[#8B92A8] text-xs mb-3">Generating a new RSA-2048 key pair…</p>
            )}
            {rotateDKIM.isError && (
              <p className="text-[#F87171] text-xs mb-3" role="alert">
                {errorMessage(rotateDKIM.error, "Rotation failed.")}
              </p>
            )}
            <div className="flex gap-3 justify-end">
              <button onClick={() => setConfirmRotate(null)}
                disabled={rotateDKIM.isPending}
                className="px-4 py-2 text-sm text-[#8B92A8] hover:text-[#E8EAF0] rounded-lg border border-[#2A2F3E] disabled:opacity-50">
                Cancel
              </button>
              <button
                onClick={() => { if (!rotateDKIM.isPending) rotateDKIM.mutate(confirmRotate.domain); }}
                disabled={rotateDKIM.isPending}
                className="px-4 py-2 text-sm rounded-lg bg-[#4F7CFF] text-white hover:bg-[#3B5FD9] disabled:opacity-50">
                {rotateDKIM.isPending ? "Rotating…" : "Rotate Key"}
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
                        onClick={() => setConfirmRotate({ domain: d })}
                        disabled={rotateDKIM.isPending}
                        className="text-[#34D399] hover:underline disabled:opacity-50 inline-flex items-center gap-1">
                        <RefreshCw size={12} className={rotateDKIM.isPending ? "animate-spin" : ""} /> Rotate
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
                      onClick={() => setConfirmDelete({ domain: d })}
                      className="text-xs text-[#F87171] hover:underline inline-flex items-center gap-1">
                      <Trash2 size={12} /> Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {/* Show DNS instructions after successful rotation */}
          {Object.entries(dkimR).filter(([, r]) => r != null).map(([idKey, r]) => (
            <div key={`dkim-${idKey}`} className="p-4 border-t border-[#2A2F3E]">
              <DKIMInstructions result={r!} />
              <button onClick={() => dismissDkim(Number(idKey))} className="mt-2 text-xs text-[#8B92A8] hover:text-[#E8EAF0]">Dismiss</button>
            </div>
          ))}
          {/* Show DKIM error messages */}
          {Object.entries(dkimErr).filter(([, e]) => e != null).map(([idKey, e]) => (
            <div key={`dkim-err-${idKey}`} className="p-4 border-t border-[#2A2F3E]">
              <p className="text-[#F87171] text-xs" role="alert">{e}</p>
              <button onClick={() => dismissDkim(Number(idKey))} className="mt-1 text-xs text-[#8B92A8] hover:text-[#E8EAF0]">Dismiss</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
