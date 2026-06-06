export default function AuditLog() {
  const logs = [
    { action: "user.login", user: "admin@orvix.email", ip: "192.168.1.1", time: "2 min ago" },
    { action: "domain.create", user: "admin@orvix.email", ip: "192.168.1.1", time: "15 min ago" },
    { action: "user.create", user: "admin@orvix.email", ip: "192.168.1.1", time: "1 hour ago" },
    { action: "firewall.rule.update", user: "admin@orvix.email", ip: "10.0.0.1", time: "3 hours ago" },
  ];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Audit Log</h2>

      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[#2A2F3E]">
              <th className="text-left p-4 text-[#8B92A8] font-medium">Action</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">User</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">IP</th>
              <th className="text-right p-4 text-[#8B92A8] font-medium">Time</th>
            </tr>
          </thead>
          <tbody>
            {logs.map((l, i) => (
              <tr key={i} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                <td className="p-4 text-[#E8EAF0]">{l.action}</td>
                <td className="p-4 text-[#8B92A8]">{l.user}</td>
                <td className="p-4 text-[#8B92A8] font-mono text-xs">{l.ip}</td>
                <td className="p-4 text-right text-[#555D73]">{l.time}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
