import { useState } from "react";
import { useBulkQueueActionMutation } from "../mutations";
import ConfirmDialog from "../../../../components/ConfirmDialog";
import type { BulkQueueAction, BulkQueueActionResult, QueueMessage } from "../contract";
import { canBounceMessage, canCancelMessage, canRetryMessage } from "../contract";

interface Props {
  messages: QueueMessage[];
}

/**
 * BulkQueueActionPanel applies retry/cancel/bounce to multiple queue
 * messages through the real POST /admin/queue/messages/bulk-action
 * endpoint (platformMW-gated). The backend reports per-row results; the
 * panel surfaces succeeded/failed counts and per-row errors without
 * pretending the operation was atomic. Selection is state-aware: retry
 * only on retryable entries; cancel/bounce only on non-terminal
 * entries. Destructive bulk actions require a typed confirmation.
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
    const eligible = eligibleFor(action).map((m) => m.id);
    setSelected((prev) => (prev.size === eligible.length && eligible.length > 0 ? new Set() : new Set(eligible)));
  };

  /** Only entries the backend state machine accepts for the action. */
  const eligibleFor = (a: BulkQueueAction): QueueMessage[] =>
    messages.filter((m) => (a === "retry" ? canRetryMessage(m) : a === "cancel" ? canCancelMessage(m) : canBounceMessage(m)));

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

  const destructive = action !== "retry";
  const typedPhrase = destructive ? `BULK-${action.toUpperCase()}` : undefined;
  const eligible = eligibleFor(action);

  if (messages.length === 0) return null;

  return (
    <div className="border border-[var(--border)] rounded-lg p-3 bg-[var(--bg-surface)] space-y-2 mb-4">
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <label className="flex items-center gap-1 text-[var(--text-secondary)]">
          <input type="checkbox" checked={selected.size === eligible.length && eligible.length > 0} onChange={toggleAll} aria-label="Select all eligible messages" />
          All eligible
        </label>
        <span className="text-[var(--text-primary)]">{selected.size} selected</span>
        <select value={action} onChange={(e) => { setAction(e.target.value as BulkQueueAction); setSelected(new Set()); }} className="px-3 py-1.5 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm">
          {(["retry", "cancel", "bounce"] as BulkQueueAction[]).map((a) => (
            <option key={a} value={a}>{a}</option>
          ))}
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

      <div className="space-y-1">
        {messages.map((m) => {
          const selectable = (action === "retry" ? canRetryMessage : action === "cancel" ? canCancelMessage : canBounceMessage)(m);
          return (
            <label key={m.id} className={`flex items-center gap-2 text-xs ${selectable ? "text-[var(--text-secondary)]" : "text-[var(--text-muted)]"}`}>
              <input type="checkbox" checked={selected.has(m.id)} disabled={!selectable} onChange={() => toggle(m.id)} aria-label={`Select message ${m.id}`} />
              #{m.id} · {m.status}
            </label>
          );
        })}
      </div>

      {result && (
        <div className="text-sm" role="status">
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

      <ConfirmDialog
        open={confirming}
        onOpenChange={(o) => !o && setConfirming(false)}
        title={`${action === "retry" ? "Retry" : action === "cancel" ? "Cancel" : "Bounce"} ${selected.size} message(s)`}
        description={`Apply ${action} to ${selected.size} queue message(s)? This is a real bulk operation; the backend reports per-message results.`}
        requireTypedName={typedPhrase}
        confirmLabel={`Confirm ${action}`}
        danger={destructive}
        pending={bulk.isPending}
        onConfirm={run}
      />
    </div>
  );
}
