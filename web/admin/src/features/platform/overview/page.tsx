import { ServerCog } from "lucide-react";
import StatCards from "./components/StatCards";
import NavCards, { type PlatformNavTarget } from "./components/NavCards";

export default function OverviewPage({
  email,
  onNavigate,
}: {
  email: string;
  onNavigate: (tab: PlatformNavTarget) => void;
}) {
  return (
    <div>
      <div className="flex items-center gap-3 mb-2">
        <ServerCog size={22} className="text-[var(--accent)]" />
        <h2 className="text-2xl font-semibold text-[var(--text-primary)]">Platform Administration</h2>
      </div>
      <p className="text-sm text-[var(--text-secondary)] mb-6">
        Signed in as <span className="text-[var(--text-primary)]">{email || "platform super admin"}</span> — platform-wide
        administration. This identity has no owning tenant and does not have access to any single
        organization's customer portal data.
      </p>

      <StatCards />
      <NavCards onNavigate={onNavigate} />
    </div>
  );
}
