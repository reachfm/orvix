import { useState, useEffect, useCallback, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Building, PlusCircle, Search, AlertTriangle, Loader2 } from "lucide-react";
import { api } from "../api";
import PageHeader from "./ui/PageHeader";
import FilterBar from "./ui/FilterBar";
import DataTable from "./ui/DataTable";
import Pagination from "./ui/Pagination";
import Dialog from "./ui/Dialog";
import Drawer from "./ui/Drawer";
import Badge from "./ui/Badge";
import Button from "./ui/Button";
import EmptyState from "./ui/EmptyState";
import ErrorBanner from "./ui/ErrorBanner";
import { useToast } from "./ui/Toast";
import type { Organization, ListOrgsResponse, OrgStats } from "../types/organizations";

export default function Organizations() {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // Create dialog state
  const [showCreate, setShowCreate] = useState(false);
  const [createForm, setCreateForm] = useState({ slug: "", name: "", domain: "", plan: "free", max_mailboxes: 10, max_storage_mb: 5000 });
  const [slugError, setSlugError] = useState("");
  const [createError, setCreateError] = useState("");

  // Detail drawer state
  const [selectedOrg, setSelectedOrg] = useState<Organization | null>(null);
  const [orgStats, setOrgStats] = useState<OrgStats | null>(null);
  const [statsLoading, setStatsLoading] = useState(false);

  // Edit dialog state
  const [showEdit, setShowEdit] = useState(false);
  const [editForm, setEditForm] = useState({ name: "", domain: "", plan: "free", max_mailboxes: 10, max_storage_mb: 5000 });

  // Suspend dialog state
  const [showSuspend, setShowSuspend] = useState(false);
  const [suspendReason, setSuspendReason] = useState("");

  // Activate confirm state
  const [showActivate, setShowActivate] = useState(false);

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => { setDebouncedSearch(search); setPage(1); }, 300);
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current); };
  }, [search]);

  const { data: listData, isLoading, isError: listError, error: listErr } = useQuery<ListOrgsResponse>({
    queryKey: ["platform-orgs", page, pageSize, debouncedSearch, statusFilter],
    queryFn: () => api.listPlatformOrganizations({ page, page_size: pageSize, search: debouncedSearch, status: statusFilter || undefined }) as Promise<ListOrgsResponse>,
  });

  const orgs = listData?.organizations || [];
  const total = listData?.total || 0;

  const loadStats = useCallback(async (id: number) => {
    setStatsLoading(true);
    try {
      const detail = await api.getPlatformOrganizationDetail(id);
      setOrgStats({
        mailbox_count: detail?.mailbox_count ?? detail?.active_mailbox_count ?? 0,
        active_mailbox_count: detail?.active_mailbox_count ?? 0,
        domain_count: detail?.domain_count ?? 0,
        storage_used_mb: detail?.storage_used_mb ?? 0,
      });
    } catch { setOrgStats(null); }
    setStatsLoading(false);
  }, []);

  const createMutation = useMutation({
    mutationFn: (data: typeof createForm) => api.createPlatformOrganization(data),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["platform-orgs"] }); setShowCreate(false); setCreateForm({ slug: "", name: "", domain: "", plan: "free", max_mailboxes: 10, max_storage_mb: 5000 }); toast({ message: "Organization created successfully.", variant: "success" }); },
    onError: (err: any) => { setCreateError(err?.message || "Failed to create organization"); },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: number; data: typeof editForm }) => api.updatePlatformOrganization(id, data),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["platform-orgs"] }); setShowEdit(false); toast({ message: "Organization updated.", variant: "success" }); },
    onError: (err: any) => { toast({ message: err?.message || "Failed to update", variant: "danger" }); },
  });

  const suspendMutation = useMutation({
    mutationFn: ({ id, reason }: { id: number; reason?: string }) => api.setPlatformOrganizationActive(id, { active: false, reason }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["platform-orgs"] }); setShowSuspend(false); setSuspendReason(""); if (selectedOrg) { setSelectedOrg({ ...selectedOrg, status: "suspended" }); } toast({ message: "Organization suspended.", variant: "info" }); },
    onError: (err: any) => { toast({ message: err?.message || "Failed to suspend", variant: "danger" }); },
  });

  const activateMutation = useMutation({
    mutationFn: (id: number) => api.setPlatformOrganizationActive(id, { active: true }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["platform-orgs"] }); setShowActivate(false); if (selectedOrg) { setSelectedOrg({ ...selectedOrg, status: "active" }); } toast({ message: "Organization activated.", variant: "success" }); },
    onError: (err: any) => { toast({ message: err?.message || "Failed to activate", variant: "danger" }); },
  });

  const validateSlug = (v: string) => { if (!/^[a-z0-9-]+$/.test(v)) setSlugError("Only lowercase letters, numbers, and hyphens allowed."); else setSlugError(""); };

  const columns = [
    { key: "name", label: "Name", render: (row: Organization) => <span className="font-medium text-[var(--text-primary)]">{row.name}</span> },
    { key: "slug", label: "Slug" },
    { key: "domain", label: "Domain" },
    { key: "plan", label: "Plan", render: (row: Organization) => <span className="capitalize">{row.plan}</span> },
    {
      key: "status", label: "Status",
      render: (row: Organization) => (
        <Badge variant={row.status === "active" ? "teal" : row.status === "suspended" ? "danger" : "warning"}>{row.status}</Badge>
      ),
    },
    { key: "active_mailboxes", label: "Mailboxes" },
    { key: "created_at", label: "Created", render: (row: Organization) => new Date(row.created_at).toLocaleDateString() },
    {
      key: "actions", label: "", width: "60px",
      render: (row: Organization) => (
        <Button variant="ghost" size="sm" onClick={(e) => { e?.stopPropagation?.(); setSelectedOrg(row); loadStats(row.id); }}>View</Button>
      ),
    },
  ];

  const openEdit = () => {
    if (!selectedOrg) return;
    setEditForm({ name: selectedOrg.name, domain: selectedOrg.domain, plan: selectedOrg.plan, max_mailboxes: selectedOrg.max_mailboxes, max_storage_mb: selectedOrg.max_storage_mb });
    setShowEdit(true);
  };

  return (
    <div className="space-y-6">
      <PageHeader
        title="Organizations"
        subtitle="Platform tenant inventory and management"
        actions={
          <Button variant="primary" onClick={() => setShowCreate(true)} iconLeft={<PlusCircle size={16} />}>
            Create Organization
          </Button>
        }
      />

      {listError && <ErrorBanner message={(listErr as any)?.message || "Failed to load organizations"} onRetry={() => queryClient.invalidateQueries({ queryKey: ["platform-orgs"] })} />}

      <FilterBar
        search={{ value: search, onChange: setSearch, placeholder: "Search by name, slug, or domain..." }}
        onClear={() => setStatusFilter("")}
      >
        <select value={statusFilter} onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }} className="orvix-select w-auto min-w-[140px]">
          <option value="">All statuses</option>
          <option value="active">Active</option>
          <option value="suspended">Suspended</option>
          <option value="pending">Pending</option>
        </select>
      </FilterBar>

      <DataTable
        columns={columns}
        rows={orgs}
        loading={isLoading}
        onRowClick={(row) => { setSelectedOrg(row); loadStats(row.id); }}
        emptyState={<EmptyState icon={Building} title="No organizations yet" description="Create your first organization to get started." action={{ label: "Create Organization", onClick: () => setShowCreate(true) }} />}
      />

      <Pagination pagination={{ page, pageSize, total }} onPageChange={setPage} />

      {/* Create dialog */}
      <Dialog open={showCreate} onClose={() => setShowCreate(false)} title="Create Organization" description="Provision a new platform tenant." footer={
        <>
          <Button variant="ghost" onClick={() => setShowCreate(false)}>Cancel</Button>
          <Button
            variant="primary"
            loading={createMutation.isPending}
            disabled={!createForm.slug || !createForm.name || !createForm.domain || !!slugError}
            onClick={() => { setCreateError(""); createMutation.mutate(createForm); }}
          >
            Create
          </Button>
        </>
      }>
        <div className="space-y-4">
          {createError && <ErrorBanner message={createError} />}
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Slug *</label>
            <input value={createForm.slug} onChange={(e) => { const v = e.target.value.toLowerCase().replace(/[^a-z0-9-]/g, ""); setCreateForm({ ...createForm, slug: v }); validateSlug(v); }}
              placeholder="my-tenant" className="orvix-input" autoComplete="off" />
            {slugError && <p className="text-xs text-[var(--status-danger)] mt-1">{slugError}</p>}
          </div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Name *</label>
            <input value={createForm.name} onChange={(e) => setCreateForm({ ...createForm, name: e.target.value })} placeholder="My Company Inc." className="orvix-input" />
          </div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Domain *</label>
            <input value={createForm.domain} onChange={(e) => setCreateForm({ ...createForm, domain: e.target.value })} placeholder="company.com" className="orvix-input" />
          </div>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1">Plan</label>
              <select value={createForm.plan} onChange={(e) => setCreateForm({ ...createForm, plan: e.target.value })} className="orvix-select">
                <option value="free">Free</option>
                <option value="starter">Starter</option>
                <option value="pro">Pro</option>
                <option value="enterprise">Enterprise</option>
              </select>
            </div>
            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1">Max Mailboxes</label>
              <input type="number" min={1} value={createForm.max_mailboxes} onChange={(e) => setCreateForm({ ...createForm, max_mailboxes: Math.max(1, parseInt(e.target.value) || 1) })} className="orvix-input" />
            </div>
            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1">Storage (MB)</label>
              <input type="number" min={1} value={createForm.max_storage_mb} onChange={(e) => setCreateForm({ ...createForm, max_storage_mb: Math.max(1, parseInt(e.target.value) || 1) })} className="orvix-input" />
            </div>
          </div>
        </div>
      </Dialog>

      {/* Detail drawer */}
      <Drawer open={!!selectedOrg} onClose={() => { setSelectedOrg(null); setOrgStats(null); }} title={selectedOrg?.name || "Organization"}>
        {selectedOrg && (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div><span className="text-[var(--text-muted)]">Slug:</span><span className="ml-1 text-[var(--text-primary)]">{selectedOrg.slug}</span></div>
              <div><span className="text-[var(--text-muted)]">Domain:</span><span className="ml-1 text-[var(--text-primary)]">{selectedOrg.domain}</span></div>
              <div><span className="text-[var(--text-muted)]">Plan:</span><span className="ml-1 capitalize text-[var(--text-primary)]">{selectedOrg.plan}</span></div>
              <div><span className="text-[var(--text-muted)]">Status:</span><span className="ml-1"><Badge variant={selectedOrg.status === "active" ? "teal" : selectedOrg.status === "suspended" ? "danger" : "warning"}>{selectedOrg.status}</Badge></span></div>
              <div><span className="text-[var(--text-muted)]">Max Mailboxes:</span><span className="ml-1 text-[var(--text-primary)]">{selectedOrg.max_mailboxes}</span></div>
              <div><span className="text-[var(--text-muted)]">Max Storage:</span><span className="ml-1 text-[var(--text-primary)]">{selectedOrg.max_storage_mb} MB</span></div>
              <div className="col-span-2"><span className="text-[var(--text-muted)]">Created:</span><span className="ml-1 text-[var(--text-primary)]">{new Date(selectedOrg.created_at).toLocaleString()}</span></div>
              <div className="col-span-2"><span className="text-[var(--text-muted)]">Updated:</span><span className="ml-1 text-[var(--text-primary)]">{new Date(selectedOrg.updated_at).toLocaleString()}</span></div>
            </div>

            <div className="border-t border-[var(--border)] pt-4">
              <h4 className="text-sm font-medium text-[var(--text-primary)] mb-3">Usage</h4>
              {statsLoading ? (
                <Loader2 size={16} className="animate-spin text-[var(--text-muted)]" />
              ) : orgStats ? (
                <div className="grid grid-cols-3 gap-3 text-sm">
                  <div className="bg-[var(--bg-elevated)] rounded-lg p-3 text-center">
                    <p className="text-2xl font-bold text-[var(--accent)]">{orgStats.active_mailbox_count}</p>
                    <p className="text-xs text-[var(--text-muted)] mt-1">Active mailboxes</p>
                  </div>
                  <div className="bg-[var(--bg-elevated)] rounded-lg p-3 text-center">
                    <p className="text-2xl font-bold text-[var(--accent-blue)]">{orgStats.domain_count}</p>
                    <p className="text-xs text-[var(--text-muted)] mt-1">Domains</p>
                  </div>
                  <div className="bg-[var(--bg-elevated)] rounded-lg p-3 text-center">
                    <p className="text-2xl font-bold text-[var(--warning)]">{(orgStats.storage_used_mb / 1024).toFixed(1)}</p>
                    <p className="text-xs text-[var(--text-muted)] mt-1">GB used</p>
                  </div>
                </div>
              ) : (
                <p className="text-sm text-[var(--text-muted)]">No usage data available.</p>
              )}
            </div>

            <div className="flex gap-2 pt-4 border-t border-[var(--border)]">
              <Button variant="secondary" size="sm" onClick={openEdit}>Edit</Button>
              {selectedOrg.status === "active" && (
                <Button variant="danger" size="sm" onClick={() => setShowSuspend(true)}>Suspend</Button>
              )}
              {(selectedOrg.status === "suspended" || selectedOrg.status === "pending") && (
                <Button variant="primary" size="sm" onClick={() => setShowActivate(true)}>Activate</Button>
              )}
            </div>
          </div>
        )}
      </Drawer>

      {/* Edit dialog */}
      <Dialog open={showEdit} onClose={() => setShowEdit(false)} title="Edit Organization" footer={
        <>
          <Button variant="ghost" onClick={() => setShowEdit(false)}>Cancel</Button>
          <Button variant="primary" loading={updateMutation.isPending} disabled={!editForm.name || !editForm.domain}
            onClick={() => selectedOrg && updateMutation.mutate({ id: selectedOrg.id, data: editForm })}>Save</Button>
        </>
      }>
        <div className="space-y-4">
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Name</label>
            <input value={editForm.name} onChange={(e) => setEditForm({ ...editForm, name: e.target.value })} className="orvix-input" />
          </div>
          <div>
            <label className="block text-sm text-[var(--text-secondary)] mb-1">Domain</label>
            <input value={editForm.domain} onChange={(e) => setEditForm({ ...editForm, domain: e.target.value })} className="orvix-input" />
          </div>
          <div className="grid grid-cols-3 gap-4">
            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1">Plan</label>
              <select value={editForm.plan} onChange={(e) => setEditForm({ ...editForm, plan: e.target.value })} className="orvix-select">
                <option value="free">Free</option><option value="starter">Starter</option><option value="pro">Pro</option><option value="enterprise">Enterprise</option>
              </select>
            </div>
            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1">Max Mailboxes</label>
              <input type="number" min={1} value={editForm.max_mailboxes} onChange={(e) => setEditForm({ ...editForm, max_mailboxes: Math.max(1, parseInt(e.target.value) || 1) })} className="orvix-input" />
            </div>
            <div>
              <label className="block text-sm text-[var(--text-secondary)] mb-1">Storage (MB)</label>
              <input type="number" min={1} value={editForm.max_storage_mb} onChange={(e) => setEditForm({ ...editForm, max_storage_mb: Math.max(1, parseInt(e.target.value) || 1) })} className="orvix-input" />
            </div>
          </div>
        </div>
      </Dialog>

      {/* Suspend confirmation */}
      <Dialog open={showSuspend} onClose={() => setShowSuspend(false)} title="Suspend Organization" description={`Are you sure you want to suspend ${selectedOrg?.name}? Mail delivery will stop.`} footer={
        <>
          <Button variant="ghost" onClick={() => setShowSuspend(false)}>Cancel</Button>
          <Button variant="danger" loading={suspendMutation.isPending} onClick={() => selectedOrg && suspendMutation.mutate({ id: selectedOrg.id, reason: suspendReason })}>Confirm Suspend</Button>
        </>
      }>
        <textarea value={suspendReason} onChange={(e) => setSuspendReason(e.target.value)} placeholder="Reason (optional)" className="orvix-input min-h-[80px]" />
      </Dialog>

      {/* Activate confirmation */}
      <Dialog open={showActivate} onClose={() => setShowActivate(false)} title="Activate Organization" description={`Reactivate ${selectedOrg?.name}? Mail delivery will resume.`} footer={
        <>
          <Button variant="ghost" onClick={() => setShowActivate(false)}>Cancel</Button>
          <Button variant="primary" loading={activateMutation.isPending} onClick={() => selectedOrg && activateMutation.mutate(selectedOrg.id)}>Confirm Activate</Button>
        </>
      } />
    </div>
  );
}
