import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ShieldAlert, Loader2, AlertCircle, Trash2, Play, Plus } from "lucide-react";
import { api } from "../api";
import ConfirmDialog from "./ConfirmDialog";

type Tab = "audit" | "ssl" | "antivirus" | "guardian" | "self-heal" | "log-rules";

function Loading() { return <div className="flex items-center justify-center h-32"><Loader2 size={20} className="text-[var(--accent)] animate-spin" /></div>; }
function ErrorBox({ error }: { error: unknown }) {
  return <div className="bg-[var(--bg-surface)] border border-[var(--danger)]/30 rounded-xl p-4 flex items-center gap-3"><AlertCircle size={18} className="text-[var(--danger)]" /><span className="text-[var(--danger)] text-sm">{(error as Error)?.message || "Failed to load"}</span></div>;
}
function Empty({ text }: { text: string }) { return <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl p-6 text-center text-[var(--text-secondary)] text-sm">{text}</div>; }

function AuditTab() {
  const q = useQuery<any[]>({ queryKey: ["platform-audit"], queryFn: api.listPlatformAuditLogs });
  const rows = q.data ?? [];
  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No audit events" /> : (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
      <table className="w-full text-sm">
        <thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Actor</th><th className="text-left p-3 text-[var(--text-secondary)]">Action</th><th className="text-left p-3 text-[var(--text-secondary)]">Target</th><th className="text-left p-3 text-[var(--text-secondary)]">Result</th><th className="text-left p-3 text-[var(--text-secondary)]">Time</th></tr></thead>
        <tbody>{rows.map((r: any, i: number) => (
          <tr key={i} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{r.actor}</td><td className="p-3 text-[var(--text-secondary)]">{r.action}</td><td className="p-3 text-[var(--text-secondary)]">{r.target}</td><td className="p-3 text-[var(--text-secondary)]">{r.result}</td><td className="p-3 text-[var(--text-muted)]">{r.timestamp}</td></tr>
        ))}</tbody>
      </table>
    </div>
  );
}

function SslTab() {
  const qc = useQueryClient();
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const certsQ = useQuery<any[]>({ queryKey: ["ssl-certs"], queryFn: api.listSslCertificates });
  const warningsQ = useQuery<any>({ queryKey: ["ssl-warnings"], queryFn: api.getSslExpiryWarnings });
  const acmeQ = useQuery<any>({ queryKey: ["acme-status"], queryFn: api.getAcmeStatus });
  const reloadMut = useMutation({ mutationFn: () => api.reloadSslCertificates(), onSuccess: () => qc.invalidateQueries({ queryKey: ["ssl-certs"] }) });
  const deleteMut = useMutation({ mutationFn: (id: string) => api.deleteSslCertificate(id), onSuccess: () => { qc.invalidateQueries({ queryKey: ["ssl-certs"] }); setConfirmDelete(null); } });
  const certs = certsQ.data ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div className="text-sm text-[var(--text-secondary)]">
          ACME: <span className="text-[var(--text-primary)]">{acmeQ.data?.status ?? (acmeQ.isLoading ? "…" : "unknown")}</span>
          {warningsQ.data?.warnings?.length > 0 && <span className="ml-3 text-[var(--warning)]">{warningsQ.data.warnings.length} expiring soon</span>}
        </div>
        <button disabled={reloadMut.isPending} onClick={() => reloadMut.mutate()} className="px-3 py-1.5 text-xs bg-[var(--bg-subtle)] text-[var(--text-primary)] rounded disabled:opacity-50">Reload certificates</button>
      </div>
      {certsQ.isLoading ? <Loading /> : certsQ.error ? <ErrorBox error={certsQ.error} /> : certs.length === 0 ? <Empty text="No certificates configured" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]"><th className="text-left p-3 text-[var(--text-secondary)]">Domain</th><th className="text-left p-3 text-[var(--text-secondary)]">Expires</th><th className="text-right p-3 text-[var(--text-secondary)]">Action</th></tr></thead>
          <tbody>{certs.map((c: any) => (
            <tr key={c.id} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{c.domain || c.name}</td><td className="p-3 text-[var(--text-secondary)]">{c.expires_at}</td>
              <td className="p-3 text-right"><button onClick={() => setConfirmDelete(c.id)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]"><Trash2 size={14} /></button></td></tr>
          ))}</tbody></table>
        </div>
      )}
      <ConfirmDialog open={!!confirmDelete} onOpenChange={(o) => !o && setConfirmDelete(null)} title="Delete certificate" description="This permanently removes the certificate." requireTypedName={confirmDelete || ""} danger pending={deleteMut.isPending} onConfirm={() => confirmDelete && deleteMut.mutate(confirmDelete)} />
    </div>
  );
}

