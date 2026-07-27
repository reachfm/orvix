import { useState, useEffect, useRef } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Users, AlertTriangle } from "lucide-react";
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
import type { User } from "../types/users";

const ROLE_BADGE: Record<string, "teal" | "blue" | "neutral" | "purple"> = {
  superadmin: "purple", platform_super_admin: "purple", admin: "blue", user: "neutral",
};

const ROLE_COLORS: Record<string, string> = {
  purple: "bg-purple-500/10 text-purple-400 border-purple-500/30",
  blue: "bg-blue-500/10 text-blue-400 border-blue-500/30",
  neutral: "bg-gray-500/10 text-gray-400 border-gray-500/30",
};

export default function UsersPage() {
  const queryClient = useQueryClient();
  const { toast } = useToast();
  const [search, setSearch] = useState("");
  const [roleFilter, setRoleFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [debouncedSearch, setDebouncedSearch] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  const [selectedUser, setSelectedUser] = useState<any>(null);
  const [showStatusChange, setShowStatusChange] = useState(false);
  const [showDelete, setShowDelete] = useState(false);
  const [deleteConfirmEmail, setDeleteConfirmEmail] = useState("");
  const [showRoleChange, setShowRoleChange] = useState(false);
  const [newRole, setNewRole] = useState("");

  useEffect(() => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => { setDebouncedSearch(search); }, 300);
    return () => { if (debounceRef.current) clearTimeout(debounceRef.current); };
  }, [search]);

  const { data: listData, isLoading, isError: listError, error: listErr } = useQuery({
    queryKey: ["admin-users", debouncedSearch, roleFilter, statusFilter],
    queryFn: async () => {
      const raw = await api.listPlatformUsers({ q: debouncedSearch || undefined, role: roleFilter || undefined, status: statusFilter || undefined });
      const users: any[] = Array.isArray(raw) ? raw : raw?.users || [];
      return { users, total: users.length };
    },
  });

  const users = listData?.users || [];
  const total = listData?.total || 0;

  const statusMutation = useMutation({
    mutationFn: ({ id, status }: { id: number; status: string }) => api.updateUserStatus(id, status),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["admin-users"] }); setShowStatusChange(false); toast({ message: "User status updated.", variant: "success" }); },
    onError: (err: any) => toast({ message: err?.message || "Failed to update", variant: "danger" }),
  });

  const roleMutation = useMutation({
    mutationFn: ({ id, role }: { id: number; role: string }) => api.updateUserRole(id, role),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin-users"] });
      setShowRoleChange(false);
      if (selectedUser) setSelectedUser({ ...selectedUser, role: newRole });
      toast({ message: "Role updated.", variant: "success" });
    },
    onError: (err: any) => toast({ message: err?.message || "Failed to update role", variant: "danger" }),
  });

  const deleteMutation = useMutation({
    mutationFn: (id: number) => api.deletePlatformUser(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin-users"] });
      setShowDelete(false); setDeleteConfirmEmail(""); setSelectedUser(null);
      toast({ message: "User deleted.", variant: "info" });
    },
    onError: (err: any) => toast({ message: err?.message || "Failed to delete", variant: "danger" }),
  });

  const cols = [
    { key: "email", label: "Email", render: (row: any) => <span className="font-medium text-[var(--text-primary)]">{row.email}</span> },
    { key: "role", label: "Role", render: (row: any) => {
      const variant = ROLE_BADGE[row.role] || "neutral";
      return <Badge variant={variant}>{row.role?.replace(/_/g, " ")}</Badge>;
    }},
    { key: "status", label: "Status", render: (row: any) => <Badge variant={row.status === "active" || row.active === true ? "teal" : row.status === "suspended" || row.active === false ? "warning" : "neutral"}>{row.status || (row.active ? "active" : "suspended")}</Badge> },
    { key: "created_at", label: "Created", render: (row: any) => row.created_at ? new Date(row.created_at).toLocaleDateString() : "—" },
    { key: "actions", label: "", width: "60px", render: () => <Button variant="ghost" size="sm">View</Button> },
  ];

  return (
    <div className="space-y-6">
      <PageHeader title="Users" subtitle="Platform user management" />

      {listError && <ErrorBanner message={(listErr as any)?.message || "Failed to load users"} onRetry={() => queryClient.invalidateQueries({ queryKey: ["admin-users"] })} />}

      <FilterBar search={{ value: search, onChange: setSearch, placeholder: "Search by email..." }} onClear={() => { setRoleFilter(""); setStatusFilter(""); }}>
        <select value={roleFilter} onChange={(e) => setRoleFilter(e.target.value)} className="orvix-select w-auto min-w-[130px]">
          <option value="">All roles</option>
          <option value="superadmin">Superadmin</option>
          <option value="platform_super_admin">Platform Super Admin</option>
          <option value="admin">Admin</option>
          <option value="user">User</option>
        </select>
        <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} className="orvix-select w-auto min-w-[130px]">
          <option value="">All statuses</option>
          <option value="active">Active</option>
          <option value="suspended">Suspended</option>
        </select>
      </FilterBar>

      <DataTable columns={cols} rows={users} loading={isLoading} onRowClick={(row) => setSelectedUser(row)}
        emptyState={<EmptyState icon={Users} title="No users found" />}
      />

      <Pagination pagination={{ page: 1, pageSize: total || 1, total }} onPageChange={() => {}} />

      {/* Detail drawer */}
      <Drawer open={!!selectedUser} onClose={() => setSelectedUser(null)} title={selectedUser?.email || "User"}>
        {selectedUser && (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3 text-sm">
              <div className="col-span-2"><span className="text-[var(--text-muted)]">Email:</span><span className="ml-1 text-[var(--text-primary)]">{selectedUser.email}</span></div>
              <div><span className="text-[var(--text-muted)]">Role:</span><span className="ml-1"><Badge variant={ROLE_BADGE[selectedUser.role] || "neutral"}>{selectedUser.role}</Badge></span></div>
              <div><span className="text-[var(--text-muted)]">Status:</span><span className="ml-1"><Badge variant={selectedUser.status === "active" || selectedUser.active !== false ? "teal" : "warning"}>{selectedUser.status || (selectedUser.active !== false ? "active" : "suspended")}</Badge></span></div>
              <div className="col-span-2"><span className="text-[var(--text-muted)]">Created:</span><span className="ml-1 text-[var(--text-primary)]">{selectedUser.created_at ? new Date(selectedUser.created_at).toLocaleString() : "—"}</span></div>
            </div>

            <div className="flex flex-col gap-2 pt-4 border-t border-[var(--border)]">
              {(selectedUser.role === "admin" || selectedUser.role === "user") && (
                <div className="flex items-center gap-2">
                  <label className="text-sm text-[var(--text-secondary)]">Change role:</label>
                  <select value={selectedUser.role} onChange={(e) => { setNewRole(e.target.value); setShowRoleChange(true); }} className="orvix-select w-auto text-sm">
                    <option value="admin">Admin</option>
                    <option value="user">User</option>
                    <option value="superadmin">Superadmin</option>
                  </select>
                </div>
              )}
              <div className="flex gap-2 mt-2">
                {selectedUser.active !== false && selectedUser.status !== "suspended" ? (
                  <Button variant="danger" size="sm" onClick={() => setShowStatusChange(true)}>Suspend</Button>
                ) : (
                  <Button variant="primary" size="sm" onClick={() => statusMutation.mutate({ id: selectedUser.id, status: "active" })}>Activate</Button>
                )}
                <Button variant="danger" size="sm" onClick={() => { setDeleteConfirmEmail(""); setShowDelete(true); }}>Delete</Button>
              </div>
            </div>
          </div>
        )}
      </Drawer>

      {/* Suspend confirmation */}
      <Dialog open={showStatusChange} onClose={() => setShowStatusChange(false)} title="Suspend User"
        description={`Suspend ${selectedUser?.email}? They will lose platform access.`}
        footer={<><Button variant="ghost" onClick={() => setShowStatusChange(false)}>Cancel</Button><Button variant="danger" onClick={() => selectedUser && statusMutation.mutate({ id: selectedUser.id, status: "suspended" })}>Confirm Suspend</Button></>}
      />

      {/* Role change confirmation */}
      <Dialog open={showRoleChange} onClose={() => setShowRoleChange(false)} title="Change Role"
        description={`Change ${selectedUser?.email} role to ${newRole}?`}
        footer={<><Button variant="ghost" onClick={() => setShowRoleChange(false)}>Cancel</Button><Button variant="primary" loading={roleMutation.isPending} onClick={() => selectedUser && roleMutation.mutate({ id: selectedUser.id, role: newRole })}>Confirm Role Change</Button></>}
      />

      {/* Delete confirmation */}
      <Dialog open={showDelete} onClose={() => setShowDelete(false)} title="Delete User"
        description={`Type ${selectedUser?.email} to confirm permanent deletion.`}
        footer={<><Button variant="ghost" onClick={() => setShowDelete(false)}>Cancel</Button><Button variant="danger" loading={deleteMutation.isPending} disabled={deleteConfirmEmail !== selectedUser?.email} onClick={() => selectedUser && deleteMutation.mutate(selectedUser.id)}>Permanently Delete</Button></>}
      >
        <input type="text" value={deleteConfirmEmail} onChange={(e) => setDeleteConfirmEmail(e.target.value)} placeholder={selectedUser?.email} className="orvix-input" autoComplete="off" />
      </Dialog>
    </div>
  );
}
