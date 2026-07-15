import { useQuery } from "@tanstack/react-query";
import { Building, Globe, Mail, Users } from "lucide-react";
import { api } from "../api";

export default function OrganizationOverviewPage() {
  const { data: org } = useQuery({ queryKey: ["org"], queryFn: api.getCurrentOrganization });
  const { data: members } = useQuery({ queryKey: ["members"], queryFn: api.listMembers });

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-white">Organization</h2>

      {org && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <Building className="w-5 h-5 text-[#4F7CFF]" />
            <h3 className="text-lg font-medium text-white">{org.name}</h3>
          </div>
          <div className="grid grid-cols-2 gap-4 text-sm">
            <div><span className="text-gray-400">Slug: </span><span className="text-white">{org.slug}</span></div>
            <div><span className="text-gray-400">Domain: </span><span className="text-white">{org.domain || "-"}</span></div>
            <div><span className="text-gray-400">Plan: </span><span className="text-white">{org.plan || "Free"}</span></div>
            <div><span className="text-gray-400">Created: </span><span className="text-white">{org.created_at ? new Date(org.created_at).toLocaleDateString() : "-"}</span></div>
          </div>
        </div>
      )}

      {members && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <div className="flex items-center gap-3 mb-4">
            <Users className="w-5 h-5 text-[#4F7CFF]" />
            <h3 className="text-lg font-medium text-white">Team ({members.length})</h3>
          </div>
          <div className="space-y-2">
            {members.map((m: any) => (
              <div key={m.id} className="flex items-center justify-between p-3 bg-[#0C0E12] rounded">
                <div>
                  <span className="text-white text-sm">{m.email}</span>
                  <span className="ml-2 text-xs text-gray-400">{m.name || ""}</span>
                </div>
                <span className="text-xs px-2 py-0.5 rounded bg-[#4F7CFF]/10 text-[#4F7CFF]">{m.role}</span>
              </div>
            ))}
          </div>
        </div>
      )}

      <div className="grid grid-cols-3 gap-4">
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-4">
          <Globe className="w-5 h-5 text-[#4F7CFF] mb-2" />
          <p className="text-2xl font-bold text-white">{org?.domain_count || 0}</p>
          <p className="text-xs text-gray-400">Domains</p>
        </div>
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-4">
          <Mail className="w-5 h-5 text-[#4F7CFF] mb-2" />
          <p className="text-2xl font-bold text-white">{org?.mailbox_count || 0}</p>
          <p className="text-xs text-gray-400">Mailboxes</p>
        </div>
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-4">
          <Users className="w-5 h-5 text-[#4F7CFF] mb-2" />
          <p className="text-2xl font-bold text-white">{members?.length || 0}</p>
          <p className="text-xs text-gray-400">Members</p>
        </div>
      </div>
    </div>
  );
}
