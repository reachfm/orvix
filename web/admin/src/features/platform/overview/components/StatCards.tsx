import { AlertCircle, Building, Building2, Globe, Mail, PieChart } from "lucide-react";
import { usePlatformDashboardQuery } from "../queries";
import { formatBytes } from "../../monitoring/format";
import KpiCard, { KpiCardSkeleton } from "./KpiCard";

// StatCards renders the platform's SaaS/business-scale KPIs — as
// distinct from InfraKPICards' host infrastructure metrics — sourced
// only from GET /platform/dashboard. Loading, error, and populated
// are visually and structurally distinct states; this component
// never falls back to a decorative "0"/"—" placeholder when the
// request has actually failed, and it renders only fields
// PlatformDashboard genuinely returns (see contract.ts's note on
// fields this endpoint does NOT have, like queue/storage alerts —
// those live on their own real pages, not fabricated here).
export default function StatCards() {
  const dashQ = usePlatformDashboardQuery();

  if (dashQ.isLoading) {
    return (
      <div className="grid grid-cols-2 lg:grid-cols-5 gap-4" role="status" aria-label="Loading platform totals">
        {Array.from({ length: 5 }).map((_, i) => <KpiCardSkeleton key={i} />)}
      </div>
    );
  }
  if (dashQ.error) {
    return (
      <div className="flex items-center gap-2 text-[var(--danger)] text-sm bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-2xl p-4" role="alert">
        <AlertCircle size={16} /> Failed to load platform dashboard: {(dashQ.error as Error).message}
      </div>
    );
  }
  const d = dashQ.data;
  if (!d) return null;

  return (
    <div className="grid grid-cols-2 lg:grid-cols-5 gap-4">
      <KpiCard accent="violet" icon={<Building size={19} />} label="Organizations" value={d.total_organizations} />
      <KpiCard accent="emerald" icon={<Building2 size={19} />} label="Active Organizations" value={d.active_organizations} />
      <KpiCard accent="blue" icon={<Globe size={19} />} label="Domains" value={d.total_domains} />
      <KpiCard accent="amber" icon={<Mail size={19} />} label="Mailboxes" value={d.total_mailboxes} />
      <KpiCard accent="orange" icon={<PieChart size={19} />} label="Mail Storage Used" value={formatBytes(d.quota_used_bytes)} />
    </div>
  );
}
