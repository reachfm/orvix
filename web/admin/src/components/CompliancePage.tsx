import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ShieldCheck, Gavel, Search, AlertTriangle, Clock, Database, FileSearch } from "lucide-react";
import { api } from "../api";

function Tabs({ tabs, active, onChange }: { tabs: { id: string; label: string }[]; active: string; onChange: (id: string) => void }) {
  return <div className="flex gap-1 border-b border-[var(--border)] mb-5">{tabs.map((t) => <button key={t.id} onClick={() => onChange(t.id)} className={`px-4 py-2.5 text-sm font-medium border-b-2 transition-colors ${active === t.id ? "border-[var(--accent)] text-[var(--accent)]" : "border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]"}`}>{t.label}</button>)}</div>;
}

export default function CompliancePage() {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState("retention");
  const [showRetention, setShowRetention] = useState(false);
  const [showHold, setShowHold] = useState(false);
  const [showDLP, setShowDLP] = useState(false);
  const [retForm, setRetForm] = useState({ name: "", scope: "all", retain_days: 365, action: "delete" });
  const [holdForm, setHoldForm] = useState({ name: "", custodian_email: "", start_date: "", reason: "" });
  const [dlpForm, setDlpForm] = useState({ name: "", pattern: "", action: "block", severity: "medium" });
  const [searchForm, setSearchForm] = useState({ from: "", to: "", keywords: "", domain: "" });

  const { data: retention } = useQuery({ queryKey: ["retention-policies"], queryFn: () => api.listRetentionPolicies?.() ?? Promise.resolve([]), enabled: tab === "retention" });
  const { data: holds } = useQuery({ queryKey: ["legal-holds"], queryFn: () => api.listLegalHolds?.() ?? Promise.resolve([]), enabled: tab === "holds" });
  const { data: dlp } = useQuery({ queryKey: ["dlp-policies"], queryFn: () => api.listDLPPolicies?.() ?? Promise.resolve([]), enabled: tab === "dlp" });

  const createRetention = useMutation({ mutationFn: (d: typeof retForm) => api.createRetentionPolicy?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["retention-policies"] }); setShowRetention(false); } });
  const deleteRetention = useMutation({ mutationFn: (id: number) => api.deleteRetentionPolicy?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["retention-policies"] }) });
  const createHold = useMutation({ mutationFn: (d: typeof holdForm) => api.createLegalHold?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["legal-holds"] }); setShowHold(false); } });
  const releaseHold = useMutation({ mutationFn: (id: number) => api.releaseLegalHold?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["legal-holds"] }) });
  const createDLP = useMutation({ mutationFn: (d: typeof dlpForm) => api.createDLPPolicy?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["dlp-policies"] }); setShowDLP(false); } });
  const deleteDLP = useMutation({ mutationFn: (id: number) => api.deleteDLPPolicy?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["dlp-policies"] }) });
  const ediscoveryMutation = useMutation({ mutationFn: (d: typeof searchForm) => api.runEDiscoverySearch?.(d) ?? Promise.resolve() });

  const retList: any[] = Array.isArray(retention) ? retention : [];
  const holdList: any[] = Array.isArray(holds) ? holds : [];
  const dlpList: any[] = Array.isArray(dlp) ? dlp : [];
  const activeHolds = holdList.filter((h: any) => h.status === "active").length;

  const renderTable = (rows: any[], cols: string[], renderRow: (r: any) => React.ReactNode) => (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden">
      <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]">{cols.map((c) => <th key={c} className="text-left px-4 py-3 text-[var(--text-muted)] text-xs uppercase tracking-[0.06em]">{c}</th>)}</tr></thead><tbody>{rows.length === 0 ? <tr><td colSpan={cols.length} className="px-4 py-8 text-center text-[var(--text-muted)]">No records</td></tr> : rows.map(renderRow)}</tbody></table>
    </div>
  );

  return (
    <div className="space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">Governance & Compliance</p>
        <h1 className="text-2xl font-bold text-[var(--text-primary)]">Compliance</h1>
      </div>

      <Tabs tabs={[{ id: "retention", label: "Retention" }, { id: "holds", label: "Legal Holds" }, { id: "dlp", label: "DLP" }, { id: "ediscovery", label: "eDiscovery" }]} active={tab} onChange={setTab} />

      {tab === "retention" && (
        <div className="space-y-4">
          <button onClick={() => setShowRetention(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-3 py-1.5 text-sm"><Database size={14} /> Add Policy</button>
          {renderTable(retList, ["Name","Scope","Days","Action","Active",""], (r: any) => <tr key={r.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-base)]"><td className="px-4 py-3 text-[var(--text-primary)]">{r.name}</td><td className="px-4 py-3 text-[var(--text-secondary)]">{r.scope}</td><td className="px-4 py-3">{r.retain_days}</td><td className="px-4 py-3"><span className="text-xs px-2 py-0.5 rounded-full bg-[var(--accent)]/10 text-[var(--accent)]">{r.action}</span></td><td className="px-4 py-3">{r.active !== false ? <span className="text-[var(--status-success)]">Active</span> : <span className="text-[var(--status-danger)]">Inactive</span>}</td><td className="px-4 py-3"><button onClick={() => deleteRetention.mutate(r.id)} className="text-[var(--text-muted)] hover:text-[var(--status-danger)]">Delete</button></td></tr>)}
        </div>
      )}

      {tab === "holds" && (
        <div className="space-y-4">
          {activeHolds > 0 && <div className="flex items-center gap-2 rounded-lg border border-[var(--accent-yellow)]/30 bg-[var(--accent-yellow)]/5 p-3 text-sm text-[var(--accent-yellow)]"><AlertTriangle size={16} /> {activeHolds} active legal hold{activeHolds > 1 ? "s" : ""} in effect</div>}
          <button onClick={() => setShowHold(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-3 py-1.5 text-sm"><Gavel size={14} /> Create Hold</button>
          {renderTable(holdList, ["Name","Custodian","Start","Status",""], (h: any) => <tr key={h.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-base)]"><td className="px-4 py-3 text-[var(--text-primary)]">{h.name}</td><td className="px-4 py-3">{h.custodian_email}</td><td className="px-4 py-3 text-[var(--text-secondary)]">{h.start_date}</td><td className="px-4 py-3"><span className={`text-xs px-2 py-0.5 rounded-full ${h.status === "active" ? "bg-[var(--status-success)]/10 text-[var(--status-success)]" : "bg-[var(--text-muted)]/10 text-[var(--text-muted)]"}`}>{h.status}</span></td><td className="px-4 py-3">{h.status === "active" && <button onClick={() => releaseHold.mutate(h.id)} className="text-sm text-[var(--accent)] hover:underline">Release</button>}</td></tr>)}
        </div>
      )}

      {tab === "dlp" && (
        <div className="space-y-4">
          <button onClick={() => setShowDLP(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-3 py-1.5 text-sm"><ShieldCheck size={14} /> Add DLP Policy</button>
          {renderTable(dlpList, ["Name","Pattern","Action","Severity",""], (d: any) => <tr key={d.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-base)]"><td className="px-4 py-3 text-[var(--text-primary)]">{d.name}</td><td className="px-4 py-3"><code className="text-xs text-[var(--accent-blue)]">{d.pattern}</code></td><td className="px-4 py-3"><span className="text-xs px-2 py-0.5 rounded-full bg-[var(--accent)]/10 text-[var(--accent)]">{d.action}</span></td><td className="px-4 py-3"><span className={`text-xs px-2 py-0.5 rounded-full ${d.severity === "critical" ? "bg-[var(--status-danger)]/10 text-[var(--status-danger)]" : d.severity === "high" ? "bg-[var(--accent-yellow)]/10 text-[var(--accent-yellow)]" : "bg-[var(--text-muted)]/10 text-[var(--text-muted)]"}`}>{d.severity}</span></td><td className="px-4 py-3"><button onClick={() => deleteDLP.mutate(d.id)} className="text-[var(--text-muted)] hover:text-[var(--status-danger)]">Delete</button></td></tr>)}
        </div>
      )}

      {tab === "ediscovery" && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-6 space-y-4">
          <h3 className="text-sm font-semibold text-[var(--text-primary)]">eDiscovery Search</h3>
          <div className="grid grid-cols-2 gap-3">
            <input type="date" value={searchForm.from} onChange={(e) => setSearchForm({ ...searchForm, from: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
            <input type="date" value={searchForm.to} onChange={(e) => setSearchForm({ ...searchForm, to: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
            <input placeholder="Keywords" value={searchForm.keywords} onChange={(e) => setSearchForm({ ...searchForm, keywords: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
            <input placeholder="Domain scope" value={searchForm.domain} onChange={(e) => setSearchForm({ ...searchForm, domain: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
          </div>
          <button onClick={() => ediscoveryMutation.mutate(searchForm)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm"><Search size={14} /> Run Search</button>
          {ediscoveryMutation.data && <div className="text-sm text-[var(--text-secondary)]">{ediscoveryMutation.data?.count || 0} results found</div>}
        </div>
      )}

      {/* Modal: Retention */}
      {showRetention && <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => setShowRetention(false)}><div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-6 w-full max-w-md" onClick={(e) => e.stopPropagation()}><h3 className="text-lg font-semibold mb-4">Add Retention Policy</h3><div className="space-y-3"><input placeholder="Name" value={retForm.name} onChange={(e) => setRetForm({ ...retForm, name: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /><select value={retForm.scope} onChange={(e) => setRetForm({ ...retForm, scope: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]"><option value="all">All</option><option value="domain">Domain</option><option value="mailbox">Mailbox</option></select><input type="number" placeholder="Days" value={retForm.retain_days} onChange={(e) => setRetForm({ ...retForm, retain_days: parseInt(e.target.value) || 0 })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /></div><div className="flex gap-2 mt-4 justify-end"><button onClick={() => setShowRetention(false)} className="border border-[var(--border)] text-[var(--text-secondary)] rounded-lg px-4 py-2 text-sm">Cancel</button><button onClick={() => createRetention.mutate(retForm)} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm">Save</button></div></div></div>}
    </div>
  );
}
