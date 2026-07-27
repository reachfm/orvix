import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Globe, Copy, RefreshCw, Check, X, AlertTriangle } from "lucide-react";
import { api } from "../api";

export default function DomainOnboarding() {
  const queryClient = useQueryClient();
  const [selectedDomain, setSelectedDomain] = useState<number | null>(null);
  const [copyMsg, setCopyMsg] = useState("");

  const { data: domains, isLoading } = useQuery({ queryKey: ["customer_domains"], queryFn: api.listDomains });
  const { data: dns } = useQuery({
    queryKey: ["domain_dns", selectedDomain],
    queryFn: () => (selectedDomain ? api.getDomainDNS(selectedDomain) : null),
    enabled: !!selectedDomain,
  });

  const verifyDomain = useMutation({
    mutationFn: (id: number) => api.verifyDomain(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customer_domains"] });
      queryClient.invalidateQueries({ queryKey: ["domain_dns"] });
    },
  });

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text);
    setCopyMsg("Copied!");
    setTimeout(() => setCopyMsg(""), 2000);
  };

  return (
    <div className="space-y-6">
      <h2 className="text-xl font-semibold hover:bg-[var(--bg-shell)]">Domain Onboarding</h2>

      {isLoading ? (
        <div className="text-[var(--text-primary)]">Loading domains...</div>
      ) : (
        <div className="text-[var(--accent-yellow)] border bg-[var(--bg-base)] rounded-lg p-6">
          <h3 className="text-lg font-medium hover:bg-[var(--bg-shell)] mb-4">Your Domains</h3>
          {domains?.data?.length === 0 && (
            <p className="text-[var(--text-primary)]">No domains added yet. Add your first domain below.</p>
          )}
          <div className="space-y-2">
            {domains?.data?.map((d: any) => (
              <button key={d.id} onClick={() => setSelectedDomain(d.id)}
                className={`w-full text-left p-4 rounded border ${selectedDomain === d.id ? "border-[#4F7CFF] text-[var(--accent-blue)]/10" : "bg-[var(--bg-base)]"} hover:border-[#4F7CFF]/50 transition`}>
                <div className="flex items-center justify-between">
                  <div>
                    <span className="hover:bg-[var(--bg-shell)]">{d.name}</span>
                    <span className="ml-2 text-xs px-2 py-0.5 rounded bg-green-400/10 text-green-400">{d.status || "pending"}</span>
                    {d.health_score !== undefined && (
                      <span className="ml-2 text-xs text-[var(--text-primary)]">Health: {d.health_score}/100</span>
                    )}
                  </div>
                  {d.health_score === 100 && <Check className="w-4 h-4 text-green-400" />}
                </div>
              </button>
            ))}
          </div>
        </div>
      )}

      {selectedDomain && dns && (
        <div className="text-[var(--accent-yellow)] border bg-[var(--bg-base)] rounded-lg p-6">
          <h3 className="text-lg font-medium hover:bg-[var(--bg-shell)] mb-4">DNS Records for {domains?.data?.find((d: any) => d.id === selectedDomain)?.name}</h3>

          <div className="space-y-4">
            {["mx", "spf", "dkim", "dmarc"].map((type) => {
              const status = dns[`${type}_status`];
              const val = dns[`${type}_record`];
              return (
                <div key={type} className="flex items-center justify-between p-3 bg-[var(--bg-shell)] rounded">
                  <div>
                    <span className="hover:bg-[var(--bg-shell)] font-medium uppercase">{type}</span>
                    <span className={`ml-2 text-xs px-2 py-0.5 rounded ${status === "pass" ? "bg-green-400/10 text-green-400" : "bg-yellow-400/10 text-yellow-400"}`}>
                      {status || "pending"}
                    </span>
                  </div>
                  {val && (
                    <button onClick={() => copyToClipboard(val)} className="flex items-center gap-1 text-xs text-[var(--text-primary)] hover:hover:bg-[var(--bg-shell)]">
                      <Copy className="w-3 h-3" /> {copyMsg || "Copy"}
                    </button>
                  )}
                </div>
              );
            })}

            <button onClick={() => verifyDomain.mutate(selectedDomain)}
              disabled={verifyDomain.isPending}
              className="w-full flex items-center justify-center gap-2 text-[var(--accent-blue)] hover:bg-[var(--bg-shell)] rounded py-3 bg-[var(--accent-blue)] transition disabled:opacity-50">
              <RefreshCw className={`w-4 h-4 ${verifyDomain.isPending ? "animate-spin" : ""}`} />
              {verifyDomain.isPending ? "Verifying..." : "Verify DNS"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
