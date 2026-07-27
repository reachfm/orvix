import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AtSign, Plus, Trash2 } from "lucide-react";
import { api } from "../api";

export default function AliasesPage() {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [alias, setAlias] = useState("");
  const [target, setTarget] = useState("");

  const { data: aliases } = useQuery({ queryKey: ["aliases"], queryFn: api.listAliases });

  const create = useMutation({
    mutationFn: () => api.createAlias({ alias, target_email: target }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["aliases"] }); setShowForm(false); setAlias(""); setTarget(""); },
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteAlias(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["aliases"] }),
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Email Aliases</h2>
        <button onClick={() => setShowForm(true)} className="flex items-center gap-1 bg-[var(--accent-blue)] text-[var(--text-primary)] rounded px-4 py-2 text-sm">
          <Plus className="w-4 h-4" /> Add Alias
        </button>
      </div>

      {showForm && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
          <h3 className="text-lg font-medium text-[var(--text-primary)] mb-4">New Alias</h3>
          <div className="space-y-3">
            <input value={alias} onChange={(e) => setAlias(e.target.value)} placeholder="sales@example.com"
              className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
            <input value={target} onChange={(e) => setTarget(e.target.value)} placeholder="Forwards to: user@example.com"
              className="w-full px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
            <div className="flex gap-2">
              <button onClick={() => create.mutate()} disabled={create.isPending || !alias || !target}
                className="bg-[var(--accent-blue)] text-[var(--text-primary)] rounded px-4 py-2 text-sm disabled:opacity-50">
                {create.isPending ? "Creating..." : "Create"}
              </button>
              <button onClick={() => setShowForm(false)} className="text-[var(--text-secondary)] px-4 py-2 text-sm">Cancel</button>
            </div>
          </div>
        </div>
      )}

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg divide-y divide-[var(--border)]">
        {aliases && aliases.length > 0 ? aliases.map((a: any) => (
          <div key={a.id} className="flex items-center justify-between p-4 hover:bg-[var(--bg-shell)]">
            <div className="flex items-center gap-3">
              <AtSign className="w-4 h-4 text-[var(--text-secondary)]" />
              <span className="text-[var(--text-primary)] text-sm">{a.alias}</span>
              <span className="text-[var(--text-muted)] text-sm">â†’ {a.target_email}</span>
            </div>
            <button onClick={() => remove.mutate(a.id)} className="text-[var(--text-secondary)] hover:text-red-400">
              <Trash2 className="w-4 h-4" />
            </button>
          </div>
        )) : (
          <div className="p-6 text-center text-[var(--text-secondary)] text-sm">No aliases configured</div>
        )}
      </div>
    </div>
  );
}
