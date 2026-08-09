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
      <div className="w-full max-w-md h-full bg-[#13161C] border-l border-[#2A2F3E] p-6 overflow-y-auto" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-[#E8EAF0]">Organization detail</h3>
          <button onClick={onClose} className="text-[#8B92A8] hover:text-[#E8EAF0]">×</button>
        </div>
        {detailQ.isLoading ? <p className="text-[#8B92A8] text-sm">Loading…</p> :
          detailQ.error ? <p className="text-[#F87171] text-sm">Failed to load: {(detailQ.error as Error).message}</p> :
          detailQ.data ? (
            <>
              <dl className="space-y-2 text-sm mb-6">
                {Object.entries(detailQ.data).filter(([k]) => !/password|secret|token|hash/i.test(k)).map(([k, v]) => (
                  <div key={k} className="flex justify-between gap-4">
                    <dt className="text-[#8B92A8] font-mono">{k}</dt>
                    <dd className="text-[#E8EAF0] text-right break-all">{typeof v === "object" ? JSON.stringify(v) : String(v)}</dd>
                  </div>
                ))}
              </dl>
              <button
                onClick={() => setConfirmToggle({ active: !detailQ.data.active })}
                className={`px-3 py-2 text-sm rounded ${detailQ.data.active ? "bg-[#F87171] text-black" : "bg-[#34D399] text-black"}`}
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

  if (loading) return <div className="text-[#8B92A8]">Loading organizations...</div>;

  return (
    <div>
      <h2 className="text-xl font-semibold text-[#E8EAF0] mb-4">Organizations</h2>
      {orgs.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center text-[#8B92A8]">
          No organizations found.
        </div>
      ) : (
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[#2A2F3E] text-[#8B92A8] text-left">
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
              <tr key={o.id} className="border-b border-[#1A1E26] hover:bg-[#1A1E26] cursor-pointer" onClick={() => setSelected(o.id)}>
                <td className="py-2 px-3 text-[#E8EAF0]">{o.name}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{o.domain}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{o.plan}</td>
                <td className="py-2 px-3">
                  <span className={`px-2 py-0.5 rounded text-xs ${
                    o.active ? "bg-green-900 text-green-300" : "bg-red-900 text-red-300"
                  }`}>{o.active ? "active" : "disabled"}</span>
                </td>
                <td className="py-2 px-3 text-[#8B92A8]">{o.mailbox_count}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{o.domain_count}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{new Date(o.created_at).toLocaleDateString()}</td>
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
