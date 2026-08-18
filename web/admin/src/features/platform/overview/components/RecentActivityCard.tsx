import { AlertCircle, ChevronRight, History, Loader2 } from "lucide-react";
import { usePlatformDashboardQuery } from "../queries";
import { formatRelativeTime } from "../../monitoring/format";

// RecentActivityCard renders ONLY real recent_audit_entries from
// GET /platform/dashboard. No invented activity rows — an empty
// backend list renders an explicit empty state, never a placeholder
// like "No recent activity yet, check back soon" dressed up to look
// like real content.
export default function RecentActivityCard({ onViewAuditLog }: { onViewAuditLog: () => void }) {
  const dashQ = usePlatformDashboardQuery();

  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-2xl p-6">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Recent Activity</h3>
        <button
          onClick={onViewAuditLog}
          className="inline-flex items-center gap-0.5 text-xs font-medium text-[var(--accent)] hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-[var(--accent)] rounded"
        >
          View audit log <ChevronRight size={13} />
        </button>
      </div>

      {dashQ.isLoading && (
        <div className="flex items-center gap-2 text-[var(--text-muted)] text-sm py-4" role="status">
          <Loader2 size={15} className="animate-spin" /> Loading recent activity…
        </div>
      )}
      {dashQ.error && (
        <div className="flex items-center gap-2 text-[var(--danger)] text-sm py-2" role="alert">
          <AlertCircle size={15} /> Failed to load recent activity: {(dashQ.error as Error).message}
        </div>
      )}
      {dashQ.data && (
        (dashQ.data.recent_audit_entries?.length ?? 0) === 0 ? (
          <div className="flex flex-col items-center text-center py-8 text-[var(--text-muted)]">
            <History size={22} className="mb-2 opacity-60" />
            <p className="text-sm">No recent platform activity.</p>
          </div>
        ) : (
          <ul className="space-y-3">
            {dashQ.data.recent_audit_entries!.slice(0, 8).map((entry, i) => (
              <li key={`${entry.timestamp}-${i}`} className="flex items-start justify-between gap-3 text-sm">
                <div className="min-w-0">
                  <p className="text-[var(--text-primary)] truncate">{entry.action}</p>
                  <p className="text-xs text-[var(--text-muted)] truncate">{entry.target}</p>
                </div>
                <span className="text-xs text-[var(--text-muted)] flex-shrink-0 whitespace-nowrap">
                  {formatRelativeTime(entry.timestamp)}
                </span>
              </li>
            ))}
          </ul>
        )
      )}
    </div>
  );
}
