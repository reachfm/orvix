import { useQueueDetailQuery } from "../queries";

export default function QueueDetailDrawer({ id, onClose }: { id: number; onClose: () => void }) {
  const detailQ = useQueueDetailQuery(id);

  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/50" onClick={onClose}>
      <div className="w-full max-w-lg h-full bg-[var(--bg-surface)] border-l border-[var(--border)] p-6 overflow-y-auto" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-lg font-semibold text-[var(--text-primary)]">Message detail</h3>
          <button onClick={onClose} aria-label="Close" className="text-[var(--text-secondary)] hover:text-[var(--text-primary)]">×</button>
        </div>

        {detailQ.isLoading ? (
          <p className="text-[var(--text-secondary)] text-sm">Loading…</p>
        ) : detailQ.error ? (
          <p className="text-[var(--danger)] text-sm">Failed to load: {(detailQ.error as Error).message}</p>
        ) : !detailQ.data ? null : (
          <>
            <dl className="text-sm space-y-1 mb-6">
              <Row label="ID" value={String(detailQ.data.message.id)} mono />
              <Row label="From" value={detailQ.data.message.from_address} />
              <Row label="To" value={detailQ.data.message.to_address} />
              <Row label="Status" value={detailQ.data.message.status} />
              <Row label="Attempts" value={`${detailQ.data.message.attempt_count} / ${detailQ.data.message.max_attempts}`} />
              {detailQ.data.message.last_error && <Row label="Last error" value={detailQ.data.message.last_error} danger />}
            </dl>
            <h4 className="text-sm font-semibold text-[var(--text-primary)] mb-2">Delivery attempts</h4>
            {detailQ.data.attempts.length === 0 ? (
              <p className="text-[var(--text-secondary)] text-sm">No delivery attempts recorded.</p>
            ) : (
              <div className="space-y-2">
                {detailQ.data.attempts.map((a) => (
                  <div key={a.attempt} className="bg-[var(--bg-elevated)] border border-[var(--border)] rounded p-3 text-xs">
                    <div className="flex justify-between text-[var(--text-secondary)]">
                      <span>Attempt {a.attempt}</span>
                      <span>{a.at}</span>
                    </div>
                    <p className="text-[var(--text-primary)] mt-1">{a.result} ({a.status_code}) — {a.remote_host}</p>
                    {a.error && <p className="text-[var(--danger)] mt-1">{a.error}</p>}
                  </div>
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function Row({ label, value, mono, danger }: { label: string; value: string; mono?: boolean; danger?: boolean }) {
  return (
    <div className="flex justify-between gap-4">
      <dt className="text-[var(--text-secondary)]">{label}</dt>
      <dd className={`text-right break-all ${danger ? "text-[var(--danger)]" : "text-[var(--text-primary)]"} ${mono ? "font-mono" : ""}`}>{value}</dd>
    </div>
  );
}