// Matches AdminAntivirusStatus's exact response
// (internal/api/handlers/enterprise_admin_v3.go) field for field —
// there is no "status" or "enabled" field on the wire; the previous
// version of this component read those two nonexistent fields through
// an operator-precedence bug (`a ?? b === c ? d : e` parses as
// `(a ?? (b === c)) ? d : e`), which meant ANY truthy value collapsed
// to the literal string "disabled" and the real status was never
// shown. engine_reachable (the daemon PING probe) and runtime_enforced
// (whether the SMTP receiver actually calls the engine) are
// independent per the handler's own "honest_notes" and must be shown
// as two distinct facts, not collapsed into one status word.
interface AntivirusStatus {
  engine: string;
  engine_configured: boolean;
  engine_reachable: boolean;
  engine_active: boolean;
  runtime_enforced: boolean;
  clamav_host: string;
  clamav_port: number;
  clamav_response: string;
  policy_on_infected: string;
  policy_on_scanner_unavailable: string;
  last_error: string;
  counts: {
    scanned: number;
    infected: number;
    rejected: number;
    quarantined: number;
    tagged: number;
    fail_open: number;
    fail_closed: number;
  };
  honest_notes: string[];
}

function StatusPill({ ok, onLabel, offLabel }: { ok: boolean; onLabel: string; offLabel: string }) {
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${ok ? "bg-[var(--success)]/10 text-[var(--success)]" : "bg-[var(--danger)]/10 text-[var(--danger)]"}`}>
      {ok ? onLabel : offLabel}
    </span>
  );
}

export function AntivirusTab() {
  const q = useQuery<AntivirusStatus>({ queryKey: ["antivirus-status"], queryFn: api.getAntivirusStatus });
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

function GuardianTab() {
  const q = useQuery<any[]>({ queryKey: ["guardian-logs"], queryFn: api.listGuardianLogs });
  const rows = q.data ?? [];
  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No guardian analysis events" /> : (
    <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
      <table className="w-full text-sm"><tbody>{rows.map((r: any, i: number) => (
        <tr key={i} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{r.subject || r.summary}</td><td className="p-3 text-[var(--text-secondary)]">{r.verdict || r.result}</td></tr>
      ))}</tbody></table>
    </div>
  );
}

function SelfHealTab() {
  const qc = useQueryClient();
  const q = useQuery<any[]>({ queryKey: ["heal-history"], queryFn: api.listHealHistory });
  const [name, setName] = useState("database");
  const runMut = useMutation({ mutationFn: (n: string) => api.runHealCheck(n), onSuccess: () => qc.invalidateQueries({ queryKey: ["heal-history"] }) });
  const rows = q.data ?? [];
  return (
    <div>
      <div className="flex items-center gap-2 mb-4">
        <select value={name} onChange={(e) => setName(e.target.value)} className="px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)]">
          <option value="database">database</option>
          <option value="disk">disk</option>
        </select>
        <button disabled={runMut.isPending} onClick={() => runMut.mutate(name)} className="flex items-center gap-1 px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-50"><Play size={12} /> Run check</button>
      </div>
      {q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No self-heal history" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><tbody>{rows.map((r: any, i: number) => (
            <tr key={i} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{r.name}</td><td className="p-3 text-[var(--text-secondary)]">{r.result || r.status}</td><td className="p-3 text-[var(--text-muted)]">{r.timestamp}</td></tr>
          ))}</tbody></table>
        </div>
      )}
    </div>
  );
}

function LogRulesTab() {
  const qc = useQueryClient();
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [newPattern, setNewPattern] = useState("");
  const q = useQuery<any[]>({ queryKey: ["log-rules"], queryFn: api.listLogRules });
  const createMut = useMutation({
    mutationFn: () => api.createLogRule({ pattern: newPattern }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["log-rules"] }); setNewPattern(""); },
  });
  const deleteMut = useMutation({ mutationFn: (id: string) => api.deleteLogRule(id), onSuccess: () => { qc.invalidateQueries({ queryKey: ["log-rules"] }); setConfirmDelete(null); } });
  const rows = q.data ?? [];

  return (
    <div>
      <div className="flex gap-2 mb-4">
        <input value={newPattern} onChange={(e) => setNewPattern(e.target.value)} placeholder="Log pattern…" className="px-3 py-2 bg-[var(--bg-elevated)] border border-[var(--border)] rounded text-sm text-[var(--text-primary)] flex-1" />
        <button disabled={!newPattern || createMut.isPending} onClick={() => createMut.mutate()} className="flex items-center gap-1 px-3 py-1.5 text-xs bg-[var(--accent)] text-white rounded disabled:opacity-50"><Plus size={12} /> Add rule</button>
      </div>
      {q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No log rules configured" /> : (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><tbody>{rows.map((r: any) => (
            <tr key={r.id} className="border-b border-[var(--border)]"><td className="p-3 text-[var(--text-primary)]">{r.pattern}</td><td className="p-3 text-right"><button onClick={() => setConfirmDelete(r.id)} className="p-1.5 text-[var(--text-secondary)] hover:text-[var(--danger)]"><Trash2 size={14} /></button></td></tr>
          ))}</tbody></table>
        </div>
      )}
      <ConfirmDialog open={!!confirmDelete} onOpenChange={(o) => !o && setConfirmDelete(null)} title="Delete log rule" description="This permanently removes the log rule." requireTypedName={confirmDelete || ""} danger pending={deleteMut.isPending} onConfirm={() => confirmDelete && deleteMut.mutate(confirmDelete)} />
    </div>
  );
}

export default function PlatformSecurity() {
  const [tab, setTab] = useState<Tab>("audit");
  const tabs: { id: Tab; label: string }[] = [
    { id: "audit", label: "Audit Log" },
    { id: "ssl", label: "SSL / ACME" },
    { id: "antivirus", label: "Antivirus" },
    { id: "guardian", label: "Guardian" },
    { id: "self-heal", label: "Self-Heal" },
    { id: "log-rules", label: "Log Rules" },
  ];
  return (
    <div>
      <h2 className="text-2xl font-semibold mb-4 text-[var(--text-primary)] flex items-center gap-2"><ShieldAlert size={22} className="text-[var(--accent)]" /> Security</h2>
      <div className="flex gap-1 mb-6 border-b border-[var(--border)] flex-wrap">
        {tabs.map((t) => (
          <button key={t.id} onClick={() => setTab(t.id)} className={`px-3 py-2 text-sm border-b-2 ${tab === t.id ? "border-[var(--accent)] text-[var(--text-primary)]" : "border-transparent text-[var(--text-secondary)]"}`}>{t.label}</button>
        ))}
      </div>
      {tab === "audit" && <AuditTab />}
      {tab === "ssl" && <SslTab />}
      {tab === "antivirus" && <AntivirusTab />}
      {tab === "guardian" && <GuardianTab />}
      {tab === "self-heal" && <SelfHealTab />}
      {tab === "log-rules" && <LogRulesTab />}
    </div>
  );
}
