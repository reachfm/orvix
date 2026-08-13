import StatusBadge from "../../components/StatusBadge";
import type { QueueMessage } from "../contract";
import { canBounceMessage, canCancelMessage, canRetryMessage, failureCategoryLabel } from "../contract";

const STATUS_TONES: Record<string, "success" | "warning" | "danger" | "neutral" | "info"> = {
  pending: "info",
  leased: "neutral",
  delivering: "info",
  deferred: "warning",
  delivered: "success",
  bounced: "danger",
  dead_letter: "danger",
  cancelled: "neutral",
};

export default function QueueTable({
  messages,
  onSelect,
  onRetry,
  onBounce,
  onCancel,
  retryPendingId,
}: {
  messages: QueueMessage[];
  onSelect: (id: number) => void;
  onRetry: (id: number) => void;
  onBounce: (id: number) => void;
  onCancel: (id: number) => void;
  retryPendingId: number | null;
}) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
      <div className="overflow-x-auto">
        <table className="w-full text-sm" aria-label="Mail queue messages">
          <thead>
            <tr className="border-b border-[var(--border)]">
              <th className="text-left p-3 text-[var(--text-secondary)] font-medium">ID</th>
              <th className="text-left p-3 text-[var(--text-secondary)] font-medium">From</th>
              <th className="text-left p-3 text-[var(--text-secondary)] font-medium">To</th>
              <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Tenant / domain</th>
              <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Status</th>
              <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Failure</th>
              <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Attempts</th>
              <th className="text-right p-3 text-[var(--text-secondary)] font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {messages.map((m) => (
              <tr key={m.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)] cursor-pointer" onClick={() => onSelect(m.id)}>
                <td className="p-3 text-[var(--text-muted)]">#{m.id}</td>
                <td className="p-3 text-[var(--text-primary)]">{m.from_address}</td>
                <td className="p-3 text-[var(--text-primary)]">{m.to_address}</td>
                <td className="p-3 text-[var(--text-secondary)]">
                  tenant {m.tenant_id}{m.domain_id ? ` · #${m.domain_id}` : ""}
                  <span className="block text-xs text-[var(--text-muted)]">{m.recipient_domain || "—"}</span>
                </td>
                <td className="p-3">
                  <StatusBadge tone={STATUS_TONES[m.status] ?? "neutral"} label={`Status ${m.status}`}>
                    {m.status}
                  </StatusBadge>
                </td>
                <td className="p-3 text-[var(--text-secondary)]">
                  {failureCategoryLabel(m.failure_category) || "—"}
                </td>
                <td className="p-3 text-[var(--text-secondary)]">{m.attempt_count} / {m.max_attempts}</td>
                <td className="p-3 text-right" onClick={(e) => e.stopPropagation()}>
                  {/* State-aware actions: retry only when the backend
                      says the entry is retryable; bounce/cancel only on
                      non-terminal statuses. */}
                  <button
                    title="Retry"
                    aria-label={`Retry message ${m.id}`}
                    disabled={retryPendingId === m.id || !canRetryMessage(m)}
                    onClick={() => onRetry(m.id)}
                    className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--accent)] disabled:opacity-40"
                  >
                    Retry
                  </button>
                  <button
                    title="Bounce"
                    aria-label={`Bounce message ${m.id}`}
                    disabled={!canBounceMessage(m)}
                    onClick={() => onBounce(m.id)}
                    className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--warning)] disabled:opacity-40"
                  >
                    Bounce
                  </button>
                  <button
                    title="Cancel"
                    aria-label={`Cancel message ${m.id}`}
                    disabled={!canCancelMessage(m)}
                    onClick={() => onCancel(m.id)}
                    className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)] disabled:opacity-40"
                  >
                    Cancel
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
