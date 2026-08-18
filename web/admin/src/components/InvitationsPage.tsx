import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { UserPlus, X, Mail, RotateCw, Copy, Check, KeyRound } from "lucide-react";
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

interface InvitationRow {
  id: number;
  email: string;
  role: string;
  status: string; // pending | accepted | revoked | expired
  expires_at?: string;
}

interface RevealedToken {
  email: string;
  token: string;
  warning: string;
  kind: "create" | "resend";
}

/** Stable status presentation per the org_invitations status values. */
function statusBadge(status: string) {
  switch (status) {
    case "pending":
      return <span className="text-xs px-2 py-0.5 rounded bg-[var(--warning)]/10 text-[var(--warning)]">pending</span>;
    case "accepted":
      return <span className="text-xs px-2 py-0.5 rounded bg-[var(--success)]/10 text-[var(--success)]">accepted</span>;
    case "revoked":
      return <span className="text-xs px-2 py-0.5 rounded bg-[var(--danger)]/10 text-[var(--danger)]">revoked</span>;
    case "expired":
      return <span className="text-xs px-2 py-0.5 rounded bg-[var(--bg-subtle)] text-[var(--text-muted)]">expired</span>;
    default:
      return <span className="text-xs px-2 py-0.5 rounded bg-[var(--bg-subtle)] text-[var(--text-secondary)]">{status}</span>;
  }
}

function invitationErrorMessage(err: unknown): string {
  if (!(err instanceof ApiError)) return err instanceof Error ? err.message : "Request failed.";
  switch (err.code) {
    case "CONFLICT":
      return err.message || "A pending invitation already exists for this email.";
    case "INVALID_STATE_TRANSITION":
      return "Only pending invitations can be re-issued. This invitation was accepted, revoked, or expired.";
    case "NOT_FOUND":
      return "This invitation no longer exists.";
    case "VALIDATION_FAILED":
      return err.message || "The invitation details are invalid.";
    default:
      return err.message || "Request failed.";
  }
}

export default function InvitationsPage() {
  const queryClient = useQueryClient();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("tenant_operator");
  const [revealed, setRevealed] = useState<RevealedToken | null>(null);
  const [copied, setCopied] = useState(false);

  const { data: invitations } = useQuery({ queryKey: ["invitations"], queryFn: api.listInvitations });

  const createInvitation = useMutation({
    mutationFn: () => api.createInvitation({ email, role }),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ["invitations"] });
      setEmail("");
      // The one-time token is returned ONLY here — reveal it once with
      // copy, never re-fetchable afterwards.
      setRevealed({
        email: res.invitation?.email ?? email,
        token: res.token,
        warning: "Save this invitation token now — it will not be shown again.",
        kind: "create",
      });
    },
  });

  const revoke = useMutation({
    mutationFn: (id: number) => api.revokeInvitation(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["invitations"] }),
  });

  const resend = useMutation({
    mutationFn: (id: number) => api.resendInvitation(id),
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ["invitations"] });
      // Rotate semantics: the new token REPLACES the previous one.
      setRevealed({
        email: res.invitation?.email ?? "",
        token: res.token,
        warning: res.warning,
        kind: "resend",
      });
    },
  });

  const copyToken = () => {
    if (!revealed) return;
    void navigator.clipboard?.writeText(revealed.token).catch(() => {});
    setCopied(true);
    window.setTimeout(() => setCopied(false), 2000);
  };

  const rows: InvitationRow[] = Array.isArray(invitations) ? invitations : [];

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
          <p className="mt-2 text-sm text-[var(--danger)]" role="alert">{invitationErrorMessage(createInvitation.error)}</p>
        )}
      </div>

      {revealed && (
        <div className="bg-[var(--bg-elevated)] border border-[var(--warning)]/40 rounded-lg p-4" role="status">
          <div className="flex items-center gap-2 mb-2">
            <KeyRound className="w-4 h-4 text-[var(--warning)]" />
            <h3 className="text-sm font-medium text-[var(--text-primary)]">
              {revealed.kind === "resend" ? "Invitation token re-issued" : "Invitation created"}
            </h3>
          </div>
          {revealed.email && <p className="text-xs text-[var(--text-secondary)] mb-2">For {revealed.email}</p>}
          <div className="flex items-start gap-2">
            <code className="flex-1 px-3 py-2 bg-[var(--bg-base)] border border-[var(--border)] rounded text-xs break-all font-mono text-[var(--text-primary)]">
              {revealed.token}
            </code>
            <button
              type="button"
              aria-label="Copy invitation token"
              onClick={copyToken}
              className="p-2 border border-[var(--border)] rounded text-[var(--text-secondary)] hover:text-[var(--text-primary)]"
            >
              {copied ? <Check size={14} /> : <Copy size={14} />}
            </button>
          </div>
          <p className="text-xs text-[var(--danger)] mt-2">{revealed.warning}</p>
          <p className="text-xs text-[var(--text-muted)] mt-1">
            Share it privately with the invitee. They redeem it at /admin/invitations/accept (token + password).
          </p>
        </div>
      )}

      {resend.error && (
        <p className="text-sm text-[var(--danger)] border border-[var(--danger)]/30 rounded-lg p-3" role="alert">
          {invitationErrorMessage(resend.error)}
        </p>
      )}

      {rows.length > 0 ? (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <div className="space-y-2">
            {rows.map((inv) => (
              <div key={inv.id} className="flex items-center justify-between p-3 bg-[var(--bg-base)] rounded">
                <div className="flex items-center gap-3 flex-wrap">
                  <Mail className="w-4 h-4 text-[var(--text-secondary)]" />
                  <span className="text-[var(--text-primary)] text-sm">{inv.email}</span>
                  <span className="text-xs text-[var(--text-muted)]">{inv.role}</span>
                  {statusBadge(inv.status)}
                  {inv.expires_at && inv.status === "pending" && (
                    <span className="text-xs text-[var(--text-muted)]">
                      expires {new Date(inv.expires_at).toLocaleDateString()}
                    </span>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  {inv.status === "pending" && (
                    <>
                      <button
                        onClick={() => resend.mutate(inv.id)}
                        disabled={resend.isPending}
                        title="Re-issue the one-time token (invalidates any prior copy)"
                        aria-label={`Re-issue invitation for ${inv.email}`}
                        className="inline-flex items-center gap-1 text-xs text-[var(--text-secondary)] hover:text-[var(--accent)] disabled:opacity-50"
                      >
                        <RotateCw className="w-3.5 h-3.5" /> Resend
                      </button>
                      <button onClick={() => revoke.mutate(inv.id)} title="Revoke invitation"
                        aria-label={`Revoke invitation for ${inv.email}`}
                        className="text-[var(--text-secondary)] hover:text-[var(--danger)]">
                        <X className="w-4 h-4" />
                      </button>
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        </div>
      ) : (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6 text-center">
          <Mail className="w-8 h-8 text-[var(--text-muted)] mx-auto mb-2" />
          <p className="text-[var(--text-secondary)] text-sm">No invitations yet</p>
        </div>
      )}
    </div>
  );
}
