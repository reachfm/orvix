import { useState, useEffect } from "react";
import { useMutation } from "@tanstack/react-query";
import { api } from "../api";

interface AdminMailbox {
  id: number;
  email: string;
  name: string;
  status: string;
  quota_mb: number;
  used_bytes: number;
  domain_id: number;
  tenant_id: number;
  created_at: string;
}

export default function MailboxList() {
  const [mailboxes, setMailboxes] = useState<AdminMailbox[]>([]);
  const [loading, setLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [newEmail, setNewEmail] = useState("");
  const [newPassword, setNewPassword] = useState("");

  const loadMailboxes = () => {
    setLoading(true);
    fetch("/api/v1/enterprise/mailboxes?limit=100")
      .then((r) => r.json())
      .then((data) => {
        setMailboxes(data.mailboxes || []);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  };

  useEffect(loadMailboxes, []);

  const createMailbox = useMutation({
    mutationFn: () => api.createMailbox({ email: newEmail, password: newPassword }),
    onSuccess: () => {
      setShowCreate(false);
      setNewEmail("");
      setNewPassword("");
      loadMailboxes();
    },
  });

  if (loading) return <div className="text-[#8B92A8]">Loading mailboxes...</div>;

  return (
    <div>
      <div className="flex justify-between items-center mb-4">
        <h2 className="text-xl font-semibold text-[#E8EAF0]">Mailboxes</h2>
        <button
          onClick={() => setShowCreate((v) => !v)}
          className="px-4 py-2 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9]"
        >
          Create Mailbox
        </button>
      </div>
      {showCreate && (
        <form
          onSubmit={(e) => { e.preventDefault(); createMailbox.mutate(); }}
          className="flex gap-2 mb-4 bg-[#13161C] border border-[#2A2F3E] rounded-lg p-3"
        >
          <input
            type="email" required placeholder="Email" value={newEmail}
            onChange={(e) => setNewEmail(e.target.value)}
            className="flex-1 px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-[#E8EAF0] text-sm"
          />
          <input
            type="password" required placeholder="Password" value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            className="flex-1 px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded text-[#E8EAF0] text-sm"
          />
          <button type="submit" disabled={createMailbox.isPending}
            className="px-4 py-2 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9] disabled:opacity-50">
            {createMailbox.isPending ? "Creating..." : "Create"}
          </button>
        </form>
      )}
      {createMailbox.isError && (
        <p className="text-[#F87171] text-sm mb-4">Failed to create mailbox: {(createMailbox.error as Error).message}</p>
      )}
      <input
        type="text"
        placeholder="Search mailboxes..."
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-sm mb-4"
      />
      {mailboxes.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center text-[#8B92A8]">
          No mailboxes found.
        </div>
      ) : (
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[#2A2F3E] text-[#8B92A8] text-left">
              <th className="py-2 px-3">Email</th>
              <th className="py-2 px-3">Name</th>
              <th className="py-2 px-3">Status</th>
              <th className="py-2 px-3">Quota</th>
              <th className="py-2 px-3">Created</th>
            </tr>
          </thead>
          <tbody>
            {mailboxes.filter((m) => m.email.includes(search)).map((m) => (
              <tr key={m.id} className="border-b border-[#1A1E26] hover:bg-[#1A1E26]">
                <td className="py-2 px-3 text-[#E8EAF0]">{m.email}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{m.name || "-"}</td>
                <td className="py-2 px-3">
                  <span className={`px-2 py-0.5 rounded text-xs ${
                    m.status === "active" ? "bg-green-900 text-green-300" : "bg-yellow-900 text-yellow-300"
                  }`}>{m.status}</span>
                </td>
                <td className="py-2 px-3 text-[#8B92A8]">{m.quota_mb} MB</td>
                <td className="py-2 px-3 text-[#8B92A8]">{new Date(m.created_at).toLocaleDateString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      )}
    </div>
  );
}
