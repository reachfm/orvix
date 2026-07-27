import { useState, useEffect, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Mail, PlusCircle, AlertTriangle } from "lucide-react";
import { api } from "../api";
import PageHeader from "./ui/PageHeader";
import FilterBar from "./ui/FilterBar";
import DataTable from "./ui/DataTable";
import Pagination from "./ui/Pagination";
import Drawer from "./ui/Drawer";
import Dialog from "./ui/Dialog";
import Badge from "./ui/Badge";
import Button from "./ui/Button";
import EmptyState from "./ui/EmptyState";
import ErrorBanner from "./ui/ErrorBanner";
import { useToast } from "./ui/Toast";
import type { Mailbox } from "../types/mailboxes";

function QuotaBar({ used, total }: { used?: number; total: number }) {
  const pct = total > 0 ? Math.min(100, Math.round(((used || 0) / total) * 100)) : 0;
  const color = pct > 80 ? "var(--status-danger)" : pct > 60 ? "var(--status-warning)" : "var(--accent)";
  return (
    <div className="flex items-center gap-2 min-w-[120px]">
      <div className="flex-1 h-2 rounded-full bg-[var(--bg-subtle)] overflow-hidden">
        <div className="h-full rounded-full transition-all" style={{ width: `${pct}%`, background: color }} />
      </div>
      <span className="text-xs text-[var(--text-muted)] w-16 text-right">{used || 0}/{total} MB</span>
    </div>
  );
}

