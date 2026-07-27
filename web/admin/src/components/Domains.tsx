import { useState, useEffect, useRef, useCallback } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Globe, RefreshCw, AlertTriangle, Loader2 } from "lucide-react";
import { api } from "../api";
import PageHeader from "./ui/PageHeader";
import FilterBar from "./ui/FilterBar";
import DataTable from "./ui/DataTable";
import Pagination from "./ui/Pagination";
import Drawer from "./ui/Drawer";
import Dialog from "./ui/Dialog";
import Badge from "./ui/Badge";
import Button from "./ui/Button";
import StatusDot from "./ui/StatusDot";
import EmptyState from "./ui/EmptyState";
import ErrorBanner from "./ui/ErrorBanner";
import { useToast } from "./ui/Toast";
import type { Domain } from "../types/domains";

export default function Domains() {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [page, setPage] = useState(1);
  const [pageSize] = useState(20);
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const [selectedDomain, setSelectedDomain] = useState<Domain | null>(null);
  const [showStatusChange, setShowStatusChange] = useState(false);
  const [pendingStatus, setPendingStatus] = useState<"active" | "disabled">("active");

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => { setDebouncedSearch(search); setPage(1); }, 300);
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current); };
  }, [search]);

  const { data: listData, isLoading, isError: listError, error: listErr } = useQuery({
    queryKey: ["admin-domains", page, pageSize, debouncedSearch, statusFilter],
    queryFn: async () => {
      const raw = await api.listAdminDomains({ page, page_size: pageSize, search: debouncedSearch, status: statusFilter || undefined });
      const domains: Domain[] = Array.isArray(raw) ? raw : raw?.domains || [];
      return { domains, total: domains.length };
    },
  });

  const domains = listData?.domains || [];
  const total = listData?.total || 0;

  const statusMutation = useMutation({
    mutationFn: ({ name, status }: { name: string; status: string }) => api.updateAdminDomainStatus(name, { status }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin-domains"] });
      setShowStatusChange(false);
      if (selectedDomain) setSelectedDomain({ ...selectedDomain, status: pendingStatus });
      toast({ message: `Domain ${pendingStatus === "active" ? "enabled" : "disabled"}.`, variant: "success" });
    },
    onError: (err: any) => { toast({ message: err?.message || "Failed to update domain", variant: "danger" }); },
  });

  const cols = [
    { key: "domain", label: "Domain", render: (row: any) => <span className="font-medium text-[var(--text-primary)]">{row.domain || row.name}</span> },
    { key: "plan", label: "Plan" },
    { key: "status", label: "Status", render: (row: Domain) => <Badge variant={row.status === "active" ? "teal" : row.status === "disabled" ? "danger" : "warning"}>{row.status}</Badge> },
    { key: "mailbox_count", label: "Mailboxes" },
    { key: "actions", label: "", width: "60px", render: (row: Domain) => <Button variant="ghost" size="sm">View</Button> },
  ];

  const domainFromRaw = (raw: any): Domain => ({
    id: raw.id, domain: raw.domain, name: raw.domain, plan: raw.plan, tenant_id: raw.tenant_id || 0,
    status: raw.status || "pending", verified: raw.verified || false,
    mx_status: raw.mx_status || "unknown", spf_status: raw.spf_status || "unknown",
    dkim_status: raw.dkim_status || "unknown", dmarc_status: raw.dmarc_status || "unknown",
    created_at: raw.created_at || "", updated_at: raw.updated_at || "",
  });

  const openDetail = (raw: any) => {
    setSelectedDomain(domainFromRaw(raw));
  };

  return (
    <div className="space-y-6">
      <PageHeader title="Domains" subtitle="Platform domain inventory" />

      {listError && <ErrorBanner message={(listErr as any)?.message || "Failed to load domains"} onRetry={() => queryClient.invalidateQueries({ queryKey: ["admin-domains"] })} />}

      <FilterBar
        search={{ value: search, onChange: setSearch, placeholder: "Search by domain name..." }}
        onClear={() => setStatusFilter("")}
      >
        <select value={statusFilter} onChange={(e) => { setStatusFilter(e.target.value); setPage(1); }} className="orvix-select w-auto min-w-[140px]">
          <option value="">All statuses</option>
          <option value="active">Active</option>
          <option value="disabled">Disabled</option>
          <option value="suspended">Suspended</option>
        </select>
      </FilterBar>

      <DataTable columns={cols} rows={domains} loading={isLoading} onRowClick={openDetail}
        emptyState={<EmptyState icon={Globe} title="No domains found" />}
      />

      <Pagination pagination={{ page, pageSize, total }} onPageChange={setPage} />

      <Drawer open={!!selectedDomain} onClose={() => setSelectedDomain(null)} title={selectedDomain?.domain || "Domain"}>
        {selectedDomain && (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div><span className="text-[var(--text-muted)]">Domain:</span><span className="ml-1 text-[var(--text-primary)]">{selectedDomain.domain}</span></div>
              <div><span className="text-[var(--text-muted)]">Status:</span><span className="ml-1"><Badge variant={selectedDomain.status === "active" ? "teal" : selectedDomain.status === "disabled" ? "danger" : "warning"}>{selectedDomain.status}</Badge></span></div>
              <div><span className="text-[var(--text-muted)]">Plan:</span><span className="ml-1 text-[var(--text-primary)]">{(selectedDomain as any).plan || "—"}</span></div>
              <div><span className="text-[var(--text-muted)]">Mailboxes:</span><span className="ml-1 text-[var(--text-primary)]">{(selectedDomain as any).mailbox_count || "—"}</span></div>
            </div>

            {/* DNS Status with StatusDots */}
            <div className="border-t border-[var(--border)] pt-4 space-y-2">
              <h4 className="text-sm font-medium text-[var(--text-primary)]">DNS Verification</h4>
              {(["mx", "spf", "dkim", "dmarc"] as const).map((type) => {
                const status = (selectedDomain as any)[`${type}_status`] || "unknown";
                return (
                  <div key={type} className="flex items-center gap-3 bg-[var(--bg-elevated)] rounded-lg px-3 py-2">
                    <StatusDot status={status === "ok" ? "success" : status === "error" ? "danger" : status === "pending" ? "checking" : "neutral"} />
                    <span className="text-sm font-medium uppercase text-[var(--text-primary)]">{type}</span>
                    <span className="text-xs text-[var(--text-muted)]">{status}</span>
                  </div>
                );
              })}
            </div>

            <div className="flex gap-2 pt-4 border-t border-[var(--border)]">
              {selectedDomain.status === "active" ? (
                <Button variant="danger" size="sm" onClick={() => { setPendingStatus("disabled"); setShowStatusChange(true); }}>Disable</Button>
              ) : (
                <Button variant="primary" size="sm" onClick={() => { setPendingStatus("active"); setShowStatusChange(true); }}>Enable</Button>
              )}
            </div>
          </div>
        )}
      </Drawer>

      <Dialog open={showStatusChange} onClose={() => setShowStatusChange(false)}
        title={pendingStatus === "active" ? "Enable Domain" : "Disable Domain"}
        description={`${pendingStatus === "active" ? "Enable" : "Disable"} ${selectedDomain?.domain}?`}
        footer={
          <>
            <Button variant="ghost" onClick={() => setShowStatusChange(false)}>Cancel</Button>
            <Button variant={pendingStatus === "active" ? "primary" : "danger"} loading={statusMutation.isPending}
              onClick={() => selectedDomain && statusMutation.mutate({ name: selectedDomain.domain, status: pendingStatus })}>
              Confirm
            </Button>
          </>
        }
      />
    </div>
  );
}
