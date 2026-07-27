import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { UserPlus, X, Mail } from "lucide-react";
import { api } from "../api";

export default function InvitationsPage() {
  const queryClient = useQueryClient();
  const [email, setEmail] = useState("");

  const { data: invitations } = useQuery({ queryKey: ["invitations"], queryFn: api.listInvitations });

  const createInvitation = useMutation({
    mutationFn: () => api.createInvitation({ email, role: "user" }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["invitations"] }); setEmail(""); },
  });

  const revoke = useMutation({
    mutationFn: (id: number) => api.revokeInvitation(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["invitations"] }),
  });

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Invitations</h2>

      <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <UserPlus className="w-5 h-5 text-[var(--accent-blue)]" />
          <h3 className="text-lg font-medium text-[var(--text-primary)]">Invite Member</h3>
        </div>
        <div className="flex gap-2">
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="colleague@example.com"
            className="flex-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-[var(--text-primary)] text-sm" />
          <button onClick={() => createInvitation.mutate()}
            disabled={createInvitation.isPending || !email}
            className="bg-[var(--accent-blue)] text-[var(--text-primary)] rounded px-4 py-2 text-sm hover:bg-[var(--accent-hover)] disabled:opacity-50">
            {createInvitation.isPending ? "Sending..." : "Invite"}
          </button>
        </div>
      </div>

      {invitations && invitations.length > 0 && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6">
          <div className="space-y-2">
            {invitations.map((inv: any) => (
              <div key={inv.id} className="flex items-center justify-between p-3 bg-[var(--bg-base)] rounded">
                <div className="flex items-center gap-3">
                  <Mail className="w-4 h-4 text-[var(--text-secondary)]" />
                  <span className="text-[var(--text-primary)] text-sm">{inv.email}</span>
                  <span className={`text-xs px-2 py-0.5 rounded ${inv.status === "pending" ? "bg-yellow-400/10 text-yellow-400" : "bg-green-400/10 text-green-400"}`}>
                    {inv.status}
                  </span>
                </div>
                {inv.status === "pending" && (
                  <button onClick={() => revoke.mutate(inv.id)} className="text-[var(--text-secondary)] hover:text-red-400">
                    <X className="w-4 h-4" />
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {(!invitations || invitations.length === 0) && (
        <div className="bg-[var(--bg-surface)] border border-[var(--border)] rounded-lg p-6 text-center">
          <Mail className="w-8 h-8 text-[var(--text-muted)] mx-auto mb-2" />
          <p className="text-[var(--text-secondary)] text-sm">No pending invitations</p>
        </div>
      )}
    </div>
  );
}
