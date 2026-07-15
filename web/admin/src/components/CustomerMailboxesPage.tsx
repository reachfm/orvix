import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Mail, Plus, Trash2, Search } from "lucide-react";
import { api } from "../api";

export default function CustomerMailboxesPage() {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [newEmail, setNewEmail] = useState("");
  const [newPassword, setNewPassword] = useState("");

  const { data: mailboxes } = useQuery({ queryKey: ["enterprise_mailboxes"], queryFn: api.listMailboxes });

  const create = useMutation({
    mutationFn: () => api.createMailbox({ email: newEmail, password: newPassword }),
    onSuccess: () => { queryClient.invalidateQueries({ queryKey: ["enterprise_mailboxes"] }); setShowCreate(false); setNewEmail(""); setNewPassword(""); },
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteMailbox(id),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["enterprise_mailboxes"] }),
  });

  const list = mailboxes?.mailboxes || mailboxes || [];

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h2 className="text-xl font-semibold text-white">Mailboxes</h2>
        <button onClick={() => setShowCreate(true)} className="flex items-center gap-1 bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm">
          <Plus className="w-4 h-4" /> Create
        </button>
      </div>

      {showCreate && (
        <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg p-6">
          <h3 className="text-lg font-medium text-white mb-4">New Mailbox</h3>
          <div className="space-y-3">
            <input value={newEmail} onChange={(e) => setNewEmail(e.target.value)} placeholder="user@example.com"
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
            <input type="password" value={newPassword} onChange={(e) => setNewPassword(e.target.value)} placeholder="Password"
              className="w-full px-3 py-2 bg-[#0C0E12] border border-[#2A2F3E] rounded text-white text-sm" />
            <div className="flex gap-2">
              <button onClick={() => create.mutate()} disabled={create.isPending || !newEmail || !newPassword}
                className="bg-[#4F7CFF] text-white rounded px-4 py-2 text-sm disabled:opacity-50">
                {create.isPending ? "Creating..." : "Create"}
              </button>
              <button onClick={() => setShowCreate(false)} className="text-gray-400 px-4 py-2 text-sm">Cancel</button>
            </div>
            {create.error && <p className="text-red-400 text-sm">{create.error.message}</p>}
          </div>
        </div>
      )}

      <div className="bg-[#1A1D24] border border-[#262A33] rounded-lg">
        <div className="p-4 border-b border-[#262A33]">
          <div className="flex items-center gap-2 bg-[#0C0E12] rounded px-3 py-2">
            <Search className="w-4 h-4 text-gray-400" />
            <input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search mailboxes..."
              className="bg-transparent text-white text-sm w-full outline-none" />
          </div>
        </div>
        <div className="divide-y divide-[#262A33]">
          {Array.isArray(list) && list.filter((m: any) => !search || m.email?.includes(search)).map((m: any) => (
            <div key={m.id} className="flex items-center justify-between p-4 hover:bg-[#1A1E26]">
              <div className="flex items-center gap-3">
                <Mail className="w-4 h-4 text-gray-400" />
                <div>
                  <span className="text-white text-sm">{m.email}</span>
                  {m.name && <span className="ml-2 text-xs text-gray-400">{m.name}</span>}
                </div>
                <span className={`text-xs px-2 py-0.5 rounded ${m.status === "active" ? "bg-green-400/10 text-green-400" : "bg-yellow-400/10 text-yellow-400"}`}>
                  {m.status}
                </span>
              </div>
              <button onClick={() => remove.mutate(m.id)} className="text-gray-400 hover:text-red-400">
                <Trash2 className="w-4 h-4" />
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
