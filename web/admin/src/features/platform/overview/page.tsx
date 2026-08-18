import InfraKPICards from "./components/InfraKPICards";
import StorageDonut from "./components/StorageDonut";
import SystemHealthPanel from "./components/SystemHealthPanel";
import StatCards from "./components/StatCards";
import RecentActivityCard from "./components/RecentActivityCard";
import NavCards, { type PlatformNavTarget } from "./components/NavCards";
import { greeting } from "../monitoring/format";

// OverviewPage is the Platform Super Admin's premium landing
// dashboard. Every number on this page is either a real value from
// GET /platform/dashboard or GET /api/v1/monitoring/health, or an
// explicit "Unavailable"/empty state — see each child component's own
// header comment for its exact data source. Nothing here is
// hard-coded demo data.
export default function OverviewPage({
  email,
  onNavigate,
}: {
  email: string;
  onNavigate: (tab: PlatformNavTarget) => void;
}) {
  const displayName = email ? email.split("@")[0] : "";

  return (
    <div className="space-y-8">
      <div>
        <h2 className="text-2xl font-semibold text-[var(--text-primary)]">
          {greeting()}{displayName ? `, ${displayName}` : ""} <span aria-hidden="true">👋</span>
        </h2>
        <p className="text-sm text-[var(--text-secondary)] mt-1">
          Platform infrastructure and administration overview
        </p>
      </div>

      <section aria-label="Infrastructure metrics">
        <InfraKPICards />
      </section>

      <section aria-label="Storage and system health" className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <StorageDonut />
        <SystemHealthPanel onNavigate={() => onNavigate("health")} />
      </section>

      <section aria-label="Platform scale metrics">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Platform Scale</h3>
        <StatCards />
      </section>

      <section aria-label="Recent activity">
        <RecentActivityCard onViewAuditLog={() => onNavigate("platform-audit")} />
      </section>

      <section aria-label="Quick actions">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] mb-3">Quick Actions</h3>
        <NavCards onNavigate={onNavigate} />
      </section>
    </div>
  );
}
