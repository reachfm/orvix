import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Package, PlusCircle, Edit, Trash2, Check, X, Zap } from "lucide-react";
import { api } from "../api";

const PLAN_COLORS: Record<string, string> = {
  starter: "text-[var(--accent-blue)] bg-[var(--accent-blue)]/10 border-[var(--accent-blue)]/30",
  business: "text-[var(--status-success)] bg-[var(--status-success)]/10 border-[var(--status-success)]/30",
  enterprise: "text-[var(--accent-yellow)] bg-[var(--accent-yellow)]/10 border-[var(--accent-yellow)]/30",
  custom: "text-purple-400 bg-purple-400/10 border-purple-400/30",
};

function SkeletonCard() { return <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5 animate-pulse"><div className="h-4 bg-[var(--border)] rounded w-1/3 mb-4" /><div className="h-8 bg-[var(--border)] rounded w-1/2 mb-3" /><div className="h-3 bg-[var(--border)] rounded w-3/4 mb-2" /><div className="h-3 bg-[var(--border)] rounded w-2/3" /></div>; }

export default function PackagesAdminPage() {
  const queryClient = useQueryClient();
  const [showCreate, setShowCreate] = useState(false);
  const [editPlan, setEditPlan] = useState<any>(null);
  const [form, setForm] = useState({ name: "", description: "", price_monthly: 0, price_yearly: 0, max_domains: 1, max_mailboxes: 5, max_storage_gb: 1, features: "", is_trial: false });

  const { data: plans, isLoading } = useQuery({ queryKey: ["platform-plans"], queryFn: () => api.listPlatformPlans?.() ?? api.getPlans?.() ?? Promise.resolve([]) });

  const createMutation = useMutation({
    mutationFn: (data: typeof form) => api.createPlatformPlan?.(data) ?? Promise.resolve(),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["platform-plans"] }); setShowCreate(false); resetForm(); },
  });
  const updateMutation = useMutation({
    mutationFn: (data: any) => api.updatePlatformPlan(data.id, data) ?? Promise.resolve(),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["platform-plans"] }); setEditPlan(null); resetForm(); },
  });
  const deleteMutation = useMutation({ mutationFn: (id: number) => api.deletePlatformPlan(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["platform-plans"] }) });
  const toggleMutation = useMutation({ mutationFn: (data: any) => api.togglePlanStatus(data.id, data.active === false) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["platform-plans"] }) });

  const resetForm = () => setForm({ name: "", description: "", price_monthly: 0, price_yearly: 0, max_domains: 1, max_mailboxes: 5, max_storage_gb: 1, features: "", is_trial: false });

  const openEdit = (p: any) => { setEditPlan(p); setForm({ name: p.name, description: p.description || "", price_monthly: p.price_monthly || 0, price_yearly: p.price_yearly || 0, max_domains: p.max_domains || 1, max_mailboxes: p.max_mailboxes || 5, max_storage_gb: (p.storage_mb || 0) / 1024, features: p.features || "", is_trial: p.is_trial || false }); };

  const planList: any[] = Array.isArray(plans) ? plans : plans?.plans || [];
  const tier = (name: string) => name?.toLowerCase().includes("enterprise") ? "enterprise" : name?.toLowerCase().includes("business") ? "business" : name?.toLowerCase().includes("starter") ? "starter" : "custom";

  return (
    <div className="space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">Pricing & Plans</p>
        <h1 className="text-2xl font-bold text-[var(--text-primary)]">Packages</h1>
        <p className="mt-1 text-sm text-[var(--text-secondary)]">Subscription plan management</p>
      </div>

      <button onClick={() => setShowCreate(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm font-medium hover:opacity-90"><PlusCircle size={16} /> Create Plan</button>

      <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
        {isLoading ? Array.from({ length: 4 }).map((_, i) => <SkeletonCard key={i} />) : planList.map((p: any) => {
          const color = PLAN_COLORS[tier(p.name || p.id)] || PLAN_COLORS.custom;
          return (
            <div key={p.id} className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-5 relative">
              <div className="flex items-center justify-between mb-3">
                <span className={`text-[11px] px-2 py-0.5 rounded-full border ${color}`}>{tier(p.name || p.id)}</span>
                <div className="flex gap-1">
                  <button onClick={() => openEdit(p)} className="p-1 text-[var(--text-muted)] hover:text-[var(--text-primary)]"><Edit size={14} /></button>
                  <button onClick={() => deleteMutation.mutate(p.id)} className="p-1 text-[var(--text-muted)] hover:text-[var(--status-danger)]"><Trash2 size={14} /></button>
                </div>
              </div>
              <h3 className="text-lg font-bold text-[var(--text-primary)]">{p.name || p.id}</h3>
              <p className="text-3xl font-bold text-[var(--text-primary)] mt-2">${((p.price_monthly || 0) / 100).toFixed(0)}<span className="text-sm text-[var(--text-muted)]">/mo</span></p>
              <div className="mt-4 space-y-1.5 text-sm">
                <p className="text-[var(--text-secondary)]">{p.max_domains || 1} domains</p>
                <p className="text-[var(--text-secondary)]">{p.max_mailboxes || 5} mailboxes</p>
                <p className="text-[var(--text-secondary)]">{(p.storage_mb || 1024) / 1024} GB storage</p>
              </div>
              <div className="flex flex-wrap gap-1 mt-3">
                {(typeof p.features === "string" ? p.features.split(",") : p.features || []).map((f: string) => <span key={f} className="text-[10px] px-2 py-0.5 rounded bg-[var(--bg-base)] text-[var(--text-muted)]">{f.trim()}</span>)}
              </div>
              <button onClick={() => toggleMutation.mutate({ id: p.id, active: p.active === false })} className={`mt-4 text-xs flex items-center gap-1 ${p.active !== false ? "text-[var(--status-success)]" : "text-[var(--status-danger)]"}`}>{p.active !== false ? <Check size={12} /> : <X size={12} />}{p.active !== false ? "Active" : "Inactive"}</button>
            </div>
          );
        })}
      </div>

      {(showCreate || editPlan) && (
        <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => { setShowCreate(false); setEditPlan(null); }}>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-6 w-full max-w-lg mx-4 max-h-[90vh] overflow-auto" onClick={(e) => e.stopPropagation()}>
            <h3 className="text-lg font-semibold text-[var(--text-primary)] mb-4">{editPlan ? "Edit Plan" : "Create Plan"}</h3>
            <div className="space-y-3">
              <input placeholder="Name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
              <input placeholder="Description" value={form.description} onChange={(e) => setForm({ ...form, description: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
              <div className="grid grid-cols-2 gap-3">
                <input type="number" placeholder="Price/mo (cents)" value={form.price_monthly} onChange={(e) => setForm({ ...form, price_monthly: parseInt(e.target.value) || 0 })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
                <input type="number" placeholder="Price/yr (cents)" value={form.price_yearly} onChange={(e) => setForm({ ...form, price_yearly: parseInt(e.target.value) || 0 })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
                <input type="number" placeholder="Max Domains" value={form.max_domains} onChange={(e) => setForm({ ...form, max_domains: parseInt(e.target.value) || 1 })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
                <input type="number" placeholder="Max Mailboxes" value={form.max_mailboxes} onChange={(e) => setForm({ ...form, max_mailboxes: parseInt(e.target.value) || 5 })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
              </div>
              <input type="number" placeholder="Max Storage (GB)" value={form.max_storage_gb} onChange={(e) => setForm({ ...form, max_storage_gb: parseInt(e.target.value) || 1 })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
              <textarea placeholder="Features (comma-separated)" value={form.features} onChange={(e) => setForm({ ...form, features: e.target.value })} rows={3} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" />
              <label className="flex items-center gap-2 text-sm text-[var(--text-secondary)]"><input type="checkbox" checked={form.is_trial} onChange={(e) => setForm({ ...form, is_trial: e.target.checked })} className="accent-[var(--accent)]" /> Trial plan</label>
            </div>
            <div className="flex gap-2 mt-4 justify-end">
              <button onClick={() => { setShowCreate(false); setEditPlan(null); }} className="border border-[var(--border)] text-[var(--text-secondary)] rounded-lg px-4 py-2 text-sm">Cancel</button>
              <button onClick={() => editPlan ? updateMutation.mutate({ ...form, id: editPlan.id }) : createMutation.mutate(form)} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm">Save</button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
