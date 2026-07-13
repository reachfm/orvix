import { useState, useEffect } from "react";

interface Organization {
  id: number;
  name: string;
  slug: string;
  domain: string;
  plan: string;
  active: boolean;
  mailbox_count: number;
  domain_count: number;
  created_at: string;
}

export default function OrganizationList() {
  const [orgs, setOrgs] = useState<Organization[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    setLoading(true);
    fetch("/api/v1/platform/organizations?limit=100")
      .then((r) => r.json())
      .then((data) => {
        setOrgs(data.organizations || []);
        setLoading(false);
      })
      .catch(() => setLoading(false));
  }, []);

  if (loading) return <div className="text-[#8B92A8]">Loading organizations...</div>;

  return (
    <div>
      <div className="flex justify-between items-center mb-4">
        <h2 className="text-xl font-semibold text-[#E8EAF0]">Organizations</h2>
        <button className="px-4 py-2 bg-[#4F7CFF] text-white rounded-lg text-sm hover:bg-[#3B5FD9]">
          Create Organization
        </button>
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[#2A2F3E] text-[#8B92A8] text-left">
              <th className="py-2 px-3">Name</th>
              <th className="py-2 px-3">Domain</th>
              <th className="py-2 px-3">Plan</th>
              <th className="py-2 px-3">Status</th>
              <th className="py-2 px-3">Mailboxes</th>
              <th className="py-2 px-3">Domains</th>
              <th className="py-2 px-3">Created</th>
            </tr>
          </thead>
          <tbody>
            {orgs.map((o) => (
              <tr key={o.id} className="border-b border-[#1A1E26] hover:bg-[#1A1E26]">
                <td className="py-2 px-3 text-[#E8EAF0]">{o.name}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{o.domain}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{o.plan}</td>
                <td className="py-2 px-3">
                  <span className={`px-2 py-0.5 rounded text-xs ${
                    o.active ? "bg-green-900 text-green-300" : "bg-red-900 text-red-300"
                  }`}>{o.active ? "active" : "disabled"}</span>
                </td>
                <td className="py-2 px-3 text-[#8B92A8]">{o.mailbox_count}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{o.domain_count}</td>
                <td className="py-2 px-3 text-[#8B92A8]">{new Date(o.created_at).toLocaleDateString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
