import { useState, useEffect } from "react";

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

  useEffect(() => {
    setLoading(true);
    fetch("/api/v1/enterprise/mailboxes?limit=100")
      .then((r) => r.json())
      .then((data) => {
        setMailboxes(data.mailboxes || []);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  if (loading) return <div className="text-[#8B92A8]">Loading mailboxes...</div>;

  return (
    <div>
      <div className="flex justify-between items-center mb-4">
        <h2 className="text-xl font-semibold text-[#E8EAF0]">Mailboxes</h2>
        <button className="px-4 py-2 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9]">
          Create Mailbox
        </button>
      </div>
      <input
        type="text"
        placeholder="Search mailboxes..."
        value={search}
        onChange={(e) => setSearch(e.target.value)}
        className="w-full px-3 py-2 bg-[#1A1E26] border border-[#2A2F3E] rounded-lg text-[#E8EAF0] text-sm mb-4"
      />
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
    </div>
  );
}