export default function MailboxList() {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const [selectedMailbox, setSelectedMailbox] = useState<Mailbox | null>(null);
  const [showSuspend, setShowSuspend] = useState(false);
  const [showDelete, setShowDelete] = useState(false);
  const [deleteConfirmEmail, setDeleteConfirmEmail] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [createForm, setCreateForm] = useState({ email: "", name: "", password: "", quota_mb: 1024 });

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => { setDebouncedSearch(search); }, 300);
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current); };
  }, [search]);

  const { data: listData, isLoading, isError: listError, error: listErr } = useQuery({
    queryKey: ["admin-mailboxes", debouncedSearch, statusFilter],
    queryFn: async () => {
      const raw = await api.listPlatformMailboxes({ q: debouncedSearch || undefined, status: statusFilter || undefined });
      const mbs: Mailbox[] = Array.isArray(raw) ? raw : raw?.mailboxes || [];
      return { mailboxes: mbs, total: mbs.length };
    },
  });

  const mailboxes = listData?.mailboxes || [];
  const total = listData?.total || 0;

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: number; status: string }) => api.updateAdminMailboxStatus(id, status),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin-mailboxes"] });
      setShowSuspend(false);
      if (selectedMailbox) setSelectedMailbox({ ...selectedMailbox, status: statusFilter === "suspended" ? "active" : "suspended" });
      toast({ message: "Mailbox status updated.", variant: "success" });
    },
    onError: (err: any) => toast({ message: err?.message || "Failed to update", variant: "danger" }),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deleteAdminMailbox(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin-mailboxes"] });
      setShowDelete(false);
      setDeleteConfirmEmail("");
      setSelectedMailbox(null);
      toast({ message: "Mailbox deleted.", variant: "info" });
    },
    onError: (err: any) => toast({ message: err?.message || "Failed to delete", variant: "danger" }),
  });

  const createMutation = useMutation({
    mutationFn: (data: typeof createForm) => api.createPlatformMailbox(data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin-mailboxes"] });
      setShowCreate(false);
      setCreateForm({ email: "", name: "", password: "", quota_mb: 1024 });
      toast({ message: "Mailbox created.", variant: "success" });
    },
    onError: (err: any) => toast({ message: err?.message || "Failed to create", variant: "danger" }),
  });

  const mbFromRaw = (raw: any): Mailbox => ({
    id: raw.id, email: raw.email, name: raw.email?.split("@")[0] || "",
    domain: raw.domain, tenant_id: raw.tenant_id || 0,
    status: raw.status || "active", quota_mb: raw.quota_mb || 1024,
    used_mb: raw.used_mb, is_admin: raw.is_admin,
    created_at: raw.created_at || "", updated_at: raw.updated_at || "",
  });

  const cols = [
    { key: "email", label: "Email", render: (row: any) => <span className="font-medium text-[var(--text-primary)]">{row.email}</span> },
    { key: "domain", label: "Domain" },
    { key: "status", label: "Status", render: (row: any) => <Badge variant={row.status === "active" ? "teal" : row.status === "suspended" ? "warning" : "danger"}>{row.status}</Badge> },
    { key: "is_admin", label: "Admin", render: (row: any) => row.is_admin ? <Badge variant="blue">Admin</Badge> : null },
    { key: "created_at", label: "Created", render: (row: any) => new Date(row.created_at).toLocaleDateString() },
    { key: "actions", label: "", width: "60px", render: () => <Button variant="ghost" size="sm">View</Button> },
  ];

  const openDetail = (raw: any) => setSelectedMailbox(mbFromRaw(raw));

  return (
    <div className="space-y-6">
      <PageHeader title="Mailboxes" subtitle="Platform mailbox inventory"
        actions={<Button variant="primary" onClick={() => setShowCreate(true)} iconLeft={<PlusCircle size={16} />}>Create Mailbox</Button>}
      />

      {listError && <ErrorBanner message={(listErr as any)?.message || "Failed to load mailboxes"} onRetry={() => queryClient.invalidateQueries({ queryKey: ["admin-mailboxes"] })} />}

      <FilterBar
        search={{ value: search, onChange: setSearch, placeholder: "Search by email..." }}
        onClear={() => setStatusFilter("")}
      >
        <select value={statusFilter} onChange={(e) => { setStatusFilter(e.target.value); }} className="orvix-select w-auto min-w-[140px]">
          <option value="">All statuses</option>
          <option value="active">Active</option>
          <option value="suspended">Suspended</option>
          <option value="disabled">Disabled</option>
        </select>
      </FilterBar>

      <DataTable columns={cols} rows={mailboxes} loading={isLoading} onRowClick={openDetail}
        emptyState={<EmptyState icon={Mail} title="No mailboxes found" />}
      />

      <Pagination pagination={{ page: 1, pageSize: total || 1, total }} onPageChange={() => {}} />

      {/* Detail drawer */}
      <Drawer open={!!selectedMailbox} onClose={() => { setSelectedMailbox(null); }} title={selectedMailbox?.email || "Mailbox"}>
        {selectedMailbox && (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div className="col-span-2"><span className="text-[var(--text-muted)]">Email:</span><span className="ml-1 text-[var(--text-primary)]">{selectedMailbox.email}</span></div>
              <div><span className="text-[var(--text-muted)]">Domain:</span><span className="ml-1 text-[var(--text-primary)]">{selectedMailbox.domain || "—"}</span></div>
              <div><span className="text-[var(--text-muted)]">Status:</span><span className="ml-1"><Badge variant={selectedMailbox.status === "active" ? "teal" : selectedMailbox.status === "suspended" ? "warning" : "danger"}>{selectedMailbox.status}</Badge></span></div>
              <div className="col-span-2"><span className="text-[var(--text-muted)]">Created:</span><span className="ml-1 text-[var(--text-primary)]">{new Date(selectedMailbox.created_at).toLocaleString()}</span></div>
            </div>

            <div className="border-t border-[var(--border)] pt-4">
              <h4 className="text-sm font-medium text-[var(--text-primary)] mb-3">Quota Usage</h4>
              <QuotaBar used={selectedMailbox.used_mb} total={selectedMailbox.quota_mb} />
            </div>

            <div className="flex gap-2 pt-4 border-t border-[var(--border)]">
              {selectedMailbox.status === "active" ? (
                <Button variant="danger" size="sm" onClick={() => setShowSuspend(true)}>Suspend</Button>
              ) : (
                <Button variant="primary" size="sm" onClick={() => statusMutation.mutate({ id: selectedMailbox.id, status: "active" })}>Activate</Button>
              )}
              <Button variant="danger" size="sm" onClick={() => { setDeleteConfirmEmail(""); setShowDelete(true); }}>Delete</Button>
            </div>
          </div>
        )}
      </Drawer>

      {/* Suspend confirmation */}
      <Dialog open={showSuspend} onClose={() => setShowSuspend(false)} title="Suspend Mailbox"
        description={`Suspend ${selectedMailbox?.email}? Mail delivery to this address will stop.`}
        footer={<><Button variant="ghost" onClick={() => setShowSuspend(false)}>Cancel</Button><Button variant="danger" loading={statusMutation.isPending} onClick={() => selectedMailbox && statusMutation.mutate({ id: selectedMailbox.id, status: "suspended" })}>Confirm Suspend</Button></>}
      />

      {/* Delete confirmation (type email to confirm) */}
      <Dialog open={showDelete} onClose={() => setShowDelete(false)} title="Delete Mailbox"
        description={`Type ${selectedMailbox?.email} below to confirm permanent deletion.`}
        footer={<><Button variant="ghost" onClick={() => setShowDelete(false)}>Cancel</Button><Button variant="danger" loading={deleteMutation.isPending} disabled={deleteConfirmEmail !== selectedMailbox?.email} onClick={() => selectedMailbox && deleteMutation.mutate(selectedMailbox.id)}>Permanently Delete</Button></>}
      >
        <input type="text" value={deleteConfirmEmail} onChange={(e) => setDeleteConfirmEmail(e.target.value)}
          placeholder={selectedMailbox?.email} className="orvix-input" autoComplete="off" />
      </Dialog>

      {/* Create dialog */}
      <Dialog open={showCreate} onClose={() => setShowCreate(false)} title="Create Mailbox"
        footer={<><Button variant="ghost" onClick={() => setShowCreate(false)}>Cancel</Button><Button variant="primary" loading={createMutation.isPending} disabled={!createForm.email || !createForm.password} onClick={() => createMutation.mutate(createForm)}>Create</Button></>}
      >
        <div className="space-y-4">
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">Email *</label><input value={createForm.email} onChange={(e) => setCreateForm({ ...createForm, email: e.target.value })} placeholder="user@domain.com" className="orvix-input" /></div>
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">Name</label><input value={createForm.name} onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })} placeholder="Display name" className="orvix-input" /></div>
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">Password *</label><input type="password" value={createForm.password} onChange={(e) => setCreateForm({ ...createForm, password: e.target.value })} placeholder="••••••••" className="orvix-input" /></div>
          <div><label className="block text-sm text-[var(--text-secondary)] mb-1">Quota (MB)</label><input type="number" min={1} value={createForm.quota_mb} onChange={(e) => setCreateForm({ ...createForm, quota_mb: Math.max(1, parseInt(e.target.value) || 1) })} className="orvix-input" /></div>
        </div>
      </Dialog>
    </div>
  );
}
