import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Users, Plus, Trash2, UserPlus, X } from "lucide-react";
import { api } from "../api";

export default function GroupsPage() {
  const queryClient = useQueryClient();
  const [showForm, setShowForm] = useState(false);
  const [name, setName] = useState("");
  const [expandedGroup, setExpandedGroup] = useState<number | null>(null);
  const [newMember, setNewMember] = useState("");

  const { data: groups } = useQuery({ queryKey: ["groups"], queryFn: api.listGroups });

  const create = useMutation({
    mutationFn: () => api.createGroup({ name }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["groups"] }); setShowForm(false); setName(""); },
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteGroup(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["groups"] }),
  });

  const addMember = useMutation({
    mutationFn: ({ groupId, email }: { groupId: number; email: string }) => api.addGroupMember(groupId, email),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["groups"] }); setNewMember(""); },
  });

  const removeMember = useMutation({
    mutationFn: ({ groupId, memberId }: { groupId: number; memberId: number }) => api.removeGroupMember(groupId, memberId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["groups"] }),
  });

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold text-white">Groups</h2>
        <button onClick={() => setShowForm(true)} className="flex items-center gap-1 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm">
          <Plus className="w-4 h-4" /> Create Group
        </button>
      </div>

      {showForm && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <h3 className="text-lg font-medium text-white mb-4">New Group</h3>
          <div className="flex gap-2">
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Group name"
              className="flex-1 px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
            <button onClick={() => create.mutate()} disabled={create.isPending || !name}
              className="bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm disabled:opacity-50">
              {create.isPending ? "Creating..." : "Create"}
            </button>
            <button onClick={() => setShowForm(false)} className="text-gray-400 px-4 py-2 text-sm">Cancel</button>
          </div>
        </div>
      )}

      <div className="space-y-3">
        {groups && groups.length > 0 ? groups.map((g: any) => (
          <div key={g.id} className="bg-[#1A1D24] border border-[#262A33] rounded-lg">
            <button onClick={() => setExpandedGroup(expandedGroup === g.id ? null : g.id)}
              className="w-full flex items-center justify-between p-4 hover:bg-[#1A1E26]">
              <div className="flex items-center gap-3">
                <Users className="w-4 h-4 text-gray-400" />
                <span className="text-white text-sm">{g.name}</span>
                <span className="text-xs text-gray-400">({g.member_count || 0} members)</span>
              </div>
              <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
                <button onClick={() => remove.mutate(g.id)} className="text-gray-400 hover:text-red-400">
                  <Trash2 className="w-4 h-4" />
                </button>
              </div>
            </button>
            {expandedGroup === g.id && (
              <div className="px-4 pb-4 border-t border-[#262A33] pt-3">
                <div className="flex gap-2 mb-3">
                  <input value={newMember} onChange={(e) => setNewMember(e.target.value)} placeholder="email@example.com"
                    className="flex-1 px-3 py-1.5 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
                  <button onClick={() => addMember.mutate({ groupId: g.id, email: newMember })}
                    disabled={!newMember} className="text-[#4F7CFF] hover:text-[#3D6AE8]">
                    <UserPlus className="w-4 h-4" />
                  </button>
                </div>
                {g.members?.map((m: any) => (
                  <div key={m.id || m.email} className="flex items-center justify-between py-1.5 text-sm">
                    <span className="text-gray-300">{m.email}</span>
                    <button onClick={() => removeMember.mutate({ groupId: g.id, memberId: m.id })} className="text-gray-400 hover:text-red-400">
                      <X className="w-3 h-3" />
                    </button>
                  </div>
                ))}
              </div>
            )}
          </div>
        )) : (
          <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6 text-center text-gray-400 text-sm">
            No groups created yet
          </div>
        )}
      </div>
    </div>
  );
}
