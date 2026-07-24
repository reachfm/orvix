import { useQuery } from "@tanstack/react-query";
import { api } from "../api";

interface ModuleInfo {
  id: string;
  version: string;
  status: string;
}

export default function Modules() {
  const { data, isLoading, error } = useQuery<ModuleInfo[]>({ queryKey: ["modules"], queryFn: api.listModules });

  if (isLoading) return <p className="text-[#8B92A8]">Loading...</p>;
  if (error) return <p className="text-[#F87171]">Failed to load modules: {(error as Error).message}</p>;

  const modules = data || [];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Modules</h2>

      {modules.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center text-[#8B92A8]">
          No modules registered.
        </div>
      ) : (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-[#2A2F3E]">
                <th className="text-left p-4 text-[#8B92A8] font-medium">Module</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Version</th>
                <th className="text-left p-4 text-[#8B92A8] font-medium">Status</th>
              </tr>
            </thead>
            <tbody>
              {modules.map((m) => (
                <tr key={m.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-4 text-[#E8EAF0]">{m.id}</td>
                  <td className="p-4 text-[#8B92A8]">v{m.version}</td>
                  <td className="p-4">
                    <span className="px-2 py-1 text-xs rounded-full bg-[#34D399]/10 text-[#34D399]">{m.status}</span>
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
