import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Users, Shield, Trash2 } from "lucide-react";
import { api } from "../api";

export default function MembersRolesPage() {
  const queryClient = useQueryClient();
  const { data: members } = useQuery({ queryKey: ["members"], queryFn: api.listMembers });

  const updateRole = useMutation({
    mutationFn: ({ userId, role }: { userId: number; role: string }) => api.updateMemberRole(userId, role),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["members"] }),
  });

  const remove = useMutation({
    mutationFn: (userId: number) => api.removeMember(userId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["members"] }),
  });

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold text-white">Members & Roles</h2>

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
        <div className="flex items-center gap-3 mb-4">
          <Users className="w-5 h-5 text-[#4F7CFF]" />
          <h3 className="text-lg font-medium text-white">Team Members</h3>
        </div>
        {members && members.length > 0 ? (
          <div className="space-y-2">
            {members.map((m: any) => (
              <div key={m.id} className="flex items-center justify-between p-3 bg-[#0C0E12] rounded">
                <div className="flex items-center gap-3">
                  <Shield className="w-4 h-4 text-gray-400" />
                  <div>
                    <span className="text-white text-sm">{m.email}</span>
                    {m.name && <span className="ml-2 text-xs text-gray-400">{m.name}</span>}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <select value={m.role} onChange={(e) => updateRole.mutate({ userId: m.id, role: e.target.value })}
                    className="bg-[#1A1E26] border border-[#2A2F3E] rounded text-white text-xs px-2 py-1">
                    <option value="user">User</option>
                    <option value="admin">Admin</option>
                    <option value="superadmin">Super Admin</option>
                  </select>
                  <button onClick={() => remove.mutate(m.id)} className="text-gray-400 hover:text-red-400">
                    <Trash2 className="w-4 h-4" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <p className="text-gray-400 text-sm">Loading members...</p>
        )}
      </div>
    </div>
  );
}
