import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ShieldAlert, Loader2, AlertCircle, Trash2, Play, Plus } from "lucide-react";
import { api } from "../api";
import ConfirmDialog from "./ConfirmDialog";

type Tab = "audit" | "ssl" | "antivirus" | "guardian" | "self-heal" | "log-rules";

function Loading() { return <div className="flex items-center justify-center h-32"><Loader2 size={20} className="text-[#4F7CFF] animate-spin" /></div>; }
function ErrorBox({ error }: { error: unknown }) {
  return <div className="bg-[#13161C] border border-[#F87171]/30 rounded-xl p-4 flex items-center gap-3"><AlertCircle size={18} className="text-[#F87171]" /><span className="text-[#F87171] text-sm">{(error as Error)?.message || "Failed to load"}</span></div>;
}
function Empty({ text }: { text: string }) { return <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6 text-center text-[#8B92A8] text-sm">{text}</div>; }

function AuditTab() {
  const q = useQuery<any[]>({ queryKey: ["platform-audit"], queryFn: api.listPlatformAuditLogs });
  const rows = q.data ?? [];
  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No audit events" /> : (
    <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
      <table className="w-full text-sm">
        <thead><tr className="border-b border-[#2A2F3E]"><th className="text-left p-3 text-[#8B92A8]">Actor</th><th className="text-left p-3 text-[#8B92A8]">Action</th><th className="text-left p-3 text-[#8B92A8]">Target</th><th className="text-left p-3 text-[#8B92A8]">Result</th><th className="text-left p-3 text-[#8B92A8]">Time</th></tr></thead>
        <tbody>{rows.map((r: any, i: number) => (
          <tr key={i} className="border-b border-[#2A2F3E]"><td className="p-3 text-[#E8EAF0]">{r.actor}</td><td className="p-3 text-[#8B92A8]">{r.action}</td><td className="p-3 text-[#8B92A8]">{r.target}</td><td className="p-3 text-[#8B92A8]">{r.result}</td><td className="p-3 text-[#555D73]">{r.timestamp}</td></tr>
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
        <div className="text-sm text-[#8B92A8]">
          ACME: <span className="text-[#E8EAF0]">{acmeQ.data?.status ?? (acmeQ.isLoading ? "…" : "unknown")}</span>
          {warningsQ.data?.warnings?.length > 0 && <span className="ml-3 text-[#FBBF24]">{warningsQ.data.warnings.length} expiring soon</span>}
        </div>
        <button disabled={reloadMut.isPending} onClick={() => reloadMut.mutate()} className="px-3 py-1.5 text-xs bg-[#222736] text-[#E8EAF0] rounded disabled:opacity-50">Reload certificates</button>
      </div>
      {certsQ.isLoading ? <Loading /> : certsQ.error ? <ErrorBox error={certsQ.error} /> : certs.length === 0 ? <Empty text="No certificates configured" /> : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><thead><tr className="border-b border-[#2A2F3E]"><th className="text-left p-3 text-[#8B92A8]">Domain</th><th className="text-left p-3 text-[#8B92A8]">Expires</th><th className="text-right p-3 text-[#8B92A8]">Action</th></tr></thead>
          <tbody>{certs.map((c: any) => (
            <tr key={c.id} className="border-b border-[#2A2F3E]"><td className="p-3 text-[#E8EAF0]">{c.domain || c.name}</td><td className="p-3 text-[#8B92A8]">{c.expires_at}</td>
              <td className="p-3 text-right"><button onClick={() => setConfirmDelete(c.id)} className="p-1.5 text-[#8B92A8] hover:text-[#F87171]"><Trash2 size={14} /></button></td></tr>
          ))}</tbody></table>
        </div>
      )}
      <ConfirmDialog open={!!confirmDelete} onOpenChange={(o) => !o && setConfirmDelete(null)} title="Delete certificate" description="This permanently removes the certificate." requireTypedName={confirmDelete || ""} danger pending={deleteMut.isPending} onConfirm={() => confirmDelete && deleteMut.mutate(confirmDelete)} />
    </div>
  );
}

function AntivirusTab() {
  const q = useQuery<any>({ queryKey: ["antivirus-status"], queryFn: api.getAntivirusStatus });
  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : (
    <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4 text-sm">
      <p className="text-[#8B92A8]">Status: <span className="text-[#E8EAF0]">{q.data?.status ?? q.data?.enabled === false ? "disabled" : "unknown"}</span></p>
      {q.data?.engine && <p className="text-[#8B92A8] mt-1">Engine: <span className="text-[#E8EAF0]">{q.data.engine}</span></p>}
    </div>
  );
}

function GuardianTab() {
  const q = useQuery<any[]>({ queryKey: ["guardian-logs"], queryFn: api.listGuardianLogs });
  const rows = q.data ?? [];
  return q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No guardian analysis events" /> : (
    <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
      <table className="w-full text-sm"><tbody>{rows.map((r: any, i: number) => (
        <tr key={i} className="border-b border-[#2A2F3E]"><td className="p-3 text-[#E8EAF0]">{r.subject || r.summary}</td><td className="p-3 text-[#8B92A8]">{r.verdict || r.result}</td></tr>
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
        <select value={name} onChange={(e) => setName(e.target.value)} className="px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-sm text-[#E8EAF0]">
          <option value="database">database</option>
          <option value="disk">disk</option>
        </select>
        <button disabled={runMut.isPending} onClick={() => runMut.mutate(name)} className="flex items-center gap-1 px-3 py-1.5 text-xs bg-[#4F7CFF] text-white rounded disabled:opacity-50"><Play size={12} /> Run check</button>
      </div>
      {q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No self-heal history" /> : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><tbody>{rows.map((r: any, i: number) => (
            <tr key={i} className="border-b border-[#2A2F3E]"><td className="p-3 text-[#E8EAF0]">{r.name}</td><td className="p-3 text-[#8B92A8]">{r.result || r.status}</td><td className="p-3 text-[#555D73]">{r.timestamp}</td></tr>
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
        <input value={newPattern} onChange={(e) => setNewPattern(e.target.value)} placeholder="Log pattern…" className="px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-sm text-[#E8EAF0] flex-1" />
        <button disabled={!newPattern || createMut.isPending} onClick={() => createMut.mutate()} className="flex items-center gap-1 px-3 py-1.5 text-xs bg-[#4F7CFF] text-white rounded disabled:opacity-50"><Plus size={12} /> Add rule</button>
      </div>
      {q.isLoading ? <Loading /> : q.error ? <ErrorBox error={q.error} /> : rows.length === 0 ? <Empty text="No log rules configured" /> : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm"><tbody>{rows.map((r: any) => (
            <tr key={r.id} className="border-b border-[#2A2F3E]"><td className="p-3 text-[#E8EAF0]">{r.pattern}</td><td className="p-3 text-right"><button onClick={() => setConfirmDelete(r.id)} className="p-1.5 text-[#8B92A8] hover:text-[#F87171]"><Trash2 size={14} /></button></td></tr>
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
      <h2 className="text-2xl font-semibold mb-4 text-[#E8EAF0] flex items-center gap-2"><ShieldAlert size={22} className="text-[#4F7CFF]" /> Security</h2>
      <div className="flex gap-1 mb-6 border-b border-[#2A2F3E] flex-wrap">
        {tabs.map((t) => (
          <button key={t.id} onClick={() => setTab(t.id)} className={`px-3 py-2 text-sm border-b-2 ${tab === t.id ? "border-[#4F7CFF] text-[#E8EAF0]" : "border-transparent text-[#8B92A8]"}`}>{t.label}</button>
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
