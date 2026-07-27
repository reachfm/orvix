import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Building2, Users, Palette, Save, PlusCircle, Edit, Trash2, UploadCloud, Paintbrush } from "lucide-react";
import { api } from "../api";

function Tabs({ tabs, active, onChange }: { tabs: { id: string; label: string }[]; active: string; onChange: (id: string) => void }) {
  return <div className="flex gap-1 border-b border-[var(--border)] mb-5">{tabs.map((t) => <button key={t.id} onClick={() => onChange(t.id)} className={`px-4 py-2.5 text-sm font-medium border-b-2 transition-colors ${active === t.id ? "border-[var(--accent)] text-[var(--accent)]" : "border-transparent text-[var(--text-muted)] hover:text-[var(--text-secondary)]"}`}>{t.label}</button>)}</div>;
}

export default function CompanyPage() {
  const queryClient = useQueryClient();
  const [tab, setTab] = useState("profile");
  const [profile, setProfile] = useState({ company_name: "", tagline: "", website: "", support_email: "", admin_email: "", timezone: "", country: "", address: "" });
  const [showDept, setShowDept] = useState(false);
  const [editDept, setEditDept] = useState<any>(null);
  const [deptForm, setDeptForm] = useState({ name: "", description: "", head_email: "" });

  const { data: profileData } = useQuery({ queryKey: ["platform-profile"], queryFn: () => api.getPlatformProfile?.() ?? Promise.resolve({}), enabled: tab === "profile" });
  const { data: departments } = useQuery<any[]>({ queryKey: ["departments"], queryFn: () => api.listDepartments(), enabled: tab === "departments" });

  const saveProfile = useMutation({ mutationFn: (d: typeof profile) => api.updatePlatformProfile?.(d) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["platform-profile"] }) });
  const createDept = useMutation({ mutationFn: (d: typeof deptForm) => api.createDepartment?.(d) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["departments"] }); setShowDept(false); resetDept(); } });
  const updateDept = useMutation({ mutationFn: (d: any) => api.updateDepartment(d.id, { name: d.name, description: d.description, head_email: d.head_email }) ?? Promise.resolve(), onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["departments"] }); setEditDept(null); resetDept(); } });
  const deleteDept = useMutation({ mutationFn: (id: number) => api.deleteDepartment?.(id) ?? Promise.resolve(), onSuccess: () => queryClient.invalidateQueries({ queryKey: ["departments"] }) });

  const resetDept = () => setDeptForm({ name: "", description: "", head_email: "" });
  const openEditDept = (d: any) => { setEditDept(d); setDeptForm({ name: d.name, description: d.description || "", head_email: d.head_email || "" }); };
  const deptList: any[] = Array.isArray(departments) ? departments : [];

  return (
    <div className="space-y-6">
      <div>
        <p className="text-xs font-semibold uppercase tracking-[0.14em] text-[var(--accent)] mb-1">Organization Settings</p>
        <h1 className="text-2xl font-bold text-[var(--text-primary)]">Company</h1>
        <p className="mt-1 text-sm text-[var(--text-secondary)]">Profile, departments, branding</p>
      </div>

      <Tabs tabs={[{ id: "profile", label: "Company Profile" }, { id: "departments", label: "Departments" }, { id: "branding", label: "Branding" }]} active={tab} onChange={setTab} />

      {tab === "profile" && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-6 space-y-4 max-w-2xl">
          <div className="flex items-center gap-4 mb-4">
            <div className="w-16 h-16 rounded-xl bg-[var(--bg-base)] border border-[var(--border)] flex items-center justify-center text-[var(--text-muted)]"><Building2 size={28} /></div>
            <div><button className="text-sm text-[var(--accent)] hover:underline inline-flex items-center gap-1"><UploadCloud size={14} /> Upload Logo</button></div>
          </div>
          <div className="grid grid-cols-2 gap-3">
            {[{ k: "company_name", l: "Company Name" },{ k: "tagline", l: "Tagline" },{ k: "website", l: "Website" },{ k: "support_email", l: "Support Email" },{ k: "admin_email", l: "Admin Email" },{ k: "timezone", l: "Timezone" },{ k: "country", l: "Country" }].map(({ k, l }) => <div key={k}><label className="block text-xs text-[var(--text-secondary)] mb-1">{l}</label><input value={(profile as any)[k]} onChange={(e) => setProfile({ ...profile, [k]: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /></div>)}
          </div>
          <div><label className="block text-xs text-[var(--text-secondary)] mb-1">Address</label><textarea value={profile.address} onChange={(e) => setProfile({ ...profile, address: e.target.value })} rows={2} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /></div>
          <button onClick={() => saveProfile.mutate(profile)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-6 py-2.5 text-sm"><Save size={14} /> Save Profile</button>
        </div>
      )}

      {tab === "departments" && (
        <div className="space-y-4">
          <button onClick={() => setShowDept(true)} className="inline-flex items-center gap-2 bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm"><PlusCircle size={14} /> Add Department</button>
          <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] overflow-hidden">
            <table className="w-full text-sm"><thead><tr className="border-b border-[var(--border)]">{["Name","Description","Head","Members","Created",""].map((h) => <th key={h} className="text-left px-4 py-3 text-[var(--text-muted)] text-xs uppercase tracking-[0.06em]">{h}</th>)}</tr></thead>
              <tbody>{deptList.length === 0 ? <tr><td colSpan={6} className="px-4 py-8 text-center text-[var(--text-muted)]">No departments</td></tr> : deptList.map((d: any) => <tr key={d.id} className="border-b border-[var(--border)] hover:bg-[var(--bg-base)]"><td className="px-4 py-3 text-[var(--text-primary)]">{d.name}</td><td className="px-4 py-3 text-[var(--text-secondary)]">{d.description}</td><td className="px-4 py-3">{d.head_email}</td><td className="px-4 py-3">{d.member_count || 0}</td><td className="px-4 py-3 text-xs text-[var(--text-muted)]">{d.created_at?.slice(0, 10)}</td><td className="px-4 py-3"><div className="flex gap-1"><button onClick={() => openEditDept(d)} className="text-[var(--text-muted)] hover:text-[var(--text-primary)]"><Edit size={14} /></button><button onClick={() => deleteDept.mutate(d.id)} className="text-[var(--text-muted)] hover:text-[var(--status-danger)]"><Trash2 size={14} /></button></div></td></tr>)}</tbody></table>
          </div>
        </div>
      )}

      {tab === "branding" && (
        <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-12 text-center">
          <Paintbrush size={48} className="mx-auto text-[var(--text-muted)] opacity-40 mb-4" />
          <h3 className="text-lg font-semibold text-[var(--text-primary)] mb-2">Branding Studio</h3>
          <p className="text-sm text-[var(--text-secondary)] max-w-md mx-auto mb-6">Customize accent colors, upload your company logo, and preview email templates. Full branding customization is coming soon.</p>
          <div className="inline-flex gap-3">
            <div className="w-10 h-10 rounded-lg bg-[var(--accent)] cursor-pointer" title="Accent color preview" />
            <div className="w-10 h-10 rounded-lg bg-[var(--accent-blue)] cursor-pointer" />
            <div className="w-10 h-10 rounded-lg bg-[var(--status-success)] cursor-pointer" />
            <div className="w-10 h-10 rounded-lg bg-[var(--accent-yellow)] cursor-pointer" />
            <div className="w-10 h-10 rounded-lg bg-[var(--status-danger)] cursor-pointer" />
          </div>
        </div>
      )}

      {(showDept || editDept) && <div className="fixed inset-0 z-50 bg-black/50 flex items-center justify-center" onClick={() => { setShowDept(false); setEditDept(null); }}><div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-6 w-full max-w-md" onClick={(e) => e.stopPropagation()}><h3 className="text-lg font-semibold mb-4">{editDept ? "Edit" : "Add"} Department</h3><div className="space-y-3"><input placeholder="Name" value={deptForm.name} onChange={(e) => setDeptForm({ ...deptForm, name: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /><input placeholder="Description" value={deptForm.description} onChange={(e) => setDeptForm({ ...deptForm, description: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /><input placeholder="Head Email" value={deptForm.head_email} onChange={(e) => setDeptForm({ ...deptForm, head_email: e.target.value })} className="w-full bg-[var(--bg-base)] border border-[var(--border)] rounded-lg px-3 py-2 text-sm text-[var(--text-primary)]" /></div><div className="flex gap-2 mt-4 justify-end"><button onClick={() => { setShowDept(false); setEditDept(null); }} className="border border-[var(--border)] text-[var(--text-secondary)] rounded-lg px-4 py-2 text-sm">Cancel</button><button onClick={() => editDept ? updateDept.mutate({ ...deptForm, id: editDept.id }) : createDept.mutate(deptForm)} className="bg-[var(--accent)] text-[var(--bg-base)] rounded-lg px-4 py-2 text-sm">Save</button></div></div></div>}
    </div>
  );
}
