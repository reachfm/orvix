import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Send, Loader2, AlertCircle, RotateCw, Ban, XCircle, MailWarning } from "lucide-react";
import { api, ApiError } from "../api";
import ConfirmDialog from "./ConfirmDialog";

function isCoreMailDisabled(err: unknown): boolean {
  return err instanceof ApiError && err.code === "COREMAIL_DISABLED";
}

// Matches internal/coremail/queue.QueueMetrics's JSON tags, as wrapped by
// AdminQueueSummary's {"metrics": {...}} envelope. "code" is present only
// on the COREMAIL_DISABLED 503 contract (coreMailUnavailableResponse).
interface QueueSummaryResponse {
  metrics?: {
    total: number;
    pending: number;
    deferred: number;
    dead_letter: number;
    bounced: number;
  };
  code?: string;
  error?: string;
}
interface QueueMessage {
  id: string;
  from: string;
  to: string;
  status: string;
  attempts: number;
  created_at?: string;
  last_error?: string;
}

export default function MailOperations() {
  const qc = useQueryClient();
  const [statusFilter, setStatusFilter] = useState("");
  const [q, setQ] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [confirmAction, setConfirmAction] = useState<{ id: string; kind: "bounce" | "cancel" } | null>(null);

  const summaryQ = useQuery<QueueSummaryResponse>({ queryKey: ["queue-summary"], queryFn: api.getQueueSummary, retry: false });
  const listQ = useQuery<{ messages?: QueueMessage[] } | QueueMessage[]>({
    queryKey: ["queue-messages", statusFilter, q],
    queryFn: () => api.listQueueMessages({ status: statusFilter || undefined, q: q || undefined }),
    retry: false,
  });

  const detailQ = useQuery<QueueMessage>({
    queryKey: ["queue-message", selected],
    queryFn: () => api.getQueueMessage(selected as string),
    enabled: !!selected,
  });

  const retryMut = useMutation({
    mutationFn: (id: string) => api.retryQueueMessage(id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["queue-messages"] }); qc.invalidateQueries({ queryKey: ["queue-summary"] }); },
  });
  const bounceMut = useMutation({
    mutationFn: (id: string) => api.bounceQueueMessage(id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["queue-messages"] }); qc.invalidateQueries({ queryKey: ["queue-summary"] }); setConfirmAction(null); },
  });
  const cancelMut = useMutation({
    mutationFn: (id: string) => api.cancelQueueMessage(id),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["queue-messages"] }); qc.invalidateQueries({ queryKey: ["queue-summary"] }); setConfirmAction(null); },
  });

  const messages: QueueMessage[] = Array.isArray(listQ.data) ? listQ.data : (listQ.data?.messages ?? []);
  const disabled = isCoreMailDisabled(listQ.error) || isCoreMailDisabled(summaryQ.error);

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0] flex items-center gap-2">
        <Send size={22} className="text-[#4F7CFF]" /> Mail Operations
      </h2>

      {disabled ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center">
          <MailWarning size={32} className="text-[#8B92A8] mx-auto mb-3" />
          <p className="text-[#E8EAF0] text-sm font-medium mb-1">CoreMail is disabled</p>
          <p className="text-[#8B92A8] text-sm">The mail queue is not available on this deployment.</p>
        </div>
      ) : (
      <>
      {summaryQ.isLoading ? (
        <p className="text-[#8B92A8] mb-6">Loading summary…</p>
      ) : summaryQ.error ? (
        <p className="text-[#F87171] mb-6">Failed to load queue summary: {(summaryQ.error as Error).message}</p>
      ) : summaryQ.data?.metrics ? (
        <div className="grid grid-cols-4 gap-4 mb-6">
          {([
            ["total", summaryQ.data.metrics.total],
            ["pending", summaryQ.data.metrics.pending],
            ["deferred", summaryQ.data.metrics.deferred],
            ["failed", summaryQ.data.metrics.dead_letter + summaryQ.data.metrics.bounced],
          ] as const).map(([k, v]) => (
            <div key={k} className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
              <p className="text-xs text-[#8B92A8] mb-1 capitalize">{k}</p>
              <p className={`text-2xl font-bold ${k === "failed" && v > 0 ? "text-[#F87171]" : "text-[#E8EAF0]"}`}>{v}</p>
            </div>
          ))}
        </div>
      ) : null}

      <div className="flex gap-2 mb-4">
        <input
          placeholder="Search from/to…"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          className="px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-sm text-[#E8EAF0]"
        />
        <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} className="px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-sm text-[#E8EAF0]">
          <option value="">All statuses</option>
          <option value="pending">Pending</option>
          <option value="deferred">Deferred</option>
          <option value="failed">Failed</option>
        </select>
      </div>

      {listQ.isLoading ? (
        <div className="flex items-center justify-center h-48"><Loader2 size={24} className="text-[#4F7CFF] animate-spin" /></div>
      ) : listQ.error ? (
        <div className="bg-[#13161C] border border-[#F87171]/30 rounded-xl p-6 flex items-center gap-3">
          <AlertCircle size={20} className="text-[#F87171]" />
          <span className="text-[#F87171] text-sm">Failed to load queue: {(listQ.error as Error).message}</span>
        </div>
      ) : messages.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center text-[#8B92A8] text-sm">Queue is empty</div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-3 text-[#8B92A8] font-medium">From</th>
                <th className="text-left p-3 text-[#8B92A8] font-medium">To</th>
                <th className="text-left p-3 text-[#8B92A8] font-medium">Status</th>
                <th className="text-left p-3 text-[#8B92A8] font-medium">Attempts</th>
                <th className="text-right p-3 text-[#8B92A8] font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {messages.map((m) => (
                <tr key={m.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26] cursor-pointer" onClick={() => setSelected(m.id)}>
                  <td className="p-3 text-[#E8EAF0]">{m.from}</td>
                  <td className="p-3 text-[#E8EAF0]">{m.to}</td>
                  <td className="p-3 text-[#8B92A8]">{m.status}</td>
                  <td className="p-3 text-[#8B92A8]">{m.attempts}</td>
                  <td className="p-3 text-right" onClick={(e) => e.stopPropagation()}>
                    <button title="Retry" disabled={retryMut.isPending} onClick={() => retryMut.mutate(m.id)} className="p-1.5 text-[#8B92A8] hover:text-[#4F7CFF]"><RotateCw size={14} /></button>
                    <button title="Bounce" onClick={() => setConfirmAction({ id: m.id, kind: "bounce" })} className="p-1.5 text-[#8B92A8] hover:text-[#FBBF24]"><Ban size={14} /></button>
                    <button title="Cancel" onClick={() => setConfirmAction({ id: m.id, kind: "cancel" })} className="p-1.5 text-[#8B92A8] hover:text-[#F87171]"><XCircle size={14} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {selected && (
        <div className="mt-6 bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <h3 className="text-sm font-semibold text-[#E8EAF0] mb-3">Message Detail</h3>
          {detailQ.isLoading ? <p className="text-[#8B92A8] text-sm">Loading…</p> :
            detailQ.error ? <p className="text-[#F87171] text-sm">Failed to load: {(detailQ.error as Error).message}</p> :
            detailQ.data ? (
              <dl className="text-sm space-y-1">
                <div className="flex justify-between"><dt className="text-[#8B92A8]">ID</dt><dd className="text-[#E8EAF0] font-mono">{detailQ.data.id}</dd></div>
                <div className="flex justify-between"><dt className="text-[#8B92A8]">Status</dt><dd className="text-[#E8EAF0]">{detailQ.data.status}</dd></div>
                {detailQ.data.last_error && <div className="flex justify-between"><dt className="text-[#8B92A8]">Last error</dt><dd className="text-[#F87171] max-w-xs truncate">{detailQ.data.last_error}</dd></div>}
              </dl>
            ) : null}
        </div>
      )}
      </>
      )}

      <ConfirmDialog
        open={!!confirmAction}
        onOpenChange={(o) => !o && setConfirmAction(null)}
        title={confirmAction?.kind === "bounce" ? "Bounce message" : "Cancel message"}
        description={`This will ${confirmAction?.kind} message ${confirmAction?.id}. This cannot be undone.`}
        requireTypedName={confirmAction?.id}
        danger
        pending={bounceMut.isPending || cancelMut.isPending}
        onConfirm={() => {
          if (!confirmAction) return;
          if (confirmAction.kind === "bounce") bounceMut.mutate(confirmAction.id);
          else cancelMut.mutate(confirmAction.id);
        }}
      />
    </div>
  );
}
