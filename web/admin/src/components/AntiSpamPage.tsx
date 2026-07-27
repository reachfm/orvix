import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { ShieldBan, MailX, MailCheck, AlertTriangle, PlusCircle, Trash2, LockKeyhole } from "lucide-react";
import { api } from "../api";

function Tabs({ tabs, active, onChange }: { tabs: { id: string; label: string }[]; active: string; onChange: (id: string) => void }) {
  return <div className="flex gap-1 border-b border-[var(--border)] mb-5">{tabs.map((t) => <button key={t.id} onClick={() => onChange(t.id)} className={`px-4 py-2.5 text-sm font-medium border-b-2 transition-colors ${active === t.id ? "border-[var(--accent)] text-[var(--accent)]" : "border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]"}`}>{t.label}</button>)}</div>;
}

export default function AntiSpamPage() {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState("config");
  const [config, setConfig] = useState({ spam_threshold: 5, reject_score: 8, enable_spf: true, enable_dkim: true, enable_dmarc: true, enable_rbl: true, rbl_zones: "", quarantine_spam: true, quarantine_days: 30 });
  const [blEntry, setBlEntry] = useState({ value: "", type: "ip", reason: "" });
  const [wlEntry, setWlEntry] = useState({ value: "", type: "ip", reason: "" });

  const { data: configData } = useQuery({ queryKey: ["spam-config"], queryFn: () => api.getSpamConfig?.() ?? Promise.resolve({}), enabled: tab === "config" });
  const { data: blacklist } = useQuery({ queryKey: ["spam-blacklist"], queryFn: () => api.listSpamBlacklist?.() ?? Promise.resolve([]), enabled: tab === "blacklist" });
  const { data: whitelist } = useQuery({ queryKey: ["spam-whitelist"], queryFn: () => api.listSpamWhitelist?.() ?? Promise.resolve([]), enabled: tab === "whitelist" });

  const saveConfig = useMutation({ mutationFn: (d: typeof config) => api.updateSpamConfig?.(d) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["spam-config"] }) });
  const addBlacklist = useMutation({ mutationFn: (d: typeof blEntry) => api.addSpamBlacklistEntry?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["spam-blacklist"] }); setBlEntry({ value: "", type: "ip", reason: "" }); } });
  const removeBlacklist = useMutation({ mutationFn: (id: number) => api.removeSpamBlacklistEntry?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["spam-blacklist"] }) });
  const addWhitelist = useMutation({ mutationFn: (d: typeof wlEntry) => api.addSpamWhitelistEntry?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["spam-whitelist"] }); setWlEntry({ value: "", type: "ip", reason: "" }); } });
  const removeWhitelist = useMutation({ mutationFn: (id: number) => api.removeSpamWhitelistEntry?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["spam-whitelist"] }) });

  const blList: any[] = Array.isArray(blacklist) ? blacklist : [];
  const wlList: any[] = Array.isArray(whitelist) ? whitelist : [];

  const renderEntryTable = (rows: any[], onDelete: (id: number) => void) => (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden">
      <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]">{["Value","Type","Reason","Added",""].map((h) => <th key={h} className="text-left px-4 py-3 text-[var(--text-muted)] text-xs uppercase tracking-[0.06em]">{h}</th>)}</tr></thead>
        <tbody>{rows.length === 0 ? <tr><td colSpan={5} className="px-4 py-8 text-center text-[var(--text-muted)]">No entries</td></tr> : rows.map((e: any) => <tr key={e.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-base)]"><td className="px-4 py-3 text-[var(--text-primary)]"><code className="text-xs">{e.value}</code></td><td className="px-4 py-3">{e.type}</td><td className="px-4 py-3 text-[var(--text-secondary)]">{e.reason}</td><td className="px-4 py-3 text-xs text-[var(--text-muted)]">{e.added_at}</td><td className="px-4 py-3"><button onClick={() => onDelete(e.id)} className="text-[var(--text-muted)] hover:text-[var(--status-danger)]"><Trash2 size={14} /></button></td></tr>)}</tbody></table>
    </div>
  );

  return (
    <div className="space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">Anti-Spam Gateway</p>
        <h1 className="text-2xl font-bold text-[var(--text-primary)]">Anti-Spam</h1>
        <p className="mt-1 text-sm text-[var(--text-secondary)]">Spam filtering, blacklists, whitelists, quarantine</p>
      </div>

      <Tabs tabs={[{ id: "config", label: "Configuration" }, { id: "blacklist", label: "Blacklist" }, { id: "whitelist", label: "Whitelist" }, { id: "quarantine", label: "Quarantine" }]} active={tab} onChange={setTab} />

      {tab === "config" && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-6 space-y-4 max-w-2xl">
          <div><label className="block text-xs text-[var(--text-secondary)] mb-1">Spam Threshold: {config.spam_threshold}</label><input type="range" min={0} max={10} value={config.spam_threshold} onChange={(e) => setConfig({ ...config, spam_threshold: parseInt(e.target.value) })} className="w-full accent-[var(--accent)]" /></div>
          <div><label className="block text-xs text-[var(--text-secondary)] mb-1">Reject Score: {config.reject_score}</label><input type="range" min={0} max={10} value={config.reject_score} onChange={(e) => setConfig({ ...config, reject_score: parseInt(e.target.value) })} className="w-full accent-[var(--accent)]" /></div>
          <div className="space-y-2">{[{ k: "enable_spf", l: "SPF Validation" },{ k: "enable_dkim", l: "DKIM Validation" },{ k: "enable_dmarc", l: "DMARC Validation" },{ k: "enable_rbl", l: "RBL Lookup" },{ k: "quarantine_spam", l: "Quarantine Spam" }].map(({ k, l }) => <label key={k} className="flex items-center gap-2 text-sm text-[var(--text-secondary)]"><input type="checkbox" checked={(config as any)[k]} onChange={(e) => setConfig({ ...config, [k]: e.target.checked })} className="accent-[var(--accent)]" />{l}</label>)}</div>
          <div><label className="block text-xs text-[var(--text-secondary)] mb-1">RBL Zones</label><textarea value={config.rbl_zones} onChange={(e) => setConfig({ ...config, rbl_zones: e.target.value })} placeholder="zen.spamhaus.org,b.barracudacentral.org" rows={2} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /></div>
          <div><label className="block text-xs text-[var(--text-secondary)] mb-1">Quarantine Days</label><input type="number" value={config.quarantine_days} onChange={(e) => setConfig({ ...config, quarantine_days: parseInt(e.target.value) || 0 })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)] w-24" /></div>
          <button onClick={() => saveConfig.mutate(config)} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-6 py-2.5 text-sm font-medium">Save Configuration</button>
        </div>
      )}

      {tab === "blacklist" && (
        <div className="space-y-4">
          <div className="flex flex-wrap gap-2 bg-[var(--bg-surface)] rounded-xl border border-[var(--border)] p-4">
            <input placeholder="IP / Domain / Email" value={blEntry.value} onChange={(e) => setBlEntry({ ...blEntry, value: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)] flex-1 min-w-[200px]" />
            <select value={blEntry.type} onChange={(e) => setBlEntry({ ...blEntry, type: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]"><option value="ip">IP</option><option value="domain">Domain</option><option value="email">Email</option></select>
            <input placeholder="Reason" value={blEntry.reason} onChange={(e) => setBlEntry({ ...blEntry, reason: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)] w-40" />
            <button onClick={() => addBlacklist.mutate(blEntry)} disabled={!blEntry.value} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm"><PlusCircle size={14} className="inline mr-1" />Add</button>
          </div>
          {renderEntryTable(blList, removeBlacklist.mutate)}
        </div>
      )}

      {tab === "whitelist" && (
        <div className="space-y-4">
          <div className="flex flex-wrap gap-2 bg-[var(--bg-surface)] rounded-xl border border-[var(--border)] p-4">
            <input placeholder="IP / Domain / Email" value={wlEntry.value} onChange={(e) => setWlEntry({ ...wlEntry, value: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)] flex-1 min-w-[200px]" />
            <select value={wlEntry.type} onChange={(e) => setWlEntry({ ...wlEntry, type: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]"><option value="ip">IP</option><option value="domain">Domain</option><option value="email">Email</option></select>
            <input placeholder="Reason" value={wlEntry.reason} onChange={(e) => setWlEntry({ ...wlEntry, reason: e.target.value })} className="bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)] w-40" />
            <button onClick={() => addWhitelist.mutate(wlEntry)} disabled={!wlEntry.value} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm"><PlusCircle size={14} className="inline mr-1" />Add</button>
          </div>
          {renderEntryTable(wlList, removeWhitelist.mutate)}
        </div>
      )}

      {tab === "quarantine" && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-12 text-center">
          <LockKeyhole size={48} className="mx-auto text-[var(--text-muted)] opacity-40 mb-4" />
          <h3 className="text-lg font-semibold text-[var(--text-primary)] mb-2">Quarantine Management</h3>
          <p className="text-sm text-[var(--text-secondary)] max-w-md mx-auto">Quarantine management is coming soon. You will be able to review, release, and delete quarantined messages.</p>
        </div>
      )}
    </div>
  );
}
