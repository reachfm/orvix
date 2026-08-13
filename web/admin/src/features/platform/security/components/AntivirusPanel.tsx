import { useAntivirusStatusQuery } from "../queries";
import { Loading, ErrorBox, Empty } from "./StateViews";

function StatusPill({ ok, onLabel, offLabel }: { ok: boolean; onLabel: string; offLabel: string }) {
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${ok ? "bg-[var(--success)]/10 text-[var(--success)]" : "bg-[var(--danger)]/10 text-[var(--danger)]"}`}>
      {ok ? onLabel : offLabel}
    </span>
  );
}

// Ported from the corrected PlatformSecurity.tsx AntivirusTab (see the
// standalone Antivirus operator-precedence fix commit) — same contract,
// now living in its own focused, independently testable component.
export default function AntivirusPanel() {
  const q = useAntivirusStatusQuery();
  if (q.isLoading) return <Loading />;
  if (q.error) return <ErrorBox error={q.error} />;
  const d = q.data;
  if (!d) return <Empty text="No antivirus status available" />;

  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 text-sm space-y-2">
          <div className="flex items-center justify-between">
            <span className="text-[var(--text-secondary)]">Engine</span>
            <span className="text-[var(--text-primary)] font-medium">{d.engine} @ {d.clamav_host}:{d.clamav_port}</span>
          </div>
          <div className="flex items-center justify-between">
            <span className="text-[var(--text-secondary)]">Daemon reachable</span>
            <StatusPill ok={d.engine_reachable} onLabel={d.clamav_response || "reachable"} offLabel="unreachable" />
          </div>
          <div className="flex items-center justify-between">
            <span className="text-[var(--text-secondary)]">Enforced on SMTP receive</span>
            <StatusPill ok={d.runtime_enforced} onLabel="enforced" offLabel="not enforced" />
          </div>
          {d.last_error && (
            <div className="flex items-center justify-between">
              <span className="text-[var(--text-secondary)]">Last error</span>
              <span className="text-[var(--danger)] text-xs max-w-[60%] truncate" title={d.last_error}>{d.last_error}</span>
            </div>
          )}
        </div>
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4 text-sm space-y-2">
          <div className="flex items-center justify-between"><span className="text-[var(--text-secondary)]">On infected</span><span className="text-[var(--text-primary)]">{d.policy_on_infected}</span></div>
          <div className="flex items-center justify-between"><span className="text-[var(--text-secondary)]">On scanner unavailable</span><span className="text-[var(--text-primary)]">{d.policy_on_scanner_unavailable}</span></div>
        </div>
      </div>

      <div className="grid grid-cols-3 md:grid-cols-7 gap-3">
        {([
          ["scanned", d.counts.scanned],
          ["infected", d.counts.infected],
          ["rejected", d.counts.rejected],
          ["quarantined", d.counts.quarantined],
          ["tagged", d.counts.tagged],
          ["fail_open", d.counts.fail_open],
          ["fail_closed", d.counts.fail_closed],
        ] as const).map(([k, v]) => (
          <div key={k} className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-3">
            <p className="text-xs text-[var(--text-secondary)] mb-1 capitalize">{k.replace("_", " ")}</p>
            <p className={`text-lg font-bold ${(k === "infected" || k === "fail_open") && v > 0 ? "text-[var(--danger)]" : "text-[var(--text-primary)]"}`}>{v}</p>
          </div>
        ))}
      </div>

      {d.honest_notes?.length > 0 && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-4">
          <p className="text-xs font-medium text-[var(--text-secondary)] mb-2">Notes</p>
          <ul className="space-y-1">
            {d.honest_notes.map((note) => <li key={note} className="text-xs text-[var(--text-muted)]">{note}</li>)}
          </ul>
        </div>
      )}
    </div>
  );
}
