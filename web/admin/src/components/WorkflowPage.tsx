import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { GitBranch, Webhook, FileText, Reply, PlusCircle, Trash2, Play, Eye, Clock } from "lucide-react";
import { api } from "../api";

function Tabs({ tabs, active, onChange }: { tabs: { id: string; label: string }[]; active: string; onChange: (id: string) => void }) {
  return <div className="flex gap-1 border-b border-[var(--border)] mb-5">{tabs.map((t) => <button key={t.id} onClick={() => onChange(t.id)} className={`px-4 py-2.5 text-sm font-medium border-b-2 transition-colors ${active === t.id ? "border-[var(--accent)] text-[var(--accent)]" : "border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]"}`}>{t.label}</button>)}</div>;
}

function SkeletonRow() { return <div className="h-10 bg-[var(--border)] rounded animate-pulse mb-2" />; }

export default function WorkflowPage() {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState("routing");
  const [showAddRule, setShowAddRule] = useState(false);
  const [showAddWebhook, setShowAddWebhook] = useState(false);
  const [showAddTemplate, setShowAddTemplate] = useState(false);
  const [ruleForm, setRuleForm] = useState({ condition: "from", condition_value: "", action: "forward", target: "", priority: 1 });
  const [webhookForm, setWebhookForm] = useState({ name: "", url: "", events: "", secret: "" });
  const [templateForm, setTemplateForm] = useState({ name: "", type: "welcome", subject: "", body: "" });

  const { data: rules, isLoading: rulesLoading } = useQuery({ queryKey: ["routing-rules"], queryFn: () => api.listRoutingRules?.() ?? Promise.resolve([]), enabled: tab === "routing" });
  const { data: webhooks, isLoading: webhooksLoading } = useQuery({ queryKey: ["webhooks"], queryFn: () => api.listWebhooks?.() ?? Promise.resolve([]), enabled: tab === "webhooks" });
  const { data: templates, isLoading: templatesLoading } = useQuery({ queryKey: ["email-templates"], queryFn: () => api.listEmailTemplates?.() ?? Promise.resolve([]), enabled: tab === "templates" });

  const createRule = useMutation({ mutationFn: (d: typeof ruleForm) => api.createRoutingRule?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["routing-rules"] }); setShowAddRule(false); } });
  const deleteRule = useMutation({ mutationFn: (id: number) => api.deleteRoutingRule?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["routing-rules"] }) });
  const createWebhook = useMutation({ mutationFn: (d: typeof webhookForm) => api.createWebhook?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["webhooks"] }); setShowAddWebhook(false); } });
  const deleteWebhook = useMutation({ mutationFn: (id: number) => api.deleteWebhook?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["webhooks"] }) });
  const testWebhook = useMutation({ mutationFn: (id: number) => api.testWebhook?.(id) ?? Promise.resolve() });
  const createTemplate = useMutation({ mutationFn: (d: typeof templateForm) => api.createEmailTemplate?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["email-templates"] }); setShowAddTemplate(false); } });
  const deleteTemplate = useMutation({ mutationFn: (id: number) => api.deleteEmailTemplate?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["email-templates"] }) });

  const ruleList: any[] = Array.isArray(rules) ? rules : [];
  const webhookList: any[] = Array.isArray(webhooks) ? webhooks : [];
  const templateList: any[] = Array.isArray(templates) ? templates : [];

  return (
    <div className="space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">Automation & Workflow</p>
        <h1 className="text-2xl font-bold text-[var(--text-primary)]">Workflow</h1>
        <p className="mt-1 text-sm text-[var(--text-secondary)]">Routing rules, webhooks, templates, auto-responders</p>
      </div>

      <Tabs tabs={[{ id: "routing", label: "Routing Rules" }, { id: "webhooks", label: "Webhooks" }, { id: "templates", label: "Email Templates" }, { id: "autoresponders", label: "Auto-Responders" }]} active={tab} onChange={setTab} />

      {tab === "routing" && (
        <div className="space-y-4">
          <button onClick={() => setShowAddRule(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-3 py-1.5 text-sm"><PlusCircle size={14} /> Add Rule</button>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden">
            <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]">{["Priority","Condition","Action","Target","Actions"].map((h) => <th key={h} className="text-left px-4 py-3 text-[var(--text-muted)] text-xs uppercase tracking-[0.06em]">{h}</th>)}</tr></thead>
              <tbody>{rulesLoading ? Array.from({ length: 3 }).map((_, i) => <tr key={i}><td colSpan={5}><SkeletonRow /></td></tr>) : ruleList.map((r: any) => <tr key={r.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-base)]"><td className="px-4 py-3 text-[var(--text-primary)]">{r.priority}</td><td className="px-4 py-3 text-[var(--text-primary)]">{r.condition} = {r.condition_value}</td><td className="px-4 py-3"><span className="text-xs px-2 py-0.5 rounded-full bg-[var(--accent)]/10 text-[var(--accent)]">{r.action}</span></td><td className="px-4 py-3 text-[var(--text-primary)]">{r.target}</td><td className="px-4 py-3"><button onClick={() => deleteRule.mutate(r.id)} className="text-[var(--text-muted)] hover:text-[var(--status-danger)]"><Trash2 size={14} /></button></td></tr>)}</tbody></table>
          </div>
        </div>
      )}

      {tab === "webhooks" && (
        <div className="space-y-4">
          <button onClick={() => setShowAddWebhook(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-3 py-1.5 text-sm"><PlusCircle size={14} /> Add Webhook</button>
          <div className="grid gap-3">{webhooksLoading ? Array.from({ length: 2 }).map((_, i) => <SkeletonRow key={i} />) : webhookList.map((w: any) => <div key={w.id} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 flex items-center justify-between"><div><p className="text-sm font-medium text-[var(--text-primary)]">{w.name}</p><p className="text-xs text-[var(--text-muted)] mt-1">{w.url?.replace(/(https?:\/\/).{3}/, "$1***")}</p><div className="flex gap-1 mt-2">{(w.events || "").split(",").filter(Boolean).map((e: string) => <span key={e} className="text-[10px] px-1.5 py-0.5 rounded bg-[var(--bg-base)] text-[var(--text-muted)]">{e.trim()}</span>)}</div></div><div className="flex gap-2"><span className={`w-2 h-2 rounded-full ${w.status === "active" ? "bg-[var(--status-success)]" : "bg-[var(--status-danger)]"}`} /><button onClick={() => testWebhook.mutate(w.id)} className="text-sm text-[var(--accent)] hover:underline"><Play size={14} /></button><button onClick={() => deleteWebhook.mutate(w.id)} className="text-[var(--text-muted)] hover:text-[var(--status-danger)]"><Trash2 size={14} /></button></div></div>)}</div>
        </div>
      )}

      {tab === "templates" && (
        <div className="space-y-4">
          <button onClick={() => setShowAddTemplate(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-3 py-1.5 text-sm"><PlusCircle size={14} /> New Template</button>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-3">{templatesLoading ? Array.from({ length: 2 }).map((_, i) => <SkeletonRow key={i} />) : templateList.map((t: any) => <div key={t.id} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4 flex items-center justify-between"><div><p className="text-sm font-medium text-[var(--text-primary)]">{t.name}</p><p className="text-xs text-[var(--text-muted)]">{t.type} · {t.subject}</p></div><div className="flex items-center gap-2"><button onClick={() => {}} className="text-sm text-[var(--accent)] hover:underline"><Eye size={14} /></button><button onClick={() => deleteTemplate.mutate(t.id)} className="text-[var(--text-muted)] hover:text-[var(--status-danger)]"><Trash2 size={14} /></button></div></div>)}</div>
        </div>
      )}

      {tab === "autoresponders" && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-12 text-center">
          <Reply size={48} className="mx-auto text-[var(--text-muted)] opacity-40 mb-4" />
          <h3 className="text-lg font-semibold text-[var(--text-primary)] mb-2">Auto-Responders</h3>
          <p className="text-sm text-[var(--text-secondary)] max-w-md mx-auto">Vacation replies and time-based auto-responders are coming in a future release. You'll be able to set start/end dates, custom messages, and reply frequency.</p>
          <div className="mt-4 inline-flex items-center gap-2 text-xs text-[var(--text-muted)]"><Clock size={14} /> Coming soon</div>
        </div>
      )}

      {/* Modals - Add Rule */}
      {showAddRule && <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => setShowAddRule(false)}><div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-6 w-full max-w-md" onClick={(e) => e.stopPropagation()}><h3 className="text-lg font-semibold text-[var(--text-primary)] mb-4">Add Routing Rule</h3><div className="space-y-3"><input placeholder="Condition value" value={ruleForm.condition_value} onChange={(e) => setRuleForm({ ...ruleForm, condition_value: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /><select value={ruleForm.action} onChange={(e) => setRuleForm({ ...ruleForm, action: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]"><option value="forward">Forward</option><option value="reject">Reject</option><option value="tag">Tag</option><option value="route">Route</option></select><input placeholder="Target" value={ruleForm.target} onChange={(e) => setRuleForm({ ...ruleForm, target: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /></div><div className="flex gap-2 mt-4 justify-end"><button onClick={() => setShowAddRule(false)} className="border border-[var(--border)] text-[var(--text-secondary)] rounded-lg px-4 py-2 text-sm">Cancel</button><button onClick={() => createRule.mutate(ruleForm)} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm">Save</button></div></div></div>}
    </div>
  );
}
