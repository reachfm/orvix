import { useState } from "react";
import { useProtocolSettingsQuery } from "../queries";
import { usePatchProtocolSettingsMutation } from "../mutations";
import { PROTOCOL_IDS } from "../schema";
import type { ProtocolSettingsPatchRequest } from "../contract";
import { Loading, ErrorBox } from "./StateViews";

// GET/PATCH /admin/settings/protocol/:protocol — a separate,
// flat-body settings surface from the sectioned /admin/settings above
// (see contract.ts's note). Invalid types never reach the network:
// the number input's own type coercion and the boolean select's
// closed value set make an invalid value unrepresentable in the form
// state, so "fail before mutation" is enforced structurally rather
// than by a separate validation pass.
export default function ProtocolSettingsPanel() {
  const [protocol, setProtocol] = useState<string>(PROTOCOL_IDS[0].id);
  const q = useProtocolSettingsQuery(protocol);
  const patchMut = usePatchProtocolSettingsMutation(protocol);
  const [draft, setDraft] = useState<ProtocolSettingsPatchRequest>({});

  const setField = (key: string, type: string, raw: string) => {
    const value: string | number | boolean = type === "bool" ? raw === "true" : type === "int" ? Number(raw) : raw;
    setDraft((d) => ({ ...d, [key]: value }));
  };

  const dirtyCount = Object.keys(draft).length;

  return (
    <div>
      <div className="mb-4">
        <label className="block text-xs text-[var(--text-secondary)] mb-1">Protocol</label>
        <select
          value={protocol}
          onChange={(e) => { setProtocol(e.target.value); setDraft({}); }}
          className="px-2 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
        >
          {PROTOCOL_IDS.map((p) => <option key={p.id} value={p.id}>{p.label}</option>)}
        </select>
      </div>

      {q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : q.data ? (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <p className="text-xs text-[var(--text-secondary)] mb-3">{q.data.description}</p>
          <dl className="space-y-2">
            {q.data.keys.map((k) => {
              const current = draft[k.key] ?? k.value;
              return (
                <div key={k.key} className="flex items-center justify-between gap-4">
                  <dt className="text-sm text-[var(--text-secondary)]">
                    {k.label}
                    {k.persisted && <span className="ml-2 text-xs text-[var(--warning)]">modified</span>}
                    {k.restart_required && <span className="ml-2 text-xs text-[var(--text-muted)]">restart required</span>}
                  </dt>
                  <dd>
                    {k.type === "bool" ? (
                      <select value={String(current)} onChange={(e) => setField(k.key, k.type, e.target.value)} className="px-2 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)]">
                        <option value="true">true</option>
                        <option value="false">false</option>
                      </select>
                    ) : (
                      <input
                        type={k.type === "int" ? "number" : "text"}
                        value={String(current)}
                        onChange={(e) => setField(k.key, k.type, e.target.value)}
                        className="px-2 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)] w-48 text-right"
                      />
                    )}
                  </dd>
                </div>
              );
            })}
          </dl>
        </div>
      ) : null}

      <button
        disabled={dirtyCount === 0 || patchMut.isPending}
        onClick={() => patchMut.mutate(draft, { onSuccess: () => setDraft({}) })}
        className="mt-4 px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-40"
      >
        {patchMut.isPending ? "Saving…" : `Save changes${dirtyCount ? ` (${dirtyCount})` : ""}`}
      </button>
      {patchMut.isError && <p className="text-[var(--danger)] text-xs mt-2">{(patchMut.error as Error).message}</p>}
      {patchMut.data?.rejected && patchMut.data.rejected.length > 0 && (
        <p className="text-[var(--danger)] text-xs mt-2">Rejected: {patchMut.data.rejected.map((r) => `${r.key} (${r.reason})`).join(", ")}</p>
      )}
      {patchMut.isSuccess && (!patchMut.data.rejected || patchMut.data.rejected.length === 0) && <p className="text-[var(--success)] text-xs mt-2">Saved.</p>}
    </div>
  );
}
