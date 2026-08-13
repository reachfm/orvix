import { useState } from "react";
import { useLegalHolds, usePlaceLegalHold, useReleaseLegalHold, usePlanPurge, useExecutePurge, useCustodyEvents, useEffectivePolicy } from "./queries";
import { PURGE_CONFIRMATION_PHRASE, type LegalHold } from "./contract";

function HoldRow({ hold, kind, id }: { hold: LegalHold; kind: string; id: number }) {
  const release = useReleaseLegalHold(kind, id);
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);
  return (
    <tr className="border-b border-[var(--bg-subtle)]">
      <td className="p-3 text-[var(--text-primary)]">#{hold.id}</td>
      <td className="p-3 text-[var(--text-primary)]">{hold.scope_kind}:{hold.scope_id}</td>
      <td className="p-3 text-[var(--text-secondary)]">{hold.case_ref || "—"}</td>
      <td className="p-3 text-[var(--text-secondary)]">{hold.reason}</td>
      <td className="p-3 text-[var(--text-secondary)]">{hold.ends_at ? new Date(hold.ends_at).toLocaleString() : "Indefinite"}</td>
      <td className="p-3">
        {!hold.released && (
          <button
            onClick={() => { setFeedback(null); release.mutate(hold.id, { onSuccess: () => setFeedback({ kind: "ok", message: "Hold released." }), onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Release failed." }) }); }}
            disabled={release.isPending}
            className="px-2 py-1 rounded text-xs bg-[var(--danger)]/10 text-[var(--danger)] disabled:opacity-50"
          >
            Release
          </button>
        )}
        {feedback && <span className={`block mt-1 text-xs ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</span>}
      </td>
    </tr>
  );
}

export default function RetentionPage() {
  const [scopeKind, setScopeKind] = useState("tenant");
  const [scopeId, setScopeId] = useState("");
  const [caseRef, setCaseRef] = useState("");
  const [holdReason, setHoldReason] = useState("");
  const [olderThan, setOlderThan] = useState("");
  const [purgeConfirm, setPurgeConfirm] = useState("");
  const [feedback, setFeedback] = useState<{ kind: "ok" | "error"; message: string } | null>(null);
  const sid = Number.parseInt(scopeId, 10) || 0;
  const { data: holds } = useLegalHolds(sid > 0 ? scopeKind : "tenant", sid);
  const { data: custody } = useCustodyEvents(sid > 0 ? scopeKind : "tenant", sid);
  const { data: effectivePolicy } = useEffectivePolicy(sid > 0 ? { tenant_id: sid } : {});
  const place = usePlaceLegalHold(scopeKind, sid);
  const planPurge = usePlanPurge(scopeKind, sid);
  const executePurge = useExecutePurge(scopeKind, sid);
  const [purgePlan, setPurgePlan] = useState<{ eligible: number; held: number } | null>(null);

  const placeHold = () => {
    if (!sid || holdReason.trim() === "") { setFeedback({ kind: "error", message: "Scope ID and reason are required." }); return; }
    setFeedback(null);
    place.mutate({ scope_kind: scopeKind, scope_id: sid, case_ref: caseRef.trim() || undefined, reason: holdReason.trim() }, {
      onSuccess: () => { setHoldReason(""); setFeedback({ kind: "ok", message: "Legal hold placed." }); },
      onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Place failed." }),
    });
  };

  const runPlan = () => {
    if (!sid) { setFeedback({ kind: "error", message: "A scope ID is required." }); return; }
    setFeedback(null);
    planPurge.mutate(olderThan || new Date().toISOString(), {
      onSuccess: (p) => { setPurgePlan({ eligible: p.eligible_count, held: p.held_count }); },
      onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Plan failed." }),
    });
  };

  const runPurge = () => {
    if (!sid) { setFeedback({ kind: "error", message: "A scope ID is required." }); return; }
    if (purgeConfirm !== PURGE_CONFIRMATION_PHRASE) { setFeedback({ kind: "error", message: `Confirmation must be exactly ${PURGE_CONFIRMATION_PHRASE}.` }); return; }
    setFeedback(null);
    executePurge.mutate(
      { scope_kind: scopeKind, scope_id: sid, older_than: olderThan || new Date().toISOString(), confirmation: purgeConfirm, idempotency_key: crypto.randomUUID() },
      {
        onSuccess: (r) => { setPurgeConfirm(""); setPurgePlan(null); setFeedback({ kind: "ok", message: `Purged ${r.purged} items.` }); },
        onError: (e) => setFeedback({ kind: "error", message: e instanceof Error ? e.message : "Purge failed." }),
      },
    );
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-xl font-semibold text-[var(--text-primary)]">Retention & Legal Hold</h2>
        <p className="text-sm text-[var(--text-secondary)]">Hierarchical policies, legal holds, and confirmed purge.</p>
      </div>

      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 space-y-3">
          <h3 className="text-sm font-semibold text-[var(--text-primary)]">Effective policy</h3>
          {!effectivePolicy ? (
            <p className="text-sm text-[var(--text-muted)]">No applicable policy for this scope.</p>
          ) : (
            <p className="text-sm text-[var(--text-primary)]">
              {effectivePolicy.level} · retention {effectivePolicy.retention_days === 0 ? "indefinite" : `${effectivePolicy.retention_days}d`} · recovery {effectivePolicy.recovery_days}d
            </p>
          )}
        </div>

        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 space-y-3">
          <h3 className="text-sm font-semibold text-[var(--text-primary)]">Scope</h3>
          <div className="flex flex-wrap gap-2">
            <select value={scopeKind} onChange={(e) => setScopeKind(e.target.value)} className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm">
              <option value="platform">platform</option><option value="tenant">tenant</option><option value="domain">domain</option><option value="mailbox">mailbox</option>
            </select>
            <input value={scopeId} onChange={(e) => setScopeId(e.target.value)} placeholder="Scope ID" type="number" min={1} className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          </div>
        </div>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 space-y-3">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Place legal hold</h3>
        <div className="flex flex-wrap gap-2">
          <input value={caseRef} onChange={(e) => setCaseRef(e.target.value)} placeholder="Case reference" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <input value={holdReason} onChange={(e) => setHoldReason(e.target.value)} placeholder="Reason (required)" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <button onClick={placeHold} disabled={place.isPending} className="px-4 py-2 bg-[var(--accent)] text-white rounded text-sm disabled:opacity-50">Place Hold</button>
        </div>
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] p-4 border-b border-[var(--border)]">Active legal holds</h3>
        {!holds || holds.holds.length === 0 ? (
          <p className="p-4 text-sm text-[var(--text-muted)]">No active holds for this scope.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">ID</th><th className="p-3">Scope</th><th className="p-3">Case</th><th className="p-3">Reason</th><th className="p-3">Ends</th><th className="p-3">Actions</th>
              </tr>
            </thead>
            <tbody>
              {holds.holds.map((h) => <HoldRow key={h.id} hold={h} kind={scopeKind} id={sid} />)}
            </tbody>
          </table>
        )}
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-4 space-y-3">
        <h3 className="text-sm font-semibold text-[var(--text-primary)]">Purge (dry-run then confirmed execution)</h3>
        <div className="flex flex-wrap gap-2 items-center">
          <input value={olderThan} onChange={(e) => setOlderThan(e.target.value)} placeholder="Older than (ISO date, default now)" className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
          <button onClick={runPlan} disabled={planPurge.isPending} className="px-4 py-2 bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded text-sm disabled:opacity-50">Plan (dry-run)</button>
        </div>
        {purgePlan && (
          <div className="text-sm text-[var(--text-primary)]">
            Eligible: {purgePlan.eligible} · Blocked by hold: {purgePlan.held}
            <div className="flex flex-wrap gap-2 items-center mt-2">
              <input value={purgeConfirm} onChange={(e) => setPurgeConfirm(e.target.value)} placeholder={PURGE_CONFIRMATION_PHRASE} className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-sm" />
              <button onClick={runPurge} disabled={executePurge.isPending || purgeConfirm !== PURGE_CONFIRMATION_PHRASE} className="px-4 py-2 bg-[var(--danger)] text-white rounded text-sm disabled:opacity-50">
                {executePurge.isPending ? "Purging…" : "Execute Purge"}
              </button>
            </div>
          </div>
        )}
      </div>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg overflow-hidden">
        <h3 className="text-sm font-semibold text-[var(--text-primary)] p-4 border-b border-[var(--border)]">Chain of custody</h3>
        {!custody || custody.events.length === 0 ? (
          <p className="p-4 text-sm text-[var(--text-muted)]">No custody events for this scope.</p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[var(--border)] text-left text-[var(--text-secondary)]">
                <th className="p-3">ID</th><th className="p-3">Operation</th><th className="p-3">Scope</th><th className="p-3">Records</th><th className="p-3">Content hash</th><th className="p-3">Created</th>
              </tr>
            </thead>
            <tbody>
              {custody.events.map((ev) => (
                <tr key={ev.id} className="border-b border-[var(--bg-subtle)]">
                  <td className="p-3 text-[var(--text-primary)]">{ev.id}</td>
                  <td className="p-3 text-[var(--text-primary)]">{ev.operation}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{ev.scope_kind}:{ev.scope_id}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{ev.record_count}</td>
                  <td className="p-3 text-[var(--text-muted)] font-mono text-xs">{ev.content_hash ? ev.content_hash.slice(0, 16) + "…" : "—"}</td>
                  <td className="p-3 text-[var(--text-secondary)]">{new Date(ev.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {feedback && <p className={`text-sm ${feedback.kind === "ok" ? "text-[var(--success)]" : "text-[var(--danger)]"}`}>{feedback.message}</p>}
    </div>
  );
}
