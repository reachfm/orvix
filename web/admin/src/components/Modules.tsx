export default function Modules() {
  const modules = [
    { name: "Mail Firewall", version: "1.0.0", latest: "1.1.0", status: "update available" },
    { name: "Guardian Agent", version: "1.0.0", latest: "1.0.0", status: "up to date" },
    { name: "Smart Compose", version: "1.0.0", latest: "1.1.0", status: "update available" },
    { name: "Auto-Heal", version: "1.0.0", latest: "1.0.0", status: "up to date" },
    { name: "Provision API", version: "1.0.0", latest: "1.0.0", status: "up to date" },
    { name: "Webmail UI", version: "1.0.0", latest: "1.0.0", status: "up to date" },
  ];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Module Versions</h2>

      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[#2A2F3E]">
              <th className="text-left p-4 text-[#8B92A8] font-medium">Module</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Current</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Latest</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
              <th className="text-right p-4 text-[#8B92A8] font-medium">Action</th>
            </tr>
          </thead>
          <tbody>
            {modules.map((m) => (
              <tr key={m.name} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                <td className="p-4 text-[#E8EAF0]">{m.name}</td>
                <td className="p-4 text-[#8B92A8]">v{m.version}</td>
                <td className="p-4 text-[#8B92A8]">v{m.latest}</td>
                <td className="p-4">
                  <span className={`px-2 py-1 text-xs rounded-full ${
                    m.status === "update available"
                      ? "bg-[#FBBF24]/10 text-[#FBBF24]"
                      : "bg-[#34D399]/10 text-[#34D399]"
                  }`}>{m.status}</span>
                </td>
                <td className="p-4 text-right">
                  {m.status === "update available" && (
                    <button className="px-3 py-1.5 bg-[#4F7CFF] text-white rounded-lg text-xs hover:bg-[#6B93FF]">
                      Update
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
