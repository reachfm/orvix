export default function Firewall() {
  const rules = [
    { name: "Block Known Spammers", condition: "IP on blacklist", action: "block", priority: 1, enabled: true },
    { name: "Geo Block", condition: "Country = RU, CN, KP", action: "block", priority: 2, enabled: true },
    { name: "Rate Limit", condition: "> 100/hr from same IP", action: "throttle", priority: 3, enabled: true },
  ];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Mail Firewall</h2>

      <div className="grid grid-cols-3 gap-4 mb-6">
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <p className="text-xs text-[#8B92A8] mb-1">Blocks Today</p>
          <p className="text-2xl font-bold text-[#F87171]">1,892</p>
        </div>
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <p className="text-xs text-[#8B92A8] mb-1">Quarantined</p>
          <p className="text-2xl font-bold text-[#FBBF24]">347</p>
        </div>
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-4">
          <p className="text-xs text-[#8B92A8] mb-1">Active Rules</p>
          <p className="text-2xl font-bold text-[#34D399]">{rules.length}</p>
        </div>
      </div>

      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[#2A2F3E]">
              <th className="text-left p-4 text-[#8B92A8] font-medium">Rule</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Condition</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Action</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Priority</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {rules.map((r) => (
              <tr key={r.name} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                <td className="p-4 text-[#E8EAF0]">{r.name}</td>
                <td className="p-4 text-[#8B92A8]">{r.condition}</td>
                <td className="p-4">
                  <span className={`px-2 py-1 text-xs rounded-full ${
                    r.action === "block" ? "bg-[#F87171]/10 text-[#F87171]" :
                    r.action === "throttle" ? "bg-[#FBBF24]/10 text-[#FBBF24]" :
                    "bg-[#34D399]/10 text-[#34D399]"
                  }`}>{r.action}</span>
                </td>
                <td className="p-4 text-[#8B92A8]">{r.priority}</td>
                <td className="p-4">
                  <span className="px-2 py-1 text-xs rounded-full bg-[#34D399]/10 text-[#34D399]">
                    {r.enabled ? "enabled" : "disabled"}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
