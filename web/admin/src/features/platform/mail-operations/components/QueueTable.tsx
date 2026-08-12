import type { QueueMessage } from "../contract";

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
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-[var(--border)]">
            <th className="text-left p-3 text-[var(--text-secondary)] font-medium">From</th>
            <th className="text-left p-3 text-[var(--text-secondary)] font-medium">To</th>
            <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Status</th>
            <th className="text-left p-3 text-[var(--text-secondary)] font-medium">Attempts</th>
            <th className="text-right p-3 text-[var(--text-secondary)] font-medium">Actions</th>
          </tr>
        </thead>
        <tbody>
          {messages.map((m) => (
            <tr key={m.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-elevated)] cursor-pointer" onClick={() => onSelect(m.id)}>
              <td className="p-3 text-[var(--text-primary)]">{m.from_address}</td>
              <td className="p-3 text-[var(--text-primary)]">{m.to_address}</td>
              <td className="p-3 text-[var(--text-secondary)]">{m.status}</td>
              <td className="p-3 text-[var(--text-secondary)]">{m.attempt_count} / {m.max_attempts}</td>
              <td className="p-3 text-right" onClick={(e) => e.stopPropagation()}>
                <button
                  title="Retry"
                  disabled={retryPendingId === m.id}
                  onClick={() => onRetry(m.id)}
                  className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--accent)] disabled:opacity-40"
                >
                  Retry
                </button>
                <button title="Bounce" onClick={() => onBounce(m.id)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--warning)]">Bounce</button>
                <button title="Cancel" onClick={() => onCancel(m.id)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]">Cancel</button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
