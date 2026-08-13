import { Loader2, AlertCircle } from "lucide-react";
import { usePlatformDashboardQuery } from "../queries";

function formatBytes(n: number): string {
  if (n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  return `${(n / Math.pow(1024, i)).toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

// Loading, error, and populated are visually and structurally distinct
// states — this component never falls back to a decorative "0" or "—"
// placeholder when the request has actually failed, and it renders
// only fields PlatformDashboard genuinely returns (see contract.ts's
// note on the fields this endpoint does NOT have, like queue/storage
// alerts — those live on their own real pages, not fabricated here).
export default function StatCards() {
  const dashQ = usePlatformDashboardQuery();

  if (dashQ.isLoading) {
    return (
      <div className="flex items-center gap-2 text-[var(--text-secondary)] mb-8" role="status">
        <Loader2 size={16} className="animate-spin" /> Loading platform totals…
      </div>
    );
  }
  if (dashQ.error) {
    return (
      <div className="flex items-center gap-2 text-[var(--danger)] text-sm mb-8" role="alert">
        <AlertCircle size={16} /> Failed to load platform dashboard: {(dashQ.error as Error).message}
      </div>
    );
  }
  const d = dashQ.data;
  if (!d) return null;

  return (
    <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-8">
      <Stat label="Organizations" value={d.total_organizations} sub={`${d.active_organizations} active`} />
      <Stat label="Domains" value={d.total_domains} />
      <Stat label="Mailboxes" value={d.total_mailboxes} />
      <Stat label="Storage used" value={formatBytes(d.quota_used_bytes)} />
    </div>
  );
}

function Stat({ label, value, sub }: { label: string; value: number | string; sub?: string }) {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
      <p className="text-xs text-[var(--text-secondary)] mb-1">{label}</p>
      <p className="text-2xl font-bold text-[var(--text-primary)]">{value}</p>
      {sub && <p className="text-xs text-[var(--text-muted)] mt-1">{sub}</p>}
    </div>
  );
}
