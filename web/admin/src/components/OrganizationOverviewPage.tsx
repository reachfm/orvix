import { useQuery } from "@tanstack/react-query";
import { Building, Globe, Mail, Users } from "lucide-react";
import { api } from "../api";

export default function OrganizationOverviewPage() {
  const { data: org } = useQuery({ queryKey: ["org"], queryFn: api.getCurrentOrganization });
  const { data: members } = useQuery({ queryKey: ["members"], queryFn: api.listMembers });

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-[var(--text-primary)]">Organization</h2>

      {org && (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <Building className="w-5 h-5 text-[var(--accent)]" />
            <h3 className="text-lg font-medium text-[var(--text-primary)]">{org.name}</h3>
          </div>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-[var(--text-secondary)]">Slug: </span><span className="text-[var(--text-primary)]">{org.slug}</span></div>
            <div><span className="text-[var(--text-secondary)]">Domain: </span><span className="text-[var(--text-primary)]">{org.domain || "-"}</span></div>
            <div><span className="text-[var(--text-secondary)]">Plan: </span><span className="text-[var(--text-primary)]">{org.plan || "Free"}</span></div>
            <div><span className="text-[var(--text-secondary)]">Created: </span><span className="text-[var(--text-primary)]">{org.created_at ? new Date(org.created_at).toLocaleDateString() : "-"}</span></div>
          </div>
        </div>
      )}

      {members && (
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <Users className="w-5 h-5 text-[var(--accent)]" />
            <h3 className="text-lg font-medium text-[var(--text-primary)]">Team ({members.length})</h3>
          </div>
          <div className="space-y-2">
            {members.map((m: any) => (
              <div key={m.id} className="flex items-center justify-between p-3 bg-[var(--bg-base)] rounded">
                <div>
                  <span className="text-[var(--text-primary)] text-sm">{m.email}</span>
                  <span className="ml-2 text-xs text-[var(--text-secondary)]">{m.name || ""}</span>
                </div>
                <span className="text-xs px-2 py-0.5 rounded bg-[var(--accent)]/10 text-[var(--accent)]">{m.role}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="grid grid-cols-3 gap-4">
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-4">
          <Globe className="w-5 h-5 text-[var(--accent)] mb-2" />
          <p className="text-2xl font-bold text-[var(--text-primary)]">{org?.domain_count || 0}</p>
          <p className="text-xs text-[var(--text-secondary)]">Domains</p>
        </div>
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-4">
          <Mail className="w-5 h-5 text-[var(--accent)] mb-2" />
          <p className="text-2xl font-bold text-[var(--text-primary)]">{org?.mailbox_count || 0}</p>
          <p className="text-xs text-[var(--text-secondary)]">Mailboxes</p>
        </div>
        <div className="bg-[var(--bg-elevated)] border border-[var(--bg-subtle)] rounded-lg p-4">
          <Users className="w-5 h-5 text-[var(--accent)] mb-2" />
          <p className="text-2xl font-bold text-[var(--text-primary)]">{members?.length || 0}</p>
          <p className="text-xs text-[var(--text-secondary)]">Members</p>
        </div>
      </div>
    </div>
  );
}
