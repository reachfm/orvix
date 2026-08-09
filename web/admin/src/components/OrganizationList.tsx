import { useState, useEffect } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import ConfirmDialog from "./ConfirmDialog";

interface Organization {
  id: number;
  name: string;
  slug: string;
  domain: string;
  plan: string;
  active: boolean;
  mailbox_count: number;
  domain_count: number;
  created_at: string;
}

function DetailDrawer({ id, onClose }: { id: number; onClose: () => void }) {
  const qc = useQueryClient();
  const [confirmToggle, setConfirmToggle] = useState<{ active: boolean } | null>(null);
  const detailQ = useQuery<any>({ queryKey: ["org-detail", id], queryFn: () => api.getPlatformOrganizationDetail(id) });
  const toggleMut = useMutation({
    mutationFn: (active: boolean) => api.setPlatformOrganizationActive(id, active),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["org-detail", id] });
      qc.invalidateQueries({ queryKey: ["platform-organizations"] });
      setConfirmToggle(null);
    },
  });

  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/50" onClick={onClose}>
      <div className="w-full max-w-md h-full bg-[var(--bg-surface)] border-l border-[var(--border)] p-6 overflow-y-auto" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-[var(--text-primary)]">Organization detail</h3>
          <button onClick={onClose} className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">×</button>
        </div>
        {detailQ.isLoading ? <p className="text-[var(--text-secondary)] text-sm">Loading…</p> :
          detailQ.error ? <p className="text-[var(--danger)] text-sm">Failed to load: {(detailQ.error as Error).message}</p> :
          detailQ.data ? (
            <>
              <dl className="space-y-2 text-sm mb-6">
                {Object.entries(detailQ.data).filter(([k]) => !/password|secret|token|hash/i.test(k)).map(([k, v]) => (
                  <div key={k} className="flex justify-between gap-4">
                    <dt className="text-[var(--text-secondary)] font-mono">{k}</dt>
                    <dd className="text-[var(--text-primary)] text-right break-all">{typeof v === "object" ? JSON.stringify(v) : String(v)}</dd>
                  </div>
                ))}
              </dl>
              <button
                onClick={() => setConfirmToggle({ active: !detailQ.data.active })}
                className={`px-3 py-2 text-sm rounded ${detailQ.data.active ? "bg-[var(--danger)] text-black" : "bg-[var(--success)] text-black"}`}
              >
                {detailQ.data.active ? "Suspend organization" : "Activate organization"}
              </button>
            </>
          ) : null}
        <ConfirmDialog
          open={!!confirmToggle}
          onOpenChange={(o) => !o && setConfirmToggle(null)}
          title={confirmToggle?.active ? "Activate organization" : "Suspend organization"}
          description={`This will ${confirmToggle?.active ? "restore" : "suspend"} access for tenant ${id}.`}
          requireTypedName={detailQ.data?.slug || detailQ.data?.name}
          danger={!confirmToggle?.active}
          pending={toggleMut.isPending}
          onConfirm={() => confirmToggle && toggleMut.mutate(confirmToggle.active)}
        />
      </div>
    </div>
  );
}

export default function OrganizationList() {
  const [orgs, setOrgs] = useState<Organization[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<number | null>(null);

  useEffect(() => {
    setLoading(true);
    fetch("/api/v1/platform/organizations?limit=100")
      .then((r) => r.json())
      .then((data) => {
        setOrgs(data.organizations || []);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  if (loading) return <div className="text-[var(--text-secondary)]">Loading organizations...</div>;

  return (
    <div>
      <h2 className="text-xl font-semibold text-[var(--text-primary)] mb-4">Organizations</h2>
      {orgs.length === 0 ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)]">
          No organizations found.
        </div>
      ) : (
      <div className="overflow-x-auto">
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
            {orgs.map((o) => (
              <tr key={o.id} className="border-b border-[var(--bg-elevated)] hover:bg-[var(--bg-elevated)] cursor-pointer" onClick={() => setSelected(o.id)}>
                <td className="py-2 px-3 text-[var(--text-primary)]">{o.name}</td>
                <td className="py-2 px-3 text-[var(--text-secondary)]">{o.domain}</td>
                <td className="py-2 px-3 text-[var(--text-secondary)]">{o.plan}</td>
                <td className="py-2 px-3">
                  <span className={`px-2 py-0.5 rounded text-xs ${
                    o.active ? "bg-[var(--success)]/20 text-[var(--success)]" : "bg-[var(--danger)]/20 text-[var(--danger)]"
                  }`}>{o.active ? "active" : "disabled"}</span>
                </td>
                <td className="py-2 px-3 text-[var(--text-secondary)]">{o.mailbox_count}</td>
                <td className="py-2 px-3 text-[var(--text-secondary)]">{o.domain_count}</td>
                <td className="py-2 px-3 text-[var(--text-secondary)]">{new Date(o.created_at).toLocaleDateString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      )}
      {selected !== null && <DetailDrawer id={selected} onClose={() => setSelected(null)} />}
    </div>
  );
}
