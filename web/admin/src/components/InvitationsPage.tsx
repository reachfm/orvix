import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { UserPlus, X, Mail } from "lucide-react";
import { api, ApiError } from "../api";

// The only member roles the backend accepts for invitations
// (internal/admin/organization service isValidOrgMemberRole). "user"
// (RoleUser, the per-mailbox webmail end-user) is deliberately not
// offered: an invited member is an Organization administrator with a
// canonical tenant role.
const MEMBER_ROLES = [
  { id: "tenant_admin", label: "Admin" },
  { id: "tenant_operator", label: "Operator" },
  { id: "tenant_support", label: "Support" },
  { id: "tenant_readonly", label: "Read-only" },
];

export default function InvitationsPage() {
  const queryClient = useQueryClient();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("tenant_operator");

  const { data: invitations } = useQuery({ queryKey: ["invitations"], queryFn: api.listInvitations });

  const createInvitation = useMutation({
    mutationFn: () => api.createInvitation({ email, role }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["invitations"] }); setEmail(""); },
  });

  const revoke = useMutation({
    mutationFn: (id: number) => api.revokeInvitation(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["invitations"] }),
  });

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Invitations</h2>

      <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <UserPlus className="w-5 h-5 text-[var(--accent)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Invite Member</h3>
        </div>
        <div className="flex gap-2">
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="colleague@example.com"
            className="flex-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          <select
            value={role}
            onChange={(e) => setRole(e.target.value)}
            aria-label="Invited member role"
            className="px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm"
          >
            {MEMBER_ROLES.map((r) => (
              <option key={r.id} value={r.id}>{r.label}</option>
            ))}
          </select>
          <button onClick={() => createInvitation.mutate()}
            disabled={createInvitation.isPending || !email}
            className="bg-[var(--accent)] text-white rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
            {createInvitation.isPending ? "Sending..." : "Invite"}
          </button>
        </div>
        {createInvitation.error && (
          <p className="mt-2 text-sm text-[var(--danger)]">{(createInvitation.error as ApiError).message}</p>
        )}
      </div>

      {invitations && invitations.length > 0 && (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <div className="space-y-2">
            {invitations.map((inv: any) => (
              <div key={inv.id} className="flex items-center justify-between p-3 bg-[var(--bg-base)] rounded">
                <div className="flex items-center gap-3">
                  <Mail className="w-4 h-4 text-[var(--text-secondary)]" />
                  <span className="text-[var(--text-primary)] text-sm">{inv.email}</span>
                  <span className="text-xs text-[var(--text-muted)]">{inv.role}</span>
                  <span className={`text-xs px-2 py-0.5 rounded ${inv.status === "pending" ? "bg-[var(--warning)]/10 text-[var(--warning)]" : "bg-[var(--success)]/10 text-[var(--success)]"}`}>
                    {inv.status}
                  </span>
                </div>
                {inv.status === "pending" && (
                  <button onClick={() => revoke.mutate(inv.id)} className="text-[var(--text-secondary)] hover:text-[var(--danger)]">
                    <X className="w-4 h-4" />
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {(!invitations || invitations.length === 0) && (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6 text-center">
          <Mail className="w-8 h-8 text-[var(--text-muted)] mx-auto mb-2" />
          <p className="text-[var(--text-secondary)] text-sm">No pending invitations</p>
        </div>
      )}
    </div>
  );
}
