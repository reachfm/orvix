import type { ReactNode } from "react";

export type KpiAccent = "violet" | "blue" | "emerald" | "amber" | "orange";

const ACCENT_VARS: Record<KpiAccent, { fg: string; bg: string }> = {
  violet: { fg: "var(--accent-violet)", bg: "var(--accent-violet-soft)" },
  blue: { fg: "var(--accent-blue)", bg: "var(--accent-blue-soft)" },
  emerald: { fg: "var(--accent-emerald)", bg: "var(--accent-emerald-soft)" },
  amber: { fg: "var(--accent-amber)", bg: "var(--accent-amber-soft)" },
  orange: { fg: "var(--accent-orange)", bg: "var(--accent-orange-soft)" },
};

// KpiCard is the shared premium stat-card primitive: a soft-tinted
// icon circle, a label, a large value, and an optional sub-line. Used
// for both infrastructure metrics (uptime/storage/IP) and business
// metrics (organizations/domains/mailboxes) so the two groups share
// one visual language but stay in visually distinct rows/sections.
export default function KpiCard({
  icon,
  accent,
  label,
  value,
  sub,
}: {
  icon: ReactNode;
  accent: KpiAccent;
  label: string;
  value: ReactNode;
  sub?: ReactNode;
}) {
  const c = ACCENT_VARS[accent];
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-2xl p-5">
      <div
        className="w-10 h-10 rounded-xl flex items-center justify-center mb-3"
        style={{ backgroundColor: c.bg, color: c.fg }}
        aria-hidden="true"
      >
        {icon}
      </div>
      <p className="text-xs font-medium text-[var(--text-secondary)] mb-1">{label}</p>
      <p className="text-2xl font-semibold text-[var(--text-primary)] leading-tight break-words">{value}</p>
      {sub && <p className="text-xs text-[var(--text-muted)] mt-1.5">{sub}</p>}
    </div>
  );
}

export function KpiCardSkeleton() {
  return (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-2xl p-5 animate-pulse" aria-hidden="true">
      <div className="w-10 h-10 rounded-xl bg-[var(--bg-subtle)] mb-3" />
      <div className="h-3 w-20 rounded bg-[var(--bg-subtle)] mb-2" />
      <div className="h-6 w-16 rounded bg-[var(--bg-subtle)]" />
    </div>
  );
}
