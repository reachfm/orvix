import { useState } from "react";
import { useBulkQueueActionMutation } from "../mutations";
import type { BulkQueueAction, BulkQueueActionResult } from "../contract";

interface Props {
  messages: Array<{ id: number; status: string }>;
}

/**
 * BulkQueueActionPanel applies retry/cancel/bounce to multiple queue
 * messages through the real POST /admin/queue/messages/bulk-action
 * endpoint (platformMW-gated). The backend reports per-row results; the
 * panel surfaces succeeded/failed counts and per-row errors without
 * pretending the operation was atomic.
 */
export default function BulkQueueActionPanel({ messages }: Props) {
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const [action, setAction] = useState<BulkQueueAction>("retry");
  const [reason, setReason] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [result, setResult] = useState<{ succeeded: number; failed: BulkQueueActionResult[] } | null>(null);
  const bulk = useBulkQueueActionMutation();

  const toggle = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };

  const toggleAll = () => {
    setSelected((prev) => (prev.size === messages.length ? new Set() : new Set(messages.map((m) => m.id))));
  };

  const run = () => {
    setConfirming(false);
    setResult(null);
    bulk.mutate(
      { ids: [...selected], action, reason: reason.trim() || undefined },
      {
        onSuccess: (r) => {
          setResult({ succeeded: r.succeeded, failed: r.results.filter((x) => !x.success) });
          setSelected(new Set());
        },
        onError: (e) => setResult({ succeeded: 0, failed: [{ id: 0, success: false, error: e instanceof Error ? e.message : "Bulk action failed", code: "request_failed" }] }),
      },
    );
  };

  const availableActions = (): BulkQueueAction[] => {
    const statuses = new Set(messages.filter((m) => selected.has(m.id)).map((m) => m.status));
    const actions: BulkQueueAction[] = [];
    if (!statuses.has("cancelled") && !statuses.has("bounced")) actions.push("retry");
    if (statuses.has("pending") || statuses.has("deferred") || statuses.has("delivering")) actions.push("cancel");
    if (statuses.has("pending") || statuses.has("deferred")) actions.push("bounce");
    return actions.length > 0 ? actions : ["retry", "cancel", "bounce"];
  };

  if (messages.length === 0) return null;

  return (
    <div className="border border-[var(--border)] rounded-lg p-3 bg-[var(--bg-surface)] space-y-2 mb-4">
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <label className="flex items-center gap-1 text-[var(--text-secondary)]">
          <input type="checkbox" checked={selected.size === messages.length && messages.length > 0} onChange={toggleAll} aria-label="Select all messages" />
          All
        </label>
        <span className="text-[var(--text-primary)]">{selected.size} selected</span>
        <select value={action} onChange={(e) => setAction(e.target.value as BulkQueueAction)} className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm">
          {availableActions().map((a) => <option key={a} value={a}>{a}</option>)}
        </select>
        {action === "bounce" && (
          <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="Bounce reason" className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm" />
        )}
        <button
          onClick={() => setConfirming(true)}
          disabled={selected.size === 0 || bulk.isPending}
          className="px-3 py-1.5 rounded text-sm bg-[var(--accent)] text-white disabled:opacity-50"
        >
          {bulk.isPending ? "Applying…" : `Apply ${action} to ${selected.size}`}
        </button>
      </div>

      {messages.map((m) => (
        <label key={m.id} className="flex items-center gap-2 text-xs text-[var(--text-secondary)]">
          <input type="checkbox" checked={selected.has(m.id)} onChange={() => toggle(m.id)} aria-label={`Select message ${m.id}`} />
          #{m.id} · {m.status}
        </label>
      ))}

      {confirming && (
        <div className="border border-[var(--warning)] rounded p-3 bg-[var(--warning)]/5 space-y-2">
          <p className="text-sm text-[var(--text-primary)]">
            Apply <b>{action}</b> to <b>{selected.size}</b> queue message(s)? This is a real, destructive bulk operation.
          </p>
          <div className="flex gap-2">
            <button onClick={run} disabled={bulk.isPending} className="px-3 py-1.5 rounded text-sm bg-[var(--danger)] text-white disabled:opacity-50">Confirm {action}</button>
            <button onClick={() => setConfirming(false)} className="px-3 py-1.5 rounded text-sm bg-[var(--bg-subtle)] text-[var(--text-primary)]">Cancel</button>
          </div>
        </div>
      )}

      {result && (
        <div className="text-sm">
          <p className={result.failed.length === 0 ? "text-[var(--success)]" : "text-[var(--warning)]"}>
            Succeeded: {result.succeeded} · Failed: {result.failed.length}
          </p>
          {result.failed.length > 0 && (
            <ul className="mt-1 space-y-0.5 text-xs text-[var(--text-secondary)]">
              {result.failed.slice(0, 10).map((f) => (
                <li key={f.id}>#{f.id}: {f.error || f.code}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
