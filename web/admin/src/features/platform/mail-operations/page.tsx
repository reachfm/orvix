import { useState } from "react";
import { Send, Loader2, AlertCircle } from "lucide-react";
import ConfirmDialog from "../../../components/ConfirmDialog";
import { useQueueMessagesQuery, isCoreMailDisabled, useQueueSummaryQuery } from "./queries";
import { useRetryQueueMessageMutation, useBounceQueueMessageMutation, useCancelQueueMessageMutation } from "./mutations";
import { bounceQueueConfirmation, cancelQueueConfirmation } from "./contract";
import QueueSummaryCards from "./components/QueueSummaryCards";
import QueueTable from "./components/QueueTable";
import QueueDetailDrawer from "./components/QueueDetailDrawer";
import CoreMailDisabledBanner from "./components/CoreMailDisabledBanner";
import BulkQueueActionPanel from "./components/BulkQueueActionPanel";

export default function MailOperationsPage() {
  const [statusFilter, setStatusFilter] = useState("");
  const [from, setFrom] = useState("");
  const [selected, setSelected] = useState<number | null>(null);
  const [confirmAction, setConfirmAction] = useState<{ id: number; kind: "bounce" | "cancel" } | null>(null);

  const summaryQ = useQueueSummaryQuery();
  const listQ = useQueueMessagesQuery({ status: statusFilter || undefined, from: from || undefined });
  const retryMut = useRetryQueueMessageMutation();
  const bounceMut = useBounceQueueMessageMutation();
  const cancelMut = useCancelQueueMessageMutation();

  const messages = listQ.data?.messages ?? [];
  const disabled = isCoreMailDisabled(listQ.error) || isCoreMailDisabled(summaryQ.error);

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[var(--text-primary)] flex items-center gap-2">
        <Send size={22} className="text-[var(--accent)]" /> Mail Operations
      </h2>

      {disabled ? (
        <CoreMailDisabledBanner />
      ) : (
        <>
          <QueueSummaryCards />

          <BulkQueueActionPanel messages={messages} />

          <div className="flex gap-2 mb-4">
            <input
              placeholder="Search sender…"
              value={from}
              onChange={(e) => setFrom(e.target.value)}
              className="px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]"
            />
            <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} className="px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
              <option value="">All statuses</option>
              <option value="pending">Pending</option>
              <option value="deferred">Deferred</option>
              <option value="delivering">Delivering</option>
              <option value="bounced">Bounced</option>
              <option value="dead_letter">Dead letter</option>
              <option value="cancelled">Cancelled</option>
            </select>
          </div>

          {listQ.isLoading ? (
            <div className="flex items-center justify-center h-48" role="status"><Loader2 size={24} className="text-[var(--accent)] animate-spin" /></div>
          ) : listQ.error ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-6 flex items-center gap-3" role="alert">
              <AlertCircle size={20} className="text-[var(--danger)]" />
              <span className="text-[var(--danger)] text-sm">Failed to load queue: {(listQ.error as Error).message}</span>
            </div>
          ) : messages.length === 0 ? (
            <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-8 text-center text-[var(--text-secondary)] text-sm">Queue is empty</div>
          ) : (
            <QueueTable
              messages={messages}
              onSelect={setSelected}
              onRetry={(id) => retryMut.mutate(id)}
              onBounce={(id) => setConfirmAction({ id, kind: "bounce" })}
              onCancel={(id) => setConfirmAction({ id, kind: "cancel" })}
              retryPendingId={retryMut.isPending ? (retryMut.variables ?? null) : null}
            />
          )}

          {selected !== null && <QueueDetailDrawer id={selected} onClose={() => setSelected(null)} />}

          <ConfirmDialog
            open={!!confirmAction}
            onOpenChange={(o) => !o && setConfirmAction(null)}
            title={confirmAction?.kind === "bounce" ? "Bounce message" : "Cancel message"}
            description={`This will ${confirmAction?.kind} message #${confirmAction?.id}. This cannot be undone.`}
            requireTypedName={
              confirmAction
                ? confirmAction.kind === "bounce"
                  ? bounceQueueConfirmation(confirmAction.id)
                  : cancelQueueConfirmation(confirmAction.id)
                : undefined
            }
            danger
            pending={bounceMut.isPending || cancelMut.isPending}
            onConfirm={() => {
              if (!confirmAction) return;
              const onSettled = () => setConfirmAction(null);
              if (confirmAction.kind === "bounce") bounceMut.mutate({ id: confirmAction.id }, { onSuccess: onSettled });
              else cancelMut.mutate(confirmAction.id, { onSuccess: onSettled });
            }}
          />
        </>
      )}
    </div>
  );
}
