import { useState } from "react";

export default function Domains() {
  const [domains] = useState([
    { name: "example.com", status: "active", mx: "✅", spf: "✅", dkim: "✅", dmarc: "✅" },
    { name: "test.org", status: "active", mx: "✅", spf: "✅", dkim: "⚠️", dmarc: "❌" },
  ]);

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Domain Management</h2>

      <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-[#2A2F3E]">
              <th className="text-left p-4 text-[#8B92A8] font-medium">Domain</th>
              <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
              <th className="text-center p-4 text-[#8B92A8] font-medium">MX</th>
              <th className="text-center p-4 text-[#8B92A8] font-medium">SPF</th>
              <th className="text-center p-4 text-[#8B92A8] font-medium">DKIM</th>
              <th className="text-center p-4 text-[#8B92A8] font-medium">DMARC</th>
              <th className="text-right p-4 text-[#8B92A8] font-medium">Actions</th>
            </tr>
          </thead>
          <tbody>
            {domains.map((d) => (
              <tr key={d.name} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                <td className="p-4 text-[#E8EAF0]">{d.name}</td>
                <td className="p-4">
                  <span className="px-2 py-1 text-xs rounded-full bg-[#34D399]/10 text-[#34D399]">
                    {d.status}
                  </span>
                </td>
                <td className="p-4 text-center">{d.mx}</td>
                <td className="p-4 text-center">{d.spf}</td>
                <td className="p-4 text-center">{d.dkim}</td>
                <td className="p-4 text-center">{d.dmarc}</td>
                <td className="p-4 text-right">
                  <button className="text-[#4F7CFF] hover:underline text-xs">DNS Wizard</button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
