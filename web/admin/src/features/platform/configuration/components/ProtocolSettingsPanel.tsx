import { useState } from "react";
import { useProtocolSettingsQuery } from "../queries";
import { usePatchProtocolSettingsMutation } from "../mutations";
import { PROTOCOL_IDS } from "../schema";
import type { ProtocolSettingsPatchRequest } from "../contract";
import { Loading, ErrorBox } from "./StateViews";
import ConfirmDialog from "../../../../components/ConfirmDialog";

// GET/PATCH /admin/settings/protocol/:protocol — a separate,
// flat-body settings surface from the sectioned /admin/settings above
// (see contract.ts's note). Invalid types never reach the network:
// the number input's own type coercion and the boolean select's
// closed value set make an invalid value unrepresentable in the form
// state, so "fail before mutation" is enforced structurally rather
// than by a separate validation pass. An empty numeric input is
// tracked as "no value typed" (draft holds undefined), never
// coerced to 0 — Number("") === 0 in JS is exactly the bug this
// guards against.
export default function ProtocolSettingsPanel() {
  const [protocol, setProtocol] = useState<string>(PROTOCOL_IDS[0].id);
  const q = useProtocolSettingsQuery(protocol);
  const patchMut = usePatchProtocolSettingsMutation(protocol);
  const [draft, setDraft] = useState<Record<string, string | number | boolean | undefined>>({});
  const [confirming, setConfirming] = useState(false);

  const setField = (key: string, type: string, raw: string) => {
    if (type === "int") {
      if (raw.trim() === "") {
        // Empty input is a real "no value" state, never 0.
        setDraft((d) => ({ ...d, [key]: undefined }));
        return;
      }
      const n = Number(raw);
      setDraft((d) => ({ ...d, [key]: Number.isFinite(n) ? n : undefined }));
      return;
    }
    const value: string | boolean = type === "bool" ? raw === "true" : raw;
    setDraft((d) => ({ ...d, [key]: value }));
  };

  // Only keys with an actual (non-undefined) typed value are real
  // changes eligible to submit — an empty numeric field never
  // silently becomes part of the patch as 0.
  const changedKeys = Object.entries(draft).filter(([, v]) => v !== undefined).map(([k]) => k);
  const dirtyCount = changedKeys.length;

  const submit = () => {
    const body: ProtocolSettingsPatchRequest = {};
    for (const k of changedKeys) body[k] = draft[k] as string | number | boolean;
    patchMut.mutate(body, { onSuccess: () => { setDraft({}); setConfirming(false); } });
  };

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
              const draftValue = draft[k.key];
              const current = draftValue !== undefined ? draftValue : k.value;
              const isChanged = draftValue !== undefined;
              return (
                <div key={k.key} className="flex items-center justify-between gap-4">
                  <dt className="text-sm text-[var(--text-secondary)]">
                    {k.label}
                    {k.persisted && <span className="ml-2 text-xs text-[var(--warning)]">modified</span>}
                    {k.restart_required && !k.read_only && <span className="ml-2 text-xs text-[var(--text-muted)]">restart required</span>}
                    {k.read_only && <span className="ml-2 text-xs text-[var(--text-muted)]" title={k.read_only}>read-only</span>}
                    {isChanged && <span className="ml-2 text-xs text-[var(--accent)]">→ changed</span>}
                  </dt>
                  <dd>
                    {k.read_only ? (
                      <span className="px-2 py-1 text-xs text-[var(--text-muted)]">{String(k.value)}</span>
                    ) : k.type === "bool" ? (
                      <select value={String(current)} onChange={(e) => setField(k.key, k.type, e.target.value)} className="px-2 py-1 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-xs text-[var(--text-primary)]">
                        <option value="true">true</option>
                        <option value="false">false</option>
                      </select>
                    ) : (
                      <input
                        type={k.type === "int" ? "number" : "text"}
                        value={draftValue !== undefined ? String(draftValue) : String(k.value)}
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
        onClick={() => setConfirming(true)}
        className="mt-4 px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-40"
      >
        {patchMut.isPending ? "Saving…" : `Save changes${dirtyCount ? ` (${dirtyCount})` : ""}`}
      </button>

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={`Apply ${dirtyCount} protocol setting change${dirtyCount === 1 ? "" : "s"}?`}
        description={
          changedKeys
            .map((k) => {
              const def = q.data?.keys.find((kk) => kk.key === k);
              return `${def?.label ?? k}: ${String(def?.value)} → ${String(draft[k])}${def?.restart_required ? " (restart required)" : ""}`;
            })
            .join("; ")
        }
        pending={patchMut.isPending}
        onConfirm={submit}
      />

      {patchMut.isError && <p className="text-[var(--danger)] text-xs mt-2">{(patchMut.error as Error).message}</p>}
      {patchMut.data?.rejected && patchMut.data.rejected.length > 0 && (
        <p className="text-[var(--danger)] text-xs mt-2">Rejected: {patchMut.data.rejected.map((r) => `${r.key} (${r.reason})`).join(", ")}</p>
      )}
      {patchMut.isSuccess && (!patchMut.data.rejected || patchMut.data.rejected.length === 0) && (
        <div className="mt-2 text-xs space-y-1">
          {patchMut.data.hot_applied && patchMut.data.hot_applied.length > 0 && (
            <p className="text-[var(--success)]">Applied immediately: {patchMut.data.hot_applied.join(", ")}</p>
          )}
          {patchMut.data.pending_restart && patchMut.data.pending_restart.length > 0 && (
            <p className="text-[var(--warning)] font-medium">⚠ Restart required to take effect: {patchMut.data.pending_restart.join(", ")}</p>
          )}
          {patchMut.data.bridge_apply_error && (
            <p className="text-[var(--warning)]">{patchMut.data.bridge_apply_error}</p>
          )}
          {(!patchMut.data.hot_applied || patchMut.data.hot_applied.length === 0) && (!patchMut.data.pending_restart || patchMut.data.pending_restart.length === 0) && (
            <p className="text-[var(--text-secondary)]">No changes were needed.</p>
          )}
        </div>
      )}
    </div>
  );
}
