import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Search } from "lucide-react";
import { api } from "../api";

export default function AuditLog() {
  const { data: logs, isLoading, error } = useQuery({ queryKey: ["auditLogs"], queryFn: api.listAuditLogs });
  const [filter, setFilter] = useState("");

  if (isLoading) return <p className="text-[#8B92A8]">Loading...</p>;
  if (error) return <p className="text-[#F87171]">Failed to load audit logs</p>;

  const items: any[] = Array.isArray(logs) ? logs : [];
  const filtered = filter
    ? items.filter((l: any) => l.action?.toLowerCase().includes(filter.toLowerCase()))
    : items;

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Audit Log</h2>

      <div className="mb-4">
        <div className="relative max-w-xs">
          <Search size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-[#555D73]" />
          <input
            placeholder="Filter by action..."
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-full pl-9 pr-3 py-2 bg-[#13161C] border border-[#2A2F3E] rounded-lg text-sm text-[#E8EAF0] placeholder-[#555D73] focus:outline-none focus:border-[#4F7CFF]"
          />
        </div>
      </div>

      {filtered.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center">
          <p className="text-[#8B92A8]">No audit entries found.</p>
        </div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-4 text-[#8B92A8] font-medium">Action</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Actor</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Target</th>
                <th className="text-center p-4 text-[#8B92A8] font-medium">Result</th>
                <th className="text-right p-4 text-[#8B92A8] font-medium">Time</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((l: any) => (
                <tr key={l.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-4 text-[#E8EAF0] font-mono text-xs">{l.action}</td>
                  <td className="p-4 text-[#8B92A8]">{l.actor || "-"}</td>
                  <td className="p-4 text-[#8B92A8]">{l.target || "-"}</td>
                  <td className="p-4 text-center">
                    <span className={`px-2 py-1 text-xs rounded-full ${
                      l.result === "success" || l.result === "ok"
                        ? "bg-[#34D399]/10 text-[#34D399]"
                        : "bg-[#F87171]/10 text-[#F87171]"
                    }`}>
                      {l.result || "unknown"}
                    </span>
                  </td>
                  <td className="p-4 text-right text-[#555D73] text-xs">
                    {l.timestamp ? new Date(l.timestamp).toLocaleString() : "-"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
