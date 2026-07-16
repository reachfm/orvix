import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { CheckCircle, XCircle, AlertTriangle, Eye } from "lucide-react";
import { api } from "../api";

function StatusIcon({ status }: { status: string }) {
  if (status === "ok" || status === "active" || status === "verified") return <CheckCircle size={16} className="text-[#34D399]" />;
  if (status === "warning" || status === "pending") return <AlertTriangle size={16} className="text-[#FBBF24]" />;
  return <XCircle size={16} className="text-[#F87171]" />;
}

export default function Domains() {
  const { data: domains, isLoading, error } = useQuery({ queryKey: ["domains"], queryFn: api.listDomains });
  const [dnsDomain, setDnsDomain] = useState<number | null>(null);
  const { data: dnsData, isLoading: dnsLoading } = useQuery({
    queryKey: ["dns", dnsDomain],
    queryFn: () => api.getDomainDNS(dnsDomain!),
    enabled: dnsDomain !== null,
  });

  if (isLoading) return <p className="text-[#8B92A8]">Loading...</p>;
  if (error) return <p className="text-[#F87171]">Failed to load domains</p>;

  const items: any[] = Array.isArray(domains) ? domains : (domains as any)?.domains || [];

  return (
    <div>
      <h2 className="text-2xl font-semibold mb-6 text-[#E8EAF0]">Domain Management</h2>

      {items.length === 0 ? (
        <div className="bg-[#13161C] border border-[#2A2F3E] rounded-xl p-8 text-center">
          <p className="text-[#8B92A8]">No domains configured yet.</p>
        </div>
      ) : (
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
              {items.map((d: any) => (
                <tr key={d.id} className="border-b border-[#2A2F3E] hover:bg-[#1A1E26]">
                  <td className="p-4 text-[#E8EAF0] font-medium">{d.name}</td>
                  <td className="p-4">
                    <span className={`inline-flex items-center gap-1 px-2 py-1 text-xs rounded-full ${
                      d.status === "active" ? "bg-[#34D399]/10 text-[#34D399]" : "bg-[#FBBF24]/10 text-[#FBBF24]"
                    }`}>
                      {d.status || "unknown"}
                    </span>
                  </td>
                  <td className="p-4 text-center"><StatusIcon status={d.mx_status || "unknown"} /></td>
                  <td className="p-4 text-center"><StatusIcon status={d.spf_status || "unknown"} /></td>
                  <td className="p-4 text-center"><StatusIcon status={d.dkim_status || "unknown"} /></td>
                  <td className="p-4 text-center"><StatusIcon status={d.dmarc_status || "unknown"} /></td>
                  <td className="p-4 text-right">
                    <button onClick={() => setDnsDomain(d.id === dnsDomain ? null : d.id)}
                      className="inline-flex items-center gap-1 text-[#4F7CFF] hover:underline text-xs">
                      <Eye size={14} /> View DNS
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {dnsDomain !== null && (
        <div className="mt-4 bg-[#13161C] border border-[#2A2F3E] rounded-xl p-6">
          <h3 className="text-sm font-medium text-[#E8EAF0] mb-4">DNS Details</h3>
          {dnsLoading ? <p className="text-[#8B92A8] text-sm">Loading DNS records...</p> :
           dnsData ? (
            <div className="space-y-3 text-sm">
              {dnsData.records && Array.isArray(dnsData.records) ? dnsData.records.map((r: any, i: number) => (
                <div key={i} className="flex items-center justify-between bg-[#0C0E12] rounded p-3">
                  <div>
                    <span className="text-[#E8EAF0] font-mono text-xs">{r.type}</span>
                    <span className="mx-2 text-[#555D73]">|</span>
                    <span className="text-[#8B92A8]">{r.host}</span>
                  </div>
                  <div className="text-[#E8EAF0] font-mono text-xs">{r.value}</div>
                  <StatusIcon status={r.status || "unknown"} />
                </div>
              )) : (
                <p className="text-[#8B92A8] text-sm">DNS information not available</p>
              )}
            </div>
          ) : <p className="text-[#8B92A8] text-sm">No DNS records found</p>}
        </div>
      )}
    </div>
  );
}
